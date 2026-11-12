package main

import (
	"net/http"
	"time"
)

// A proposal shown in a surface of the workspace (the catalog's Proposal
// component): the page names it by its own ref, kind:id, and reads here
// what its card says and which card answers it; Oui and Non then go to the
// card, POST /cards/{id}/yes|no, as in the conversation.

// proposalView is a proposal as the workspace's Proposal component shows it.
type proposalView struct {
	Ref     string    `json:"ref"`
	Card    int64     `json:"card"`  // the card that answers it
	State   string    `json:"state"` // pending, approved, rejected, gone
	Text    string    `json:"text"`
	Why     string    `json:"why,omitempty"`
	Tainted bool      `json:"tainted,omitempty"`
	Fields  []Field   `json:"fields,omitempty"`
	Created time.Time `json:"created"`
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
	lang := w.lang(r)
	x, p := w.c.liveCard(xs[0])
	v := proposalView{Ref: ref, Card: x.ID, State: x.State, Text: x.Text, Why: p.Why, Tainted: p.Tainted != "", Created: x.Created}
	if t := p.Text(lang); t != "" {
		v.Text = t
	}
	for _, f := range p.FieldsIn(lang) {
		v.Fields = append(v.Fields, Field{f.Name, f.Value})
	}
	rw.Header().Set("Cache-Control", "no-store")
	writeJSON(rw, http.StatusOK, v)
}
