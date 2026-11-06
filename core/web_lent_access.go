package main

import (
	"errors"
	"log"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"golang.org/x/crypto/ssh"
)

// The accesses of Settings → Tools (lent_access.go): their list, under the
// tools, and a page to add or change one. The secret is typed here, goes
// straight to the vault, and is never shown back.

// accessRow is an access in the list.
type accessRow struct {
	Name, Use, Hosts, Env, Value, Source string

	Untested bool // an SSH access with no host key: it cannot be lent yet
}

// Editable: an access of the configuration file is changed there.
func (a accessRow) Editable() bool { return a.Source == sourceWorkspace }

func (w *web) accessRows() []accessRow {
	conf := w.c.conf()
	var out []accessRow
	for _, name := range slices.Sorted(maps.Keys(conf.Accesses)) {
		a := conf.Accesses[name]
		row := accessRow{Name: name, Use: a.Use, Hosts: strings.Join(a.Hosts, ", "), Env: a.Env, Value: a.value(),
			Source: w.c.source(kindAccess, name)}
		if a.isSSH() {
			row.Hosts, row.Untested = a.User+"@"+a.sshTarget(), a.HostKey == ""
		}
		out = append(out, row)
	}
	return out
}

// accessForm is the page of an access: the fields as typed, a line each for
// its hosts and its rules.
type accessForm struct {
	Name, Use, Hosts, Env, Value, Header, Rules string

	// An SSH access: its account and host key as typed; its public key and
	// the host key's fingerprint, to show.
	Kind, User, HostKey, PublicKey, Fingerprint string

	Edit   bool
	Tested bool
	Error  string
}

func (f accessForm) SSH() bool { return f.Kind == accessSSH }

func accessFormOf(name string, a Access) accessForm {
	f := accessForm{Name: name, Use: a.Use, Hosts: strings.Join(a.Hosts, "\n"), Env: a.Env, Value: a.value(), Header: a.Header,
		Rules: strings.Join(a.Rules, "\n"), Kind: a.Kind, User: a.User, HostKey: a.HostKey, Edit: true}
	f.showSSH(a)
	return f
}

// showSSH fills what the page of an SSH access shows of it.
func (f *accessForm) showSSH(a Access) {
	if !a.isSSH() {
		return
	}
	if public, err := a.sshPublicKey(f.Name); err == nil {
		f.PublicKey = public
	}
	if k, _, _, _, err := ssh.ParseAuthorizedKey([]byte(a.HostKey)); err == nil {
		f.Fingerprint = ssh.FingerprintSHA256(k)
	}
}

func readAccessForm(r *http.Request, name string) accessForm {
	f := accessForm{Name: strings.TrimSpace(r.PostFormValue("name")), Use: strings.TrimSpace(r.PostFormValue("use")),
		Hosts: r.PostFormValue("hosts"), Env: strings.TrimSpace(r.PostFormValue("env")), Value: r.PostFormValue("value"),
		Header: strings.TrimSpace(r.PostFormValue("header")), Rules: r.PostFormValue("rules"),
		Kind: r.PostFormValue("kind"), User: strings.TrimSpace(r.PostFormValue("user")), HostKey: strings.TrimSpace(r.PostFormValue("host_key"))}
	if name != "" {
		f.Name, f.Edit = name, true
	}
	return f
}

func (f accessForm) access() Access {
	if f.SSH() {
		key := f.HostKey
		if k, err := normalHostKey(key); err == nil {
			key = k // else refused as it is
		}
		return Access{Kind: accessSSH, Use: f.Use, Hosts: lines(f.Hosts), Value: f.Value, User: f.User, HostKey: key}
	}
	return Access{Use: f.Use, Hosts: lines(f.Hosts), Env: f.Env, Value: f.Value, Header: f.Header, Rules: lines(f.Rules)}
}

func (w *web) handleNewAccess(rw http.ResponseWriter, r *http.Request, s *session) {
	kind, title := "", "lent.add"
	if r.URL.Query().Get("kind") == accessSSH {
		kind, title = accessSSH, "lent.addssh"
	}
	w.render(rw, r, s, "settings", "", page{Title: title, Nav: "settings", Tab: "tools", View: "lent-access", Data: accessForm{Value: gridApproval, Kind: kind}})
}

func (w *web) handleEditAccess(rw http.ResponseWriter, r *http.Request, s *session) {
	name := r.PathValue("name")
	a, ok := w.c.conf().Accesses[name]
	if !ok || w.c.source(kindAccess, name) != sourceWorkspace {
		http.Redirect(rw, r, "/settings/tools", http.StatusSeeOther)
		return
	}
	f := accessFormOf(name, a)
	f.Tested = r.URL.Query().Get("tested") != ""
	w.render(rw, r, s, "settings", "", page{Title: "lent.title", Nav: "settings", Tab: "tools", View: "lent-access", Data: f})
}

func (w *web) handleSaveAccess(rw http.ResponseWriter, r *http.Request, s *session) {
	f := readAccessForm(r, r.PathValue("name"))
	mode := putCreate
	if f.Edit {
		mode = putUpdate
	}
	if err := w.c.putAccess(mode, f.Name, f.access(), strings.TrimSpace(r.PostFormValue("secret"))); err != nil {
		f.Error = w.formError(r, err)
		w.render(rw, r, s, "settings", "lent-access-form-frag", page{Data: f})
		return
	}
	log.Printf("web: access %s saved", f.Name)
	w.notify()
	if f.SSH() {
		// Its public key, to put on the machine, and the test.
		rw.Header().Set("HX-Redirect", "/settings/accesses/"+url.PathEscape(f.Name)+"/edit")
		rw.WriteHeader(http.StatusOK)
		return
	}
	rw.Header().Set("HX-Redirect", "/settings/tools?savedaccess="+url.QueryEscape(f.Name)+"#accesses")
	rw.WriteHeader(http.StatusOK)
}

func (w *web) handleDeleteAccess(rw http.ResponseWriter, r *http.Request, s *session) {
	name := r.PathValue("name")
	if err := w.c.deleteAccess(name); err != nil {
		w.toast(rw, w.formError(r, err), false)
		return
	}
	log.Printf("web: access %s removed", name)
	w.notify()
	rw.Header().Set("HX-Redirect", "/settings/tools?removedaccess="+url.QueryEscape(name)+"#accesses")
	rw.WriteHeader(http.StatusOK)
}

// handleTestAccess connects once to an SSH access's machine. Its page is
// loaded again when it worked, for the form to hold the host key recorded.
func (w *web) handleTestAccess(rw http.ResponseWriter, r *http.Request, s *session) {
	name := r.PathValue("name")
	found, err := w.c.testSSH(r.Context(), name)
	if err == nil {
		rw.Header().Set("HX-Redirect", "/settings/accesses/"+url.PathEscape(name)+"/edit?tested=1")
		rw.WriteHeader(http.StatusOK)
		return
	}
	log.Printf("web: access %s: test: %v", name, err)
	key := "lent.test.failed"
	switch {
	case errors.Is(err, errSSHHostKey):
		key = "lent.test.hostkey"
	case errors.Is(err, errSSHRefused):
		key = "lent.test.refused"
	case errors.Is(err, errSSHUnreachable):
		key = "lent.test.unreachable"
	}
	w.render(rw, r, s, "settings", "lent-ssh-test", page{Data: map[string]string{"Key": key, "Found": found}})
}

// handleWebValue sets the web's value and shows the tools again.
func (w *web) handleWebValue(rw http.ResponseWriter, r *http.Request, s *session) {
	if err := w.c.setWebValue(r.PostFormValue("value")); err != nil {
		http.Error(rw, err.Error(), http.StatusBadRequest)
		return
	}
	log.Printf("web: the web is %s", r.PostFormValue("value"))
	w.handleTools(rw, r, s)
}
