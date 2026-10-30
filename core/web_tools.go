package main

import (
	"errors"
	"fmt"
	"log"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
)

// The tools of the workspace: the list, the gallery of presets and the
// form of a tool. A preset's form asks only what the user decides (the
// credential, the address that differs per person, the name, what it is
// for and what Eriphe may do without asking); the rest is under the
// advanced options. Mail and calendar first ask which kind of account.

type toolView struct {
	serverView
	Source  string
	Preset  toolPreset // the preset it comes from, if any
	Detail  string     // a line for a preset's tool: "Gmail · sam@gmail.com"
	Pending bool       // a local server whose service does not run yet
	Placed  bool       // … but a machine has it and is starting it
	Problem string     // why it does not start, in words; the raw error stays in Error
}

type toolsPage struct {
	Tools   []toolView
	Presets []toolPreset
	Flash   string
	Alert   string // a pending tool that was not connected
	Skipped map[string]string
	Secrets secretsView
	Waiting bool // a tool is starting: the list refreshes itself until it is up
	// Home Assistant talks to Eriphe (its voice token) but Eriphe has no
	// tool of it: its entry is still in the list (web_homeassistant.go).
	VoiceOnly bool
	Voice     bool // Home Assistant has a voice token
}

// presetOf finds what preset a catalog entry comes from, and the line that
// says which account it is.
func presetOf(lang string, s Server) (toolPreset, string) {
	if !s.local() {
		if p, ok := presetByURL(s.URL); ok {
			return p, tr(lang, "preset."+p.ID+".title")
		}
		if homeAssistantURL(s.URL) {
			p, _ := presetByID("home-assistant")
			u, _ := url.Parse(s.URL)
			return p, tr(lang, "preset.home-assistant.title") + " · " + u.Host
		}
		return toolPreset{}, ""
	}
	spec, err := decodeRun(s.Run)
	if err != nil {
		return toolPreset{}, ""
	}
	if m, ok := mailFormOf(spec); ok {
		p, _ := presetByID("mail")
		return p, tr(lang, "mailp."+m.Provider) + " · " + m.Address
	}
	if c, ok := calendarFormOf(spec); ok {
		p, _ := presetByID("calendar")
		return p, tr(lang, "calp."+c.Provider) + " · " + c.Username
	}
	if fc, ok := filesConfigOf(Server{Run: marshalRun(spec)}); ok {
		p, _ := presetByID("gdrive")
		return p, tr(lang, "preset.gdrive.title") + " · " + driveCount(lang, len(fc.Folders))
	}
	return toolPreset{}, ""
}

// startState says whether a local tool is not up yet, and if so whether a
// machine has it and is starting it, or none has taken it.
func startState(sv serverView) (pending, placed bool) {
	if sv.Kind != "local" || sv.Service == stateRunning || sv.Status == statusReady {
		return false, false
	}
	return true, sv.Node != "" && sv.Service != stateFailed
}

// serviceProblem says in words why a local tool does not start, from the
// error its machine reported; empty when it is only waiting or starting.
func serviceProblem(lang string, sv serverView) string {
	if sv.Service != stateFailed {
		return ""
	}
	e := strings.ToLower(sv.Error)
	for _, s := range []string{"unauthorized", "pull access denied", "manifest unknown", "unable to find image", "denied:"} {
		if strings.Contains(e, s) {
			return fmt.Sprintf(tr(lang, "tools.err.pull"), sv.Node)
		}
	}
	return fmt.Sprintf(tr(lang, "tools.err.stopped"), sv.Node)
}

func (w *web) handleTools(rw http.ResponseWriter, r *http.Request, s *session) {
	lang := w.lang(r)
	v := toolsPage{Presets: toolPresets, Secrets: w.secrets("", false)}
	conf := w.c.conf()
	v.Voice = w.c.store.get(voiceTokenKey) != ""
	v.VoiceOnly = v.Voice
	for _, sv := range w.servers() {
		t := toolView{serverView: sv, Source: sv.Source}
		t.Preset, t.Detail = presetOf(lang, conf.Servers[sv.Name])
		if t.Preset.ID == "home-assistant" {
			v.VoiceOnly = false
		}
		t.Pending, t.Placed = startState(sv)
		if t.Pending {
			t.Problem = serviceProblem(lang, sv)
		}
		v.Tools = append(v.Tools, t)
		// a server being placed or still connecting (e.g. just authorized):
		// the list refreshes itself until it settles
		v.Waiting = v.Waiting || t.Placed || sv.Status == statusConnecting
	}
	q := r.URL.Query()
	if n := q.Get("saved"); n != "" && w.c.source(kindServer, n) != "" {
		v.Flash = fmt.Sprintf(tr(lang, "tools.saved"), n)
	}
	if n := q.Get("removed"); n != "" && serverName.MatchString(n) && w.c.source(kindServer, n) == "" {
		v.Flash = fmt.Sprintf(tr(lang, "tools.removed"), n)
	}
	if n := q.Get("connected"); n != "" && w.c.source(kindServer, n) != "" {
		v.Flash = fmt.Sprintf(tr(lang, "tools.connectedflash"), n)
		if title := serviceTitle(lang, "", conf.Servers[n].URL); title != "" {
			v.Flash = fmt.Sprintf(tr(lang, "tools.connectedas"), title)
		}
	}
	if n := q.Get("notconnected"); n != "" && serverName.MatchString(n) {
		why := q.Get("why")
		if why != "cancelled" && why != "expired" {
			why = "failed"
		}
		end := "tools.pendingdropped"
		if q.Get("kept") != "" {
			end = "tools.pendingkept"
		}
		v.Alert = fmt.Sprintf(tr(lang, "tools.notconnected."+why), n) + " " + tr(lang, end)
	}
	v.Skipped = map[string]string{}
	for k, why := range w.c.skippedEntries() {
		if name, ok := strings.CutPrefix(k, kindServer+"/"); ok {
			v.Skipped[name] = why
		}
	}
	w.render(rw, r, s, "settings", "", page{Title: "settings.tools", Nav: "settings", Tab: "tools", Data: v})
}

// envRow is one environment variable of a local server, in the form.
type envRow struct {
	Name   string
	Value  string // literal values only
	Secret bool
	IsSet  bool // a secret is set: the field stays empty
	index  int  // its row in the posted form
}

// toolForm is the form of a tool.
type toolForm struct {
	Edit   bool
	Preset toolPreset
	Name   string
	Use    string
	Kind   string // remote or local
	URL    string
	Auth   string // none, bearer or oauth
	TokenSet,
	ClientIDSet, ClientSecretSet bool
	Scopes, ClientMetadata string
	// Trust is "what Eriphe may do without asking": true is "reads free,
	// the rest asks me" (trust_annotations), false "ask me everything".
	Trust   bool
	Timeout string
	// Local servers.
	Image, Transport, Port, Path, Args string
	Node, Needs, Prefers, User         string
	Volume, Egress, Writable           bool
	Env                                []envRow
	// Mail and calendar: the kind of account comes first (Choose), then
	// its own form.
	Mail      mailForm
	Calendar  calendarForm
	SecretSet bool // their password is set
	Choose    bool // the kind of account is to be picked
	Switched  bool // an edit moved to another account: a new password is needed
	// OAuth: the primary action connects; Service names whom to.
	Service   string
	OrigOAuth bool // an edit of a server already on OAuth: it can be saved without signing in again

	Error string
}

// PickURL is where a kind of account picked from the list leads.
func (f toolForm) PickURL() string {
	if f.Edit {
		return "/settings/tools/" + f.Name + "/edit?provider="
	}
	return "/settings/tools/new?preset=" + f.Preset.ID + "&provider="
}

// MailChoices and CalChoices are the kinds of account offered.
func (f toolForm) MailChoices() []mailChoice {
	return slices.DeleteFunc(slices.Clone(mailChoices), func(c mailChoice) bool { return c.Hidden })
}
func (f toolForm) CalChoices() []calChoice { return calChoices }

// Testable tells whether "Test the connection" says something useful: not
// for a vendor's OAuth service, whose sign-in is the test.
func (f toolForm) Testable() bool { return !f.Preset.Vendor() }

// serviceTitle is how the form and the flash name an OAuth service: the
// title of the preset it comes from, else "" (the tool's own name is used).
func serviceTitle(lang, presetID, url string) string {
	for _, p := range toolPresets {
		if p.Auth == "oauth" && (p.ID == presetID || (url != "" && p.URL == url)) {
			return tr(lang, "preset."+p.ID+".title")
		}
	}
	return ""
}

// newToolForm is the form of a preset, for a new tool.
func newToolForm(lang string, p toolPreset) toolForm {
	f := toolForm{Preset: p, Name: p.Name, Kind: p.Kind, URL: p.URL, Auth: p.Auth, Trust: p.Trust,
		Transport: transportStdio, Egress: true}
	if f.Kind == "" {
		f.Kind = "remote"
	}
	if f.Auth == "" {
		f.Auth = "none"
	}
	if p.AskURL {
		f.URL = "" // the user's own address: the field shows an example
	}
	if k := "preset." + p.ID + ".use"; tr(lang, k) != k {
		f.Use = tr(lang, k)
	}
	if p.Form == "calendar" {
		f.Calendar = calendarForm{Timezone: "Europe/Paris"}
	}
	f.Env = padEnv(nil)
	return f
}

func padEnv(rows []envRow) []envRow {
	for len(rows) < 3 || rows[len(rows)-1].Name != "" {
		rows = append(rows, envRow{})
	}
	return rows
}

// toolFormOf fills the form from a workspace server, for an edit.
func toolFormOf(name string, s Server) toolForm {
	f := toolForm{Edit: true, Name: name, Use: s.Use, Trust: s.TrustAnnotations, Kind: "remote", URL: s.URL,
		Auth: s.authKind(), Transport: transportStdio, Egress: true}
	f.Preset, _ = presetByID("remote")
	if p, ok := presetByURL(s.URL); ok && !s.local() {
		f.Preset = p // a vendor's service: its address stays fixed
	}
	if s.Timeout > 0 {
		f.Timeout = strconv.Itoa(s.Timeout)
	}
	f.OrigOAuth = s.authKind() == "oauth"
	if a := s.Auth; a != nil {
		f.TokenSet = a.BearerFile != ""
		if o := a.OAuth; o != nil {
			f.Scopes, f.ClientMetadata = strings.Join(o.Scopes, " "), o.ClientMetadata
			f.ClientIDSet, f.ClientSecretSet = o.ClientIDFile != "", o.ClientSecretFile != ""
		}
	}
	if s.local() {
		f.Kind, f.Auth = "local", "none"
		f.Preset, _ = presetByID("local")
		spec, _ := decodeRun(s.Run)
		f.Image, f.Transport, f.Path, f.Args = spec.Image, spec.Transport, spec.Path, strings.Join(spec.Args, "\n")
		if spec.Port > 0 {
			f.Port = strconv.Itoa(spec.Port)
		}
		f.Node, f.Needs, f.Prefers = spec.Node, strings.Join(spec.Needs, ", "), strings.Join(spec.Prefers, ", ")
		f.Volume, f.Writable, f.Egress = spec.Volume, spec.Writable, spec.Egress == nil || *spec.Egress
		if spec.User != defaultUser {
			f.User = spec.User
		}
		for _, k := range slices.Sorted(maps.Keys(spec.Env)) {
			v := spec.Env[k]
			f.Env = append(f.Env, envRow{Name: k, Value: v.Value, Secret: v.File != "", IsSet: v.File != ""})
		}
		if m, ok := mailFormOf(spec); ok {
			f.Preset, _ = presetByID("mail")
			f.Mail, f.SecretSet = m, spec.Env[mailSecretEnv].File != ""
		} else if cf, ok := calendarFormOf(spec); ok {
			f.Preset, _ = presetByID("calendar")
			f.Calendar, f.SecretSet = cf, spec.Env[calSecretEnv].File != ""
		}
	}
	f.Env = padEnv(f.Env)
	return f
}

// readToolForm reads a posted tool form.
func readToolForm(r *http.Request) toolForm {
	v := func(k string) string { return strings.TrimSpace(r.PostFormValue(k)) }
	on := func(k string) bool { return r.PostFormValue(k) != "" }
	f := toolForm{Name: v("name"), Use: v("use"), Kind: v("kind"), URL: v("url"), Auth: v("auth"),
		Scopes: v("scopes"), ClientMetadata: v("client_metadata_url"), Timeout: v("timeout"),
		Image: v("image"), Transport: v("transport"), Port: v("port"), Path: v("path"), Args: r.PostFormValue("args"),
		Node: v("node"), Needs: v("needs"), Prefers: v("prefers"), User: v("user"),
		Volume: on("volume"), Egress: on("egress"), Writable: on("writable"),
		Mail: mailForm{Address: v("address"), Provider: v("mail_provider"), IMAPHost: v("imap_host"), SMTPHost: v("smtp_host"),
			IMAPPort: v("imap_port"), SMTPPort: v("smtp_port"), Username: v("mail_user")},
		Calendar: calendarForm{Provider: v("cal_provider"), URL: v("cal_url"), Username: v("cal_user"), Timezone: v("timezone")}}
	// "access": read (reads free, the rest asks) or ask (everything asks);
	// "trust" is the API's and older forms' word for the first.
	switch v("access") {
	case "read":
		f.Trust = true
	case "ask":
	default:
		f.Trust = on("trust")
	}
	if slug := slugName(f.Name); slug != "" {
		f.Name = slug
	}
	f.Preset, _ = presetByID(v("preset"))
	n, _ := strconv.Atoi(r.PostFormValue("env_count"))
	for i := 0; i < min(n, 50); i++ {
		name := v(fmt.Sprintf("env_name_%d", i))
		if name == "" {
			continue
		}
		f.Env = append(f.Env, envRow{Name: name, Value: r.PostFormValue(fmt.Sprintf("env_value_%d", i)),
			Secret: on(fmt.Sprintf("env_secret_%d", i)), index: i})
	}
	return f
}

// server builds the catalog entry and its credentials from the form; old
// is the entry being edited, whose secret references stay unless replaced.
func (f toolForm) server(r *http.Request, old *Server) (Server, credentials, error) {
	creds := credentials{}
	s := Server{Use: f.Use, TrustAnnotations: f.Trust}
	if f.Use == "" {
		return s, nil, errors.New("form.nouse")
	}
	if f.Timeout != "" {
		t, err := strconv.Atoi(f.Timeout)
		if err != nil || t < 0 {
			return s, nil, errors.New("form.badtimeout")
		}
		s.Timeout = t
	}
	var oldSpec *RunSpec
	if old != nil && old.local() {
		if spec, err := decodeRun(old.Run); err == nil {
			oldSpec = &spec
		}
	}
	switch f.Preset.Form {
	case "mail", "calendar":
		var spec RunSpec
		var err error
		env, moved := mailSecretEnv, false
		if f.Preset.Form == "mail" {
			if err = f.Mail.normalize(oldSpec); err == nil {
				spec, err = f.Mail.run(oldSpec)
			}
			if prev, ok := mailFormOf(deref(oldSpec)); ok {
				moved = !prev.same(f.Mail)
			}
		} else {
			env = calSecretEnv
			if err = f.Calendar.normalize(); err == nil {
				spec, err = f.Calendar.run(oldSpec)
			}
			if prev, ok := calendarFormOf(deref(oldSpec)); ok {
				moved = !prev.same(f.Calendar)
			}
		}
		if err != nil {
			return s, nil, err
		}
		// Another mailbox or calendar account: the old password is not its own.
		pw := accountPassword(r, f)
		if strings.TrimSpace(pw) == "" && (spec.Env[env].File == "" || moved) {
			if moved {
				return s, nil, errors.New("form.newpassword")
			}
			return s, nil, errors.New("form.nopassword")
		}
		creds["env:"+env] = pw
		s.Run = marshalRun(spec)
		return s, creds, nil
	}
	if f.Kind == "local" {
		spec := RunSpec{Image: f.Image, Transport: f.Transport, Path: f.Path, Node: f.Node,
			Needs: words(f.Needs), Prefers: words(f.Prefers), User: f.User, Volume: f.Volume, Writable: f.Writable}
		egress := f.Egress
		spec.Egress = &egress
		if f.Port != "" {
			p, err := strconv.Atoi(f.Port)
			if err != nil {
				return s, nil, errors.New("form.badport")
			}
			spec.Port = p
		}
		for _, a := range strings.Split(f.Args, "\n") {
			if a = strings.TrimSpace(a); a != "" {
				spec.Args = append(spec.Args, a)
			}
		}
		if spec.Transport != transportHTTP {
			spec.Port, spec.Path = 0, ""
		}
		spec.Env = map[string]EnvValue{}
		for _, e := range f.Env {
			if !e.Secret {
				spec.Env[e.Name] = EnvValue{Value: e.Value}
				continue
			}
			value := r.PostFormValue(fmt.Sprintf("env_value_%d", e.index))
			if strings.TrimSpace(value) != "" {
				creds["env:"+e.Name] = value
				spec.Env[e.Name] = EnvValue{File: "vault:pending"} // replaced by the catalog
				continue
			}
			if oldSpec != nil && oldSpec.Env[e.Name].File != "" {
				spec.Env[e.Name] = oldSpec.Env[e.Name]
				continue
			}
			return s, nil, fmt.Errorf("form.nosecret")
		}
		s.Run = marshalRun(spec)
		return s, creds, nil
	}
	s.URL = f.URL
	if u, err := url.Parse(f.URL); f.URL == "" {
		return s, nil, errors.New("form.nourl")
	} else if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || strings.ContainsAny(f.URL, "<>") {
		return s, nil, errors.New("form.badurl")
	}
	switch f.Auth {
	case "bearer":
		s.Auth = &ServerAuth{}
		if old != nil && old.Auth != nil && old.Auth.BearerFile != "" {
			s.Auth.BearerFile = old.Auth.BearerFile
		}
		tok := r.PostFormValue("token")
		if strings.TrimSpace(tok) == "" && s.Auth.BearerFile == "" {
			return s, nil, errors.New("form.notoken")
		}
		creds["token"] = tok
	case "oauth":
		o := &OAuthConfig{Scopes: words(f.Scopes), ClientMetadata: f.ClientMetadata}
		if old != nil && old.Auth != nil && old.Auth.OAuth != nil {
			o.ClientIDFile, o.ClientSecretFile, o.RedirectURI = old.Auth.OAuth.ClientIDFile, old.Auth.OAuth.ClientSecretFile, old.Auth.OAuth.RedirectURI
		}
		if r.PostFormValue("forget_client") != "" {
			o.ClientIDFile, o.ClientSecretFile = "", ""
		}
		s.Auth = &ServerAuth{OAuth: o}
		creds["client-id"], creds["client-secret"] = r.PostFormValue("client_id"), r.PostFormValue("client_secret")
		if strings.TrimSpace(creds["client-secret"]) != "" && strings.TrimSpace(creds["client-id"]) == "" && o.ClientIDFile == "" {
			return s, nil, errors.New("form.noclientid")
		}
	default:
		s.Auth = &ServerAuth{None: true}
	}
	return s, creds, nil
}

func (w *web) toolFormPage(rw http.ResponseWriter, r *http.Request, s *session, f toolForm) {
	f.Service = serviceTitle(w.lang(r), f.Preset.ID, f.URL)
	w.render(rw, r, s, "settings", "", page{Title: "tools.add", Nav: "settings", Tab: "tools", View: "form", Data: f})
}

// pickAccount applies ?provider= to a mail or calendar form: the kind of
// account picked from the list, or the list itself ("choose", or nothing
// for a new tool).
func pickAccount(f *toolForm, provider string) {
	switch f.Preset.Form {
	case "mail":
		c, ok := mailChoiceByID(provider)
		switch {
		case ok && !c.Soon && !c.Hidden:
			if f.Edit && c.ID != f.Mail.Provider {
				f.Mail, f.SecretSet, f.Switched = mailForm{Address: f.Mail.Address, Provider: c.ID}, false, true
			}
			f.Mail.Provider = c.ID
		case provider == "choose" || !f.Edit:
			f.Choose = true
		}
	case "calendar":
		c, ok := calChoiceByID(provider)
		switch {
		case ok && !c.Soon:
			if f.Edit && c.ID != f.Calendar.Provider {
				f.Calendar = calendarForm{Provider: c.ID, Username: f.Calendar.Username, Timezone: f.Calendar.Timezone}
				f.SecretSet, f.Switched = false, true
			}
			f.Calendar.Provider = c.ID
		case provider == "choose" || !f.Edit:
			f.Choose = true
		}
	}
}

func (w *web) handleNewTool(rw http.ResponseWriter, r *http.Request, s *session) {
	p, ok := presetByID(r.URL.Query().Get("preset"))
	if !ok {
		p, _ = presetByID("remote")
	}
	if p.Form == "drive" {
		http.Redirect(rw, r, p.Page(), http.StatusSeeOther)
		return
	}
	f := newToolForm(w.lang(r), p)
	pickAccount(&f, r.URL.Query().Get("provider"))
	w.toolFormPage(rw, r, s, f)
}

// handleGallery is "Add a tool": the tiles of the presets.
func (w *web) handleGallery(rw http.ResponseWriter, r *http.Request, s *session) {
	w.render(rw, r, s, "settings", "", page{Title: "tools.add", Nav: "settings", Tab: "tools", View: "gallery", Data: toolsPage{Presets: toolPresets}})
}

func (w *web) handleEditTool(rw http.ResponseWriter, r *http.Request, s *session) {
	name := r.PathValue("name")
	srv, ok := w.workspaceServer(name)
	if !ok {
		http.Redirect(rw, r, "/settings/tools", http.StatusSeeOther)
		return
	}
	f := toolFormOf(name, srv)
	if p := r.URL.Query().Get("provider"); p != "" {
		pickAccount(&f, p)
	}
	w.toolFormPage(rw, r, s, f)
}

func (w *web) workspaceServer(name string) (Server, bool) {
	if w.c.source(kindServer, name) != sourceWorkspace {
		return Server{}, false
	}
	srv, ok := w.c.conf().Servers[name]
	return srv, ok
}

// handleSaveTool creates (POST /settings/tools) or updates
// (POST /settings/tools/{name}) a tool. A refusal shows the form again.
func (w *web) handleSaveTool(rw http.ResponseWriter, r *http.Request, s *session) {
	f := readToolForm(r)
	mode, name := putCreate, f.Name
	var old *Server
	if n := r.PathValue("name"); n != "" {
		mode, name, f.Name, f.Edit = putUpdate, n, n, true
		if srv, ok := w.workspaceServer(n); ok {
			old = &srv
		}
	}
	fail := func(err error) {
		f.Error = w.formError(r, err)
		if old != nil {
			back := toolFormOf(name, *old)
			f.TokenSet, f.ClientIDSet, f.ClientSecretSet = back.TokenSet, back.ClientIDSet, back.ClientSecretSet
			f.OrigOAuth = back.OrigOAuth
			switch f.Preset.Form {
			case "mail":
				f.SecretSet = back.SecretSet && back.Mail.same(f.Mail)
			case "calendar":
				f.SecretSet = back.SecretSet && back.Calendar.same(f.Calendar)
			}
			f.Switched = !f.SecretSet && back.SecretSet
			for i := range f.Env {
				for _, o := range back.Env {
					if o.Name == f.Env[i].Name && f.Env[i].Secret {
						f.Env[i].IsSet, f.Env[i].Value = o.IsSet, ""
					}
				}
			}
		}
		for i := range f.Env {
			if f.Env[i].Secret {
				f.Env[i].Value = "" // a secret is never sent back
			}
		}
		f.Env = padEnv(f.Env)
		f.Service = serviceTitle(w.lang(r), f.Preset.ID, f.URL)
		w.render(rw, r, s, "settings", "tool-form", page{Data: f})
	}
	if mode == putUpdate && old == nil {
		if w.c.source(kindServer, name) == sourceConfig {
			fail(errReadOnly(kindServer, name))
		} else {
			fail(errNotFound(kindServer, name))
		}
		return
	}
	if mode == putCreate && name == "" {
		fail(errors.New("form.noname"))
		return
	}
	srv, creds, err := f.server(r, old)
	if err != nil {
		fail(err)
		return
	}
	// OAuth: saving and signing in are one action. The entry stays pending
	// until the service authorizes Eriphe (pending.go). Only an edit of a
	// server already on OAuth may be saved as is; a client that only knows a
	// loopback redirect keeps the save-then-paste way.
	plainSave := r.PostFormValue("connect") == "" && old != nil && old.authKind() == "oauth"
	if srv.authKind() == "oauth" && srv.Auth.OAuth.RedirectURI == "" && !plainSave {
		authURL, err := w.c.startPending(r.Context(), mode, name, srv, creds, w.callbackURL(r), w.oauthBinding(rw, r))
		if err != nil {
			fail(connectError(err))
			return
		}
		log.Printf("web: tool %s pending its authorization", name)
		if authURL == "" { // connected without one: saved
			w.notify()
			authURL = "/settings/tools?connected=" + url.QueryEscape(name)
		}
		rw.Header().Set("HX-Redirect", authURL)
		rw.WriteHeader(http.StatusOK)
		return
	}
	if err := w.c.putServer(mode, name, srv, creds); err != nil {
		fail(err)
		return
	}
	log.Printf("web: tool %s saved", name)
	w.notify()
	rw.Header().Set("HX-Redirect", "/settings/tools?saved="+url.QueryEscape(name))
	rw.WriteHeader(http.StatusOK)
}

func (w *web) handleDeleteTool(rw http.ResponseWriter, r *http.Request, s *session) {
	name := r.PathValue("name")
	if err := w.c.deleteServer(name); err != nil {
		w.toast(rw, w.formError(r, err), false)
		return
	}
	log.Printf("web: tool %s removed", name)
	w.notify()
	rw.Header().Set("HX-Redirect", "/settings/tools?removed="+url.QueryEscape(name))
	rw.WriteHeader(http.StatusOK)
}

// accountPassword is the password typed for a mailbox or calendar account.
// Google shows its app passwords in groups of four letters: the spaces
// copied with them are not part of it.
func accountPassword(r *http.Request, f toolForm) string {
	pw := r.PostFormValue("password")
	if f.Preset.Form == "mail" && f.Mail.Provider == "gmail" {
		pw = strings.ReplaceAll(pw, " ", "")
	}
	return pw
}
