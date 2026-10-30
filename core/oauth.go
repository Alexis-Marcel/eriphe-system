package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
	"golang.org/x/oauth2"
)

// OAuth for catalog servers, per the MCP authorization spec, with the
// go-sdk's authorization code handler: protected resource metadata,
// authorization server metadata, a client (Client ID Metadata Document,
// pre-registered, or registered dynamically), PKCE, the resource
// parameter, the iss check, refresh and step-up.
//
// The client registration and the tokens are kept in eriphe-core's store,
// so a restart reuses them. Tokens are secrets: never logged, never in a
// task's spec; a rotated refresh token is written before the new access
// token is used.
//
// eriphe-core cannot open a browser: `eriphe connect <name>` does. It asks
// for a flow (POST /v1/servers/{name}/connect with its loopback redirect),
// gets the authorization URL, receives the redirect on 127.0.0.1 and hands
// the code and state over (POST /v1/servers/{name}/callback). Without a
// flow, a server that wants an authorization is marked needs_auth.

const flowTimeout = 10 * time.Minute

var errNeedsAuth = errors.New("needs an authorization")

const oauthSchema = `CREATE TABLE IF NOT EXISTS oauth_clients (
  server TEXT PRIMARY KEY, client_id TEXT NOT NULL, client_secret TEXT NOT NULL,
  redirect_uri TEXT NOT NULL, created INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS oauth_tokens (
  server TEXT PRIMARY KEY, client TEXT NOT NULL, token TEXT NOT NULL, updated INTEGER NOT NULL);`

func init() { extraSchema = append(extraSchema, oauthSchema) }

// oauthClient is a dynamically registered client, kept for later flows.
type oauthClient struct {
	ClientID     string
	ClientSecret string
	RedirectURI  string
}

func (s *Store) saveOAuthClient(server string, c oauthClient) error {
	_, err := s.db.Exec(`INSERT INTO oauth_clients (server, client_id, client_secret, redirect_uri, created) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(server) DO UPDATE SET client_id = excluded.client_id, client_secret = excluded.client_secret,
		redirect_uri = excluded.redirect_uri, created = excluded.created`,
		server, c.ClientID, c.ClientSecret, c.RedirectURI, time.Now().Unix())
	return err
}

func (s *Store) oauthClient(server string) *oauthClient {
	var c oauthClient
	err := s.db.QueryRow(`SELECT client_id, client_secret, redirect_uri FROM oauth_clients WHERE server = ?`, server).
		Scan(&c.ClientID, &c.ClientSecret, &c.RedirectURI)
	if err != nil {
		return nil
	}
	return &c
}

// tokenClient is what a refresh needs besides the token.
type tokenClient struct {
	ClientID     string   `json:"client_id"`
	ClientSecret string   `json:"client_secret,omitempty"`
	TokenURL     string   `json:"token_url"`
	AuthStyle    int      `json:"auth_style"`
	Scopes       []string `json:"scopes,omitempty"`
}

func (t tokenClient) config() *oauth2.Config {
	return &oauth2.Config{ClientID: t.ClientID, ClientSecret: t.ClientSecret, Scopes: t.Scopes,
		Endpoint: oauth2.Endpoint{TokenURL: t.TokenURL, AuthStyle: oauth2.AuthStyle(t.AuthStyle)}}
}

type storedToken struct {
	AccessToken  string    `json:"access_token"`
	TokenType    string    `json:"token_type,omitempty"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	Expiry       time.Time `json:"expiry,omitzero"`
}

func (s *Store) saveOAuthToken(server string, cfg *oauth2.Config, tok *oauth2.Token) error {
	client, _ := json.Marshal(tokenClient{ClientID: cfg.ClientID, ClientSecret: cfg.ClientSecret,
		TokenURL: cfg.Endpoint.TokenURL, AuthStyle: int(cfg.Endpoint.AuthStyle), Scopes: cfg.Scopes})
	raw, _ := json.Marshal(storedToken{AccessToken: tok.AccessToken, TokenType: tok.TokenType,
		RefreshToken: tok.RefreshToken, Expiry: tok.Expiry})
	_, err := s.db.Exec(`INSERT INTO oauth_tokens (server, client, token, updated) VALUES (?, ?, ?, ?)
		ON CONFLICT(server) DO UPDATE SET client = excluded.client, token = excluded.token, updated = excluded.updated`,
		server, string(client), string(raw), time.Now().Unix())
	return err
}

func (s *Store) oauthToken(server string) (*oauth2.Config, *oauth2.Token, error) {
	var client, raw string
	err := s.db.QueryRow(`SELECT client, token FROM oauth_tokens WHERE server = ?`, server).Scan(&client, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	var tc tokenClient
	var st storedToken
	if err := json.Unmarshal([]byte(client), &tc); err != nil {
		return nil, nil, err
	}
	if err := json.Unmarshal([]byte(raw), &st); err != nil {
		return nil, nil, err
	}
	return tc.config(), &oauth2.Token{AccessToken: st.AccessToken, TokenType: st.TokenType,
		RefreshToken: st.RefreshToken, Expiry: st.Expiry}, nil
}

// persistingSource saves every new token before handing it out: a refresh
// that rotates the refresh token is on disk before the access token is used.
type persistingSource struct {
	mu    sync.Mutex
	store *Store
	name  string
	cfg   *oauth2.Config
	base  oauth2.TokenSource
	last  *oauth2.Token
}

func (s *persistingSource) Token() (*oauth2.Token, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, err := s.base.Token()
	if err != nil {
		return nil, err
	}
	if s.last == nil || t.AccessToken != s.last.AccessToken || t.RefreshToken != s.last.RefreshToken {
		if err := s.store.saveOAuthToken(s.name, s.cfg, t); err != nil {
			// Never the token itself.
			log.Printf("server %s: saving its token: %v", s.name, err)
		}
		s.last = t
	}
	return t, nil
}

func (p *pool) persisting(ctx context.Context, name string, cfg *oauth2.Config, tok *oauth2.Token, saved bool) *persistingSource {
	s := &persistingSource{store: p.store, name: name, cfg: cfg, base: cfg.TokenSource(ctx, tok)}
	if saved {
		s.last = tok
	}
	return s
}

// authFlow is one `eriphe connect` in progress, or one Connect of the
// workspace. A workspace flow is bound to the browser that started it
// (binding, the hash of a cookie it holds) and ends on the workspace's own
// HTTPS callback, found by its state.
type authFlow struct {
	id       string
	redirect string
	binding  string // workspace flows: hash of the browser's flow cookie

	mu       sync.Mutex
	granted  string          // what the scopes asked allow (access.go), once known
	state    string          // the state of the authorization URL, once known
	answered error           // once the redirect came: why another fetch fails
	urls     chan string     // the authorization URL, once known
	results  chan flowResult // the redirect, from the CLI
	done     chan struct{}   // closed when the connection attempt ends
	err      error
	once     sync.Once
}

type flowResult struct {
	res *auth.AuthorizationResult
	err error
}

func newAuthFlow(redirect string) *authFlow {
	return &authFlow{id: randomSecret()[:16], redirect: redirect, urls: make(chan string, 1),
		results: make(chan flowResult, 1), done: make(chan struct{})}
}

func (f *authFlow) finish(err error) {
	f.once.Do(func() {
		f.err = err
		close(f.done)
	})
}

// fetch is the handler's AuthorizationCodeFetcher: it gives the URL to the
// CLI and waits for the redirect.
func (f *authFlow) fetch(ctx context.Context, args *auth.AuthorizationArgs) (*auth.AuthorizationResult, error) {
	f.mu.Lock()
	if f.answered != nil {
		// The client tries again after a refusal: a flow is answered once.
		err := f.answered
		f.mu.Unlock()
		return nil, err
	}
	if u, err := url.Parse(args.URL); err == nil {
		f.state = u.Query().Get("state")
	}
	f.mu.Unlock()
	select {
	case f.urls <- args.URL:
	default:
	}
	select {
	case r := <-f.results:
		f.mu.Lock()
		f.answered = r.err
		if r.err == nil {
			f.answered = errors.New("this authorization was already used")
		}
		f.mu.Unlock()
		return r.res, r.err
	case <-time.After(flowTimeout):
		return nil, errors.New("no authorization within 10 minutes")
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// oauthGate is the OAuth handler of a server's transport. Outside of a
// flow it only lends the stored token: a server asking for an
// authorization is marked needs_auth instead of starting one.
type oauthGate struct {
	u     *upstream
	ts    oauth2.TokenSource
	inner *auth.AuthorizationCodeHandler // only during a flow
}

func (g *oauthGate) TokenSource(ctx context.Context) (oauth2.TokenSource, error) {
	if g.inner != nil {
		return g.inner.TokenSource(ctx)
	}
	if g.ts == nil {
		return nil, nil
	}
	return g.ts, nil
}

func (g *oauthGate) Authorize(ctx context.Context, req *http.Request, resp *http.Response) error {
	if g.inner != nil {
		return g.inner.Authorize(ctx, req, resp)
	}
	defer resp.Body.Close()
	reason := "not authorized yet"
	challenges, _ := oauthex.ParseWWWAuthenticate(resp.Header.Values("WWW-Authenticate"))
	for _, c := range challenges {
		if c.Scheme == "bearer" && c.Params["error"] == "insufficient_scope" {
			reason = "more scopes are needed (" + c.Params["scope"] + ")"
		}
	}
	if resp.StatusCode == http.StatusForbidden && !strings.HasPrefix(reason, "more scopes") {
		return errors.New("forbidden by the server")
	}
	if g.ts != nil && reason == "not authorized yet" {
		reason = "the authorization expired or was revoked"
	}
	g.u.mu.Lock()
	g.u.needsAuth = reason
	g.u.status, g.u.err = statusNeedsAuth, reason
	g.u.mu.Unlock()
	return errNeedsAuth
}

// oauthHandler builds the OAuth handler of one connection attempt.
func (p *pool) oauthHandler(u *upstream) (*oauthGate, error) {
	o := u.srv.Auth.OAuth
	refreshCtx := context.WithValue(context.Background(), oauth2.HTTPClient, p.http)
	g := &oauthGate{u: u}
	cfg, tok, err := p.store.oauthToken(u.name)
	if err != nil {
		return nil, err
	}
	if tok != nil {
		g.ts = p.persisting(refreshCtx, u.name, cfg, tok, true)
	}
	u.mu.Lock()
	flow := u.flow
	u.mu.Unlock()
	if flow == nil {
		return g, nil
	}
	// A connect asks for a new authorization: the stored token is not tried.
	hc := &http.Client{Transport: &registrationCapture{base: transportOf(p.http), save: func(c oauthClient) {
		if err := p.store.saveOAuthClient(u.name, c); err != nil {
			log.Printf("server %s: saving its client registration: %v", u.name, err)
		}
	}}}
	hcfg := &auth.AuthorizationCodeHandlerConfig{
		RedirectURL:              flow.redirect,
		AuthorizationCodeFetcher: flow.fetch,
		RequestRefreshToken:      true,
		AcceptUnadvertisedIss:    true,
		Client:                   hc,
		NewTokenSource: func(ctx context.Context, oc *oauth2.Config, t *oauth2.Token) (oauth2.TokenSource, error) {
			if err := p.store.saveOAuthToken(u.name, oc, t); err != nil {
				return nil, err
			}
			flow.mu.Lock()
			granted := flow.granted
			flow.mu.Unlock()
			if granted != "" {
				p.store.set(tokenAccessKey(u.name), granted)
			}
			return p.persisting(ctx, u.name, oc, t, true), nil
		},
	}
	// The scopes follow the server's access level where it advertises a
	// read-only one; scopes of the configuration are asked as they are.
	preset, _ := presetByURL(u.srv.URL)
	level, fixed := u.srv.level(), o.Scopes
	hcfg.ScopeFilter = func(found []string) []string {
		if len(fixed) > 0 {
			found = fixed
		}
		scopes, granted := scopesFor(preset.ID, level, found, len(fixed) > 0)
		flow.mu.Lock()
		flow.granted = granted
		flow.mu.Unlock()
		return scopes
	}
	if o.ClientMetadata != "" {
		hcfg.ClientIDMetadataDocumentConfig = &auth.ClientIDMetadataDocumentConfig{URL: o.ClientMetadata}
	}
	switch reg := p.store.oauthClient(u.name); {
	case o.ClientIDFile != "" || u.clientID != "":
		// A pending server's client is typed in the form, not stored yet.
		id, secret := u.clientID, u.clientSecret
		var err error
		if id == "" {
			if id, err = readSecret(o.ClientIDFile); err != nil {
				return nil, err
			}
		}
		if secret == "" && o.ClientSecretFile != "" {
			if secret, err = readSecret(o.ClientSecretFile); err != nil {
				return nil, err
			}
		}
		pre := &oauthex.ClientCredentials{ClientID: id}
		if secret != "" {
			pre.ClientSecretAuth = &oauthex.ClientSecretAuth{ClientSecret: secret}
		}
		hcfg.PreregisteredClient = pre
	case reg != nil && reg.RedirectURI == flow.redirect:
		// The client registered before, for this redirect.
		pre := &oauthex.ClientCredentials{ClientID: reg.ClientID}
		if reg.ClientSecret != "" {
			pre.ClientSecretAuth = &oauthex.ClientSecretAuth{ClientSecret: reg.ClientSecret}
		}
		hcfg.PreregisteredClient = pre
	default:
		hcfg.DynamicClientRegistrationConfig = &auth.DynamicClientRegistrationConfig{Metadata: &oauthex.ClientRegistrationMetadata{
			ClientName:              "Eriphe",
			RedirectURIs:            []string{flow.redirect},
			GrantTypes:              []string{"authorization_code", "refresh_token"},
			ResponseTypes:           []string{"code"},
			TokenEndpointAuthMethod: "none",
		}}
	}
	h, err := auth.NewAuthorizationCodeHandler(hcfg)
	if err != nil {
		return nil, err
	}
	g.inner = h
	return g, nil
}

func transportOf(c *http.Client) http.RoundTripper {
	if c != nil && c.Transport != nil {
		return c.Transport
	}
	return http.DefaultTransport
}

// registrationCapture keeps the answer of a dynamic client registration
// (a JSON POST with redirect_uris), which the handler does not expose, so
// that later flows reuse the client instead of registering again.
type registrationCapture struct {
	base http.RoundTripper
	save func(oauthClient)
}

func (r *registrationCapture) RoundTrip(req *http.Request) (*http.Response, error) {
	var sent struct {
		RedirectURIs []string `json:"redirect_uris"`
	}
	if req.Method == http.MethodPost && req.Body != nil && strings.Contains(req.Header.Get("Content-Type"), "json") {
		body, err := io.ReadAll(req.Body)
		req.Body.Close()
		if err != nil {
			return nil, err
		}
		req = req.Clone(req.Context())
		req.Body = io.NopCloser(bytes.NewReader(body))
		json.Unmarshal(body, &sent)
	}
	resp, err := r.base.RoundTrip(req)
	if err != nil || len(sent.RedirectURIs) == 0 || resp.StatusCode >= 300 {
		return resp, err
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return nil, err
	}
	resp.Body = io.NopCloser(bytes.NewReader(body))
	var got struct {
		ClientID     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`
	}
	if json.Unmarshal(body, &got) == nil && got.ClientID != "" {
		r.save(oauthClient{ClientID: got.ClientID, ClientSecret: got.ClientSecret, RedirectURI: sent.RedirectURIs[0]})
	}
	return resp, nil
}

// The connect API, behind the bearer token.

func (c *core) serverRoutes(mux *http.ServeMux, token string) {
	mux.Handle("GET /v1/servers", bearer(token, http.HandlerFunc(c.handleServers)))
	mux.Handle("POST /v1/servers/{name}/connect", bearer(token, http.HandlerFunc(c.handleServerConnect)))
	mux.Handle("POST /v1/servers/{name}/callback", bearer(token, http.HandlerFunc(c.handleServerCallback)))
}

func (c *core) handleServers(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, c.listServers("", false))
}

// handleServerConnect starts a flow: {redirect_uri} → {flow, auth_url}, or
// {status} if the server connected without needing one. The API takes
// only loopback redirects: the CLI's.
func (c *core) handleServerConnect(w http.ResponseWriter, r *http.Request) {
	var in struct {
		RedirectURI string `json:"redirect_uri"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || !loopbackRedirect(in.RedirectURI) {
		if c.pool.get(r.PathValue("name")) == nil {
			http.Error(w, "no such server", http.StatusNotFound)
			return
		}
		http.Error(w, "redirect_uri must be http://127.0.0.1:<port>/callback", http.StatusBadRequest)
		return
	}
	f, authURL, err := c.startConnect(r.Context(), r.PathValue("name"), in.RedirectURI, "")
	switch {
	case err != nil:
		var ce *catalogError
		if errors.As(err, &ce) {
			http.Error(w, err.Error(), ce.Status)
			return
		}
		http.Error(w, err.Error(), http.StatusBadGateway)
	case authURL != "":
		writeJSON(w, http.StatusOK, map[string]string{"flow": f.id, "auth_url": authURL})
	default:
		writeJSON(w, http.StatusOK, map[string]string{"status": statusReady})
	}
}

// startConnect starts an authorization of an OAuth server, redirected to
// redirect: it returns the URL to open, or none if the server connected
// without one. binding ties a workspace flow to its browser.
func (c *core) startConnect(ctx context.Context, name, redirect, binding string) (*authFlow, string, error) {
	u := c.pool.get(name)
	if u == nil {
		return nil, "", &catalogError{http.StatusNotFound, "catalog.notfound", name, errors.New("no such server")}
	}
	if u.srv.authKind() != "oauth" {
		return nil, "", &catalogError{http.StatusConflict, "", "", errors.New("this server does not use OAuth: nothing to connect")}
	}
	if want := u.srv.Auth.OAuth.RedirectURI; want != "" && redirect != want {
		return nil, "", &catalogError{http.StatusBadRequest, "", "", errors.New("this server's client is registered for " + want)}
	}
	f := newAuthFlow(redirect)
	f.binding = binding
	u.mu.Lock()
	old := u.flow
	u.flow = f
	u.mu.Unlock()
	if old != nil {
		select {
		case old.results <- flowResult{err: errors.New("replaced by a new connect")}:
		default:
		}
	}
	u.reconnect()
	select {
	case authURL := <-f.urls:
		return f, authURL, nil
	case <-f.done:
		return f, "", f.err
	case <-time.After(time.Minute):
		return nil, "", errors.New("the server did not ask for an authorization in time")
	case <-ctx.Done():
		return nil, "", ctx.Err()
	}
}

// flowByState finds the workspace flow an authorization redirect answers.
func (p *pool) flowByState(state string) (*upstream, *authFlow) {
	if state == "" {
		return nil, nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	ups := slices.Collect(maps.Values(p.ups))
	for _, ps := range p.pending {
		ups = append(ups, ps.u)
	}
	for _, u := range ups {
		u.mu.Lock()
		f := u.flow
		u.mu.Unlock()
		if f == nil || f.binding == "" {
			continue
		}
		f.mu.Lock()
		match := f.state == state
		f.mu.Unlock()
		if match {
			return u, f
		}
	}
	return nil, nil
}

// finishFlow hands a redirect's parameters to a flow and waits for the
// server to connect: its status, error and tool count.
func finishFlow(ctx context.Context, u *upstream, f *authFlow, code, state, iss, errCode, errDesc string) (string, string, int, error) {
	res := flowResult{res: &auth.AuthorizationResult{Code: code, State: state, Iss: iss}}
	if errCode != "" {
		res = flowResult{err: fmt.Errorf("authorization refused: %s %s", errCode, errDesc)}
	} else if code == "" {
		res = flowResult{err: errors.New("no code in the redirect")}
	}
	select {
	case f.results <- res:
	default:
		return "", "", 0, errors.New("this flow already has its answer")
	}
	select {
	case <-f.done:
	case <-time.After(2 * time.Minute):
	case <-ctx.Done():
		return "", "", 0, ctx.Err()
	}
	status, errText, tools := u.snapshot()
	if f.err != nil && errText == "" {
		errText = f.err.Error()
	}
	return status, errText, len(tools), nil
}

// handleServerCallback hands the redirect over and waits for the server to
// connect: {flow, code, state, iss} or {flow, error}.
func (c *core) handleServerCallback(w http.ResponseWriter, r *http.Request) {
	u := c.pool.get(r.PathValue("name"))
	if u == nil {
		http.Error(w, "no such server", http.StatusNotFound)
		return
	}
	var in struct {
		Flow        string `json:"flow"`
		Code        string `json:"code"`
		State       string `json:"state"`
		Iss         string `json:"iss"`
		Error       string `json:"error"`
		Description string `json:"error_description"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	u.mu.Lock()
	f := u.flow
	u.mu.Unlock()
	if f == nil || f.id != in.Flow {
		http.Error(w, "no such flow: run eriphe connect again", http.StatusNotFound)
		return
	}
	status, errText, tools, err := finishFlow(r.Context(), u, f, in.Code, in.State, in.Iss, in.Error, in.Description)
	if err != nil {
		if r.Context().Err() == nil {
			http.Error(w, err.Error(), http.StatusConflict)
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": status, "error": errText, "tools": tools})
}
