package main

import (
	"encoding/json"
	"log"
	"time"

	"github.com/Alexis-Marcel/eriphe-system/core/a2ui"
)

// The surfaces on the page (#419): what the event stream sends of them,
// and what the page sends back.
//
// The stream's "a2ui" event carries the A2UI messages of the surfaces'
// changes, in order (surfaceFeed). The first one of a connection is a
// snapshot that resets the page: one createSurface per surface. So is one
// after the stream fell behind: the page clears what it has and starts
// again from it.
//
// The page sends back an A2UI action (surfaceAction, surface_actions.go)
// or a render error (surfaceError), each about one surface.

const (
	surfaceEventMax = 512 // messages in one a2ui event
	surfaceErrorMax = 500 // characters of an error kept
)

// a2uiEvent is the data of the event stream's "a2ui" event. Seq is the
// seq of the last change in it (of the snapshot, for a reset). Reset says
// the page drops the surfaces it has before applying Messages.
type a2uiEvent struct {
	Seq      int64          `json:"seq"`
	Reset    bool           `json:"reset,omitempty"`
	Messages []a2ui.Message `json:"messages"`
}

// surfaceFeed follows the surfaces for one event stream.
type surfaceFeed struct {
	c       *core
	changes <-chan surfaceChange
	cancel  func()
}

// feedSurfaces subscribes, and gives the snapshot to send first.
func (c *core) feedSurfaces() (*surfaceFeed, a2uiEvent) {
	f := &surfaceFeed{c: c}
	return f, f.subscribe()
}

func (f *surfaceFeed) subscribe() a2uiEvent {
	if f.cancel != nil {
		f.cancel()
	}
	snap, changes, cancel := f.c.surfaceSubscribe()
	f.changes, f.cancel = changes, cancel
	ev := a2uiEvent{Reset: true, Messages: []a2ui.Message{}}
	for _, ch := range snap {
		ev.Seq = ch.Seq
		ev.Messages = append(ev.Messages, ch.Message)
	}
	return ev
}

// next is what the stream sends once a change came (or the channel was
// closed: ok false): it with the changes waiting behind it, in one event;
// or, when the hub dropped this feed for falling behind, a new snapshot.
func (f *surfaceFeed) next(first surfaceChange, ok bool) a2uiEvent {
	if !ok {
		return f.subscribe()
	}
	ev := a2uiEvent{Seq: first.Seq, Messages: []a2ui.Message{first.Message}}
	for len(ev.Messages) < surfaceEventMax {
		select {
		case ch, ok := <-f.changes:
			if !ok {
				return f.subscribe()
			}
			ev.Seq = ch.Seq
			ev.Messages = append(ev.Messages, ch.Message)
		default:
			return ev
		}
	}
	return ev
}

func (f *surfaceFeed) close() { f.cancel() }

// surfaceActed is the last action the user took on a surface.
type surfaceActed struct {
	Name      string `json:"name"`
	Component string `json:"component"`
	At        int64  `json:"at"`
}

// surfaceLastError is the last error about a surface for its author to
// fix: one the page met rendering it, or one code met with it (a binding
// a source no longer gives). c.surface(id).Look.LastError reads it; the
// author writing the surface again clears it.
type surfaceLastError struct {
	Code    string `json:"code"`
	Path    string `json:"path,omitempty"`
	Message string `json:"message"`
	At      int64  `json:"at"`
}

// surfaceError keeps an error about a surface on it, for its author
// (Look.LastError), and logs it: one the page met rendering it
// (POST /api/espace/error), or one code met with it. It sends nothing to
// the page. It takes c.surf.mu: never call it with the lock held (as in
// refreshOnce); note the error, then call it once the lock is released.
func (c *core) surfaceError(id string, e a2ui.Error) error {
	r := surfaceLastError{Code: truncate(e.Code, 64), Path: truncate(e.Path, 200),
		Message: truncate(oneLine(e.Message), surfaceErrorMax), At: time.Now().Unix()}
	var author string
	err := c.noteOnSurface(id, func(l *surfaceLook) { l.LastError = &r })
	if err != nil {
		return err
	}
	if s, err := c.surface(id); err == nil {
		author = s.Author
	}
	log.Printf("surface %s (%s): error %s at %q: %s", id, author, r.Code, r.Path, r.Message)
	return nil
}

// noteOnSurface changes what code knows of a surface (its look), with no
// message to the page; errNoSurface if there is none.
func (c *core) noteOnSurface(id string, change func(*surfaceLook)) error {
	c.surf.mu.Lock()
	defer c.surf.mu.Unlock()
	s, err := c.surface(id)
	if err != nil {
		return err
	}
	change(&s.Look)
	raw, _ := json.Marshal(s.Look)
	_, err = c.store.db.Exec(`UPDATE surfaces SET look = ? WHERE id = ?`, string(raw), id)
	return err
}
