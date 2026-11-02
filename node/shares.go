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

// Shares: the folders of this machine the user gives to Eriphe. The
// machine declares its roots when it is installed (-share name=/path[:ro]):
// the places a folder may be shared from, on this machine only
// (shared.go), never from eriphe-core. A task eriphe-core lends a shared
// folder gets it mounted at /work/folders/<name>: read-only, and with the
// right to drop results, its Eriphe/ subfolder writable. Every path is
// resolved again when it is shared and when it is used: no "..", no
// symlink out of its root, no sensitive place (sensitiveNames), nothing of
// eriphe-node's own data.

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

// privateMarker, an empty file the user puts in a folder of their own, makes
// it private: never listed, never shared, nor any folder holding it down to
// scanDepth levels, like a sensitive one. Hidden, so the user's sync tools
// and apps leave it alone.
const privateMarker = ".eriphe-private"

// marked tells whether dir holds the private marker.
func marked(dir string) bool {
	_, err := os.Lstat(filepath.Join(dir, privateMarker))
	return err == nil
}

// unlisted are the directories the explorer does not show: the sensitive
// ones and every hidden one.
func unlisted(name string) bool { return strings.HasPrefix(name, ".") || sensitive(name) }

const (
	scanDepth = 3     // levels of subfolders searched for a sensitive name, besides the folder itself
	scanDirs  = 20000 // directories searched at most; beyond, the rest is not
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
	for d := rootReal; ; {
		if marked(d) {
			return nil, shareErr(http.StatusForbidden, "sensitive", "this folder is hidden or sensitive")
		}
		if d == real {
			break
		}
		rel, _ := filepath.Rel(d, real)
		d = filepath.Join(d, strings.SplitN(rel, string(filepath.Separator), 2)[0])
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

// checkShareable refuses a folder that holds something sensitive or a
// private folder, up to scanDepth levels down: its content would go to a
// task whole.
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
			if e.Name() == privateMarker {
				rel, _ := filepath.Rel(real, d.path)
				return holdsPrivate(filepath.ToSlash(rel))
			}
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

// holdsPrivate refuses a folder holding the private folder rel.
func holdsPrivate(rel string) error {
	return shareErr(http.StatusForbidden, "holds_private", "this folder holds %s, which its owner made private: share a folder inside it", rel)
}

func writeShareError(w http.ResponseWriter, err error) {
	var se *shareError
	if !errors.As(err, &se) {
		se = shareErr(http.StatusInternalServerError, "error", "%v", err)
	}
	writeJSON(w, se.status, se)
}

// TaskFolder is a folder eriphe-core lends a task: one shared on this
// machine, by its id (shared.go), mounted at /work/folders/<name>.
type TaskFolder struct {
	Name  string `json:"name"`
	ID    string `json:"id"`              // in the list of shared folders; nothing else is mounted
	Write bool   `json:"write,omitempty"` // results may be dropped in its results subfolder, if shared so
	// With Write: the results subfolder's name (the assistant's), and its
	// former names, newest first. Empty from an older eriphe-core: Eriphe.
	Results string   `json:"results,omitempty"`
	Former  []string `json:"former,omitempty"`
}

// resultsDir is where a task may write in a folder lent with Write, until
// eriphe-core names it after the assistant.
const resultsDir = "Eriphe"

// resultsName is the folder's results subfolder, as the task sees it.
func (tf TaskFolder) resultsName() string {
	if validFolderName(tf.Results) && !strings.Contains(tf.Results, ",") {
		return tf.Results
	}
	return resultsDir
}

// chooseResults picks the results subfolder of a folder lent with Write,
// the folder at dir: the current name, made by ensureResults if missing.
// A former results folder there is renamed to it, on its first use under
// the new name. When the new name is already taken, nothing is merged: the
// former one stays in use, and kept names it, to be said.
func chooseResults(dir, cur string, former []string) (use, kept string) {
	isDir := func(name string) bool {
		fi, err := os.Lstat(filepath.Join(dir, name))
		return err == nil && fi.IsDir() && fi.Mode()&fs.ModeSymlink == 0
	}
	_, err := os.Lstat(filepath.Join(dir, cur))
	taken := err == nil
	for _, old := range former {
		if old == cur || !validFolderName(old) || !isDir(old) {
			continue
		}
		if !taken && renameResults(filepath.Join(dir, old), filepath.Join(dir, cur)) == nil {
			return cur, ""
		}
		return old, old
	}
	return cur, ""
}

// renameResults renames a results folder (tests replace it).
var renameResults = os.Rename

// resultsConflict says that a folder's results stay under a former name.
func resultsConflict(t *Task, tf TaskFolder, kept string) {
	t.emit("results_conflict", map[string]any{"folder": tf.Name, "kept": kept, "wanted": tf.resultsName(),
		"message": fmt.Sprintf("folder %s: %s/ is already there, the results stay in %s/", tf.Name, tf.resultsName(), kept)})
}

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
// each read-only, its results subfolder writable when lent with Write
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
		f, err := n.lentFolder(t, tf)
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
			return nil, fmt.Errorf("folder %s: its place is read-only on this machine", tf.Name)
		}
		// The task sees it under the current name, whichever is used here.
		cur := tf.resultsName()
		use, kept := chooseResults(f.real, cur, tf.Former)
		if kept != "" {
			resultsConflict(t, tf, kept)
		}
		gid, err := ensureResults(filepath.Join(f.real, use), ownerGID(f.real))
		if err != nil {
			return nil, fmt.Errorf("folder %s: %w", tf.Name, err)
		}
		if gid > 0 {
			groups[gid] = true
		}
		args = append(args, "--mount", mountSpec("type=bind", "src="+filepath.Join(f.src, use), "dst="+dst+"/"+cur))
		results = append(results, filepath.Join(f.real, use))
	}
	// What the task writes there as root goes back to the results folder's owner (handback.go).
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

// ensureResults makes the results subfolder if it is missing: owned by the
// tasks' user, in the folder's group, group-writable and setgid (what is
// written there keeps the group). One already there must be a real
// directory the tasks' user can write to, as its owner, through its group
// (added to the task) or as anyone. It returns its group.
func ensureResults(p string, gid int) (int, error) {
	name := filepath.Base(p)
	fi, err := os.Lstat(p)
	if errors.Is(err, fs.ErrNotExist) {
		if err := os.Mkdir(p, 0o775); err != nil {
			return 0, fmt.Errorf("cannot create %s/: %w", name, err)
		}
		// setgid while the directory is still in the node's group: a
		// directory keeps it through chown, but a chmod after would drop it
		// (the node is not in the folder's group, nor has CAP_FSETID).
		os.Chmod(p, 0o775|fs.ModeSetgid)
		if err := chownResults(p, taskUID, gid); err != nil {
			return 0, fmt.Errorf("cannot give %s/ to the tasks' user: %w", name, err)
		}
		fi, err = os.Lstat(p)
	}
	if err != nil {
		return 0, err
	}
	if !fi.IsDir() || fi.Mode()&fs.ModeSymlink != 0 {
		return 0, fmt.Errorf("%s/ is not a folder", name)
	}
	if !writableByTasks(fi) {
		return 0, fmt.Errorf("%s/ cannot be written by the tasks' user (UID %d)", name, taskUID)
	}
	return ownerGID(p), nil
}
