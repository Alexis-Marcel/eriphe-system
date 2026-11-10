package main

import (
	"log"
	"net/http"
	"slices"
	"strings"
)

// Connected accounts in Settings → Tools (connection_tools.go): sign an
// account in for the tools ticked, add a tool to it, reconnect it with the
// tools it has, or remove it with its tools. The tools list shows each
// account as a card (web_tools_groups.go); these are its buttons.

func (w *web) connectionRoutes(mux *http.ServeMux) {
	mux.Handle("POST /settings/connections/{provider}/connect", w.auth(w.handleConnectionConnect))
	mux.Handle("POST /settings/connections/{provider}/remove", w.auth(w.handleConnectionRemove))
}

// connectionBacks are the pages a sign-in may go back to.
var connectionBacks = []string{"/settings/tools", "/settings/google", "/settings/drive"}

// reconnectAsk is what reconnecting an account asks for: the tools it
// has, or else the ones its scopes serve, with the rights they had.
func (c *core) reconnectAsk(cn connection) connectAsk {
	in := connectAsk{Provider: cn.Provider, Hint: cn.Identity}
	for _, t := range c.connectionKinds(cn) {
		if !slices.Contains(in.Tools, t.Kind) {
			in.Tools = append(in.Tools, t.Kind)
		}
	}
	if len(in.Tools) == 0 { // its tools are gone: the ones its scopes serve
		for _, k := range toolsAt(cn.Provider) {
			if t, _ := signInOf(k, cn.Provider); func() bool { ok, _ := t.granted(cn.Scopes); return ok }() {
				in.Tools = append(in.Tools, k)
			}
		}
	}
	for _, k := range in.Tools {
		t, _ := signInOf(k, cn.Provider)
		if _, write := t.granted(cn.Scopes); write && len(t.Write) > 0 {
			in.Write = true
		}
	}
	return in
}

// handleConnectionConnect signs an account in for the tools ticked (tool=,
// several), or reconnects one (account= and reconnect=).
func (w *web) handleConnectionConnect(rw http.ResponseWriter, r *http.Request, s *session) {
	r.ParseForm()
	provider := r.PathValue("provider")
	if signInProviders[provider] == nil {
		http.NotFound(rw, r)
		return
	}
	in := connectAsk{Provider: provider, Tools: r.PostForm["tool"], Write: r.PostFormValue("write") != "",
		Hint: strings.TrimSpace(r.PostFormValue("account")), Back: "/settings/tools"}
	if cn, ok := w.c.connectionOf(provider, in.Hint); ok && r.PostFormValue("reconnect") != "" {
		in = w.c.reconnectAsk(cn)
		in.Back = "/settings/tools"
	}
	if back := r.PostFormValue("back"); slices.Contains(connectionBacks, back) {
		in.Back = back
	}
	w.startSignInFlow(rw, r, s, in, "connection-result", func(err error) any {
		return struct{ Error string }{w.driveError(r, err)}
	})
}

// handleConnectionRemove removes an account: its tools, its token, and
// its authorization at the provider.
func (w *web) handleConnectionRemove(rw http.ResponseWriter, r *http.Request, s *session) {
	if err := w.c.removeConnection(r.Context(), r.PathValue("provider"), r.PostFormValue("account")); err != nil {
		w.toast(rw, w.driveError(r, err), false)
		return
	}
	log.Printf("web: an account was removed")
	w.notify()
	rw.Header().Set("HX-Refresh", "true")
	rw.WriteHeader(http.StatusOK)
}
