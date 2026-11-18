package main

import (
	"net/http"
)

// The workspace's « Ce que je sais » (#635): what Eriphe knows, read only,
// drawn from memory.db with recall's rules (knownMemories) and in the
// files of its readable mirror (memory_mirror.go). An external memory is
// marked. It is the user's reading: no use is counted, no turn is
// tainted, and nothing is written; to correct or forget, the user tells
// Eriphe. It is not a node: the node tools never reach it.

func (w *web) knownRoutes(mux *webMux) {
	mux.Handle("GET /api/known", w.auth(w.handleKnown))
}

// knownView is a file of the mirror as the workspace shows it.
type knownView struct {
	Path    string          `json:"path"`
	Section string          `json:"section"`
	Title   string          `json:"title"`
	Lines   []knownLineView `json:"lines"`
}

type knownLineView struct {
	ID       int64  `json:"id"`
	Group    string `json:"group,omitempty"`
	When     string `json:"when,omitempty"`
	Text     string `json:"text"`
	External bool   `json:"external,omitempty"`
}

// handleKnown is GET /api/known: the mirror's files in order, with their
// titles in the request's language; none when the memory is off.
func (w *web) handleKnown(rw http.ResponseWriter, r *http.Request, _ *session) {
	out := []knownView{}
	if w.c.mem != nil {
		xs, err := w.c.mem.knownMemories()
		if err != nil {
			writeJSON(rw, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		lang := w.lang(r)
		for _, f := range knownFiles(xs) {
			v := knownView{Path: f.Path, Section: string(f.Section), Title: f.heading(lang), Lines: []knownLineView{}}
			for _, l := range f.Lines {
				v.Lines = append(v.Lines, knownLineView{ID: l.ID, Group: l.Group, When: l.When, Text: l.Text, External: l.External})
			}
			out = append(out, v)
		}
	}
	rw.Header().Set("Cache-Control", "no-store")
	writeJSON(rw, http.StatusOK, map[string]any{"files": out})
}
