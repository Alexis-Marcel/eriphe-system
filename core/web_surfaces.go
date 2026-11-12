package main

import (
	"fmt"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"
)

// Settings → Surfaces (surfaces.go, workspace_tools.go): what the
// workspace holds, who wrote each surface, what it reads, how long it
// stays, when it last changed and the last error found on it; the user
// removes any.

func (w *web) surfaceRoutes(mux *http.ServeMux) {
	mux.Handle("GET /settings/surfaces", w.auth(w.handleSurfaces))
	mux.Handle("POST /settings/surfaces/{id}/remove", w.auth(w.handleRemoveSurface))
}

// surfaceRow is a surface as the page lists it.
type surfaceRow struct {
	ID, Title, Author, Lifetime, Updated, Error string
	Sources                                     []string
	Waiting, Tainted                            bool
}

// authorWords is who wrote a surface, in the user's words.
func authorWords(author, lang string) string {
	kind, id, _ := strings.Cut(author, ":")
	switch kind {
	case surfaceAuthorBrain:
		return tr(lang, "surfaces.by.brain")
	case "skill":
		return fmt.Sprintf(tr(lang, "surfaces.by.skill"), id)
	case "lead":
		return fmt.Sprintf(tr(lang, "surfaces.by.lead"), id)
	case "task":
		return fmt.Sprintf(tr(lang, "surfaces.by.task"), id)
	}
	return author
}

func lifetimeWords(l surfaceLifetime, lang string, loc *time.Location) string {
	switch {
	case !l.Until.IsZero():
		return fmt.Sprintf(tr(lang, "surfaces.life.until"), l.Until.In(loc).Format("2006-01-02 15:04"))
	case l.While != "":
		return fmt.Sprintf(tr(lang, "surfaces.life.while"), l.While)
	case l.Kept:
		return tr(lang, "surfaces.life.kept")
	}
	return ""
}

func (w *web) handleSurfaces(rw http.ResponseWriter, r *http.Request, s *session) {
	lang, now := w.lang(r), w.c.notifyTime()
	all, err := w.c.surfaces()
	if err != nil {
		http.Error(rw, err.Error(), http.StatusInternalServerError)
		return
	}
	var rows []surfaceRow
	for _, x := range all {
		row := surfaceRow{ID: x.ID, Title: x.Title, Author: authorWords(x.Author, lang), Lifetime: lifetimeWords(x.Lifetime, lang, now.Location()),
			Updated: x.Updated.In(now.Location()).Format("2006-01-02 15:04"), Error: lastErrorText(x), Waiting: len(surfaceWaiting(x)) > 0,
			Tainted: x.Tainted != ""}
		if row.Error == "" {
			if f := surfaceFailing(x); len(f) > 0 {
				var parts []string
				for ptr, why := range f {
					parts = append(parts, ptr+" : "+why)
				}
				sort.Strings(parts)
				row.Error = strings.Join(parts, " · ")
			}
		}
		for _, src := range x.Sources {
			row.Sources = append(row.Sources, src.String())
		}
		sort.Strings(row.Sources)
		rows = append(rows, row)
	}
	w.render(rw, r, s, "settings", "", page{Title: "settings.surfaces", Nav: "settings", Tab: "surfaces", Data: rows})
}

func (w *web) handleRemoveSurface(rw http.ResponseWriter, r *http.Request, s *session) {
	id := r.PathValue("id")
	if err := w.c.surfaceRemove(id, surfaceByUser); err != nil && err != errNoSurface {
		w.toast(rw, err.Error(), false)
		return
	}
	log.Printf("web: surface %s removed", id)
	rw.Header().Set("HX-Redirect", "/settings/surfaces")
	rw.WriteHeader(http.StatusOK)
}
