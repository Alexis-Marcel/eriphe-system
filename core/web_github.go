package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"maps"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
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
// leads on to the page, which the cookie reaches again, with the
// installation GitHub names. It changes nothing.
func (w *web) handleGitHubInstalled(rw http.ResponseWriter, r *http.Request) {
	next := "/settings/github?installed=1"
	if id := r.URL.Query().Get("installation_id"); githubID.MatchString(id) && r.URL.Query().Get("setup_action") == "install" {
		next += "&installation=" + id
	}
	w.render(rw, r, nil, "oauth", "", page{Title: "github.title", Data: oauthDone{Next: next}})
}

// githubID is a GitHub installation's id.
var githubID = regexp.MustCompile(`^[0-9]{1,19}$`)

// githubFlows are the app creations in progress, by state.
type githubFlows struct {
	mu sync.Mutex
	m  map[string]githubFlow
}

type githubFlow struct {
	binding string // the hash of the browser's flow cookie
	started time.Time
	public  bool // the manifest asked for an app installable on other accounts
}

const githubFlowTimeout = time.Hour // GitHub's code lasts an hour

func (f *githubFlows) add(binding string, public bool) string {
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
	f.m[state] = githubFlow{binding: binding, started: time.Now(), public: public}
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
	Installations []githubInstView
	InstallErr    string
	InstallURL    string
	Suffix        string
	// The GitHub connections of the catalog (one name, one connection, one
	// right), on the app or on a token.
	Conns []githubConn
	// ?switch=: the token connection the user came to move to the app;
	// ?switched=: the one just moved (Revoke: it had a token, to revoke).
	Switch, Switched string
	Revoke           bool
	// ?added=: the connection just added by itself for an installation.
	Added        string
	Created      bool
	Installed    bool
	Flash, Error string
	// The page that posts the manifest to GitHub.
	PostURL, Manifest string
}

// githubConn is a GitHub connection of the catalog.
type githubConn struct {
	Name     string
	Auth     string // github-app, bearer, oauth…
	Account  string // on the app: its installation's account ("" the owner's)
	ReadOnly bool   // the configuration file's
}

// githubInstView is where the app is installed, with the connection that
// uses that installation, if any, or the name a new one would get.
type githubInstView struct {
	githubInstallation
	Conn, Suggest string
}

// githubConns are the catalog's GitHub connections, by name.
func (w *web) githubConns() []githubConn {
	conf := w.c.conf()
	var out []githubConn
	for _, name := range slices.Sorted(maps.Keys(conf.Servers)) {
		s := conf.Servers[name]
		if !isGitHubServer(name, s) {
			continue
		}
		c := githubConn{Name: name, Auth: s.authKind(), ReadOnly: w.c.source(kindServer, name) == sourceConfig}
		if s.Auth != nil {
			c.Account = s.Auth.Account
		}
		out = append(out, c)
	}
	return out
}

// usesInstallation tells a connection on the app that takes its tokens
// from the installation on login.
func (c githubConn) usesInstallation(login, owner string, only bool) bool {
	if c.Auth != "github-app" {
		return false
	}
	if c.Account == "" {
		return only || strings.EqualFold(login, owner)
	}
	return strings.EqualFold(c.Account, login)
}

func (w *web) githubView(ctx context.Context, r *http.Request) githubView {
	q := r.URL.Query()
	v := githubView{Vault: currentVault() != nil, Suffix: w.c.conf().User,
		Created: q.Get("created") != "", Installed: q.Get("installed") != "", Conns: w.githubConns()}
	for _, c := range v.Conns {
		if c.Name == q.Get("switch") && c.Auth != "github-app" {
			v.Switch = c.Name
		}
		if c.Name == q.Get("switched") && c.Auth == "github-app" {
			v.Switched, v.Revoke = c.Name, q.Get("revoke") != ""
		}
		if c.Name == q.Get("added") && c.Auth == "github-app" {
			v.Added = c.Name
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
		list, err := w.c.github.installations(ctx, app)
		if err != nil {
			v.InstallErr = err.Error()
			break
		}
		w.c.github.countRepos(ctx, app, list)
		taken := map[string]bool{}
		for _, c := range v.Conns {
			taken[c.Name] = true
		}
		for _, i := range list {
			iv := githubInstView{githubInstallation: i}
			for _, c := range v.Conns {
				if c.usesInstallation(i.Account.Login, app.Owner, len(list) == 1) {
					iv.Conn = c.Name
					break
				}
			}
			if iv.Conn == "" {
				iv.Suggest = suggestGitHubName(i.Account.Login, strings.EqualFold(i.Account.Login, app.Owner), taken)
				taken[iv.Suggest] = true
			}
			v.Installations = append(v.Installations, iv)
		}
	case !errors.Is(err, errNoGitHubApp):
		v.Error = err.Error()
	}
	return v
}

// suggestGitHubName is the name a new connection on an installation gets:
// github for the owner's, github-<account> for the others, free of the
// names taken.
func suggestGitHubName(login string, owner bool, taken map[string]bool) string {
	base := "github"
	if !owner || taken[base] {
		base = slugName("github-" + strings.ToLower(login))
		if base == "" || !serverName.MatchString(base) {
			base = "github-2"
		}
	}
	name := base
	for n := 2; taken[name]; n++ {
		name = fmt.Sprintf("%s-%d", base, n)
	}
	return name
}

func (w *web) handleGitHub(rw http.ResponseWriter, r *http.Request, s *session) {
	v := w.githubView(r.Context(), r)
	if id := r.URL.Query().Get("installation"); id != "" {
		if name, switched, had, ok := w.connectInstallation(v, id, w.lang(r)); ok {
			// To the page as it is now, saying what was done; reloading it
			// does nothing again.
			next := "/settings/github?added=" + url.QueryEscape(name)
			if switched {
				next = "/settings/github?switched=" + url.QueryEscape(name)
				if had == "bearer" {
					next += "&revoke=1"
				}
			}
			http.Redirect(rw, r, next, http.StatusSeeOther)
			return
		}
	}
	w.render(rw, r, s, "settings", "", page{Title: "github.title", Nav: "settings", Tab: "tools", View: "github", Data: v})
}

// githubFreshInstall is how recent an installation is for its connection
// to come by itself: the one the user just made, not an older one.
const githubFreshInstall = 15 * time.Minute

// connectInstallation gives the installation the user just made on GitHub
// its connection (its id: the setup_url's installation_id). Installing the
// app on an account is the user's choice of that account and its
// repositories: no second click. Only an installation of the app, made in
// the last minutes and used by no connection: one the user disconnected
// later stays so. A GitHub connection on a token named as this account's
// would be (github for the app owner, github-<account> otherwise) moves
// to the app, keeping its name and rights; otherwise a new one comes, at
// the presets' level.
func (w *web) connectInstallation(v githubView, id, lang string) (name string, switched bool, had string, ok bool) {
	if v.App == nil || !githubID.MatchString(id) {
		return "", false, "", false
	}
	var inst *githubInstView
	for i := range v.Installations {
		if strconv.FormatInt(v.Installations[i].ID, 10) == id {
			inst = &v.Installations[i]
		}
	}
	if inst == nil || inst.Conn != "" || inst.CreatedAt.IsZero() || time.Since(inst.CreatedAt) > githubFreshInstall {
		return "", false, "", false
	}
	login, owner := inst.Account.Login, v.App.Owner
	account := ""
	if !strings.EqualFold(login, owner) {
		account = login
	}
	base := "github"
	if account != "" {
		base = slugName("github-" + strings.ToLower(login))
	}
	conf := w.c.conf()
	srv, exists := conf.Servers[base]
	name = inst.Suggest
	if exists && isGitHubServer(base, srv) && srv.authKind() != "github-app" && w.c.source(kindServer, base) != sourceConfig {
		name, switched = base, true
	} else {
		srv, exists = Server{}, false
	}
	if name == "" {
		return "", false, "", false
	}
	had, err := w.c.putGitHubConnection(name, srv, exists, account, owner, lang)
	if err != nil {
		log.Printf("web: GitHub installation %s on %s: %v", id, login, err)
		return "", false, "", false
	}
	log.Printf("web: GitHub installation on %s: tool %s on the app (%s)", login, name, map[bool]string{true: "moved", false: "added"}[switched])
	w.notify()
	return name, switched, had, true
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
	if a := w.elsewhere(r); a != "" {
		// GitHub sends the browser back to the address, where this one's cookie is not.
		v.Error = fmt.Sprintf(tr(w.lang(r), "address.elsewhere"), a)
		w.render(rw, r, s, "settings", "", page{Title: "github.title", Nav: "settings", Tab: "tools", View: "github", Data: v})
		return
	}
	public := r.PostFormValue("public") != ""
	manifest, _ := json.Marshal(githubAppManifest(githubAppName(r.PostFormValue("suffix")), w.origin(r), public))
	state := w.c.github.flows.add(w.oauthBinding(rw, r), public)
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
	app, err := w.c.github.convertManifest(ctx, q.Get("code"), f.public)
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

// handleGitHubTool puts a GitHub connection on the app, from the
// installation on an account (account; empty: the app owner's): a new
// one, or one on a token moved to the app (name). A connection moved keeps
// its name, so its grants and rules; its token leaves the vault with the
// change (dropOrphanSecrets), and the page says to revoke it on GitHub.
// Without a name: the connection named github, else the first, else a
// new one named github.
func (w *web) handleGitHubTool(rw http.ResponseWriter, r *http.Request, s *session) {
	lang := w.lang(r)
	app, err := loadGitHubApp()
	if err != nil {
		w.toast(rw, tr(lang, "github.noapp"), false)
		return
	}
	account := strings.TrimSpace(r.PostFormValue("account"))
	if account != "" && !githubLogin.MatchString(account) {
		w.toast(rw, tr(lang, "github.badorg"), false)
		return
	}
	conf := w.c.conf()
	name := strings.TrimSpace(r.PostFormValue("name"))
	srv, exists := Server{}, false
	if name == "" {
		if sv, ok := conf.Servers["github"]; ok {
			name, srv, exists = "github", sv, true
		} else {
			for _, n := range slices.Sorted(maps.Keys(conf.Servers)) {
				if isGitHubServer(n, conf.Servers[n]) {
					name, srv, exists = n, conf.Servers[n], true
					break
				}
			}
		}
		if name == "" {
			name = "github"
		}
	} else {
		if !serverName.MatchString(name) {
			w.toast(rw, w.formError(r, errInvalid(fmt.Errorf("bad name %q", name))), false)
			return
		}
		srv, exists = conf.Servers[name]
		if exists && !isGitHubServer(name, srv) {
			w.toast(rw, fmt.Sprintf(tr(lang, "github.notgithub"), name), false)
			return
		}
	}
	if w.c.source(kindServer, name) == sourceConfig {
		w.toast(rw, w.formError(r, errReadOnly(kindServer, name)), false)
		return
	}
	// The app must be on that account, when GitHub says where it is.
	if account != "" {
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		list, err := w.c.github.installations(ctx, app)
		cancel()
		if err == nil {
			if _, err := pickInstallation(app, list, account); err != nil {
				w.toast(rw, fmt.Sprintf(tr(lang, "github.notthere"), account), false)
				return
			}
		}
	}
	had, err := w.c.putGitHubConnection(name, srv, exists, account, app.Owner, lang)
	if err != nil {
		w.toast(rw, w.formError(r, err), false)
		return
	}
	log.Printf("web: tool %s now on the GitHub App (%s)", name, orStr(account, "the owner's installation"))
	w.notify()
	next := "/settings/tools?saved=" + url.QueryEscape(name)
	if exists {
		next = "/settings/github?switched=" + url.QueryEscape(name)
		if had == "bearer" {
			next += "&revoke=1"
		}
	}
	rw.Header().Set("HX-Redirect", next)
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

// putGitHubConnection puts the connection name on the app's installation
// on account ("" the owner's): srv when it exists (a connection moved
// keeps its name, level and rules; its token leaves the vault with the
// change), else a new one at the presets' level. It returns how the
// connection authenticated before.
func (c *core) putGitHubConnection(name string, srv Server, exists bool, account, owner, lang string) (string, error) {
	if !exists {
		srv = Server{Use: tr(lang, "preset.github.use")}.withLevel(accessReads)
		if account != "" && !strings.EqualFold(account, owner) {
			srv.Use += " (" + account + ")"
		}
	}
	had := srv.authKind()
	if srv.local() || srv.URL == "" {
		srv = Server{Use: srv.Use, Access: srv.Access, TrustAnnotations: srv.TrustAnnotations, AlwaysLoad: srv.AlwaysLoad, Timeout: srv.Timeout}
		srv.URL = "https://api.githubcopilot.com/mcp/"
	}
	srv.Auth = &ServerAuth{GitHubApp: true, Account: account}
	return had, c.putServer(putUpsert, name, srv, nil)
}
