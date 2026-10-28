package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Local MCP servers run as long-lived containers on a node, like a
// Kubernetes Deployment of one replica behind a Service: eriphe-core keeps
// what each should be (its spec, its node), the node keeps its container
// running and serves it as a streamable HTTP MCP endpoint through the
// tunnel, and eriphe-core reaches it there (dialService).
//
//	GET  /v1/node/assignments              the node's tasks, and its services
//	POST /v1/node/services/{name}/report   state, restarts, last error
//	node: /v1/services/{name}/mcp          the service's MCP endpoint

// RunSpec is how a local MCP server runs: the `run` variant of a catalog
// server. Its secrets stay in files, read when its node fetches it.
type RunSpec struct {
	Image     string `json:"image"`
	Transport string `json:"transport"`      // stdio (the node holds its stdin and stdout) or http
	Port      int    `json:"port,omitempty"` // http: the port it listens on in its container
	Path      string `json:"path,omitempty"` // http: its MCP path, /mcp by default
	// Command line arguments, after the image's entrypoint.
	Args []string            `json:"args,omitempty"`
	Env  map[string]EnvValue `json:"env,omitempty"`

	Node    string   `json:"node,omitempty"`    // pin it to a node; empty lets the scheduler choose
	Needs   []string `json:"needs,omitempty"`   // node labels required
	Prefers []string `json:"prefers,omitempty"` // cpu, memory or a node label

	Volume   bool   `json:"volume,omitempty"`   // a persistent /data, kept on its node
	User     string `json:"user,omitempty"`     // uid:gid, 1000:1000 by default; never root
	Writable bool   `json:"writable,omitempty"` // a writable root filesystem (read-only by default)
	Egress   *bool  `json:"egress,omitempty"`   // reach the network; true by default
}

// EnvValue is a literal, or a secret read from a file: "x" or {"file": "/secrets/x"}.
type EnvValue struct {
	Value string
	File  string
}

func (e *EnvValue) UnmarshalJSON(raw []byte) error {
	if err := json.Unmarshal(raw, &e.Value); err == nil {
		return nil
	}
	var f struct {
		File string `json:"file"`
	}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil || f.File == "" {
		return errors.New(`an environment value is a string or {"file": "/path"}`)
	}
	e.File = f.File
	return nil
}

func (e EnvValue) MarshalJSON() ([]byte, error) {
	if e.File != "" {
		return json.Marshal(map[string]string{"file": e.File})
	}
	return json.Marshal(e.Value)
}

const (
	transportStdio = "stdio"
	transportHTTP  = "http"
	defaultUser    = "1000:1000"
)

var (
	serviceName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)
	envName     = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	userSpec    = regexp.MustCompile(`^([0-9]+)(:[0-9]+)?$`)
)

// normalize checks a run spec and fills in its defaults.
func (s RunSpec) normalize() (RunSpec, error) {
	if s.Image == "" || strings.ContainsAny(s.Image, " \t\n") || strings.HasPrefix(s.Image, "-") {
		return s, fmt.Errorf("bad image %q", s.Image)
	}
	switch s.Transport {
	case transportStdio:
		if s.Port != 0 || s.Path != "" {
			return s, errors.New("port and path are for the http transport")
		}
	case transportHTTP:
		if s.Port < 1 || s.Port > 65535 {
			return s, errors.New("the http transport needs the container's port")
		}
		if s.Path == "" {
			s.Path = "/mcp"
		}
		if !strings.HasPrefix(s.Path, "/") {
			return s, fmt.Errorf("bad path %q", s.Path)
		}
	default:
		return s, fmt.Errorf("transport is stdio or http, not %q", s.Transport)
	}
	for name, v := range s.Env {
		if !envName.MatchString(name) {
			return s, fmt.Errorf("bad environment variable name %q", name)
		}
		if v.File != "" && !filepath.IsAbs(v.File) && !strings.HasPrefix(v.File, vaultPrefix) {
			return s, fmt.Errorf("env %s: the file must be an absolute path or vault:<name>", name)
		}
	}
	if s.Node != "" && !nodeName.MatchString(s.Node) {
		return s, fmt.Errorf("bad node %q", s.Node)
	}
	for _, l := range append(append([]string{}, s.Needs...), s.Prefers...) {
		if l == "" || strings.ContainsAny(l, " ,") {
			return s, fmt.Errorf("bad label %q", l)
		}
	}
	if s.User == "" {
		s.User = defaultUser
	}
	m := userSpec.FindStringSubmatch(s.User)
	if m == nil {
		return s, fmt.Errorf("user is uid or uid:gid in numbers, not %q", s.User)
	}
	if strings.TrimLeft(m[1], "0") == "" {
		return s, errors.New("a service never runs as root")
	}
	if s.Egress == nil {
		yes := true
		s.Egress = &yes
	}
	return s, nil
}

// localServers reads the run specs of the catalog's local servers.
func (c *Config) localServers() (map[string]RunSpec, error) {
	specs := map[string]RunSpec{}
	for name, s := range c.Servers {
		if !s.local() {
			continue
		}
		var spec RunSpec
		dec := json.NewDecoder(bytes.NewReader(s.Run))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&spec); err != nil {
			return nil, fmt.Errorf("server %s: run: %w", name, err)
		}
		specs[name] = spec
	}
	return checkServices(specs)
}

// checkServices validates run specs, by service name.
func checkServices(specs map[string]RunSpec) (map[string]RunSpec, error) {
	out := map[string]RunSpec{}
	for name, s := range specs {
		if !serviceName.MatchString(name) {
			return nil, fmt.Errorf("service %q: a name of lowercase letters, digits and dashes", name)
		}
		n, err := s.normalize()
		if err != nil {
			return nil, fmt.Errorf("service %s: %w", name, err)
		}
		out[name] = n
	}
	return out, nil
}

func (s RunSpec) hash() string {
	raw, _ := json.Marshal(s)
	h := sha256.Sum256(raw)
	return hex.EncodeToString(h[:8])
}

func (s RunSpec) placement() placement {
	return placement{Service: true, Needs: s.Needs, Prefers: s.Prefers, Image: s.Image}
}

// lendService reads a service's environment, secrets included, for its
// node. The values are never stored or logged.
func lendService(s RunSpec) (map[string]string, error) {
	env := map[string]string{}
	for name, v := range s.Env {
		if v.File == "" {
			env[name] = v.Value
			continue
		}
		secret, err := readSecret(v.File)
		if err != nil {
			return nil, fmt.Errorf("env %s: %w", name, err)
		}
		env[name] = secret
	}
	return env, nil
}

// A node gone for longer than this loses its services to another node,
// unless they are pinned to it or keep data on it.
const serviceNodeGrace = 5 * time.Minute

// serviceMoveAfter is how many failed starts on a node make a tool try
// another one.
const serviceMoveAfter = 3

// registerServices makes specs the desired services: new ones are placed,
// changed ones restart (on another node if theirs no longer fits), and
// those left out are removed, their containers stopped by their node. A
// service's volume stays on its node. The catalog calls it with its local
// servers (the run entries of its configuration) at start.
func (c *core) registerServices(specs map[string]RunSpec) error {
	specs, err := checkServices(specs)
	if err != nil {
		return err
	}
	rows, err := c.store.services()
	if err != nil {
		return err
	}
	have := map[string]*ServiceRow{}
	for _, r := range rows {
		have[r.Name] = r
	}
	for name, spec := range specs {
		old := have[name]
		delete(have, name)
		if old != nil && old.Hash == spec.hash() {
			continue
		}
		if err := c.store.putService(name, spec); err != nil {
			return err
		}
		c.failedOn.Delete(name) // a new spec gets a new chance everywhere
		if old != nil && old.Node != "" {
			// A new pin, or new needs: the node may not fit any more.
			nodes, _ := c.store.nodes()
			if spec.Node != "" && spec.Node != old.Node || !nodeFits(nodes, old.Node, spec.placement()) {
				c.store.unassignService(name, "")
			}
		}
	}
	for name := range have {
		if err := c.store.deleteService(name); err != nil {
			return err
		}
		log.Printf("service %s removed", name)
	}
	c.placeServices()
	c.bump()
	return nil
}

func nodeFits(nodes []NodeRow, name string, p placement) bool {
	for _, n := range nodes {
		if n.Name == name {
			return p.fits(n)
		}
	}
	return false
}

// movable says whether the scheduler may move a service: one pinned to a
// node, or keeping a /data there, stays.
func movable(s RunSpec) bool { return s.Node == "" && !s.Volume }

// serviceNodes narrows the nodes a tool may go to. A tool must answer at
// 8 am and at midnight: the always-on nodes first, the others only when
// none of those can run it. The node it kept failing on is left out while
// another can take it.
func serviceNodes(nodes []NodeRow, p placement, avoid string) []NodeRow {
	var steady, rest []NodeRow
	for _, n := range nodes {
		if n.Name == avoid || !p.fits(n) {
			continue
		}
		if parseInfo(n).AlwaysOn {
			steady = append(steady, n)
		} else {
			rest = append(rest, n)
		}
	}
	switch {
	case len(steady) > 0:
		return steady
	case len(rest) > 0:
		return rest
	}
	return nodes // none other: the scheduler says why
}

// steadyOnline reports whether an online always-on node, other than the
// one to avoid, can run p.
func steadyOnline(nodes []NodeRow, p placement, avoid string) bool {
	for _, n := range nodes {
		if n.Name != avoid && n.Online && p.fits(n) && parseInfo(n).AlwaysOn {
			return true
		}
	}
	return false
}

// avoided is the node a service kept failing on, if any.
func (c *core) avoided(name string) string {
	v, _ := c.failedOn.Load(name)
	s, _ := v.(string)
	return s
}

// placeServices gives a node to the services without one, and takes theirs
// from the services whose node has been gone too long. It runs when a node
// answers, and every 15 seconds.
func (c *core) placeServices() {
	c.placing.Lock()
	defer c.placing.Unlock()
	rows, err := c.store.services()
	if err != nil {
		log.Printf("store: %v", err)
		return
	}
	nodes, err := c.store.nodes()
	if err != nil {
		log.Printf("store: %v", err)
		return
	}
	byName := map[string]NodeRow{}
	for _, n := range nodes {
		byName[n.Name] = n
	}
	changed := false
	for _, r := range rows {
		if r.Desired != desiredRunning {
			continue
		}
		avoid := c.avoided(r.Name)
		if r.Node != "" {
			n, known := byName[r.Node]
			switch {
			case n.Online && (parseInfo(n).AlwaysOn || !movable(r.Spec) || !steadyOnline(nodes, r.Spec.placement(), avoid)):
				continue
			case n.Online:
				// A tool on a machine that sleeps, now that one that does not can take it.
			case !known:
				// The node was forgotten: its services, volumes or not, go elsewhere.
			case r.Spec.Node != "" || r.Spec.Volume:
				continue // it waits for its node
			case time.Since(n.LastSeen) < serviceNodeGrace:
				continue
			}
			log.Printf("service %s leaves node %s", r.Name, r.Node)
			c.store.unassignService(r.Name, fmt.Sprintf("node %s is gone", r.Node))
			r.Node = ""
			changed = true
		}
		candidates := nodes
		if movable(r.Spec) {
			candidates = serviceNodes(nodes, r.Spec.placement(), avoid)
		}
		name, err := schedule(candidates, r.Spec.Node, r.Spec.placement(), c.serviceLoad)
		if err != nil {
			if err.Error() != r.PendingReason {
				c.store.updateService(r.Name, map[string]any{"pending_reason": err.Error()})
			}
			continue
		}
		if ok, _ := c.store.assignService(r.Name, name); ok {
			log.Printf("service %s placed on %s", r.Name, name)
			changed = true
		}
	}
	if changed {
		c.bump()
	}
}

// serviceLoad is what a node already runs, for the scheduler's score.
func (c *core) serviceLoad(node string) (int, error) {
	tasks, err := c.store.activeCount(node)
	if err != nil {
		return 0, err
	}
	return tasks + c.store.serviceCount(node), nil
}

// nodeService is one service as its node receives it, with its secrets.
type nodeService struct {
	Name string          `json:"name"`
	Hash string          `json:"hash"`
	Spec nodeServiceSpec `json:"spec"`
}

type nodeServiceSpec struct {
	Image     string            `json:"image"`
	Transport string            `json:"transport"`
	Port      int               `json:"port,omitempty"`
	Path      string            `json:"path,omitempty"`
	Args      []string          `json:"args,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
	User      string            `json:"user"`
	Writable  bool              `json:"writable,omitempty"`
	Volume    bool              `json:"volume,omitempty"`
	Egress    bool              `json:"egress"`
}

// serviceAssignments are the services a node must run, their secrets read
// now. One whose secret cannot be read fails, and is left out.
func (c *core) serviceAssignments(node string) ([]nodeService, error) {
	rows, err := c.store.services()
	if err != nil {
		return nil, err
	}
	out := []nodeService{}
	for _, r := range rows {
		if r.Node != node || r.Desired != desiredRunning {
			continue
		}
		env, err := lendService(r.Spec)
		if err != nil {
			msg := err.Error()
			if r.State != stateFailed || r.Error != msg {
				log.Printf("service %s fails: %v", r.Name, err)
				c.store.updateService(r.Name, map[string]any{"state": stateFailed, "error": msg})
			}
			continue
		}
		if _, set := env["TZ"]; !set && c.conf().TimeZone != "" {
			env["TZ"] = c.conf().TimeZone // its dates in the user's zone, like the tasks'
		}
		s := r.Spec
		out = append(out, nodeService{Name: r.Name, Hash: r.Hash, Spec: nodeServiceSpec{
			Image: s.Image, Transport: s.Transport, Port: s.Port, Path: s.Path, Args: s.Args, Env: env,
			User: s.User, Writable: s.Writable, Volume: s.Volume, Egress: s.Egress == nil || *s.Egress}})
	}
	return out, nil
}

// serviceReport is what a node says about one of its services.
type serviceReport struct {
	State    string `json:"state"` // starting, running, failed (restarting), stopped
	Restarts int    `json:"restarts"`
	Error    string `json:"error,omitempty"`
}

func (c *core) handleServiceReport(w http.ResponseWriter, r *http.Request) {
	node, ok := c.nodeFromRequest(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	s, err := c.store.service(r.PathValue("name"))
	if err != nil || s.Node != node {
		http.Error(w, "no such service on this node", http.StatusNotFound)
		return
	}
	var in serviceReport
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, "bad report", http.StatusBadRequest)
		return
	}
	switch in.State {
	case "starting", stateRunning, stateFailed, stateStopped:
	default:
		http.Error(w, "unknown state", http.StatusBadRequest)
		return
	}
	c.store.updateService(s.Name, map[string]any{"state": in.State, "restarts": in.Restarts,
		"error": tail(in.Error, 2000)})
	if in.State == stateRunning && s.State != stateRunning {
		c.wakeUpstream(s.Name)
	}
	if in.State == stateFailed && in.Restarts >= serviceMoveAfter && movable(s.Spec) {
		go c.moveFailed(s.Name, node, in.Restarts)
	}
	w.WriteHeader(http.StatusNoContent)
}

// moveFailed takes a service that keeps failing on a node (an image the
// machine cannot pull, say) to another one. The node stays avoided until
// the service's spec changes or eriphe-core restarts, so that it does not
// come back to fail again.
func (c *core) moveFailed(name, node string, restarts int) {
	log.Printf("service %s failed %d times on %s, trying another node", name, restarts, node)
	c.failedOn.Store(name, node)
	c.store.unassignService(name, fmt.Sprintf("failed on node %s", node))
	c.placeServices()
	c.bump()
}

func tail(s string, n int) string {
	if len(s) > n {
		return s[len(s)-n:]
	}
	return s
}

// handleServices lists the services and where they run (admin only).
func (c *core) handleServices(w http.ResponseWriter, _ *http.Request) {
	rows, err := c.store.services()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if rows == nil {
		rows = []*ServiceRow{}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
	writeJSON(w, http.StatusOK, rows)
}

// serviceEndpoint is how eriphe-core reaches a running service: its node's
// MCP endpoint, through the tunnel. The client has no timeout: an MCP
// session keeps a stream open.
func (c *core) serviceEndpoint(name string) (*http.Client, string, error) {
	s, err := c.store.service(name)
	if err != nil {
		return nil, "", err
	}
	switch {
	case s.Node == "":
		reason := s.PendingReason
		if reason == "" {
			reason = "not placed yet"
		}
		return nil, "", fmt.Errorf("service %s has no node: %s", name, reason)
	case s.State != stateRunning:
		msg := fmt.Sprintf("service %s is %s on %s", name, s.State, s.Node)
		if s.Error != "" {
			msg += ": " + s.Error
		}
		return nil, "", errors.New(msg)
	}
	n := c.node(s.Node)
	if !n.connected() {
		return nil, "", fmt.Errorf("service %s: node %s is not connected", name, s.Node)
	}
	return n.stream, "http://node/v1/services/" + name + "/mcp", nil
}

// wakeUpstream tells the catalog's pool that a local server it waits for
// is up, rather than letting it wait for its backoff. A live session stays.
func (c *core) wakeUpstream(name string) {
	if c.pool == nil {
		return
	}
	u := c.pool.get(name)
	if u == nil {
		return
	}
	u.mu.Lock()
	idle := u.session == nil
	u.mu.Unlock()
	if idle {
		select {
		case u.wake <- struct{}{}:
		default:
		}
	}
}

// dialService opens an MCP transport to a running local server, for the
// catalog's pool (its dialLocal).
func (c *core) dialService(_ context.Context, name string) (mcp.Transport, error) {
	client, url, err := c.serviceEndpoint(name)
	if err != nil {
		return nil, err
	}
	return &mcp.StreamableClientTransport{Endpoint: url, HTTPClient: client}, nil
}
