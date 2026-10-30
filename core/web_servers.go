package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
)

// The tool catalog in the workspace: its servers with their status, and
// Connect for those that need an OAuth authorization. Connect sends the
// browser to the vendor's authorization page, which redirects back to the
// workspace's own /oauth/callback (web_catalog.go). A server whose
// pre-registered client only knows a loopback redirect (redirect_uri in
// the configuration) takes the CLI's way instead: the browser lands on a
// page of 127.0.0.1 that does not load, and its address is pasted back.

type serverView struct {
	serverInfo
	Node    string // a local server's node and state
	Service string
}

type connectView struct {
	Name    string
	Flow    string
	AuthURL string
	Error   string
	Status  string
	Tools   int
}

func (w *web) servers() []serverView {
	var out []serverView
	for _, info := range w.c.listServers("", false) {
		v := serverView{serverInfo: info}
		if info.Kind == "local" {
			if s, err := w.c.store.service(info.Name); err == nil {
				v.Node, v.Service = s.Node, s.State
			}
		}
		out = append(out, v)
	}
	return out
}

// callAPI runs one of eriphe-core's own API handlers with a JSON body, so
// the workspace takes exactly the CLI's path.
func callAPI(h http.HandlerFunc, r *http.Request, name string, body any, out any) (int, string) {
	raw, _ := json.Marshal(body)
	req := httptest.NewRequestWithContext(r.Context(), http.MethodPost, "/", bytes.NewReader(raw))
	req.SetPathValue("name", name)
	rec := httptest.NewRecorder()
	h(rec, req)
	if rec.Code < 300 && out != nil {
		json.Unmarshal(rec.Body.Bytes(), out)
	}
	return rec.Code, strings.TrimSpace(rec.Body.String())
}

// handleServerConnect starts an authorization: the browser goes to the
// authorization page and comes back to the workspace.
func (w *web) handleServerConnect(rw http.ResponseWriter, r *http.Request, s *session) {
	name := r.PathValue("name")
	v := connectView{Name: name}
	srv, ok := w.c.conf().Servers[name]
	if !ok {
		v.Error = fmt.Sprintf(tr(w.lang(r), "catalog.notfound"), name)
		w.render(rw, r, s, "settings", "connect", page{Data: v})
		return
	}
	redirect, binding := w.callbackURL(r), ""
	if o := oauthOf(srv); o != nil && o.RedirectURI != "" {
		redirect = o.RedirectURI // the paste step
	} else {
		if !w.atAddress(rw, r) {
			return
		}
		binding = w.oauthBinding(rw, r)
	}
	f, authURL, err := w.c.startConnect(r.Context(), name, redirect, binding)
	switch {
	case err != nil:
		v.Error = w.formError(r, err)
	case authURL != "" && binding != "":
		rw.Header().Set("HX-Redirect", authURL)
		rw.WriteHeader(http.StatusOK)
		return
	case authURL != "":
		v.Flow, v.AuthURL = f.id, authURL
	default:
		v.Status = statusReady
	}
	w.render(rw, r, s, "settings", "connect", page{Data: v})
}

// handleServerCallback hands the pasted redirect over.
func (w *web) handleServerCallback(rw http.ResponseWriter, r *http.Request, s *session) {
	name := r.PathValue("name")
	v := connectView{Name: name, Flow: r.PostFormValue("flow")}
	u, err := url.Parse(strings.TrimSpace(r.PostFormValue("url")))
	q := url.Values{}
	if err == nil {
		q = u.Query()
	}
	if q.Get("code") == "" && q.Get("error") == "" {
		v.Error = tr(w.lang(r), "tools.badurl")
		w.render(rw, r, s, "settings", "connect", page{Data: v})
		return
	}
	var out struct {
		Status string `json:"status"`
		Error  string `json:"error"`
		Tools  int    `json:"tools"`
	}
	code, text := callAPI(w.c.handleServerCallback, r, name, map[string]string{"flow": v.Flow, "code": q.Get("code"),
		"state": q.Get("state"), "iss": q.Get("iss"), "error": q.Get("error"), "error_description": q.Get("error_description")}, &out)
	if code >= 300 {
		v.Error = text
	} else {
		v.Status, v.Error, v.Tools = out.Status, out.Error, out.Tools
	}
	w.notify()
	w.render(rw, r, s, "settings", "connect", page{Data: v})
}
