package main

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"slices"
)

// Connection apps: the user's own OAuth client at a provider, made once in
// the provider's console and used for every account signed in there, as
// with Home Assistant. Settings → Tools → <provider>'s app
// (/settings/apps/<provider>) guides them through it from the
// provider's data (its steps, the scopes to declare, the redirect URI to
// paste), then keeps the client id and secret in the vault as
// app.<provider>.client; the secret is never shown back nor logged.

// What a step of the guide gives to copy.
const (
	appCopyScopes   = "scopes"   // the scopes to declare
	appCopyCallback = "callback" // the redirect URI to register
)

// appGuide is how to make one's app at a provider, as data.
type appGuide struct {
	Steps []appStep
	// Scopes are the scopes to declare in the console, as one line to copy.
	Scopes string
	// Source is the provider's own guide.
	Source string
	// JSON: the console downloads the client as a JSON file, taken as is.
	JSON          bool
	IDPlaceholder string
	// Legacy is where the client was kept before connection apps: read
	// until the app is saved again.
	Legacy string
}

// appStep is one step of the guide: a text key, a link, what to copy.
type appStep struct {
	Text     string
	Link     string
	LinkText string
	Copy     string // appCopyScopes, appCopyCallback, or ""
}

var errNoAppClient = errors.New("no app")

// appVault is the vault name of a provider's app.
func appVault(p *signInProvider) string { return "app." + p.Name + ".client" }

// appClient reads the user's app at a provider from the vault.
func appClient(p *signInProvider) (oauthApp, error) {
	v := currentVault()
	if v == nil {
		return oauthApp{}, errNoVault
	}
	raw, err := v.get(appVault(p))
	if errors.Is(err, errNoSecret) && p.App.Legacy != "" {
		raw, err = v.get(p.App.Legacy)
	}
	if errors.Is(err, errNoSecret) {
		return oauthApp{}, errNoAppClient
	}
	if err != nil {
		return oauthApp{}, err
	}
	var app oauthApp
	if json.Unmarshal([]byte(raw), &app) != nil || app.ID == "" || app.Secret == "" {
		return oauthApp{}, errNoAppClient
	}
	return app, nil
}

// storeAppClient keeps the user's app at a provider; the name it had
// before goes.
func storeAppClient(p *signInProvider, app oauthApp) error {
	v := currentVault()
	if v == nil {
		return errNoVault
	}
	raw, _ := json.Marshal(app)
	if err := v.set(appVault(p), string(raw)); err != nil {
		return err
	}
	if p.App.Legacy != "" {
		v.remove(p.App.Legacy)
	}
	return nil
}

// readAppClient reads the app posted: the JSON the console downloads, or
// its client id and secret typed.
func readAppClient(p *signInProvider, rw http.ResponseWriter, r *http.Request) (oauthApp, []string, error) {
	r.Body = http.MaxBytesReader(rw, r.Body, 64<<10)
	if f, _, ferr := r.FormFile("client_json"); ferr == nil && p.App.JSON {
		raw, rerr := io.ReadAll(io.LimitReader(f, 32<<10))
		f.Close()
		if rerr != nil {
			raw = nil
		}
		return p.parseClient(raw)
	}
	app, err := p.checkClient(r.PostFormValue("client_id"), r.PostFormValue("client_secret"))
	return app, nil, err
}

// The page.

type appView struct {
	Provider  *signInProvider
	Origin    string // this workspace, as the browser reaches it
	Callback  string // the redirect URI to register
	OriginWhy string // why the provider refuses that redirect, or ""
	Remote    string // the tailnet address, when remote access gives one it takes
	ClientID  string // the saved app's client id, or ""
	Saved     string // just saved: "ok", or the redirect URI its file lacks
	Back      string // where to go on
	Error     string
}

// Copy is what a step gives to copy, for the template.
func (v appView) Copy(what string) string {
	switch what {
	case appCopyScopes:
		return v.Provider.App.Scopes
	case appCopyCallback:
		return v.Callback
	}
	return ""
}

// appBacks are the pages the app page may go back to.
var appBacks = []string{"/settings/tools", "/settings/google", "/settings/drive"}

func (w *web) appRoutes(mux *http.ServeMux) {
	mux.Handle("GET /settings/apps/{provider}", w.auth(w.handleApp))
	mux.Handle("POST /settings/apps/{provider}", w.auth(w.handleAppSave))
}

func (w *web) appView(r *http.Request, p *signInProvider) appView {
	v := appView{Provider: p, Origin: w.origin(r), Back: "/settings/tools"}
	if b := r.FormValue("back"); slices.Contains(appBacks, b) {
		v.Back = b
	}
	v.Callback = v.Origin + "/oauth/callback"
	v.OriginWhy = p.refuses(v.Origin)
	if v.OriginWhy != "" && w.c.remote != nil {
		if u := w.c.remote.current().URL(); u != "" && p.refuses(u) == "" {
			v.Remote = u
		}
	}
	if app, err := appClient(p); err == nil {
		v.ClientID = app.ID
	}
	return v
}

func (w *web) appProvider(rw http.ResponseWriter, r *http.Request) *signInProvider {
	p := signInProviders[r.PathValue("provider")]
	if p == nil {
		http.NotFound(rw, r)
	}
	return p
}

func (w *web) handleApp(rw http.ResponseWriter, r *http.Request, s *session) {
	if p := w.appProvider(rw, r); p != nil {
		w.render(rw, r, s, "settings", "", page{Title: "app.title", Nav: "settings", Tab: "tools", View: "app", Data: w.appView(r, p)})
	}
}

func (w *web) handleAppSave(rw http.ResponseWriter, r *http.Request, s *session) {
	p := w.appProvider(rw, r)
	if p == nil {
		return
	}
	app, redirects, err := readAppClient(p, rw, r)
	if err == nil {
		err = storeAppClient(p, app)
	}
	v := w.appView(r, p)
	if err != nil {
		v.Error = w.driveError(r, err)
	} else {
		log.Printf("web: the %s app was saved", p.Label)
		v.ClientID, v.Saved = app.ID, "ok"
		if len(redirects) > 0 && !slices.Contains(redirects, v.Callback) {
			v.Saved = v.Callback
		}
	}
	w.render(rw, r, s, "settings", "app-wrap-frag", page{Data: v})
}
