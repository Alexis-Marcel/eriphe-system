package main

import (
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
)

// Settings → « Ce qu'Eriphe a appris » (learning.go): the learning journal,
// newest first, by kind and status. Each entry says where it comes from,
// when, its status and what it changed, with « Annuler », which undoes it
// (itself an entry). An undo the brain proposed on a tainted turn is asked
// here too, beside its card in the conversation. A skill shows its name and
// version only; a memory, its text, which the scanner keeps free of secrets.

func (w *web) learnedRoutes(mux *http.ServeMux) {
	mux.Handle("GET /settings/learned", w.auth(w.handleLearned))
	mux.Handle("POST /settings/learned/{id}/undo", w.auth(w.handleLearnedUndo))
	mux.Handle("POST /settings/learned/{id}/keep", w.auth(w.handleLearnedKeep))
}

// learnedRow is an entry as the page shows it.
type learnedRow struct {
	*learnEntry
	CanUndo bool
	Version string // a skill's, short
	Links   []learnedLink
}

type learnedLink struct{ Ref, Href string }

type learnedPage struct {
	Rows     []learnedRow
	Kind     string
	Status   string
	Kinds    []string
	Statuses []string
	Off      bool // no memory
}

func (w *web) handleLearned(rw http.ResponseWriter, r *http.Request, s *session) {
	q := r.URL.Query()
	v := learnedPage{Kind: q.Get("kind"), Status: q.Get("status"), Kinds: learnKinds, Statuses: learnStatuses}
	if !containsStr(learnKinds, v.Kind) {
		v.Kind = ""
	}
	if !containsStr(learnStatuses, v.Status) {
		v.Status = ""
	}
	if m := w.c.mem; m == nil {
		v.Off = true
	} else {
		f := learnFilter{Limit: learnedMax}
		if v.Kind != "" {
			f.Kinds = []string{v.Kind}
		}
		if v.Status != "" {
			f.Statuses = []string{v.Status}
		}
		xs, err := m.learnings(f)
		if err != nil {
			http.Error(rw, err.Error(), http.StatusInternalServerError)
			return
		}
		for _, e := range xs {
			row := learnedRow{learnEntry: e, CanUndo: m.undoable(e) == ""}
			if e.Kind == kindSkill {
				row.Version = shortSHA(orStr(e.Version, e.PrevVersion))
			}
			for _, ref := range e.Evidence {
				row.Links = append(row.Links, learnedLink{Ref: ref, Href: learnedHref(ref)})
			}
			v.Rows = append(v.Rows, row)
		}
	}
	w.render(rw, r, s, "settings", "", page{Title: "learned.title", Nav: "settings", Tab: "learned", Data: v})
}

// learnedHref is where a piece of evidence is seen in the workspace.
func learnedHref(ref string) string {
	kind, id, _ := strings.Cut(ref, ":")
	switch kind {
	case "msg":
		return "/#m" + id
	case "learned":
		return "/settings/learned#learned-" + id
	}
	return happenedLink(ref) // web_activity.go
}

func (w *web) learnedID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return id, err == nil && id > 0 && w.c.mem != nil
}

func (w *web) handleLearnedUndo(rw http.ResponseWriter, r *http.Request, s *session) {
	id, ok := w.learnedID(r)
	if !ok {
		w.toast(rw, tr(w.lang(r), "err.internal"), false)
		return
	}
	e, err := w.c.unlearn(id, "owner", "", nil)
	if err != nil {
		log.Printf("web: learned %d: %v", id, err)
		w.toast(rw, tr(w.lang(r), "learned.cannot"), false)
		return
	}
	log.Printf("web: learned %d undone (learned %d)", id, e.ID)
	rw.Header().Set("HX-Refresh", "true")
	rw.WriteHeader(http.StatusOK)
}

// handleLearnedKeep declines an undo the brain proposed.
func (w *web) handleLearnedKeep(rw http.ResponseWriter, r *http.Request, s *session) {
	id, ok := w.learnedID(r)
	if !ok {
		w.toast(rw, tr(w.lang(r), "err.internal"), false)
		return
	}
	if e, err := w.c.mem.learning(id); err != nil || e.Undo != undoProposed {
		w.toast(rw, tr(w.lang(r), "err.internal"), false)
		return
	}
	if err := w.c.mem.setUndo(id, undoRejected, ""); err != nil {
		w.toast(rw, tr(w.lang(r), "err.internal"), false)
		return
	}
	rw.Header().Set("HX-Refresh", "true")
	rw.WriteHeader(http.StatusOK)
}

// learnedSummary is an entry in a line, in lang: for a card, and the brief.
func learnedSummary(e *learnEntry, lang string) string {
	if e.Kind == kindSkill {
		return fmt.Sprintf(tr(lang, "learned.skillline"), e.Skill, shortSHA(orStr(e.Version, e.PrevVersion)))
	}
	text := e.Text
	if e.Trust == trustExternal {
		text = tr(lang, "learned.external")
	}
	return tr(lang, "kind."+e.Kind) + " · " + truncate(oneLine(text), 200)
}
