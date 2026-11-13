package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Images a task hands to a surface (#470). Most sites forbid being framed,
// and a surface never loads anything from outside (surfaces_media.go): to
// show a page, a task lent eriphe-workspace captures it in its container
// (eriphe-capture, worker/base) and posts the image to its node, which
// relays it here with the node's secret and the task's id:
//
//	POST /v1/node/tasks/{id}/media?surface=<id>   (node secret, the image as body)
//
// eriphe-core keeps it in its data folder under a random id, for that
// surface only, and answers its address, media:<id>, which the task writes
// as an Image's url. The bytes never go through a model. surfaceView signs
// that address for the page like any other (GET /espace/media), and only
// on the surface it was given for. A PNG or a JPEG only, sniffed from its
// bytes, 5 MiB at most, a few per surface; it goes with its surface
// (surfaceRemove), and an image its surface no longer shows goes after an
// hour (tickSurfaces).

const (
	surfaceMediaMax        = 5 << 20
	surfaceMediaPerSurface = 10
	surfaceMediaUnused     = time.Hour
	surfaceMediaScheme     = "media:"
)

const surfaceMediaSchema = `
CREATE TABLE IF NOT EXISTS surface_media (
  id TEXT PRIMARY KEY, surface TEXT NOT NULL, task TEXT NOT NULL, type TEXT NOT NULL,
  size INTEGER NOT NULL, created INTEGER NOT NULL);
CREATE INDEX IF NOT EXISTS surface_media_surface ON surface_media (surface);
`

func init() { extraSchema = append(extraSchema, surfaceMediaSchema) }

var surfaceMediaID = regexp.MustCompile(`^med_[0-9a-f]{32}$`)

// surfaceMedia is an image kept for a surface.
type surfaceMedia struct {
	ID, Surface, Task, Type string
	Size                    int64
	Created                 time.Time
}

func (s *Store) surfaceMediaDir() string { return filepath.Join(s.dir, "surface-media") }

func (s *Store) surfaceMediaPath(id string) string { return filepath.Join(s.surfaceMediaDir(), id) }

// surfaceMediaRef is the id an address names, "" for none of ours.
func surfaceMediaRef(raw string) string {
	id, ok := strings.CutPrefix(strings.TrimSpace(raw), surfaceMediaScheme)
	if !ok || !surfaceMediaID.MatchString(id) {
		return ""
	}
	return id
}

func (c *core) surfaceMediaGet(id string) (*surfaceMedia, error) {
	if !surfaceMediaID.MatchString(id) {
		return nil, errors.New("no such image")
	}
	var m surfaceMedia
	var created int64
	err := c.store.db.QueryRow(`SELECT id, surface, task, type, size, created FROM surface_media WHERE id = ?`, id).
		Scan(&m.ID, &m.Surface, &m.Task, &m.Type, &m.Size, &created)
	if err != nil {
		return nil, errors.New("no such image")
	}
	m.Created = time.Unix(created, 0)
	return &m, nil
}

// surfaceMediaType tells a PNG or a JPEG from its bytes; anything else is
// refused, whatever its sender called it.
func surfaceMediaType(data []byte) (string, bool) {
	switch t := http.DetectContentType(data); t {
	case "image/png", "image/jpeg":
		return t, true
	}
	return "", false
}

// errMediaRefused is an image refused for what it is or where it goes.
type errMediaRefused struct {
	status int
	msg    string
}

func (e errMediaRefused) Error() string { return e.msg }

// surfaceMediaPut keeps an image a task gives for one of its surfaces.
func (c *core) surfaceMediaPut(taskID, surfaceID string, data []byte) (*surfaceMedia, error) {
	if !surfaceIDRe.MatchString(surfaceID) {
		return nil, errMediaRefused{http.StatusBadRequest, "surface: the id of the surface the image is for"}
	}
	who := c.taskCaller(taskID)
	switch {
	case who.only != "":
		if surfaceID != who.only {
			return nil, errMediaRefused{http.StatusForbidden, "this task fills the surface " + who.only + " only"}
		}
	default:
		if s, err := c.surface(surfaceID); err == nil && !mayChange(who.author, s.Author) {
			return nil, errMediaRefused{http.StatusForbidden, "the surface " + surfaceID + " is " + s.Author + "'s"}
		}
	}
	if len(data) == 0 {
		return nil, errMediaRefused{http.StatusBadRequest, "no image"}
	}
	if len(data) > surfaceMediaMax {
		return nil, errMediaRefused{http.StatusRequestEntityTooLarge, "too large: " + humanSize(surfaceMediaMax) + " at most"}
	}
	typ, ok := surfaceMediaType(data)
	if !ok {
		return nil, errMediaRefused{http.StatusUnsupportedMediaType, "a PNG or a JPEG only"}
	}
	var n int
	c.store.db.QueryRow(`SELECT COUNT(*) FROM surface_media WHERE surface = ?`, surfaceID).Scan(&n)
	if n >= surfaceMediaPerSurface {
		return nil, errMediaRefused{http.StatusTooManyRequests, strconv.Itoa(surfaceMediaPerSurface) + " images per surface at most: show fewer"}
	}
	if err := os.MkdirAll(c.store.surfaceMediaDir(), 0o700); err != nil {
		return nil, err
	}
	b := make([]byte, 16)
	rand.Read(b)
	m := &surfaceMedia{ID: "med_" + hex.EncodeToString(b), Surface: surfaceID, Task: taskID, Type: typ, Size: int64(len(data)), Created: time.Now()}
	if err := os.WriteFile(c.store.surfaceMediaPath(m.ID), data, 0o600); err != nil {
		return nil, err
	}
	if _, err := c.store.db.Exec(`INSERT INTO surface_media (id, surface, task, type, size, created) VALUES (?, ?, ?, ?, ?, ?)`,
		m.ID, m.Surface, m.Task, m.Type, m.Size, m.Created.Unix()); err != nil {
		os.Remove(c.store.surfaceMediaPath(m.ID))
		return nil, err
	}
	log.Printf("surface %s: image %s (%s, %s) from task %s", surfaceID, m.ID, typ, humanSize(m.Size), taskID)
	return m, nil
}

// surfaceMediaDrop removes images: their files, then their rows.
func (c *core) surfaceMediaDrop(ids []string) {
	for _, id := range ids {
		os.Remove(c.store.surfaceMediaPath(id))
		c.store.db.Exec(`DELETE FROM surface_media WHERE id = ?`, id)
	}
}

// surfaceMediaOf are the images kept for a surface.
func (c *core) surfaceMediaOf(surfaceID string) []surfaceMedia {
	rows, err := c.store.db.Query(`SELECT id, surface, task, type, size, created FROM surface_media WHERE surface = ?`, surfaceID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []surfaceMedia
	for rows.Next() {
		var m surfaceMedia
		var created int64
		if rows.Scan(&m.ID, &m.Surface, &m.Task, &m.Type, &m.Size, &created) == nil {
			m.Created = time.Unix(created, 0)
			out = append(out, m)
		}
	}
	return out
}

// dropSurfaceMedia removes the images of a surface removed.
func (c *core) dropSurfaceMedia(surfaceID string) {
	var ids []string
	for _, m := range c.surfaceMediaOf(surfaceID) {
		ids = append(ids, m.ID)
	}
	c.surfaceMediaDrop(ids)
}

// sweepSurfaceMedia removes, after surfaceMediaUnused, the images whose
// surface is gone or no longer shows them.
func (c *core) sweepSurfaceMedia(all []surface, now time.Time) {
	shown := map[string]string{}
	for _, s := range all {
		shown[s.ID] = string(s.Components) + string(s.Data)
	}
	rows, err := c.store.db.Query(`SELECT id, surface FROM surface_media WHERE created < ?`, now.Add(-surfaceMediaUnused).Unix())
	if err != nil {
		return
	}
	var gone []string
	for rows.Next() {
		var id, surfaceID string
		if rows.Scan(&id, &surfaceID) != nil {
			continue
		}
		if text, ok := shown[surfaceID]; !ok || !strings.Contains(text, id) {
			gone = append(gone, id)
		}
	}
	rows.Close()
	c.surfaceMediaDrop(gone)
}

// handleTaskMedia is POST /v1/node/tasks/{id}/media, for nodes: an image a
// task lent the workspace gives for one of its surfaces.
func (c *core) handleTaskMedia(w http.ResponseWriter, r *http.Request) {
	node, ok := c.nodeFromRequest(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	t, err := c.store.task(r.PathValue("id"))
	if err != nil || t.Node != node || finished(t.State) {
		http.Error(w, "no such task on this node", http.StatusNotFound)
		return
	}
	if !grantAllows(c.taskGrant(t), workspaceServer, "surface_write") {
		http.Error(w, "this task has no access to "+workspaceServer, http.StatusForbidden)
		return
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, surfaceMediaMax+1))
	if err != nil {
		http.Error(w, "the image could not be read", http.StatusBadRequest)
		return
	}
	m, err := c.surfaceMediaPut(t.ID, strings.TrimSpace(r.URL.Query().Get("surface")), data)
	if err != nil {
		status := http.StatusInternalServerError
		if e, ok := err.(errMediaRefused); ok {
			status = e.status
		}
		http.Error(w, err.Error(), status)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"src": surfaceMediaScheme + m.ID, "type": m.Type, "size": m.Size,
		"note": "Write src as an Image's url in the surface " + m.Surface + " (only there), with a Link to the page's address beside it.",
	})
}
