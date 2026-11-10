package main

import (
	"errors"
	"log"
	"net/http"
	"net/url"
	"slices"
	"strings"
)

// Connected accounts in Settings → Tools (connection_tools.go): sign an
// account in for the tools ticked (its provider's connect page, which a
// connection preset's tile opens), add a tool to it, reconnect it with the
// tools it has, or remove it with its tools. The tools list shows each
// account as a card (web_tools_groups.go); these are its buttons.

func (w *web) connectionRoutes(mux *http.ServeMux) {
	mux.Handle("GET /settings/connections/{provider}", w.auth(w.handleConnectionPage))
	mux.Handle("POST /settings/connections/{provider}/connect", w.auth(w.handleConnectionConnect))
	mux.Handle("POST /settings/connections/{provider}/remove", w.auth(w.handleConnectionRemove))
	// The pages before connections, kept as addresses.
	for _, old := range oldConnectionPages { // legacy.go
		mux.HandleFunc("GET "+old, func(rw http.ResponseWriter, r *http.Request) {
			http.Redirect(rw, r, "/settings/tools", http.StatusMovedPermanently)
		})
	}
}

// connectionBacks are the pages a sign-in may go back to.
var connectionBacks = []string{"/settings/tools"}

// connectPage is a provider's connect page, with the tools to tick.
func connectPage(provider string, tools ...string) string {
	u := "/settings/connections/" + url.PathEscape(provider)
	if len(tools) > 0 {
		u += "?" + url.Values{"tool": tools}.Encode()
	}
	return u
}

// connError is an error of a connection in the user's words.
func (w *web) connError(r *http.Request, err error) string {
	var ce *catalogError
	if errors.As(err, &ce) && ce.Key != "" {
		return w.formError(r, err)
	}
	return err.Error()
}

// connectPageView is a provider's connect page.
type connectPageView struct {
	Provider  *signInProvider
	Tools     []connectTool
	App       string // its app's client id; "" before it is made
	Origin    string
	OriginWhy string // why the provider would refuse the workspace's address
	Error     string
}

// connectTool is a kind of tool to tick.
type connectTool struct {
	Kind, Product string
	Ticked        bool
}

func (w *web) handleConnectionPage(rw http.ResponseWriter, r *http.Request, s *session) {
	p := signInProviders[r.PathValue("provider")]
	if p == nil {
		http.NotFound(rw, r)
		return
	}
	lang, ticked := w.lang(r), r.URL.Query()["tool"]
	v := connectPageView{Provider: p, Origin: w.origin(r)}
	v.OriginWhy = p.refuses(v.Origin)
	if app, err := appClient(p); err == nil {
		v.App = app.ID
	}
	for _, k := range toolsAt(p.Name) {
		v.Tools = append(v.Tools, connectTool{Kind: k, Product: productName(lang, p.Name, k), Ticked: len(ticked) == 0 || slices.Contains(ticked, k)})
	}
	w.render(rw, r, s, "settings", "", page{Title: "conn.title", Nav: "settings", Tab: "tools", View: "connect", Data: v})
}

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
		return struct{ Error string }{w.connError(r, err)}
	})
}

// handleConnectionRemove removes an account: its tools, its token, and
// its authorization at the provider.
func (w *web) handleConnectionRemove(rw http.ResponseWriter, r *http.Request, s *session) {
	if err := w.c.removeConnection(r.Context(), r.PathValue("provider"), r.PostFormValue("account")); err != nil {
		w.toast(rw, w.connError(r, err), false)
		return
	}
	log.Printf("web: an account was removed")
	w.notify()
	rw.Header().Set("HX-Refresh", "true")
	rw.WriteHeader(http.StatusOK)
}
