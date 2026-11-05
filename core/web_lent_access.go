package main

import (
	"log"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strings"
)

// The accesses of Settings → Tools (lent_access.go): their list, under the
// tools, and a page to add or change one. The secret is typed here, goes
// straight to the vault, and is never shown back.

// accessRow is an access in the list.
type accessRow struct {
	Name, Use, Hosts, Env, Value, Source string
}

// Editable: an access of the configuration file is changed there.
func (a accessRow) Editable() bool { return a.Source == sourceWorkspace }

func (w *web) accessRows() []accessRow {
	conf := w.c.conf()
	var out []accessRow
	for _, name := range slices.Sorted(maps.Keys(conf.Accesses)) {
		a := conf.Accesses[name]
		out = append(out, accessRow{Name: name, Use: a.Use, Hosts: strings.Join(a.Hosts, ", "), Env: a.Env, Value: a.value(),
			Source: w.c.source(kindAccess, name)})
	}
	return out
}

// accessForm is the page of an access: the fields as typed, a line each for
// its hosts and its rules.
type accessForm struct {
	Name, Use, Hosts, Env, Value, Header, Rules string

	Edit  bool
	Error string
}

func accessFormOf(name string, a Access) accessForm {
	return accessForm{Name: name, Use: a.Use, Hosts: strings.Join(a.Hosts, "\n"), Env: a.Env, Value: a.value(), Header: a.Header,
		Rules: strings.Join(a.Rules, "\n"), Edit: true}
}

func readAccessForm(r *http.Request, name string) accessForm {
	f := accessForm{Name: strings.TrimSpace(r.PostFormValue("name")), Use: strings.TrimSpace(r.PostFormValue("use")),
		Hosts: r.PostFormValue("hosts"), Env: strings.TrimSpace(r.PostFormValue("env")), Value: r.PostFormValue("value"),
		Header: strings.TrimSpace(r.PostFormValue("header")), Rules: r.PostFormValue("rules")}
	if name != "" {
		f.Name, f.Edit = name, true
	}
	return f
}

func (f accessForm) access() Access {
	return Access{Use: f.Use, Hosts: lines(f.Hosts), Env: f.Env, Value: f.Value, Header: f.Header, Rules: lines(f.Rules)}
}

func (w *web) handleNewAccess(rw http.ResponseWriter, r *http.Request, s *session) {
	w.render(rw, r, s, "settings", "", page{Title: "lent.add", Nav: "settings", Tab: "tools", View: "lent-access", Data: accessForm{Value: gridApproval}})
}

func (w *web) handleEditAccess(rw http.ResponseWriter, r *http.Request, s *session) {
	name := r.PathValue("name")
	a, ok := w.c.conf().Accesses[name]
	if !ok || w.c.source(kindAccess, name) != sourceWorkspace {
		http.Redirect(rw, r, "/settings/tools", http.StatusSeeOther)
		return
	}
	w.render(rw, r, s, "settings", "", page{Title: "lent.title", Nav: "settings", Tab: "tools", View: "lent-access", Data: accessFormOf(name, a)})
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
	rw.Header().Set("HX-Redirect", "/settings/tools?saved="+url.QueryEscape(f.Name)+"#accesses")
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
	rw.Header().Set("HX-Redirect", "/settings/tools?removed="+url.QueryEscape(name)+"#accesses")
	rw.WriteHeader(http.StatusOK)
}
