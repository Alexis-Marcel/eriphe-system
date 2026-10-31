package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
)

// "Test the connection" on the tool form: the values of the form, not yet
// saved, are tried once. A remote server gets a short-lived MCP session
// (initialize and tools/list) with the URL and the credential typed in the
// form, or the stored one when an edit leaves it as set; an OAuth server
// is only probed for its protected resource metadata, as signing in comes
// after the tool is added. Nothing is saved, and the credential is never
// logged nor shown back.

// testConnTimeout bounds one test.
var testConnTimeout = 10 * time.Second

// testView is the result box under the form.
type testView struct {
	State  string // ok, err or info
	Text   string
	Tools  []string // the first tools
	More   int      // how many others
	Models []string // a self-hosted model server's models, all of them
	Detail string   // the technical cause, for an unclassified failure
	Cause  string   // a failure's cause (problems.go), when it is known
}

// testLimiter allows a few tests per session and minute.
type testLimiter struct {
	mu   sync.Mutex
	hits map[string][]time.Time
	max  int
	per  time.Duration
}

func newTestLimiter() *testLimiter {
	return &testLimiter{hits: map[string][]time.Time{}, max: 10, per: time.Minute}
}

func (l *testLimiter) allow(id string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	recent := l.hits[id][:0]
	for _, t := range l.hits[id] {
		if now.Sub(t) < l.per {
			recent = append(recent, t)
		}
	}
	for k, ts := range l.hits { // forget idle sessions
		if k != id && (len(ts) == 0 || now.Sub(ts[len(ts)-1]) >= l.per) {
			delete(l.hits, k)
		}
	}
	if len(recent) >= l.max {
		l.hits[id] = recent
		return false
	}
	l.hits[id] = append(recent, now)
	return true
}

// handleTestTool tries the posted tool form: POST /settings/tool-test for
// a new tool, POST /settings/tool-test/{name} for an edit.
func (w *web) handleTestTool(rw http.ResponseWriter, r *http.Request, s *session) {
	lang := w.lang(r)
	show := func(v testView) { w.render(rw, r, s, "settings", "tool-test", page{Data: v}) }
	if !w.testLimit.allow(s.ID, w.now()) {
		show(testView{State: "err", Text: tr(lang, "test.ratelimit")})
		return
	}
	f := readToolForm(r)
	var old *Server
	if n := r.PathValue("name"); n != "" {
		if srv, ok := w.workspaceServer(n); ok {
			old = &srv
		}
	}
	var v testView
	kind := "remote"
	switch {
	case f.Preset.Form == "mail":
		kind, v = "mail", w.testMail(r, f, old)
	case f.Preset.Form == "calendar":
		kind, v = "calendar", w.testCalendar(r, f, old)
	case f.Kind == "local":
		kind, v = "local", w.testLocal(r, f, old)
	default:
		v = w.testRemote(r, f, old)
	}
	log.Printf("web: tool test (%s): %s", kind, v.State) // never the URL nor the credential
	show(v)
}

// imageRef is the shape of a container image reference:
// [host[:port]/]path[:tag][@digest].
var imageRef = regexp.MustCompile(`^([a-zA-Z0-9][a-zA-Z0-9.-]*(:[0-9]+)?/)?[a-z0-9]+([._-]+[a-z0-9]+)*(/[a-z0-9]+([._-]+[a-z0-9]+)*)*(:[A-Za-z0-9_][A-Za-z0-9_.-]{0,127})?(@sha256:[a-f0-9]{64})?$`)

// testLocal checks what can be checked of a local server before it runs:
// the run spec as the catalog would take it, and the image's reference.
func (w *web) testLocal(r *http.Request, f toolForm, old *Server) testView {
	lang := w.lang(r)
	if f.Use == "" {
		f.Use = "-" // not what is tested here
	}
	srv, _, err := f.server(r, old)
	if err == nil {
		var spec RunSpec
		if spec, err = decodeRun(srv.Run); err == nil {
			if _, err = spec.normalize(); err == nil && !imageRef.MatchString(spec.Image) {
				err = fmt.Errorf("form.badimage")
			}
		}
	}
	if err != nil {
		return testView{State: "err", Text: w.formError(r, err)}
	}
	return testView{State: "info", Text: tr(lang, "test.local")}
}

// testRemote opens a session with the form's URL and credential.
func (w *web) testRemote(r *http.Request, f toolForm, old *Server) testView {
	lang := w.lang(r)
	fail := func(key string, args ...any) testView {
		text := tr(lang, key)
		if len(args) > 0 {
			text = fmt.Sprintf(text, args...)
		}
		return testView{State: "err", Text: text}
	}
	u, err := url.Parse(f.URL)
	switch {
	case f.URL == "":
		return fail("test.nourl")
	case strings.ContainsAny(f.URL, "<>"):
		return fail("test.placeholder")
	case err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "":
		return fail("test.badurl")
	}
	ctx, cancel := context.WithTimeout(r.Context(), testConnTimeout)
	defer cancel()
	token := ""
	switch f.Auth {
	case "oauth":
		return w.testOAuth(ctx, lang, f.URL)
	case "bearer":
		token = strings.TrimSpace(r.PostFormValue("token"))
		if token == "" && old != nil && old.Auth != nil && old.Auth.BearerFile != "" {
			if token, err = readSecret(old.Auth.BearerFile); err != nil {
				return fail("test.nostored")
			}
		}
		if token == "" {
			return fail("form.notoken")
		}
	}
	rec := &probeTransport{base: testTransport(), token: token}
	hc := &http.Client{Transport: rec}
	t := &mcp.StreamableClientTransport{Endpoint: f.URL, HTTPClient: hc, MaxRetries: -1}
	client := mcp.NewClient(&mcp.Implementation{Name: "eriphe-core", Version: version}, nil)
	sess, err := client.Connect(ctx, t, nil)
	var tools []*mcp.Tool
	if err == nil {
		tools, err = listTools(ctx, sess)
		info := sess.InitializeResult()
		sess.Close()
		if err == nil {
			v := testView{State: "ok"}
			name := ""
			if info != nil && info.ServerInfo != nil {
				name = strings.TrimSpace(info.ServerInfo.Name + " " + info.ServerInfo.Version)
			}
			if name == "" {
				name = u.Host
			}
			v.Text = fmt.Sprintf(tr(lang, "test.ok"), name, len(tools))
			for i, tool := range tools {
				if i == 6 {
					v.More = len(tools) - i
					break
				}
				v.Tools = append(v.Tools, tool.Name)
			}
			return v
		}
	}
	v := classifyTest(lang, ctx, rec, err, f.Auth)
	if v.Detail != "" && token != "" {
		v.Detail = strings.ReplaceAll(v.Detail, token, "••••")
	}
	return v
}

// testOAuth checks that the server answers and advertises OAuth: its
// protected resource metadata names an authorization server.
func (w *web) testOAuth(ctx context.Context, lang, endpoint string) testView {
	rec := &probeTransport{base: testTransport()}
	hc := &http.Client{Transport: rec, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"eriphe-core","version":"` + version + `"}}}`
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	resp, err := hc.Do(req)
	if err != nil {
		return classifyTest(lang, ctx, rec, err, "oauth")
	}
	io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return testView{State: "err", Text: tr(lang, "test.notfound")}
	}
	var candidates []string
	challenges, _ := oauthex.ParseWWWAuthenticate(resp.Header.Values("WWW-Authenticate"))
	for _, c := range challenges {
		if m := c.Params["resource_metadata"]; c.Scheme == "bearer" && m != "" {
			candidates = append(candidates, m)
		}
	}
	u, _ := url.Parse(endpoint)
	origin := u.Scheme + "://" + u.Host
	if p := strings.TrimSuffix(u.Path, "/"); p != "" {
		candidates = append(candidates, origin+"/.well-known/oauth-protected-resource"+p)
	}
	candidates = append(candidates, origin+"/.well-known/oauth-protected-resource")
	for _, m := range candidates {
		if as := authServers(ctx, hc, m); len(as) > 0 {
			host := as[0]
			if p, err := url.Parse(as[0]); err == nil && p.Host != "" {
				host = p.Host
			}
			return testView{State: "ok", Text: fmt.Sprintf(tr(lang, "test.oauth"), host)}
		}
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return testView{State: "err", Text: tr(lang, "test.oauthnometa")}
	}
	if resp.StatusCode < 300 {
		return testView{State: "info", Text: tr(lang, "test.oauthopen")}
	}
	return testView{State: "err", Text: fmt.Sprintf(tr(lang, "test.status"), resp.StatusCode)}
}

// authServers reads protected resource metadata and returns its
// authorization servers, the http(s) ones only.
func authServers(ctx context.Context, hc *http.Client, metadataURL string) []string {
	if p, err := url.Parse(metadataURL); err != nil || (p.Scheme != "http" && p.Scheme != "https") {
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, metadataURL, nil)
	if err != nil {
		return nil
	}
	req.Header.Set("Accept", "application/json")
	resp, err := hc.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil
	}
	var meta struct {
		AuthorizationServers []string `json:"authorization_servers"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&meta) != nil {
		return nil
	}
	var out []string
	for _, a := range meta.AuthorizationServers {
		if p, err := url.Parse(a); err == nil && (p.Scheme == "https" || p.Scheme == "http") {
			out = append(out, a)
		}
	}
	return out
}

// testTransport is a fresh transport: nothing pooled outlives the test.
func testTransport() *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.DisableKeepAlives = true
	t.Proxy = nil
	t.TLSHandshakeTimeout = testConnTimeout
	t.ResponseHeaderTimeout = testConnTimeout
	if testRoots != nil {
		t.TLSClientConfig = &tls.Config{RootCAs: testRoots}
	}
	return t
}

// probeTransport adds the bearer token and remembers what the server
// answered, to tell the user why a test failed.
type probeTransport struct {
	base  http.RoundTripper
	token string

	mu     sync.Mutex
	status int    // of the first failed POST, else of the first POST
	ctype  string // its content type
	err    error  // the first transport error
}

func (p *probeTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if p.token != "" {
		r = r.Clone(r.Context())
		r.Header.Set("Authorization", "Bearer "+p.token)
	}
	resp, err := p.base.RoundTrip(r)
	p.mu.Lock()
	defer p.mu.Unlock()
	if err != nil {
		if p.err == nil {
			p.err = err
		}
		return nil, err
	}
	if r.Method == http.MethodPost && (p.status == 0 || (p.status < 400 && resp.StatusCode >= 400)) {
		p.status, p.ctype = resp.StatusCode, resp.Header.Get("Content-Type")
	}
	return resp, err
}

func (p *probeTransport) seen() (int, string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.status, p.ctype, p.err
}

// classifyTest turns a failed test into what the user can do about it.
func classifyTest(lang string, ctx context.Context, rec *probeTransport, err error, auth string) testView {
	status, ctype, terr := rec.seen()
	v := func(key string, args ...any) testView {
		text := tr(lang, key)
		if len(args) > 0 {
			text = fmt.Sprintf(text, args...)
		}
		return testView{State: "err", Text: text}
	}
	if terr != nil {
		if key := netErrorKey(terr); key != "" {
			return v(key)
		}
	}
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		if auth == "none" {
			return v("test.needsauth")
		}
		return v("test.denied")
	case status == http.StatusNotFound:
		return v("test.notfound")
	case status >= 400:
		return v("test.status", status)
	}
	if ctx.Err() != nil || errors.Is(err, context.DeadlineExceeded) {
		return v("test.timeout")
	}
	if terr != nil {
		out := v("test.unreachable")
		out.Detail = truncate(terr.Error(), 200)
		return out
	}
	if status != 0 && !strings.Contains(ctype, "json") && !strings.Contains(ctype, "event-stream") {
		return v("test.notmcp")
	}
	out := v("test.notmcp")
	if err != nil {
		out.Detail = truncate(err.Error(), 200)
	}
	return out
}

// netErrorKey names a transport failure: DNS, certificate, refused or
// unreachable, or a timeout.
func netErrorKey(err error) string {
	var dns *net.DNSError
	var unknown x509.UnknownAuthorityError
	var host x509.HostnameError
	var invalid x509.CertificateInvalidError
	var verify *tls.CertificateVerificationError
	var record tls.RecordHeaderError
	var nerr net.Error
	switch {
	case errors.As(err, &dns):
		return "test.dns"
	case errors.As(err, &unknown), errors.As(err, &host), errors.As(err, &invalid), errors.As(err, &verify):
		return "test.tls"
	case errors.As(err, &record), strings.Contains(err.Error(), "HTTP response to HTTPS client"):
		return "test.tlsmismatch"
	case errors.Is(err, syscall.ECONNREFUSED), errors.Is(err, syscall.EHOSTUNREACH), errors.Is(err, syscall.ENETUNREACH):
		return "test.unreachable"
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &nerr) && nerr.Timeout():
		return "test.timeout"
	}
	var op *net.OpError
	if errors.As(err, &op) && op.Op == "dial" {
		return "test.unreachable"
	}
	return ""
}
