package main

import (
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"time"
)

// Settings → Agents: the brain at the top, in plain words (its name, the
// agent it thinks with, how it is doing), then its own assistants, which
// report to it. The brain is an assistant underneath, never shown as one.
// The workspace sets what the file leaves out of them; new ones come
// through the API (assistants.go).

type assistantCard struct {
	assistantView
	RoleLabel   string
	AgentModel  string
	FreeReads   bool // its role adds the catalog's free reads to its tools
	AgentLocked bool // the file names its agent
	NameLocked  bool // the file names it
	DisplayName string
	ToolsLocked bool // the file sets its tools
	ToolsText   string
	LastTurn    time.Time
	Choices     []agentView // the agents that can run it
	Plain       string      // how it is doing, in plain words: available, replying, stopped or noai
	ReportsTo   string      // the display name of the one it reports to; the brain's helpers only
}

// plainStatus is how an assistant is doing, as the page says it: no AI
// chosen, stopped, replying or available.
func plainStatus(v assistantView) string {
	if v.Spec.Agent == "" {
		return "noai"
	}
	on := map[string]bool{}
	for _, c := range v.Status.Conditions {
		on[c.Type] = c.Status
	}
	switch {
	case v.Spec.Disabled || !on["Running"]:
		return "stopped"
	case on["Busy"]:
		return "replying"
	}
	return "available"
}

// brainCards are the brain's card, nil without a brain, and its helpers':
// the other assistants.
func (w *web) brainCards(r *http.Request, v agentsPage) (*assistantCard, []assistantCard) {
	var brain *assistantCard
	var helpers []assistantCard
	for _, c := range w.assistantCards(r, v) {
		if c.Spec.has(triggerConversation) {
			brain = &c
		} else {
			helpers = append(helpers, c)
		}
	}
	return brain, helpers
}

func (w *web) assistantCards(r *http.Request, v agentsPage) []assistantCard {
	lang := w.lang(r)
	m := w.c.conf()
	var out []assistantCard
	for _, av := range w.c.assistantViews() {
		sets := w.c.fileSets(av.Name)
		card := assistantCard{assistantView: av, RoleLabel: av.Spec.Role, FreeReads: assistantRoles[av.Spec.Role].FreeReads,
			AgentLocked: sets["agent"], NameLocked: sets["display_name"], ToolsLocked: sets["tools"], ToolsText: strings.Join(av.Spec.Tools, ", "),
			DisplayName: av.Spec.displayName(), Plain: plainStatus(av)}
		if to, ok := m.Assistants[m.reportsTo(av.Spec)]; ok {
			card.ReportsTo = to.displayName()
		}
		if l := tr(lang, "assistants.role."+av.Spec.Role); l != "assistants.role."+av.Spec.Role {
			card.RoleLabel = l
		}
		if a, ok := m.Agents[av.Spec.Agent]; ok {
			card.AgentModel = modelLabel(m.Providers[a.Provider].Kind, a.Runtime, a.Model)
		}
		if av.Status.LastTurn != nil {
			card.LastTurn = *av.Status.LastTurn
		}
		for _, c := range v.BrainChoices {
			c.Brain = c.Name == av.Spec.Agent // selected
			card.Choices = append(card.Choices, c)
		}
		out = append(out, card)
	}
	return out
}

// splitTools reads a grant typed in a field: commas, spaces or lines.
func splitTools(s string) []string {
	var out []string
	for _, f := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' || r == '\n' || r == '\r' || r == '\t' }) {
		out = append(out, f)
	}
	return out
}

// editOf is what an assistant's form sets: its agent, and its tools and
// name when the form has them.
func editOf(r *http.Request) assistantEdit {
	r.ParseForm()
	var tools []string
	if r.PostForm.Has("tools") {
		tools = splitTools(r.PostFormValue("tools"))
	}
	return assistantEdit{Agent: r.PostFormValue("agent"), Tools: tools, SetTools: r.PostForm.Has("tools"),
		DisplayName: r.PostFormValue("display_name"), SetName: r.PostForm.Has("display_name")}
}

func (w *web) handleSaveAssistant(rw http.ResponseWriter, r *http.Request, s *session) {
	name := r.PathValue("name")
	if err := w.c.editAssistant(name, editOf(r)); err != nil {
		w.toast(rw, w.formError(r, err), false)
		return
	}
	w.c.ping()
	rw.Header().Set("HX-Redirect", "/settings/agents?assistant="+name)
	rw.WriteHeader(http.StatusOK)
}

// assistantEdit is what Settings may change of an assistant: its agent,
// its tools if SetTools, its name if SetName.
type assistantEdit struct {
	Agent       string
	Tools       []string
	SetTools    bool
	DisplayName string
	SetName     bool
}

// editAssistant sets what the workspace may of an assistant; what the
// file sets stays the file's.
func (c *core) editAssistant(name string, e assistantEdit) error {
	agent, tools, setTools := e.Agent, e.Tools, e.SetTools
	c.catalogMu.Lock()
	defer c.catalogMu.Unlock()
	cur, ok := c.conf().Assistants[name]
	if !ok {
		return errNotFound(kindAssistant, name)
	}
	file, inBase := fileAssistants(c.base)[name]
	ws := c.dyn.Assistants[name]
	before := ws
	event := ""
	if agent != "" && agent != cur.Agent {
		if inBase && file.Agent != "" {
			return errReadOnly("assistant", name+".agent")
		}
		a, ok := c.conf().Agents[agent]
		if !ok {
			return errNotFound(kindAgent, agent)
		}
		if !harnessOf(a.Runtime).canBeBrain() {
			return &catalogError{http.StatusUnprocessableEntity, "catalog.notbrain", agent,
				fmt.Errorf("agent %s runs %s, an assistant needs a runtime that takes MCP servers and a system prompt", agent, a.Runtime)}
		}
		ws.Agent = agent
		if cur.has(triggerConversation) {
			event = fmt.Sprintf("Catalogue : %s réfléchit maintenant avec l'agent « %s » (%s).", cur.displayName(), agent, a.Runtime)
		}
	}
	if setTools {
		if inBase && len(file.Tools) > 0 {
			if !equalGrant(tools, file.Tools) {
				return errReadOnly("assistant", name+".tools")
			}
		} else {
			ws.Tools = tools
		}
	}
	if e.SetName {
		n := normalizeDisplayName(e.DisplayName)
		if n == cur.displayName() {
			n = ws.DisplayName // unchanged: an Eriphe left as it is stays the default
		}
		if inBase && file.DisplayName != "" {
			if n != file.DisplayName && n != "" {
				return errReadOnly("assistant", name+".display_name")
			}
		} else {
			if n != "" {
				if err := checkDisplayName(n); err != nil {
					return badDisplayName(n, err)
				}
			}
			ws.DisplayName = n
		}
	}
	if !inBase && ws.Role == "" {
		return errInvalid(errors.New("an assistant without a role"))
	}
	if reflect.DeepEqual(ws, before) {
		return nil // nothing changed: nothing to tell
	}
	return c.putAssistantLocked(name, ws, event)
}

func equalGrant(a, b []string) bool {
	return strings.Join(a, ",") == strings.Join(b, ",")
}
