package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// Files of a shared folder, for eriphe-core (POST /v1/shares/{id}/files):
// a folder is a tool like any other. eriphe-core lists, searches and reads
// the files of a folder shared here, and writes, moves and deletes them,
// each call held to the folder's rights on its side (its policy, its
// cards); here, every call is held to the folder itself:
//
//   - the folder is the one in the list now, under the path it was shared
//     with, at its level now: a write needs it shared write; a copy kept
//     here (syncfolders.go) is only read;
//   - a path is relative to the folder: no "..", nothing hidden or
//     sensitive on the way (unlisted), no folder made private
//     (privateMarker), no symlink followed; an os.Root keeps every open
//     inside the folder besides;
//   - what is answered never holds a path of this machine: names relative
//     to the folder, and errors of our own words;
//   - before a write, a snapshot of the folder (snapshot.go), at most one
//     an hour for these writes; and the previous version of a file
//     changed or deleted is kept in the node's data (trash/<id>/<time>/),
//     -snapshot-days, never in the folder nor outside the node's data.
//
// With the one-container install, a helper does it with the folder alone
// mounted (helper.go, op files); the Mac's explorer is not asked: such a
// folder is lent to a task.

const (
	filesListMax     = 500     // entries of one listing
	filesSearchMax   = 50      // matches of one search
	filesSearchFiles = 5000    // files looked at by one search
	filesSearchSize  = 1 << 20 // bytes of a file searched at most
	filesTextMax     = 1 << 20 // bytes of a text read or written
	filesImageMax    = 5 << 20 // bytes of an image read
	filesSniff       = 8 << 10 // bytes looked at to tell text
	filesSnapEvery   = time.Hour
	filesSnapTask    = "eriphe-core files"
	trashDir         = "trash"
	filesTmpSuffix   = ".eriphe-tmp"
)

// filesReq is one operation in a folder.
type filesReq struct {
	Op      string  `json:"op"`              // list, search, read, write, update, move, delete
	Path    string  `json:"path,omitempty"`  // relative to the folder; "" for the folder itself
	To      string  `json:"to,omitempty"`    // move: where to
	Query   string  `json:"query,omitempty"` // search
	Content *string `json:"content,omitempty"`
	// Expect: update, the hash of the content it replaces, as read: refused
	// when the file changed since.
	Expect string `json:"expect,omitempty"`
	// Copy: the copy of the folder kept here (its Syncthing id), read only.
	Copy string `json:"copy,omitempty"`
}

func (r filesReq) writes() bool {
	return r.Op == "write" || r.Op == "update" || r.Op == "move" || r.Op == "delete"
}

// fileEntry is a file or a folder in a listing.
type fileEntry struct {
	Path     string    `json:"path"`
	Dir      bool      `json:"dir,omitempty"`
	Size     int64     `json:"size,omitempty"`
	Modified time.Time `json:"modified"`
}

// fileMatch is a search's match: a file by its name, or a line of it.
type fileMatch struct {
	Path string `json:"path"`
	Line int    `json:"line,omitempty"`
	Text string `json:"text,omitempty"`
}

// filesResp is an operation's answer.
type filesResp struct {
	Entries   []fileEntry `json:"entries,omitempty"`
	Matches   []fileMatch `json:"matches,omitempty"`
	Truncated bool        `json:"truncated,omitempty"`
	// read: the file; update: the new one.
	Path     string    `json:"path,omitempty"`
	Size     int64     `json:"size,omitempty"`
	Modified time.Time `json:"modified,omitzero"`
	Hash     string    `json:"hash,omitempty"`
	Text     bool      `json:"text,omitempty"`
	Mime     string    `json:"mime,omitempty"`
	Content  []byte    `json:"content,omitempty"`
	// update: what it replaced.
	Previous []byte `json:"previous,omitempty"`
	// Kept: the previous version kept in the node's data; Snapshot: the
	// snapshot taken before.
	Kept     bool   `json:"kept,omitempty"`
	Snapshot string `json:"snapshot,omitempty"`
}

// handleFiles is POST /v1/shares/{id}/files.
func (n *node) handleFiles(w http.ResponseWriter, r *http.Request) {
	var req filesReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2*filesTextMax+4096)).Decode(&req); err != nil {
		writeShareError(w, shareErr(http.StatusBadRequest, "invalid", "want {\"op\": …, \"path\": …}"))
		return
	}
	resp, err := n.folderFiles(r.Context(), r.PathValue("id"), req)
	if err != nil {
		writeShareError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// folderFiles does one operation in the folder shared as id (or in the
// copy of it kept here).
func (n *node) folderFiles(ctx context.Context, id string, req filesReq) (*filesResp, error) {
	if !slices.Contains([]string{"list", "search", "read", "write", "update", "move", "delete"}, req.Op) {
		return nil, shareErr(http.StatusBadRequest, "invalid", "op %q: list, search, read, write, update, move or delete", req.Op)
	}
	if req.Copy != "" {
		if req.writes() {
			return nil, shareErr(http.StatusForbidden, "read_only", "a copy is only read here: write on the folder's own machine")
		}
		real, err := n.filesCopy(id, req.Copy)
		if err != nil {
			return nil, err
		}
		return filesDo(real, "", req)
	}
	if n.engine().traits().explores {
		return nil, shareErr(http.StatusConflict, "lend_only", "this machine opens its folders for tasks only: lend the folder to a task")
	}
	list, err := loadShared(n.opt.root)
	if err != nil {
		return nil, err
	}
	i := indexShared(list, id)
	if id == "" || i < 0 {
		return nil, shareErr(http.StatusNotFound, "not_shared", "this folder is not shared on this machine")
	}
	sf := list[i]
	if req.writes() && sf.Access != accessWrite {
		auditShare(n.opt.root, map[string]any{"event": "refused", "id": sf.ID, "name": sf.Name, "by": "eriphe-core", "op": req.Op, "reason": "level " + sf.Access})
		return nil, shareErr(http.StatusForbidden, "read_only", "this folder is shared read-only on this machine")
	}
	trash := ""
	if req.writes() {
		trash = filepath.Join(n.opt.root, trashDir, sf.ID, time.Now().UTC().Format(snapshotStamp+".000000000"))
	}
	var resp *filesResp
	if n.helper() {
		resp, err = n.helperFiles(ctx, sf, req, trash)
	} else {
		resp, err = n.directFiles(ctx, sf, req, trash)
	}
	if err != nil {
		if req.writes() {
			auditShare(n.opt.root, map[string]any{"event": "refused", "id": sf.ID, "name": sf.Name, "by": "eriphe-core", "op": req.Op, "file": req.Path, "reason": err.Error()})
		}
		return nil, err
	}
	if req.writes() {
		auditShare(n.opt.root, map[string]any{"event": "file_" + req.Op, "id": sf.ID, "name": sf.Name, "by": "eriphe-core", "file": req.Path, "to": req.To, "snapshot": resp.Snapshot})
		n.pruneTrash()
	}
	return resp, nil
}

// directFiles is an operation in a folder the node sees itself.
func (n *node) directFiles(ctx context.Context, sf sharedFolder, req filesReq, trash string) (*filesResp, error) {
	f, err := n.checkShared(sf.Path)
	if err == nil && filepath.Clean(f.src) != filepath.Clean(sf.Path) {
		err = shareErr(http.StatusForbidden, "moved", "this folder leads elsewhere since it was shared: share it again")
	}
	if err != nil {
		return nil, err
	}
	snap := ""
	if req.writes() {
		f.shared = sf
		if snap, err = n.filesSnapshot(ctx, sf, f.real); err != nil {
			return nil, err
		}
	}
	resp, err := filesDo(f.real, trash, req)
	if resp != nil {
		resp.Snapshot = snap
	}
	return resp, err
}

// helperFiles is an operation in a folder a helper sees: the folder
// mounted alone, read-only unless it writes, and the place its previous
// versions are kept.
func (n *node) helperFiles(ctx context.Context, sf sharedFolder, req filesReq, trash string) (*filesResp, error) {
	snap := ""
	mounts := []helperMount{{sf.Path, !req.writes()}}
	if req.writes() {
		var err error
		if snap, err = n.filesSnapshot(ctx, sf, sf.Path); err != nil {
			return nil, err
		}
		if err := os.MkdirAll(trash, 0o700); err != nil {
			return nil, err
		}
		mounts = append(mounts, helperMount{trash, false})
	}
	resp, err := n.helperCall(helperReq{Op: "files", Folder: sf.Path, Own: n.ownData(), Files: &req, Dst: trash}, mounts...)
	if err != nil {
		return nil, err
	}
	if resp.Files == nil {
		return nil, shareErr(http.StatusInternalServerError, "error", "the folder helper answered nothing")
	}
	resp.Files.Snapshot = snap
	return resp.Files, nil
}

// filesSnapshot takes a snapshot of the folder before a write, unless one
// was taken for these writes within the hour. A caller gone meanwhile (a
// big folder's snapshot outlasted it) has nothing written: it was told
// the call failed.
func (n *node) filesSnapshot(ctx context.Context, sf sharedFolder, real string) (string, error) {
	gone := shareErr(http.StatusRequestTimeout, "gone", "eriphe-core gave up on this call: nothing was changed")
	if ctx.Err() != nil {
		return "", gone
	}
	for _, m := range n.snapshots(sf.ID) {
		if m.Task == filesSnapTask && time.Since(m.Time) < filesSnapEvery {
			return m.Stamp, nil
		}
	}
	m, err := n.snapshot(sf, real, filesSnapTask)
	if err != nil {
		return "", shareErr(http.StatusConflict, "no_snapshot", "no snapshot of the folder could be taken before changing it, so nothing was changed")
	}
	if ctx.Err() != nil {
		return "", gone
	}
	return m.Stamp, nil
}

// filesCopy is the copy of the folder id kept here, by its Syncthing id.
func (n *node) filesCopy(id, copyID string) (string, error) {
	plan := n.loadPlan()
	i := slices.IndexFunc(plan.Folders, func(f planFolder) bool { return f.ID == copyID && f.Role == roleCopy })
	if i < 0 || plan.Folders[i].Share != id {
		return "", shareErr(http.StatusNotFound, "not_shared", "no copy of this folder on this machine")
	}
	p := n.copyPath(plan.Folders[i].ID)
	if fi, err := os.Lstat(p); err != nil || !fi.IsDir() {
		return "", shareErr(http.StatusConflict, "missing", "the copy of this folder is not there yet")
	}
	return p, nil
}

// pruneTrash removes the previous versions kept longer than
// -snapshot-days.
func (n *node) pruneTrash() {
	base := filepath.Join(n.opt.root, trashDir)
	ids, _ := os.ReadDir(base)
	keep := time.Duration(n.snapshotDays()) * 24 * time.Hour
	for _, id := range ids {
		dir := filepath.Join(base, id.Name())
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			if fi, err := e.Info(); err == nil && time.Since(fi.ModTime()) > keep {
				forceRemove(filepath.Join(dir, e.Name()))
			}
		}
	}
}

// filesDo does one operation in the folder at real (resolved, checked);
// trash is where a previous version goes, outside the folder.
func filesDo(real, trash string, req filesReq) (*filesResp, error) {
	root, err := os.OpenRoot(real)
	if err != nil {
		return nil, shareErr(http.StatusServiceUnavailable, "unavailable", "the folder cannot be opened now")
	}
	defer root.Close()
	fd := &folderDo{root: root, real: real, trash: trash}
	var resp *filesResp
	switch req.Op {
	case "list":
		resp, err = fd.list(req.Path)
	case "search":
		resp, err = fd.search(req.Path, req.Query)
	case "read":
		resp, err = fd.read(req.Path)
	case "write":
		resp, err = fd.write(req.Path, req.Content)
	case "update":
		resp, err = fd.update(req.Path, req.Content, req.Expect)
	case "move":
		resp, err = fd.move(req.Path, req.To)
	case "delete":
		resp, err = fd.remove(req.Path)
	default:
		err = shareErr(http.StatusBadRequest, "invalid", "op %q", req.Op)
	}
	return resp, filesError(err)
}

// filesError says an error in our own words: an error of the system
// carries a path of this machine.
func filesError(err error) error {
	var se *shareError
	switch {
	case err == nil:
		return nil
	case errors.As(err, &se):
		return se
	case errors.Is(err, fs.ErrNotExist):
		return shareErr(http.StatusNotFound, "not_found", "no such file")
	case errors.Is(err, fs.ErrExist):
		return shareErr(http.StatusConflict, "exists", "there is already a file there")
	case errors.Is(err, fs.ErrPermission):
		return shareErr(http.StatusForbidden, "denied", "this machine does not let eriphe-node do that there")
	}
	log.Printf("folder files: %v", err)
	return shareErr(http.StatusInternalServerError, "error", "the operation failed on the folder's machine")
}

// folderDo is a folder opened for one operation.
type folderDo struct {
	root  *os.Root
	real  string
	trash string
}

// rel checks a path in the folder: relative, nothing hidden, sensitive or
// private on the way, no symlink, and returns it clean ("." for the folder).
// last tells whether its last part must exist.
func (fd *folderDo) rel(p string, last bool) (string, error) {
	p = strings.TrimPrefix(strings.ReplaceAll(strings.TrimSpace(p), "\\", "/"), "./")
	if p == "." || p == "/" {
		p = ""
	}
	parts, err := splitRel(p)
	if err != nil {
		return "", shareErr(http.StatusBadRequest, "invalid_path", "the path must be relative to the folder, without . or .. parts")
	}
	if len(parts) == 0 {
		return ".", nil
	}
	for _, part := range parts {
		if unlisted(part) || part == privateMarker || strings.HasSuffix(part, filesTmpSuffix) {
			return "", shareErr(http.StatusForbidden, "sensitive", "this file is hidden or sensitive: it is never reached")
		}
	}
	for i := range parts {
		sub := path.Join(parts[:i+1]...)
		fi, err := fd.root.Lstat(sub)
		if errors.Is(err, fs.ErrNotExist) && (i < len(parts)-1 || !last) {
			break // made by a write
		}
		if err != nil {
			return "", err
		}
		if fi.Mode()&fs.ModeSymlink != 0 {
			return "", shareErr(http.StatusForbidden, "symlink", "this path goes through a link, which is never followed")
		}
		if fi.IsDir() && marked(filepath.Join(fd.real, filepath.FromSlash(sub))) {
			return "", shareErr(http.StatusForbidden, "sensitive", "this folder was made private by its owner")
		}
	}
	return path.Join(parts...), nil
}

// skipped tells whether a listing or a search leaves an entry out.
func (fd *folderDo) skipped(rel string, e fs.DirEntry) bool {
	if unlisted(e.Name()) || strings.HasSuffix(e.Name(), filesTmpSuffix) || e.Type()&fs.ModeSymlink != 0 {
		return true
	}
	return e.IsDir() && marked(filepath.Join(fd.real, filepath.FromSlash(rel)))
}

func (fd *folderDo) list(p string) (*filesResp, error) {
	rel, err := fd.rel(p, true)
	if err != nil {
		return nil, err
	}
	dir, err := fd.root.Open(rel)
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	if fi, err := dir.Stat(); err != nil || !fi.IsDir() {
		return nil, shareErr(http.StatusBadRequest, "not_dir", "this is a file: read it")
	}
	entries, err := dir.ReadDir(-1)
	if err != nil {
		return nil, err
	}
	resp := &filesResp{Entries: []fileEntry{}}
	for _, e := range entries {
		sub := path.Join(rel, e.Name())
		if fd.skipped(sub, e) {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			continue
		}
		fe := fileEntry{Path: sub, Dir: e.IsDir(), Modified: fi.ModTime().UTC()}
		if !e.IsDir() {
			fe.Size = fi.Size()
		}
		resp.Entries = append(resp.Entries, fe)
	}
	sort.Slice(resp.Entries, func(i, j int) bool {
		a, b := resp.Entries[i], resp.Entries[j]
		if a.Dir != b.Dir {
			return a.Dir
		}
		return strings.ToLower(a.Path) < strings.ToLower(b.Path)
	})
	if len(resp.Entries) > filesListMax {
		resp.Entries, resp.Truncated = resp.Entries[:filesListMax], true
	}
	return resp, nil
}

func (fd *folderDo) search(p, query string) (*filesResp, error) {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return nil, shareErr(http.StatusBadRequest, "invalid", "search for some words")
	}
	rel, err := fd.rel(p, true)
	if err != nil {
		return nil, err
	}
	resp := &filesResp{Matches: []fileMatch{}}
	looked := 0
	stop := errors.New("enough")
	err = fs.WalkDir(fd.root.FS(), rel, func(sub string, e fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if sub != rel && fd.skipped(sub, e) {
			if e.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if len(resp.Matches) >= filesSearchMax || looked >= filesSearchFiles {
			resp.Truncated = true
			return stop
		}
		if sub == rel {
			return nil
		}
		if strings.Contains(strings.ToLower(e.Name()), query) {
			resp.Matches = append(resp.Matches, fileMatch{Path: sub})
		}
		if e.IsDir() || !e.Type().IsRegular() {
			return nil
		}
		looked++
		fi, err := e.Info()
		if err != nil || fi.Size() > filesSearchSize {
			return nil
		}
		raw, err := fd.root.ReadFile(sub)
		if err != nil || !isText(raw) {
			return nil
		}
		for i, line := range strings.Split(string(raw), "\n") {
			if strings.Contains(strings.ToLower(line), query) {
				resp.Matches = append(resp.Matches, fileMatch{Path: sub, Line: i + 1, Text: clip(strings.TrimSpace(line), 200)})
				if len(resp.Matches) >= filesSearchMax {
					break
				}
			}
		}
		return nil
	})
	if err != nil && !errors.Is(err, stop) {
		return nil, err
	}
	return resp, nil
}

// clip keeps the start of a line, whole runes.
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n] + "…"
}

// isText tells whether a file's start reads as text: UTF-8, no NUL.
func isText(raw []byte) bool {
	head := raw
	if len(head) > filesSniff {
		head = head[:filesSniff]
		for i := 0; i < utf8.UTFMax-1 && !utf8.Valid(head); i++ {
			head = head[:len(head)-1] // a rune cut at the end
		}
	}
	return !bytes.Contains(head, []byte{0}) && utf8.Valid(head)
}

// imageTypes are the images read as images.
var imageTypes = map[string]string{".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".gif": "image/gif", ".webp": "image/webp"}

func hashOf(raw []byte) string {
	h := sha256.Sum256(raw)
	return hex.EncodeToString(h[:])
}

// regular opens a path that must be a file.
func (fd *folderDo) regular(rel string) (fs.FileInfo, error) {
	fi, err := fd.root.Lstat(rel)
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, shareErr(http.StatusBadRequest, "not_file", "this is a folder, not a file")
	}
	return fi, nil
}

func (fd *folderDo) read(p string) (*filesResp, error) {
	rel, err := fd.rel(p, true)
	if err != nil {
		return nil, err
	}
	fi, err := fd.regular(rel)
	if err != nil {
		return nil, err
	}
	resp := &filesResp{Path: rel, Size: fi.Size(), Modified: fi.ModTime().UTC()}
	mime := imageTypes[strings.ToLower(path.Ext(rel))]
	limit := int64(filesTextMax)
	if mime != "" {
		limit = filesImageMax
	}
	if fi.Size() > limit {
		return nil, shareErr(http.StatusRequestEntityTooLarge, "too_large", "this file is too big to be read here (%d bytes, %d at most): lend the folder to a task", fi.Size(), limit)
	}
	f, err := fd.root.Open(rel)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	resp.Hash = hashOf(raw)
	switch {
	case mime != "":
		resp.Mime, resp.Content = mime, raw
	case isText(raw):
		resp.Text, resp.Content = true, raw
	}
	return resp, nil
}

// text checks a content to write.
func text(content *string) ([]byte, error) {
	if content == nil {
		return nil, shareErr(http.StatusBadRequest, "invalid", "give the file's content")
	}
	if len(*content) > filesTextMax || !utf8.ValidString(*content) {
		return nil, shareErr(http.StatusRequestEntityTooLarge, "too_large", "a file written here is UTF-8 text, %d bytes at most", filesTextMax)
	}
	return []byte(*content), nil
}

// parent makes the folders a new path needs, each checked as rel does.
func (fd *folderDo) parent(rel string) error {
	dir := path.Dir(rel)
	if dir == "." {
		return nil
	}
	fi, err := fd.root.Stat(dir)
	if err == nil {
		if !fi.IsDir() {
			return shareErr(http.StatusConflict, "not_dir", "%s is a file, not a folder", dir)
		}
		return nil
	}
	if err := fd.root.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if like, err := fd.root.Stat("."); err == nil {
		for d := dir; d != "."; d = path.Dir(d) {
			keepOwnerIn(fd.root, d, like)
		}
	}
	return nil
}

func (fd *folderDo) write(p string, content *string) (*filesResp, error) {
	raw, err := text(content)
	if err != nil {
		return nil, err
	}
	rel, err := fd.rel(p, false)
	if err != nil {
		return nil, err
	}
	if rel == "." {
		return nil, shareErr(http.StatusBadRequest, "invalid_path", "name the file")
	}
	if err := fd.parent(rel); err != nil {
		return nil, err
	}
	f, err := fd.root.OpenFile(rel, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return nil, err
	}
	_, err = f.Write(raw)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		fd.root.Remove(rel)
		return nil, err
	}
	if like, err := fd.root.Stat(path.Dir(rel)); err == nil {
		keepOwnerIn(fd.root, rel, like)
	}
	return fd.done(rel)
}

// done is what a write answers: the file as it is now.
func (fd *folderDo) done(rel string) (*filesResp, error) {
	fi, err := fd.root.Lstat(rel)
	if err != nil {
		return nil, err
	}
	resp := &filesResp{Path: rel, Modified: fi.ModTime().UTC()}
	if fi.Mode().IsRegular() {
		resp.Size = fi.Size()
		if raw, err := fd.root.ReadFile(rel); err == nil {
			resp.Hash = hashOf(raw)
		}
	}
	return resp, nil
}

// keep copies a file's previous version to the trash, outside the folder.
func (fd *folderDo) keep(rel string) error {
	if fd.trash == "" {
		return shareErr(http.StatusInternalServerError, "error", "nowhere to keep the previous version")
	}
	dst := filepath.Join(fd.trash, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	in, err := fd.root.Open(rel)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	return err
}

func (fd *folderDo) update(p string, content *string, expect string) (*filesResp, error) {
	raw, err := text(content)
	if err != nil {
		return nil, err
	}
	rel, err := fd.rel(p, true)
	if err != nil {
		return nil, err
	}
	if rel == "." {
		return nil, shareErr(http.StatusBadRequest, "invalid_path", "name the file")
	}
	fi, err := fd.regular(rel)
	if err != nil {
		return nil, err
	}
	if fi.Size() > filesTextMax {
		return nil, shareErr(http.StatusRequestEntityTooLarge, "too_large", "this file is too big to be changed here: lend the folder to a task")
	}
	old, err := fd.root.ReadFile(rel)
	if err != nil {
		return nil, err
	}
	if !isText(old) {
		return nil, shareErr(http.StatusBadRequest, "not_text", "this file is not text: it is not changed here")
	}
	if expect != "" && expect != hashOf(old) {
		return nil, shareErr(http.StatusConflict, "changed", "the file changed since it was read: read it again")
	}
	if err := fd.keep(rel); err != nil {
		return nil, err
	}
	tmp := rel + filesTmpSuffix
	if err := fd.root.WriteFile(tmp, raw, fi.Mode().Perm()); err != nil {
		fd.root.Remove(tmp)
		return nil, err
	}
	keepOwnerIn(fd.root, tmp, fi)
	if err := fd.root.Rename(tmp, rel); err != nil {
		fd.root.Remove(tmp)
		return nil, err
	}
	resp, err := fd.done(rel)
	if resp != nil {
		resp.Previous, resp.Kept = old, true
	}
	return resp, err
}

func (fd *folderDo) move(from, to string) (*filesResp, error) {
	src, err := fd.rel(from, true)
	if err != nil {
		return nil, err
	}
	dst, err := fd.rel(to, false)
	if err != nil {
		return nil, err
	}
	if src == "." || dst == "." {
		return nil, shareErr(http.StatusBadRequest, "invalid_path", "the folder itself is not moved")
	}
	if dst == src || strings.HasPrefix(dst, src+"/") {
		return nil, shareErr(http.StatusBadRequest, "invalid_path", "a folder is not moved into itself")
	}
	if _, err := fd.root.Lstat(dst); err == nil {
		return nil, shareErr(http.StatusConflict, "exists", "there is already a file there: nothing is overwritten")
	}
	if err := fd.parent(dst); err != nil {
		return nil, err
	}
	if err := fd.root.Rename(src, dst); err != nil {
		return nil, err
	}
	return fd.done(dst)
}

func (fd *folderDo) remove(p string) (*filesResp, error) {
	rel, err := fd.rel(p, true)
	if err != nil {
		return nil, err
	}
	if rel == "." {
		return nil, shareErr(http.StatusBadRequest, "invalid_path", "the folder itself is not deleted")
	}
	fi, err := fd.root.Lstat(rel)
	if err != nil {
		return nil, err
	}
	if fi.IsDir() {
		// An empty folder only: its files are deleted one by one, each kept.
		if err := fd.root.Remove(rel); err != nil {
			return nil, shareErr(http.StatusConflict, "not_empty", "this folder is not empty: delete its files first")
		}
		return &filesResp{Path: rel}, nil
	}
	if _, err := fd.regular(rel); err != nil {
		return nil, err
	}
	if err := fd.keep(rel); err != nil {
		return nil, err
	}
	if err := fd.root.Remove(rel); err != nil {
		return nil, err
	}
	return &filesResp{Path: rel, Kept: true}, nil
}
