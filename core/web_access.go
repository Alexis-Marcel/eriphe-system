package main

import (
	"errors"
	"log"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// A tool's page, its Edit: its grid and exceptions (access_grid.go), how its
// credential follows, and the actions allowed without asking (access.go).
// Those of the configuration file have it too: their grid is the file's,
// their allowed actions the workspace's. Its connection has its form, or
// a page of its own.

// formAccess reads the access level a form posted: one of the levels;
// "read" was the older forms' "reads free", and "trust" the API's.
func formAccess(v string, trust bool) string {
	switch {
	case v == "read":
		return accessReads
	case v != "" && validAccess(v):
		return v
	case trust:
		return accessReads
	}
	return accessAsk
}

// level is a preset's default access: its reads free when its server's
// annotations are vouched for, else everything asked.
func (p toolPreset) level() string {
	if p.Trust {
		return accessReads
	}
	return accessAsk
}

// toolAccess is a tool's access page, and its allowed actions.
type toolAccess struct {
	Name    string
	Account string // the account it is about, on a server of several (accounts.go)
	Preset  toolPreset
	Access  string
	Locked  bool // the configuration file's: its level is changed there
	Auth    string
	Status  string
	Scope   tokenScope
	Allowed []allowedAction
	Saved   bool
	Error   string
	// Its grid, what believing the server's annotations says, and its tools.
	Rows    []gridRow
	Trusted bool
	Tools   []toolException
	Own     map[string]string // its own exceptions, of the tools listed now or not
	// Inherited is what an account has from its server's exceptions.
	Inherited map[string]string
	// Connection is the page of its connection, when it has one.
	Connection string
	// ContentTrusted: the user marked the tool trusted, for the whole
	// server (tool_trust.go); TrustSaved, just changed.
	ContentTrusted bool
	TrustSaved     bool
}

// gridRow is one category of a grid.
type gridRow struct{ Category, Value string }

// toolException is one tool of the exceptions' panel; Value is "" when it
// follows its category.
type toolException struct{ Name, Category, Value string }

func (w *web) toolAccess(lang, name string) (toolAccess, bool) { return w.toolAccessOf(lang, name, "") }

// toolAccessOf is toolAccess for one account of a server of several: its
// level and its allowed actions.
func (w *web) toolAccessOf(lang, name, account string) (toolAccess, bool) {
	srv, ok := w.c.conf().Servers[name]
	if !ok {
		return toolAccess{}, false
	}
	v := toolAccess{Name: name, Access: srv.level(), Locked: w.c.source(kindServer, name) != sourceWorkspace,
		Auth: srv.authKind(), Scope: w.c.tokenScope(name, srv), Allowed: w.c.store.allowedActions(name),
		ContentTrusted: w.c.trustsServer(name)}
	v.Preset, _ = presetOf(lang, srv)
	if svc, cfg, isSvc := accountsOf(srv); isSvc && account != "" {
		i := cfg.index(account)
		if i < 0 {
			return toolAccess{}, false
		}
		a := cfg.Accounts[i]
		v.Account, v.Preset = a.Name, accountPreset(svc, a)
		v.Access, v.Scope = svcServer{Server: srv}.level(a.Name), tokenScope{}
		v.Allowed = w.c.store.accountActions(name, a.Name)
	}
	access, own := srv.baseAccess(), srv.Exceptions
	if v.Account != "" {
		own, v.Inherited = nil, srv.Exceptions
		if a, ok := srv.accountAccess(map[string]any{"account": v.Account}); ok {
			access = a
		}
		for name, g := range srv.AccountGrids {
			if strings.EqualFold(name, v.Account) {
				own = g.Exceptions
			}
		}
	}
	for _, c := range categories {
		v.Rows = append(v.Rows, gridRow{c, access.grid.value(c)})
	}
	v.Trusted, v.Own = access.trusted, own
	if u := w.c.pool.get(name); u != nil {
		var tools []*mcp.Tool
		v.Status, _, tools = u.snapshot()
		for _, t := range tools {
			v.Tools = append(v.Tools, toolException{t.Name, toolCategory(t, access.trusted), access.exceptions[t.Name]})
		}
	}
	for name, value := range own { // a tool not listed now keeps its line: its exception can go
		if !slices.ContainsFunc(v.Tools, func(t toolException) bool { return t.Name == name }) {
			v.Tools = append(v.Tools, toolException{Name: name, Value: value})
		}
	}
	slices.SortFunc(v.Tools, func(a, b toolException) int { return strings.Compare(a.Name, b.Name) })
	v.Connection = connectionPage(name, v.Account, v.Preset, srv.authKind(), w.c.source(kindServer, name))
	return v, true
}

// connectionPage is where a tool's connection is changed: its form, or its
// own page; "" when it has neither.
func connectionPage(name, account string, p toolPreset, auth, source string) string {
	switch {
	case editable(source, p, auth):
		return "/settings/tools/" + url.PathEscape(name) + "/edit" + accountQuery(account)
	case auth == "github-app" || p.ID == "github":
		return "/settings/github"
	case p.Form == "connection":
		return "/settings/tools" // its account's card
	}
	return ""
}

// Key names the access's parts in their ids, as its row does.
func (v toolAccess) Key() string { return rowKey(v.Name, v.Account) }

// Title is whom the access is of: the tool, or the account.
func (v toolAccess) Title() string { return cmpOr(v.Account, v.Name) }

// Query is "?account=…" for an account's access.
func (v toolAccess) Query() string { return accountQuery(v.Account) }

func (w *web) handleToolAccess(rw http.ResponseWriter, r *http.Request, s *session) {
	name := r.PathValue("name")
	v, ok := w.toolAccessOf(w.lang(r), name, r.URL.Query().Get("account"))
	if !ok {
		http.Redirect(rw, r, "/settings/tools", http.StatusSeeOther)
		return
	}
	v.Saved = r.URL.Query().Get("saved") != ""
	w.render(rw, r, s, "settings", "", page{Title: "access.page", Nav: "settings", Tab: "tools", View: "access", Data: v})
}

// formGrid reads the grid a page posted, each category required.
func formGrid(r *http.Request) (accessGrid, error) {
	g := accessGrid{r.PostFormValue("grid-read"), r.PostFormValue("grid-change"), r.PostFormValue("grid-delete"), r.PostFormValue("grid-send")}
	for _, c := range categories {
		if r.PostFormValue("grid-"+c) == "" {
			return g, errInvalid(errors.New("grid: " + c + " has no value"))
		}
	}
	return g, nil
}

// formExceptions reads the exceptions a page posted. Only the tools it
// showed change: a page drawn while the server was reconnecting has none.
// On an account, what its server says stays out of its own exceptions; to
// follow its category instead, it needs that value of its own.
func formExceptions(r *http.Request, g accessGrid, v toolAccess) map[string]string {
	exceptions := map[string]string{}
	maps.Copy(exceptions, v.Own)
	for _, t := range v.Tools {
		key := "exception-" + t.Name
		if !r.PostForm.Has(key) {
			continue
		}
		delete(exceptions, t.Name)
		value := r.PostForm.Get(key)
		if held, ok := v.Inherited[t.Name]; ok {
			if value == held {
				continue
			}
			if value == "" && t.Category != "" {
				value = g.value(t.Category)
			}
		}
		if value != "" {
			exceptions[t.Name] = value
		}
	}
	return exceptions
}

// posted is the page as the user left it, when it could not be saved.
func (v toolAccess) posted(r *http.Request) toolAccess {
	for i, row := range v.Rows {
		v.Rows[i].Value = cmpOr(r.PostFormValue("grid-"+row.Category), row.Value)
	}
	for i, t := range v.Tools {
		if r.PostForm.Has("exception-" + t.Name) {
			v.Tools[i].Value = r.PostForm.Get("exception-" + t.Name)
		}
	}
	v.Trusted = r.PostFormValue("trust") != ""
	return v
}

// saveGrid gives a tool, or one of its accounts, the grid of the page. A
// server of several accounts has no grid of its own: they each do.
func (w *web) saveGrid(r *http.Request, name, account string, srv Server, v toolAccess) error {
	if _, cfg, isSvc := accountsOf(srv); isSvc && account == "" && len(cfg.Accounts) > 0 {
		return errInvalid(errors.New("grid: choose one of its accounts"))
	}
	g, err := formGrid(r)
	if err != nil {
		return err
	}
	exceptions, trusted := formExceptions(r, g, v), r.PostFormValue("trust") != ""
	if err := (Server{Grid: &g, Exceptions: exceptions}).checkAccess(); err != nil {
		return errInvalid(err)
	}
	if account != "" {
		return w.c.setAccountGrid(name, account, accountGrid{Grid: g, Exceptions: exceptions, Trust: &trusted})
	}
	srv.Grid, srv.Exceptions, srv.TrustAnnotations = &g, exceptions, trusted
	srv.Access = ""
	return w.c.putServer(putUpdate, name, srv, nil)
}

// handleSaveToolAccess changes a workspace tool's grid. Going down applies
// at once; going up past what the stored credential allows asks to connect
// again (the page says so).
func (w *web) handleSaveToolAccess(rw http.ResponseWriter, r *http.Request, s *session) {
	name := r.PathValue("name")
	srv, ok := w.workspaceServer(name)
	level := r.PostFormValue("access")
	account := r.URL.Query().Get("account")
	v, found := w.toolAccessOf(w.lang(r), name, account)
	if !found {
		http.Redirect(rw, r, "/settings/tools", http.StatusSeeOther)
		return
	}
	if !ok || (level != "" && !validAccess(level)) {
		err := errReadOnly(kindServer, name)
		if ok {
			err = errInvalid(errors.New("unknown access level"))
		}
		v.Error = w.formError(r, err)
		w.render(rw, r, s, "settings", "tool-access-frag", page{Data: v})
		return
	}
	var err error
	switch {
	case level == "":
		err = w.saveGrid(r, name, account, srv, v)
	case account != "":
		err = w.c.setAccountLevel(name, account, level) // one account of its service (accounts.go)
	default:
		err = w.c.putServer(putUpdate, name, srv.withLevel(level), nil)
	}
	if err != nil {
		if level == "" {
			v = v.posted(r)
		}
		v.Error = w.formError(r, err)
		w.render(rw, r, s, "settings", "tool-access-frag", page{Data: v})
		return
	}
	log.Printf("web: tool %s: its access changed", name)
	w.notify()
	q := "?saved=1"
	if account != "" {
		q = accountQuery(account) + "&saved=1"
	}
	rw.Header().Set("HX-Redirect", "/settings/tools/"+url.PathEscape(name)+"/access"+q)
	rw.WriteHeader(http.StatusOK)
}

// handleToolTrust marks a tool trusted or not (tool_trust.go), any tool of
// the catalog, the configuration file's too: it is the user's to say.
func (w *web) handleToolTrust(rw http.ResponseWriter, r *http.Request, s *session) {
	name := r.PathValue("name")
	if _, ok := w.c.conf().Servers[name]; !ok || r.PostFormValue("set") == "" {
		http.Error(rw, "unknown tool", http.StatusBadRequest)
		return
	}
	trusted := r.PostFormValue("trusted") != ""
	if err := w.c.setServerTrust(name, trusted, trustByUser); err != nil {
		w.toast(rw, err.Error(), false)
		return
	}
	log.Printf("web: tool %s: trusted %v", name, trusted)
	w.notify()
	v, ok := w.toolAccessOf(w.lang(r), name, r.URL.Query().Get("account"))
	if !ok {
		http.Redirect(rw, r, "/settings/tools", http.StatusSeeOther)
		return
	}
	v.TrustSaved = true
	w.render(rw, r, s, "settings", "tool-trust-frag", page{Data: v})
}

// handleForgetAction removes an allowed action: the tool asks again.
func (w *web) handleForgetAction(rw http.ResponseWriter, r *http.Request, s *session) {
	name := r.PathValue("name")
	if err := w.c.store.forgetAction(name, r.PostFormValue("tool"), r.PostFormValue("account")); err != nil {
		w.toast(rw, err.Error(), false)
		return
	}
	log.Printf("web: tool %s: an allowed action removed", name)
	v, ok := w.toolAccessOf(w.lang(r), name, r.PostFormValue("account"))
	if !ok {
		w.toast(rw, tr(w.lang(r), "allowed.removed"), true)
		return
	}
	rw.Header().Set("HX-Trigger", hxTrigger(tr(w.lang(r), "allowed.removed"), true, false))
	w.render(rw, r, s, "settings", "allowed-actions-frag", page{Data: v})
}
