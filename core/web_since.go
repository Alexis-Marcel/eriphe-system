package main

import (
	"net/http"
	"strings"
	"time"
)

// « Ce qui s'est passé »: the feed the brain reads with since (since.go),
// for the user, by kind, since they were last here by default. It is the
// user's own page: external items are shown, marked.

const happenedPageMax = 200

type happenedItem struct {
	At       time.Time
	Kind     string
	Summary  string
	External bool
	Link     string
}

type happenedPage struct {
	Items    []happenedItem
	Kind     string // "" for all
	Range    string // last, day or week
	From     time.Time
	FromLast bool // since the user left
	NoLast   bool // they were not away: the last 24 hours instead
	More     bool
	Max      int
}

var (
	happenedKinds  = []string{feedTask, feedNotification, feedIntention, feedMessage, feedWatch}
	happenedRanges = []string{"last", "day", "week"}
)

func (w *web) handleHappened(rw http.ResponseWriter, r *http.Request, s *session) {
	q := r.URL.Query()
	v := happenedPage{Kind: q.Get("kind"), Range: q.Get("range"), Max: happenedPageMax}
	if !containsStr(happenedKinds, v.Kind) {
		v.Kind = ""
	}
	if !containsStr(happenedRanges, v.Range) {
		v.Range = "last"
	}
	now := w.c.notifyTime()
	switch v.Range {
	case "week":
		v.From = now.AddDate(0, 0, -7)
	case "day":
		v.From = now.Add(-24 * time.Hour)
	default:
		if a, ok := w.c.lastAbsence(now); ok {
			v.From, v.FromLast = a.Left, true
		} else {
			v.From, v.NoLast = now.Add(-24*time.Hour), true
		}
	}
	var kinds []string
	if v.Kind != "" {
		kinds = []string{v.Kind}
	}
	items, more, err := w.c.feed(v.From, kinds, happenedPageMax)
	if err != nil {
		http.Error(rw, err.Error(), http.StatusInternalServerError)
		return
	}
	lang := w.lang(r)
	v.More = more
	for _, h := range items {
		v.Items = append(v.Items, happenedItem{At: h.At, Kind: h.Kind, Summary: h.summary(lang, true),
			External: h.Trust == feedExternal, Link: happenedLink(h.Ref)})
	}
	w.render(rw, r, s, "settings", "", page{Title: "settings.happened", Nav: "settings", Tab: "happened", Data: v})
}

// happenedLink is where an item is seen in full in the workspace.
func happenedLink(ref string) string {
	kind, id, _ := strings.Cut(ref, ":")
	switch kind {
	case "task":
		return "/tasks/" + id
	case "notification":
		return "/settings/notifications?n=" + id + "#notif-" + id
	case "intention":
		return "/memory?view=commitments"
	case "watch":
		return "/settings/watches#watch-" + id
	}
	return ""
}
