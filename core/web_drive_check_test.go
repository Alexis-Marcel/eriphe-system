package main

import (
	"encoding/base64"
	"encoding/json"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
)

// googleCheck is Google as the Drive test sees it: the token endpoint, the
// authorization endpoint and Drive's about.
type googleCheck struct {
	srv *httptest.Server

	mu         sync.Mutex
	clientID   string
	secret     string
	redirects  []string // registered for the client
	refresh    string   // the refresh token Google still takes
	apiOff     bool
	signins    int // authorization requests that reached the sign-in
	followings int // requests to the sign-in pages: none expected
}

const (
	checkRefresh = "rt-very-secret-5678"
	checkAccess  = "at-very-secret-9012"
)

// authError is Google's authError parameter for an error code.
func authError(code string) string {
	msg := append([]byte{0x0a, byte(len(code))}, code...)
	msg = append(msg, 0x12, 4, 't', 'e', 'x', 't')
	return base64.URLEncoding.EncodeToString(msg)
}

func newGoogleCheck(t *testing.T) *googleCheck {
	g := &googleCheck{clientID: testClientID, secret: testClientSecret, refresh: checkRefresh}
	g.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		g.mu.Lock()
		defer g.mu.Unlock()
		r.ParseForm()
		f := r.Form
		jsonErr := func(status int, code, desc string) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			json.NewEncoder(w).Encode(map[string]string{"error": code, "error_description": desc})
		}
		switch r.URL.Path {
		case "/token":
			switch {
			case f.Get("client_id") != g.clientID:
				jsonErr(401, "invalid_client", "The OAuth client was not found.")
			case f.Get("client_secret") != g.secret:
				jsonErr(401, "invalid_client", "The provided client secret is invalid.")
			case f.Get("grant_type") == "authorization_code":
				jsonErr(400, "invalid_grant", "Malformed auth code.")
			case f.Get("grant_type") == "refresh_token" && f.Get("refresh_token") == g.refresh:
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]any{"access_token": checkAccess, "expires_in": 3599, "token_type": "Bearer"})
			default:
				jsonErr(400, "invalid_grant", "Token has been expired or revoked.")
			}
		case "/auth":
			q := r.URL.Query()
			if q.Get("response_type") != "code" || q.Get("code_challenge_method") != "S256" || len(q.Get("code_challenge")) != 43 {
				http.Redirect(w, r, "/signin/oauth/error?authError="+authError("invalid_request"), http.StatusFound)
			} else if q.Get("client_id") != g.clientID {
				http.Redirect(w, r, "/signin/oauth/error?authError="+authError("invalid_client"), http.StatusFound)
			} else if !strings.Contains(strings.Join(g.redirects, " "), q.Get("redirect_uri")) {
				http.Redirect(w, r, "/signin/oauth/error?authError="+authError("redirect_uri_mismatch")+"&client_id="+g.clientID, http.StatusFound)
			} else {
				g.signins++
				http.Redirect(w, r, "/v3/signin/identifier?client_id="+g.clientID, http.StatusFound)
			}
		case "/drive/v3/about":
			if r.Header.Get("Authorization") != "Bearer "+checkAccess {
				jsonErr(401, "unauthorized", "")
				return
			}
			w.Header().Set("Content-Type", "application/json")
			if g.apiOff {
				w.WriteHeader(403)
				w.Write([]byte(`{"error":{"code":403,"message":"Google Drive API has not been used in project 1 before or it is disabled.","errors":[{"reason":"accessNotConfigured"}],"details":[{"reason":"SERVICE_DISABLED"}]}}`))
				return
			}
			if f.Get("fields") != "user(emailAddress,displayName),storageQuota" {
				w.WriteHeader(400)
				return
			}
			w.Write([]byte(`{"user":{"displayName":"Sam Doe","emailAddress":"sam@example.com"},"storageQuota":{"limit":"16106127360","usage":"3435973837"}}`))
		default:
			g.followings++
			w.Write([]byte("<html>sign in</html>"))
		}
	}))
	t.Cleanup(g.srv.Close)
	old, oldAPI := googleOAuth, driveAPI
	googleOAuth.AuthURL, googleOAuth.TokenURL, driveAPI = g.srv.URL+"/auth", g.srv.URL+"/token", g.srv.URL+"/drive/v3"
	t.Cleanup(func() { googleOAuth, driveAPI = old, oldAPI })
	return g
}

// driveTestBody posts the test and returns its lines, checking that no
// secret is shown.
func driveTestBody(t *testing.T, tw *testWeb, csrf string) string {
	t.Helper()
	code, body, _ := tw.post("/settings/drive/test", nil, csrf)
	if code != 200 {
		t.Fatalf("test: %d %s", code, body)
	}
	for _, s := range []string{testClientSecret, checkRefresh, checkAccess} {
		if strings.Contains(body, s) {
			t.Fatalf("a secret is shown: %s", body)
		}
	}
	return html.UnescapeString(body)
}

func wantLines(t *testing.T, body string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(body, w) {
			t.Fatalf("want %q in:\n%s", w, body)
		}
	}
}

func TestDriveTest(t *testing.T) {
	g := newGoogleCheck(t)
	tw, c, csrf := filesCore(t, nil)
	callback := tw.srv.URL + "/oauth/callback"
	g.redirects = []string{callback}

	if _, body, _ := tw.do("GET", "/settings/drive", nil, nil); !strings.Contains(body, `hx-post="/settings/drive/test"`) {
		t.Fatal("no test button")
	}
	if code, _, _ := tw.post("/settings/drive/test", nil, "wrong"); code == 200 {
		t.Fatal("a test without the CSRF token")
	}

	// No Google app yet.
	body := driveTestBody(t, tw, csrf)
	wantLines(t, body, "test-ok", "Google accepte l'adresse de ton espace", "Pas encore d'app Google", "pas encore connecté")

	// A wrong secret, and a redirect URI the client does not list.
	storeGoogleClient(oauthApp{ID: testClientID, Secret: "GOCSPX-wrong"})
	g.redirects = []string{"https://elsewhere.example.org/oauth/callback"}
	body = driveTestBody(t, tw, csrf)
	wantLines(t, body, "L'identifiant ou le code secret de ton app Google est refusé", "The provided client secret is invalid.",
		"n'est pas enregistrée", "ajoute exactement "+callback, "test-err")
	if strings.Contains(body, "GOCSPX-wrong") {
		t.Fatal("the secret is shown")
	}

	// An unknown client: the client line says it, no redirect line.
	storeGoogleClient(oauthApp{ID: "999-zzz" + googleClientIDEnd, Secret: testClientSecret})
	body = driveTestBody(t, tw, csrf)
	wantLines(t, body, "est refusé", "The OAuth client was not found.")
	if strings.Contains(body, "adresse de retour") {
		t.Fatalf("a redirect line for an unknown client: %s", body)
	}

	// The right app, its redirect registered.
	storeGoogleClient(oauthApp{ID: testClientID, Secret: testClientSecret})
	g.redirects = []string{callback}
	body = driveTestBody(t, tw, csrf)
	wantLines(t, body, "Google reconnaît ton app ("+testClientID+")", "L'adresse de retour "+callback+" est bien enregistrée")
	if strings.Contains(body, "test-err") || g.signins == 0 || g.followings != 0 {
		t.Fatalf("signins %d, followed %d: %s", g.signins, g.followings, body)
	}

	// Connected: the account.
	tok, _ := json.Marshal(map[string]string{"client_id": testClientID, "client_secret": testClientSecret, "refresh_token": checkRefresh})
	err := c.putDrive("fr", func(fc *filesConfig) {
		fc.Accounts = []filesAccount{{Name: driveAccount, Provider: "google", Scopes: []string{scopeDrive}}}
		fc.Accounts[0].Auth.TokenEnv = driveTokenEnv
	}, credentials{"env:" + driveTokenEnv: string(tok)})
	if err != nil {
		t.Fatal(err)
	}
	body = driveTestBody(t, tw, csrf)
	wantLines(t, body, "Connecté en tant que sam@example.com (Sam Doe). 3,2 Go utilisés sur 15 Go.")
	if strings.Contains(body, "test-err") || strings.Contains(body, "/settings/drive/connect") {
		t.Fatalf("connected: %s", body)
	}

	// The Drive API off in the app's project.
	g.apiOff = true
	wantLines(t, driveTestBody(t, tw, csrf), "L'API Google Drive n'est pas activée")
	g.apiOff = false

	// The authorization expired (7 days in Testing): Reconnect.
	g.refresh = "another"
	body = driveTestBody(t, tw, csrf)
	wantLines(t, body, "Google a retiré l'autorisation", "7 jours", `hx-post="/settings/drive/connect"`, `name="write" value="1"`, "Reconnecter Google Drive")

	// In English.
	tw.do("POST", "/lang", url.Values{"csrf": {csrf}, "lang": {"en"}}, nil)
	body = driveTestBody(t, tw, csrf)
	wantLines(t, body, "Google takes your workspace's address", "Google knows your app", "is registered in your Google app",
		"Google withdrew the Google Drive authorization", "Reconnect Google Drive")
	g.refresh = checkRefresh
	wantLines(t, driveTestBody(t, tw, csrf), "Connected as sam@example.com (Sam Doe). 3.2 GB used of 15 GB.")
}

// An address Google refuses is the first line, and its redirect is not asked.
func TestDriveTestOrigin(t *testing.T) {
	g := newGoogleCheck(t)
	_, c, _ := filesCore(t, nil)
	storeGoogleClient(oauthApp{ID: testClientID, Secret: testClientSecret})
	d := c.testDrive(t.Context(), "fr", "https://192.168.1.2:8443")
	if len(d.Lines) < 2 || d.Lines[0].State != "err" || !strings.Contains(d.Lines[0].Text, "Google n'accepte pas d'adresse IP") ||
		d.Lines[1].State != "ok" {
		t.Fatalf("lines: %+v", d.Lines)
	}
	for _, l := range d.Lines {
		if strings.Contains(l.Text, "/oauth/callback") {
			t.Fatalf("a redirect line: %+v", d.Lines)
		}
	}
	if g.signins != 0 {
		t.Fatal("the authorization was asked")
	}
}

// Google's own authError (an invalid_client, as accounts.google.com sent it).
func TestAuthErrorCode(t *testing.T) {
	for in, want := range map[string]string{
		"Cg5pbnZhbGlkX2NsaWVudBIfVGhlIE9BdXRoIGNsaWVudCB3YXMgbm90IGZvdW5kLiCRAw": "invalid_client",
		authError("redirect_uri_mismatch"):                                       "redirect_uri_mismatch",
		"":                                                                       "",
		"not base64 !":                                                           "",
		base64.RawURLEncoding.EncodeToString([]byte{0x12, 2, 'h', 'i'}): "",
	} {
		if got := authErrorCode(in); got != want {
			t.Errorf("authErrorCode(%q) = %q, want %q", in, got, want)
		}
	}
}
