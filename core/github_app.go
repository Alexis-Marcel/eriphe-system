package main

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Eriphe's GitHub App. Each user creates their own in one click, from a
// manifest eriphe-core writes (GitHub's manifest flow): the browser posts
// it to github.com, GitHub creates the app and sends the browser back to
// the workspace with a code, which eriphe-core exchanges for the app's id
// and private key, kept in the vault (one secret, github-app). The user
// then installs it on the repositories Eriphe may work on.
//
// The app mints the credentials of tasks: an installation token restricted
// to one repository and the rights asked for, valid one hour
// (POST /app/installations/{id}/access_tokens). The GitHub MCP tool can use
// it too ({"auth": {"github_app": true}}): an installation token, refreshed
// before it ends. One GitHub connection for the tool and for git.

var errNoGitHubApp = errors.New("no GitHub App")

// githubAppVault is the vault's secret holding the app.
const githubAppVault = "github-app"

// githubAppSecret is what the vault keeps of the app.
type githubAppSecret struct {
	ID            int64  `json:"id"`
	Slug          string `json:"slug"`
	Name          string `json:"name"`
	Owner         string `json:"owner"`
	HTMLURL       string `json:"html_url"`
	PEM           string `json:"pem"`
	WebhookSecret string `json:"webhook_secret,omitempty"`
	ClientID      string `json:"client_id,omitempty"`
	ClientSecret  string `json:"client_secret,omitempty"`
}

// githubApp is the app, its key parsed.
type githubApp struct {
	githubAppSecret
	key *rsa.PrivateKey
}

func loadGitHubApp() (*githubApp, error) {
	v := currentVault()
	if v == nil {
		return nil, errNoGitHubApp
	}
	raw, err := v.get(githubAppVault)
	if errors.Is(err, errNoSecret) {
		return nil, errNoGitHubApp
	}
	if err != nil {
		return nil, err
	}
	var s githubAppSecret
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		return nil, fmt.Errorf("the GitHub App in the vault does not read: %w", err)
	}
	key, err := parseRSAKey(s.PEM)
	if err != nil {
		return nil, fmt.Errorf("the GitHub App's private key: %w", err)
	}
	return &githubApp{githubAppSecret: s, key: key}, nil
}

func storeGitHubApp(s githubAppSecret) error {
	v := currentVault()
	if v == nil {
		return errNoVault
	}
	if _, err := parseRSAKey(s.PEM); err != nil {
		return fmt.Errorf("GitHub sent a private key that does not read: %w", err)
	}
	raw, _ := json.Marshal(s)
	return v.set(githubAppVault, string(raw))
}

func forgetGitHubApp() error {
	v := currentVault()
	if v == nil {
		return errNoVault
	}
	if err := v.remove(githubAppVault); err != nil && !errors.Is(err, errNoSecret) {
		return err
	}
	return nil
}

func parseRSAKey(p string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(p))
	if block == nil {
		return nil, errors.New("no PEM block")
	}
	if k, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return k, nil
	}
	k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	rk, ok := k.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("not an RSA key")
	}
	return rk, nil
}

// jwt is the app's own token, to call GitHub as the app: RS256, issued a
// minute ago (clock drift), valid nine minutes (GitHub takes ten at most).
func (a *githubApp) jwt(now time.Time) (string, error) {
	enc := base64.RawURLEncoding
	header := enc.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	claims, _ := json.Marshal(map[string]any{"iat": now.Add(-time.Minute).Unix(), "exp": now.Add(9 * time.Minute).Unix(),
		"iss": strconv.FormatInt(a.ID, 10)})
	input := header + "." + enc.EncodeToString(claims)
	sum := sha256.Sum256([]byte(input))
	sig, err := rsa.SignPKCS1v15(nil, a.key, crypto.SHA256, sum[:])
	if err != nil {
		return "", err
	}
	return input + "." + enc.EncodeToString(sig), nil
}

// githubClient calls GitHub's API; its addresses are replaced in tests.
type githubClient struct {
	api, web string
	http     *http.Client
	now      func() time.Time

	flows githubFlows // the app's creations in progress (web_github.go)

	mu sync.Mutex
	// The GitHub tool's installation tokens: with all the app's rights, and
	// with their reading only.
	toolToks [2]cachedToken
}

// cachedToken is a token and when it ends.
type cachedToken struct {
	tok string
	exp time.Time
}

func newGitHubClient() *githubClient {
	return &githubClient{api: "https://api.github.com", web: "https://github.com",
		http: &http.Client{Timeout: 20 * time.Second}, now: time.Now}
}

type githubError struct {
	Status  int
	Message string
}

func (e *githubError) Error() string { return fmt.Sprintf("GitHub: %d %s", e.Status, e.Message) }

// do calls the API; auth is "Bearer <jwt>" or "token <…>", or "".
func (g *githubClient) do(ctx context.Context, method, path, auth string, in, out any) error {
	var body io.Reader
	if in != nil {
		raw, _ := json.Marshal(in)
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimSuffix(g.api, "/")+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "eriphe-core")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	resp, err := g.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		var e struct {
			Message string `json:"message"`
		}
		json.Unmarshal(raw, &e)
		return &githubError{Status: resp.StatusCode, Message: orStr(e.Message, strings.TrimSpace(truncate(string(raw), 200)))}
	}
	if out != nil {
		return json.Unmarshal(raw, out)
	}
	return nil
}

func (g *githubClient) asApp(app *githubApp) (string, error) {
	tok, err := app.jwt(g.now())
	return "Bearer " + tok, err
}

// installationFor is the app's installation that covers a repository.
func (g *githubClient) installationFor(ctx context.Context, app *githubApp, owner, repo string) (int64, error) {
	auth, err := g.asApp(app)
	if err != nil {
		return 0, err
	}
	var out struct {
		ID int64 `json:"id"`
	}
	err = g.do(ctx, http.MethodGet, "/repos/"+url.PathEscape(owner)+"/"+url.PathEscape(repo)+"/installation", auth, nil, &out)
	var ge *githubError
	if errors.As(err, &ge) && ge.Status == http.StatusNotFound {
		return 0, fmt.Errorf("the GitHub App %s is not installed on %s/%s: install it on that repository (Settings → Tools → GitHub)", app.Slug, owner, repo)
	}
	if err != nil {
		return 0, err
	}
	return out.ID, nil
}

// installationToken mints a token of an installation, limited to the
// repositories (by name) and the permissions given; GitHub makes it last
// one hour. No repositories and no permissions: all the installation's.
func (g *githubClient) installationToken(ctx context.Context, app *githubApp, inst int64, repos []string, perms map[string]string) (string, time.Time, error) {
	auth, err := g.asApp(app)
	if err != nil {
		return "", time.Time{}, err
	}
	in := map[string]any{}
	if len(repos) > 0 {
		in["repositories"] = repos
	}
	if len(perms) > 0 {
		in["permissions"] = perms
	}
	var out struct {
		Token   string    `json:"token"`
		Expires time.Time `json:"expires_at"`
	}
	err = g.do(ctx, http.MethodPost, fmt.Sprintf("/app/installations/%d/access_tokens", inst), auth, in, &out)
	var ge *githubError
	if errors.As(err, &ge) && ge.Status == http.StatusUnprocessableEntity {
		return "", time.Time{}, fmt.Errorf("GitHub refused the token (%s): is the repository in the app's installation, and does the app have these permissions?", ge.Message)
	}
	if err != nil {
		return "", time.Time{}, err
	}
	if out.Token == "" {
		return "", time.Time{}, errors.New("GitHub gave no token")
	}
	return out.Token, out.Expires, nil
}

// githubInstallation is where the app is installed.
type githubInstallation struct {
	ID      int64 `json:"id"`
	Account struct {
		Login string `json:"login"`
		Type  string `json:"type"`
	} `json:"account"`
	Selection string `json:"repository_selection"` // all or selected
	HTMLURL   string `json:"html_url"`
}

func (g *githubClient) installations(ctx context.Context, app *githubApp) ([]githubInstallation, error) {
	auth, err := g.asApp(app)
	if err != nil {
		return nil, err
	}
	var out []githubInstallation
	err = g.do(ctx, http.MethodGet, "/app/installations?per_page=100", auth, nil, &out)
	return out, err
}

// githubReadPermissions are the app's rights, downscoped to reading: the
// GitHub tool's token when the tool is read only.
var githubReadPermissions = map[string]string{
	"contents": "read", "pull_requests": "read", "issues": "read", "metadata": "read", "checks": "read",
}

// toolToken is the GitHub MCP tool's token on the app: an installation
// token of the app owner's installation (or the only one), with all its
// repositories, and all its rights or (readOnly) their reading only,
// renewed five minutes before it ends.
func (g *githubClient) toolToken(ctx context.Context, readOnly bool) (string, error) {
	app, err := loadGitHubApp()
	if err != nil {
		if err == errNoGitHubApp {
			return "", errors.New("Eriphe's GitHub App is not created yet (Settings → Tools → GitHub)")
		}
		return "", err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	slot := 0
	var perms map[string]string
	if readOnly {
		slot, perms = 1, githubReadPermissions
	}
	if c := g.toolToks[slot]; c.tok != "" && c.exp.Sub(g.now()) > 5*time.Minute {
		return c.tok, nil
	}
	list, err := g.installations(ctx, app)
	if err != nil {
		return "", err
	}
	if len(list) == 0 {
		return "", fmt.Errorf("the GitHub App %s is not installed anywhere yet", app.Slug)
	}
	inst := list[0].ID
	for _, i := range list {
		if strings.EqualFold(i.Account.Login, app.Owner) {
			inst = i.ID
		}
	}
	tok, exp, err := g.installationToken(ctx, app, inst, nil, perms)
	if err != nil {
		return "", err
	}
	g.toolToks[slot] = cachedToken{tok, exp}
	return tok, nil
}

// forget drops the cached tool tokens (the app was removed or replaced).
func (g *githubClient) forget() {
	g.mu.Lock()
	g.toolToks = [2]cachedToken{}
	g.mu.Unlock()
}

// The manifest flow (docs.github.com, "Registering a GitHub App from a
// manifest"). The manifest's redirect_url is a browser redirect: a
// workspace on the LAN works, no public address is needed.

// githubAppPermissions are the app's rights, what tasks may ask for at most.
var githubAppPermissions = map[string]string{
	"contents": "write", "pull_requests": "write", "issues": "write", "metadata": "read", "checks": "read",
}

// githubAppManifest describes Eriphe's app; origin is the workspace's
// address, where GitHub sends the browser back.
func githubAppManifest(name, origin string) map[string]any {
	return map[string]any{
		"name":                name,
		"url":                 "https://github.com/Alexis-Marcel/eriphe-system",
		"description":         "Eriphe, a personal assistant: short-lived, per-repository access for its tasks.",
		"redirect_url":        origin + "/github-app/callback",
		"setup_url":           origin + "/github-app/installed",
		"setup_on_update":     true,
		"public":              false,
		"default_permissions": githubAppPermissions,
		"default_events":      []string{},
		// No webhook: eriphe-core asks GitHub, GitHub never calls it.
		"hook_attributes": map[string]any{"url": origin + "/github-app/hook", "active": false},
	}
}

// githubAppName is the app's name: GitHub wants it unique, 34 characters at most.
func githubAppName(suffix string) string {
	suffix = strings.Join(strings.Fields(suffix), " ")
	name := "Eriphe"
	if suffix != "" {
		name += " – " + suffix
	}
	if r := []rune(name); len(r) > 34 {
		name = strings.TrimSpace(string(r[:34]))
	}
	return name
}

// newAppURL is where the browser posts the manifest: the user's account,
// or an organization's.
func (g *githubClient) newAppURL(org, state string) string {
	u := strings.TrimSuffix(g.web, "/") + "/settings/apps/new"
	if org != "" {
		u = strings.TrimSuffix(g.web, "/") + "/organizations/" + url.PathEscape(org) + "/settings/apps/new"
	}
	return u + "?state=" + url.QueryEscape(state)
}

// installURL is where the user installs the app on repositories.
func (g *githubClient) installURL(slug string) string {
	return strings.TrimSuffix(g.web, "/") + "/apps/" + url.PathEscape(slug) + "/installations/new"
}

// convertManifest exchanges the code GitHub sent back (within an hour) for
// the app: its id, slug and private key, and stores it in the vault.
func (g *githubClient) convertManifest(ctx context.Context, code string) (githubAppSecret, error) {
	var out struct {
		ID            int64  `json:"id"`
		Slug          string `json:"slug"`
		Name          string `json:"name"`
		HTMLURL       string `json:"html_url"`
		PEM           string `json:"pem"`
		WebhookSecret string `json:"webhook_secret"`
		ClientID      string `json:"client_id"`
		ClientSecret  string `json:"client_secret"`
		Owner         struct {
			Login string `json:"login"`
		} `json:"owner"`
	}
	if err := g.do(ctx, http.MethodPost, "/app-manifests/"+url.PathEscape(code)+"/conversions", "", nil, &out); err != nil {
		return githubAppSecret{}, err
	}
	s := githubAppSecret{ID: out.ID, Slug: out.Slug, Name: out.Name, Owner: out.Owner.Login, HTMLURL: out.HTMLURL,
		PEM: out.PEM, WebhookSecret: out.WebhookSecret, ClientID: out.ClientID, ClientSecret: out.ClientSecret}
	if s.ID == 0 || s.Slug == "" || s.PEM == "" {
		return s, errors.New("GitHub's answer has no app")
	}
	if err := storeGitHubApp(s); err != nil {
		return s, err
	}
	g.forget()
	return s, nil
}

// tokenTransport adds a token from a source to every request: the GitHub
// tool on the app.
type tokenTransport struct {
	source func(context.Context) (string, error)
	base   http.RoundTripper
}

func (t tokenTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	tok, err := t.source(r.Context())
	if err != nil {
		return nil, err
	}
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+tok)
	return t.base.RoundTrip(r)
}
