package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/Alexis-Marcel/eriphe-system/core/a2ui"
)

// The composed workspace, /espace: a page next to the conversation where
// Eriphe's composition is laid out (#381). It is a React app (web/espace),
// built into web/static/espace and embedded with the other static files;
// eriphe-core serves it from a small HTML shell (templates/espace.html).
// The app gets its words, the CSRF token and the assistant's name from
// /espace/boot, never from inline code: the CSP stays strict (web.go).

func (w *web) espaceRoutes(mux *http.ServeMux) {
	mux.Handle("GET /espace", w.auth(w.handleEspace))
	mux.Handle("GET /espace/boot", w.auth(w.handleEspaceBoot))
	mux.Handle("GET /espace/catalog/v1.json", w.auth(w.handleEspaceCatalog))
	mux.Handle("GET /espace/media", w.auth(w.handleMedia)) // surfaces_media.go
	mux.Handle("POST /api/espace/gesture", http.MaxBytesHandler(w.auth(w.handleGesture), 4<<10))
	mux.Handle("GET /api/proposals/{ref}", w.auth(w.handleProposal)) // web_espace_proposal.go
	mux.Handle("POST /api/espace/action", http.MaxBytesHandler(w.auth(w.handleSurfaceAction), 64<<10))
	mux.Handle("POST /api/espace/error", http.MaxBytesHandler(w.auth(w.handleSurfaceError), 16<<10))
}

// readEvent reads an A2UI renderer-to-agent message the page sends about a
// surface, checked against the protocol's schema. It answers 400 itself.
func readEvent(rw http.ResponseWriter, r *http.Request) (a2ui.Event, bool) {
	var ev a2ui.Event
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSON(rw, http.StatusBadRequest, map[string]string{"error": "too large"})
		return ev, false
	}
	if errs := a2ui.Eriphe().ValidateEvent(raw); len(errs) > 0 {
		writeJSON(rw, http.StatusBadRequest, map[string]any{"error": "not an A2UI v1.0 message", "errors": errs})
		return ev, false
	}
	if json.Unmarshal(raw, &ev) != nil {
		writeJSON(rw, http.StatusBadRequest, map[string]string{"error": "not an A2UI v1.0 message"})
		return ev, false
	}
	return ev, true
}

// handleSurfaceAction is POST /api/espace/action: an A2UI action the user
// took on a surface (surfaceAction). 204; 400 for anything but a valid
// action, 404 for a surface there is not, 501 for an action not handled.
func (w *web) handleSurfaceAction(rw http.ResponseWriter, r *http.Request, _ *session) {
	ev, ok := readEvent(rw, r)
	if !ok {
		return
	}
	if ev.Action == nil {
		writeJSON(rw, http.StatusBadRequest, map[string]string{"error": "an action, not an error"})
		return
	}
	switch err := w.c.surfaceAction(r.Context(), ev.Action.SurfaceID, *ev.Action); {
	case errors.Is(err, errNoSurface):
		writeJSON(rw, http.StatusNotFound, map[string]string{"error": err.Error()})
	case errors.Is(err, errActionNotHandled):
		writeJSON(rw, http.StatusNotImplemented, map[string]string{"error": err.Error()})
	case err != nil:
		writeJSON(rw, http.StatusInternalServerError, map[string]string{"error": err.Error()})
	default:
		rw.WriteHeader(http.StatusNoContent)
	}
}

// handleSurfaceError is POST /api/espace/error: an error the page met
// rendering a surface, for its author (surfaceError). 204; 400 for
// anything but a valid error about a surface, 404 for a surface there is
// not.
func (w *web) handleSurfaceError(rw http.ResponseWriter, r *http.Request, _ *session) {
	ev, ok := readEvent(rw, r)
	if !ok {
		return
	}
	if ev.Error == nil || ev.Error.SurfaceID == "" {
		writeJSON(rw, http.StatusBadRequest, map[string]string{"error": "an error about a surface"})
		return
	}
	switch err := w.c.surfaceError(ev.Error.SurfaceID, *ev.Error); {
	case errors.Is(err, errNoSurface):
		writeJSON(rw, http.StatusNotFound, map[string]string{"error": err.Error()})
	case err != nil:
		writeJSON(rw, http.StatusInternalServerError, map[string]string{"error": err.Error()})
	default:
		rw.WriteHeader(http.StatusNoContent)
	}
}

// gestureIn is a gesture of the user's on the workspace (workspace.go).
type gestureIn struct {
	What  string `json:"what"` // opened, ignored
	Ref   string `json:"ref"`
	Title string `json:"title"`
}

// handleGesture is POST /api/espace/gesture: the page says what the user
// opened, or what left the screen unopened, for the extraction to read.
// 204; 400 for a gesture it does not know.
func (w *web) handleGesture(rw http.ResponseWriter, r *http.Request, _ *session) {
	var in gestureIn
	if json.NewDecoder(r.Body).Decode(&in) != nil || w.c.noteGesture(in.What, in.Ref, in.Title, time.Now()) != nil {
		writeJSON(rw, http.StatusBadRequest, map[string]string{"error": errGesture.Error()})
		return
	}
	rw.WriteHeader(http.StatusNoContent)
}

func (w *web) handleEspace(rw http.ResponseWriter, r *http.Request, s *session) {
	w.render(rw, r, s, "espace", "espace", page{Title: "nav.espace"})
}

// espaceBoot is what the app needs before its first paint.
type espaceBoot struct {
	Lang    string            `json:"lang"`
	Name    string            `json:"name"`
	CSRF    string            `json:"csrf"`
	Strings map[string]string `json:"strings"`
}

func (w *web) handleEspaceBoot(rw http.ResponseWriter, r *http.Request, s *session) {
	lang := w.lang(r)
	w.c.noteWorkspaceLang(lang) // the queue's surfaces and Eriphe's line follow it
	b := espaceBoot{Lang: lang, Name: assistantName(), CSRF: s.CSRF, Strings: espaceWords(lang, assistantName())}
	rw.Header().Set("Content-Type", "application/json")
	rw.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(rw).Encode(b)
}

// handleEspaceCatalog is GET /espace/catalog/v1.json: the catalog of shapes
// a surface is made of (core/a2ui), which the app's renderer reads. The
// path carries the catalog's version: a v2 gets a path of its own.
func (w *web) handleEspaceCatalog(rw http.ResponseWriter, r *http.Request, _ *session) {
	rw.Header().Set("Content-Type", "application/json")
	rw.Header().Set("Cache-Control", "no-cache")
	rw.Write(a2ui.CatalogJSON())
}

// espaceWords are the app's words in a language, with the assistant's name.
func espaceWords(lang, name string) map[string]string {
	out := map[string]string{}
	for k := range espaceStrings["fr"] {
		in := lang
		s, ok := espaceStrings[in][k]
		if !ok {
			s, in = espaceStrings["fr"][k], "fr"
		}
		out[k] = withName(in, s, name)
	}
	return out
}

// espaceStringsFile is the words the app uses on its dev server, with no
// eriphe-core behind (web/espace/src/strings.json): a test keeps it the
// same as the tables above.
func espaceStringsFile() []byte {
	langs := map[string]map[string]string{}
	for _, lang := range []string{"fr", "en"} {
		langs[lang] = espaceWords(lang, defaultDisplayName)
	}
	// Sorted keys, two-space indent: json.Marshal sorts map keys.
	raw, _ := json.MarshalIndent(langs, "", "  ")
	return append(raw, '\n')
}

// espaceKeys lists the keys of a language, for the tests.
func espaceKeys(lang string) []string {
	var keys []string
	for k := range espaceStrings[lang] {
		if strings.HasPrefix(k, "espace.") || k == "nav.espace" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys
}
