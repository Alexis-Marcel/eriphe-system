package main

import (
	"bytes"
	"crypto/sha256"
	"crypto/tls"
	"embed"
	"encoding/hex"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// The workspace: Eriphe's own interface, served by eriphe-core on its own
// HTTPS listener. Server-rendered pages (html/template), htmx for the forms
// and actions, Server-Sent Events for what changes. The conversation with
// the brain is in the middle, its tasks on the side, the permissions it
// waits for inline in the conversation, and the settings behind.

//go:embed web/templates/*.html web/static/*
var webFS embed.FS

// WebConfig turns the workspace on.
type WebConfig struct {
	Listen   string `json:"listen"` // default 0.0.0.0:8443
	CertFile string `json:"cert_file,omitempty"`
	KeyFile  string `json:"key_file,omitempty"`
	// Names and addresses for the self-signed certificate, besides this
	// machine's own and public_url's.
	Hostnames   []string `json:"hostnames,omitempty"`
	Lang        string   `json:"lang,omitempty"`         // fr (default) or en
	SessionDays int      `json:"session_days,omitempty"` // how long a login lasts, 30 by default
}

type web struct {
	c         *core
	cfg       WebConfig
	now       func() time.Time
	pages     map[string]*template.Template
	limiter   *loginLimiter
	testLimit *testLimiter // the tool form's connection tests
	setup     string       // the one-time code that creates the owner's password
	trusted   bool         // served with a certificate of the configuration: HSTS
	sessions  time.Duration
	idle      time.Duration

	mu   sync.Mutex
	wake chan struct{} // closed and replaced when something happens here
}

func newWeb(c *core, cfg WebConfig) (*web, error) {
	if cfg.Listen == "" {
		cfg.Listen = "0.0.0.0:8443"
	}
	if cfg.Lang != "en" {
		cfg.Lang = "fr"
	}
	if cfg.SessionDays <= 0 {
		cfg.SessionDays = 30
	}
	w := &web{c: c, cfg: cfg, now: time.Now, limiter: newLoginLimiter(), testLimit: newTestLimiter(), wake: make(chan struct{}),
		trusted: cfg.CertFile != "", sessions: time.Duration(cfg.SessionDays) * 24 * time.Hour, idle: 7 * 24 * time.Hour}
	if err := w.parse(); err != nil {
		return nil, err
	}
	if !w.hasOwner() {
		w.setup = setupCode()
	}
	return w, nil
}

// parse reads the templates: every page is the layout plus its own file.
func (w *web) parse() error {
	w.pages = map[string]*template.Template{}
	files, err := fs.Glob(webFS, "web/templates/*.html")
	if err != nil {
		return err
	}
	for _, f := range files {
		name := strings.TrimSuffix(filepath.Base(f), ".html")
		if name == "layout" || name == "parts" || name == "catalog" || name == "tools" || name == "agents" || name == "remote" || name == "folders" {
			continue
		}
		t, err := template.New(name).Funcs(templateFuncs("fr")).ParseFS(webFS,
			"web/templates/layout.html", "web/templates/parts.html", "web/templates/catalog.html", "web/templates/tools.html",
			"web/templates/agents.html", "web/templates/remote.html", "web/templates/folders.html", f)
		if err != nil {
			return err
		}
		w.pages[name] = t
	}
	return nil
}

// notify wakes the event streams at once, rather than at their next tick.
func (w *web) notify() {
	w.mu.Lock()
	close(w.wake)
	w.wake = make(chan struct{})
	w.mu.Unlock()
}

func (w *web) waker() <-chan struct{} {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.wake
}

func (w *web) routes() http.Handler {
	mux := http.NewServeMux()
	static, _ := fs.Sub(webFS, "web/static")
	files := http.FileServerFS(static)
	mux.Handle("GET /static/", http.StripPrefix("/static/", cacheFor(files, 24*time.Hour)))
	// The worker's scope is where it is served from: the root.
	mux.Handle("GET /sw.js", http.StripPrefix("/", files))
	mux.Handle("GET /manifest.webmanifest", http.StripPrefix("/", files))
	mux.HandleFunc("GET /health", func(rw http.ResponseWriter, _ *http.Request) { rw.Write([]byte("ok\n")) })

	mux.HandleFunc("GET /setup", w.handleSetupPage)
	mux.HandleFunc("POST /setup", w.handleSetup)
	mux.HandleFunc("GET /login", w.handleLoginPage)
	mux.HandleFunc("POST /login", w.handleLogin)
	mux.Handle("POST /logout", w.auth(w.handleLogout))
	mux.Handle("POST /lang", w.auth(w.handleLang))

	mux.Handle("GET /{$}", w.auth(w.handleHome))
	mux.Handle("GET /events", w.auth(w.handleEvents))
	// A message carries its attachments: the body may hold them all, and a little more.
	mux.Handle("POST /messages", http.MaxBytesHandler(w.auth(w.handleSend), maxAttachments*maxAttachmentSize+1<<20))
	mux.Handle("GET /attachments/{id}", w.auth(w.handleAttachment))
	mux.Handle("GET /tasks/{id}", w.auth(w.handleTask))
	mux.Handle("POST /tasks/{id}/answer", w.auth(w.handleAnswer))
	mux.Handle("POST /tasks/{id}/reply", w.auth(w.handleReply))
	mux.Handle("POST /tasks/{id}/stop", w.auth(w.handleStop))

	mux.Handle("GET /settings", w.auth(func(rw http.ResponseWriter, r *http.Request, s *session) {
		http.Redirect(rw, r, "/settings/machines", http.StatusSeeOther)
	}))
	mux.Handle("GET /settings/machines", w.auth(w.handleMachines))
	mux.Handle("POST /settings/machines/join", w.auth(w.handleJoinCommand))
	mux.Handle("POST /settings/machines/tls", w.auth(w.handleNodesTLS))
	mux.Handle("POST /settings/updates/policy", w.auth(w.handleUpdatePolicy))
	mux.Handle("POST /settings/updates/request", w.auth(w.handleUpdateNow))
	mux.Handle("POST /settings/machines/{name}/forget", w.auth(w.handleForget))
	mux.Handle("POST /settings/machines/{name}/rejoin", w.auth(w.handleRejoinCommand))
	mux.Handle("POST /settings/voice-token", w.auth(w.handleVoiceToken))
	w.agentRoutes(mux) // web_agents.go
	mux.Handle("GET /settings/tools", w.auth(w.handleTools))
	mux.Handle("GET /settings/tools/new", w.auth(w.handleNewTool))
	mux.Handle("GET /settings/tools/add", w.auth(w.handleGallery))
	mux.Handle("POST /settings/tools", w.auth(w.handleSaveTool))
	mux.Handle("GET /settings/tools/{name}/edit", w.auth(w.handleEditTool))
	mux.Handle("POST /settings/tools/{name}", w.auth(w.handleSaveTool))
	mux.Handle("POST /settings/tools/{name}/delete", w.auth(w.handleDeleteTool))
	mux.Handle("POST /settings/tool-test", w.auth(w.handleTestTool))
	mux.Handle("POST /settings/tool-test/{name}", w.auth(w.handleTestTool))
	// The vault and the tools were one page: its address leads to the tools.
	mux.Handle("GET /settings/accounts", w.auth(func(rw http.ResponseWriter, r *http.Request, s *session) {
		http.Redirect(rw, r, "/settings/tools", http.StatusSeeOther)
	}))
	mux.Handle("POST /settings/secrets", w.auth(w.handleSetSecret))
	mux.Handle("POST /settings/secrets/import", w.auth(w.handleImportSecret))
	mux.Handle("POST /settings/secrets/{name}/delete", w.auth(w.handleDeleteSecret))
	mux.Handle("POST /settings/servers/{name}/connect", w.auth(w.handleServerConnect))
	mux.Handle("POST /settings/servers/{name}/callback", w.auth(w.handleServerCallback))
	// The authorization redirect comes from the vendor's site, without the
	// session cookie (SameSite=Strict): the flow's own cookie binds it.
	mux.HandleFunc("GET /oauth/callback", w.handleOAuthCallback)
	mux.Handle("GET /settings/journal", w.auth(w.handleJournal))
	mux.Handle("GET /settings/account", w.auth(w.handleAccount))
	mux.Handle("POST /settings/password", w.auth(w.handlePassword))
	w.memoryRoutes(mux)  // web_memory.go
	w.remoteRoutes(mux)  // web_remote.go
	w.driveRoutes(mux)   // web_drive.go
	w.folderRoutes(mux)  // web_folders.go
	w.welcomeRoutes(mux) // web_welcome.go
	return w.headers(mux)
}

var (
	assetOnce sync.Once
	assetHash string
)

// assetVersion changes with the content of the static files, so a browser
// fetches the new stylesheet and scripts after each update instead of
// keeping the cached ones.
func assetVersion() string {
	assetOnce.Do(func() {
		h := sha256.New()
		fs.WalkDir(webFS, "web/static", func(path string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() {
				raw, _ := webFS.ReadFile(path)
				h.Write([]byte(path))
				h.Write(raw)
			}
			return nil
		})
		assetHash = hex.EncodeToString(h.Sum(nil))[:12]
	})
	return assetHash
}

func cacheFor(h http.Handler, d time.Duration) http.Handler {
	return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		rw.Header().Set("Cache-Control", fmt.Sprintf("public, max-age=%d", int(d.Seconds())))
		h.ServeHTTP(rw, r)
	})
}

// headers sets the security headers of every answer: no inline script or
// style, no framing, no referrer to other sites, and HSTS on a trusted certificate.
func (w *web) headers(next http.Handler) http.Handler {
	const csp = "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data: blob:; " +
		"connect-src 'self'; font-src 'self'; manifest-src 'self'; worker-src 'self'; object-src 'none'; " +
		"base-uri 'none'; form-action 'self'; frame-ancestors 'none'"
	return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		h := rw.Header()
		h.Set("Content-Security-Policy", csp)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin") // "no-referrer" would make forms send Origin: null
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		h.Set("Cross-Origin-Resource-Policy", "same-origin")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=()")
		if w.trusted {
			h.Set("Strict-Transport-Security", "max-age=31536000")
		}
		next.ServeHTTP(rw, r)
	})
}

// page is what every page's template gets.
type page struct {
	Lang    string
	Title   string
	Nav     string // conversation or settings
	Tab     string // the settings tab
	View    string // a form within the tab
	CSRF    string
	User    string
	Version string // of the static files, for the browser's cache (assetVersion)
	Build   string // eriphe-core's version
	Updates updateNotice
	Data    any
}

// updateNotice is what runs behind its latest version (updates.go), for
// the discreet marks of the navigation and the account.
type updateNotice struct {
	Core     string // eriphe-core's latest version, when this one is behind
	Commands []updateCommand
	Nodes    bool // a machine is behind
}

func (w *web) updateNotice() updateNotice {
	var n updateNotice
	if _, p := w.c.updatePolicy(coreMachine); p != updateNever && w.c.updates.behind("eriphe-core", version) {
		n.Core, n.Commands = w.c.updates.version("eriphe-core"), updateCommands(coreInstall)
	}
	nodes, _ := w.c.store.nodes()
	for _, row := range nodes {
		if _, p := w.c.updatePolicy("node:" + row.Name); p != updateNever && w.c.updates.behind("eriphe-node", parseInfo(row).Version) {
			n.Nodes = true
			break
		}
	}
	return n
}

// render executes a page, or one of its templates (a fragment for htmx).
func (w *web) render(rw http.ResponseWriter, r *http.Request, s *session, name, block string, p page) {
	t, ok := w.pages[name]
	if !ok {
		http.Error(rw, "no page "+name, http.StatusInternalServerError)
		return
	}
	p.Lang = w.lang(r)
	p.Version = assetVersion()
	p.Build = version
	if block == "" {
		p.Updates = w.updateNotice()
	}
	p.User = w.c.conf().User
	if s != nil {
		p.CSRF = s.CSRF
	}
	t, err := t.Clone()
	if err == nil {
		t = t.Funcs(templateFuncs(p.Lang))
		if block == "" {
			block = "layout"
		}
		var buf bytes.Buffer
		if err = t.ExecuteTemplate(&buf, block, p); err == nil {
			rw.Header().Set("Content-Type", "text/html; charset=utf-8")
			rw.Header().Set("Cache-Control", "no-store")
			buf.WriteTo(rw)
			return
		}
	}
	log.Printf("web: %s/%s: %v", name, block, err)
	http.Error(rw, "template error", http.StatusInternalServerError)
}

// fragment renders a named template of a page to a string, for the event stream.
func (w *web) fragment(lang, name, block string, data any) (string, error) {
	t, err := w.pages[name].Clone()
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	err = t.Funcs(templateFuncs(lang)).ExecuteTemplate(&buf, block, data)
	return buf.String(), err
}

func (w *web) lang(r *http.Request) string {
	if c, err := r.Cookie("lang"); err == nil && (c.Value == "fr" || c.Value == "en") {
		return c.Value
	}
	return w.cfg.Lang
}

// serveWeb starts the workspace's HTTPS listener.
func (c *core) serveWeb(cfg *Config) error {
	w, err := newWeb(c, *cfg.Web)
	if err != nil {
		return err
	}
	cert, err := w.certificate(cfg)
	if err != nil {
		return fmt.Errorf("certificate: %w", err)
	}
	if w.setup != "" {
		log.Printf("web: no password yet: open https://%s/setup with the setup code %s", w.cfg.Listen, w.setup)
	}
	handler := w.routes()
	if c.remote != nil {
		// The same workspace on the tailnet, once remote access is on (remote.go).
		c.remote.setWorkspace(tailnetHandler(c.remote.name, handler))
		c.remote.boot()
	}
	srv := &http.Server{
		Addr:              w.cfg.Listen,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
		TLSConfig:         &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{cert}},
		ErrorLog:          log.New(tlsNoise{}, "", 0),
	}
	log.Printf("web: workspace on https://%s", w.cfg.Listen)
	return srv.ListenAndServeTLS("", "")
}

// tlsNoise drops the handshake errors of browsers that do not trust the
// self-signed certificate yet, and logs the rest.
type tlsNoise struct{}

func (tlsNoise) Write(p []byte) (int, error) {
	if !bytes.Contains(p, []byte("TLS handshake error")) {
		log.Printf("web: %s", bytes.TrimSpace(p))
	}
	return len(p), nil
}
