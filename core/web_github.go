package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Settings → Tools → GitHub: Eriphe's GitHub App in one click (github_app.go).
//
//  1. "Créer l'app GitHub d'Eriphe" posts the suffix of its name (and an
//     organization, if any) here; eriphe-core writes the manifest and a
//     state bound to this browser (the OAuth flow's own cookie, SameSite=Lax).
//  2. The page that comes back posts the manifest to github.com; the user
//     confirms there, and GitHub sends the browser to /github-app/callback
//     with a code and the state (no session cookie: it is SameSite=Strict).
//  3. eriphe-core exchanges the code for the app (id, private key), stores
//     it in the vault, and sends the browser back here, where it installs
//     the app on the repositories Eriphe may work on.

func (w *web) githubRoutes(mux *http.ServeMux) {
	mux.Handle("GET /settings/github", w.auth(w.handleGitHub))
	mux.Handle("POST /settings/github/create", w.auth(w.handleGitHubCreate))
	mux.Handle("POST /settings/github/tool", w.auth(w.handleGitHubTool))
	mux.Handle("POST /settings/github/forget", w.auth(w.handleGitHubForget))
	mux.HandleFunc("GET /github-app/callback", w.handleGitHubCallback)
	mux.HandleFunc("GET /github-app/installed", w.handleGitHubInstalled)
}

// handleGitHubInstalled is where GitHub sends the browser after an
// installation (the app's setup_url): without the session cookie, it only
// leads on to the page, which the cookie reaches again. It changes nothing.
func (w *web) handleGitHubInstalled(rw http.ResponseWriter, r *http.Request) {
	w.render(rw, r, nil, "oauth", "", page{Title: "github.title", Data: oauthDone{Next: "/settings/github?installed=1"}})
}

// githubFlows are the app creations in progress, by state.
type githubFlows struct {
	mu sync.Mutex
	m  map[string]githubFlow
}

type githubFlow struct {
	binding string // the hash of the browser's flow cookie
	started time.Time
}

const githubFlowTimeout = time.Hour // GitHub's code lasts an hour

func (f *githubFlows) add(binding string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.m == nil {
		f.m = map[string]githubFlow{}
	}
	for k, v := range f.m {
		if time.Since(v.started) > githubFlowTimeout {
			delete(f.m, k)
		}
	}
	state := "gha-" + randomToken(24)
	f.m[state] = githubFlow{binding: binding, started: time.Now()}
	return state
}

// take finds a flow by its state, once.
func (f *githubFlows) take(state string) (githubFlow, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fl, ok := f.m[state]
	delete(f.m, state)
	return fl, ok && time.Since(fl.started) <= githubFlowTimeout
}

type githubView struct {
	Vault         bool
	App           *githubAppSecret // without its key
	Installations []githubInstallation
	InstallErr    string
	InstallURL    string
	Suffix        string
	// The GitHub catalog tool: its name, whether it is on the app, and
	// whether the workspace may change it.
	Tool, ToolAuth string
	ToolReadOnly   bool
	Created        bool
	Installed      bool
	Flash, Error   string
	// The page that posts the manifest to GitHub.
	PostURL, Manifest string
}

func (w *web) githubView(ctx context.Context, r *http.Request) githubView {
	v := githubView{Vault: currentVault() != nil, Suffix: w.c.conf().User,
		Created: r.URL.Query().Get("created") != "", Installed: r.URL.Query().Get("installed") != ""}
	conf := w.c.conf()
	for name, s := range conf.Servers {
		if isGitHubServer(name, s) && (v.Tool == "" || name == "github") {
			v.Tool, v.ToolAuth, v.ToolReadOnly = name, s.authKind(), w.c.source(kindServer, name) == sourceConfig
		}
	}
	app, err := loadGitHubApp()
	switch {
	case err == nil:
		s := app.githubAppSecret
		s.PEM, s.ClientSecret, s.WebhookSecret = "", "", ""
		v.App = &s
		v.InstallURL = w.c.github.installURL(app.Slug)
		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		if v.Installations, err = w.c.github.installations(ctx, app); err != nil {
			v.InstallErr = err.Error()
		}
	case !errors.Is(err, errNoGitHubApp):
		v.Error = err.Error()
	}
	return v
}

func (w *web) handleGitHub(rw http.ResponseWriter, r *http.Request, s *session) {
	w.render(rw, r, s, "settings", "", page{Title: "github.title", Nav: "settings", Tab: "tools", View: "github",
		Data: w.githubView(r.Context(), r)})
}

// handleGitHubCreate writes the manifest and shows the page that posts it
// to GitHub.
func (w *web) handleGitHubCreate(rw http.ResponseWriter, r *http.Request, s *session) {
	v := w.githubView(r.Context(), r)
	if !v.Vault {
		v.Error = tr(w.lang(r), "accounts.novault")
		w.render(rw, r, s, "settings", "", page{Title: "github.title", Nav: "settings", Tab: "tools", View: "github", Data: v})
		return
	}
	org := strings.TrimSpace(r.PostFormValue("org"))
	if org != "" && !githubLogin.MatchString(org) {
		v.Error = tr(w.lang(r), "github.badorg")
		w.render(rw, r, s, "settings", "", page{Title: "github.title", Nav: "settings", Tab: "tools", View: "github", Data: v})
		return
	}
	manifest, _ := json.Marshal(githubAppManifest(githubAppName(r.PostFormValue("suffix")), w.origin(r)))
	state := w.c.github.flows.add(w.oauthBinding(rw, r))
	v.PostURL, v.Manifest = w.c.github.newAppURL(org, state), string(manifest)
	// This page's form goes to GitHub: the policy lets it.
	rw.Header().Set("Content-Security-Policy", strings.Replace(rw.Header().Get("Content-Security-Policy"),
		"form-action 'self'", "form-action 'self' "+strings.TrimSuffix(w.c.github.web, "/"), 1))
	w.render(rw, r, s, "settings", "", page{Title: "github.title", Nav: "settings", Tab: "tools", View: "github-post", Data: v})
}

// githubLogin is a GitHub account or organization name.
var githubLogin = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,37}[A-Za-z0-9])?$`)

// handleGitHubCallback is where GitHub sends the browser back with the
// app's code.
func (w *web) handleGitHubCallback(rw http.ResponseWriter, r *http.Request) {
	lang := w.lang(r)
	q := r.URL.Query()
	fail := func(status int, msg string) {
		rw.WriteHeader(status)
		w.render(rw, r, nil, "oauth", "", page{Title: "github.title", Data: oauthDone{Error: msg, Back: "/settings/github"}})
	}
	f, ok := w.c.github.flows.take(q.Get("state"))
	c, err := r.Cookie(oauthCookie)
	if !ok || err != nil || subtle.ConstantTimeCompare([]byte(hashToken(c.Value)), []byte(f.binding)) != 1 {
		fail(http.StatusBadRequest, tr(lang, "github.expired"))
		return
	}
	if q.Get("code") == "" {
		fail(http.StatusBadRequest, tr(lang, "github.nocode"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	app, err := w.c.github.convertManifest(ctx, q.Get("code"))
	if err != nil {
		log.Printf("web: GitHub App: %v", err)
		fail(http.StatusBadGateway, err.Error())
		return
	}
	log.Printf("web: GitHub App %s (%d) created for %s, kept in the vault", app.Slug, app.ID, app.Owner)
	w.notify()
	// Back to the page, which the session cookie reaches again.
	w.render(rw, r, nil, "oauth", "", page{Title: "github.title", Data: oauthDone{Next: "/settings/github?created=1"}})
}

// handleGitHubTool puts the GitHub catalog tool on the app: one GitHub
// connection for the tool and for git.
func (w *web) handleGitHubTool(rw http.ResponseWriter, r *http.Request, s *session) {
	lang := w.lang(r)
	if _, err := loadGitHubApp(); err != nil {
		w.toast(rw, tr(lang, "github.noapp"), false)
		return
	}
	// The GitHub tool there is, the one named github first; else a new one.
	conf := w.c.conf()
	name, srv := "", Server{}
	if sv, ok := conf.Servers["github"]; ok {
		name, srv = "github", sv
	} else {
		for n, sv := range conf.Servers {
			if isGitHubServer(n, sv) {
				name, srv = n, sv
				break
			}
		}
	}
	if name == "" {
		name, srv = "github", Server{Use: tr(lang, "preset.github.use")}.withLevel(accessReads)
	}
	if srv.local() || srv.URL == "" {
		srv = Server{Use: srv.Use, Access: srv.Access, TrustAnnotations: srv.TrustAnnotations, AlwaysLoad: srv.AlwaysLoad, Timeout: srv.Timeout}
		srv.URL = "https://api.githubcopilot.com/mcp/"
	}
	if w.c.source(kindServer, name) == sourceConfig {
		w.toast(rw, w.formError(r, errReadOnly(kindServer, name)), false)
		return
	}
	srv.Auth = &ServerAuth{GitHubApp: true}
	if err := w.c.putServer(putUpsert, name, srv, nil); err != nil {
		w.toast(rw, w.formError(r, err), false)
		return
	}
	log.Printf("web: tool %s now on the GitHub App", name)
	w.notify()
	rw.Header().Set("HX-Redirect", "/settings/tools?saved="+url.QueryEscape(name))
	rw.WriteHeader(http.StatusOK)
}

// handleGitHubForget forgets the app here (it stays on GitHub, where the
// user deletes it).
func (w *web) handleGitHubForget(rw http.ResponseWriter, r *http.Request, s *session) {
	if err := forgetGitHubApp(); err != nil {
		w.toast(rw, err.Error(), false)
		return
	}
	w.c.github.forget()
	log.Printf("web: GitHub App forgotten")
	w.notify()
	rw.Header().Set("HX-Redirect", "/settings/github")
	rw.WriteHeader(http.StatusOK)
}
