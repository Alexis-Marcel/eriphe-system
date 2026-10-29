package main

import (
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"time"
)

// Settings → Agents → Assistants: the long-lived assistants, the brain
// first, with their agent, their tools and how they are doing. The
// workspace sets what the file leaves out of them; new ones come through
// the API (assistants.go).

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
}

func (w *web) assistantCards(r *http.Request, v agentsPage) []assistantCard {
	lang := w.lang(r)
	m := w.c.conf()
	var out []assistantCard
	for _, av := range w.c.assistantViews() {
		sets := w.c.fileSets(av.Name)
		card := assistantCard{assistantView: av, RoleLabel: av.Spec.Role, FreeReads: assistantRoles[av.Spec.Role].FreeReads,
			AgentLocked: sets["agent"], NameLocked: sets["display_name"], ToolsLocked: sets["tools"], ToolsText: strings.Join(av.Spec.Tools, ", "),
			DisplayName: av.Spec.displayName()}
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
	// The brain first.
	for i, c := range out {
		if c.Spec.has(triggerConversation) && i > 0 {
			out[0], out[i] = out[i], out[0]
		}
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

func (w *web) handleSaveAssistant(rw http.ResponseWriter, r *http.Request, s *session) {
	r.ParseForm()
	var tools []string
	if r.PostForm.Has("tools") {
		tools = splitTools(r.PostFormValue("tools"))
	}
	name := r.PathValue("name")
	e := assistantEdit{Agent: r.PostFormValue("agent"), Tools: tools, SetTools: r.PostForm.Has("tools"),
		DisplayName: r.PostFormValue("display_name"), SetName: r.PostForm.Has("display_name")}
	if err := w.c.editAssistant(name, e); err != nil {
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
