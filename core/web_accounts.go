package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"slices"
	"strings"
)

// The accounts of the mail and of the calendar in the tools list: their
// service is one server (accounts.go), but the list shows a row for each
// account, as before, with its own state, access level, actions allowed
// without asking, form and removal. A mailbox or a calendar on a password
// is changed with its form; one signed in, from its account's card
// (web_connections.go).

// accountProvider is the provider an account signed in is at, or "".
func accountProvider(svc toolCard, a svcAccount) string {
	if t, ok := signInByAccount(svc.ID, a.Provider); ok && a.signedIn() {
		return t.Provider
	}
	return ""
}

// accountPreset is the preset an account comes from: its provider's for
// one signed in, else the service's.
func accountPreset(svc toolCard, a svcAccount) toolPreset {
	if p, ok := connectionPreset(accountProvider(svc, a), svc.ID); ok {
		return p
	}
	p, _ := presetByID(svc.ID)
	return p
}

// accountDetail is an account's line: "Gmail · sam@example.org".
func accountDetail(lang string, svc toolCard, a svcAccount) string {
	if a.signedIn() || svc.Account == nil {
		return productName(lang, accountProvider(svc, a), svc.ID) + " · " + a.Name
	}
	return accountProduct(lang, svc, a) + " · " + cmpOr(a.get(svc.Account.key(svc.Account.NameBy)), a.Name)
}

// rowKey is the part of an account's row ids that names it: a short hash
// of its name, which holds dots and @.
func rowKey(server, account string) string {
	if account == "" {
		return server
	}
	h := sha256.Sum256([]byte(strings.ToLower(account)))
	return server + "-" + hex.EncodeToString(h[:4])
}

// placesOfAccount are the places of a server that are in one of its
// accounts; one without an account is in the only one.
func placesOfAccount(name string, srv Server, account string, accounts int) []Place {
	return slices.DeleteFunc(placesIn(name, srv), func(p Place) bool {
		return !strings.EqualFold(p.Account, account) && (p.Account != "" || accounts != 1)
	})
}

// accountRows are the rows of a server of accounts: one for each.
func (w *web) accountRows(lang string, conf *Config, sv serverView) []toolView {
	srv := conf.Servers[sv.Name]
	svc, cfg, ok := accountsOf(srv)
	if !ok || len(cfg.Accounts) == 0 {
		return []toolView{w.toolRow(lang, conf, sv)}
	}
	var out []toolView
	for _, a := range cfg.Accounts {
		t := w.toolRow(lang, conf, sv)
		t.Account, t.Preset, t.Detail = a.Name, accountPreset(svc, a), accountDetail(lang, svc, a)
		st := svcServer{Name: sv.Name, Server: srv}
		t.Access = st.level(a.Name)
		t.Allowed = len(w.c.store.accountActions(sv.Name, a.Name))
		t.Connection, t.ConnProvider, t.Reconnect = "", "", false
		if g, ok := w.c.connectionBySecret(svcServer{Name: sv.Name, Server: srv}, a); ok {
			t.Connection, t.ConnProvider, t.Reconnect = g.Identity, g.Provider, g.Lapsed != ""
		}
		t.Scope = tokenScope{}
		t.Part, t.Label = svc.ID, tr(lang, "acct.svc."+svc.ID)
		t.Owner, t.Provider = accountOwner(svc, a)
		t.Mark, t.Detail = svc.mark(), accountProduct(lang, svc, a)
		if key := svc.placesKey(); key != "" {
			t.Detail += " · " + placeCount(lang, len(placesOfAccount(sv.Name, srv, a.Name, len(cfg.Accounts))))
		}
		out = append(out, t)
	}
	return out
}

// accountOwner is the account a mailbox or a calendar is of, for its card:
// its address, and who gives it (a Gmail on a password is the mailbox of
// the account signed in at its provider all the same).
func accountOwner(svc toolCard, a svcAccount) (address, provider string) {
	address, provider = a.Name, a.Provider
	switch s := svc.Account; {
	case a.signedIn() || s == nil:
		return a.Name, cmpOr(accountProvider(svc, a), a.Provider)
	default:
		provider = cmpOr(s.choiceOf(a).ID, a.Provider)
		if login := a.get(s.key(s.NameBy)); strings.Contains(login, "@") {
			address = login
		}
	}
	if t, ok := signInByAccount(svc.ID, provider); ok {
		provider = t.Provider
	}
	return address, cmpOr(provider, "other")
}

// accountProduct is the line of a service in its account's card: what it
// is at its provider ("Gmail", "Google Agenda", "iCloud").
func accountProduct(lang string, svc toolCard, a svcAccount) string {
	if a.signedIn() || svc.Account == nil {
		return productName(lang, accountProvider(svc, a), svc.ID)
	}
	return tr(lang, svc.Account.Words+"."+svc.Account.choiceOf(a).ID)
}

// toolRows are the rows of one server of the list.
func (w *web) toolRows(lang string, conf *Config, sv serverView) []toolView {
	srv := conf.Servers[sv.Name]
	if _, _, ok := accountsOf(srv); ok {
		return w.accountRows(lang, conf, sv)
	}
	return []toolView{w.toolRow(lang, conf, sv)}
}

// Key names the row in its ids: the tool, or the tool and its account.
func (t toolView) Key() string { return rowKey(t.Name, t.rowAccount()) }

// rowAccount is the account the row is of its server, "" for a tool's.
func (t toolView) rowAccount() string { return t.Account }

// accountQuery is "?account=…" for an account's row, "" for a tool's.
func accountQuery(account string) string {
	if account == "" {
		return ""
	}
	return "?account=" + url.QueryEscape(account)
}

// EditURL is the row's form.
func (t toolView) EditURL() string {
	return "/settings/tools/" + t.Name + "/edit" + accountQuery(t.Account)
}

// EditHash is the row's form in the tools page's dialog: edit/mail, or
// edit/mail~sam@example.org for an account (app.js).
func (t toolView) EditHash() string { return editHash(t.Name, t.Account) }

func editHash(name, account string) string {
	if account == "" {
		return "edit/" + name
	}
	return "edit/" + name + "~" + account
}

// RowURL is the row alone, asked again while it is followed.
func (t toolView) RowURL() string {
	return "/settings/tools/" + t.Name + "/row" + accountQuery(t.rowAccount())
}

// Title is the row's heading: in an account's card, its service ("Mail");
// else the tool's name.
func (t toolView) Title() string { return cmpOr(t.Named, cmpOr(t.Label, cmpOr(t.Account, t.Name))) }

// accountOfRequest is the account a request is about (?account=, or the
// form's account), as the server has it; "" for a server without accounts
// or an account it does not hold.
func (w *web) accountOfRequest(r *http.Request, server string) (toolCard, svcAccount, bool) {
	svc, cfg, ok := accountsOf(w.c.conf().Servers[server])
	if !ok {
		return toolCard{}, svcAccount{}, false
	}
	name := cmpOr(r.URL.Query().Get("account"), r.PostFormValue("account"))
	if i := cfg.index(name); i >= 0 {
		return svc, cfg.Accounts[i], true
	}
	return svc, svcAccount{}, false
}

// toolFormOfAccount fills the form of an account, for an edit; ok is false
// for an account signed in at a provider (its card, not a form).
func (w *web) toolFormOfAccount(name string, srv Server, svc toolCard, a svcAccount) (toolForm, bool) {
	_, cfg, _ := accountsOf(srv)
	f := toolForm{Edit: true, Name: name, Use: srv.Use, Kind: "local", Auth: "none", Account: a.Name,
		Access: svcServer{Server: srv}.level(a.Name), Transport: transportStdio, Egress: true}
	f.Preset, _ = presetByID(svc.ID)
	spec, _ := decodeRun(srv.Run)
	f.SecretSet = a.Auth.PasswordEnv != "" && spec.Env[a.Auth.PasswordEnv].File != ""
	s := svc.Account
	if s == nil || f.Preset.Form != "account" {
		return f, false
	}
	var ok bool
	f.Acct, ok = s.formOf(a)
	for _, o := range s.Server {
		f.Acct.Values[o.Name] = cmpOr(cfg.option(o.Key), o.Default)
	}
	return f, ok
}

// saveAccount saves the form of an account of a tool with a card: one
// more account of its service, or the one edited (name, f.Account).
func (w *web) saveAccount(rw http.ResponseWriter, r *http.Request, s *session, f toolForm, name string, old *Server) {
	fail := func(err error) { w.accountFormAgain(rw, r, s, f, name, old, err) }
	svc, shape := f.Card(), f.Shape()
	if shape == nil {
		fail(errInvalid(errors.New("no account form")))
		return
	}
	var prev *svcAccount
	if f.Edit {
		st, ok := w.c.serviceServer(svc)
		a, found := st.account(f.Account)
		if !ok || st.Name != name || !found {
			fail(errAccountUnknown)
			return
		}
		prev = &a
	}
	if err := shape.normalize(&f.Acct, prev); err != nil {
		fail(err)
		return
	}
	a := shape.account(f.Acct)
	moved := false
	if prev != nil {
		p, _ := shape.formOf(*prev)
		moved = !shape.same(p, f.Acct)
	}
	pw := accountPassword(r, f)
	if strings.TrimSpace(pw) == "" && (prev == nil || moved || prev.signedIn()) {
		if moved {
			fail(errors.New("form.newpassword"))
		} else {
			fail(errors.New("form.nopassword"))
		}
		return
	}
	in := accountPut{Lang: w.lang(r), Account: a, Level: f.Access, Password: pw}
	if prev != nil {
		in.Replace = prev.Name
	}
	in.Options = map[string]string{}
	for _, o := range shape.Server {
		in.Options[o.Name] = f.Acct.Values[o.Name]
	}
	if err := w.c.putAccount(svc, in); err != nil {
		fail(err)
		return
	}
	st, _ := w.c.serviceServer(svc)
	log.Printf("web: an account of %s saved", st.Name) // never the address
	w.notify()
	key := rowKey(st.Name, a.Name)
	if f.InDialog {
		w.accountSaved(rw, r, key, a.Name, map[bool]string{false: "added", true: "edited"}[f.Edit])
		return
	}
	next := "/settings/tools?saved=" + url.QueryEscape(st.Name) + "&tool=" + url.QueryEscape(key)
	if !f.Edit {
		next += "&new=1"
	}
	rw.Header().Set("HX-Redirect", next)
	rw.WriteHeader(http.StatusOK)
}

// accountSaved ends the dialog after an account's save, as toolSaved does
// for a tool: the toast names the account.
func (w *web) accountSaved(rw http.ResponseWriter, r *http.Request, key, account, kind string) {
	ev := map[string]any{"name": key, "new": kind != "edited",
		"message": fmt.Sprintf(tr(w.lang(r), "saved."+kind), account)}
	rw.Header().Set("HX-Trigger", hxEvents(map[string]any{"tool-saved": ev}))
	rw.Header().Set("HX-Reswap", "none")
	rw.WriteHeader(http.StatusOK)
}

// accountFormAgain shows a refused account form again, with why.
func (w *web) accountFormAgain(rw http.ResponseWriter, r *http.Request, s *session, f toolForm, name string, old *Server, err error) {
	f.Error = w.formError(r, err)
	if old != nil && f.Account != "" {
		if svc, a, ok := w.accountOfRequest(r, name); ok {
			back, _ := w.toolFormOfAccount(name, *old, svc, a)
			f.SecretSet = back.SecretSet && svc.Account != nil && svc.Account.same(back.Acct, f.Acct)
			f.Switched = !f.SecretSet && back.SecretSet
			if v, ok := w.toolAccessOf(w.lang(r), name, a.Name); ok {
				f.AccessView = &v
			}
		}
	}
	w.render(rw, r, s, "settings", "tool-form", page{Data: f})
}

// deleteAccount takes an account out of its service (POST
// /settings/tools/{name}/delete with account).
func (w *web) deleteAccount(rw http.ResponseWriter, r *http.Request, name string) {
	svc, a, ok := w.accountOfRequest(r, name)
	if !ok {
		w.toast(rw, w.formError(r, errAccountUnknown), false)
		return
	}
	if a.signedIn() {
		// the Google account's page takes it away, with its token
		w.toast(rw, tr(w.lang(r), "accounts.connremove"), false)
		return
	}
	if err := w.c.dropAccount(svc, a.Name); err != nil {
		w.toast(rw, w.formError(r, err), false)
		return
	}
	log.Printf("web: an account of %s removed", name)
	w.notify()
	rw.Header().Set("HX-Redirect", "/settings/tools?removedaccount="+url.QueryEscape(a.Name))
	rw.WriteHeader(http.StatusOK)
}
