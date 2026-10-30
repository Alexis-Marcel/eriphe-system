package main

import (
	"errors"
	"fmt"
	"log"
	"maps"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"slices"
	"sort"
	"strings"
)

// Settings → Agents: the AI accounts (providers) and the agents that use
// them, the agents worth creating with an account, the default agent and
// the brain's. Someone who is not a developer adds an account by picking
// a card, follows its steps to get the credential, tests it, and gets the
// usual agents created with it.

func (w *web) agentRoutes(mux *http.ServeMux) {
	mux.Handle("GET /settings/agents", w.auth(w.handleAgents))
	mux.Handle("GET /settings/agents/new", w.auth(w.handleNewAgent))
	mux.Handle("POST /settings/agents", w.auth(w.handleSaveAgent))
	mux.Handle("POST /settings/agents/suggested", w.auth(w.handleSuggestedAgent))
	mux.Handle("GET /settings/agents/{name}/edit", w.auth(w.handleEditAgent))
	mux.Handle("POST /settings/agents/{name}", w.auth(w.handleSaveAgent))
	mux.Handle("POST /settings/agents/{name}/delete", w.auth(w.handleDeleteAgent))
	mux.Handle("POST /settings/default-agent", w.auth(w.handleDefaultAgent))
	mux.Handle("POST /settings/brain-agent", w.auth(w.handleBrainAgent))
	mux.Handle("POST /settings/assistants/{name}", w.auth(w.handleSaveAssistant)) // web_assistants.go
	mux.Handle("GET /settings/providers/new", w.auth(w.handleNewProvider))
	mux.Handle("POST /settings/providers", w.auth(w.handleSaveProvider))
	mux.Handle("POST /settings/provider-test", w.auth(w.handleTestProvider))
	mux.Handle("POST /settings/provider-test/{name}", w.auth(w.handleTestProvider))
	mux.Handle("GET /settings/providers/{name}/edit", w.auth(w.handleEditProvider))
	mux.Handle("POST /settings/providers/{name}", w.auth(w.handleSaveProvider))
	mux.Handle("POST /settings/providers/{name}/delete", w.auth(w.handleDeleteProvider))
	w.loginRoutes(mux)
}

type providerView struct {
	Name, Kind, Source string
	Billing            string
	Agents             []string
	InVault            bool
	Suggested          []suggestion // agents worth creating with it, not there yet
}

// suggestion is a preset agent offered for a provider.
type suggestion struct {
	Provider string
	Name     string // the name it would get
	Preset   string
	Model    string // its usual name
	Use      string // a text key
	Default  bool
}

type agentView struct {
	agentInfo
	Provider   string
	Kind       string
	Billing    string
	ModelLabel string
	Image      string
	Source     string
	Brain      bool
	Advanced   bool // it needs or prefers something of the machines
}

type agentsPage struct {
	Agents        []agentView
	Providers     []providerView
	Kinds         []providerKind
	Suggested     []suggestion
	Default       string
	DefaultSource string
	Brain         string
	BrainModel    string
	BrainSource   string          // config when the file names the brain's agent
	BrainChoices  []agentView     // the agents that can be the brain
	BrainCard     *assistantCard  // the brain, at the top; nil without one
	Helpers       []assistantCard // its assistants
	AgentCount    int             // the agents it may give a task to (list_agents)
	Flash         string
	Skipped       map[string]string
}

func (w *web) agentsData(r *http.Request) agentsPage {
	m := w.c.conf()
	v := agentsPage{Default: m.DefaultAgent, DefaultSource: sourceWorkspace, Skipped: map[string]string{}, Kinds: providerKinds}
	if w.c.base.DefaultAgent != "" {
		v.DefaultSource = sourceConfig
	}
	if b := m.brainEntry(); b != nil {
		v.Brain, v.BrainSource = b.Agent, w.c.brainAgentSource(b.Name)
	}
	infos, _ := w.c.listAgents()
	for _, a := range infos {
		spec := m.Agents[a.Name]
		kind := m.Providers[spec.Provider].Kind
		k, _ := kindOf(kind)
		av := agentView{agentInfo: a, Provider: spec.Provider, Kind: kind, Billing: k.Billing, Image: spec.Image,
			ModelLabel: modelLabel(kind, a.Runtime, a.Model), Source: w.c.source(kindAgent, a.Name), Brain: a.Name == v.Brain,
			Advanced: len(a.Needs) > 0 || len(a.Prefers) > 0 || spec.Image != "" || a.Runtime != k.Runtime()}
		if av.Brain {
			v.BrainModel = av.ModelLabel
		}
		if harnessOf(a.Runtime).canBeBrain() {
			v.BrainChoices = append(v.BrainChoices, av)
		}
		v.Agents = append(v.Agents, av)
	}
	// Agents first by what they are for: the brain's, the default, then by name.
	sort.SliceStable(v.Agents, func(i, j int) bool {
		rank := func(a agentView) int {
			switch {
			case a.Brain:
				return 0
			case a.Default:
				return 1
			}
			return 2
		}
		return rank(v.Agents[i]) < rank(v.Agents[j])
	})
	for _, n := range slices.Sorted(maps.Keys(m.Providers)) {
		p := m.Providers[n]
		k, _ := kindOf(p.Kind)
		pv := providerView{Name: n, Kind: p.Kind, Billing: k.Billing, Source: w.c.source(kindProvider, n), InVault: strings.HasPrefix(p.Secret, vaultPrefix)}
		for an, a := range m.Agents {
			if a.Provider == n {
				pv.Agents = append(pv.Agents, an)
			}
		}
		sort.Strings(pv.Agents)
		pv.Suggested = suggestionsFor(m, n)
		v.Suggested = append(v.Suggested, pv.Suggested...)
		v.Providers = append(v.Providers, pv)
	}
	for k, why := range w.c.skippedEntries() {
		if !strings.HasPrefix(k, kindServer+"/") {
			v.Skipped[k] = why
		}
	}
	lang := w.lang(r)
	q := r.URL.Query()
	switch {
	case q.Get("saved") != "" && (w.c.source(kindAgent, q.Get("saved")) != "" || w.c.source(kindProvider, q.Get("saved")) != ""):
		v.Flash = fmt.Sprintf(tr(lang, "agents.saved"), q.Get("saved"))
	case q.Get("created") != "" && w.c.source(kindProvider, q.Get("created")) != "":
		n := len(m.agentsOf(q.Get("created")))
		v.Flash = fmt.Sprintf(tr(lang, "providers.created"), q.Get("created"))
		if n > 0 {
			v.Flash += " " + fmt.Sprintf(tr(lang, "providers.createdagents"), n)
		}
	case q.Get("removed") != "" && serverName.MatchString(q.Get("removed")):
		v.Flash = fmt.Sprintf(tr(lang, "agents.removed"), q.Get("removed"))
	case q.Get("default") != "":
		v.Flash = fmt.Sprintf(tr(lang, "agents.defaultset"), m.DefaultAgent)
	case q.Get("brain") == "saved":
		v.Flash = tr(lang, "agents.brainsaved")
	case q.Get("brain") != "":
		v.Flash = fmt.Sprintf(tr(lang, "agents.brainset"), v.Brain)
	case q.Get("assistant") != "" && m.Assistants[q.Get("assistant")].Name != "":
		v.Flash = fmt.Sprintf(tr(lang, "assistants.saved"), q.Get("assistant"))
	}
	v.BrainCard, v.Helpers = w.brainCards(r, v)
	v.AgentCount = len(m.Agents)
	return v
}

func (m *Config) agentsOf(provider string) []string {
	var out []string
	for n, a := range m.Agents {
		if a.Provider == provider {
			out = append(out, n)
		}
	}
	return out
}

// suggestionsFor are the presets of a provider's kind that no agent of
// that provider runs yet (same runtime and model).
func suggestionsFor(m *Config, provider string) []suggestion {
	p, ok := m.Providers[provider]
	if !ok {
		return nil
	}
	k, _ := kindOf(p.Kind)
	var out []suggestion
	for _, ps := range k.Presets {
		have := false
		for _, a := range m.Agents {
			if a.Provider == provider && a.Model == ps.Model && a.Runtime == k.Runtime() {
				have = true
			}
		}
		if have {
			continue
		}
		name := presetName(ps, provider, func(n string) bool { _, ok := m.Agents[n]; return ok })
		out = append(out, suggestion{Provider: provider, Name: name, Preset: ps.Name, Model: modelLabel(p.Kind, k.Runtime(), ps.Model),
			Use: ps.Use, Default: ps.Default})
	}
	return out
}

// presetAgent is the agent a preset makes for a provider, its use in the
// user's language.
func presetAgent(lang string, k providerKind, provider string, ps agentPreset) Agent {
	return Agent{Runtime: k.Runtime(), Provider: provider, Model: ps.Model, Use: tr(lang, ps.Use)}
}

func (w *web) handleAgents(rw http.ResponseWriter, r *http.Request, s *session) {
	w.render(rw, r, s, "settings", "", page{Title: "settings.agents", Nav: "settings", Tab: "agents", Data: w.agentsData(r)})
}

// The agent form.

type agentForm struct {
	Edit       bool
	Name       string
	Provider   string
	Model      string // a choice's ID, "" for the runtime's default, or "other"
	AutoModel  bool   // the runtime picks a model when none is named
	ModelOther string
	Use        string
	Default    bool // make it the default agent
	IsDefault  bool // it is the default agent already
	CanDefault bool // the workspace picks the default agent
	Runtime    string
	Needs      string
	PrefCPU    bool
	PrefMem    bool
	Prefers    string // other preferences: node labels
	Image      string
	Command    string // an ACP agent's command, one argument per line
	Brain      bool
	Error      string
	refresh    bool // sent again after a change of provider or runtime

	Providers []providerView
	Kind      providerKind
	Runtimes  []string
	Models    []modelChoice
}

const modelOther = "other"

// complete fills what the form shows from the provider chosen: its kind,
// the runtimes it can run and the models worth offering.
func (w *web) complete(f *agentForm) {
	m := w.c.conf()
	f.Providers = w.agentsData(&http.Request{URL: &url.URL{}}).Providers
	if _, ok := m.Providers[f.Provider]; !ok && len(f.Providers) > 0 {
		f.Provider = f.Providers[0].Name
	}
	f.Kind, _ = kindOf(m.Providers[f.Provider].Kind)
	f.Runtimes = runtimesOf(f.Kind.ID)
	if !slices.Contains(f.Runtimes, f.Runtime) {
		f.Runtime = f.Kind.Runtime()
	}
	f.Models = f.Kind.Models[f.Runtime]
	f.AutoModel = harnessOf(f.Runtime).AutoModel
	known := f.Model == modelOther || f.Model == "" && f.AutoModel ||
		slices.ContainsFunc(f.Models, func(c modelChoice) bool { return c.ID == f.Model })
	if !known && f.Model != "" && !f.refresh {
		// A model the list does not offer, posted as is.
		f.Model, f.ModelOther = modelOther, f.Model
		known = true
	}
	if !known {
		f.Model = ""
		for _, p := range f.Kind.Presets {
			if p.Default && slices.ContainsFunc(f.Models, func(c modelChoice) bool { return c.ID == p.Model }) {
				f.Model = p.Model
			}
		}
		if f.Model == "" && len(f.Models) > 0 {
			f.Model = f.Models[0].ID
		}
		if f.Model == "" {
			f.Model = modelOther
		}
	}
	f.CanDefault = w.c.base.DefaultAgent == ""
}

func (w *web) agentFormPage(rw http.ResponseWriter, r *http.Request, s *session, f agentForm, block string) {
	w.complete(&f)
	w.render(rw, r, s, "settings", block, page{Title: "agents.add", Nav: "settings", Tab: "agents", View: "agent-form", Data: f})
}

// readAgentForm reads the form, posted or sent again to follow a change of
// provider (GET with refresh).
func readAgentForm(v url.Values) agentForm {
	g := func(k string) string { return strings.TrimSpace(v.Get(k)) }
	return agentForm{Name: strings.ToLower(g("name")), Runtime: g("runtime"), Provider: g("provider"), Model: g("model"),
		ModelOther: g("model_other"), Use: g("use"), Needs: g("needs"), Prefers: g("prefers"), Image: g("image"),
		Command: g("command"), PrefCPU: v.Get("prefer_cpu") != "", PrefMem: v.Get("prefer_memory") != "", Default: v.Get("default") != ""}
}

func (w *web) handleNewAgent(rw http.ResponseWriter, r *http.Request, s *session) {
	q := r.URL.Query()
	if q.Has("refresh") {
		f := readAgentForm(q)
		f.refresh = true
		w.agentFormPage(rw, r, s, f, "agent-form")
		return
	}
	f := agentForm{Provider: q.Get("provider")}
	m := w.c.conf()
	f.Default = len(m.Agents) == 0
	w.agentFormPage(rw, r, s, f, "")
}

func (w *web) handleEditAgent(rw http.ResponseWriter, r *http.Request, s *session) {
	name := r.PathValue("name")
	m := w.c.conf()
	a, ok := m.Agents[name]
	if !ok || w.c.source(kindAgent, name) != sourceWorkspace {
		http.Redirect(rw, r, "/settings/agents", http.StatusSeeOther)
		return
	}
	isBrain := m.brain() != nil && m.brain().Agent == name
	if q := r.URL.Query(); q.Has("refresh") {
		f := readAgentForm(q)
		f.Edit, f.Name, f.IsDefault, f.Brain, f.refresh = true, name, m.DefaultAgent == name, isBrain, true
		w.agentFormPage(rw, r, s, f, "agent-form")
		return
	}
	f := agentForm{Edit: true, Name: name, Runtime: a.Runtime, Provider: a.Provider, Model: a.Model, Use: a.Use,
		Needs: strings.Join(a.Needs, ", "), Image: a.Image, Command: strings.Join(a.Command, "\n"), IsDefault: m.DefaultAgent == name, Default: m.DefaultAgent == name, Brain: isBrain}
	k, _ := kindOf(m.Providers[a.Provider].Kind)
	if a.Model != "" && !slices.ContainsFunc(k.Models[a.Runtime], func(c modelChoice) bool { return c.ID == a.Model }) {
		f.Model, f.ModelOther = modelOther, a.Model
	}
	var other []string
	for _, p := range a.Prefers {
		switch p {
		case "cpu":
			f.PrefCPU = true
		case "memory":
			f.PrefMem = true
		default:
			other = append(other, p)
		}
	}
	f.Prefers = strings.Join(other, ", ")
	w.agentFormPage(rw, r, s, f, "")
}

// slug makes an agent's name out of a model: its last part, lowercase,
// letters, digits and dashes.
var notSlug = regexp.MustCompile(`[^a-z0-9]+`)

func slug(s string) string {
	if i := strings.LastIndex(s, "/"); i >= 0 {
		s = s[i+1:]
	}
	s = strings.Trim(notSlug.ReplaceAllString(strings.ToLower(s), "-"), "-")
	if len(s) > 32 {
		s = strings.TrimRight(s[:32], "-")
	}
	return s
}

func (w *web) handleSaveAgent(rw http.ResponseWriter, r *http.Request, s *session) {
	r.ParseForm()
	f := readAgentForm(r.PostForm)
	mode := putCreate
	if n := r.PathValue("name"); n != "" {
		mode, f.Name, f.Edit = putUpdate, n, true
		m := w.c.conf()
		f.IsDefault = m.DefaultAgent == n
		f.Brain = m.brain() != nil && m.brain().Agent == n
	}
	w.complete(&f)
	model := f.Model
	if model == modelOther {
		model = fullModel(f.Kind.ID, f.Runtime, f.ModelOther)
	}
	var command []string
	if f.Runtime == "acp" {
		// Its model is its own; its command is what names it.
		model, command = "", commandLines(f.Command)
		if len(command) > 0 {
			model = path.Base(command[0])
		}
	}
	if f.Name == "" && !f.Edit {
		f.Name = slug(model)
		if f.Name == "" {
			f.Name = slug(f.Runtime)
		}
		if _, taken := w.c.conf().Agents[f.Name]; taken {
			f.Name = slug(f.Name + "-" + f.Provider)
		}
	}
	if f.Runtime == "acp" {
		model = ""
	}
	a := Agent{Runtime: f.Runtime, Provider: f.Provider, Model: model, Use: f.Use, Needs: words(f.Needs), Image: f.Image, Command: command}
	if f.PrefCPU {
		a.Prefers = append(a.Prefers, "cpu")
	}
	if f.PrefMem {
		a.Prefers = append(a.Prefers, "memory")
	}
	a.Prefers = append(a.Prefers, words(f.Prefers)...)
	var err error
	switch {
	case len(f.Providers) == 0:
		err = errors.New("agents.noprovider")
	case a.Use == "":
		err = errors.New("form.nouse")
	case f.Runtime == "acp" && len(command) == 0:
		err = errors.New("agents.nocommand")
	case f.Runtime != "acp" && f.Model == modelOther && strings.TrimSpace(f.ModelOther) == "":
		err = errors.New("agents.nomodel")
	default:
		err = w.c.putAgent(mode, f.Name, a)
	}
	if err == nil && f.Default && f.CanDefault && !f.IsDefault {
		err = w.c.setDefaultAgent(f.Name)
	}
	if err != nil {
		f.Error = w.agentError(r, err)
		w.render(rw, r, s, "settings", "agent-form", page{Data: f})
		return
	}
	log.Printf("web: agent %s saved", f.Name)
	rw.Header().Set("HX-Redirect", "/settings/agents?saved="+url.QueryEscape(f.Name))
	rw.WriteHeader(http.StatusOK)
}

// commandLines reads an ACP agent's command: one argument per line, as
// is, blank lines left out. No quoting, no shell.
func commandLines(s string) []string {
	var out []string
	for _, l := range strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

// agentError words a refusal of the agent or provider forms.
func (w *web) agentError(r *http.Request, err error) string {
	var shown shownError
	if errors.As(err, &shown) {
		return string(shown)
	}
	if k := err.Error(); strings.HasPrefix(k, "agents.") || strings.HasPrefix(k, "providers.") || strings.HasPrefix(k, "cglogin.") {
		return tr(w.lang(r), k)
	}
	return w.formError(r, err)
}

// shownError is a refusal already worded for the user.
type shownError string

func (e shownError) Error() string { return string(e) }

// handleSuggestedAgent creates one of the agents suggested for a provider.
func (w *web) handleSuggestedAgent(rw http.ResponseWriter, r *http.Request, s *session) {
	provider, preset := r.PostFormValue("provider"), r.PostFormValue("preset")
	m := w.c.conf()
	p, ok := m.Providers[provider]
	k, _ := kindOf(p.Kind)
	i := slices.IndexFunc(k.Presets, func(ps agentPreset) bool { return ps.Name == preset })
	if !ok || i < 0 {
		w.toast(rw, tr(w.lang(r), "agents.nopreset"), false)
		return
	}
	ps := k.Presets[i]
	name := presetName(ps, provider, func(n string) bool { _, ok := m.Agents[n]; return ok })
	if err := w.c.putAgent(putCreate, name, presetAgent(w.lang(r), k, provider, ps)); err != nil {
		w.toast(rw, w.formError(r, err), false)
		return
	}
	log.Printf("web: agent %s created from a preset", name)
	rw.Header().Set("HX-Redirect", "/settings/agents?saved="+url.QueryEscape(name))
	rw.WriteHeader(http.StatusOK)
}

func (w *web) handleDeleteAgent(rw http.ResponseWriter, r *http.Request, s *session) {
	name := r.PathValue("name")
	if err := w.c.deleteAgent(name); err != nil {
		w.toast(rw, w.formError(r, err), false)
		return
	}
	log.Printf("web: agent %s removed", name)
	rw.Header().Set("HX-Redirect", "/settings/agents?removed="+url.QueryEscape(name))
	rw.WriteHeader(http.StatusOK)
}

func (w *web) handleDefaultAgent(rw http.ResponseWriter, r *http.Request, s *session) {
	if err := w.c.setDefaultAgent(r.PostFormValue("agent")); err != nil {
		w.toast(rw, w.formError(r, err), false)
		return
	}
	rw.Header().Set("HX-Redirect", "/settings/agents?default=1")
	rw.WriteHeader(http.StatusOK)
}

// handleBrainAgent saves the brain's block: its name, the agent it thinks
// with and its extra tools, as the form has them.
func (w *web) handleBrainAgent(rw http.ResponseWriter, r *http.Request, s *session) {
	e := editOf(r)
	b := w.c.conf().brainEntry()
	if b == nil {
		w.toast(rw, w.formError(r, w.c.setBrainAgent(e.Agent)), false) // no brain: says so
		return
	}
	if err := w.c.editAssistant(b.Name, e); err != nil {
		w.toast(rw, w.formError(r, err), false)
		return
	}
	w.c.ping()
	to := "/settings/agents?brain=saved"
	if e.Agent != "" && e.Agent != b.Agent {
		to = "/settings/agents?brain=1"
	}
	rw.Header().Set("HX-Redirect", to)
	rw.WriteHeader(http.StatusOK)
}

// The provider form: a gallery of kinds, then the kind's own form.

type presetChoice struct {
	Preset  string
	Name    string
	Model   string
	Use     string
	Default bool
	Checked bool
}

type providerForm struct {
	Edit     bool
	Name     string
	Kind     providerKind
	Env      string // the variable of its credential, when the provider names it
	KeySet   bool
	Presets  []presetChoice
	Login    *loginView // a ChatGPT sign-in going on or done (chatgpt.go)
	Agents   []string   // the agents using it, on an edit
	Error    string
	Gallery  bool
	AllKinds []providerKind
	Next     string // "welcome" in the first-run wizard (web_welcome.go), which it goes back to
}

func (w *web) providerFormPage(rw http.ResponseWriter, r *http.Request, s *session, f providerForm, block string) {
	f.AllKinds = providerKinds
	if !f.Edit && f.Kind.ID != "" {
		f.Presets = w.presetChoices(f.Kind, f.Name, f.Presets)
	}
	w.render(rw, r, s, "settings", block, page{Title: "providers.add", Nav: "settings", Tab: "agents", View: "provider-form", Data: f})
}

// presetChoices are the agents offered with a new provider, checked unless
// the form said otherwise.
func (w *web) presetChoices(k providerKind, provider string, posted []presetChoice) []presetChoice {
	m := w.c.conf()
	var out []presetChoice
	for _, ps := range k.Presets {
		c := presetChoice{Preset: ps.Name, Name: presetName(ps, provider, func(n string) bool { _, ok := m.Agents[n]; return ok }),
			Model: modelLabel(k.ID, k.Runtime(), ps.Model), Use: ps.Use, Default: ps.Default, Checked: true}
		if posted != nil {
			c.Checked = slices.ContainsFunc(posted, func(p presetChoice) bool { return p.Preset == ps.Name && p.Checked })
		}
		out = append(out, c)
	}
	return out
}

func (w *web) handleNewProvider(rw http.ResponseWriter, r *http.Request, s *session) {
	k, ok := kindOf(r.URL.Query().Get("kind"))
	if !ok {
		w.providerFormPage(rw, r, s, providerForm{Gallery: true}, "")
		return
	}
	m := w.c.conf()
	name := defaultProviderName(k.ID, func(n string) bool { _, ok := m.Providers[n]; return ok })
	w.providerFormPage(rw, r, s, providerForm{Kind: k, Name: name}, "")
}

func (w *web) handleEditProvider(rw http.ResponseWriter, r *http.Request, s *session) {
	name := r.PathValue("name")
	m := w.c.conf()
	p, ok := m.Providers[name]
	if !ok || w.c.source(kindProvider, name) != sourceWorkspace {
		http.Redirect(rw, r, "/settings/agents", http.StatusSeeOther)
		return
	}
	k, _ := kindOf(p.Kind)
	agents := m.agentsOf(name)
	sort.Strings(agents)
	w.providerFormPage(rw, r, s, providerForm{Edit: true, Name: name, Kind: k, Env: p.Env, KeySet: p.Secret != "", Agents: agents}, "")
}

func (w *web) handleSaveProvider(rw http.ResponseWriter, r *http.Request, s *session) {
	r.ParseForm()
	lang := w.lang(r)
	f := providerForm{Name: strings.ToLower(strings.TrimSpace(r.PostFormValue("name")))}
	f.Kind, _ = kindOf(r.PostFormValue("kind"))
	if r.PostFormValue("next") == "welcome" {
		f.Next = "welcome"
	}
	mode := putCreate
	p := Provider{Kind: f.Kind.ID}
	m := w.c.conf()
	if n := r.PathValue("name"); n != "" {
		mode, f.Name, f.Edit = putUpdate, n, true
		if old, ok := m.Providers[n]; ok && w.c.source(kindProvider, n) == sourceWorkspace {
			// A provider keeps its kind: its agents' models are the kind's.
			p.Kind, p.Secret, f.KeySet = old.Kind, old.Secret, old.Secret != ""
			f.Kind, _ = kindOf(old.Kind)
			p.Env = old.Env
		}
		f.Agents = m.agentsOf(n)
		sort.Strings(f.Agents)
	} else if f.Name == "" {
		f.Name = defaultProviderName(f.Kind.ID, func(n string) bool { _, ok := m.Providers[n]; return ok })
	}
	if f.Kind.CustomEnv {
		f.Env = strings.TrimSpace(r.PostFormValue("env"))
		p.Env = f.Env
	}
	checked := r.PostForm["preset"]
	for _, ps := range f.Kind.Presets {
		f.Presets = append(f.Presets, presetChoice{Preset: ps.Name, Checked: slices.Contains(checked, ps.Name)})
	}
	key := r.PostFormValue("key")
	var err error
	login := ""
	if f.Kind.ID == "chatgpt-subscription" {
		// The credential is the sign-in's, not a pasted key.
		login, key = r.PostFormValue("login"), ""
		f.Login = chatgptLogins.status(login, s.ID)
		if tok, ok := chatgptLogins.tokens(login, s.ID); ok {
			key = tok
		}
	}
	switch {
	case f.Kind.ID == "":
		err = errors.New("providers.nokind")
	case f.Kind.ID == "chatgpt-subscription" && p.Secret == "" && key == "":
		err = errors.New("cglogin.notyet")
	case f.Kind.ID != "chatgpt-subscription" && strings.TrimSpace(key) == "" && p.Secret == "":
		err = errors.New("form.nokey")
	case f.Kind.CustomEnv && checkCredentialEnv(f.Env) != nil:
		err = errors.New("providers.badenv")
	default:
		// A custom provider's key can be anyone's: its variable says whose.
		if other := misplaced(f.Kind.ID, strings.TrimSpace(key)); key != "" && login == "" && other != "" && !f.Kind.CustomEnv {
			err = shownError(trf(lang, "ptest.misplaced", tr(lang, "kind."+other)))
			break
		}
		agents := map[string]Agent{}
		def := ""
		if !f.Edit {
			taken := func(n string) bool { _, ok := m.Agents[n]; _, mine := agents[n]; return ok || mine }
			for _, ps := range f.Kind.Presets {
				if !slices.Contains(checked, ps.Name) {
					continue
				}
				n := presetName(ps, f.Name, taken)
				agents[n] = presetAgent(lang, f.Kind, f.Name, ps)
				if ps.Default {
					def = n
				}
			}
		}
		err = w.c.putProviderAgents(mode, f.Name, p, key, agents, def)
	}
	if err == nil && login != "" {
		chatgptLogins.drop(login)
	}
	if err != nil {
		f.Error = w.agentError(r, err)
		w.providerFormPage(rw, r, s, f, "provider-form")
		return
	}
	log.Printf("web: provider %s saved", f.Name)
	switch {
	case f.Next == "welcome" && !f.Edit:
		rw.Header().Set("HX-Redirect", "/welcome")
	case f.Edit:
		rw.Header().Set("HX-Redirect", "/settings/agents?saved="+url.QueryEscape(f.Name))
	default:
		rw.Header().Set("HX-Redirect", "/settings/agents?created="+url.QueryEscape(f.Name))
	}
	rw.WriteHeader(http.StatusOK)
}

func (w *web) handleDeleteProvider(rw http.ResponseWriter, r *http.Request, s *session) {
	name := r.PathValue("name")
	if err := w.c.deleteProvider(name); err != nil {
		w.toast(rw, w.formError(r, err), false)
		return
	}
	log.Printf("web: provider %s removed", name)
	rw.Header().Set("HX-Redirect", "/settings/agents?removed="+url.QueryEscape(name))
	rw.WriteHeader(http.StatusOK)
}

// handleTestProvider tries the key of the form (POST /settings/provider-test),
// or the stored one of a provider (POST /settings/provider-test/{name})
// when none is typed.
func (w *web) handleTestProvider(rw http.ResponseWriter, r *http.Request, s *session) {
	lang := w.lang(r)
	show := func(v testView) { w.render(rw, r, s, "settings", "tool-test", page{Data: v}) }
	if !w.testLimit.allow(s.ID, w.now()) {
		show(testView{State: "err", Text: tr(lang, "test.ratelimit")})
		return
	}
	kind, key := r.PostFormValue("kind"), r.PostFormValue("key")
	if n := r.PathValue("name"); n != "" {
		p, ok := w.c.conf().Providers[n]
		if !ok {
			show(testView{State: "err", Text: tr(lang, "ptest.unknown")})
			return
		}
		kind = p.Kind
		if strings.TrimSpace(key) == "" {
			if p.Kind == "chatgpt-subscription" {
				show(w.checkChatGPT(r, n))
				return
			}
			v, err := readSecret(p.Secret)
			if err != nil {
				show(testView{State: "err", Text: tr(lang, "ptest.nosecret")})
				return
			}
			key = v
		}
	}
	v := checkProvider(r.Context(), lang, kind, key)
	log.Printf("web: provider test (%s): %s", kind, v.State) // never the key
	show(v)
}

// The ChatGPT sign-in (chatgpt.go): POST starts one and shows its code, GET
// shows where it is, every few seconds until it is done.

func (w *web) loginRoutes(mux *http.ServeMux) {
	mux.Handle("POST /settings/chatgpt-login", w.auth(func(rw http.ResponseWriter, r *http.Request, s *session) {
		w.render(rw, r, s, "settings", "login-fragment", page{Data: chatgptLogins.start(s.ID)})
	}))
	mux.Handle("GET /settings/chatgpt-login/{id}", w.auth(func(rw http.ResponseWriter, r *http.Request, s *session) {
		w.render(rw, r, s, "settings", "login-fragment", page{Data: chatgptLogins.status(r.PathValue("id"), s.ID)})
	}))
}

// checkChatGPT tells whether a stored ChatGPT sign-in still works,
// refreshing it if it is about to end.
func (w *web) checkChatGPT(r *http.Request, name string) testView {
	lang := w.lang(r)
	p := w.c.conf().Providers[name]
	t, err := chatgptKeep.fresh(r.Context(), p.Secret, chatgptMinLeft)
	switch {
	case errors.Is(err, errSignedOut):
		return testView{State: "err", Text: tr(lang, "ptest.chatgptgone")}
	case err != nil:
		return testView{State: "err", Text: tr(lang, "ptest.chatgptgone"), Detail: truncate(err.Error(), 160)}
	}
	if plan := planName(t.identity().Plan); plan != "" {
		return testView{State: "ok", Text: fmt.Sprintf(tr(lang, "ptest.chatgptplan"), plan)}
	}
	return testView{State: "ok", Text: tr(lang, "ptest.chatgpt")}
}
