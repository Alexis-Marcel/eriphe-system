package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"slices"
	"strings"
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

// storeAppClient keeps the user's app at a provider.
func storeAppClient(p *signInProvider, app oauthApp) error {
	v := currentVault()
	if v == nil {
		return errNoVault
	}
	raw, _ := json.Marshal(app)
	return v.set(appVault(p), string(raw))
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
// (and a provider's connect page, /settings/connections/<provider>).
var appBacks = []string{"/settings/tools"}

func appBack(p *signInProvider, back string) bool {
	return slices.Contains(appBacks, back) || back == connectPage(p.Name)
}

func (w *web) appRoutes(mux *webMux) {
	mux.Handle("GET /settings/apps/{provider}", w.auth(w.handleApp))
	mux.Handle("POST /settings/apps/{provider}", w.auth(w.handleAppSave))
	mux.Handle("POST /settings/apps/{provider}/test", w.auth(w.handleAppTest))
}

// appTestCode is the made-up code of the client check.
const appTestCode = "eriphe-connection-test"

// testApp checks a provider's app without a consent on its side, a line
// per check, each ✓ or ✗:
//
//  1. The workspace's address: one the provider takes as a redirect.
//  2. The client id and secret (typed, when the form gives them, else the
//     app saved): a code exchange with a made-up code. The
//     provider checks the client first: invalid_client (or
//     unauthorized_client) is a client it does not know or a wrong
//     secret, invalid_grant a client it knows that sent a bad code, as
//     expected here.
//  3. The redirect URI: not asked (providers say it only on their sign-in
//     pages); the line says what to check in the console.
//  4. Each account connected at it: its refresh token for an access token
//     (checkConnection, which also marks it).
//
// The secret and the tokens are never shown nor logged.
func (c *core) testApp(ctx context.Context, lang, origin string, p *signInProvider, typed *oauthApp) []testView {
	var out []testView
	add := func(state, key string, args ...any) *testView {
		out = append(out, testView{State: state, Text: fmt.Sprintf(tr(lang, key), args...)})
		return &out[len(out)-1]
	}
	unreachable := func(err error) {
		l := add("err", "app.test.unreachable", p.Label)
		if key := netErrorKey(err); key != "" {
			l.Detail = tr(lang, key)
		}
	}
	why := p.refuses(origin)
	if why != "" {
		add("err", "app.origin."+why, origin)
	} else {
		add("ok", "app.test.origin", p.Label, origin)
	}
	redirect := origin + "/oauth/callback"
	app, err := appClient(p)
	if typed != nil {
		app, err = *typed, nil
	}
	switch {
	case errors.Is(err, errNoAppClient):
		add("info", "app.test.noclient")
	case err != nil:
		add("err", "app.test.clientunreadable")
	default:
		hide := func(s string) string { return truncate(strings.ReplaceAll(s, app.Secret, "••••"), 200) }
		code, desc, _, err := p.tokenCall(ctx, signInHTTP, url.Values{"grant_type": {"authorization_code"}, "code": {appTestCode},
			"redirect_uri": {redirect}, "client_id": {app.ID}, "client_secret": {app.Secret}})
		bad := code == "invalid_client" || code == "unauthorized_client"
		switch {
		case err != nil:
			unreachable(err)
		case code == "invalid_grant":
			add("ok", "app.test.clientok", p.Label, app.ID)
		case bad:
			add("err", "app.test.clientbad").Detail = hide(desc)
		default:
			add("err", "app.test.clientother", p.Label, hide(strings.TrimSpace(code+" "+desc)))
		}
		if why == "" && !bad {
			add("info", "app.test.redirectcheck", redirect)
		}
	}
	conns := c.connectionsAt(p.Name)
	if len(conns) == 0 {
		add("info", "app.test.noaccount")
	}
	for _, cn := range conns {
		lapsed, err := c.checkConnection(ctx, cn)
		switch {
		case err != nil:
			unreachable(err)
		case lapsed == "":
			add("ok", "app.test.account.ok", cn.Identity)
		default:
			key := map[string]string{lapsedRevoked: "revoked", lapsedBadClient: "badclient"}[lapsed]
			add("err", "app.test.account."+cmpOr(key, "notoken"), cn.Identity)
		}
	}
	return out
}

// appTyped is the app typed in the form, not yet saved, when it gives one:
// the test then checks it rather than the app saved.
func appTyped(p *signInProvider, rw http.ResponseWriter, r *http.Request) (*oauthApp, error) {
	r.Body = http.MaxBytesReader(rw, r.Body, 64<<10)
	if r.ParseMultipartForm(64<<10) != nil {
		r.ParseForm()
	}
	file := r.MultipartForm != nil && len(r.MultipartForm.File["client_json"]) > 0
	if !file && strings.TrimSpace(r.PostFormValue("client_id")+r.PostFormValue("client_secret")) == "" {
		return nil, nil
	}
	app, _, err := readAppClient(p, rw, r)
	return &app, err
}

func (w *web) handleAppTest(rw http.ResponseWriter, r *http.Request, s *session) {
	p := w.appProvider(rw, r)
	if p == nil {
		return
	}
	lines := []testView{{State: "err", Text: tr(w.lang(r), "test.ratelimit")}}
	if w.testLimit.allow(s.ID, w.now()) {
		typed, err := appTyped(p, rw, r)
		if err != nil {
			w.render(rw, r, s, "settings", "app-test", page{Data: []testView{{State: "err", Text: w.connError(r, err)}}})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*testConnTimeout)
		defer cancel()
		lines = w.c.testApp(ctx, w.lang(r), w.origin(r), p, typed)
		log.Printf("web: the %s app was tested", p.Label) // never the client nor a token
	}
	w.render(rw, r, s, "settings", "app-test", page{Data: lines})
}

func (w *web) appView(r *http.Request, p *signInProvider) appView {
	v := appView{Provider: p, Origin: w.origin(r), Back: "/settings/tools"}
	if b := r.FormValue("back"); appBack(p, b) {
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
		v.Error = w.connError(r, err)
	} else {
		log.Printf("web: the %s app was saved", p.Label)
		v.ClientID, v.Saved = app.ID, "ok"
		if len(redirects) > 0 && !slices.Contains(redirects, v.Callback) {
			v.Saved = v.Callback
		}
	}
	w.render(rw, r, s, "settings", "app-wrap-frag", page{Data: v})
}
