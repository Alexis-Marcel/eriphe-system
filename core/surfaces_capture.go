package main

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Images a task handed to a thread's surface (#470). A thread has no
// surface any more (#631): an upload is refused (handleTaskMedia), and
// what was kept goes as its surface goes (surfaceRemove) or after an hour
// unshown (tickSurfaces). The media a node shows come with the workspace
// (#633).

const (
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

// handleTaskMedia is POST /v1/device/tasks/{id}/media, for devices: an
// image a task captured. No surface takes one any more (#631: a thread
// has no surface): it is refused, once the device and its task are known.
func (c *core) handleTaskMedia(w http.ResponseWriter, r *http.Request) {
	device, ok := c.deviceFromRequest(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	t, err := c.store.task(r.PathValue("id"))
	if err != nil || t.Device != device || finished(t.State) {
		http.Error(w, "no such task on this device", http.StatusNotFound)
		return
	}
	http.Error(w, "no surface takes a task's image any more", http.StatusGone)
}
