package main

import (
	"net/http"
)

// The first-run wizard, after the owner's account: name the assistant,
// connect the AI account it thinks with (the provider form of Settings →
// Agents, its usual agents created with it), then wait for a machine and
// the brain to start.
// The conversation leads here while the brain has no agent, until the
// owner skips it.

const (
	welcomeSkipped = "welcome_skipped"
	welcomeNamed   = "welcome_named" // the name step is done
)

func (w *web) welcomeRoutes(mux *http.ServeMux) {
	mux.Handle("GET /welcome", w.auth(w.handleWelcome))
	mux.Handle("GET /welcome/status", w.auth(w.handleWelcomeStatus))
	mux.Handle("POST /welcome/name", w.auth(w.handleWelcomeName))
	mux.Handle("POST /welcome/skip", w.auth(w.handleWelcomeSkip))
}

// needsWelcome tells whether the conversation should lead to the wizard:
// a brain without an agent, and a wizard not skipped.
func (w *web) needsWelcome() bool {
	b := w.c.conf().brain()
	return b != nil && b.Agent == "" && w.c.store.get(welcomeSkipped) == ""
}

type welcomeView struct {
	Naming    bool   // the first step: what to call the assistant
	Name      string // its name, or the one typed
	NameError string
	Agent     string         // the brain's agent, once there is one
	Model     string         // its model, as the agents page names it
	Form      *providerForm  // the account form, once its kind is picked
	Kinds     []providerKind // the kinds to pick from, while there is no agent
	Machines  int            // online
	Devices   int            // registered: an install with its core has its own
	Brain     string         // brainState: "" or thinking once it runs
	Ready     bool
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
			v.Devices++
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
	if v.Agent == "" && w.c.store.get(welcomeNamed) == "" {
		v.Naming = true // a new install chooses: the field starts empty
	} else if v.Agent == "" {
		// Every kind of account, as equal choices, none picked: one picked
		// (?kind=) shows its form.
		v.Kinds = providerKinds
		if k, ok := kindOf(r.URL.Query().Get("kind")); ok {
			m := w.c.conf()
			f := providerForm{Kind: k, Next: "welcome", AllKinds: providerKinds,
				Name: defaultProviderName(k.ID, func(n string) bool { _, ok := m.Providers[n]; return ok })}
			f.Presets = w.presetChoices(k, f.Name, nil)
			v.Form = &f
		}
	}
	w.render(rw, r, s, "welcome", "", page{Title: "welcome.title", Tab: "agents", Data: v})
}

// handleWelcomeName names the assistant, then goes on to its AI account.
func (w *web) handleWelcomeName(rw http.ResponseWriter, r *http.Request, s *session) {
	b := w.c.conf().brain()
	if b == nil {
		http.Redirect(rw, r, "/", http.StatusSeeOther)
		return
	}
	name := normalizeDisplayName(r.PostFormValue("name"))
	problem := ""
	if checkDisplayName(name) != nil {
		problem = tr(w.lang(r), "welcome.namebad")
	} else if err := w.c.editAssistant(b.Name, assistantEdit{DisplayName: name, SetName: true}); err != nil {
		problem = w.formError(r, err)
	}
	if problem != "" {
		v := w.welcomeData(r)
		v.Naming, v.Name, v.NameError = true, name, problem
		w.render(rw, r, s, "welcome", "", page{Title: "welcome.title", Tab: "agents", Data: v})
		return
	}
	w.c.ping() // a brain already running restarts with its name
	w.c.store.set(welcomeNamed, "1")
	http.Redirect(rw, r, "/welcome", http.StatusSeeOther)
}

// handleWelcomeStatus is the wizard's last step, polled until the brain runs.
func (w *web) handleWelcomeStatus(rw http.ResponseWriter, r *http.Request, s *session) {
	w.render(rw, r, s, "welcome", "welcome-status", page{Data: w.welcomeData(r)})
}

func (w *web) handleWelcomeSkip(rw http.ResponseWriter, r *http.Request, s *session) {
	w.c.store.set(welcomeSkipped, "1")
	redirect(rw, r, "/")
}
