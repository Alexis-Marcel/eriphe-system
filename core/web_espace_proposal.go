package main

import (
	"net/http"
	"time"
)

// A proposal shown in a surface of the workspace (the catalog's Proposal
// component): the page names it by its own ref, kind:id, and reads here
// what its card says and which card answers it; Oui and Non then go to the
// card, POST /cards/{id}/yes|no, as in the conversation. A Day component
// reads here the dated proposals of its day, which it shows as ghosts
// (surface_proposals.go).

// proposalView is a proposal as the workspace's Proposal component shows it.
type proposalView struct {
	Ref     string      `json:"ref"`
	Card    int64       `json:"card"`  // the card that answers it
	Kind    string      `json:"kind"`  // what it is: memory and intention only ask to keep something
	State   string      `json:"state"` // pending, approved, rejected, gone
	Text    string      `json:"text"`
	Why     string      `json:"why,omitempty"`
	Tainted bool        `json:"tainted,omitempty"`
	Fields  []cardField `json:"fields,omitempty"`
	When    []spanView  `json:"when,omitempty"` // when it takes place, if it has a time
	Created time.Time   `json:"created"`
}

// view is a card and its proposal as the workspace shows them, in lang.
func (w *web) proposalView(x convoCard, p proposal, lang string) proposalView {
	v := proposalView{Ref: x.Ref, Card: x.ID, Kind: x.Kind, State: x.State, Text: x.Text, Why: p.Why, Tainted: p.Tainted != "", Created: x.Created}
	if t := p.Text(lang); t != "" {
		v.Text = t
	}
	v.Fields = p.FieldsIn(lang)
	if x.State == cardPending {
		for _, s := range w.c.proposalSpans(p.Kind, p.ID, w.c.userZone()) {
			v.When = append(v.When, s.view())
		}
	}
	return v
}

// handleProposal is GET /api/proposals/{ref}: the proposal's card, in the
// request's language; 404 for a ref no card carries.
func (w *web) handleProposal(rw http.ResponseWriter, r *http.Request, _ *session) {
	ref := r.PathValue("ref")
	if _, _, ok := parseProposalRef(ref); !ok {
		writeJSON(rw, http.StatusNotFound, map[string]string{"error": "no such proposal"})
		return
	}
	xs, err := w.c.cardsWhere(`ref = ?`, ref)
	if err != nil || len(xs) == 0 {
		writeJSON(rw, http.StatusNotFound, map[string]string{"error": "no such proposal"})
		return
	}
	x, p := w.c.liveCard(xs[0])
	rw.Header().Set("Cache-Control", "no-store")
	writeJSON(rw, http.StatusOK, w.proposalView(x, p, w.lang(r)))
}

// dayProposals is the answer of GET /api/proposals?day=.
type dayProposals struct {
	Day       string         `json:"day"`
	Proposals []proposalView `json:"proposals"`
}

// handleDayProposals is GET /api/proposals?day=YYYY-MM-DD: the proposals
// that wait and take place that day, in the user's zone, with their times
// (when); 400 without a day.
func (w *web) handleDayProposals(rw http.ResponseWriter, r *http.Request, _ *session) {
	loc := w.c.userZone()
	from, err := time.ParseInLocation(time.DateOnly, r.URL.Query().Get("day"), loc)
	if err != nil {
		writeJSON(rw, http.StatusBadRequest, map[string]string{"error": "day is YYYY-MM-DD"})
		return
	}
	lang := w.lang(r)
	out := dayProposals{Day: from.Format(time.DateOnly), Proposals: []proposalView{}}
	for _, wc := range w.c.waitingCards() {
		var on []spanView
		for _, s := range w.c.proposalSpans(wc.p.Kind, wc.p.ID, loc) {
			if s.on(from) {
				on = append(on, s.view())
			}
		}
		if len(on) == 0 {
			continue
		}
		v := w.proposalView(wc.card, wc.p, lang)
		v.When = on
		out.Proposals = append(out.Proposals, v)
	}
	rw.Header().Set("Cache-Control", "no-store")
	writeJSON(rw, http.StatusOK, out)
}
