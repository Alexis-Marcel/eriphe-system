package main

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
)

// Toolchains. A task's repository says which toolchain it builds with (Go's
// go.mod, Node's .nvmrc or engines): the node detects it once the
// repository is cloned, downloads the official release for linux/<its
// arch> itself, checks it against its publisher's checksum, and keeps it
// once in <root>/toolchains/<name>, mounted read-only into every task at
// toolchainMount. The task's entrypoint asks for it, through the task
// listener, with the task's own secret:
//
//	POST /v1/task/toolchains   (task secret)
//
// and the answer is the environment to add, one NAME=value per line,
// ERIPHE_TOOLCHAIN_PATH among them (the toolchains' bin directories, put
// first in PATH). The node keeps that environment for the task: its
// contract checks run with it (turnend.go).

const (
	toolchainPath  = "/v1/task/toolchains"
	toolchainMount = "/opt/eriphe-toolchains"

	toolchainArchiveMax  = 512 << 20 // a toolchain's archive, at most
	toolchainUnpackedMax = 2 << 30   // and unpacked
	toolchainEntriesMax  = 200_000
	toolchainFileMax     = 1 << 20  // a file of the repository the node reads to detect them
	toolchainIndexMax    = 64 << 20 // a release index (go.dev's JSON, nodejs.org's)
	toolchainIndexTTL    = time.Hour
	toolchainUnused      = 30 * 24 * time.Hour // a toolchain no task used for this long is removed
	toolchainTimeout     = 15 * time.Minute    // the whole request of a task
)

// toolchainKind is one language's toolchain: how a repository names the
// version it wants, and where its publisher's releases are.
type toolchainKind interface {
	// kind is its short name: go, node.
	kind() string
	// detect reads the files of one directory of the repository and gives
	// the version wanted, and the file that says so; "" when it says
	// nothing.
	detect(read func(name string) ([]byte, bool)) (want, source string)
	// exact is the release's directory name when want names one version
	// exactly (no network needed when it is there already).
	exact(want, arch string) (name string, ok bool)
	// resolve finds the release for want and arch: its name, archive URL,
	// SHA-256 and top directory in the archive.
	resolve(ctx context.Context, s *toolchainStore, want, arch string) (toolchainRelease, error)
	// env is what a task gets of it, mounted at dir in its container: the
	// directories to put first in PATH, and other variables.
	env(dir string) (bin []string, vars map[string]string)
}

type toolchainRelease struct {
	Name   string // directory under <root>/toolchains, e.g. go1.25.7-linux-arm64
	URL    string
	SHA256 string
	Top    string // the archive's top directory, stripped
}

var toolchainKinds = []toolchainKind{goToolchain{}, nodeToolchain{}}

// toolchainStore keeps the toolchains of this node.
type toolchainStore struct {
	dir    string // <root>/toolchains
	arch   string // the tasks' architecture: amd64 or arm64
	client *http.Client
	// Where the releases are; replaced in tests.
	goDL     string // https://go.dev/dl/
	nodeDist string // https://nodejs.org/dist/

	mu      sync.Mutex
	locks   map[string]*sync.Mutex // one download of a toolchain at a time
	inUse   map[string][]string    // the toolchains of each running task
	indexes map[string]cachedIndex
}

type cachedIndex struct {
	at   time.Time
	data []byte
}

func newToolchainStore(root string) *toolchainStore {
	return &toolchainStore{
		dir: filepath.Join(root, "toolchains"), arch: runtime.GOARCH,
		client: &http.Client{Timeout: 30 * time.Minute},
		goDL:   "https://go.dev/dl/", nodeDist: "https://nodejs.org/dist/",
		locks: map[string]*sync.Mutex{}, inUse: map[string][]string{}, indexes: map[string]cachedIndex{},
	}
}

// supported tells whether the node has toolchains for its tasks' arch.
func (s *toolchainStore) supported() bool { return s.arch == "amd64" || s.arch == "arm64" }

// wanted is one toolchain a repository asks for.
type wanted struct {
	kind   toolchainKind
	want   string
	source string // the file that says so, relative to the work tree
}

// detectToolchains reads the repository of a task, from the agent's
// working directory up to the repository's root: for each kind, the
// nearest file that names a version wins. The files are the task's, so
// they are read under root only (os.Root: no link leads out of it),
// regular files only, a bounded size.
func detectToolchains(root *os.Root, dirs []string) []wanted {
	var found []wanted
	for _, k := range toolchainKinds {
		for _, d := range dirs {
			read := func(name string) ([]byte, bool) { return readTaskFile(root, path.Join(d, name)) }
			if want, src := k.detect(read); want != "" {
				found = append(found, wanted{kind: k, want: want, source: path.Join(d, src)})
				break
			}
		}
	}
	return found
}

func readTaskFile(root *os.Root, name string) ([]byte, bool) {
	fi, err := root.Lstat(name)
	if err != nil || !fi.Mode().IsRegular() || fi.Size() > toolchainFileMax {
		return nil, false
	}
	f, err := root.Open(name)
	if err != nil {
		return nil, false
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, toolchainFileMax+1))
	if err != nil || len(b) > toolchainFileMax {
		return nil, false
	}
	return b, true
}

// searchDirs are the directories of the task's work tree (relative to its
// work folder, /work in the container) where the toolchains are looked
// for: the agent's working directory and its parents, up to the
// repository's root. Nothing outside the repository: a lent folder is the
// user's, not the node's to read.
func searchDirs(t *Task) []string {
	wd := workDir(t)
	rel, ok := strings.CutPrefix(wd, "/work/repo")
	if !ok || (rel != "" && !strings.HasPrefix(rel, "/")) {
		return nil
	}
	dirs := []string{}
	for d := path.Join("repo", rel); ; d = path.Dir(d) {
		dirs = append(dirs, d)
		if d == "repo" || d == "." || d == "/" {
			break
		}
	}
	return dirs
}

// ensure has the release of w ready in the store, downloading it the
// first time, and gives its name.
func (s *toolchainStore) ensure(ctx context.Context, w wanted) (string, error) {
	if name, ok := w.kind.exact(w.want, s.arch); ok && s.has(name) {
		s.touch(name)
		return name, nil
	}
	rel, err := w.kind.resolve(ctx, s, w.want, s.arch)
	if err != nil {
		return "", err
	}
	if !validToolchainName(rel.Name) {
		return "", fmt.Errorf("bad toolchain name %q", rel.Name)
	}
	lock := s.lock(rel.Name)
	lock.Lock()
	defer lock.Unlock()
	if s.has(rel.Name) {
		s.touch(rel.Name)
		return rel.Name, nil
	}
	start := time.Now()
	size, err := s.download(ctx, rel)
	if err != nil {
		return "", fmt.Errorf("%s: %w", rel.Name, err)
	}
	log.Printf("toolchain %s ready (%s downloaded from %s in %s, sha256 %s checked)",
		rel.Name, humanBytes(size), rel.URL, time.Since(start).Round(time.Second), rel.SHA256)
	return rel.Name, nil
}

var toolchainNameRe = regexp.MustCompile(`^[a-z]+-?v?[0-9][0-9a-z.]*-linux-(amd64|arm64|x64)$`)

func validToolchainName(s string) bool { return toolchainNameRe.MatchString(s) }

func (s *toolchainStore) lock(name string) *sync.Mutex {
	s.mu.Lock()
	defer s.mu.Unlock()
	l := s.locks[name]
	if l == nil {
		l = &sync.Mutex{}
		s.locks[name] = l
	}
	return l
}

func (s *toolchainStore) has(name string) bool {
	fi, err := os.Lstat(filepath.Join(s.dir, name))
	return err == nil && fi.IsDir()
}

// touch marks a toolchain used now: prune keeps it another 30 days.
func (s *toolchainStore) touch(name string) {
	now := time.Now()
	os.Chtimes(filepath.Join(s.dir, name), now, now)
}

// download fetches a release's archive into a temporary file of the store,
// checks its SHA-256 before anything of it is unpacked, unpacks it and
// renames it into place.
func (s *toolchainStore) download(ctx context.Context, rel toolchainRelease) (int64, error) {
	if len(rel.SHA256) != 64 {
		return 0, errors.New("no checksum for the release")
	}
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return 0, err
	}
	tmp, err := os.MkdirTemp(s.dir, ".partial-")
	if err != nil {
		return 0, err
	}
	defer os.RemoveAll(tmp)
	archive := filepath.Join(tmp, "archive")
	size, err := s.fetchChecked(ctx, rel.URL, rel.SHA256, archive)
	if err != nil {
		return size, err
	}
	dst := filepath.Join(tmp, "tree")
	if err := os.Mkdir(dst, 0o755); err != nil {
		return size, err
	}
	f, err := os.Open(archive)
	if err != nil {
		return size, err
	}
	err = untarToolchain(f, dst, rel.Top)
	f.Close()
	if err != nil {
		return size, fmt.Errorf("unpacking: %w", err)
	}
	if err := os.Rename(dst, filepath.Join(s.dir, rel.Name)); err != nil {
		return size, err
	}
	s.touch(rel.Name)
	return size, nil
}

// fetchChecked downloads url to file, and fails unless its SHA-256 is sum.
func (s *toolchainStore) fetchChecked(ctx context.Context, url, sum, file string) (int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("%s: %s", url, resp.Status)
	}
	out, err := os.OpenFile(file, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return 0, err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(out, h), io.LimitReader(resp.Body, toolchainArchiveMax+1))
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return n, err
	}
	if n > toolchainArchiveMax {
		return n, fmt.Errorf("larger than %s", humanBytes(toolchainArchiveMax))
	}
	if got := hex.EncodeToString(h.Sum(nil)); !strings.EqualFold(got, sum) {
		return n, fmt.Errorf("checksum mismatch: sha256 %s, the publisher says %s", got, sum)
	}
	return n, nil
}

// index fetches a release index, kept an hour.
func (s *toolchainStore) index(ctx context.Context, url string) ([]byte, error) {
	s.mu.Lock()
	c, ok := s.indexes[url]
	s.mu.Unlock()
	if ok && time.Since(c.at) < toolchainIndexTTL {
		return c.data, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", url, resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, toolchainIndexMax+1))
	if err != nil {
		return nil, err
	}
	if len(data) > toolchainIndexMax {
		return nil, fmt.Errorf("%s: too large", url)
	}
	s.mu.Lock()
	s.indexes[url] = cachedIndex{at: time.Now(), data: data}
	s.mu.Unlock()
	return data, nil
}

// untarToolchain unpacks a .tar.gz release into dst, without its top
// directory: directories, regular files and symbolic links that stay in
// the tree, never a setuid bit, nothing out of dst (os.Root), within the
// size and entry limits.
func untarToolchain(r io.Reader, dst, top string) error {
	zr, err := gzip.NewReader(r)
	if err != nil {
		return err
	}
	root, err := os.OpenRoot(dst)
	if err != nil {
		return err
	}
	defer root.Close()
	tr := tar.NewReader(zr)
	var written int64
	for entries := 0; ; entries++ {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if entries > toolchainEntriesMax {
			return errors.New("too many files")
		}
		name := strings.TrimPrefix(h.Name, "./")
		if top != "" {
			rest, ok := strings.CutPrefix(name, top+"/")
			if !ok {
				if strings.TrimSuffix(name, "/") == top {
					continue
				}
				return fmt.Errorf("%q: not under %s/", h.Name, top)
			}
			name = rest
		}
		name = strings.TrimSuffix(name, "/")
		if name == "" || h.Typeflag == tar.TypeXGlobalHeader {
			continue
		}
		if !fs.ValidPath(name) {
			return fmt.Errorf("bad path %q", h.Name)
		}
		switch h.Typeflag {
		case tar.TypeDir:
			if err := root.MkdirAll(name, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if written += h.Size; written > toolchainUnpackedMax {
				return errors.New("too large")
			}
			if err := root.MkdirAll(path.Dir(name), 0o755); err != nil {
				return err
			}
			mode := os.FileMode(0o644)
			if h.Mode&0o111 != 0 {
				mode = 0o755
			}
			f, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
			if err != nil {
				return err
			}
			_, err = io.CopyN(f, tr, h.Size)
			if cerr := f.Close(); err == nil {
				err = cerr
			}
			if err != nil {
				return err
			}
			root.Chmod(name, mode) // whatever the umask
		case tar.TypeSymlink:
			// npm and npx are links into lib/: a link must stay in the tree.
			target := h.Linkname
			if path.IsAbs(target) || !fs.ValidPath(path.Join(path.Dir(name), target)) {
				return fmt.Errorf("%s: a link out of the toolchain (%s)", h.Name, target)
			}
			if err := root.MkdirAll(path.Dir(name), 0o755); err != nil {
				return err
			}
			if err := root.Symlink(target, name); err != nil {
				return err
			}
		default:
			return fmt.Errorf("%s: only files, directories and links (type %q)", h.Name, h.Typeflag)
		}
	}
}

// acquire records the toolchains a running task uses: prune keeps them.
func (s *toolchainStore) acquire(taskID string, names []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.inUse[taskID] = names
}

func (s *toolchainStore) release(taskID string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.inUse, taskID)
}

// prune removes the toolchains no task used for 30 days and none runs
// with, and what an interrupted download left.
func (s *toolchainStore) prune() {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return
	}
	s.mu.Lock()
	used := map[string]bool{}
	for _, names := range s.inUse {
		for _, n := range names {
			used[n] = true
		}
	}
	s.mu.Unlock()
	for _, e := range entries {
		name := e.Name()
		fi, err := e.Info()
		if err != nil || used[name] {
			continue
		}
		if strings.HasPrefix(name, ".partial-") {
			if time.Since(fi.ModTime()) > 6*time.Hour {
				os.RemoveAll(filepath.Join(s.dir, name))
			}
			continue
		}
		if time.Since(fi.ModTime()) > toolchainUnused {
			l := s.lock(name)
			l.Lock()
			os.RemoveAll(filepath.Join(s.dir, name))
			l.Unlock()
			log.Printf("toolchain %s removed: unused for %s", name, toolchainUnused)
		}
	}
}

// prepareToolchains detects the toolchains of a task's repository, has
// them ready, and gives the task's environment for them, NAME=value,
// sorted. The task keeps it for its checks.
func (n *node) prepareToolchains(ctx context.Context, t *Task) ([]string, error) {
	s := n.toolchains
	if s == nil || !s.supported() || t.dir == "" {
		return nil, nil
	}
	dirs := searchDirs(t)
	if len(dirs) == 0 {
		return nil, nil
	}
	root, err := os.OpenRoot(filepath.Join(t.dir, "work"))
	if err != nil {
		return nil, nil
	}
	found := detectToolchains(root, dirs)
	root.Close()
	var bins, names, errs []string
	vars := map[string]string{}
	for _, w := range found {
		name, err := s.ensure(ctx, w)
		if err != nil {
			log.Printf("task %s: toolchain %s %s (%s): %v", t.ID, w.kind.kind(), w.want, w.source, err)
			errs = append(errs, fmt.Sprintf("%s %s (%s): %v", w.kind.kind(), w.want, w.source, err))
			continue
		}
		log.Printf("task %s: toolchain %s for %s %s (%s)", t.ID, name, w.kind.kind(), w.want, w.source)
		names = append(names, name)
		b, v := w.kind.env(path.Join(toolchainMount, name))
		bins = append(bins, b...)
		for k, val := range v {
			vars[k] = val
		}
		if n.opt.cacheVolume != "" {
			// Its caches in the node's /cache (worker/README.md), for an
			// image that does not name them itself.
			for k, val := range toolchainCaches[w.kind.kind()] {
				vars[k] = val
			}
		}
	}
	s.acquire(t.ID, names)
	go s.prune()
	if len(bins) > 0 {
		vars["ERIPHE_TOOLCHAIN_PATH"] = strings.Join(bins, ":")
	}
	env := make([]string, 0, len(vars))
	for k, v := range vars {
		if strings.ContainsAny(v, "\n\r\x00") {
			continue
		}
		env = append(env, k+"="+v)
	}
	sort.Strings(env)
	t.mu.Lock()
	t.toolchainEnv = env
	t.mu.Unlock()
	if len(errs) > 0 {
		return env, errors.New(strings.Join(errs, "; "))
	}
	return env, nil
}

// toolchainCaches are the caches of each kind, under the node's /cache.
var toolchainCaches = map[string]map[string]string{
	"go":   {"GOMODCACHE": "/cache/go/mod", "GOCACHE": "/cache/go/build"},
	"node": {"npm_config_cache": "/cache/npm"},
}

// toolchainHandler answers a task's entrypoint: the environment of the
// toolchains its repository asks for, ready. A toolchain that could not be
// had is left out, and said in X-Eriphe-Toolchain-Error: the agent can
// still install one itself.
func (n *node) toolchainHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t := n.taskBySecret(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		if t == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if finished(t.state()) {
			http.Error(w, "the task is over", http.StatusGone)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), toolchainTimeout)
		defer cancel()
		env, err := n.prepareToolchains(ctx, t)
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		if err != nil {
			w.Header().Set("X-Eriphe-Toolchain-Error", strings.Map(func(r rune) rune {
				if r < 0x20 || r > 0x7e {
					return ' '
				}
				return r
			}, tail(err.Error(), 500)))
		}
		for _, kv := range env {
			fmt.Fprintln(w, kv)
		}
	})
}

// toolchainEnvOf is the environment a task's toolchains gave it.
func toolchainEnvOf(t *Task) []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return slices.Clone(t.toolchainEnv)
}

// toolchainsFor tells whether a task gets the node's toolchains: one with
// a repository, on a node whose architecture has them.
func (n *node) toolchainsFor(t *Task) bool {
	return n.toolchains != nil && n.toolchains.supported() && t.Spec.Repo != ""
}

// toolchainMounts mounts the node's toolchains read-only in a task's
// container: those its repository asks for appear there when they are
// ready, even after the container started.
func (n *node) toolchainMounts(t *Task) ([]string, error) {
	if !n.toolchainsFor(t) {
		return nil, nil
	}
	if err := os.MkdirAll(n.toolchains.dir, 0o755); err != nil {
		return nil, err
	}
	return n.readOnlyMount(n.toolchains.dir, toolchainMount)
}
