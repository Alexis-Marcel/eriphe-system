package main

import (
	"net/http"
	"time"
)

// A proposal shown in a node's page (its cards): the page names it by its
// own ref, kind:id, and reads here what its card says and which card
// answers it; Oui and Non then go to the card, POST /cards/{id}/yes|no, as
// in the conversation.

// proposalView is a proposal as the workspace's Proposal component shows it.
type proposalView struct {
	Ref     string      `json:"ref"`
	Card    int64       `json:"card"`  // the card that answers it
	Kind    string      `json:"kind"`  // what it is: an intention only asks to keep something
	State   string      `json:"state"` // pending, approved, rejected, gone
	Text    string      `json:"text"`
	Context string      `json:"context,omitempty"` // where it acts, quietly beside its text (an account)
	Why     string      `json:"why,omitempty"`
	Tainted bool        `json:"tainted,omitempty"`
	Summary []string    `json:"summary,omitempty"` // what a yes makes, in plain sentences
	Fields  []cardField `json:"fields,omitempty"`
	Node    string      `json:"node,omitempty"`         // the node it belongs to (card_origin.go)
	From    string      `json:"from,omitempty"`         // the node and the task that ask, in words
	FromExt bool        `json:"fromExternal,omitempty"` // From quotes words that may be someone else's, marked
	Yes     string      `json:"yes,omitempty"`          // what Oui does, in words
	Exec    string      `json:"exec,omitempty"`         // approved, its call: running, done or failed (access_card.go)
	ExecWhy string      `json:"execWhy,omitempty"`      // failed: notkept, gone, notallowed or error
	// ExecError: failed, why in words: the tool's own for an error
	// (callError), external when its server is not trusted (shown marked).
	ExecError    string `json:"execError,omitempty"`
	ExecErrorExt bool   `json:"execErrorExternal,omitempty"`
	// A calls card of several calls: each one the user may leave out, and
	// what Oui says for 1, 2… items chosen (call_fold.go).
	Choices []proposalChoice `json:"choices,omitempty"`
	Acts    []string         `json:"acts,omitempty"`
	// Verb: what its yes button says, always a verb (card_verb.go).
	Verb    string    `json:"verb"`
	Created time.Time `json:"created"`
}

// view is a card and its proposal as the workspace shows them, in lang.
func (w *web) proposalView(x convoCard, p proposal, lang string) proposalView {
	v := proposalView{Ref: x.Ref, Card: x.ID, Kind: x.Kind, State: x.State, Text: x.Text, Why: p.Why, Tainted: p.Tainted != "", Created: x.Created}
	if t := p.Text(lang); t != "" {
		v.Text = t
	}
	v.Context = p.ContextIn(lang)
	v.Summary = p.SummaryIn(lang)
	v.Fields = p.FieldsIn(lang)
	o := p.Origin(lang)
	v.Node, v.From, v.Yes, v.FromExt = o.Node, o.From, o.Yes, o.External
	if p.choices != nil {
		v.Choices = p.choices(lang)
	}
	if p.acts != nil && x.State == cardPending {
		v.Acts = p.acts(lang)
	}
	v.Verb = p.VerbIn(lang)
	if x.State == cardApproved {
		var asks []accessQuestion
		switch p.Kind {
		case cardAccess:
			if a, err := w.c.accessAskOf(p.ID); err == nil {
				asks = []accessQuestion{a}
			}
		case cardCalls:
			asks = w.c.batchAsks(p.ID)
		}
		v.Exec, v.ExecWhy, v.ExecError, v.ExecErrorExt = w.c.asksExec(asks)
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
