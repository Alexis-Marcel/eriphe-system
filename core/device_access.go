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
//     file a task wrote in a folder), kept 90 days;
//   - "Cut access": the device stays registered and installed, but
//     eriphe-core sends it no work, refuses to list or lend its folders,
//     drops its tunnel and lets it in again only once access is restored.
//
//	GET  /v1/devices/{name}/log       its last entries, newest first
//	POST /v1/devices/{name}/suspend   cut access
//	POST /v1/devices/{name}/resume    restore access

// Kinds of entries of the access log.
const (
	logList    = "list"    // a folder of it listed: Detail is share:/path
	logShare   = "share"   // a folder opened to Eriphe
	logUnshare = "unshare" // a folder closed again
	logLower   = "lower"   // a folder's copies turned off, from here
	logCopy    = "copy"    // a copy of a folder kept on it, or deleted: Detail says which
	logTask    = "task"    // a task run on it: Detail is its agent
	logLend    = "lend"    // a folder lent to a task, with its right
	logResult  = "result"  // a file a task wrote in a folder: Detail is its path
	logCut     = "cut"     // access cut
	logRestore = "restore" // access restored
)

const (
	deviceLogRetention = 90 * 24 * time.Hour
	deviceLogMax       = 2000 // entries kept per device, whatever their age
	// The same entry again within this time is not repeated (a folder
	// browsed back and forth, a result written several times).
	deviceLogRepeat = 10 * time.Minute
)

func init() {
	extraSchema = append(extraSchema, `
CREATE TABLE IF NOT EXISTS device_log (
  id INTEGER PRIMARY KEY AUTOINCREMENT, device TEXT NOT NULL, time INTEGER NOT NULL,
  kind TEXT NOT NULL, task TEXT NOT NULL DEFAULT '', folder TEXT NOT NULL DEFAULT '',
  access TEXT NOT NULL DEFAULT '', detail TEXT NOT NULL DEFAULT '');
CREATE INDEX IF NOT EXISTS device_log_device ON device_log (device, id);`)
}

// DeviceLogEntry is one thing Eriphe did on a device.
type DeviceLogEntry struct {
	ID     int64     `json:"id"`
	Time   time.Time `json:"time"`
	Kind   string    `json:"kind"`
	Task   string    `json:"task,omitempty"`
	Folder string    `json:"folder,omitempty"`
	Access string    `json:"access,omitempty"` // read or write, for a folder
	Detail string    `json:"detail,omitempty"`
}

// logDevice records an entry for a device, unless the same one was just
// recorded, and forgets what is past the retention.
func (s *Store) logDevice(device string, e DeviceLogEntry) error {
	if device == "" {
		return nil
	}
	now := time.Now()
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM device_log WHERE device = ? AND kind = ? AND task = ? AND folder = ? AND access = ? AND detail = ? AND time >= ?`,
		device, e.Kind, e.Task, e.Folder, e.Access, e.Detail, now.Add(-deviceLogRepeat).Unix()).Scan(&n)
	if n > 0 {
		return nil
	}
	if _, err := s.db.Exec(`INSERT INTO device_log (device, time, kind, task, folder, access, detail) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		device, now.Unix(), e.Kind, e.Task, e.Folder, e.Access, e.Detail); err != nil {
		return err
	}
	_, err := s.db.Exec(`DELETE FROM device_log WHERE device = ? AND (time < ? OR id <= COALESCE(
		(SELECT id FROM device_log WHERE device = ? ORDER BY id DESC LIMIT 1 OFFSET ?), 0))`,
		device, now.Add(-deviceLogRetention).Unix(), device, deviceLogMax)
	return err
}

// deviceLog is a device's log, newest first: up to limit entries older than
// the entry before (all if 0).
func (s *Store) deviceLog(device string, limit int, before int64) ([]DeviceLogEntry, error) {
	if before <= 0 {
		before = 1<<63 - 1
	}
	rows, err := s.db.Query(`SELECT id, time, kind, task, folder, access, detail FROM device_log
		WHERE device = ? AND id < ? AND time >= ? ORDER BY id DESC LIMIT ?`,
		device, before, time.Now().Add(-deviceLogRetention).Unix(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DeviceLogEntry{}
	for rows.Next() {
		var e DeviceLogEntry
		var at int64
		if err := rows.Scan(&e.ID, &at, &e.Kind, &e.Task, &e.Folder, &e.Access, &e.Detail); err != nil {
			return nil, err
		}
		e.Time = time.Unix(at, 0)
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Store) setDeviceSuspended(name string, suspended bool) error {
	res, err := s.db.Exec(`UPDATE devices SET suspended = ? WHERE name = ?`, suspended, name)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errNoDevice
	}
	return nil
}

// deviceSuspended says whether a device's access is cut.
func (s *Store) deviceSuspended(name string) bool {
	var v bool
	err := s.db.QueryRow(`SELECT suspended FROM devices WHERE name = ?`, name).Scan(&v)
	return err == nil && v
}

var errDeviceSuspended = errors.New("access to this device is cut: it is restored in Settings → Devices")

// suspendedError is the refusal of anything for a device whose access is cut.
func suspendedError(device string) error {
	return fmt.Errorf("access to the device %s is cut: it is restored in Settings → Devices", device)
}

// logDevice records an entry for a device, and says why in eriphe-core's log
// if it could not.
func (c *core) logDevice(device string, e DeviceLogEntry) {
	if err := c.store.logDevice(device, e); err != nil {
		log.Printf("access log of %s: %v", device, err)
	}
}

// logPlaced records a task given to a device, with the folders lent to it.
func (c *core) logPlaced(t *TaskRow, device string) {
	if isRoleKind(t.Kind) {
		return // an assistant: Eriphe itself, not a task it gives out
	}
	c.logDevice(device, DeviceLogEntry{Kind: logTask, Task: t.ID, Detail: t.Agent})
	for _, name := range splitNames(t.Folders) {
		access := accessRead
		if f, err := c.store.folder(name); err == nil {
			access = f.Access
		}
		c.logDevice(device, DeviceLogEntry{Kind: logLend, Task: t.ID, Folder: name, Access: access})
	}
}

// logResults records the files a task wrote in the folders it may write
// in, as its agent reports them (the file tools of Claude Code: Write,
// Edit, MultiEdit, NotebookEdit).
func (c *core) logResults(t *TaskRow, events []EventRow) {
	if t.Folders == "" {
		return
	}
	var write []string
	for _, name := range splitNames(t.Folders) {
		if f, err := c.store.folder(name); err == nil && f.Access == accessWrite {
			write = append(write, f.Name)
		}
	}
	if len(write) == 0 {
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
		for _, name := range write {
			prefix := (&Folder{Name: name}).mountPath() + "/"
			if strings.HasPrefix(path, prefix) && !strings.Contains(path[len(prefix):], "..") {
				c.logDevice(t.Device, DeviceLogEntry{Kind: logResult, Task: t.ID, Folder: name, Access: accessWrite, Detail: path[len(prefix):]})
			}
		}
	}
}

// suspendDevice cuts a device's access at once: nothing is uninstalled, but
// its tasks stop, its services move to another device, no work goes to it,
// its folders are neither listed nor lent, and its tunnel is dropped and
// refused until resumeDevice.
func (c *core) suspendDevice(name string) error {
	if err := c.store.setDeviceSuspended(name, true); err != nil {
		return err
	}
	// Its tasks end here: the device can no longer report them. Told to stop
	// first, in case it reads its assignments once more.
	rows, err := c.store.db.Query(`SELECT id, kind FROM tasks WHERE device = ? AND state NOT IN ('stopped', 'failed', 'lost')`, name)
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
	c.store.setDeviceStatus(name, false, nil)
	c.logDevice(name, DeviceLogEntry{Kind: logCut})
	log.Printf("device %s: access cut (%d task(s) stopped)", name, len(tasks))
	for _, t := range tasks {
		if !isRoleKind(t.kind) {
			c.notifyBrain(t.id, stateStopped)
		}
	}
	go c.placeServices()
	return nil
}

// resumeDevice restores a device's access: it connects again by itself.
func (c *core) resumeDevice(name string) error {
	if err := c.store.setDeviceSuspended(name, false); err != nil {
		return err
	}
	c.logDevice(name, DeviceLogEntry{Kind: logRestore})
	log.Printf("device %s: access restored", name)
	return nil
}

// deviceAccessRoutes serve the access log and the cut (admin token).
func (c *core) deviceAccessRoutes(mux *http.ServeMux, token string) {
	mux.Handle("GET /v1/devices/{name}/log", bearer(token, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if !c.store.hasDevice(name) {
			http.Error(w, errNoDevice.Error(), http.StatusNotFound)
			return
		}
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		if limit <= 0 || limit > 500 {
			limit = 100
		}
		before, _ := strconv.ParseInt(r.URL.Query().Get("before"), 10, 64)
		entries, err := c.store.deviceLog(name, limit, before)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"device": name, "suspended": c.store.deviceSuspended(name), "log": entries})
	})))
	access := func(suspend bool) http.Handler {
		return bearer(token, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			name := r.PathValue("name")
			err := c.resumeDevice
			if suspend {
				err = c.suspendDevice
			}
			switch e := err(name); {
			case errors.Is(e, errNoDevice):
				http.Error(w, e.Error(), http.StatusNotFound)
				return
			case e != nil:
				http.Error(w, e.Error(), http.StatusInternalServerError)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"name": name, "suspended": suspend})
		}))
	}
	mux.Handle("POST /v1/devices/{name}/suspend", access(true))
	mux.Handle("POST /v1/devices/{name}/resume", access(false))
}
