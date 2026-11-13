package main

import (
	"fmt"
	"log"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"
)

// Settings → Surfaces (surfaces.go, workspace_tools.go): what the
// workspace holds, who wrote each surface, what it reads, how long it
// stays, when it last changed and the last error found on it; the user
// removes any.

func (w *web) surfaceRoutes(mux *webMux) {
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
	use := useWords(w.c.surfaceUseSince(now.Add(-surfaceUseWindow), now), lang)
	w.render(rw, r, s, "settings", "", page{Title: "settings.surfaces", Nav: "settings", Tab: "surfaces",
		Data: surfacesPage{Rows: rows, Use: use}})
}

// surfacesPage is what Settings → Surfaces shows: the surfaces, and how
// those put in front fared (workspace_use.go).
type surfacesPage struct {
	Rows []surfaceRow
	Use  string
}

// useWords says the measure of use in the user's language.
func useWords(u surfaceUse, lang string) string {
	if u.Shown == 0 {
		return tr(lang, "surfaces.use.none")
	}
	out := fmt.Sprintf(tr(lang, "surfaces.use"), u.Shown, u.Opened, u.Ignored, int(math.Round(100*u.IgnoredShare())), u.Gone, u.Front)
	if u.Opened > 0 {
		out += " " + fmt.Sprintf(tr(lang, "surfaces.use.open"), useSpan(u.ToOpen, lang))
	}
	return out
}

// useSpan words a wait: 40 s, 12 min, 3 h 05, 2 j 4 h.
func useSpan(d time.Duration, lang string) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%d s", int(d.Seconds()))
	case d < 24*time.Hour:
		return span(d)
	}
	h := int(d.Hours())
	return fmt.Sprintf("%d %s %d h", h/24, tr(lang, "surfaces.use.day"), h%24)
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
