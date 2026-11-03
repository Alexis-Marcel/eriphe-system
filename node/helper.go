package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Folder helpers. With the one-container install (eriphe-launcher), the
// node mounts no shared folder at all: a task gets its folders as bind
// mounts from the Docker daemon when its container is made, through the
// socket proxy, which allows only the folders shared (the launcher's list,
// re-read at each request). What the node does inside a shared folder runs
// instead in a short-lived container of its own image, the helper, made
// through the proxy with only that folder (and, for a snapshot, its place
// in the node's data) bind-mounted at its path, no network, a read-only
// root, no capability but a task's (CHOWN, DAC_OVERRIDE, FOWNER) and
// no-new-privileges (dockerproxy.go, checkHelper). It runs the same code
// the node runs on a machine where it sees the folders itself:
//
//   - check: a folder's checks, when it is shared and before every lend
//     (resolveFolder, shareable);
//   - prepare: a folder lent to a task, checked again, its group (for the
//     task's --group-add), and with drop its results subfolder chosen,
//     renamed from a former name, made and handed to the tasks' user
//     (chooseResults, ensureResults);
//   - snapshot: the copy of a folder before a task that manages it, made
//     inside the helper straight into the node's data (the folder read-only,
//     the snapshot's new folder writable): no copy goes through the node;
//   - restore: a folder emptied and its snapshot copied back;
//   - handback: what a task wrote as root given back to the results'
//     owners (handBackDir);
//   - probe: whether each folder opens, and may be written to (access.go).
//
// The helper answers one JSON object on its stdout. -folder-helper (env
// ERIPHE_FOLDER_HELPER) is the node's own image, the one the proxy allows
// for it; empty, the node does all this itself (install.sh, a Mac, WSL).

const helperTimeout = 30 * time.Minute // a snapshot of a big folder

// helperReq is what a helper is asked.
type helperReq struct {
	Op      string   `json:"op"`
	Folder  string   `json:"folder,omitempty"`
	Folders []string `json:"folders,omitempty"` // probe: each, with its level
	Levels  []string `json:"levels,omitempty"`
	Dirs    []string `json:"dirs,omitempty"` // handback
	Own     string   `json:"own,omitempty"`  // the node's root: never shared, nor holding a share
	// prepare, with Write: the results subfolder and its former names.
	Write   bool     `json:"write,omitempty"`
	Results string   `json:"results,omitempty"`
	Former  []string `json:"former,omitempty"`
	// snapshot: where the copy goes (does not exist yet), and at most
	// what it copies; restore: the snapshot's files.
	Dst   string `json:"dst,omitempty"`
	Limit int64  `json:"limit,omitempty"`
}

// helperResp is a helper's answer.
type helperResp struct {
	Refused *shareError  `json:"refused,omitempty"` // a check's refusal, with its code
	Error   string       `json:"error,omitempty"`   // any other failure
	TooBig  bool         `json:"too_big,omitempty"` // snapshot: errSnapshotTooBig
	Real    string       `json:"real,omitempty"`
	GID     int          `json:"gid,omitempty"`
	Use     string       `json:"use,omitempty"`  // prepare: the results subfolder used
	Kept    string       `json:"kept,omitempty"` // and the former name it stays under, if any
	RGID    int          `json:"rgid,omitempty"`
	Method  string       `json:"method,omitempty"`
	Copied  int64        `json:"copied,omitempty"`
	Notes   []accessNote `json:"notes,omitempty"` // probe, in Folders' order
}

func (r *helperResp) err() error {
	switch {
	case r.Refused != nil:
		r.Refused.status = map[string]int{"not_found": 404, "invalid_path": 400, "unavailable": 503}[r.Refused.Code]
		if r.Refused.status == 0 {
			r.Refused.status = 403
		}
		return r.Refused
	case r.TooBig:
		return errSnapshotTooBig
	case r.Error != "":
		return errors.New(r.Error)
	}
	return nil
}

// helper tells whether shared folders are reached through helpers.
func (n *node) helper() bool { return n.opt.folderHelper != "" && !n.apple() }

// helperMount is one bind mount of a helper, at its own path.
type helperMount struct {
	path     string
	readOnly bool
}

// runHelper runs a helper with its mounts; a variable for the tests.
var runHelper = func(n *node, req helperReq, mounts []helperMount) (*helperResp, error) {
	args := []string{"run", "--rm", "-i", "--network", "none", "--read-only", "--user", "0:0",
		"--cap-drop", "ALL", "--cap-add", "CHOWN", "--cap-add", "DAC_OVERRIDE", "--cap-add", "FOWNER",
		"--security-opt", "no-new-privileges", "--pids-limit", "64", "--label", "eriphe.helper=" + req.Op,
		"--entrypoint", "/usr/local/bin/eriphe-node"}
	seen := map[string]bool{}
	for _, m := range mounts {
		if seen[m.path] {
			continue
		}
		seen[m.path] = true
		spec := []string{"type=bind", "src=" + m.path, "dst=" + m.path}
		if m.readOnly {
			spec = append(spec, "readonly")
		}
		args = append(args, "--mount", mountSpec(spec...))
	}
	args = append(args, n.opt.folderHelper, "folder-helper")
	raw, _ := json.Marshal(req)
	ctx, cancel := context.WithTimeout(context.Background(), helperTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, n.opt.docker, args...)
	cmd.Stdin = bytes.NewReader(raw)
	var out, errs bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errs
	err := cmd.Run()
	var resp helperResp
	if jerr := json.Unmarshal(out.Bytes(), &resp); jerr != nil {
		msg := strings.TrimSpace(errs.String())
		if i := strings.LastIndex(msg, "\n"); i >= 0 {
			msg = msg[i+1:]
		}
		if err == nil {
			err = jerr
		}
		if msg != "" {
			err = fmt.Errorf("%v: %s", err, msg)
		}
		return nil, fmt.Errorf("folder helper %s: %w", req.Op, err)
	}
	return &resp, nil
}

// helperCall runs a helper and returns its answer, or what failed.
func (n *node) helperCall(req helperReq, mounts ...helperMount) (*helperResp, error) {
	resp, err := runHelper(n, req, mounts)
	if err != nil {
		// The proxy refuses a folder that is not shared (not in its list)
		// or not there: said as the node's own refusals are.
		msg := err.Error()
		switch {
		case strings.Contains(msg, "outside the allowed places"):
			return nil, shareErr(403, "outside", "this folder is not one of the folders shared on this machine (eriphe share add)")
		case strings.Contains(msg, "cannot be resolved"), strings.Contains(msg, "no such file or directory"):
			return nil, shareErr(404, "not_found", "no such folder")
		}
		return nil, err
	}
	return resp, resp.err()
}

// helperCheck is checkShared through a helper: the folder is its own place.
func (n *node) helperCheck(path string) (*resolvedFolder, error) {
	path = filepath.Clean(path)
	resp, err := n.helperCall(helperReq{Op: "check", Folder: path, Own: n.ownData()}, helperMount{path, true})
	if err != nil {
		return nil, err
	}
	return &resolvedFolder{root: shareRoot{Path: path}, real: resp.Real, src: path, helper: true}, nil
}

// helperPrepare is what folderMounts does in a folder lent to a task.
func (n *node) helperPrepare(f *resolvedFolder, tf TaskFolder) (*helperResp, error) {
	req := helperReq{Op: "prepare", Folder: f.src, Own: n.ownData(), Write: tf.Write && !tf.Manage, Results: tf.resultsName(), Former: tf.Former}
	return n.helperCall(req, helperMount{f.src, !req.Write})
}

// helperFolderMounts is folderMounts for a folder the node does not see:
// a helper checks it again and prepares it, a snapshot is taken by another
// for manage. It returns the task's mounts, its results folders (handed
// back by helpers too) and the groups to add.
func (n *node) helperFolderMounts(t *Task, tf TaskFolder, f *resolvedFolder, dst string) (args, results []string, gids []int, err error) {
	sf := f.shared
	resp, err := n.helperPrepare(f, tf)
	if err == nil && filepath.Clean(resp.Real) != filepath.Clean(sf.Path) {
		err = shareErr(403, "moved", "this folder leads elsewhere since it was shared: share it again")
	}
	if err != nil {
		auditShare(n.opt.root, map[string]any{"event": "refused", "id": sf.ID, "name": sf.Name, "task": t.ID, "reason": err.Error()})
		return nil, nil, nil, err
	}
	auditShare(n.opt.root, map[string]any{"event": "mount", "id": sf.ID, "name": sf.Name, "path": sf.Path, "task": t.ID, "access": tf.level()})
	if resp.GID > 0 {
		gids = append(gids, resp.GID)
	}
	switch {
	case tf.Manage:
		if err := n.snapshotLent(t, f); err != nil {
			return nil, nil, nil, err
		}
		args = append(args, "--mount", mountSpec("type=bind", "src="+f.src, "dst="+dst))
		results = append(results, f.src)
	case tf.Write:
		args = append(args, "--mount", mountSpec("type=bind", "src="+f.src, "dst="+dst, "readonly"))
		if resp.Kept != "" {
			resultsConflict(t, tf, resp.Kept)
		}
		if resp.RGID > 0 {
			gids = append(gids, resp.RGID)
		}
		args = append(args, "--mount", mountSpec("type=bind", "src="+filepath.Join(f.src, resp.Use), "dst="+dst+"/"+tf.resultsName()))
		results = append(results, filepath.Join(f.src, resp.Use))
	default:
		args = append(args, "--mount", mountSpec("type=bind", "src="+f.src, "dst="+dst, "readonly"))
	}
	return args, results, gids, nil
}

// helperProbe notes whether each shared folder opens (and may be written
// to, above read), from one helper; one at a time if that one fails (a
// folder not there now fails the whole helper).
func (n *node) helperProbe(list []sharedFolder) {
	if len(list) == 0 {
		return
	}
	req := helperReq{Op: "probe"}
	var mounts []helperMount
	for _, f := range list {
		req.Folders = append(req.Folders, f.Path)
		req.Levels = append(req.Levels, f.Access)
		mounts = append(mounts, helperMount{f.Path, f.Access == accessRead}) // as tasks get it
	}
	resp, err := n.helperCall(req, mounts...)
	if err == nil && len(resp.Notes) == len(list) {
		for i, f := range list {
			note := resp.Notes[i]
			note.Program = programNode
			n.access.set(f.ID, note)
		}
		return
	}
	if len(list) == 1 {
		problem := accessProblem(err)
		var se *shareError
		if errors.As(err, &se) && se.Code == "not_found" {
			problem = problemMissing
		}
		if problem == "" {
			problem = problemMissing
		}
		msg := ""
		if err != nil {
			msg = err.Error()
		}
		n.access.set(list[0].ID, accessNote{Problem: problem, Error: msg, Program: programNode})
		return
	}
	for _, f := range list {
		n.helperProbe([]sharedFolder{f})
	}
}

// probeLoop probes every shared folder now and then, through helpers: what
// the node reports of them (folderProblem) is what was last seen.
func (n *node) probeLoop(every time.Duration) {
	for {
		if list, err := loadShared(n.opt.root); err == nil {
			n.helperProbe(list)
		}
		time.Sleep(every)
	}
}

// runFolderHelper is `eriphe-node folder-helper`, in the helper's
// container: the request on stdin, the answer on stdout.
func runFolderHelper(in io.Reader, out io.Writer) int {
	var req helperReq
	resp := &helperResp{}
	if err := json.NewDecoder(io.LimitReader(in, 1<<20)).Decode(&req); err != nil {
		resp.Error = "bad request: " + err.Error()
	} else {
		resp = doHelper(req)
	}
	json.NewEncoder(out).Encode(resp)
	if resp.Refused != nil || resp.Error != "" || resp.TooBig {
		return 1
	}
	return 0
}

// doHelper does what a helper is asked, with the node's own functions.
func doHelper(req helperReq) *helperResp {
	resp := &helperResp{}
	fail := func(err error) *helperResp {
		var se *shareError
		switch {
		case errors.As(err, &se):
			resp.Refused = se
		case errors.Is(err, errSnapshotTooBig):
			resp.TooBig = true
		default:
			resp.Error = err.Error()
		}
		return resp
	}
	// The folder, as its own place.
	check := func() (*resolvedFolder, error) {
		if !filepath.IsAbs(req.Folder) {
			return nil, shareErr(400, "invalid_path", "%s: not an absolute path", req.Folder)
		}
		h := &node{opt: options{root: req.Own, shares: shareFlag{"folder": {Path: filepath.Clean(req.Folder)}}}}
		f, err := h.resolveFolder("folder", "")
		if err == nil {
			err = h.shareable(f)
		}
		return f, err
	}
	switch req.Op {
	case "check":
		f, err := check()
		if err != nil {
			return fail(err)
		}
		resp.Real = f.real
	case "prepare":
		f, err := check()
		if err != nil {
			return fail(err)
		}
		resp.Real, resp.GID = f.real, ownerGID(f.real)
		if req.Write {
			tf := TaskFolder{Results: req.Results, Former: req.Former}
			use, kept := chooseResults(f.real, tf.resultsName(), req.Former)
			gid, err := ensureResults(filepath.Join(f.real, use), resp.GID)
			if err != nil {
				return fail(err)
			}
			resp.Use, resp.Kept, resp.RGID = use, kept, gid
		}
	case "snapshot":
		method, copied, err := takeSnapshot(req.Folder, req.Dst, req.Limit)
		if err != nil {
			return fail(err)
		}
		resp.Method, resp.Copied = method, copied
	case "restore":
		if err := refill(req.Folder, req.Dst); err != nil {
			return fail(err)
		}
	case "handback":
		for _, d := range req.Dirs {
			if _, err := handBackDir(d); err != nil {
				log.Printf("results in %s: %v", d, err)
			}
		}
	case "probe":
		for i, p := range req.Folders {
			note := accessNote{At: time.Now().UTC()}
			if err := probeRead(p); err != nil {
				note.Problem, note.Error = accessProblem(err), err.Error()
			} else if i < len(req.Levels) && req.Levels[i] != accessRead && !writableByUser(p) {
				note.Problem = problemReadOnly
			}
			resp.Notes = append(resp.Notes, note)
		}
	default:
		resp.Error = "unknown op " + req.Op
	}
	return resp
}

// refill empties the folder at path and copies files back into it: a
// restore's second half (restoreSnapshot).
func refill(path, files string) error {
	entries, err := os.ReadDir(path)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if err := forceRemove(filepath.Join(path, e.Name())); err != nil {
			return fmt.Errorf("cannot empty the folder: %v", err)
		}
	}
	if _, _, err := copyTree(files, path, 0); err != nil {
		return fmt.Errorf("cannot copy the snapshot back: %v", err)
	}
	return nil
}
