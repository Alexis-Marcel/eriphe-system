package main

import (
	"encoding/json"
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
