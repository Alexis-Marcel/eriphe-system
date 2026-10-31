package main

import (
	"context"
	"fmt"
	"html"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const testToken = "tok-very-secret-1234"

// mcpTestServer is an MCP server over streamable HTTP at /api/mcp that
// wants testToken, with a few tools.
func mcpTestServer(t *testing.T, tls bool) *httptest.Server {
	t.Helper()
	s := mcp.NewServer(&mcp.Implementation{Name: "home-assistant", Version: "2026.10"}, nil)
	for i := range 8 {
		mcp.AddTool(s, &mcp.Tool{Name: fmt.Sprintf("tool_%d", i)}, func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, map[string]any, error) {
			return nil, map[string]any{}, nil
		})
	}
	h := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return s }, &mcp.StreamableHTTPOptions{DisableLocalhostProtection: true})
	mux := http.NewServeMux()
	mux.HandleFunc("/api/mcp", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+testToken {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		h.ServeHTTP(w, r)
	})
	mux.HandleFunc("/page", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte("<html>hello</html>"))
	})
	var srv *httptest.Server
	if tls {
		srv = httptest.NewTLSServer(mux)
	} else {
		srv = httptest.NewServer(mux)
	}
	t.Cleanup(srv.Close)
	return srv
}

func remoteForm(u, auth, token string) url.Values {
	f := url.Values{"preset": {"remote"}, "name": {"maison"}, "use": {"the house"}, "kind": {"remote"}, "url": {u}, "auth": {auth}}
	if token != "" {
		f.Set("token", token)
	}
	return f
}

// testTool posts the form to the test endpoint and returns the result box's text.
func testTool(t *testing.T, tw *testWeb, csrf, path string, form url.Values) string {
	t.Helper()
	code, body, h := tw.post(path, form, csrf)
	if code != 200 || h.Get("HX-Redirect") != "" {
		t.Fatalf("test %s: %d %v %s", path, code, h, body)
	}
	if strings.Contains(body, testToken) {
		t.Fatalf("the token is shown back: %s", body)
	}
	return html.UnescapeString(body)
}

func TestToolTestRemote(t *testing.T) {
	tw, c, csrf := catalogWeb(t, nil)
	srv := mcpTestServer(t, false)
	before := len(c.conf().Servers)

	body := testTool(t, tw, csrf, "/settings/tool-test", remoteForm(srv.URL+"/api/mcp", "bearer", testToken))
	if !strings.Contains(body, "Connexion réussie : home-assistant 2026.10, 8 outils") || !strings.Contains(body, "<code>tool_0</code>") ||
		!strings.Contains(body, "et 2 autres") || !strings.Contains(body, "test-ok") {
		t.Fatalf("success: %s", body)
	}
	// Nothing saved: no server, no secret.
	if len(c.conf().Servers) != before {
		t.Fatal("the test saved a server")
	}
	if list, _ := currentVault().list(); len(list) != 0 {
		t.Fatalf("the test stored secrets: %v", list)
	}

	cases := []struct {
		name string
		form url.Values
		want string
	}{
		{"wrong token", remoteForm(srv.URL+"/api/mcp", "bearer", "nope"), "Jeton refusé"},
		{"no auth", remoteForm(srv.URL+"/api/mcp", "none", ""), "demande une authentification"},
		{"no token", remoteForm(srv.URL+"/api/mcp", "bearer", ""), "Colle le jeton"},
		{"404", remoteForm(srv.URL+"/mcp", "bearer", testToken), "Adresse MCP introuvable (vérifie le chemin, ex. /api/mcp)"},
		{"not mcp", remoteForm(srv.URL+"/page", "none", ""), "ce n'est pas un serveur MCP"},
		{"scheme", remoteForm("file:///etc/passwd", "none", ""), "http:// ou https://"},
		{"empty", remoteForm("", "none", ""), "Indique l'adresse"},
	}
	for _, tc := range cases {
		if body := testTool(t, tw, csrf, "/settings/tool-test", tc.form); !strings.Contains(body, tc.want) || !strings.Contains(body, "test-err") {
			t.Errorf("%s: want %q in %s", tc.name, tc.want, body)
		}
	}
}

func TestToolTestStoredToken(t *testing.T) {
	tw, c, csrf := catalogWeb(t, nil)
	srv := mcpTestServer(t, false)
	form := remoteForm(srv.URL+"/api/mcp", "bearer", testToken)
	if code, _, h := tw.post("/settings/tools", form, csrf); code != 200 || h.Get("HX-Redirect") == "" {
		t.Fatalf("save: %d", code)
	}
	// The edit form leaves the token as set: the stored one is used.
	form.Del("token")
	body := testTool(t, tw, csrf, "/settings/tool-test/maison", form)
	if !strings.Contains(body, "Connexion réussie") {
		t.Fatalf("stored token: %s", body)
	}
	// A token typed in the edit form is tried, and not stored.
	form.Set("token", "typed-but-wrong")
	if body := testTool(t, tw, csrf, "/settings/tool-test/maison", form); !strings.Contains(body, "Jeton refusé") || strings.Contains(body, "typed-but-wrong") {
		t.Fatalf("typed token: %s", body)
	}
	if got, _ := readSecret(c.conf().Servers["maison"].Auth.BearerFile); got != testToken {
		t.Fatal("the test replaced the stored token")
	}
	// The edit form has the button, to its own endpoint.
	if _, body, _ := tw.do("GET", "/settings/tools/maison/edit", nil, nil); !strings.Contains(body, `hx-post="/settings/tool-test/maison"`) ||
		!strings.Contains(body, "Tester la connexion") || strings.Contains(body, testToken) {
		t.Fatalf("edit form: %s", body)
	}
	if _, body, _ := tw.do("GET", "/settings/tools/new?preset=remote", nil, nil); !strings.Contains(body, `hx-post="/settings/tool-test"`) {
		t.Fatal("add form has no test button")
	}
}

func TestToolTestNetworkErrors(t *testing.T) {
	tw, _, csrf := catalogWeb(t, nil)
	// Unreachable: a port nobody listens on.
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := l.Addr().String()
	l.Close()
	if body := testTool(t, tw, csrf, "/settings/tool-test", remoteForm("http://"+addr+"/mcp", "none", "")); !strings.Contains(body, "Adresse injoignable") {
		t.Errorf("unreachable: %s", body)
	}
	// TLS: a self-signed certificate.
	tlsSrv := mcpTestServer(t, true)
	if body := testTool(t, tw, csrf, "/settings/tool-test", remoteForm(tlsSrv.URL+"/api/mcp", "bearer", testToken)); !strings.Contains(body, "certificat TLS") {
		t.Errorf("tls: %s", body)
	}
	// HTTPS to a plain HTTP server.
	plain := mcpTestServer(t, false)
	if body := testTool(t, tw, csrf, "/settings/tool-test", remoteForm(strings.Replace(plain.URL, "http:", "https:", 1)+"/api/mcp", "none", "")); !strings.Contains(body, "ne parle pas HTTPS") {
		t.Errorf("https to http: %s", body)
	}
	// Timeout: a server that never answers.
	old := testConnTimeout
	testConnTimeout = 300 * time.Millisecond
	t.Cleanup(func() { testConnTimeout = old })
	stuck := make(chan struct{})
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-stuck:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(func() { close(stuck); slow.Close() })
	if body := testTool(t, tw, csrf, "/settings/tool-test", remoteForm(slow.URL+"/mcp", "none", "")); !strings.Contains(body, "Délai dépassé") {
		t.Errorf("timeout: %s", body)
	}
}

func TestNetErrorKey(t *testing.T) {
	dns := &url.Error{Op: "Post", URL: "http://x.invalid", Err: &net.OpError{Op: "dial", Err: &net.DNSError{Err: "no such host", Name: "x.invalid", IsNotFound: true}}}
	if k := netErrorKey(dns); k != "test.dns" {
		t.Fatalf("dns: %s", k)
	}
	if k := netErrorKey(context.DeadlineExceeded); k != "test.timeout" {
		t.Fatalf("deadline: %s", k)
	}
}

func TestToolTestOAuth(t *testing.T) {
	tw, _, csrf := catalogWeb(t, nil)
	as := newFakeAS(t)
	body := testTool(t, tw, csrf, "/settings/tool-test", remoteForm(as.srv.URL+"/mcp", "oauth", ""))
	if !strings.Contains(body, "annonce OAuth") || !strings.Contains(body, "il faudra se connecter après l'ajout") || !strings.Contains(body, "test-ok") {
		t.Fatalf("oauth: %s", body)
	}
	if as.registered() != nil {
		t.Fatal("the test registered a client")
	}
	// A server without OAuth metadata.
	srv := mcpTestServer(t, false)
	if body := testTool(t, tw, csrf, "/settings/tool-test", remoteForm(srv.URL+"/api/mcp", "oauth", "")); !strings.Contains(body, "n'annonce pas OAuth") {
		t.Fatalf("no metadata: %s", body)
	}
	if body := testTool(t, tw, csrf, "/settings/tool-test", remoteForm(srv.URL+"/nope", "oauth", "")); !strings.Contains(body, "introuvable") {
		t.Fatalf("oauth 404: %s", body)
	}
}

func TestToolTestLocal(t *testing.T) {
	tw, c, csrf := catalogWeb(t, nil)
	form := url.Values{"preset": {"local"}, "name": {"think"}, "kind": {"local"}, "image": {"mcp/sequentialthinking"}, "transport": {"stdio"}, "egress": {"1"}}
	if body := testTool(t, tw, csrf, "/settings/tool-test", form); !strings.Contains(body, "démarrera sur un de tes appareils") || !strings.Contains(body, "test-info") {
		t.Fatalf("local: %s", body)
	}
	form.Set("image", "Not/An_Image::")
	if body := testTool(t, tw, csrf, "/settings/tool-test", form); !strings.Contains(body, "Nom d'image invalide") {
		t.Fatalf("bad image: %s", body)
	}
	if _, ok := c.conf().Servers["think"]; ok {
		t.Fatal("the test saved a local server")
	}
}

func TestToolTestGuards(t *testing.T) {
	tw, _, csrf := catalogWeb(t, nil)
	form := remoteForm("http://127.0.0.1:1/mcp", "none", "")
	if code, _, _ := tw.post("/settings/tool-test", form, "wrong"); code != http.StatusForbidden {
		t.Fatalf("wrong CSRF: %d", code)
	}
	if code, _, _ := tw.do("POST", "/settings/tool-test", form, nil); code != http.StatusForbidden {
		t.Fatalf("no CSRF: %d", code)
	}
	tw.w.testLimit.max = 2
	for range 2 {
		testTool(t, tw, csrf, "/settings/tool-test", form)
	}
	if body := testTool(t, tw, csrf, "/settings/tool-test", form); !strings.Contains(body, "Trop d'essais") {
		t.Fatalf("rate limit: %s", body)
	}
	// Logged out: no test.
	anon := newTestWebFor(t, tw.c)
	if code, _, _ := anon.do("POST", "/settings/tool-test", form, map[string]string{"X-CSRF-Token": csrf}); code == 200 {
		t.Fatal("tested without a session")
	}
}

func TestTestLimiter(t *testing.T) {
	l := newTestLimiter()
	l.max = 2
	now := time.Now()
	if !l.allow("a", now) || !l.allow("a", now) || l.allow("a", now) {
		t.Fatal("limit")
	}
	if !l.allow("b", now) {
		t.Fatal("per session")
	}
	if !l.allow("a", now.Add(time.Minute)) {
		t.Fatal("window")
	}
}

func TestToolFormHints(t *testing.T) {
	tw, _, csrf := catalogWeb(t, nil)
	_, body, _ := tw.do("GET", "/settings/tools/new?preset=home-assistant", nil, nil)
	body = html.UnescapeString(body)
	for _, want := range []string{`placeholder="http://192.168.0.10:8123/api/mcp"`, `value="bearer" checked`, "Jetons d'accès longue durée", ":8123", "port 80",
		"Tu seras envoyé chez le service pour autoriser Eriphe, puis ramené ici."} {
		if !strings.Contains(body, want) {
			t.Errorf("home assistant form: no %q", want)
		}
	}
	if body := testTool(t, tw, csrf, "/settings/tool-test", remoteForm("http://<ip>/api/mcp", "bearer", "x")); !strings.Contains(body, "Remplace <ip>") {
		t.Fatalf("placeholder: %s", body)
	}
}

// calendarAnswer: each answer of a CalDAV server to the test, as a line.
func TestCalendarAnswer(t *testing.T) {
	for status, want := range map[int]string{
		207: "ok Connected: the calendar accepts sam and this password.",
		200: "ok Connected: the calendar accepts sam and this password.",
		401: "err Login or password refused by the calendar.",
		403: "err Login or password refused by the calendar.",
		404: "err No CalDAV calendar at this address: check it.",
		405: "err No CalDAV calendar at this address: check it.",
		500: "err The service answered with error 500.",
	} {
		if v := calendarAnswer("en", "sam", status); v.State+" "+v.Text != want {
			t.Errorf("%d: %+v", status, v)
		}
	}
}

// davClient follows the same server's https redirects only, three at
// most, with the account's credentials.
func TestDAVClientRedirects(t *testing.T) {
	check := davClient("sam", "pw").CheckRedirect
	first := httptest.NewRequest("PROPFIND", "https://dav.example.org/", nil)
	for _, c := range []struct {
		to   string
		hops int
		ok   bool
	}{
		{"https://dav.example.org/.well-known/caldav", 1, true},
		{"https://dav.example.org/dav/", 4, false},
		{"http://dav.example.org/dav/", 1, false},
		{"https://other.example.org/dav/", 1, false},
	} {
		req := httptest.NewRequest("PROPFIND", c.to, nil)
		via := make([]*http.Request, c.hops)
		for i := range via {
			via[i] = first
		}
		err := check(req, via)
		if (err == nil) != c.ok {
			t.Errorf("%s after %d: %v", c.to, c.hops, err)
		}
		if user, pw, _ := req.BasicAuth(); c.ok && (user != "sam" || pw != "pw") {
			t.Errorf("%s: credentials %q %q", c.to, user, pw)
		}
	}
}
