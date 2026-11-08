package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
)

// The accounts of the mail and of the calendar in the tools list: their
// service is one server (accounts.go), but the list shows a row for each
// account, as before, with its own state, access level, actions allowed
// without asking, form and removal. A mailbox or a calendar on a password
// is changed with its form; one on a Google account, from that account's
// page (web_google.go).

// accountPreset is the preset an account comes from: the Google account
// for one on its token, else the service's.
func accountPreset(svc accountService, a svcAccount) toolPreset {
	id := svc.ID
	if a.google() {
		id = "google"
	}
	p, _ := presetByID(id)
	return p
}

// accountDetail is an account's line: "Gmail · sam@example.org".
func accountDetail(lang string, svc accountService, a svcAccount) string {
	switch {
	case a.google():
		return tr(lang, "google."+svc.Google) + " · " + a.Name
	case svc == mailService:
		f, _ := mailFormOf(a)
		return tr(lang, "mailp."+f.Provider) + " · " + a.Address
	}
	return tr(lang, "calp."+a.Provider) + " · " + a.Username
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
		t.Google, t.Reconnect = "", false
		if g, ok := w.c.googleAccount(a.Name); ok && a.google() {
			t.Google, t.Reconnect = g.Email, g.Reconnect != ""
		}
		t.Scope = tokenScope{}
		t.Part, t.Label = svc.ID, tr(lang, "acct.svc."+svc.ID)
		t.Owner, t.Provider = accountOwner(svc, a)
		t.Mark, t.Detail = svc.ID, accountProduct(lang, svc, a)
		out = append(out, t)
	}
	return out
}

// accountOwner is the account a mailbox or a calendar is of, for its card:
// its address, and who gives it (a Gmail on a password is the Google
// account's mailbox all the same).
func accountOwner(svc accountService, a svcAccount) (address, provider string) {
	address, provider = a.Name, a.Provider
	switch {
	case a.google():
		return a.Name, "google"
	case svc == mailService:
		f, _ := mailFormOf(a)
		address, provider = cmpOr(a.Address, a.Name), f.Provider
	case strings.Contains(a.Username, "@"):
		address = a.Username
	}
	if provider == "gmail" {
		provider = "google"
	}
	return address, cmpOr(provider, "other")
}

// accountProduct is the line of a service in its account's card: what it
// is at its provider ("Gmail", "Google Agenda", "iCloud").
func accountProduct(lang string, svc accountService, a svcAccount) string {
	switch {
	case a.google():
		return tr(lang, "google."+svc.Google)
	case svc == mailService:
		f, _ := mailFormOf(a)
		return tr(lang, "mailp."+f.Provider)
	}
	return tr(lang, "calp."+a.Provider)
}

// driveRows are the rows of Google Drive: one under each Google account
// whose Drive it reads, with that account's folders.
func (w *web) driveRows(lang string, conf *Config, sv serverView, fc filesConfig) []toolView {
	var out []toolView
	for _, a := range fc.Accounts {
		t := w.toolRow(lang, conf, sv)
		n := 0
		for _, f := range fc.Folders {
			if strings.EqualFold(f.Account, a.Name) || f.Account == "" && len(fc.Accounts) == 1 {
				n++
			}
		}
		t.Drive, t.Part, t.Label, t.Mark = a.Name, googleDrive, tr(lang, "acct.svc."+googleDrive), "gdrive"
		t.Owner, t.Provider = a.Name, "google"
		t.Detail = tr(lang, "preset.gdrive.title") + " · " + driveCount(lang, n)
		t.Google, t.Reconnect = a.Name, false
		if g, ok := w.c.googleAccount(a.Name); ok {
			t.Reconnect = g.Reconnect != ""
		}
		out = append(out, t)
	}
	return out
}

// toolRows are the rows of one server of the list.
func (w *web) toolRows(lang string, conf *Config, sv serverView) []toolView {
	srv := conf.Servers[sv.Name]
	if _, _, ok := accountsOf(srv); ok {
		return w.accountRows(lang, conf, sv)
	}
	if fc, ok := filesConfigOf(srv); ok && len(fc.Accounts) > 0 && w.c.source(kindServer, sv.Name) == sourceWorkspace {
		return w.driveRows(lang, conf, sv, fc)
	}
	return []toolView{w.toolRow(lang, conf, sv)}
}

// Key names the row in its ids: the tool, or the tool and its account.
func (t toolView) Key() string { return rowKey(t.Name, t.rowAccount()) }

// rowAccount is the account the row is of its server: an account of the
// mail or the calendar, or the Google account of a Drive row.
func (t toolView) rowAccount() string { return cmpOr(t.Account, t.Drive) }

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
func (t toolView) Title() string { return cmpOr(t.Label, cmpOr(t.Account, t.Name)) }

// accountOfRequest is the account a request is about (?account=, or the
// form's account), as the server has it; "" for a server without accounts
// or an account it does not hold.
func (w *web) accountOfRequest(r *http.Request, server string) (accountService, svcAccount, bool) {
	svc, cfg, ok := accountsOf(w.c.conf().Servers[server])
	if !ok {
		return accountService{}, svcAccount{}, false
	}
	name := cmpOr(r.URL.Query().Get("account"), r.PostFormValue("account"))
	if i := cfg.index(name); i >= 0 {
		return svc, cfg.Accounts[i], true
	}
	return svc, svcAccount{}, false
}

// toolFormOfAccount fills the form of an account, for an edit; ok is false
// for an account on a Google account (its page, not a form).
func (w *web) toolFormOfAccount(name string, srv Server, svc accountService, a svcAccount) (toolForm, bool) {
	_, cfg, _ := accountsOf(srv)
	f := toolForm{Edit: true, Name: name, Use: srv.Use, Kind: "local", Auth: "none", Account: a.Name,
		Access: svcServer{Server: srv}.level(a.Name), Transport: transportStdio, Egress: true}
	f.Preset, _ = presetByID(svc.ID)
	spec, _ := decodeRun(srv.Run)
	f.SecretSet = a.Auth.PasswordEnv != "" && spec.Env[a.Auth.PasswordEnv].File != ""
	var ok bool
	if svc == mailService {
		f.Mail, ok = mailFormOf(a)
	} else {
		f.Calendar, ok = calendarFormOf(a, cfg.Timezone)
	}
	return f, ok
}

// saveAccount saves the form of a mailbox or a calendar account: one more
// account of its service, or the one edited (name, f.Account).
func (w *web) saveAccount(rw http.ResponseWriter, r *http.Request, s *session, f toolForm, name string, old *Server) {
	fail := func(err error) { w.accountFormAgain(rw, r, s, f, name, old, err) }
	svc := mailService
	if f.Preset.Form == "calendar" {
		svc = calendarService
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
	var a svcAccount
	var err error
	moved := false
	if svc == mailService {
		a, err = f.Mail.account(prev)
		if prev != nil {
			p, _ := mailFormOf(*prev)
			moved = !p.same(f.Mail)
		}
	} else {
		a, err = f.Calendar.account()
		if prev != nil {
			p, _ := calendarFormOf(*prev, "")
			moved = !p.same(f.Calendar)
		}
	}
	if err != nil {
		fail(err)
		return
	}
	pw := accountPassword(r, f)
	if strings.TrimSpace(pw) == "" && (prev == nil || moved || prev.google()) {
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
	if svc == calendarService {
		in.Timezone = f.Calendar.Timezone
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
			switch f.Preset.Form {
			case "mail":
				f.SecretSet = back.SecretSet && back.Mail.same(f.Mail)
			case "calendar":
				f.SecretSet = back.SecretSet && back.Calendar.same(f.Calendar)
			}
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
	if a.google() {
		// the Google account's page takes it away, with its token
		w.toast(rw, tr(w.lang(r), "accounts.googleremove"), false)
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
