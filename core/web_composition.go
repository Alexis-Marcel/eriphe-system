package main

import (
	"net/http"
)

// The workspace reads its composition (composer.go) as JSON, and Settings →
// Agents turns the composer on or off and picks its agent.

// handleComposition is GET /api/composition: the last composition, at once.
func (w *web) handleComposition(rw http.ResponseWriter, r *http.Request, _ *session) {
	rw.Header().Set("Cache-Control", "no-store")
	writeJSON(rw, http.StatusOK, w.c.currentComposition())
}

// composerView is the composer's panel in Settings → Agents.
type composerView struct {
	On      bool
	Agent   string
	Plain   string      // how it is doing: available, replying, stopped or noai
	Choices []agentView // the agents that can run it, its own selected
	Last    *composition
}

func (w *web) composerData(v agentsPage) composerView {
	m := w.c.conf()
	cv := composerView{Plain: "stopped", Last: w.c.lastComposition()}
	a, ok := m.Assistants[composerAssistant]
	if ok {
		cv.On, cv.Agent = !a.Disabled, a.Agent
		cv.Plain = plainStatus(w.c.assistantView(a))
	}
	pick := cv.Agent
	if pick == "" {
		pick = composerDefaultAgent(m)
	}
	for _, c := range v.BrainChoices {
		c.Brain = c.Name == pick // selected
		cv.Choices = append(cv.Choices, c)
	}
	return cv
}

// handleComposer is POST /settings/composer: on or off, and its agent.
func (w *web) handleComposer(rw http.ResponseWriter, r *http.Request, _ *session) {
	r.ParseForm()
	if err := w.c.setComposer(r.PostFormValue("on") == "1", r.PostFormValue("agent")); err != nil {
		w.toast(rw, w.formError(r, err), false)
		return
	}
	rw.Header().Set("HX-Redirect", "/settings/agents?composer=1")
	rw.WriteHeader(http.StatusOK)
}
