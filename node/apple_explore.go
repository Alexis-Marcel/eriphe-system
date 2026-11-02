package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// The explorer, with the container engine. On a Mac, Documents, the Desktop,
// Downloads, iCloud Drive and the clouds' folders are protected by macOS:
// a program reads them only once the user allows it, and the permission is
// tied to the program's signature. eriphe-node is no app of Apple's nor
// signed with a Developer ID: every update would ask again. So the node never
// reads a shared place itself. A short-lived container, Apple's
// container-runtime-linux, does it: the place's root mounted read-only, no
// network, the task's user. macOS asks once for it, the first time a
// protected folder is listed or given to a task, and keeps the answer across
// updates of Eriphe (they do not change Apple's program). A listing costs
// about 0.7 s.
//
// Inside, paths are resolved there: a symlink out of the root points
// nowhere the container can see, and the checks of shares.go (nothing
// hidden or sensitive on the way, nor in a shared folder three levels down,
// not the node's own data) apply to what it reports.

// explorerScript runs in the container: $1 the path relative to the root,
// $2 "tree" to list the folder's entries three levels down too, or "results"
// to make its results subfolder (the folder is then mounted at $F, writable;
// $1 is the results folder's name, $3… its former names, newest first: one
// of them is renamed to $1 unless $1 is taken, then it stays in use).
// It writes NUL-separated records: E code (refused), P path, D dirs…, F
// count, T entries…, U (the tree is not whole); a list ends with an empty
// record. ERIPHE_R and ERIPHE_F
// stand for /r and /f in the tests.
const explorerScript = `set -u
R=${ERIPHE_R:-/r} F=${ERIPHE_F:-/f}
if [ "$2" = results ]; then
  cur=$1 use=$1; shift 2
  for old in "$@"; do
    [ "$old" != "$cur" ] && [ -d "$F/$old" ] && [ ! -L "$F/$old" ] || continue
    if [ -e "$F/$cur" ] || [ -L "$F/$cur" ] || ! mv "$F/$old" "$F/$cur" 2>/dev/null; then use=$old; fi
    break
  done
  if [ -L "$F/$use" ] || { [ -e "$F/$use" ] && [ ! -d "$F/$use" ]; }; then printf 'E\0notdir\0'; exit 0; fi
  mkdir -p "$F/$use" 2>/dev/null
  if [ -d "$F/$use" ] && [ -w "$F/$use" ]; then printf 'P\0%s\0%s\0' "$F" "$use"; else printf 'E\0nowrite\0'; fi
  exit 0
fi
p=$(realpath "$R/$1" 2>/dev/null) || { printf 'E\0not_found\0'; exit 0; }
case $p in "$R" | "$R"/*) ;; *) printf 'E\0outside\0'; exit 0 ;; esac
[ -d "$p" ] || { printf 'E\0not_found\0'; exit 0; }
# A private folder, or one in a private folder (shares.go: privateMarker).
q=$p
while :; do
  [ -e "$q/.eriphe-private" ] || [ -L "$q/.eriphe-private" ] && { printf 'E\0private\0'; exit 0; }
  [ "$q" = "$R" ] && break
  q=${q%/*}
done
# Read, not tested: on the share's root, test -r goes by owners the Mac maps.
ls -a "$p" >/dev/null 2>&1 || { printf 'E\0unreadable\0'; exit 0; }
printf 'P\0%s\0%s\0' "$p" "${p#"$R"}"
printf 'D\0'
find "$p" -mindepth 1 -maxdepth 1 -type d -exec sh -c '[ ! -e "$1/.eriphe-private" ] && [ ! -L "$1/.eriphe-private" ]' sh {} \; -print0 2>/dev/null
printf '\0F\0%s\0' "$(find "$p" -mindepth 1 -maxdepth 1 -type f -print0 2>/dev/null | tr -cd '\000' | wc -c | tr -d ' ')"
if [ "$2" = tree ]; then
  printf 'T\0'
  find "$p" -mindepth 1 -maxdepth 4 -print0 2>/dev/null; s=$?
  printf '\0'
  # A folder find could not read: the tree is not whole.
  [ $s -eq 0 ] || printf 'U\0'
fi
`

// explorerImage is the explorer's image: small, pulled at install.
const explorerImage = "alpine:3.22"

// treeMax is how many entries of a folder's tree the check reads at most.
const treeMax = 200000

// exploration is what the explorer saw of a folder.
type exploration struct {
	rel   string   // resolved, relative to the root ("" for the root)
	dirs  []string // its real directories
	files int      // how many regular files it has
	tree  []string // with "tree": its entries three levels down, relative to it
	// With "tree": a folder in it could not be read, the tree is not whole.
	unreadable bool
}

// explore runs the explorer on a root.
func (n *node) explore(ctx context.Context, root shareRoot, rel, mode string) (*exploration, error) {
	mount, err := appleMount(root.Path, "/r", true)
	if err != nil {
		return nil, shareErr(http.StatusBadRequest, "invalid_path", "%v", err)
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute) // the first time, macOS waits for the user's answer
	defer cancel()
	args := append([]string{"run", "--rm", "--progress", "none", "--network", "none", "--read-only",
		"--user", fmt.Sprintf("%d:%d", taskUID, taskGID), "--cap-drop", "ALL", "--memory", "256m", "--cpus", "1",
		"--label", "eriphe.explorer=1"}, mount...)
	args = append(args, "--entrypoint", "sh", explorerImage, "-c", explorerScript, "sh", rel, mode)
	var out, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, n.opt.docker, args...)
	cmd.Stdout, cmd.Stderr = &out, &stderr
	if err := cmd.Run(); err != nil {
		return nil, shareErr(http.StatusServiceUnavailable, "unavailable", "this Mac could not look into %s: %v %s", filepath.Base(root.Path), err, tail(strings.TrimSpace(stderr.String()), 300))
	}
	return parseExploration(out.Bytes())
}

func parseExploration(raw []byte) (*exploration, error) {
	recs := strings.Split(string(raw), "\x00")
	e := &exploration{}
	full := "" // the folder's path in the container
	next := func(i int) string {
		if i+1 < len(recs) {
			return recs[i+1]
		}
		return ""
	}
	for i := 0; i < len(recs); i++ {
		switch tag := recs[i]; tag {
		case "E":
			return nil, explorerRefusal(next(i))
		case "P":
			full, e.rel = next(i), strings.TrimPrefix(next(i+1), "/")
			i += 2
		case "F":
			e.files, _ = strconv.Atoi(next(i))
			i++
		case "U":
			e.unreadable = true
		case "D", "T":
			for i++; i < len(recs) && recs[i] != ""; i++ {
				if tag == "D" {
					e.dirs = append(e.dirs, path.Base(recs[i]))
				} else {
					e.tree = append(e.tree, strings.TrimPrefix(strings.TrimPrefix(recs[i], full), "/"))
				}
			}
		}
	}
	return e, nil
}

func explorerRefusal(code string) error {
	switch code {
	case "outside":
		return shareErr(http.StatusForbidden, "outside", "this folder leads out of its root")
	case "unreadable":
		return shareErr(http.StatusForbidden, "unreadable", "this folder cannot be read")
	case "private":
		return shareErr(http.StatusForbidden, "sensitive", "this folder is hidden or sensitive")
	case "notdir":
		return errors.New("the results folder is not a folder")
	case "nowrite":
		return errors.New("the results folder cannot be written")
	}
	return shareErr(http.StatusNotFound, "not_found", "no such folder")
}

// appleResolve is resolveFolder with the container engine: the path checked,
// resolved by the explorer, and checked again as resolved.
func (n *node) appleResolve(ctx context.Context, share, rel, mode string) (*resolvedFolder, *exploration, error) {
	root, ok := n.opt.shares[share]
	if !ok {
		return nil, nil, shareErr(http.StatusNotFound, "unknown_share", "this machine shares no root %q", share)
	}
	parts, err := splitRel(rel)
	if err != nil {
		return nil, nil, err
	}
	// Refused before the explorer looks: nothing hidden or private on the way.
	if err := n.checkRel(root, parts); err != nil {
		return nil, nil, err
	}
	e, err := n.explore(ctx, root, strings.Join(parts, "/"), mode)
	if err != nil {
		return nil, nil, err
	}
	var real []string
	if e.rel != "" {
		real = strings.Split(e.rel, "/")
	}
	if err := n.checkRel(root, real); err != nil {
		return nil, nil, err
	}
	p := filepath.Join(append([]string{root.Path}, real...)...)
	return &resolvedFolder{root: root, real: p, src: p, rel: e.rel}, e, nil
}

// checkRel refuses a path of a root that is hidden, sensitive, private or
// eriphe-node's own data.
func (n *node) checkRel(root shareRoot, parts []string) error {
	for _, p := range parts {
		if unlisted(p) {
			return shareErr(http.StatusForbidden, "sensitive", "this folder is hidden or sensitive")
		}
	}
	p := filepath.Join(append([]string{root.Path}, parts...)...)
	if private(root.Path, p) {
		return shareErr(http.StatusForbidden, "sensitive", "this folder is hidden or sensitive")
	}
	// Inside it, never; holding it (the home holds ~/.eriphe, hidden), it can
	// be looked into but not shared (holdsOwn).
	if own := n.ownData(); own != "" && within(own, p) {
		return shareErr(http.StatusForbidden, "eriphe_data", "this folder holds eriphe-node's own data")
	}
	return nil
}

// holdsOwn refuses to share a folder that holds eriphe-node's own data.
func (n *node) holdsOwn(real string) error {
	if own := n.ownData(); own != "" && within(real, own) {
		return shareErr(http.StatusForbidden, "eriphe_data", "this folder holds eriphe-node's own data")
	}
	return nil
}

// checkTree is checkShareable on the explorer's tree of a folder (paths
// relative to it): its entries, files too, so the private marker is seen.
func checkTree(tree []string) error {
	if len(tree) > treeMax { // not checked whole, not shared (checkShareable)
		return shareErr(http.StatusForbidden, "too_large", "this folder has too many entries to be checked: share a folder inside it")
	}
	sort.Strings(tree)
	for _, p := range tree {
		if path.Base(p) == privateMarker {
			return holdsPrivate(path.Dir(p))
		}
		if sensitive(path.Base(p)) {
			return shareErr(http.StatusForbidden, "holds_sensitive", "this folder holds %s, which is never shared: share a folder inside it", p)
		}
	}
	return nil
}

// checkExplored is checkTree on what the explorer saw, which must be whole.
func checkExplored(e *exploration) error {
	if err := checkTree(e.tree); err != nil {
		return err
	}
	if e.unreadable {
		return unreadableSub("")
	}
	return nil
}

// appleShareDirs is handleShareDirs with the container engine.
func (n *node) appleShareDirs(w http.ResponseWriter, r *http.Request) {
	check := r.URL.Query().Get("check") == "1"
	mode := "list"
	if check {
		mode = "tree"
	}
	f, e, err := n.appleResolve(r.Context(), r.PathValue("share"), r.URL.Query().Get("path"), mode)
	if err != nil {
		writeShareError(w, err)
		return
	}
	dirs := []string{}
	for _, d := range e.dirs {
		if !unlisted(d) && !private(f.root.Path, filepath.Join(f.real, d)) {
			dirs = append(dirs, d)
		}
	}
	sort.Slice(dirs, func(i, j int) bool { return strings.ToLower(dirs[i]) < strings.ToLower(dirs[j]) })
	truncated := len(dirs) > maxDirs
	if truncated {
		dirs = dirs[:maxDirs]
	}
	out := map[string]any{"share": r.PathValue("share"), "path": f.rel, "dirs": dirs, "files": e.files,
		"truncated": truncated, "writable": !f.root.ReadOnly}
	if check {
		out["shareable"] = true
		err := n.holdsOwn(f.real)
		if err == nil {
			err = checkExplored(e)
		}
		if err != nil {
			var se *shareError
			errors.As(err, &se)
			out["shareable"], out["code"], out["reason"] = false, se.Code, se.Msg
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// appleResults makes the folder's results subfolder through the explorer,
// the folder mounted writable, a former one renamed (chooseResults): it
// returns the one in use.
func (n *node) appleResults(ctx context.Context, real string, tf TaskFolder) (string, error) {
	mount, err := appleMount(real, "/f", false)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	args := append([]string{"run", "--rm", "--progress", "none", "--network", "none", "--read-only",
		"--user", fmt.Sprintf("%d:%d", taskUID, taskGID), "--cap-drop", "ALL", "--memory", "256m", "--cpus", "1",
		"--label", "eriphe.explorer=1"}, mount...)
	cur := tf.resultsName()
	args = append(args, "--entrypoint", "sh", explorerImage, "-c", explorerScript, "sh", cur, "results")
	for _, old := range tf.Former {
		if validFolderName(old) && !strings.Contains(old, ",") {
			args = append(args, old)
		}
	}
	out, err := exec.CommandContext(ctx, n.opt.docker, args...).Output()
	if err != nil {
		return "", fmt.Errorf("cannot make %s/: %v", cur, err)
	}
	e, err := parseExploration(out)
	if err != nil {
		return "", err
	}
	if e.rel == "" || !validFolderName(e.rel) {
		return cur, nil
	}
	return e.rel, nil
}
