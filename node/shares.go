package main

import (
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Shares: the folders of this machine the user may give to Eriphe. The
// machine declares its roots when it is installed (-share name=/path[:ro]),
// never eriphe-core, which only ever sees a root's name and a path relative
// to it. Through the tunnel, eriphe-core lists a root's directories (never
// a file's content) for the workspace's explorer, and a task it lends a
// folder gets it mounted at /work/folders/<name>: read-only, and with the
// right to drop results, its Eriphe/ subfolder writable. Every path is
// resolved again when it is used: no "..", no symlink out of its root, no
// sensitive place (sensitiveNames), nothing of eriphe-node's own data.

// shareRoot is one root this machine shares from.
type shareRoot struct {
	Path     string // as eriphe-node sees it, and the Docker daemon too: the same path
	ReadOnly bool   // results may never be dropped in its folders (":ro")
}

// shareFlag reads repeated -share name=/path[:ro] flags.
type shareFlag map[string]shareRoot

func (f shareFlag) String() string { return "" }

func (f shareFlag) Set(s string) error {
	for _, item := range strings.Split(s, ",") {
		if item = strings.TrimSpace(item); item == "" {
			continue
		}
		name, path, ok := strings.Cut(item, "=")
		path, ro := strings.CutSuffix(path, ":ro")
		if !ok || !validName(name) || !filepath.IsAbs(path) {
			return fmt.Errorf("share %q: want name=/absolute/path or name=/absolute/path:ro, the name in lowercase letters, digits, - or _", item)
		}
		if _, dup := f[name]; dup {
			return fmt.Errorf("share %q: %s is given twice", item, name)
		}
		f[name] = shareRoot{Path: filepath.Clean(path), ReadOnly: ro}
	}
	return nil
}

// shareInfo is a root as eriphe-core sees it: its name, never its path.
type shareInfo struct {
	Name     string `json:"name"`
	Writable bool   `json:"writable"`          // results may be dropped in its folders
	Missing  bool   `json:"missing,omitempty"` // its path is not there now (an unplugged disk)
}

func (o options) shareList() []shareInfo {
	out := []shareInfo{}
	for name, s := range o.shares {
		missing := false
		if o.engine != engineContainer { // a Mac's places are never looked at by the node (apple_explore.go)
			fi, err := os.Stat(s.Path)
			missing = err != nil || !fi.IsDir()
		}
		out = append(out, shareInfo{Name: name, Writable: !s.ReadOnly, Missing: missing})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// sensitiveNames are never listed, never part of a shared folder's path,
// and a folder that holds one (in it or in its subfolders down to
// scanDepth levels) cannot be
// shared: keys, credentials, password stores, browser and mail profiles,
// application settings. Compared without case.
var sensitiveNames = map[string]bool{
	// Keys and credentials.
	".ssh": true, ".gnupg": true, ".gpg": true, ".password-store": true, ".pass": true,
	".aws": true, ".azure": true, ".gcloud": true, ".kube": true, ".docker": true, ".terraform.d": true,
	".vault": true, ".secrets": true, "secrets": true, "credentials": true, ".credentials": true,
	".pki": true, ".cert": true, "keychains": true, ".keychain": true,
	// Application settings and state: tokens live there (gh, rclone, keyrings…).
	".config": true, ".local": true, ".mozilla": true, ".thunderbird": true, ".claude": true, ".codex": true,
	// A NAS's containers keep their settings and tokens there (Unraid).
	"appdata": true,
	// Files.
	".netrc": true, ".git-credentials": true, ".pgpass": true, ".vault-token": true, ".npmrc": true,
	".pypirc": true, ".my.cnf": true, ".bash_history": true, ".zsh_history": true,
	"id_rsa": true, "id_ecdsa": true, "id_ed25519": true, "id_dsa": true,
}

func sensitive(name string) bool { return sensitiveNames[strings.ToLower(name)] }

// unlisted are the directories the explorer does not show: the sensitive
// ones and every hidden one.
func unlisted(name string) bool { return strings.HasPrefix(name, ".") || sensitive(name) }

const (
	scanDepth = 3     // levels of subfolders searched for a sensitive name, besides the folder itself
	scanDirs  = 20000 // directories searched at most; beyond, the rest is not
	maxDirs   = 1000  // directories listed at most
)

// shareError is a refusal, with a code eriphe-core translates.
type shareError struct {
	status int
	Code   string `json:"code"`
	Msg    string `json:"error"`
}

func (e *shareError) Error() string { return e.Msg }

func shareErr(status int, code, format string, a ...any) *shareError {
	return &shareError{status: status, Code: code, Msg: fmt.Sprintf(format, a...)}
}

// resolvedFolder is a folder once checked.
type resolvedFolder struct {
	root shareRoot
	real string // its path, without any symlink
	src  string // the same under the root's declared path: what the Docker daemon and the socket proxy are given
	rel  string // relative to the root, without any symlink ("" for the root itself)
}

// resolveFolder checks a path relative to a root: no "..", nothing hidden
// or sensitive on the way, symlinks resolved and kept inside the root, a
// directory, and not inside eriphe-node's own data. A folder that holds
// that data can be listed (the root of a NAS's shares does) but not shared:
// see shareable.
func (n *node) resolveFolder(share, rel string) (*resolvedFolder, error) {
	root, ok := n.opt.shares[share]
	if !ok {
		return nil, shareErr(http.StatusNotFound, "unknown_share", "this machine shares no root %q", share)
	}
	parts, err := splitRel(rel)
	if err != nil {
		return nil, err
	}
	rootReal, err := filepath.EvalSymlinks(root.Path)
	if err != nil {
		return nil, shareErr(http.StatusServiceUnavailable, "unavailable", "the root %s is not there now", share)
	}
	real, err := filepath.EvalSymlinks(filepath.Join(append([]string{rootReal}, parts...)...))
	if err != nil {
		return nil, shareErr(http.StatusNotFound, "not_found", "no such folder")
	}
	if !within(rootReal, real) {
		return nil, shareErr(http.StatusForbidden, "outside", "this folder leads out of the root %s", share)
	}
	relReal, _ := filepath.Rel(rootReal, real)
	if relReal == "." {
		relReal = ""
	}
	relReal = filepath.ToSlash(relReal)
	if relReal != "" {
		for _, p := range strings.Split(relReal, "/") {
			if unlisted(p) {
				return nil, shareErr(http.StatusForbidden, "sensitive", "this folder is hidden or sensitive")
			}
		}
	}
	if private(rootReal, real) {
		return nil, shareErr(http.StatusForbidden, "sensitive", "this folder is hidden or sensitive")
	}
	if fi, err := os.Stat(real); err != nil || !fi.IsDir() {
		return nil, shareErr(http.StatusNotFound, "not_found", "no such folder")
	}
	if own := n.ownData(); own != "" && within(own, real) {
		return nil, shareErr(http.StatusForbidden, "eriphe_data", "this folder is eriphe-node's own data")
	}
	return &resolvedFolder{root: root, real: real, src: filepath.Join(root.Path, filepath.FromSlash(relReal)), rel: relReal}, nil
}

// splitRel splits a relative path, refusing an absolute one, "." and ".."
// parts, and control characters.
func splitRel(rel string) ([]string, error) {
	rel = strings.TrimSuffix(rel, "/")
	if rel == "" {
		return nil, nil
	}
	if strings.HasPrefix(rel, "/") || !utf8.ValidString(rel) {
		return nil, shareErr(http.StatusBadRequest, "invalid_path", "the path must be relative to its root")
	}
	parts := strings.Split(rel, "/")
	for _, p := range parts {
		if p == "" || p == "." || p == ".." || strings.ContainsFunc(p, unicode.IsControl) {
			return nil, shareErr(http.StatusBadRequest, "invalid_path", "the path has an empty, . or .. part")
		}
	}
	return parts, nil
}

// within tells whether p is base or under it.
func within(base, p string) bool {
	rel, err := filepath.Rel(base, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// ownData is the node's root without symlinks: its secret, the tasks'
// folders and profiles are never shared.
func (n *node) ownData() string {
	if p, err := filepath.EvalSymlinks(n.opt.root); err == nil {
		return p
	}
	return n.opt.root
}

// shareable refuses a folder that holds eriphe-node's own data or something
// sensitive: its content would go to a task whole.
func (n *node) shareable(f *resolvedFolder) error {
	if own := n.ownData(); own != "" && within(f.real, own) {
		return shareErr(http.StatusForbidden, "eriphe_data", "this folder holds eriphe-node's own data")
	}
	return checkShareable(f.real)
}

// checkShareable refuses a folder that holds something sensitive, up to
// scanDepth levels down: its content would go to a task whole.
func checkShareable(real string) error {
	type dir struct {
		path  string
		depth int
	}
	queue := []dir{{real, 0}}
	for seen := 0; len(queue) > 0 && seen < scanDirs; seen++ {
		d := queue[0]
		queue = queue[1:]
		entries, err := os.ReadDir(d.path)
		if err != nil {
			continue // unreadable to the node too
		}
		for _, e := range entries {
			if sensitive(e.Name()) {
				rel, _ := filepath.Rel(real, filepath.Join(d.path, e.Name()))
				return shareErr(http.StatusForbidden, "holds_sensitive", "this folder holds %s, which is never shared: share a folder inside it", filepath.ToSlash(rel))
			}
			if e.IsDir() && d.depth < scanDepth {
				queue = append(queue, dir{filepath.Join(d.path, e.Name()), d.depth + 1})
			}
		}
	}
	return nil
}

// The node's API for shares, through the tunnel.
func (n *node) shareRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/shares", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"shares": n.opt.shareList()})
	})
	mux.HandleFunc("GET /v1/shares/{share}/dirs", n.handleShareDirs)
}

// handleShareDirs lists the directories of a folder: names only, and how
// many files it has, never what they hold. With check=1, it also says
// whether the folder can be shared.
func (n *node) handleShareDirs(w http.ResponseWriter, r *http.Request) {
	if n.apple() {
		n.appleShareDirs(w, r) // the node never reads a Mac's places itself
		return
	}
	f, err := n.resolveFolder(r.PathValue("share"), r.URL.Query().Get("path"))
	if err != nil {
		writeShareError(w, err)
		return
	}
	entries, err := os.ReadDir(f.real)
	if err != nil {
		writeShareError(w, shareErr(http.StatusForbidden, "unreadable", "this folder cannot be read"))
		return
	}
	dirs, files := []string{}, 0
	own := n.ownData()
	rootReal, _ := filepath.EvalSymlinks(f.root.Path)
	for _, e := range entries {
		switch {
		case e.IsDir(): // a symlink is not listed: only real directories
			if !unlisted(e.Name()) && !private(rootReal, filepath.Join(f.real, e.Name())) && !within(own, filepath.Join(f.real, e.Name())) {
				dirs = append(dirs, e.Name())
			}
		case e.Type().IsRegular():
			files++
		}
	}
	sort.Slice(dirs, func(i, j int) bool { return strings.ToLower(dirs[i]) < strings.ToLower(dirs[j]) })
	truncated := len(dirs) > maxDirs
	if truncated {
		dirs = dirs[:maxDirs]
	}
	out := map[string]any{"share": r.PathValue("share"), "path": f.rel, "dirs": dirs, "files": files,
		"truncated": truncated, "writable": !f.root.ReadOnly}
	if r.URL.Query().Get("check") == "1" {
		out["shareable"] = true
		if err := n.shareable(f); err != nil {
			var se *shareError
			errors.As(err, &se)
			out["shareable"], out["code"], out["reason"] = false, se.Code, se.Msg
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func writeShareError(w http.ResponseWriter, err error) {
	var se *shareError
	if !errors.As(err, &se) {
		se = shareErr(http.StatusInternalServerError, "error", "%v", err)
	}
	writeJSON(w, se.status, se)
}

// TaskFolder is a folder eriphe-core lends a task, by the name the user
// gave it: mounted at /work/folders/<name>.
type TaskFolder struct {
	Name  string `json:"name"`
	Share string `json:"share"`           // the root
	Path  string `json:"path"`            // relative to it
	Write bool   `json:"write,omitempty"` // results may be dropped in its Eriphe/ subfolder
}

// resultsDir is where a task may write in a folder lent with Write.
const resultsDir = "Eriphe"

// chownResults gives the results folder to the tasks' user (tests replace it).
var chownResults = os.Lchown

// validFolderName is a name usable as one directory of /work/folders.
func validFolderName(s string) bool {
	if s == "" || utf8.RuneCountInString(s) > 64 || strings.HasPrefix(s, ".") || !utf8.ValidString(s) {
		return false
	}
	return !strings.ContainsFunc(s, func(r rune) bool { return r == '/' || r == '\\' || unicode.IsControl(r) })
}

// folderMounts are the docker run arguments that mount a task's folders:
// each read-only, its Eriphe/ subfolder writable when lent with Write
// (created if missing, in the folder's group, so that what the task writes
// stays the user's to open, change and delete), and the folders' groups
// added to the task's user so that it reads what the group may.
func (n *node) folderMounts(t *Task) ([]string, error) {
	if n.apple() {
		return n.appleFolderMounts(t)
	}
	var args, results []string
	groups := map[int]bool{}
	seen := map[string]bool{}
	for _, tf := range t.Spec.Folders {
		if !validFolderName(tf.Name) || seen[strings.ToLower(tf.Name)] {
			return nil, fmt.Errorf("folder %q: invalid or repeated name", tf.Name)
		}
		seen[strings.ToLower(tf.Name)] = true
		f, err := n.resolveFolder(tf.Share, tf.Path)
		if err == nil {
			err = n.shareable(f)
		}
		if err != nil {
			return nil, fmt.Errorf("folder %s: %w", tf.Name, err)
		}
		dst := "/work/folders/" + tf.Name
		args = append(args, "--mount", mountSpec("type=bind", "src="+f.src, "dst="+dst, "readonly"))
		if gid := ownerGID(f.real); gid > 0 {
			groups[gid] = true
		}
		if !tf.Write {
			continue
		}
		if f.root.ReadOnly {
			return nil, fmt.Errorf("folder %s: its root %s is read-only on this machine", tf.Name, tf.Share)
		}
		gid, err := ensureResults(filepath.Join(f.real, resultsDir), ownerGID(f.real))
		if err != nil {
			return nil, fmt.Errorf("folder %s: %w", tf.Name, err)
		}
		if gid > 0 {
			groups[gid] = true
		}
		args = append(args, "--mount", mountSpec("type=bind", "src="+filepath.Join(f.src, resultsDir), "dst="+dst+"/"+resultsDir))
		results = append(results, filepath.Join(f.real, resultsDir))
	}
	// What the task writes there as root goes back to Eriphe/'s owner (handback.go).
	t.mu.Lock()
	t.results = results
	t.mu.Unlock()
	gids := []int{}
	for g := range groups {
		if g != taskGID {
			gids = append(gids, g)
		}
	}
	sort.Ints(gids)
	for _, g := range gids {
		args = append(args, "--group-add", fmt.Sprint(g))
	}
	return args, nil
}

// mountSpec is a --mount value, its fields quoted when they hold a comma
// or a quote (docker reads it as CSV).
func mountSpec(fields ...string) string {
	var b bytes.Buffer
	w := csv.NewWriter(&b)
	w.Write(fields)
	w.Flush()
	return strings.TrimSuffix(b.String(), "\n")
}

// ensureResults makes the Eriphe/ subfolder if it is missing: owned by the
// tasks' user, in the folder's group, group-writable and setgid (what is
// written there keeps the group). One already there must be a real
// directory the tasks' user can write to, as its owner, through its group
// (added to the task) or as anyone. It returns its group.
func ensureResults(p string, gid int) (int, error) {
	fi, err := os.Lstat(p)
	if errors.Is(err, fs.ErrNotExist) {
		if err := os.Mkdir(p, 0o775); err != nil {
			return 0, fmt.Errorf("cannot create %s/: %w", resultsDir, err)
		}
		// setgid while the directory is still in the node's group: a
		// directory keeps it through chown, but a chmod after would drop it
		// (the node is not in the folder's group, nor has CAP_FSETID).
		os.Chmod(p, 0o775|fs.ModeSetgid)
		if err := chownResults(p, taskUID, gid); err != nil {
			return 0, fmt.Errorf("cannot give %s/ to the tasks' user: %w", resultsDir, err)
		}
		fi, err = os.Lstat(p)
	}
	if err != nil {
		return 0, err
	}
	if !fi.IsDir() || fi.Mode()&fs.ModeSymlink != 0 {
		return 0, fmt.Errorf("%s/ is not a folder", resultsDir)
	}
	if !writableByTasks(fi) {
		return 0, fmt.Errorf("%s/ cannot be written by the tasks' user (UID %d)", resultsDir, taskUID)
	}
	return ownerGID(p), nil
}
