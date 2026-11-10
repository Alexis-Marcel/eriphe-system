package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"golang.org/x/oauth2"
)

// fakeProvider is a provider no code knows, given only as data: an
// authorization server with a token endpoint and a revocation one.
type fakeProvider struct {
	*signInProvider
	srv *httptest.Server

	mu        sync.Mutex
	challenge string
	refreshes map[string]string // refresh token → the error a refresh gets ("" works)
	revoked   []string
}

func newFakeProvider(t *testing.T) *fakeProvider {
	f := &fakeProvider{refreshes: map[string]string{}}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		r.ParseForm()
		q := r.Form
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/revoke":
			f.revoked = append(f.revoked, q.Get("token"))
		case q.Get("grant_type") == "refresh_token":
			if e := f.refreshes[q.Get("refresh_token")]; e != "" || q.Get("client_id") != "app-id" {
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(map[string]string{"error": cmpOr(e, "invalid_client")})
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"access_token": "at", "token_type": "Bearer"})
		default:
			sum := sha256.Sum256([]byte(q.Get("code_verifier")))
			if q.Get("code") != "the-code" || q.Get("client_secret") != "app-secret" || base64.RawURLEncoding.EncodeToString(sum[:]) != f.challenge {
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant"})
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"access_token": "at", "refresh_token": "rt", "token_type": "Bearer",
				"scope": "openid email mail.read", "id_token": fakeIDToken("sam@example.org")})
		}
	}))
	t.Cleanup(f.srv.Close)
	old := signInHTTP
	signInHTTP = f.srv.Client()
	t.Cleanup(func() { signInHTTP = old })
	f.signInProvider = &signInProvider{Name: "example", Label: "Example",
		Endpoint:   oauth2.Endpoint{AuthURL: f.srv.URL + "/auth", TokenURL: f.srv.URL + "/token", AuthStyle: oauth2.AuthStyleInParams},
		RevokeURL:  f.srv.URL + "/revoke",
		Scopes:     []string{"openid", "email"},
		AuthParams: map[string]string{"prompt": "consent"},
		ForgetAt:   "example.org/apps"}
	return f
}

func TestSignInFlow(t *testing.T) {
	f := newFakeProvider(t)
	c := signInCore(t)
	app := oauthApp{ID: "app-id", Secret: "app-secret"}
	in := signInStart{Origin: "http://eriphe.lan:8080", Binding: "b", Scopes: []string{"mail.read"}, Hint: "sam@example.org",
		Back: "/back", Asked: []string{"mail"}}

	authURL, err := c.startSignIn(f.signInProvider, app, in)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(authURL)
	q := u.Query()
	if !strings.HasPrefix(authURL, f.srv.URL+"/auth?") || q.Get("scope") != "openid email mail.read" || q.Get("prompt") != "consent" ||
		q.Get("access_type") != "offline" || q.Get("login_hint") != "sam@example.org" || q.Get("code_challenge_method") != "S256" ||
		q.Get("redirect_uri") != "http://eriphe.lan:8080/oauth/callback" || !isSignInState(q.Get("state")) {
		t.Fatalf("auth URL %s", authURL)
	}
	f.challenge = q.Get("code_challenge")

	// Another browser, or another workspace address: refused, and the flow is spent.
	if _, _, err := c.finishSignIn(context.Background(), in.Origin, q.Get("state"), "the-code", "", "other"); err != errBadState {
		t.Fatalf("other browser: %v", err)
	}
	authURL, _ = c.startSignIn(f.signInProvider, app, in)
	u, _ = url.Parse(authURL)
	q = u.Query()
	f.challenge = q.Get("code_challenge")
	if _, _, err := c.finishSignIn(context.Background(), "https://elsewhere.example.org", q.Get("state"), "the-code", "", "b"); err != errBadState {
		t.Fatalf("other origin: %v", err)
	}

	authURL, _ = c.startSignIn(f.signInProvider, app, in)
	u, _ = url.Parse(authURL)
	q = u.Query()
	f.challenge = q.Get("code_challenge")
	fl, tok, err := c.finishSignIn(context.Background(), in.Origin, q.Get("state"), "the-code", "", "b")
	if err != nil || fl.back != "/back" || fl.asked[0] != "mail" || tok.Identity != "sam@example.org" || tok.Refresh != "rt" ||
		strings.Join(tok.Scopes, " ") != "openid email mail.read" {
		t.Fatalf("finish: %+v %+v %v", fl, tok, err)
	}
	var kept lentToken
	if json.Unmarshal([]byte(tok.secret(fl)), &kept) != nil || kept != (lentToken{"app-id", "app-secret", "rt", f.srv.URL + "/token"}) {
		t.Fatalf("secret %s", tok.secret(fl))
	}
	// Replayed: the flow was handed out once.
	if _, _, err := c.finishSignIn(context.Background(), in.Origin, q.Get("state"), "the-code", "", "b"); err != errBadState {
		t.Fatalf("replay: %v", err)
	}

	// Denied at the provider: the flow is found, for where to go back.
	authURL, _ = c.startSignIn(f.signInProvider, app, in)
	u, _ = url.Parse(authURL)
	fl, _, err = c.finishSignIn(context.Background(), in.Origin, u.Query().Get("state"), "", "access_denied", "b")
	wantKey(t, err, "drive.denied")
	if fl == nil || fl.back != "/back" {
		t.Fatalf("denied: %+v", fl)
	}

	// The refresh check, then revocation.
	f.refreshes["gone"] = "invalid_grant"
	for raw, want := range map[string]int{
		kept.secretJSON(): refreshWorks,
		`{"client_id":"app-id","refresh_token":"gone"}`:  refreshRevoked,
		`{"client_id":"other-app","refresh_token":"rt"}`: refreshBadClient,
		`{"client_id":"app-id"}`:                         refreshUnreadable,
		`not json`:                                       refreshUnreadable,
	} {
		if got, err := f.checkRefresh(context.Background(), raw); err != nil || got != want {
			t.Errorf("checkRefresh(%s) = %d %v, want %d", raw, got, err, want)
		}
	}
	f.srv.Close()
	if _, err := f.checkRefresh(context.Background(), kept.secretJSON()); err == nil {
		t.Error("an unreachable provider must say nothing is known")
	}
}

func (l lentToken) secretJSON() string { raw, _ := json.Marshal(l); return string(raw) }

func TestSignInRevoke(t *testing.T) {
	f := newFakeProvider(t)
	f.revoke(context.Background(), `{"client_id":"app-id","refresh_token":"rt"}`)
	f.revoke(context.Background(), `{"client_id":"app-id"}`)
	none := *f.signInProvider
	none.RevokeURL = ""
	none.revoke(context.Background(), `{"refresh_token":"other"}`)
	if len(f.revoked) != 1 || f.revoked[0] != "rt" {
		t.Fatalf("revoked %v", f.revoked)
	}
}

// A provider's data says what a client id looks like and which redirects it
// takes; with none, any.
func TestSignInProviderRules(t *testing.T) {
	p := &signInProvider{Label: "Example", ClientIDSuffix: ".example.org"}
	if _, err := p.checkClient("abc.example.org", "s"); err != nil {
		t.Fatal(err)
	}
	_, err := p.checkClient("abc.example.com", "s")
	wantKey(t, err, "drive.badclient")
	if p.refuses("http://192.0.2.1") != "" {
		t.Error("no rule: any redirect")
	}
	p.Redirect = redirectPublicHTTPS
	if p.refuses("http://192.0.2.1") != "ip" || p.refuses("https://eriphe.example.org") != "" {
		t.Error("public-https")
	}
	c := signInCore(t)
	_, err = c.startSignIn(p, oauthApp{ID: "a", Secret: "b"}, signInStart{Origin: "https://eriphe.local"})
	wantKey(t, err, "drive.origin.local")
}

// signInCore is a core with only a store, for the flows.
func signInCore(t *testing.T) *core {
	t.Helper()
	s, err := openStore(filepath.Join(t.TempDir(), "core.db"))
	if err != nil {
		t.Fatal(err)
	}
	return newCore(&Config{}, s)
}
