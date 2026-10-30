package main

import (
	"errors"
	"log"
	"net/http"
	"net/url"
)

// A tool's access: what Eriphe may do on it, how its credential follows,
// and the actions allowed without asking (access.go). A tool changed with
// its form has them in it; the others have this page, those of the
// configuration file too: their level is the file's, their allowed actions
// the workspace's.

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
	Preset  toolPreset
	Access  string
	Locked  bool // the configuration file's: its level is changed there
	Auth    string
	Status  string
	Scope   tokenScope
	Allowed []allowedAction
	Saved   bool
	Error   string
}

func (w *web) toolAccess(lang, name string) (toolAccess, bool) {
	srv, ok := w.c.conf().Servers[name]
	if !ok {
		return toolAccess{}, false
	}
	v := toolAccess{Name: name, Access: srv.level(), Locked: w.c.source(kindServer, name) != sourceWorkspace,
		Auth: srv.authKind(), Scope: w.c.tokenScope(name, srv), Allowed: w.c.store.allowedActions(name)}
	v.Preset, _ = presetOf(lang, srv)
	if u := w.c.pool.get(name); u != nil {
		v.Status, _, _ = u.snapshot()
	}
	return v, true
}

func (w *web) handleToolAccess(rw http.ResponseWriter, r *http.Request, s *session) {
	name := r.PathValue("name")
	v, ok := w.toolAccess(w.lang(r), name)
	if !ok {
		http.Redirect(rw, r, "/settings/tools", http.StatusSeeOther)
		return
	}
	// A tool changed with its form: its access is part of it.
	if srv := w.c.conf().Servers[name]; editable(w.c.source(kindServer, name), v.Preset, srv.authKind()) {
		http.Redirect(rw, r, "/settings/tools/"+url.PathEscape(name)+"/edit#form-access", http.StatusSeeOther)
		return
	}
	v.Saved = r.URL.Query().Get("saved") != ""
	w.render(rw, r, s, "settings", "", page{Title: "access.page", Nav: "settings", Tab: "tools", View: "access", Data: v})
}

// handleSaveToolAccess changes a workspace tool's level. Going down
// applies at once; going up past what the stored credential allows asks
// to connect again (the page says so).
func (w *web) handleSaveToolAccess(rw http.ResponseWriter, r *http.Request, s *session) {
	name := r.PathValue("name")
	srv, ok := w.workspaceServer(name)
	level := r.PostFormValue("access")
	if !ok || level == "" || !validAccess(level) {
		v, found := w.toolAccess(w.lang(r), name)
		if !found {
			http.Redirect(rw, r, "/settings/tools", http.StatusSeeOther)
			return
		}
		err := errReadOnly(kindServer, name)
		if ok {
			err = errInvalid(errors.New("unknown access level"))
		}
		v.Error = w.formError(r, err)
		w.render(rw, r, s, "settings", "tool-access-frag", page{Data: v})
		return
	}
	if err := w.c.putServer(putUpdate, name, srv.withLevel(level), nil); err != nil {
		v, _ := w.toolAccess(w.lang(r), name)
		v.Access, v.Error = level, w.formError(r, err)
		w.render(rw, r, s, "settings", "tool-access-frag", page{Data: v})
		return
	}
	log.Printf("web: tool %s set to %s", name, level)
	w.notify()
	rw.Header().Set("HX-Redirect", "/settings/tools/"+url.PathEscape(name)+"/access?saved=1")
	rw.WriteHeader(http.StatusOK)
}

// handleForgetAction removes an allowed action: the tool asks again.
func (w *web) handleForgetAction(rw http.ResponseWriter, r *http.Request, s *session) {
	name := r.PathValue("name")
	if err := w.c.store.forgetAction(name, r.PostFormValue("tool")); err != nil {
		w.toast(rw, err.Error(), false)
		return
	}
	log.Printf("web: tool %s: an allowed action removed", name)
	v, ok := w.toolAccess(w.lang(r), name)
	if !ok {
		w.toast(rw, tr(w.lang(r), "allowed.removed"), true)
		return
	}
	rw.Header().Set("HX-Trigger", hxTrigger(tr(w.lang(r), "allowed.removed"), true, false))
	w.render(rw, r, s, "settings", "allowed-actions-frag", page{Data: v})
}
