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
	"os"
	"os/exec"
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

// Agents are injected into the task container, not built into its image.
// Each agent is an image of its own holding only its binary under
// /opt/eriphe/bin (worker/agents/): eriphe-node downloads it from its
// registry itself (registry.go), unpacks its /opt/eriphe into
// <root>/agents/<agent>/<digest>, and mounts that directory read-only at
// /opt/eriphe in the task's container, first in PATH. The same works with
// Docker (the NAS's socket proxy only sees one more bind mount of the node's
// root), WSL and Apple's container, whatever image the task runs.
//
// The node's own helpers for tasks (eriphe-cred, …) go in the same bin/: see
// taskHelpers.

// injectDir is where a task sees its agent and the node's helpers.
const injectDir = "/opt/eriphe"

// agentPackages are the agents a runtime needs, by runtime. acp runs the
// command its agent gives, from the task's image.
var agentPackages = map[string]string{
	"claude-code": "claude",
	"codex":       "codex",
	"opencode":    "opencode",
}

// injectedRuntimes are the runtimes this node can run in any image.
var injectedRuntimes = []string{"claude-code", "opencode", "codex", "acp"}

// noAgent is the package of a task whose agent is not injected: only the
// helpers.
const noAgent = "none"

// imageTag is the tag of Eriphe's images that goes with this build: its own
// version for a release, sha-<commit> for a build of main (what CI
// publishes, RELEASING.md), latest otherwise.
func imageTag(v string) string {
	if releaseVersion.MatchString(v) {
		return v
	}
	if _, sha, ok := strings.Cut(v, "-dev+"); ok && shortSHA.MatchString(sha) {
		return "sha-" + sha
	}
	return "latest"
}

var (
	releaseVersion = regexp.MustCompile(`^\d+\.\d+\.\d+(-[0-9A-Za-z.-]+)?$`)
	shortSHA       = regexp.MustCompile(`^[0-9a-f]{7,40}$`)
)

// agentImage is the image of an agent package, from -agent-image
// ("…/eriphe-agent-{agent}:tag").
func (n *node) agentImage(pkg string) string {
	return strings.ReplaceAll(n.opt.agentImage, "{agent}", pkg)
}

// agentStore keeps the unpacked agents of this node.
type agentStore struct {
	dir     string // <root>/agents
	helpers string // -task-bin, the node's helpers by platform
	reg     *registryClient

	mu       sync.Mutex
	inflight map[string]*prepareCall // by package
	inUse    map[string]int          // directory → tasks running from it
	failed   map[string]string       // package → why its last preparation failed
	helperMu sync.Mutex
}

type prepareCall struct {
	done chan struct{}
	dir  string
	err  error
}

func newAgentStore(root, helpers string) *agentStore {
	return &agentStore{dir: filepath.Join(root, "agents"), helpers: helpers, reg: newRegistryClient(),
		inflight: map[string]*prepareCall{}, inUse: map[string]int{}, failed: map[string]string{}}
}

// current is the directory of the package's last prepared version, "" if
// there is none.
func (s *agentStore) current(pkg string) string {
	raw, err := os.ReadFile(filepath.Join(s.dir, pkg, "current"))
	if err != nil {
		return ""
	}
	d := filepath.Join(s.dir, pkg, strings.TrimSpace(string(raw)))
	if !validDigestDir(filepath.Base(d)) {
		return ""
	}
	if fi, err := os.Lstat(d); err != nil || !fi.IsDir() {
		return ""
	}
	return d
}

func validDigestDir(s string) bool {
	if len(s) != 64 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

// ready is the directory a task of the package mounts: the prepared one,
// or else prepared now. A package that was prepared before is used as it
// is, without asking the registry: a task starts at once, also offline;
// prepare (in the background) moves it to a newer version.
func (s *agentStore) ready(ctx context.Context, pkg, image string) (string, error) {
	if pkg == noAgent {
		d := filepath.Join(s.dir, noAgent)
		if err := os.MkdirAll(filepath.Join(d, "bin"), 0o755); err != nil {
			return "", err
		}
		return d, nil
	}
	if d := s.current(pkg); d != "" {
		return d, nil
	}
	return s.prepare(ctx, pkg, image)
}

// prepare fetches the package's image (its digest for this platform first:
// nothing more when it is unpacked already), unpacks it, makes it current
// and removes the older versions no task runs from. One at a time per
// package.
func (s *agentStore) prepare(ctx context.Context, pkg, image string) (string, error) {
	s.mu.Lock()
	if c, ok := s.inflight[pkg]; ok {
		s.mu.Unlock()
		select {
		case <-c.done:
			return c.dir, c.err
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	c := &prepareCall{done: make(chan struct{})}
	s.inflight[pkg] = c
	s.mu.Unlock()

	c.dir, c.err = s.fetch(ctx, pkg, image)
	s.mu.Lock()
	delete(s.inflight, pkg)
	if c.err != nil {
		s.failed[pkg] = c.err.Error()
	} else {
		delete(s.failed, pkg)
	}
	s.mu.Unlock()
	close(c.done)
	return c.dir, c.err
}

func (s *agentStore) fetch(ctx context.Context, pkg, image string) (string, error) {
	if !validName(pkg) || pkg == noAgent {
		return "", fmt.Errorf("bad agent package %q", pkg)
	}
	ref, err := parseImageRef(image)
	if err != nil {
		return "", err
	}
	m, digest, err := s.reg.platformManifest(ctx, ref, "linux", runtime.GOARCH)
	if err != nil {
		if d := s.current(pkg); d != "" {
			log.Printf("agent %s: %v; keeping the version it has", pkg, err)
			return d, nil
		}
		return "", fmt.Errorf("agent %s (%s): %w", pkg, image, err)
	}
	hexDigest := strings.TrimPrefix(digest, "sha256:")
	base := filepath.Join(s.dir, pkg)
	final := filepath.Join(base, hexDigest)
	if fi, err := os.Lstat(final); err != nil || !fi.IsDir() {
		start := time.Now()
		if err := os.MkdirAll(base, 0o755); err != nil {
			return "", err
		}
		tmp, err := os.MkdirTemp(base, ".tmp-")
		if err != nil {
			return "", err
		}
		defer os.RemoveAll(tmp)
		os.Chmod(tmp, 0o755)
		size, err := s.unpack(ctx, ref, m, tmp)
		if err != nil {
			return "", fmt.Errorf("agent %s (%s): %w", pkg, image, err)
		}
		if err := os.Rename(tmp, final); err != nil {
			return "", err
		}
		log.Printf("agent %s: %s ready (%s downloaded in %s)", pkg, image, humanBytes(size), time.Since(start).Round(time.Second))
	}
	if err := writeFileAtomic(filepath.Join(base, "current"), []byte(hexDigest+"\n"), 0o644); err != nil {
		return "", err
	}
	s.prune(pkg, final)
	return final, nil
}

// unpack applies the image's layers in order, keeping what is under
// /opt/eriphe, and checks each layer against its digest. It returns the
// bytes downloaded.
func (s *agentStore) unpack(ctx context.Context, ref imageRef, m imageManifest, dst string) (int64, error) {
	var total int64
	for _, l := range m.Layers {
		var gz bool
		switch {
		case strings.HasSuffix(l.MediaType, "gzip"):
			gz = true
		case strings.HasSuffix(l.MediaType, ".tar") || strings.HasSuffix(l.MediaType, "tar"):
		default:
			return total, fmt.Errorf("layer %s: unsupported media type %s", l.Digest, l.MediaType)
		}
		body, err := s.reg.blob(ctx, ref, l.Digest)
		if err != nil {
			return total, err
		}
		h := sha256.New()
		counted := &countingReader{r: io.TeeReader(body, h)}
		var r io.Reader = counted
		if gz {
			zr, err := gzip.NewReader(counted)
			if err != nil {
				body.Close()
				return total, fmt.Errorf("layer %s: %w", l.Digest, err)
			}
			r = zr
		}
		err = untarInjected(r, dst)
		if err == nil {
			// What the tar reader left (padding), so that the digest covers it all.
			_, err = io.Copy(io.Discard, counted)
		}
		body.Close()
		total += counted.n
		if err != nil {
			return total, fmt.Errorf("layer %s: %w", l.Digest, err)
		}
		if got := "sha256:" + hex.EncodeToString(h.Sum(nil)); got != l.Digest {
			return total, fmt.Errorf("layer %s: its content is %s", l.Digest, got)
		}
	}
	return total, nil
}

type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

// maxInjected is the most a package unpacks.
const maxInjected = 4 << 30

// untarInjected unpacks the entries under opt/eriphe/ of a layer into dst:
// directories and regular files only, without setuid or setgid bits, never
// out of dst. A link, a device or a whiteout fails it: an agent package is
// one binary or a few, and nothing the node reads later may point
// elsewhere.
func untarInjected(r io.Reader, dst string) error {
	tr := tar.NewReader(r)
	var written int64
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		name := strings.TrimPrefix(strings.TrimPrefix(h.Name, "./"), "/")
		rel, ok := strings.CutPrefix(name, "opt/eriphe/")
		if !ok {
			continue // the image's other directories (opt/ itself)
		}
		rel = strings.TrimSuffix(rel, "/")
		if rel == "" {
			continue
		}
		if path.Clean(rel) != rel || strings.HasPrefix(rel, "../") || rel == ".." || path.IsAbs(rel) {
			return fmt.Errorf("bad path %q", h.Name)
		}
		if strings.HasPrefix(path.Base(rel), ".wh.") {
			return fmt.Errorf("%s: whiteouts are not supported", h.Name)
		}
		target := filepath.Join(dst, filepath.FromSlash(rel))
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if written += h.Size; written > maxInjected {
				return errors.New("too large")
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			mode := os.FileMode(0o644)
			if h.Mode&0o111 != 0 {
				mode = 0o755
			}
			f, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
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
			os.Chmod(target, mode)
		default:
			return fmt.Errorf("%s: only files and directories (type %q)", h.Name, h.Typeflag)
		}
	}
}

// prune removes the package's other versions that no running task uses,
// and what an interrupted download left (one package downloads at a time:
// the caller's own is renamed already).
func (s *agentStore) prune(pkg, keep string) {
	entries, err := os.ReadDir(filepath.Join(s.dir, pkg))
	if err != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range entries {
		d := filepath.Join(s.dir, pkg, e.Name())
		if !e.IsDir() || d == keep || s.inUse[d] > 0 || (!validDigestDir(e.Name()) && !strings.HasPrefix(e.Name(), ".tmp-")) {
			continue
		}
		os.RemoveAll(d)
	}
}

func (s *agentStore) acquire(dir string) {
	s.mu.Lock()
	s.inUse[dir]++
	s.mu.Unlock()
}

func (s *agentStore) release(dir string) {
	s.mu.Lock()
	if s.inUse[dir]--; s.inUse[dir] <= 0 {
		delete(s.inUse, dir)
	}
	s.mu.Unlock()
}

// readyRuntimes are the runtimes whose agent is prepared on this node (acp
// needs none): the scheduler prefers a node where a task starts at once.
func (s *agentStore) readyRuntimes() []string {
	out := []string{"acp"}
	for rt, pkg := range agentPackages {
		if s.current(pkg) != "" {
			out = append(out, rt)
		}
	}
	sort.Strings(out)
	return out
}

// taskHelpers puts the node's own programs for tasks into the bin/ of an
// injected directory, next to the agent. The contract (node/README.md,
// Injected programs): every regular file of <-task-bin>/linux-<arch>/ is
// copied as is, executable, into /opt/eriphe/bin of every task; e.g. the
// node's image ships eriphe-cred there. A file is replaced by a rename, so
// a task running the old one keeps it.
func (s *agentStore) taskHelpers(dir string) error {
	if s.helpers == "" {
		return nil
	}
	src := filepath.Join(s.helpers, "linux-"+runtime.GOARCH)
	entries, err := os.ReadDir(src)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	s.helperMu.Lock()
	defer s.helperMu.Unlock()
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		return err
	}
	for _, e := range entries {
		if !e.Type().IsRegular() || !validHelperName(e.Name()) {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(src, e.Name()))
		if err != nil {
			return err
		}
		target := filepath.Join(bin, e.Name())
		if old, err := os.ReadFile(target); err == nil && string(old) == string(raw) {
			continue
		}
		if err := writeFileAtomic(target, raw, 0o755); err != nil {
			return err
		}
	}
	return nil
}

func validHelperName(s string) bool {
	return validName(s) && !strings.HasPrefix(s, "-")
}

func writeFileAtomic(p string, data []byte, mode os.FileMode) error {
	f, err := os.CreateTemp(filepath.Dir(p), ".new-")
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(f.Name(), mode)
	}
	if err == nil {
		err = os.Rename(f.Name(), p)
	}
	if err != nil {
		os.Remove(f.Name())
	}
	return err
}

func humanBytes(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}

// injection is what a task's container gets of its agent: the directory to
// mount, taken until the container is gone.
func (n *node) injection(ctx context.Context, t *Task) (string, error) {
	pkg, ok := agentPackages[t.Spec.Runtime]
	if t.Spec.Runtime == "" {
		pkg, ok = agentPackages["claude-code"]
	}
	if !ok || n.opt.agentImage == "" {
		pkg = noAgent
	}
	dir, err := n.agents.ready(ctx, pkg, n.agentImage(pkg))
	if err != nil {
		return "", err
	}
	if err := n.agents.taskHelpers(dir); err != nil {
		return "", fmt.Errorf("the node's programs for tasks: %w", err)
	}
	return dir, nil
}

// prepareSpec is what eriphe-core says this node should have ready: the
// runtimes of its configured agents and the images they name (assignments,
// prepare). None from an older eriphe-core.
type prepareSpec struct {
	Runtimes []string `json:"runtimes"`
	Images   []string `json:"images"`
}

func (p *prepareSpec) key() string {
	if p == nil {
		return ""
	}
	r, i := slices.Clone(p.Runtimes), slices.Clone(p.Images)
	sort.Strings(r)
	sort.Strings(i)
	return strings.Join(r, ",") + "|" + strings.Join(i, ",")
}

// preparer gets ready, in the background, what the tasks of this node will
// need: the default image and the agents' images pulled, the configured
// agents unpacked. At the first assignments, whenever eriphe-core's list
// changes, and every few hours (a newer image under the same tag).
type preparer struct {
	mu   sync.Mutex
	want *prepareSpec
	key  string
	seen bool
	wake chan struct{}
}

const prepareEvery = 6 * time.Hour

// wanted takes eriphe-core's list from the assignments, and wakes the
// preparer when it is new.
func (n *node) wanted(p *prepareSpec) {
	pr := &n.prep
	pr.mu.Lock()
	k := p.key()
	changed := !pr.seen || k != pr.key
	pr.want, pr.key, pr.seen = p, k, true
	pr.mu.Unlock()
	if changed {
		select {
		case pr.wake <- struct{}{}:
		default:
		}
	}
}

func (n *node) prepareLoop() {
	for {
		select {
		case <-n.prep.wake:
		case <-time.After(prepareEvery):
		}
		n.prep.mu.Lock()
		want, seen := n.prep.want, n.prep.seen
		n.prep.mu.Unlock()
		if seen {
			n.prepareNow(want)
		}
	}
}

// prepareNow pulls the images and unpacks the agents of p. Agents no
// configured agent uses are never downloaded.
func (n *node) prepareNow(p *prepareSpec) {
	images := []string{n.opt.image}
	var runtimes []string
	if p != nil {
		images = append(images, p.Images...)
		runtimes = p.Runtimes
	}
	slices.Sort(images)
	for _, im := range slices.Compact(images) {
		if im == "" || strings.HasPrefix(im, "-") {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		if err := n.pullImage(ctx, im); err != nil {
			log.Printf("prepare: pull %s: %v", im, err)
		}
		cancel()
	}
	for _, rt := range runtimes {
		pkg, ok := agentPackages[rt]
		if !ok {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		if _, err := n.agents.prepare(ctx, pkg, n.agentImage(pkg)); err != nil {
			log.Printf("prepare: %v", err)
		}
		cancel()
	}
}

// pullImage pulls an image with the node's engine.
func (n *node) pullImage(ctx context.Context, image string) error {
	args := []string{"pull", "-q", image}
	if n.apple() {
		args = []string{"image", "pull", "--progress", "none", image}
	}
	out, err := exec.CommandContext(ctx, n.opt.docker, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v: %s", err, tail(strings.TrimSpace(string(out)), 300))
	}
	return nil
}
