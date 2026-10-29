package main

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Access to a device. Connecting a device gives Eriphe access to it; this
// file keeps that access visible and in the user's hands:
//
//   - the access log: what Eriphe did on each device (a folder listed, a
//     folder opened or closed, a task run there, a folder lent to it, a
//     result dropped in a folder's Eriphe/), kept 90 days;
//   - "Cut access": the device stays registered and installed, but
//     eriphe-core sends it no work, refuses to list or lend its folders,
//     drops its tunnel and lets it in again only once access is restored.
//
//	GET  /v1/nodes/{name}/log       its last entries, newest first
//	POST /v1/nodes/{name}/suspend   cut access
//	POST /v1/nodes/{name}/resume    restore access

// Kinds of entries of the access log.
const (
	logList    = "list"    // a folder of it listed: Detail is share:/path
	logShare   = "share"   // a folder opened to Eriphe
	logUnshare = "unshare" // a folder closed again
	logTask    = "task"    // a task run on it: Detail is its agent
	logLend    = "lend"    // a folder lent to a task, with its right
	logResult  = "result"  // a file a task wrote in a folder's Eriphe/: Detail is its path
	logCut     = "cut"     // access cut
	logRestore = "restore" // access restored
)

const (
	nodeLogRetention = 90 * 24 * time.Hour
	nodeLogMax       = 2000 // entries kept per device, whatever their age
	// The same entry again within this time is not repeated (a folder
	// browsed back and forth, a result written several times).
	nodeLogRepeat = 10 * time.Minute
)

func init() {
	extraSchema = append(extraSchema, `
CREATE TABLE IF NOT EXISTS node_log (
  id INTEGER PRIMARY KEY AUTOINCREMENT, node TEXT NOT NULL, time INTEGER NOT NULL,
  kind TEXT NOT NULL, task TEXT NOT NULL DEFAULT '', folder TEXT NOT NULL DEFAULT '',
  access TEXT NOT NULL DEFAULT '', detail TEXT NOT NULL DEFAULT '');
CREATE INDEX IF NOT EXISTS node_log_node ON node_log (node, id);`)
}

// NodeLogEntry is one thing Eriphe did on a device.
type NodeLogEntry struct {
	ID     int64     `json:"id"`
	Time   time.Time `json:"time"`
	Kind   string    `json:"kind"`
	Task   string    `json:"task,omitempty"`
	Folder string    `json:"folder,omitempty"`
	Access string    `json:"access,omitempty"` // read or drop, for a folder
	Detail string    `json:"detail,omitempty"`
}

// logNode records an entry for a device, unless the same one was just
// recorded, and forgets what is past the retention.
func (s *Store) logNode(node string, e NodeLogEntry) error {
	if node == "" {
		return nil
	}
	now := time.Now()
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM node_log WHERE node = ? AND kind = ? AND task = ? AND folder = ? AND access = ? AND detail = ? AND time >= ?`,
		node, e.Kind, e.Task, e.Folder, e.Access, e.Detail, now.Add(-nodeLogRepeat).Unix()).Scan(&n)
	if n > 0 {
		return nil
	}
	if _, err := s.db.Exec(`INSERT INTO node_log (node, time, kind, task, folder, access, detail) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		node, now.Unix(), e.Kind, e.Task, e.Folder, e.Access, e.Detail); err != nil {
		return err
	}
	_, err := s.db.Exec(`DELETE FROM node_log WHERE node = ? AND (time < ? OR id <= COALESCE(
		(SELECT id FROM node_log WHERE node = ? ORDER BY id DESC LIMIT 1 OFFSET ?), 0))`,
		node, now.Add(-nodeLogRetention).Unix(), node, nodeLogMax)
	return err
}

// nodeLog is a device's log, newest first: up to limit entries older than
// the entry before (all if 0).
func (s *Store) nodeLog(node string, limit int, before int64) ([]NodeLogEntry, error) {
	if before <= 0 {
		before = 1<<63 - 1
	}
	rows, err := s.db.Query(`SELECT id, time, kind, task, folder, access, detail FROM node_log
		WHERE node = ? AND id < ? AND time >= ? ORDER BY id DESC LIMIT ?`,
		node, before, time.Now().Add(-nodeLogRetention).Unix(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []NodeLogEntry{}
	for rows.Next() {
		var e NodeLogEntry
		var at int64
		if err := rows.Scan(&e.ID, &at, &e.Kind, &e.Task, &e.Folder, &e.Access, &e.Detail); err != nil {
			return nil, err
		}
		e.Time = time.Unix(at, 0)
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Store) setNodeSuspended(name string, suspended bool) error {
	res, err := s.db.Exec(`UPDATE nodes SET suspended = ? WHERE name = ?`, suspended, name)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errNoNode
	}
	return nil
}

// nodeSuspended says whether a device's access is cut.
func (s *Store) nodeSuspended(name string) bool {
	var v bool
	err := s.db.QueryRow(`SELECT suspended FROM nodes WHERE name = ?`, name).Scan(&v)
	return err == nil && v
}

var errNodeSuspended = errors.New("access to this device is cut: it is restored in Settings → Devices")

// suspendedError is the refusal of anything for a device whose access is cut.
func suspendedError(node string) error {
	return fmt.Errorf("access to the device %s is cut: it is restored in Settings → Devices", node)
}

// logNode records an entry for a device, and says why in eriphe-core's log
// if it could not.
func (c *core) logNode(node string, e NodeLogEntry) {
	if err := c.store.logNode(node, e); err != nil {
		log.Printf("access log of %s: %v", node, err)
	}
}

// logPlaced records a task given to a device, with the folders lent to it.
func (c *core) logPlaced(t *TaskRow, node string) {
	if isRoleKind(t.Kind) {
		return // an assistant: Eriphe itself, not a task it gives out
	}
	c.logNode(node, NodeLogEntry{Kind: logTask, Task: t.ID, Detail: t.Agent})
	for _, name := range splitNames(t.Folders) {
		access := folderRead
		if f, err := c.store.folder(name); err == nil {
			access = f.Access
		}
		c.logNode(node, NodeLogEntry{Kind: logLend, Task: t.ID, Folder: name, Access: access})
	}
}

// logResults records the files a task wrote in the Eriphe/ subfolder of
// the folders it may drop results in, as its agent reports them (the file
// tools of Claude Code: Write, Edit, MultiEdit, NotebookEdit).
func (c *core) logResults(t *TaskRow, events []EventRow) {
	if t.Folders == "" {
		return
	}
	var drop []string
	for _, name := range splitNames(t.Folders) {
		if f, err := c.store.folder(name); err == nil && f.Access == folderDrop {
			drop = append(drop, f.Name)
		}
	}
	if len(drop) == 0 {
		return
	}
	for _, ev := range events {
		if ev.Type != "tool_use" {
			continue
		}
		switch ev.Data["name"] {
		case "Write", "Edit", "MultiEdit", "NotebookEdit":
		default:
			continue
		}
		in, _ := ev.Data["input"].(map[string]any)
		path, _ := in["file_path"].(string)
		if path == "" {
			path, _ = in["notebook_path"].(string)
		}
		for _, name := range drop {
			prefix := (Folder{Name: name}).mountPath() + "/Eriphe/"
			if strings.HasPrefix(path, prefix) && !strings.Contains(path[len(prefix):], "..") {
				c.logNode(t.Node, NodeLogEntry{Kind: logResult, Task: t.ID, Folder: name, Access: folderDrop,
					Detail: "Eriphe/" + path[len(prefix):]})
			}
		}
	}
}

// suspendNode cuts a device's access at once: nothing is uninstalled, but
// its tasks stop, its services move to another device, no work goes to it,
// its folders are neither listed nor lent, and its tunnel is dropped and
// refused until resumeNode.
func (c *core) suspendNode(name string) error {
	if err := c.store.setNodeSuspended(name, true); err != nil {
		return err
	}
	// Its tasks end here: the node can no longer report them. Told to stop
	// first, in case it reads its assignments once more.
	rows, err := c.store.db.Query(`SELECT id, kind FROM tasks WHERE node = ? AND state NOT IN ('stopped', 'failed', 'lost')`, name)
	if err != nil {
		return err
	}
	type ended struct{ id, kind string }
	var tasks []ended
	for rows.Next() {
		var e ended
		if rows.Scan(&e.id, &e.kind) == nil {
			tasks = append(tasks, e)
		}
	}
	rows.Close()
	for _, t := range tasks {
		c.store.updateTask(t.id, map[string]any{"desired": desiredStopped, "state": stateStopped,
			"pending_reason": "access to " + name + " was cut"})
	}
	c.bump()
	c.disconnect(name)
	c.store.setNodeStatus(name, false, nil)
	c.logNode(name, NodeLogEntry{Kind: logCut})
	log.Printf("node %s: access cut (%d task(s) stopped)", name, len(tasks))
	for _, t := range tasks {
		if !isRoleKind(t.kind) {
			c.notifyBrain(t.id, stateStopped)
		}
	}
	go c.placeServices()
	return nil
}

// resumeNode restores a device's access: it connects again by itself.
func (c *core) resumeNode(name string) error {
	if err := c.store.setNodeSuspended(name, false); err != nil {
		return err
	}
	c.logNode(name, NodeLogEntry{Kind: logRestore})
	log.Printf("node %s: access restored", name)
	return nil
}

// nodeAccessRoutes serve the access log and the cut (admin token).
func (c *core) nodeAccessRoutes(mux *http.ServeMux, token string) {
	mux.Handle("GET /v1/nodes/{name}/log", bearer(token, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if !c.store.hasNode(name) {
			http.Error(w, errNoNode.Error(), http.StatusNotFound)
			return
		}
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		if limit <= 0 || limit > 500 {
			limit = 100
		}
		before, _ := strconv.ParseInt(r.URL.Query().Get("before"), 10, 64)
		entries, err := c.store.nodeLog(name, limit, before)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"node": name, "suspended": c.store.nodeSuspended(name), "log": entries})
	})))
	access := func(suspend bool) http.Handler {
		return bearer(token, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			name := r.PathValue("name")
			err := c.resumeNode
			if suspend {
				err = c.suspendNode
			}
			switch e := err(name); {
			case errors.Is(e, errNoNode):
				http.Error(w, e.Error(), http.StatusNotFound)
				return
			case e != nil:
				http.Error(w, e.Error(), http.StatusInternalServerError)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"name": name, "suspended": suspend})
		}))
	}
	mux.Handle("POST /v1/nodes/{name}/suspend", access(true))
	mux.Handle("POST /v1/nodes/{name}/resume", access(false))
}
