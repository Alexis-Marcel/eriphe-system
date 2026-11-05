package main

import (
	"encoding/base64"
	"errors"
	"fmt"
	"log"
	"maps"
	"net"
	"net/http"
	"net/http/httputil"
	"net/netip"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// An access: something a task uses with a real program, in its container,
// rather than a tool it calls (a package registry, an API with a key). It
// is declared in Settings → Tools, its secret in the vault, and lent as a
// resource, {"kind": "access", "ref": "npm"}. Eriphe cannot see what the
// program does with it, only each use of the secret: so an access has no
// grid, only a value, Free or On approval, asked once per task on the card
// of a tool (access_card.go). What it can limit (its hosts, its rules) is a
// property of it.
//
// The agent never holds the secret. Its container gets a placeholder in the
// access's variable; the node's relay decrypts the access's hosts only, with
// the task's own certificate authority, and hands each request here:
//
//	program → the node's relay (node/egress_access.go) → eriphe-core → the service
//
//	/v1/node/tasks/{id}/access   (node secret)
//
// eriphe-core puts the secret in the access's one header, in place of the
// placeholder, and goes on to the host the relay was asked for, never the
// one a request names. A placeholder sent anywhere else stays a placeholder.
//
// This does not stop a service that reflects its headers from handing the
// secret back: only response headers are scrubbed here (not bodies, trailers
// nor 1xx answers), and the access's page says a long-lived secret is only
// as safe as its service.

const (
	accessHTTP = "http" // the only delivery yet: a secret in a header

	// catUse is what an access is lent for: it has no other category.
	catUse = "use"

	accessDefaultHeader = "Authorization"

	// What the node's relay adds to a request: the host the task connected
	// to, the request's own target, and its Authorization (the node's
	// secret takes that header on the way here).
	accessHostHeader   = "Eriphe-Access-Host"
	accessTargetHeader = "Eriphe-Access-Target"
	accessAuthHeader   = "Eriphe-Access-Authorization"

	// accessLogTool names an access's uses in the credential log.
	accessLogTool = "access"
)

// Access is one declared access.
type Access struct {
	Use    string `json:"use,omitempty"`    // what it is, for the brain and the user
	Kind   string `json:"kind,omitempty"`   // how it is delivered: http
	Secret string `json:"secret,omitempty"` // a file, or vault:<name>
	// Hosts are what it reaches: "registry.npmjs.org", or with a port other
	// than 443, "registry.example.org:8443".
	Hosts []string `json:"hosts"`
	Env   string   `json:"env"`             // the variable holding the placeholder
	Value string   `json:"value,omitempty"` // free, or approval (the default)
	// Header is the one header the secret goes in; Authorization by default.
	Header string `json:"header,omitempty"`
	// Rules, if any, are the only requests the secret goes on:
	// "GET /v1/*", "* /api/packages", a method or *, then a path, ending in
	// * for all that starts with it.
	Rules []string `json:"rules,omitempty"`
}

var (
	accessHeaderName = regexp.MustCompile(`^[A-Za-z0-9-]+$`)
	accessMethod     = regexp.MustCompile(`^([A-Z]+|\*)$`)
	accessHostName   = regexp.MustCompile(`^[a-z0-9]([a-z0-9.-]*[a-z0-9])?$`)
)

// checkAccessEnv tells whether a variable may hold a placeholder: what a
// credential may not take (agents.go), and the bundle's. GitHub's token
// variables are a credential's own: a placeholder is what they should hold.
func checkAccessEnv(env string) error {
	switch env {
	case "GH_TOKEN", "GITHUB_TOKEN":
		return nil
	case "REQUESTS_CA_BUNDLE", "CURL_CA_BUNDLE", "PIP_CERT":
		return fmt.Errorf("its variable %s is the system's, not a credential's", env)
	}
	return checkCredentialEnv(env)
}

// accessHeldBack are headers the secret could not get through: those a
// proxy drops on the way, the relay's own, and what a request is not sent
// by name.
func accessHeldBack(header string) bool {
	switch http.CanonicalHeaderKey(header) {
	case "Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization", "Proxy-Connection", "Te", "Trailer",
		"Transfer-Encoding", "Upgrade", "Host", "Content-Length":
		return true
	}
	return strings.HasPrefix(http.CanonicalHeaderKey(header), "Eriphe-")
}

func (a Access) value() string {
	if a.Value == gridFree {
		return gridFree
	}
	return gridApproval
}

func (a Access) header() string {
	if a.Header != "" {
		return http.CanonicalHeaderKey(a.Header)
	}
	return accessDefaultHeader
}

// accessTarget is a host as the relay and eriphe-core compare it: lowercase,
// no trailing dot, its port always there.
func accessTarget(host string) (string, error) {
	host = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
	name, port, err := net.SplitHostPort(host)
	if err != nil {
		name, port = strings.Trim(host, "[]"), "443"
	}
	if name = strings.TrimSuffix(name, "."); name == "" {
		return "", fmt.Errorf("%q is not a host", host)
	}
	if _, err := netip.ParseAddr(name); err != nil && !accessHostName.MatchString(name) {
		return "", fmt.Errorf("%q is not a host: a name such as registry.npmjs.org, with a port if not 443", host)
	}
	if p, err := strconv.Atoi(port); err != nil || p < 1 || p > 65535 || strconv.Itoa(p) != port {
		return "", fmt.Errorf("%q: bad port", host)
	}
	return net.JoinHostPort(name, port), nil
}

// targets are the access's hosts, compared as accessTarget says.
func (a Access) targets() []string {
	var out []string
	for _, h := range a.Hosts {
		if t, err := accessTarget(h); err == nil {
			out = append(out, t)
		}
	}
	return out
}

// accessRefusal is what is wrong with an access: in English for the log and
// the file, under a key of web_text_tools.go for the workspace's form.
func accessRefusal(key, arg, name, format string, args ...any) error {
	return &catalogError{http.StatusBadRequest, key, arg, fmt.Errorf("access %s: "+format, append([]any{name}, args...)...)}
}

func (a Access) check(name string) error {
	if a.Kind != "" && a.Kind != accessHTTP {
		return fmt.Errorf("access %s: unknown kind %q", name, a.Kind)
	}
	if a.Secret == "" {
		return accessRefusal("lent.err.secret", "", name, "it needs a secret")
	}
	if len(a.Hosts) == 0 {
		return accessRefusal("lent.err.nohost", "", name, "it needs at least one host")
	}
	for _, h := range a.Hosts {
		t, err := accessTarget(h)
		if err != nil {
			return accessRefusal("lent.err.host", h, name, "%w", err)
		}
		host, _, _ := net.SplitHostPort(t)
		if ip, err := netip.ParseAddr(host); err == nil && !accessAddr(ip) {
			return accessRefusal("lent.err.private", host, name, "%s is not on the public Internet: eriphe-core only goes to public addresses", host)
		}
	}
	if err := checkAccessEnv(a.Env); err != nil {
		return accessRefusal("lent.err.env", a.Env, name, "%w", err)
	}
	if a.Value != "" && a.Value != gridFree && a.Value != gridApproval {
		return fmt.Errorf("access %s: its value is free or approval, not %q", name, a.Value)
	}
	if a.Header != "" && (!accessHeaderName.MatchString(a.Header) || accessHeldBack(a.Header)) {
		return accessRefusal("lent.err.header", a.Header, name, "%q is not a header name", a.Header)
	}
	for _, r := range a.Rules {
		if _, _, err := parseAccessRule(r); err != nil {
			return accessRefusal("lent.err.rule", r, name, "%w", err)
		}
	}
	return nil
}

// checkAccesses checks the accesses against each other and the tools: a
// card and a release name them by name alone.
func (c *Config) checkAccesses() error {
	for _, name := range slices.Sorted(maps.Keys(c.Accesses)) {
		if err := checkEntryName(kindAccess, name); err != nil {
			return err
		}
		if err := c.Accesses[name].check(name); err != nil {
			return err
		}
		if _, ok := c.Servers[name]; ok {
			return accessRefusal("lent.err.tool", name, name, "a tool has this name")
		}
	}
	return nil
}

func parseAccessRule(rule string) (method, path string, err error) {
	f := strings.Fields(rule)
	if len(f) != 2 || !accessMethod.MatchString(f[0]) || !strings.HasPrefix(f[1], "/") {
		return "", "", fmt.Errorf("rule %q: a method or *, then a path, as GET /v1/*", rule)
	}
	return f[0], f[1], nil
}

// accessMethodOverrides make some services take a POST for another method:
// with rules, they go.
var accessMethodOverrides = []string{"X-Http-Method-Override", "X-Http-Method", "X-Method-Override"}

// allows tells whether the rules let a request have the secret. A path that
// may climb on the service would pass a prefix on its way elsewhere: refused
// once there are rules. That is a segment of .. (or ..; as Java servers read
// it), a backslash some servers read as a slash, and what is still encoded
// after one decoding, for a server that decodes twice. A limit against
// mistakes and plain REST: the real one is the token's rights.
func (a Access) allows(method, path string) bool {
	if len(a.Rules) == 0 {
		return true
	}
	if strings.ContainsAny(path, "\\%") || slices.ContainsFunc(strings.Split(path, "/"), func(s string) bool { return strings.HasPrefix(s, "..") }) {
		return false
	}
	for _, r := range a.Rules {
		m, p, err := parseAccessRule(r)
		if err != nil || (m != "*" && m != method) {
			continue
		}
		if prefix, ok := strings.CutSuffix(p, "*"); ok && strings.HasPrefix(path, prefix) || p == path {
			return true
		}
	}
	return false
}

// accessPlaceholder is what a task holds in place of a secret: of no use
// anywhere else, nor in another task.
func accessPlaceholder(taskID, name string) string { return "eriphe-access-" + name + "-" + taskID }

// swapPlaceholder puts the secret where the placeholder is in a header's
// value: as it is, or inside Basic credentials, as git sends it.
func swapPlaceholder(value, placeholder, secret string) (string, bool) {
	if strings.Contains(value, placeholder) {
		return strings.ReplaceAll(value, placeholder, secret), true
	}
	scheme, encoded, ok := strings.Cut(value, " ")
	if !ok || !strings.EqualFold(scheme, "Basic") {
		return value, false
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encoded))
	if err != nil || !strings.Contains(string(raw), placeholder) {
		return value, false
	}
	return scheme + " " + base64.StdEncoding.EncodeToString([]byte(strings.ReplaceAll(string(raw), placeholder, secret))), true
}

// accessBlocked are the non-public ranges netip has no predicate for, as in
// the node's relay (node/egress.go): some embed a private IPv4.
var accessBlocked = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("64:ff9b::/96"),
	netip.MustParsePrefix("64:ff9b:1::/48"),
	netip.MustParsePrefix("2001::/32"),
	netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("2002::/16"),
}

// accessAddr is an address eriphe-core goes to for an access: a public one.
// A declared name must not lead it to its own network, the cloud's metadata
// or the node's LAN (a private target leaves from the node: not yet).
func accessAddr(a netip.Addr) bool {
	a = a.Unmap()
	if !a.IsValid() || !a.IsGlobalUnicast() || localAddr(a) {
		return false
	}
	return !slices.ContainsFunc(accessBlocked, func(p netip.Prefix) bool { return p.Contains(a) })
}

// accessControl refuses, at the socket, any other address, whatever
// resolved the name.
func accessControl(_, address string, _ syscall.RawConn) error {
	ap, err := netip.ParseAddrPort(address)
	if err != nil || !accessAddr(ap.Addr()) {
		return fmt.Errorf("%s: not a public address", address)
	}
	return nil
}

// accessTransport reaches the services: never through a proxy of the
// environment, HTTP/1.1 (an Upgrade goes through it), no limit on an answer
// beyond the headers'.
var accessTransport http.RoundTripper = &http.Transport{
	DialContext:           (&net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second, Control: accessControl}).DialContext,
	TLSHandshakeTimeout:   15 * time.Second,
	ResponseHeaderTimeout: 2 * time.Minute,
	MaxIdleConnsPerHost:   4,
	IdleConnTimeout:       time.Minute,
}

// accessesOn are the accesses lent to a task that reach a host, held ones
// included: being lent is not being usable (accessUsable).
func (c *core) accessesOn(t *TaskRow, target string) []string {
	var out []string
	conf := c.conf()
	for _, e := range t.Resources {
		if a, ok := conf.Accesses[e.Ref]; e.Kind == resAccess && ok && slices.Contains(a.targets(), target) {
			out = append(out, e.Ref)
		}
	}
	return out
}

// accessUsable tells why a task may not use an access now, if it may not:
// it is held back, or it needs a yes it did not get. Free holds on a clean
// start only, whatever it read since, as for a tool (policy.go).
func (c *core) accessUsable(t *TaskRow, name string) error {
	i := slices.IndexFunc(t.Resources, func(e ResourceRef) bool { return e.Kind == resAccess && e.Ref == name })
	if i < 0 {
		return fmt.Errorf("the access %s is not lent to this task", name)
	}
	if t.Resources[i].Hold {
		return fmt.Errorf("the access %s is held back: it waits for the user to release it", name)
	}
	if c.conf().Accesses[name].value() == gridFree && c.store.get(lentByUserKey(t.ID)) != "" {
		return nil
	}
	if c.accessGranted(t.ID, accessNeed{Server: name, Category: catUse}) {
		return nil
	}
	return fmt.Errorf("the access %s needs the user's approval, which this task did not get", name)
}

// taskAccessHandler serves /v1/node/tasks/{id}/access, for nodes.
func (c *core) taskAccessHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		node, ok := c.nodeFromRequest(r)
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		t, err := c.store.task(r.PathValue("id"))
		if err != nil || t.Node != node || finished(t.State) {
			http.Error(w, "no such task on this node", http.StatusNotFound)
			return
		}
		target, err := accessTarget(r.Header.Get(accessHostHeader))
		if err != nil {
			http.Error(w, "which host?", http.StatusBadRequest)
			return
		}
		uri, err := url.ParseRequestURI(r.Header.Get(accessTargetHeader))
		if err != nil || uri.Host != "" {
			http.Error(w, "which path?", http.StatusBadRequest)
			return
		}
		names := c.accessesOn(t, target)
		if len(names) == 0 {
			http.Error(w, "no access of this task reaches "+target, http.StatusForbidden)
			return
		}
		// What the service gets: the task's request, nothing of the node's.
		auth := r.Header.Get(accessAuthHeader)
		for k := range r.Header {
			if strings.HasPrefix(k, "Eriphe-") {
				r.Header.Del(k)
			}
		}
		r.Header.Del("Authorization")
		if auth != "" {
			r.Header.Set("Authorization", auth)
		}
		scrub, err := c.injectAccess(t, names, target, r, uri)
		if err != nil {
			http.Error(w, err.Error(), http.StatusForbidden)
			return
		}
		proxy := &httputil.ReverseProxy{
			Rewrite: func(pr *httputil.ProxyRequest) {
				pr.Out.URL = &url.URL{Scheme: "https", Host: target, Path: uri.Path, RawPath: uri.RawPath, RawQuery: uri.RawQuery}
				pr.Out.Host = strings.TrimSuffix(target, ":443")
			},
			ModifyResponse: func(res *http.Response) error {
				for k, vs := range res.Header {
					for i := range vs {
						res.Header[k][i] = scrub.Replace(vs[i])
					}
				}
				return nil
			},
			FlushInterval: -1,
			Transport:     accessTransport,
			ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
				if r.Context().Err() == nil {
					log.Printf("access: task %s: %s: %v", t.ID, target, err)
				}
				http.Error(w, "eriphe-core cannot reach "+target, http.StatusBadGateway)
			},
		}
		proxy.ServeHTTP(w, r)
	})
}

// injectAccess puts the secret of each access the request carries a
// placeholder of in its header, or says why not. A request carrying none
// asks for no secret and goes as it is. What it answers takes the secrets
// out of the response's headers.
func (c *core) injectAccess(t *TaskRow, names []string, target string, r *http.Request, uri *url.URL) (*strings.Replacer, error) {
	var pairs []string
	conf := c.conf()
	host := strings.TrimSuffix(target, ":443")
	for _, name := range names {
		a := conf.Accesses[name]
		placeholder := accessPlaceholder(t.ID, name)
		header := a.header()
		value := r.Header.Get(header)
		if _, carried := swapPlaceholder(value, placeholder, ""); !carried {
			continue
		}
		deny := func(why error) error {
			// The path is the task's: one short line in what watches it.
			line := truncate(oneLine(why.Error()), 300)
			c.store.logCredential(t.ID, accessLogTool, host, name, r.Method, decideDeny, byPolicy, "", line)
			log.Printf("access: task %s: %s %s refused: %s", t.ID, r.Method, host, line)
			return why
		}
		if err := c.accessUsable(t, name); err != nil {
			return nil, deny(err)
		}
		if !a.allows(r.Method, uri.Path) {
			return nil, deny(fmt.Errorf("the access %s does not allow %s %s", name, r.Method, uri.Path))
		}
		secret, err := readSecret(a.Secret)
		if err != nil {
			log.Printf("access: %s: %v", name, err)
			return nil, deny(fmt.Errorf("the secret of the access %s cannot be read", name))
		}
		swapped, _ := swapPlaceholder(value, placeholder, secret)
		r.Header.Set(header, swapped)
		if len(a.Rules) > 0 {
			for _, h := range accessMethodOverrides {
				r.Header.Del(h)
			}
		}
		// The secret as it left, too: Basic is its base64, whole value.
		pairs = append(pairs, swapped, value, secret, placeholder)
		if _, encoded, ok := strings.Cut(swapped, " "); ok {
			_, original, _ := strings.Cut(value, " ")
			pairs = append(pairs, encoded, original)
		}
		c.store.logCredential(t.ID, accessLogTool, host, name, r.Method, decideAllow, byPolicy, "", truncate(oneLine(uri.Path), 200))
	}
	return strings.NewReplacer(pairs...), nil
}

// lentAccesses are a task's accesses as its node gets them: no secret.
func (c *core) lentAccesses(taskID string, list []ResourceRef) []nodeAccess {
	var out []nodeAccess
	conf := c.conf()
	for _, e := range list {
		if a, ok := conf.Accesses[e.Ref]; e.Kind == resAccess && ok {
			out = append(out, nodeAccess{Name: e.Ref, Hosts: a.targets(), Env: a.Env, Placeholder: accessPlaceholder(taskID, e.Ref)})
		}
	}
	return out
}

// checkAgentEnv refuses an access whose variable is one the task's agent is
// lent: the real credential would win over the placeholder, and go out to
// the access's hosts.
func (c *core) checkAgentEnv(list []ResourceRef, agentEnv map[string]string) error {
	for _, e := range list {
		if e.Kind != resAccess {
			continue
		}
		if env := c.conf().Accesses[e.Ref].Env; agentEnv[env] != "" {
			return fmt.Errorf("access %s uses the variable %s, which holds this task's agent's own key: rename it, or use another agent", e.Ref, env)
		}
	}
	return nil
}

// checkLentAccesses refuses what one task cannot be lent together: two
// accesses cannot share a variable.
func (c *core) checkLentAccesses(list []ResourceRef) error {
	byEnv := map[string]string{}
	for _, e := range list {
		if e.Kind != resAccess {
			continue
		}
		env := c.conf().Accesses[e.Ref].Env
		if other, ok := byEnv[env]; ok && other != e.Ref {
			return fmt.Errorf("accesses %s and %s both use the variable %s: lend one per task", other, e.Ref, env)
		}
		byEnv[env] = e.Ref
	}
	return nil
}

// accessInstructions tell a task how to use what it is lent.
func (c *core) accessInstructions(list []ResourceRef) string {
	var b strings.Builder
	conf := c.conf()
	for _, e := range list {
		a, ok := conf.Accesses[e.Ref]
		if e.Kind != resAccess || !ok {
			continue
		}
		fmt.Fprintf(&b, "\n- %s: $%s, for %s", e.Ref, a.Env, strings.Join(a.Hosts, ", "))
		if a.header() != accessDefaultHeader {
			fmt.Fprintf(&b, " (in the header %s)", a.header())
		}
		if e.Hold {
			b.WriteString(" (held back until the user releases it)")
		}
	}
	if b.Len() == 0 {
		return ""
	}
	return "You are lent these accesses:" + b.String() + "\nEach variable holds a placeholder, not the real secret: use it as you would the secret (a bearer token, a password, a tool's configuration), over HTTPS, and the real one is put in on the way out, for those hosts only. It cannot be read, and is of no use anywhere else; never ask for the real one."
}

// accessView is an access as the brain sees it.
type accessView struct {
	Name  string   `json:"name"`
	Use   string   `json:"use,omitempty"`
	Hosts []string `json:"hosts"`
	Env   string   `json:"env"`
	Value string   `json:"value"`
	Lend  any      `json:"lend"`
}

func (c *core) listAccesses() []accessView {
	conf := c.conf()
	out := []accessView{}
	for _, name := range slices.Sorted(maps.Keys(conf.Accesses)) {
		a := conf.Accesses[name]
		out = append(out, accessView{Name: name, Use: a.Use, Hosts: a.Hosts, Env: a.Env, Value: a.value(),
			Lend: map[string]string{"kind": resAccess, "ref": name}})
	}
	return out
}

// putAccess adds or replaces a workspace access; secret, if given, goes in
// the vault, and an empty one keeps the current one.
func (c *core) putAccess(mode, name string, a Access, secret string) error {
	c.catalogMu.Lock()
	defer c.catalogMu.Unlock()
	_, inBase := c.base.Accesses[name]
	old, inDyn := c.dyn.Accesses[name]
	if err := checkPut(mode, kindAccess, name, inBase, inDyn); err != nil {
		return err
	}
	a.Kind = accessHTTP
	writes := map[string]string{}
	switch {
	case secret != "":
		ref := entrySecret(kindAccess, name, "secret")
		a.Secret = vaultPrefix + ref
		writes[ref] = secret
	case inDyn:
		a.Secret = old.Secret
	default:
		return accessRefusal("lent.err.secret", "", name, "it needs a secret")
	}
	d := c.dyn.clone()
	d.Accesses[name] = a
	verb := "ajouté"
	if inDyn {
		verb = "modifié"
	}
	return c.commit(d, change{writes: writes, persist: func() error { return c.store.putCatalog(kindAccess, name, a) },
		event: fmt.Sprintf("Catalogue : l'accès « %s » a été %s depuis l'espace de travail (%s). list_tools le montre.", name, verb, oneLine(truncate(a.Use, 200)))})
}

func (c *core) deleteAccess(name string) error {
	c.catalogMu.Lock()
	defer c.catalogMu.Unlock()
	if _, ok := c.base.Accesses[name]; ok {
		return errReadOnly(kindAccess, name)
	}
	if _, ok := c.dyn.Accesses[name]; !ok {
		return errNotFound(kindAccess, name)
	}
	d := c.dyn.clone()
	delete(d.Accesses, name)
	return c.commit(d, change{persist: func() error { return c.store.deleteCatalog(kindAccess, name) },
		event: fmt.Sprintf("Catalogue : l'accès « %s » a été retiré depuis l'espace de travail ; ne le prête plus aux tâches.", name)})
}

// errNoAccess is a lend of an access that is not declared.
func errNoAccess(name string) error {
	return errors.New("access " + name + ": unknown (see list_tools)")
}
