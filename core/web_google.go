package main

import (
	"io"
	"log"
	"net/http"
	"slices"
	"strings"
)

// Settings → Tools → Google (/settings/google): the user's own Google app,
// set up once for every service and every account (a short guide through
// the Google Cloud console, with what to copy), then the Google accounts:
// each one with its services (Mail, Calendar, Drive), its state, a way to
// add a service, to reconnect it and to remove it, and "Add a Google
// account" with the services to tick. One sign-in per account asks for the
// services ticked only (connection_tools.go).

type googleView struct {
	Origin    string // this workspace, as the browser reaches it
	Callback  string // the redirect URI to register in the Google app
	OriginWhy string // why Google refuses that redirect (signInProvider.refuses), or ""
	Remote    string // the tailnet address, when remote access gives one Google takes
	ClientID  string // the saved Google app's client id, or ""
	Scopes    string // the scopes to declare in the console, to copy
	APIs      string // the console's page that turns on the APIs, all at once
	Accounts  []googleAccountView
	Add       []string // the services ticked for a new account (?add=)
	Connected bool     // just back from Google
	Missing   []string // … and these services were not granted
	Saved     string   // just saved the Google app: "ok", or the redirect URI its file lacks
	Error     string
	Waiting   bool // a service's tool is starting: the accounts refresh themselves until it is up
}

// googleAccountView is one Google account on the page.
type googleAccountView struct {
	Email     string
	Reconnect bool // Google refused its token: connect it again
	Services  []googleServiceView
	Others    []string // the services it could add
	Folders   int      // its Drive folders given to Eriphe
}

// googleServiceView is one service of an account, and its tool.
type googleServiceView struct {
	Service string // mail, calendar, drive
	Tool    string // the catalog server
	Status  string // the server's: ready, connecting, needs_auth…
	Pending bool   // its local service does not run yet (startState)
	Placed  bool   // … but a machine has it and is starting it
	Problem string // why it does not start, in words (serviceProblem)
}

// Has tells whether the page ticks a service for a new account.
func (v googleView) Has(service string) bool { return slices.Contains(v.Add, service) }

// AllServices are the services, in their order, for the template.
func (v googleView) AllServices() []string { return googleServices() }

// googleServices are the kinds of tools a Google account serves.
func googleServices() []string { return toolsAt(providerGoogle.Name) }

// googleAccounts are the Google accounts connected.
func (c *core) googleAccounts() []connection { return c.connectionsAt(providerGoogle.Name) }

func (c *core) googleAccount(email string) (connection, bool) {
	return c.connectionOf(providerGoogle.Name, email)
}

// googleTools are the tools of a Google account, by kind.
func (c *core) googleTools(email string) []connTool {
	cn, ok := c.googleAccount(email)
	if !ok {
		return nil
	}
	return c.connectionKinds(cn)
}

func (w *web) googleRoutes(mux *http.ServeMux) {
	mux.Handle("GET /settings/google", w.auth(w.handleGoogle))
	mux.Handle("POST /settings/google/client", w.auth(w.handleGoogleClient))
	mux.Handle("POST /settings/google/connect", w.auth(w.handleGoogleConnect))
	mux.Handle("POST /settings/google/remove", w.auth(w.handleGoogleRemove))
}

// googleAPIs is the console's page that turns on, in the project picked,
// the APIs of the three services at once.
const googleAPIs = "https://console.cloud.google.com/flows/enableapi?apiid=gmail.googleapis.com,caldav.googleapis.com,drive.googleapis.com"

func (w *web) googleView(r *http.Request) googleView {
	v := googleView{Origin: w.origin(r), APIs: googleAPIs,
		Scopes: strings.Join([]string{scopeMail, scopeCalendar, scopeDrive, scopeDriveReadonly}, ",")}
	v.Callback = v.Origin + "/oauth/callback"
	v.OriginWhy = providerGoogle.refuses(v.Origin)
	if v.OriginWhy != "" && w.c.remote != nil {
		if u := w.c.remote.current().URL(); u != "" && providerGoogle.refuses(u) == "" {
			v.Remote = u
		}
	}
	if app, err := googleClient(); err == nil {
		v.ClientID = app.ID
	}
	q := r.URL.Query()
	v.Connected = q.Get("connected") != ""
	for _, m := range strings.Split(q.Get("missing"), ",") {
		if slices.Contains(googleServices(), m) {
			v.Missing = append(v.Missing, m)
		}
	}
	for _, a := range strings.Split(q.Get("add"), ",") {
		if slices.Contains(googleServices(), a) {
			v.Add = append(v.Add, a)
		}
	}
	if len(v.Add) == 0 {
		v.Add = googleServices()
	}
	v.Accounts = w.googleAccountViews(w.lang(r))
	for _, a := range v.Accounts {
		for _, sv := range a.Services {
			v.Waiting = v.Waiting || sv.Placed || sv.Status == statusConnecting
		}
	}
	return v
}

func (w *web) googleAccountViews(lang string) []googleAccountView {
	drive := w.c.driveState()
	var out []googleAccountView
	for _, a := range w.c.googleAccounts() {
		av := googleAccountView{Email: a.Identity, Reconnect: a.Lapsed != ""}
		have := map[string]bool{}
		for _, t := range w.c.googleTools(a.Identity) {
			sv := googleServiceView{Service: t.Kind, Tool: t.Name}
			srv := serverView{serverInfo: serverInfo{Name: t.Name, Kind: "local"}}
			if u := w.c.pool.get(t.Name); u != nil {
				srv.Status, _, _ = u.snapshot()
			}
			if st, err := w.c.store.service(t.Name); err == nil {
				srv.Node, srv.Service, srv.Error = st.Node, st.State, st.Error
			}
			// a tool whose service is still being placed or started is
			// starting, not in error: its first dial fails until it runs
			sv.Status = srv.Status
			if sv.Pending, sv.Placed = startState(srv); sv.Pending {
				sv.Problem = serviceProblem(lang, srv)
			}
			av.Services = append(av.Services, sv)
			have[t.Kind] = true
		}
		for _, s := range googleServices() {
			if !have[s] {
				av.Others = append(av.Others, s)
			}
		}
		for _, f := range drive.Folders {
			if strings.EqualFold(f.Account, a.Identity) {
				av.Folders++
			}
		}
		out = append(out, av)
	}
	return out
}

func (w *web) handleGoogle(rw http.ResponseWriter, r *http.Request, s *session) {
	w.render(rw, r, s, "settings", "", page{Title: "google.title", Nav: "settings", Tab: "tools", View: "google", Data: w.googleView(r)})
}

// handleGoogleClient keeps the user's Google app, as the Drive page does.
func (w *web) handleGoogleClient(rw http.ResponseWriter, r *http.Request, s *session) {
	app, redirects, err := readGoogleClient(rw, r)
	if err == nil {
		err = storeGoogleClient(app)
	}
	v := w.googleView(r)
	if err != nil {
		v.Error = w.driveError(r, err)
	} else {
		log.Printf("web: Google: Google app saved")
		v.ClientID, v.Saved = app.ID, "ok"
		if len(redirects) > 0 && !slices.Contains(redirects, v.Callback) {
			v.Saved = v.Callback
		}
	}
	w.render(rw, r, s, "settings", "google-wrap-frag", page{Data: v})
}

// readGoogleClient reads the Google app posted: the JSON the console
// downloads, or its client id and secret typed. The secret is never shown
// back nor logged.
func readGoogleClient(rw http.ResponseWriter, r *http.Request) (oauthApp, []string, error) {
	r.Body = http.MaxBytesReader(rw, r.Body, 64<<10)
	if f, _, ferr := r.FormFile("client_json"); ferr == nil {
		raw, rerr := io.ReadAll(io.LimitReader(f, 32<<10))
		f.Close()
		if rerr != nil {
			raw = nil
		}
		return providerGoogle.parseClient(raw)
	}
	app, err := providerGoogle.checkClient(r.PostFormValue("client_id"), r.PostFormValue("client_secret"))
	return app, nil, err
}

// handleGoogleConnect connects a Google account with the services ticked,
// or reconnects one (account=, with its services).
func (w *web) handleGoogleConnect(rw http.ResponseWriter, r *http.Request, s *session) {
	r.ParseForm()
	in := connectAsk{Provider: providerGoogle.Name, Tools: r.PostForm["service"], Write: r.PostFormValue("write") != "",
		Hint: strings.TrimSpace(r.PostFormValue("account")), Back: "/settings/google"}
	if in.Hint != "" && r.PostFormValue("reconnect") != "" {
		// Reconnect: the services it has, Drive with the rights it had.
		in.Tools = nil
		for _, t := range w.c.googleTools(in.Hint) {
			in.Tools = append(in.Tools, t.Kind)
		}
		if a, ok := w.c.googleAccount(in.Hint); ok && len(in.Tools) == 0 {
			for _, k := range googleServices() {
				if t, _ := signInOf(k, a.Provider); func() bool { ok, _ := t.granted(a.Scopes); return ok }() {
					in.Tools = append(in.Tools, k)
				}
			}
		}
		in.Write = w.c.driveState().canWrite(in.Hint)
	}
	if back := r.PostFormValue("back"); back == "/settings/tools" {
		in.Back = back
	}
	w.startSignInFlow(rw, r, s, in, "google-connect-result", func(err error) any {
		v := w.googleView(r)
		v.Error = w.driveError(r, err)
		return v
	})
}

// handleGoogleRemove takes a Google account away, with its services.
func (w *web) handleGoogleRemove(rw http.ResponseWriter, r *http.Request, s *session) {
	err := w.c.removeConnection(r.Context(), providerGoogle.Name, r.PostFormValue("account"))
	v := w.googleView(r)
	if err != nil {
		v.Error = w.driveError(r, err)
	} else {
		w.notify()
	}
	w.render(rw, r, s, "settings", "google-wrap-frag", page{Data: v})
}
