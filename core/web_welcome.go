package main

import (
	"net/http"
)

// The first-run wizard, after the owner's account: connect the AI account
// the brain thinks with (the provider form of Settings → Agents, its usual
// agents created with it), then wait for a machine and the brain to start.
// The conversation leads here while the brain has no agent, until the
// owner skips it.

const welcomeSkipped = "welcome_skipped"

func (w *web) welcomeRoutes(mux *http.ServeMux) {
	mux.Handle("GET /welcome", w.auth(w.handleWelcome))
	mux.Handle("GET /welcome/status", w.auth(w.handleWelcomeStatus))
	mux.Handle("POST /welcome/skip", w.auth(w.handleWelcomeSkip))
}

// needsWelcome tells whether the conversation should lead to the wizard:
// a brain without an agent, and a wizard not skipped.
func (w *web) needsWelcome() bool {
	b := w.c.conf().brain()
	return b != nil && b.Agent == "" && w.c.store.get(welcomeSkipped) == ""
}

type welcomeView struct {
	Agent    string        // the brain's agent, once there is one
	Model    string        // its model, as the agents page names it
	Form     *providerForm // the account form, while there is no agent
	Kinds    []providerKind
	Machines int    // online
	Brain    string // brainState: "" or thinking once it runs
	Ready    bool
}

func (w *web) welcomeData(r *http.Request) welcomeView {
	m := w.c.conf()
	var v welcomeView
	if b := m.brain(); b != nil && b.Agent != "" {
		v.Agent = b.Agent
		a := m.Agents[v.Agent]
		v.Model = modelLabel(m.Providers[a.Provider].Kind, a.Runtime, a.Model)
	}
	if nodes, err := w.c.store.nodes(); err == nil {
		for _, n := range nodes {
			if n.Online {
				v.Machines++
			}
		}
	}
	v.Brain = w.brainState()
	v.Ready = v.Agent != "" && (v.Brain == "" || v.Brain == "thinking")
	return v
}

func (w *web) handleWelcome(rw http.ResponseWriter, r *http.Request, s *session) {
	if w.c.conf().brain() == nil {
		http.Redirect(rw, r, "/", http.StatusSeeOther)
		return
	}
	v := w.welcomeData(r)
	if v.Agent == "" {
		k, ok := kindOf(r.URL.Query().Get("kind"))
		if !ok {
			k, _ = kindOf("claude-subscription")
		}
		m := w.c.conf()
		f := providerForm{Kind: k, Next: "welcome", AllKinds: providerKinds,
			Name: defaultProviderName(k.ID, func(n string) bool { _, ok := m.Providers[n]; return ok })}
		f.Presets = w.presetChoices(k, f.Name, nil)
		v.Form = &f
		for _, o := range providerKinds {
			if o.ID != k.ID && o.ID != "custom-api" {
				v.Kinds = append(v.Kinds, o)
			}
		}
	}
	w.render(rw, r, s, "welcome", "", page{Title: "welcome.title", Tab: "agents", Data: v})
}

// handleWelcomeStatus is the wizard's last step, polled until the brain runs.
func (w *web) handleWelcomeStatus(rw http.ResponseWriter, r *http.Request, s *session) {
	w.render(rw, r, s, "welcome", "welcome-status", page{Data: w.welcomeData(r)})
}

func (w *web) handleWelcomeSkip(rw http.ResponseWriter, r *http.Request, s *session) {
	w.c.store.set(welcomeSkipped, "1")
	redirect(rw, r, "/")
}
