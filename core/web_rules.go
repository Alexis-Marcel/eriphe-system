package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
)

// The user's rules (attention_rules.go): made from the composed
// workspace's ⋯ menu (/api/rules, JSON) and listed and removed in the
// settings (Settings → Règles d'affichage, /settings/display), where the
// rules the brain proposed are answered as on their card.

func (w *web) ruleRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/rules", w.auth(w.handleRules))
	mux.Handle("POST /api/rules", http.MaxBytesHandler(w.auth(w.handleAddRule), 16<<10))
	mux.Handle("DELETE /api/rules/{id}", w.auth(w.handleDeleteRule))
	mux.Handle("GET /settings/display", w.auth(w.handleDisplay))
	mux.Handle("POST /settings/display/{id}/{action}", w.auth(w.handleDisplayAction))
}

// rulesOut is GET /api/rules: the rules in force, the latest first.
type rulesOut struct {
	Rules []attentionRule `json:"rules"`
}

func (w *web) handleRules(rw http.ResponseWriter, r *http.Request, _ *session) {
	rw.Header().Set("Cache-Control", "no-store")
	writeJSON(rw, http.StatusOK, rulesOut{Rules: w.c.attentionRules()})
}

// handleAddRule is POST /api/rules: a rule the user made, in force at once
// (their own gesture: no card).
func (w *web) handleAddRule(rw http.ResponseWriter, r *http.Request, _ *session) {
	var in showRuleIn
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSON(rw, http.StatusBadRequest, objectError{`the body is {"key", "delta", "label", "remark"}`, http.StatusBadRequest})
		return
	}
	rule, err := w.c.addRule(in)
	if err != nil {
		writeJSON(rw, http.StatusBadRequest, objectError{err.Error(), http.StatusBadRequest})
		return
	}
	w.notify()
	writeJSON(rw, http.StatusCreated, rule)
}

func (w *web) handleDeleteRule(rw http.ResponseWriter, r *http.Request, _ *session) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	switch err := w.c.removeRule(id); {
	case errors.Is(err, errNoRule):
		writeJSON(rw, http.StatusNotFound, objectError{err.Error(), http.StatusNotFound})
	case err != nil:
		writeJSON(rw, http.StatusInternalServerError, objectError{err.Error(), http.StatusInternalServerError})
	default:
		w.notify()
		rw.WriteHeader(http.StatusNoContent)
	}
}

// displayRuleView is a rule as the settings show it.
type displayRuleView struct {
	ID      int64
	Line    string // what it does, in words
	Key     string
	Remark  string
	Why     string
	Tainted string
	At      string
}

type displayPage struct {
	Rules    []displayRuleView
	Proposed []displayRuleView
}

func (w *web) displayPage(lang string) displayPage {
	view := func(r attentionRule) displayRuleView {
		return displayRuleView{ID: r.ID, Line: ruleLine(r, lang), Key: r.Key, Remark: r.Remark, Why: r.why, Tainted: r.tainted,
			At: r.At.In(w.c.notifyZone()).Format("2006-01-02")}
	}
	var d displayPage
	for _, r := range w.c.attentionRules() {
		d.Rules = append(d.Rules, view(r))
	}
	for _, id := range w.c.proposedRules() {
		if r, err := w.c.attentionRule(id); err == nil {
			d.Proposed = append(d.Proposed, view(r))
		}
	}
	return d
}

func (w *web) handleDisplay(rw http.ResponseWriter, r *http.Request, s *session) {
	w.render(rw, r, s, "settings", "", page{Title: "display.title", Nav: "settings", Tab: "display", Data: w.displayPage(w.lang(r))})
}

// handleDisplayAction removes a rule, or answers a proposed one.
func (w *web) handleDisplayAction(rw http.ResponseWriter, r *http.Request, s *session) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	var err error
	var msg string
	switch r.PathValue("action") {
	case "remove":
		err, msg = w.c.removeRule(id), "display.removed"
	case "approve", "reject":
		yes := r.PathValue("action") == "approve"
		err, msg = w.c.decideProposal(r.Context(), cardShowRule, id, yes), "display.rejected"
		if yes {
			msg = "display.approved"
		}
	default:
		http.NotFound(rw, r)
		return
	}
	lang := w.lang(r)
	if err != nil {
		rw.Header().Set("HX-Trigger", hxTrigger(tr(lang, "display.gone"), false, false))
	} else {
		rw.Header().Set("HX-Trigger", hxTrigger(tr(lang, msg), true, false))
		w.notify()
	}
	w.render(rw, r, s, "settings", "display-rules-frag", page{Data: w.displayPage(lang)})
}
