package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The upstream pool keeps one MCP session per catalog server, like a
// controller: it connects, lists the server's tools and keeps them cached
// (refreshed on tools/list_changed), and reconnects with backoff when the
// session drops. Tasks' calls go through these sessions (proxy.go).

const (
	statusConnecting = "connecting"
	statusReady      = "ready"
	statusNeedsAuth  = "needs_auth" // OAuth: `eriphe connect <name>`
	statusError      = "error"
)

var errLocalUnsupported = errors.New("local servers are not supported yet")

type pool struct {
	store *Store
	// dial opens the transport to a server: remote ones over streamable
	// HTTP, local ones through dialLocal. Tests replace it.
	dial func(ctx context.Context, u *upstream) (mcp.Transport, error)
	// dialLocal reaches a local server (run), which a node runs as a
	// service. Until services exist, local servers are not supported.
	dialLocal func(ctx context.Context, name string, run json.RawMessage) (mcp.Transport, error)
	// http is the client for remote servers and their authorization servers.
	http *http.Client

	mu      sync.Mutex
	ctx     context.Context // set by run; servers added later start in it
	ups     map[string]*upstream
	pending map[string]*pendingServer // OAuth servers being authorized before they are saved (pending.go)
}

// upstream is one catalog server and its session.
type upstream struct {
	p    *pool
	name string
	srv  Server

	mu        sync.Mutex
	status    string
	err       string
	session   *mcp.ClientSession
	tools     []*mcp.Tool
	needsAuth string    // why the server wants an authorization, set by the OAuth gate
	flow      *authFlow // a `eriphe connect` in progress
	// The OAuth client typed in the form of a pending server, in memory
	// until the server is saved.
	clientID, clientSecret string
	wake                   chan struct{}
	cancel                 context.CancelFunc // stops its controller, once running
}

func newPool(store *Store, servers map[string]Server) *pool {
	p := &pool{store: store, ups: map[string]*upstream{}, pending: map[string]*pendingServer{}, http: &http.Client{Timeout: 0},
		dialLocal: func(context.Context, string, json.RawMessage) (mcp.Transport, error) { return nil, errLocalUnsupported }}
	p.dial = p.dialServer
	for name, s := range servers {
		p.ups[name] = &upstream{p: p, name: name, srv: s, status: statusConnecting, wake: make(chan struct{}, 1)}
	}
	return p
}

func (p *pool) get(name string) *upstream {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.ups[name]
}

// run starts one controller per server.
func (p *pool) run(ctx context.Context) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.ctx = ctx
	for _, u := range p.ups {
		p.start(u)
	}
}

// start runs a server's controller in its own context. p.mu is held.
func (p *pool) start(u *upstream) {
	if p.ctx == nil {
		return // not running yet: run starts it
	}
	ctx, cancel := context.WithCancel(p.ctx)
	u.cancel = cancel
	go u.run(ctx)
}

// stop ends a server's controller and its session; a connect in progress
// fails. p.mu is held.
func (u *upstream) stop() {
	if u.cancel != nil {
		u.cancel()
	}
	u.mu.Lock()
	s, flow := u.session, u.flow
	u.session, u.flow, u.status, u.err = nil, nil, statusError, "removed from the catalog"
	u.mu.Unlock()
	if s != nil {
		s.Close()
	}
	if flow != nil {
		select {
		case flow.results <- flowResult{err: errors.New("the server was changed or removed")}:
		default:
		}
	}
}

// sync makes the pool follow the catalog: new servers are connected,
// removed ones closed, changed ones started over.
func (p *pool) sync(servers map[string]Server) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for name, u := range p.ups {
		s, ok := servers[name]
		if ok && sameServer(s, u.srv) {
			continue
		}
		u.stop()
		delete(p.ups, name)
		log.Printf("server %s: closed (%s)", name, map[bool]string{true: "changed", false: "removed"}[ok])
	}
	for name, s := range servers {
		if _, ok := p.ups[name]; ok {
			continue
		}
		u := &upstream{p: p, name: name, srv: s, status: statusConnecting, wake: make(chan struct{}, 1)}
		p.ups[name] = u
		p.start(u)
	}
}

func sameServer(a, b Server) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}

// dialServer is the default dial.
func (p *pool) dialServer(ctx context.Context, u *upstream) (mcp.Transport, error) {
	if u.srv.local() {
		return p.dialLocal(ctx, u.name, u.srv.Run)
	}
	t := &mcp.StreamableClientTransport{Endpoint: u.srv.URL, HTTPClient: p.http, MaxRetries: 3}
	switch u.srv.authKind() {
	case "bearer":
		tok, err := readSecret(u.srv.Auth.BearerFile)
		if err != nil {
			return nil, err
		}
		t.HTTPClient = &http.Client{Transport: bearerTransport{token: tok, base: http.DefaultTransport}}
	case "oauth":
		h, err := p.oauthHandler(u)
		if err != nil {
			return nil, err
		}
		t.OAuthHandler = h
	}
	return t, nil
}

// bearerTransport adds a static bearer token to every request.
type bearerTransport struct {
	token string
	base  http.RoundTripper
}

func (b bearerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return b.base.RoundTrip(r)
}

// run keeps the server connected: after a failure it waits, doubling from
// 1 s to 5 min, or until woken (a connect). A server that needs an
// authorization only waits to be woken.
func (u *upstream) run(ctx context.Context) {
	backoff := time.Second
	for ctx.Err() == nil {
		start := time.Now()
		err := u.connect(ctx)
		if err == nil || time.Since(start) > 5*time.Minute {
			backoff = time.Second
		}
		u.mu.Lock()
		status := u.status
		u.mu.Unlock()
		var wait <-chan time.Time
		// Waiting for the user, or for local servers to exist: retrying is no use.
		if status != statusNeedsAuth && !errors.Is(err, errLocalUnsupported) {
			wait = time.After(backoff)
			backoff = min(backoff*2, 5*time.Minute)
		}
		select {
		case <-ctx.Done():
		case <-u.wake:
		case <-wait:
		}
	}
}

// reconnect drops the current session, if any, and wakes the controller.
func (u *upstream) reconnect() {
	u.mu.Lock()
	s := u.session
	u.mu.Unlock()
	if s != nil {
		s.Close()
	}
	select {
	case u.wake <- struct{}{}:
	default:
	}
}

// connect opens a session, caches the tools, and waits for it to end.
func (u *upstream) connect(ctx context.Context) error {
	u.mu.Lock()
	flow := u.flow
	u.needsAuth = ""
	if u.status != statusReady {
		u.status = statusConnecting
	}
	u.mu.Unlock()
	timeout := time.Minute
	if flow != nil {
		timeout = flowTimeout + time.Minute // the user authorizes in a browser meanwhile
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	sess, err := u.open(cctx)
	if flow != nil {
		u.mu.Lock()
		if u.flow == flow {
			u.flow = nil
		}
		u.mu.Unlock()
		flow.finish(err)
	}
	if err != nil {
		u.mu.Lock()
		u.status, u.err = statusError, err.Error()
		if u.needsAuth != "" {
			u.status, u.err = statusNeedsAuth, u.needsAuth
		}
		u.mu.Unlock()
		log.Printf("server %s: %s", u.name, u.err)
		return err
	}
	log.Printf("server %s: ready", u.name)
	stop := context.AfterFunc(ctx, func() { sess.Close() })
	defer stop()
	sess.Wait()
	u.mu.Lock()
	if u.session == sess {
		u.session = nil
		if u.status == statusReady {
			u.status, u.err = statusConnecting, "disconnected"
		}
	}
	u.mu.Unlock()
	return nil
}

// open connects and lists the tools; the server is ready once it has them.
func (u *upstream) open(ctx context.Context) (*mcp.ClientSession, error) {
	tr, err := u.p.dial(ctx, u)
	if err != nil {
		return nil, err
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "eriphe-core", Version: version}, &mcp.ClientOptions{
		KeepAlive: 30 * time.Second,
		ToolListChangedHandler: func(context.Context, *mcp.ToolListChangedRequest) {
			go u.refreshTools()
		},
	})
	sess, err := client.Connect(ctx, tr, nil)
	if err != nil {
		return nil, err
	}
	tools, err := listTools(ctx, sess)
	if err != nil {
		sess.Close()
		return nil, err
	}
	u.mu.Lock()
	u.session, u.tools, u.status, u.err = sess, tools, statusReady, ""
	u.mu.Unlock()
	return sess, nil
}

func listTools(ctx context.Context, sess *mcp.ClientSession) ([]*mcp.Tool, error) {
	var tools []*mcp.Tool
	for t, err := range sess.Tools(ctx, nil) {
		if err != nil {
			return nil, err
		}
		if setupTool(t) {
			continue // eriphe-core's own, never lent (cloud.go)
		}
		tools = append(tools, t)
	}
	sort.Slice(tools, func(i, j int) bool { return tools[i].Name < tools[j].Name })
	return tools, nil
}

func (u *upstream) refreshTools() {
	u.mu.Lock()
	sess := u.session
	u.mu.Unlock()
	if sess == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	tools, err := listTools(ctx, sess)
	if err != nil {
		log.Printf("server %s: tools: %v", u.name, err)
		return
	}
	u.mu.Lock()
	if u.session == sess {
		u.tools = tools
	}
	u.mu.Unlock()
}

// snapshot is the server's state at one moment.
func (u *upstream) snapshot() (status, errText string, tools []*mcp.Tool) {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.status, u.err, u.tools
}

func (u *upstream) tool(name string) *mcp.Tool {
	_, _, tools := u.snapshot()
	for _, t := range tools {
		if t.Name == name {
			return t
		}
	}
	return nil
}

// call forwards one tool call, within the server's timeout.
func (u *upstream) call(ctx context.Context, tool string, args json.RawMessage) (*mcp.CallToolResult, error) {
	u.mu.Lock()
	sess, status, errText := u.session, u.status, u.err
	u.mu.Unlock()
	if sess == nil {
		if status == statusNeedsAuth {
			return nil, fmt.Errorf("server %s needs an authorization: the user must run `eriphe connect %s`", u.name, u.name)
		}
		return nil, fmt.Errorf("server %s is not connected (%s %s)", u.name, status, errText)
	}
	timeout := time.Duration(u.srv.Timeout) * time.Second
	if timeout == 0 {
		timeout = 10 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	params := &mcp.CallToolParams{Name: tool}
	if len(args) > 0 {
		params.Arguments = args
	}
	return sess.CallTool(ctx, params)
}

// readOnly tells whether a catalog tool, by its Claude Code name, is
// read-only by its own annotations on a server trusted for them.
func (p *pool) readOnly(claudeName string) bool {
	if p == nil {
		return false
	}
	server, tool, ok := splitToolName(claudeName)
	if !ok {
		return false
	}
	u := p.get(server)
	if u == nil || !u.srv.TrustAnnotations {
		return false
	}
	t := u.toolByClaudeName(tool)
	return t != nil && t.Annotations != nil && t.Annotations.ReadOnlyHint
}

// toolByClaudeName finds a tool by the name Claude Code gives it.
func (u *upstream) toolByClaudeName(name string) *mcp.Tool {
	_, _, tools := u.snapshot()
	for _, t := range tools {
		if t.Name == name || claudeToolName(u.name, t.Name) == claudeToolName(u.name, name) {
			return t
		}
	}
	return nil
}
