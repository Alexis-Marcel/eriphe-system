package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/yamux"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestRunSpecValidation(t *testing.T) {
	no := false
	ok := []RunSpec{
		{Image: "mcp/time", Transport: "stdio"},
		{Image: "ghcr.io/x/caldav:1", Transport: "http", Port: 8080, Env: map[string]EnvValue{"A": {Value: "b"}, "B": {File: "/secrets/b"}}},
		{Image: "x", Transport: "stdio", User: "1001", Volume: true, Writable: true, Egress: &no, Node: "nas", Needs: []string{"gpu"}},
	}
	for _, s := range ok {
		n, err := s.normalize()
		if err != nil {
			t.Errorf("%+v: %v", s, err)
			continue
		}
		if n.User == "" || n.Egress == nil || (n.Transport == "http" && n.Path != "/mcp") {
			t.Errorf("defaults not filled: %+v", n)
		}
	}
	if n, _ := ok[0].normalize(); n.User != "1000:1000" || !*n.Egress {
		t.Errorf("defaults = %+v", n)
	}
	bad := []RunSpec{
		{Transport: "stdio"},
		{Image: "-v", Transport: "stdio"},
		{Image: "x", Transport: "sse"},
		{Image: "x", Transport: "http"},
		{Image: "x", Transport: "http", Port: 70000},
		{Image: "x", Transport: "stdio", Port: 8080},
		{Image: "x", Transport: "http", Port: 80, Path: "mcp"},
		{Image: "x", Transport: "stdio", User: "0:0"},
		{Image: "x", Transport: "stdio", User: "000"},
		{Image: "x", Transport: "stdio", User: "root"},
		{Image: "x", Transport: "stdio", Env: map[string]EnvValue{"A-B": {Value: "x"}}},
		{Image: "x", Transport: "stdio", Env: map[string]EnvValue{"A": {File: "relative"}}},
		{Image: "x", Transport: "stdio", Node: "Not A Node"},
		{Image: "x", Transport: "stdio", Prefers: []string{"a b"}},
	}
	for _, s := range bad {
		if _, err := s.normalize(); err == nil {
			t.Errorf("%+v: accepted", s)
		}
	}
	if _, err := checkServices(map[string]RunSpec{"Mail": {Image: "x", Transport: "stdio"}}); err == nil {
		t.Error("bad service name accepted")
	}
}

func TestRunSpecJSON(t *testing.T) {
	var s RunSpec
	err := json.Unmarshal([]byte(`{"image":"x","transport":"stdio","env":{"HOST":"imap.example.org","PASSWORD":{"file":"/secrets/mail"}}}`), &s)
	if err != nil || s.Env["HOST"].Value != "imap.example.org" || s.Env["PASSWORD"].File != "/secrets/mail" {
		t.Fatalf("%+v, %v", s, err)
	}
	raw, _ := json.Marshal(s)
	if !bytes.Contains(raw, []byte(`"PASSWORD":{"file":"/secrets/mail"}`)) || !bytes.Contains(raw, []byte(`"HOST":"imap.example.org"`)) {
		t.Fatalf("marshal = %s", raw)
	}
	for _, in := range []string{`{"env":{"A":{"value":"x"}}}`, `{"env":{"A":{"file":""}}}`, `{"env":{"A":3}}`} {
		if err := json.Unmarshal([]byte(in), &s); err == nil {
			t.Errorf("%s: accepted", in)
		}
	}
}

func newServiceCore(t *testing.T) (*core, *Store) {
	t.Helper()
	store, err := openStore(filepath.Join(t.TempDir(), "core.db"))
	if err != nil {
		t.Fatal(err)
	}
	return newCore(&Config{TimeZone: "Europe/Paris"}, store), store
}

func TestServicePlacement(t *testing.T) {
	c, store := newServiceCore(t)
	for _, n := range []string{"nas", "pc", "old"} {
		store.registerNode(n, hashSecret(n))
	}
	store.setNodeStatus("nas", true, []byte(`{"services":true,"cpus":4}`))
	store.setNodeStatus("pc", true, []byte(`{"services":true,"cpus":16,"labels":["gpu"],"images":["mcp/time:latest"]}`))
	store.setNodeStatus("old", true, []byte(`{"runtimes":["claude-code"],"cpus":64}`)) // runs no services

	err := c.registerServices(map[string]RunSpec{
		"time":  {Image: "mcp/time", Transport: "stdio", Prefers: []string{"gpu"}},        // prefers pc
		"mail":  {Image: "mail", Transport: "stdio", Node: "nas"},                         // pinned
		"gpu":   {Image: "x", Transport: "stdio", Needs: []string{"gpu"}},                 // only pc
		"tpu":   {Image: "x", Transport: "stdio", Needs: []string{"tpu"}},                 // nobody: waits
		"notes": {Image: "x", Transport: "stdio", Volume: true, Prefers: []string{"cpu"}}, // pc, then sticks to it
	})
	if err != nil {
		t.Fatal(err)
	}
	where := func(name string) *ServiceRow {
		s, err := store.service(name)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	for name, node := range map[string]string{"time": "pc", "mail": "nas", "gpu": "pc", "tpu": "", "notes": "pc"} {
		if s := where(name); s.Node != node || s.State != statePending {
			t.Errorf("%s on %q (%s), want %q", name, s.Node, s.State, node)
		}
	}
	if s := where("tpu"); !strings.Contains(s.PendingReason, "tpu") {
		t.Errorf("tpu pending reason = %q", s.PendingReason)
	}

	// pc goes away: within the grace period nothing moves; after it, the
	// services that may move do, a volume stays.
	store.setNodeStatus("pc", false, nil)
	c.placeServices()
	if s := where("time"); s.Node != "pc" {
		t.Fatalf("moved within the grace period: %+v", s)
	}
	store.db.Exec(`UPDATE nodes SET last_seen = ? WHERE name = 'pc'`, time.Now().Add(-serviceNodeGrace-time.Minute).Unix())
	c.placeServices()
	if s := where("time"); s.Node != "nas" {
		t.Errorf("time = %+v, want re-placed on nas", s)
	}
	if s := where("gpu"); s.Node != "" || s.PendingReason == "" {
		t.Errorf("gpu = %+v, want waiting", s)
	}
	if s := where("notes"); s.Node != "pc" {
		t.Errorf("notes (volume) = %+v, want kept on pc", s)
	}

	// The pinned service waits for its node.
	store.setNodeStatus("nas", false, nil)
	store.db.Exec(`UPDATE nodes SET last_seen = 0 WHERE name = 'nas'`)
	c.placeServices()
	if s := where("mail"); s.Node != "nas" {
		t.Errorf("mail (pinned) = %+v", s)
	}

	// A new pin moves it; a service left out is removed.
	store.setNodeStatus("pc", true, []byte(`{"services":true,"labels":["gpu"]}`))
	if err := c.registerServices(map[string]RunSpec{"time": {Image: "mcp/time", Transport: "stdio", Node: "pc"}}); err != nil {
		t.Fatal(err)
	}
	if s := where("time"); s.Node != "pc" {
		t.Errorf("time = %+v, want moved to its new pin", s)
	}
	if rows, _ := store.services(); len(rows) != 1 {
		t.Errorf("services = %+v", rows)
	}
}

// A node fetches its services with their secrets, which are never stored,
// and reports their state.
func TestServiceAssignments(t *testing.T) {
	c, store := newServiceCore(t)
	secret := filepath.Join(t.TempDir(), "mail")
	os.WriteFile(secret, []byte("hunter2\n"), 0o600)
	store.registerNode("nas", hashSecret("nas-secret"))
	store.registerNode("pc", hashSecret("pc-secret"))
	store.setNodeStatus("nas", true, []byte(`{"services":true}`))
	store.setNodeStatus("pc", true, []byte(`{"runtimes":["claude-code"]}`))
	if err := c.registerServices(map[string]RunSpec{
		"mail": {Image: "mail", Transport: "stdio", Volume: true,
			Env: map[string]EnvValue{"IMAP_HOST": {Value: "imap.example.org"}, "IMAP_PASSWORD": {File: secret}}},
		"web": {Image: "web", Transport: "http", Port: 8080, Env: map[string]EnvValue{"KEY": {File: "/nonexistent"}}},
	}); err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/node/assignments", c.handleAssignments)
	mux.HandleFunc("POST /v1/node/services/{name}/report", c.handleServiceReport)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	call := func(node, method, path string, in, out any) int {
		var body bytes.Buffer
		json.NewEncoder(&body).Encode(in)
		req, _ := http.NewRequest(method, srv.URL+path, &body)
		req.Header.Set("Authorization", "Bearer "+node+"-secret")
		req.Header.Set("Eriphe-Node", node)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if out != nil {
			json.NewDecoder(resp.Body).Decode(out)
		}
		return resp.StatusCode
	}
	var a struct {
		Tasks    []nodeAssignment `json:"tasks"`
		Services []nodeService    `json:"services"`
	}
	call("nas", "GET", "/v1/node/assignments?rev=0&wait=0", nil, &a)
	if len(a.Services) != 1 || a.Services[0].Name != "mail" {
		t.Fatalf("services = %+v", a.Services)
	}
	got := a.Services[0]
	if got.Spec.Env["IMAP_PASSWORD"] != "hunter2" || got.Spec.Env["IMAP_HOST"] != "imap.example.org" ||
		got.Spec.Env["TZ"] != "Europe/Paris" ||
		got.Spec.User != "1000:1000" || !got.Spec.Egress || !got.Spec.Volume || got.Hash == "" {
		t.Fatalf("service = %+v", got)
	}
	// The secret is lent, never written.
	var dump bytes.Buffer
	rows, _ := store.db.Query(`SELECT spec || error FROM services`)
	for rows.Next() {
		var s string
		rows.Scan(&s)
		dump.WriteString(s)
	}
	rows.Close()
	if strings.Contains(dump.String(), "hunter2") || !strings.Contains(dump.String(), secret) {
		t.Fatalf("stored: %s", dump.String())
	}
	// The one whose secret cannot be read fails, and is not sent.
	if s, _ := store.service("web"); s.State != stateFailed || !strings.Contains(s.Error, "KEY") {
		t.Fatalf("web = %+v", s)
	}

	if code := call("nas", "POST", "/v1/node/services/mail/report", serviceReport{State: stateRunning, Restarts: 2}, nil); code != http.StatusNoContent {
		t.Fatalf("report: %d", code)
	}
	if s, _ := store.service("mail"); s.State != stateRunning || s.Restarts != 2 {
		t.Fatalf("mail = %+v", s)
	}
	// Another node cannot report on it, and does not get it.
	if code := call("pc", "POST", "/v1/node/services/mail/report", serviceReport{State: stateFailed}, nil); code != http.StatusNotFound {
		t.Fatalf("report from another node: %d", code)
	}
	call("pc", "GET", "/v1/node/assignments?rev=0&wait=0", nil, &a)
	if len(a.Services) != 0 {
		t.Fatalf("pc got %+v", a.Services)
	}
}

// dialService reaches a service's MCP endpoint on its node, through the
// tunnel.
func TestDialService(t *testing.T) {
	cfg := &Config{Servers: map[string]Server{
		"time": {Use: "the time", Run: json.RawMessage(`{"image": "mcp/time", "transport": "stdio"}`)},
		"web":  {Use: "a remote one", URL: "https://example.org/mcp", Auth: &ServerAuth{None: true}},
	}}
	if err := cfg.checkServers(); err != nil {
		t.Fatal(err)
	}
	specs, err := cfg.localServers()
	if err != nil || len(specs) != 1 || specs["time"].Image != "mcp/time" || specs["time"].User != defaultUser {
		t.Fatalf("local servers = %+v, %v", specs, err)
	}
	bad := &Config{Servers: map[string]Server{"x": {Use: "x", Run: json.RawMessage(`{"image": "x", "transport": "stdio", "privileged": true}`)}}}
	if _, err := bad.localServers(); err == nil {
		t.Fatal("unknown run field accepted")
	}
	store, err := openStore(filepath.Join(t.TempDir(), "core.db"))
	if err != nil {
		t.Fatal(err)
	}
	c := newCore(cfg, store)
	store.registerNode("nas", hashSecret("x"))
	if _, err := c.dialService(context.Background(), "time"); err == nil {
		t.Fatal("dialed an unknown service")
	}

	// A fake node on the other end of a tunnel, serving one MCP server.
	server := mcp.NewServer(&mcp.Implementation{Name: "time"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "now"}, func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "noon"}}}, nil, nil
	})
	api := http.NewServeMux()
	api.HandleFunc("GET /v1/node", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte(`{"services":true}`)) })
	api.Handle("/v1/services/time/mcp", mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server },
		&mcp.StreamableHTTPOptions{DisableLocalhostProtection: true}))
	coreEnd, nodeEnd := net.Pipe()
	nodeSession, _ := yamux.Server(nodeEnd, tunnelConfig())
	go http.Serve(nodeSession, api)
	coreSession, _ := yamux.Client(coreEnd, tunnelConfig())
	c.attach("nas", coreSession, false)
	defer coreSession.Close()
	waitUntil(t, func() bool { n, _ := store.nodes(); return len(n) == 1 && n[0].Online })

	if err := c.registerServices(specs); err != nil {
		t.Fatal(err)
	}
	if _, err := c.dialService(context.Background(), "time"); err == nil || !strings.Contains(err.Error(), "pending") {
		t.Fatalf("dialed a service not running yet: %v", err)
	}
	store.updateService("time", map[string]any{"state": stateRunning})
	// The catalog's pool dials it through the services.
	transport, err := c.pool.dial(context.Background(), c.pool.get("time"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "eriphe-core"}, nil).Connect(ctx, transport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "now"})
	if err != nil || len(res.Content) != 1 || res.Content[0].(*mcp.TextContent).Text != "noon" {
		t.Fatalf("call = %+v, %v", res, err)
	}
}

func waitUntil(t *testing.T, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// The tools go to the machines that never sleep, leave a sleeping one when
// such a machine can take them, and try another machine when they keep
// failing on one.
func TestServicesOnAlwaysOnNodes(t *testing.T) {
	c, store := newServiceCore(t)
	for _, n := range []string{"nas", "pc"} {
		store.registerNode(n, hashSecret(n))
	}
	store.setNodeStatus("nas", false, []byte(`{"services":true,"always_on":true,"cpus":4}`))
	store.setNodeStatus("pc", true, []byte(`{"services":true,"cpus":16,"labels":["gpu"]}`))
	where := func(name string) string {
		s, err := store.service(name)
		if err != nil {
			t.Fatal(err)
		}
		return s.Node
	}
	err := c.registerServices(map[string]RunSpec{
		"agenda": {Image: "cal", Transport: "stdio", Prefers: []string{"cpu"}}, // pc is stronger, but sleeps
		"gpu":    {Image: "x", Transport: "stdio", Needs: []string{"gpu"}},     // only pc can
	})
	if err != nil {
		t.Fatal(err)
	}
	if where("agenda") != "pc" {
		t.Fatalf("agenda on %q, want pc while nas is off", where("agenda"))
	}
	// nas comes up: the tool leaves pc for it; the one only pc can run stays.
	store.setNodeStatus("nas", true, []byte(`{"services":true,"always_on":true,"cpus":4}`))
	c.placeServices()
	c.placeServices()
	if where("agenda") != "nas" || where("gpu") != "pc" {
		t.Fatalf("agenda on %q, gpu on %q", where("agenda"), where("gpu"))
	}
	// It keeps failing on nas: it goes to pc, and stays there.
	c.moveFailed("agenda", "nas", serviceMoveAfter)
	c.placeServices()
	if where("agenda") != "pc" {
		t.Fatalf("agenda on %q after failing on nas, want pc", where("agenda"))
	}
	// A new spec gets a new chance on nas.
	err = c.registerServices(map[string]RunSpec{
		"agenda": {Image: "cal:2", Transport: "stdio"},
		"gpu":    {Image: "x", Transport: "stdio", Needs: []string{"gpu"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	c.placeServices()
	if where("agenda") != "nas" {
		t.Fatalf("agenda on %q after a new spec, want nas", where("agenda"))
	}
}
