package main

import (
	"errors"
	"fmt"
	"log"
	"maps"
	"net/http"
	"net/url"
	"regexp"
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
	// Account is the account the row is, on a server of several (the
	// mail, the calendar: web_accounts.go); "" for a tool.
	Account string
	Source  string
	Preset  toolPreset // the preset it comes from, if any
	Detail  string     // a line for a preset's tool: "Gmail · sam@gmail.com"
	Pending bool       // a local server whose service does not run yet
	Placed  bool       // … but a machine has it and is starting it
	Problem string     // why it does not start, in words; the raw error stays in Error
	Scope   tokenScope // how its credential follows its access level (access.go)
	Allowed int        // its actions allowed without asking
	Token   bool       // OAuth: a credential is stored (it may have expired)
	// Connection is the account the tool reads (connections.go), one that
	// must be connected again first, and ConnProvider the provider it is
	// signed in at; Reconnect, it must be connected again.
	Connection   string
	ConnProvider string
	Reconnect    bool
	// Places tells a tool that has places, picked on its page (places.go).
	Places bool
	// ConnPlaces is the connection whose places it uses, for a remote tool
	// on a connection that mints its tokens (connection_places.go).
	ConnPlaces string
	// Follow is the line that follows a tool just saved, in its row, until
	// it is up or says why not (web_tools_dialog.go).
	Follow *toolStatus
	// A service of an account (web_tools_groups.go): Part is mail,
	// calendar or files (not Service, the state of its service on its
	// machine); Owner the account's address and Provider who
	// gives it (google, icloud…), the card it is in; Label its heading
	// there and Mark its icon.
	Part     string
	Label    string
	Owner    string
	Provider string
	Mark     string
}

// Editable tells a tool changed with its form, in the dialog; the others
// have a page of their own (a tool with places, Home Assistant, the GitHub App)
// or are the configuration file's.
func (t toolView) Editable() bool { return editable(t.Source, t.Preset, t.Auth) }

// Removable tells a row with its own removal: a workspace tool, or an
// account on a password (a connected account's goes with it, from its
// card).
func (t toolView) Removable() bool {
	return t.Source == sourceWorkspace && (t.Account == "" || t.Connection == "")
}

// ConnLabel is the name of the provider of the account the tool reads.
func (t toolView) ConnLabel() string { return providerLabel(t.ConnProvider) }

// PlacesURL is the page of the tool's places (web_places.go).
func (t toolView) PlacesURL() string {
	return "/settings/tools/" + t.Name + "/places" + accountQuery(t.Account)
}

func editable(source string, p toolPreset, auth string) bool {
	return source == sourceWorkspace && auth != "connection" && p.Form != "connection"
}

// AccessURL is the tool's page, every row's Edit (web_access.go); AllowedURL,
// its actions allowed without asking there.
func (t toolView) AccessURL() string {
	return "/settings/tools/" + t.Name + "/access" + accountQuery(t.Account)
}

func (t toolView) AllowedURL() string { return t.AccessURL() + "#allowed-" + t.Key() }

// TrustURL is where its trust mark is changed (tool_trust.go).
func (t toolView) TrustURL() string { return t.AccessURL() + "#trust-" + t.Name }

// SignIn is the row's way to sign in to an OAuth service, only when it
// is needed: "connect" when it never was, "reconnect" when the
// authorization expired, was revoked or lacks a right; "connection" when
// the account it reads must be connected again (web_connections.go). Otherwise the
// level, changed with the form, is what asks for a new token.
func (t toolView) SignIn() string {
	switch {
	case t.Reconnect:
		return "connection"
	case t.Auth != "oauth" || t.Status != statusNeedsAuth:
		return ""
	case t.Token:
		return "reconnect"
	}
	return "connect"
}

type toolsPage struct {
	Tools    []toolView
	Accesses []accessRow // what tasks use with their own programs (web_lent_access.go)
	Web      string      // the web's value (brain_web.go)
	Groups   []toolGroup // the tools, a card per kind with several accounts (web_tools_groups.go)
	Gallery  galleryPage // the "Add a tool" dialog (web_gallery.go)
	Flash    string
	Alert    string // a pending tool that was not connected
	Skipped  map[string]string
	Waiting  bool   // a tool is starting: the list refreshes itself until it is up
	Follow   string // ?tool=: the tool just saved, its row followed and lit
	New      bool   // ?new=1: … a new one, at the top of the list
}

// RefreshURL is the list asking for itself again: the tool followed stays
// followed, and at the top.
func (v toolsPage) RefreshURL() string {
	if v.Follow == "" {
		return "/settings/tools"
	}
	u := "/settings/tools?tool=" + url.QueryEscape(v.Follow)
	if v.New {
		u += "&new=1"
	}
	return u
}

// presetOf finds what preset a catalog entry comes from, and the line that
// says which account it is.
func presetOf(lang string, s Server) (toolPreset, string) {
	if !s.local() {
		if p, ok := presetByURL(s.URL); ok {
			if p.AskURL {
				u, _ := url.Parse(s.URL)
				return p, tr(lang, "preset."+p.ID+".title") + " · " + u.Host
			}
			return p, tr(lang, "preset."+p.ID+".title")
		}
		return toolPreset{}, ""
	}
	spec, err := decodeRun(s.Run)
	if err != nil {
		return toolPreset{}, ""
	}
	if svc, cfg, ok := accountsOf(Server{Run: marshalRun(spec)}); ok {
		// The service, every account (accounts.go); one account says which,
		// and comes from where it does.
		if len(cfg.Accounts) == 1 {
			return accountPreset(svc, cfg.Accounts[0]), accountDetail(lang, svc, cfg.Accounts[0])
		}
		p, ok := presetByID(svc.ID)
		if !ok && len(cfg.Accounts) > 0 {
			p = accountPreset(svc, cfg.Accounts[0])
		}
		return p, ""
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
	v := toolsPage{}
	conf := w.c.conf()
	q := r.URL.Query()
	if n := q.Get("tool"); followKey.MatchString(n) {
		v.Follow, v.New = n, q.Get("new") != "" // a tool, or an account's row (its Key)
	}
	for _, sv := range w.servers() {
		for _, t := range w.toolRows(lang, conf, sv) {
			if t.Key() == v.Follow || t.Account != "" && sv.Name == v.Follow && v.New {
				st := statusOf(lang, sv)
				st.Row = t.RowURL()
				t.Follow = &st
				if v.New {
					v.Tools = append([]toolView{t}, v.Tools...)
					continue
				}
			} else {
				// a server being placed or still connecting (e.g. just
				// authorized): the list refreshes itself until it settles; the
				// tool followed asks for its own row
				v.Waiting = v.Waiting || t.Placed || sv.Status == statusConnecting
			}
			v.Tools = append(v.Tools, t)
		}
	}
	v.Groups = groupTools(v.Tools)
	v.Gallery = w.gallery(lang)
	if n := q.Get("saved"); n != "" && w.c.source(kindServer, n) != "" {
		v.Flash = fmt.Sprintf(tr(lang, "tools.saved"), n)
	}
	if n := q.Get("savedaccess"); n != "" && w.c.source(kindAccess, n) != "" {
		v.Flash = fmt.Sprintf(tr(lang, "lent.saved"), n)
	}
	if n := q.Get("removedaccess"); serverName.MatchString(n) && w.c.source(kindAccess, n) == "" {
		v.Flash = fmt.Sprintf(tr(lang, "lent.removed"), n)
	}
	if n := q.Get("removed"); n != "" && serverName.MatchString(n) && w.c.source(kindServer, n) == "" {
		v.Flash = fmt.Sprintf(tr(lang, "tools.removed"), n)
	}
	if n := q.Get("removedaccount"); n != "" && len(n) <= 254 && !strings.ContainsAny(n, "<>\"") {
		v.Flash = fmt.Sprintf(tr(lang, "accounts.removed"), n)
	}
	if n := q.Get("connected"); n != "" && w.c.source(kindServer, n) != "" {
		v.Flash = fmt.Sprintf(tr(lang, "tools.connectedflash"), n)
		if title := serviceTitle(lang, "", conf.Servers[n].URL); title != "" {
			v.Flash = fmt.Sprintf(tr(lang, "tools.connectedas"), title)
		}
	}
	if q.Get("connected") == "1" {
		v.Flash = tr(lang, "conn.connected")
		var missing []string
		for _, k := range strings.Split(q.Get("missing"), ",") {
			if slices.Contains(toolKinds, k) {
				missing = append(missing, tr(lang, "acct.svc."+k))
			}
		}
		if len(missing) > 0 {
			v.Alert = fmt.Sprintf(tr(lang, "conn.missing"), strings.Join(missing, ", "))
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
	v.Accesses = w.accessRows() // web_lent_access.go
	v.Web = w.c.webValue()
	v.Skipped = map[string]string{}
	for k, why := range w.c.skippedEntries() {
		name, ok := strings.CutPrefix(k, kindServer+"/")
		if !ok {
			name, ok = strings.CutPrefix(k, kindAccess+"/")
		}
		if ok {
			v.Skipped[name] = why
		}
	}
	w.render(rw, r, s, "settings", "", page{Title: "settings.tools", Nav: "settings", Tab: "tools", Data: v})
}

// followKey is what ?tool= may name: a tool, or an account's row.
var followKey = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,47}$`)

// toolRow is one tool of the list.
func (w *web) toolRow(lang string, conf *Config, sv serverView) toolView {
	t := toolView{serverView: sv, Source: sv.Source}
	t.Preset, t.Detail = presetOf(lang, conf.Servers[sv.Name])
	t.Scope = w.c.tokenScope(sv.Name, conf.Servers[sv.Name])
	t.Allowed = len(w.c.store.allowedActions(sv.Name))
	if sv.Auth == "oauth" {
		_, tok, _ := w.c.store.oauthToken(sv.Name)
		t.Token = tok != nil
	}
	for _, a := range w.c.connectionsOf(conf.Servers[sv.Name]) {
		if t.Connection == "" || a.Lapsed != "" && !t.Reconnect {
			t.Connection, t.ConnProvider, t.Reconnect = a.Identity, a.Provider, a.Lapsed != ""
		}
	}
	if t.Preset.AppPage != "" {
		t.Detail = appDetail(lang, t.Preset, conf.Servers[sv.Name], t.Connection)
	}
	if cn := conf.Servers[sv.Name].authConnection(); cn != "" && sv.Source == sourceWorkspace {
		t.ConnPlaces = cn
	}
	_, _, t.Places = keeperOf(conf.Servers[sv.Name])
	t.Places = t.Places && sv.Source == sourceWorkspace
	t.Pending, t.Placed = startState(sv)
	if t.Pending {
		t.Problem = serviceProblem(lang, sv)
	}
	return t
}

// appDetail is the line of a tool that also goes on an app's connection
// (AppPage): on the app, and on which account's connection; or on a token.
func appDetail(lang string, p toolPreset, s Server, account string) string {
	d := tr(lang, "preset."+p.ID+".title")
	switch s.authKind() {
	case "connection":
		d += " · " + tr(lang, "tools.viaapp")
		if account != "" {
			d += " · " + account
		}
	case "bearer":
		d += " · " + tr(lang, "tools.viatoken")
	}
	return d
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
	// Access is what Eriphe may do on the tool (access.go): read-only,
	// reads, free or ask.
	Access  string
	Timeout string
	// Local servers.
	Image, Transport, Port, Path, Args string
	Node, Needs, Prefers, User         string
	Volume, Egress, Writable           bool
	Env                                []envRow
	// An account of a tool with a card (account_forms.go): the kind of
	// account comes first (Choose), then its own form. Account is the
	// account edited, in its service's server (accounts.go).
	Account   string
	Acct      accountForm
	SecretSet bool // its password is set
	Choose    bool // the kind of account is to be picked
	Switched  bool // an edit moved to another account: a new password is needed
	// A tool that also goes on an app (AppPage): the app or a token comes
	// first (Choose); Way is "token" once the token is picked.
	Way string
	// OAuth: the primary action connects; Service names whom to.
	Service   string
	OrigOAuth bool // an edit of a server already on OAuth: it can be saved without signing in again
	// … but not at these levels, which its stored credential cannot serve:
	// saving at one signs in again, for a token of that level (Reauth).
	Reauth []string
	// An edit: how its credential follows the level, and its actions
	// allowed without asking (web_access.go).
	AccessView *toolAccess
	// InDialog: the form is the second step of the tools page's dialog
	// (web_tools_dialog.go), not a page of its own.
	InDialog bool

	Error string
}

// ReauthClass marks the form with the levels that sign in again, for the
// save button to say so (app.css, .reauth-*).
func (f toolForm) ReauthClass() string {
	var b strings.Builder
	for _, l := range f.Reauth {
		b.WriteString(" reauth-" + l)
	}
	return b.String()
}

// reauthLevels are the levels an OAuth tool's stored credential cannot
// serve as it is: none stored, a read-only token for a level that writes,
// or a token that writes for read only when the service has read-only
// tokens (access.go).
func (w *web) reauthLevels(name string, s Server) []string {
	if s.authKind() != "oauth" {
		return nil
	}
	if _, tok, _ := w.c.store.oauthToken(name); tok == nil {
		return accessLevels
	}
	t := w.c.tokenScope(name, s)
	if !t.Scoped || t.Granted == "" {
		return nil
	}
	var out []string
	for _, l := range accessLevels {
		if (l == accessReadOnly) != (t.Granted == grantedRead) {
			out = append(out, l)
		}
	}
	return out
}

// IsAccount tells the form of an account of a tool with a card: an account
// of its service's one server, without a name or a use of its own.
func (f toolForm) IsAccount() bool { return f.Preset.Form == "account" }

// Card is the card of the tool an account form is of.
func (f toolForm) Card() toolCard {
	card, _ := serviceByID(f.Preset.Card)
	return card
}

// Shape is how an account form is made; nil for another form.
func (f toolForm) Shape() *accountShape {
	if !f.IsAccount() {
		return nil
	}
	return f.Card().Account
}

// Choice is the kind of account picked, and Choices the ones offered.
func (f toolForm) Choice() accountChoice { c, _ := f.Shape().choice(f.Acct.Choice); return c }
func (f toolForm) Choices() []accountChoice {
	if s := f.Shape(); s != nil {
		return s.offered()
	}
	return nil
}

// Fields are the fields of the account's choice, the advanced ones apart;
// Options the server's options the form sets (accountShape.Server).
func (f toolForm) Fields(advanced bool) []accountField { return f.Shape().fields(f.Choice(), advanced) }
func (f toolForm) Options() []accountField             { return f.Shape().Server }

// Fill fills a pattern of the shape's words with a choice: "mailp.%s.note".
func (f toolForm) Fill(pattern, choice string) string { return fill(pattern, choice) }

// SignInPage is where a choice signed in at a provider is connected.
func (c accountChoice) SignInPage(card string) string { return connectPage(c.SignIn, card) }

// Title is what an edit's form is of: the account, or the tool.
func (f toolForm) Title() string { return cmpOr(f.Account, f.Name) }

// PickURL is where a kind of account picked from the list leads.
func (f toolForm) PickURL() string {
	if f.Edit && f.Account != "" {
		return "/settings/tools/" + f.Name + "/edit?account=" + url.QueryEscape(f.Account) + "&provider="
	}
	if f.Edit {
		return "/settings/tools/" + f.Name + "/edit?provider="
	}
	return "/settings/tools/new?preset=" + f.Preset.ID + "&provider="
}

// PickKind names the choice the form starts with, for its words
// (pick.<kind>.*): the tool's kind (mail, calendar), or the preset's for
// one that goes on an app or a token (AppPage).
func (f toolForm) PickKind() string {
	if f.Preset.AppPage != "" {
		return f.Preset.ID
	}
	return f.Preset.Card
}

// Picked is what was picked from that choice, if anything.
func (f toolForm) Picked() string {
	if f.Way != "" {
		return f.Way
	}
	return f.Acct.Choice
}

// Testable tells whether "Test the connection" says something useful: not
// for a vendor's OAuth service, whose sign-in is the test, nor for an
// account, which its server tests once saved (web_tools_dialog.go).
func (f toolForm) Testable() bool { return !f.Preset.Vendor() && !f.IsAccount() }

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
	f := toolForm{Preset: p, Name: p.Name, Kind: p.Kind, URL: p.URL, Auth: p.Auth, Access: p.level(),
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
	if f.IsAccount() {
		f.Kind, f.Acct.Values = "local", map[string]string{}
		for _, o := range f.Options() {
			f.Acct.Values[o.Name] = o.Default
		}
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
	f := toolForm{Edit: true, Name: name, Use: s.Use, Access: s.level(), Kind: "remote", URL: s.URL,
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
		if svc, _, ok := accountsOf(s); ok {
			f.Preset, _ = presetByID(svc.ID) // its accounts have their forms (web_accounts.go)
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
		Account: v("account")}
	f.Access = formAccess(v("access"), on("trust"))
	f.InDialog = on("dialog")
	if slug := slugName(f.Name); slug != "" {
		f.Name = slug
	}
	f.Preset, _ = presetByID(v("preset"))
	f.Acct = readAccountForm(f.Shape(), v)
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
	s := Server{Use: f.Use}.withLevel(f.Access)
	if old != nil {
		s.Exceptions = old.Exceptions
		if old.level() == f.Access { // the form shows a level: the grid behind it stays
			s.Grid, s.AccountGrids, s.TrustAnnotations = old.Grid, old.AccountGrids, old.TrustAnnotations
		}
	}
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
	switch {
	case f.Kind == "local":
		return f.localServer(r, s, oldSpec)
	}
	return f.remoteServer(r, s, old)
}

// localServer is a server run on a node, from its image.
func (f toolForm) localServer(r *http.Request, s Server, oldSpec *RunSpec) (Server, credentials, error) {
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
	creds := credentials{}
	env, err := f.localEnv(r, oldSpec, creds)
	if err != nil {
		return s, nil, err
	}
	spec.Env = env
	s.Run = marshalRun(spec)
	return s, creds, nil
}

// localEnv is a local server's environment. A secret typed goes to creds;
// one left empty keeps the edited server's, and is required otherwise.
func (f toolForm) localEnv(r *http.Request, oldSpec *RunSpec, creds credentials) (map[string]EnvValue, error) {
	env := map[string]EnvValue{}
	for _, e := range f.Env {
		if !e.Secret {
			env[e.Name] = EnvValue{Value: e.Value}
			continue
		}
		value := r.PostFormValue(fmt.Sprintf("env_value_%d", e.index))
		if strings.TrimSpace(value) != "" {
			creds["env:"+e.Name] = value
			env[e.Name] = EnvValue{File: "vault:pending"} // replaced by the catalog
			continue
		}
		if oldSpec != nil && oldSpec.Env[e.Name].File != "" {
			env[e.Name] = oldSpec.Env[e.Name]
			continue
		}
		return nil, fmt.Errorf("form.nosecret")
	}
	return env, nil
}

// remoteServer is a server at an address, with its authentication.
func (f toolForm) remoteServer(r *http.Request, s Server, old *Server) (Server, credentials, error) {
	s.URL = f.URL
	if u, err := url.Parse(f.URL); f.URL == "" {
		return s, nil, errors.New("form.nourl")
	} else if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || strings.ContainsAny(f.URL, "<>") {
		return s, nil, errors.New("form.badurl")
	}
	creds := credentials{}
	var err error
	s.Auth, err = f.remoteAuth(r, old, creds)
	if err != nil {
		return s, nil, err
	}
	return s, creds, nil
}

// remoteAuth is a remote server's authentication, its credentials put in
// creds. The edited server's token or OAuth client stays unless replaced.
func (f toolForm) remoteAuth(r *http.Request, old *Server, creds credentials) (*ServerAuth, error) {
	switch f.Auth {
	case "bearer":
		a := &ServerAuth{}
		if old != nil && old.Auth != nil && old.Auth.BearerFile != "" {
			a.BearerFile = old.Auth.BearerFile
		}
		tok := r.PostFormValue("token")
		if strings.TrimSpace(tok) == "" && a.BearerFile == "" {
			return a, errors.New("form.notoken")
		}
		creds["token"] = tok
		return a, nil
	case "oauth":
		o := &OAuthConfig{Scopes: words(f.Scopes), ClientMetadata: f.ClientMetadata}
		if old != nil && old.Auth != nil && old.Auth.OAuth != nil {
			o.ClientIDFile, o.ClientSecretFile, o.RedirectURI = old.Auth.OAuth.ClientIDFile, old.Auth.OAuth.ClientSecretFile, old.Auth.OAuth.RedirectURI
		}
		if r.PostFormValue("forget_client") != "" {
			o.ClientIDFile, o.ClientSecretFile = "", ""
		}
		creds["client-id"], creds["client-secret"] = r.PostFormValue("client_id"), r.PostFormValue("client_secret")
		if strings.TrimSpace(creds["client-secret"]) != "" && strings.TrimSpace(creds["client-id"]) == "" && o.ClientIDFile == "" {
			return &ServerAuth{OAuth: o}, errors.New("form.noclientid")
		}
		return &ServerAuth{OAuth: o}, nil
	}
	return &ServerAuth{None: true}, nil
}

func (w *web) toolFormPage(rw http.ResponseWriter, r *http.Request, s *session, f toolForm) {
	f.Service = serviceTitle(w.lang(r), f.Preset.ID, f.URL)
	if inDialog(r) {
		f.InDialog = true
		w.render(rw, r, s, "settings", "tool-step", page{Data: f})
		return
	}
	w.render(rw, r, s, "settings", "", page{Title: "tools.add", Nav: "settings", Tab: "tools", View: "form", Data: f})
}

// pickAccount applies ?provider= to a mail or calendar form: the kind of
// account picked from the list, or the list itself ("choose", or nothing
// for a new tool).
func pickAccount(f *toolForm, provider string) {
	if f.Preset.AppPage != "" && !f.Edit {
		// The app or a token: the choice first (AppPage).
		if provider == "token" {
			f.Way = provider
		} else {
			f.Choose = true
		}
		return
	}
	s := f.Shape()
	if s == nil {
		return
	}
	c, ok := s.choice(provider)
	switch {
	case ok && !c.Soon && !c.Hidden && c.SignIn == "":
		if f.Edit && c.ID != f.Acct.Choice {
			// Another kind of account: its login and the server's options
			// stay, a new password is needed.
			keep := map[string]string{s.NameBy: f.Acct.Values[s.NameBy]}
			for _, o := range s.Server {
				keep[o.Name] = f.Acct.Values[o.Name]
			}
			f.Acct.Values, f.SecretSet, f.Switched = keep, false, true
		}
		f.Acct.Choice = c.ID
	case provider == "choose" || !f.Edit:
		f.Choose = true
	}
}

func (w *web) handleNewTool(rw http.ResponseWriter, r *http.Request, s *session) {
	p, ok := presetByID(r.URL.Query().Get("preset"))
	if !ok {
		p, _ = presetByID("remote")
	}
	if p.Form == "connection" {
		if inDialog(r) {
			rw.Header().Set("HX-Redirect", p.Page())
			rw.WriteHeader(http.StatusOK)
			return
		}
		http.Redirect(rw, r, p.Page(), http.StatusSeeOther)
		return
	}
	f := newToolForm(w.lang(r), p)
	if q := r.URL.Query(); p.ID == "remote" {
		// From "Autre service": what Eriphe found at the address (web_gallery.go).
		if q.Get("url") != "" {
			f.URL = q.Get("url")
		}
		if a := q.Get("auth"); a == "none" || a == "bearer" || a == "oauth" {
			f.Auth = a
		}
		if n := slugName(q.Get("name")); n != "" {
			f.Name = n
		}
	}
	pickAccount(&f, r.URL.Query().Get("provider"))
	if f.IsAccount() {
		f.Name = "" // an account of the service's one server (accounts.go)
		if st, ok := w.c.serviceServer(f.Card()); ok {
			for _, o := range f.Options() { // one zone for the calendar
				if v := st.Config.option(o.Key); v != "" {
					f.Acct.Values[o.Name] = v
				}
			}
		}
	} else if f.Name != "" {
		// a second account of a kind: a name of its own, not one taken
		// (agenda, then agenda-icloud, agenda-icloud-2…)
		f.Name = w.c.freeServerName(f.Name, f.Picked())
	}
	w.toolFormPage(rw, r, s, f)
}

func (w *web) handleEditTool(rw http.ResponseWriter, r *http.Request, s *session) {
	name := r.PathValue("name")
	srv, ok := w.workspaceServer(name)
	if !ok {
		http.Redirect(rw, r, "/settings/tools", http.StatusSeeOther)
		return
	}
	f := toolFormOf(name, srv)
	if svc, a, ok := w.accountOfRequest(r, name); ok {
		af, editable := w.toolFormOfAccount(name, srv, svc, a)
		if !editable {
			http.Redirect(rw, r, "/settings/tools", http.StatusSeeOther) // an account signed in: its card
			return
		}
		f = af
	} else if _, _, isSvc := accountsOf(srv); isSvc {
		http.Redirect(rw, r, "/settings/tools", http.StatusSeeOther) // its accounts have the forms
		return
	}
	if p := r.URL.Query().Get("provider"); p != "" {
		pickAccount(&f, p)
	}
	if v, ok := w.toolAccessOf(w.lang(r), name, f.Account); ok {
		f.AccessView = &v
	}
	f.Reauth = w.reauthLevels(name, srv)
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
	fail := func(err error) { w.toolFormAgain(rw, r, s, f, name, old, err) }
	if mode == putUpdate && old == nil {
		if w.c.source(kindServer, name) == sourceConfig {
			fail(errReadOnly(kindServer, name))
		} else {
			fail(errNotFound(kindServer, name))
		}
		return
	}
	if f.IsAccount() {
		w.saveAccount(rw, r, s, f, name, old) // web_accounts.go
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
	// A new level its credential cannot serve signs in again, for a token
	// of that level.
	plainSave := r.PostFormValue("connect") == "" && old != nil && old.authKind() == "oauth" &&
		!slices.Contains(w.reauthLevels(name, *old), srv.level())
	if srv.authKind() == "oauth" && srv.Auth.OAuth.RedirectURI == "" && !plainSave {
		w.connectTool(rw, r, s, f, mode, name, srv, creds, fail)
		return
	}
	if err := w.c.putServer(mode, name, srv, creds); err != nil {
		fail(err)
		return
	}
	log.Printf("web: tool %s saved", name)
	w.notify()
	if f.InDialog {
		w.toolSaved(rw, r, name, map[bool]string{false: "added", true: "edited"}[f.Edit])
		return
	}
	// the list, the tool's row followed (web_tools_dialog.go)
	next := "/settings/tools?saved=" + url.QueryEscape(name) + "&tool=" + url.QueryEscape(name)
	if !f.Edit {
		next += "&new=1"
	}
	rw.Header().Set("HX-Redirect", next)
	rw.WriteHeader(http.StatusOK)
}

// connectTool saves an OAuth tool by signing in: it stays pending until
// the service authorizes Eriphe, unless it connects without a consent.
func (w *web) connectTool(rw http.ResponseWriter, r *http.Request, s *session, f toolForm, mode, name string, srv Server, creds credentials, fail func(error)) {
	if !w.atAddress(rw, r) {
		return
	}
	authURL, err := w.c.startPending(r.Context(), mode, name, srv, creds, w.callbackURL(r), w.oauthBinding(rw, r))
	if err != nil {
		fail(connectError(err))
		return
	}
	log.Printf("web: tool %s pending its authorization", name)
	if authURL == "" { // connected without one: saved
		w.notify()
		if f.InDialog {
			w.toolSaved(rw, r, name, "connected")
			return
		}
		authURL = "/settings/tools?connected=" + url.QueryEscape(name)
	} else if f.InDialog {
		w.leavingStep(rw, r, s, f, authURL)
		return
	}
	rw.Header().Set("HX-Redirect", authURL)
	rw.WriteHeader(http.StatusOK)
}

// toolFormAgain shows a refused tool form again, with why. On an edit, it
// says again which secrets the tool has; a secret typed is never sent back.
func (w *web) toolFormAgain(rw http.ResponseWriter, r *http.Request, s *session, f toolForm, name string, old *Server, err error) {
	f.Error = w.formError(r, err)
	if old != nil {
		w.keepToolSecrets(r, &f, name, *old)
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

// keepToolSecrets marks, on the form of an edit, the secrets the tool
// already has, and its access and sign-in state.
func (w *web) keepToolSecrets(r *http.Request, f *toolForm, name string, old Server) {
	back := toolFormOf(name, old)
	f.TokenSet, f.ClientIDSet, f.ClientSecretSet = back.TokenSet, back.ClientIDSet, back.ClientSecretSet
	f.OrigOAuth, f.Reauth = back.OrigOAuth, w.reauthLevels(name, old)
	if v, ok := w.toolAccess(w.lang(r), name); ok {
		f.AccessView = &v
	}
	for i := range f.Env {
		for _, o := range back.Env {
			if o.Name == f.Env[i].Name && f.Env[i].Secret {
				f.Env[i].IsSet, f.Env[i].Value = o.IsSet, ""
			}
		}
	}
}

func (w *web) handleDeleteTool(rw http.ResponseWriter, r *http.Request, s *session) {
	name := r.PathValue("name")
	if r.PostFormValue("account") != "" {
		w.deleteAccount(rw, r, name) // web_accounts.go
		return
	}
	if err := w.c.deleteServer(name); err != nil {
		w.toast(rw, w.formError(r, err), false)
		return
	}
	log.Printf("web: tool %s removed", name)
	w.notify()
	rw.Header().Set("HX-Redirect", "/settings/tools?removed="+url.QueryEscape(name))
	rw.WriteHeader(http.StatusOK)
}

// accountPassword is the password typed for a mailbox or calendar account,
// without the spaces of a provider that shows it in groups (SpacedPassword).
func accountPassword(r *http.Request, f toolForm) string {
	pw := r.PostFormValue("password")
	if s := f.Shape(); s != nil {
		return s.password(f.Acct, pw)
	}
	return pw
}
