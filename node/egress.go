package main

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/netip"
	"net/url"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"
)

// The egress relay. The task network is internal: a task container has no
// route out, and reaches only this node. Its one way to the outside is this
// HTTP forward proxy (CONNECT for HTTPS, absolute URLs for plain HTTP), which
// it is pointed at with HTTP_PROXY/HTTPS_PROXY and its own credentials. Like
// an egress gateway enforcing a NetworkPolicy, the relay applies the task's
// rule from eriphe-core (TaskSpec.Egress), resolves names itself and dials
// public addresses only: never the LAN (the router, the NAS, Home
// Assistant), the loopback, link-local addresses (cloud metadata), CGNAT or
// ULA ranges. It dials the very address it checked, so a name that resolves
// to a public address when checked and a private one later (DNS rebinding)
// gains nothing. A program that ignores the proxy variables fails closed.

// EgressRule is where a task may connect.
type EgressRule struct {
	Mode  string   `json:"mode"`            // "internet" (the default: public addresses only) or "none"
	Allow []string `json:"allow,omitempty"` // if set, only these hosts: "github.com", "*.npmjs.org" (subdomains)
	// Endpoints are the host:port of the task's self-hosted model server
	// ("192.0.2.10:11434"): with the mode internet, each is reachable even
	// on the LAN or another port, but only that very host and port, and
	// never at a loopback, link-local, multicast or unspecified address.
	Endpoints []string `json:"endpoints,omitempty"`
}

const (
	egressInternet = "internet"
	egressNone     = "none"
)

// egressPorts are the ports the relay connects to: HTTP and HTTPS (git over
// HTTPS included), not SMTP, SSH nor anything else.
var egressPorts = map[string]bool{"80": true, "443": true}

// blockedPrefixes are the non-public ranges netip has no predicate for.
var blockedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),       // "this network"
	netip.MustParsePrefix("100.64.0.0/10"),   // CGNAT (and Tailscale)
	netip.MustParsePrefix("192.0.0.0/24"),    // IETF protocol assignments
	netip.MustParsePrefix("192.0.2.0/24"),    // documentation
	netip.MustParsePrefix("198.18.0.0/15"),   // benchmarking
	netip.MustParsePrefix("198.51.100.0/24"), // documentation
	netip.MustParsePrefix("203.0.113.0/24"),  // documentation
	netip.MustParsePrefix("240.0.0.0/4"),     // reserved, broadcast
	netip.MustParsePrefix("64:ff9b::/96"),    // NAT64: may embed a private IPv4
	netip.MustParsePrefix("64:ff9b:1::/48"),  // local NAT64
	netip.MustParsePrefix("2001::/32"),       // Teredo
	netip.MustParsePrefix("2001:db8::/32"),   // documentation
	netip.MustParsePrefix("2002::/16"),       // 6to4: may embed a private IPv4
}

// publicAddr tells whether an address is on the public Internet: not
// loopback, private (RFC 1918, ULA fc00::/7), link-local (169.254.0.0/16,
// fe80::/10), multicast, unspecified, CGNAT nor any other special range.
func publicAddr(a netip.Addr) bool {
	a = a.Unmap()
	if !a.IsValid() || !a.IsGlobalUnicast() || a.IsPrivate() {
		return false
	}
	for _, p := range blockedPrefixes {
		if p.Contains(a) {
			return false
		}
	}
	return true
}

var errNotPublic = errors.New("not a public address")

// endpointAddr tells whether a declared model server may be dialed at an
// address: a unicast one, private included, but never the loopback (the
// node itself), link-local (169.254.169.254, the cloud's metadata),
// multicast, unspecified, broadcast nor "this network".
func endpointAddr(a netip.Addr) bool {
	a = a.Unmap()
	return a.IsValid() && a.IsGlobalUnicast() && !(a.Is4() && a.As4()[0] == 0)
}

// endpointControl is guardControl for a declared model server's address.
func endpointControl(_, address string, _ syscall.RawConn) error {
	ap, err := netip.ParseAddrPort(address)
	if err != nil || !endpointAddr(ap.Addr()) {
		return fmt.Errorf("%s: not a model server's address", address)
	}
	return nil
}

// guardControl refuses, at the socket, any connection to a non-public
// address: the last check, whatever resolved the name.
func guardControl(_, address string, _ syscall.RawConn) error {
	ap, err := netip.ParseAddrPort(address)
	if err != nil || !publicAddr(ap.Addr()) {
		return fmt.Errorf("%s: %w", address, errNotPublic)
	}
	return nil
}

// hostAllowed matches a host against a rule's allowlist: an exact name, or
// "*.domain" for any name under domain (not domain itself).
func hostAllowed(allow []string, host string) bool {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	for _, a := range allow {
		a = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(a)), ".")
		if suffix, ok := strings.CutPrefix(a, "*."); ok {
			if strings.HasSuffix(host, "."+suffix) {
				return true
			}
		} else if a != "" && host == a {
			return true
		}
	}
	return false
}

type egressTask struct {
	id   string
	rule EgressRule
	seen func(host string) bool // true the first time this task goes to host
}

type egressProxy struct {
	// authorize finds the running task a proxy credential belongs to.
	authorize func(id, secret string) (*egressTask, bool)
	// self are the host:port a task reaches this node's task listener at
	// (its callback URL): allowed, and dialed at listener.
	self map[string]bool
	// isSelf tells whether host:port is the node too: with the container
	// engine, the gateway of a task's network (apple.go).
	isSelf   func(hostport string) bool
	listener string
	lookup   func(ctx context.Context, host string) ([]netip.Addr, error)
	dialer   *net.Dialer // dials public addresses only (guardControl)
	// dialPublic dials a checked address: dialer.DialContext, but for tests.
	dialPublic func(ctx context.Context, network, addr string) (net.Conn, error)
	// dialEndpoint dials a checked address of a declared model server
	// (endpointControl), but for tests.
	dialEndpoint func(ctx context.Context, network, addr string) (net.Conn, error)
	logf         func(format string, args ...any)
}

func newEgressProxy(authorize func(id, secret string) (*egressTask, bool), self []string, listener string) *egressProxy {
	p := &egressProxy{
		authorize: authorize, self: map[string]bool{}, listener: listener,
		lookup: func(ctx context.Context, host string) ([]netip.Addr, error) {
			return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		},
		dialer: &net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second, Control: guardControl},
		logf:   log.Printf,
	}
	p.dialPublic = p.dialer.DialContext
	p.dialEndpoint = (&net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second, Control: endpointControl}).DialContext
	for _, s := range self {
		p.self[strings.ToLower(s)] = true
	}
	return p
}

// egressAuthorizer checks a credential against the node's tasks: the task's
// id as the user name, its secret as the password.
func (n *node) egressAuthorizer(id, secret string) (*egressTask, bool) {
	t := n.get(id)
	if t == nil || t.secret == "" || subtle.ConstantTimeCompare([]byte(secret), []byte(t.secret)) != 1 || finished(t.state()) {
		return nil, false
	}
	rule := EgressRule{Mode: egressInternet}
	if t.Spec.Egress != nil {
		rule = *t.Spec.Egress
	}
	return &egressTask{id: t.ID, rule: rule, seen: t.egressSeen}, true
}

// egressSeen records a host the task went to, and tells if it is new.
func (t *Task) egressSeen(host string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.egressHosts == nil {
		t.egressHosts = map[string]bool{}
	}
	if t.egressHosts[host] {
		return false
	}
	t.egressHosts[host] = true
	return true
}

type egressError struct {
	status int
	msg    string
}

func (e *egressError) Error() string { return e.msg }

func refused(format string, args ...any) error {
	return &egressError{http.StatusForbidden, fmt.Sprintf(format, args...)}
}

// dial connects to hostport for the task, or says why it may not.
func (p *egressProxy) dial(ctx context.Context, t *egressTask, hostport string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(hostport)
	if err != nil {
		return nil, &egressError{http.StatusBadRequest, "bad address " + hostport}
	}
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	if p.self[net.JoinHostPort(host, port)] || (p.isSelf != nil && p.isSelf(net.JoinHostPort(host, port))) {
		// The node's own task listener: tasks reach it anyway.
		var d net.Dialer
		return d.DialContext(ctx, "tcp", p.listener)
	}
	if t.isEndpoint(host, port) {
		return p.dialModelServer(ctx, t, host, port)
	}
	if err := p.check(t, host, port); err != nil {
		p.note(t, host, err)
		return nil, err
	}
	var addrs []netip.Addr
	if a, err := netip.ParseAddr(host); err == nil {
		addrs = []netip.Addr{a}
	} else if addrs, err = p.lookup(ctx, host); err != nil {
		return nil, &egressError{http.StatusBadGateway, fmt.Sprintf("cannot resolve %s: %v", host, err)}
	}
	var public []netip.Addr
	for _, a := range addrs {
		if publicAddr(a) {
			public = append(public, a.Unmap())
		}
	}
	if len(public) == 0 {
		err := refused("%s is not on the public Internet", host)
		p.note(t, host, err)
		return nil, err
	}
	p.note(t, host, nil)
	var last error
	for _, a := range public {
		c, err := p.dialPublic(ctx, "tcp", netip.AddrPortFrom(a, mustPort(port)).String())
		if err == nil {
			return c, nil
		}
		last = err
	}
	return nil, &egressError{http.StatusBadGateway, fmt.Sprintf("cannot reach %s: %v", hostport, last)}
}

// isEndpoint tells whether host:port is exactly a model server the task's
// rule declares, with the mode internet: no other host, no other port.
func (t *egressTask) isEndpoint(host, port string) bool {
	if t.rule.Mode != egressInternet && t.rule.Mode != "" {
		return false
	}
	target := net.JoinHostPort(host, port)
	for _, e := range t.rule.Endpoints {
		h, p, err := net.SplitHostPort(strings.TrimSpace(e))
		if err == nil && net.JoinHostPort(strings.TrimSuffix(strings.ToLower(h), "."), p) == target {
			return true
		}
	}
	return false
}

// dialModelServer connects to the task's declared model server, which may
// be on the LAN: its name resolved here, the addresses the node itself,
// link-local (cloud metadata), multicast or unspecified left out, and the
// very address checked dialed.
func (p *egressProxy) dialModelServer(ctx context.Context, t *egressTask, host, port string) (net.Conn, error) {
	var addrs []netip.Addr
	if a, err := netip.ParseAddr(host); err == nil {
		addrs = []netip.Addr{a}
	} else if addrs, err = p.lookup(ctx, host); err != nil {
		return nil, &egressError{http.StatusBadGateway, fmt.Sprintf("cannot resolve %s: %v", host, err)}
	}
	var ok []netip.Addr
	for _, a := range addrs {
		if endpointAddr(a) {
			ok = append(ok, a.Unmap())
		}
	}
	if len(ok) == 0 {
		err := refused("%s is not a model server's address (loopback, link-local, multicast or unspecified)", host)
		p.note(t, host, err)
		return nil, err
	}
	p.note(t, host, nil)
	var last error
	for _, a := range ok {
		c, err := p.dialEndpoint(ctx, "tcp", netip.AddrPortFrom(a, mustPort(port)).String())
		if err == nil {
			return c, nil
		}
		last = err
	}
	return nil, &egressError{http.StatusBadGateway, fmt.Sprintf("cannot reach %s: %v", net.JoinHostPort(host, port), last)}
}

func mustPort(port string) uint16 {
	var p uint16
	fmt.Sscan(port, &p)
	return p
}

// check applies the task's rule to a host and port, before any lookup.
func (p *egressProxy) check(t *egressTask, host, port string) error {
	switch t.rule.Mode {
	case egressInternet, "":
	case egressNone:
		return refused("this task has no egress")
	default:
		return refused("unknown egress mode %q", t.rule.Mode)
	}
	if !egressPorts[port] {
		return refused("port %s is not allowed (only 80 and 443)", port)
	}
	if len(t.rule.Allow) > 0 && !hostAllowed(t.rule.Allow, host) {
		return refused("%s is not in this task's allowlist", host)
	}
	return nil
}

// note logs a task's first connection to each host, allowed or refused.
func (p *egressProxy) note(t *egressTask, host string, err error) {
	if t.seen != nil && !t.seen(host) {
		return
	}
	if err != nil {
		p.logf("task %s: egress to %s refused: %v", t.id, host, err)
		return
	}
	p.logf("task %s: egress to %s", t.id, host)
}

// credentials reads Proxy-Authorization: Basic base64(id:secret).
func credentials(r *http.Request) (id, secret string, ok bool) {
	auth, ok := strings.CutPrefix(r.Header.Get("Proxy-Authorization"), "Basic ")
	if !ok {
		return "", "", false
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(auth))
	if err != nil {
		return "", "", false
	}
	return strings.Cut(string(raw), ":")
}

func (p *egressProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	id, secret, ok := credentials(r)
	var t *egressTask
	if ok {
		t, ok = p.authorize(id, secret)
	}
	if !ok {
		w.Header().Set("Proxy-Authenticate", `Basic realm="eriphe-node"`)
		http.Error(w, "proxy authentication required", http.StatusProxyAuthRequired)
		return
	}
	if r.Method == http.MethodConnect {
		p.connect(w, r, t)
		return
	}
	if r.URL.Scheme != "http" || r.URL.Host == "" {
		http.Error(w, "this is a proxy: plain http URLs or CONNECT only", http.StatusBadRequest)
		return
	}
	p.forward(w, r, t)
}

func writeEgressError(w http.ResponseWriter, err error) {
	var e *egressError
	if errors.As(err, &e) {
		http.Error(w, e.msg, e.status)
		return
	}
	http.Error(w, err.Error(), http.StatusBadGateway)
}

// connect opens a tunnel, for HTTPS: the relay sees the host, not the traffic.
func (p *egressProxy) connect(w http.ResponseWriter, r *http.Request, t *egressTask) {
	upstream, err := p.dial(r.Context(), t, r.Host)
	if err != nil {
		writeEgressError(w, err)
		return
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		upstream.Close()
		http.Error(w, "cannot tunnel", http.StatusInternalServerError)
		return
	}
	client, buf, err := hj.Hijack()
	if err != nil {
		upstream.Close()
		return
	}
	if _, err := client.Write([]byte("HTTP/1.1 200 Connection established\r\n\r\n")); err != nil {
		client.Close()
		upstream.Close()
		return
	}
	// What the client sent after its CONNECT, already read.
	if n := buf.Reader.Buffered(); n > 0 {
		b, _ := buf.Reader.Peek(n)
		if _, err := upstream.Write(b); err != nil {
			client.Close()
			upstream.Close()
			return
		}
	}
	var wg sync.WaitGroup
	wg.Add(2)
	pipe := func(dst, src net.Conn) {
		defer wg.Done()
		io.Copy(dst, src)
		if c, ok := dst.(interface{ CloseWrite() error }); ok {
			c.CloseWrite()
		} else {
			dst.Close()
		}
	}
	go pipe(upstream, client)
	go pipe(client, upstream)
	wg.Wait()
	client.Close()
	upstream.Close()
}

// forward relays a plain HTTP request, its credentials removed.
func (p *egressProxy) forward(w http.ResponseWriter, r *http.Request, t *egressTask) {
	target := r.URL.Host
	if r.URL.Port() == "" {
		target = net.JoinHostPort(r.URL.Hostname(), "80")
	}
	// Dialed before relaying, so that a refusal is a 403, not a 502.
	conn, err := p.dial(r.Context(), t, target)
	if err != nil {
		writeEgressError(w, err)
		return
	}
	taken := false
	defer func() {
		if !taken {
			conn.Close()
		}
	}()
	rp := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			u := *pr.In.URL
			pr.Out.URL = &u
			pr.Out.Host = pr.In.Host
			// Hop-by-hop headers, Proxy-Authorization among them, are
			// already gone.
		},
		Transport: &http.Transport{
			Proxy:             nil, // never the node's own proxy settings
			DisableKeepAlives: true,
			DialContext: func(context.Context, string, string) (net.Conn, error) {
				if taken {
					return nil, errors.New("one connection per request")
				}
				taken = true
				return conn, nil
			},
			ResponseHeaderTimeout: 2 * time.Minute,
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			http.Error(w, err.Error(), http.StatusBadGateway)
		},
		FlushInterval: -1,
	}
	rp.ServeHTTP(w, r)
}

// egressURL is the proxy URL a task is given, its credentials in it.
func egressURL(host, port, id, secret string) string {
	return (&url.URL{Scheme: "http", User: url.UserPassword(id, secret), Host: net.JoinHostPort(host, port)}).String()
}

// egressEnv points a task's programs at the relay, the node's own listener
// excepted (its permission endpoint and catalog relay stay direct). The
// brain goes out through it too (its web reads must not reach the LAN),
// but reaches its own MCP servers (eriphe-core, on the services' network)
// directly.
func (n *node) egressEnv(t *Task) map[string]string {
	if n.opt.egressPort == "" {
		return nil
	}
	cb, err := url.Parse(n.callbackFor(t))
	if err != nil || cb.Hostname() == "" {
		return nil
	}
	proxy := egressURL(cb.Hostname(), n.opt.egressPort, t.ID, t.secret)
	noProxy := []string{cb.Hostname(), "localhost", "127.0.0.1"}
	if host, _, err := net.SplitHostPort(n.opt.listenAddr); err == nil && host != cb.Hostname() {
		noProxy = append(noProxy, host)
	}
	if t.Spec.SystemNetwork {
		for _, def := range t.Spec.MCPServers {
			d, _ := def.(map[string]any)
			raw, _ := d["url"].(string)
			if u, err := url.Parse(raw); err == nil && u.Hostname() != "" && !slices.Contains(noProxy, u.Hostname()) {
				noProxy = append(noProxy, u.Hostname())
			}
		}
		slices.Sort(noProxy[3:])
	}
	np := strings.Join(noProxy, ",")
	return map[string]string{
		"HTTP_PROXY": proxy, "HTTPS_PROXY": proxy, "http_proxy": proxy, "https_proxy": proxy,
		"NO_PROXY": np, "no_proxy": np,
		// Node's own fetch ignores the variables unless asked (Node 24).
		"NODE_USE_ENV_PROXY": "1",
	}
}

// selfTargets are the host:port a task may name to reach the node's task
// listener through the relay: its callback URL, and the bound address.
func selfTargets(callbackURL, listenAddr string) []string {
	var out []string
	if u, err := url.Parse(callbackURL); err == nil && u.Hostname() != "" {
		port := u.Port()
		if port == "" {
			port = "80"
		}
		out = append(out, net.JoinHostPort(u.Hostname(), port))
	}
	if listenAddr != "" {
		out = append(out, listenAddr)
	}
	return out
}

// egressListenAddr is where the relay binds: an empty host is the task
// listener's.
func egressListenAddr(listen, taskListener string) (string, error) {
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return "", fmt.Errorf("-egress-listen %q: %w", listen, err)
	}
	if host == "" {
		if host, _, err = net.SplitHostPort(taskListener); err != nil {
			return "", err
		}
	}
	return net.JoinHostPort(host, port), nil
}
