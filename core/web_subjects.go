package main

import (
	"net/http"
	"time"
)

// Settings → « Sujets » (subject_card.go): the known subjects, the latest
// seen first, each with its title and aliases; one picked shows its card
// exactly as the model gets it. The key goes in the query (?s=), never in
// the path: pr:owner/repo#12 holds a / and a #.

func (w *web) subjectsRoutes(mux *webMux) {
	mux.Handle("GET /settings/subjects", w.auth(w.handleSubjects))
}

// subjectsShown are the subjects the page lists, at most.
const subjectsShown = 200

type subjectsPage struct {
	Subjects []knownSubject
	More     int    // known but not listed
	Key      string // the one picked
	Card     string // its card, as the model gets it
	Stats    subjectCardStats
	Thread   int64 // its live thread, if any
	Unknown  bool  // picked, but not known
	// The cards pushed over cardUsesDays and how they were used, all and
	// the picked subject's (subject_card_use.go).
	Uses, KeyUses []cardUse
}

// cardUsesDays is the window of the counts the page shows.
const cardUsesDays = 30

func (w *web) handleSubjects(rw http.ResponseWriter, r *http.Request, s *session) {
	all, err := w.c.store.subjectsWhere(`merged_into = ''`)
	if err != nil {
		http.Error(rw, err.Error(), http.StatusInternalServerError)
		return
	}
	since := time.Now().AddDate(0, 0, -cardUsesDays)
	v := subjectsPage{Subjects: all, Uses: w.c.store.cardUses(since, "")}
	if len(all) > subjectsShown {
		v.Subjects, v.More = all[:subjectsShown], len(all)-subjectsShown
	}
	if q := r.URL.Query().Get("s"); q != "" {
		v.Key = w.c.store.canonSubject(q)
		if _, ok := w.c.store.knownSubject(v.Key); !ok {
			v.Unknown = true
		} else {
			v.Card, v.Stats = w.c.subjectCard(v.Key, w.c.cardBudget())
			v.Thread, _ = w.c.liveThreadOf(v.Key)
			v.KeyUses = w.c.store.cardUses(since, v.Key)
		}
	}
	w.render(rw, r, s, "settings", "", page{Title: "subjects.title", Nav: "settings", Tab: "subjects", Data: v})
}
