package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The test binary doubles as the "container" of a service: a small MCP
// server, on stdio or on HTTP, started by a fake docker script.
func TestMain(m *testing.M) {
	switch os.Getenv("ERIPHE_FAKE_MCP") {
	case "stdio":
		fakeMCPServer().Run(context.Background(), &mcp.StdioTransport{})
		os.Exit(0)
	case "http":
		h := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return fakeMCPServer() }, nil)
		mux := http.NewServeMux()
		mux.Handle("/mcp", h)
		http.ListenAndServe("127.0.0.1:"+os.Getenv("FAKE_PORT"), mux)
		os.Exit(1)
	case "fail":
		f, _ := os.OpenFile(os.Getenv("FAKE_RUNS"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		fmt.Fprintf(f, "%.4f\n", float64(time.Now().UnixNano())/1e9)
		f.Close()
		fmt.Fprintln(os.Stderr, "boom")
		os.Exit(1)
	}
	os.Exit(m.Run())
}

func fakeMCPServer() *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "fake", Version: "1"}, &mcp.ServerOptions{Instructions: "a fake server"})
	type echoIn struct {
		Text string `json:"text"`
	}
	mcp.AddTool(s, &mcp.Tool{Name: "echo", Description: "echo text"}, func(_ context.Context, _ *mcp.CallToolRequest, in echoIn) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: in.Text}}}, nil, nil
	})
	mcp.AddTool(s, &mcp.Tool{Name: "pid"}, func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: strconv.Itoa(os.Getpid())}}}, nil, nil
	})
	mcp.AddTool(s, &mcp.Tool{Name: "crash"}, func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, any, error) {
		os.Exit(3)
		return nil, nil, nil
	})
	return s
}

// fakeDocker writes a docker CLI that logs its calls, answers network
// inspect and ps, and runs this test binary as the container (or fails, if
// run is "fail").
func fakeDocker(t *testing.T, run string) (docker, dir string) {
	t.Helper()
	if filepath.Separator != '/' {
		t.Skip("needs sh")
	}
	dir = t.TempDir()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if run == "" {
		run = `port=""; prev=""
  for a in "$@"; do
    if [ "$prev" = "-p" ]; then port=${a#127.0.0.1:}; port=${port%%:*}; fi
    prev=$a
  done
  mode=stdio; [ -n "$port" ] && mode=http
  echo $$ > ` + dir + `/pid
  ERIPHE_FAKE_MCP=$mode FAKE_PORT=$port exec ` + self
	} else if run == "fail" {
		run = `ERIPHE_FAKE_MCP=fail FAKE_RUNS=` + dir + `/runs exec ` + self
	}
	script := `#!/bin/sh
echo "$*" >> ` + dir + `/calls
case "$1" in
network) echo "172.17.0.0/16 172.17.0.1;" ;;
ps) echo c1 c2 ;;
stop) [ -f ` + dir + `/pid ] && kill $(cat ` + dir + `/pid) 2>/dev/null; true ;;
run)
  printf '%s\n' "$@" > ` + dir + `/args
  ` + run + `
  ;;
esac
`
	docker = filepath.Join(dir, "docker")
	if err := os.WriteFile(docker, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return docker, dir
}

func hasSeq(args []string, want ...string) bool {
	for i := range args {
		if slices.Equal(args[i:min(i+len(want), len(args))], want) {
			return true
		}
	}
	return false
}

func TestServiceArgs(t *testing.T) {
	docker, _ := fakeDocker(t, "true")
	root := t.TempDir()
	n := newNode(options{taskNetwork: "eriphe-tasks", root: root, hostRoot: "/host/node", docker: docker, serviceMemory: "1g"})
	spec := serviceSpec{Image: "ghcr.io/x/mail:1", Transport: "http", Port: 8080, Path: "/mcp", Args: []string{"--flag"},
		Env: map[string]string{"IMAP_PASSWORD": "hunter2"}, User: "1001:1002", Volume: true, Egress: true}
	args, env, target, err := n.serviceArgs("mail", spec)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range [][]string{
		{"run", "--rm", "--name", "eriphe-svc-mail", "--label", "eriphe.service=mail"},
		{"--user", "1001:1002"}, {"--cap-drop", "ALL"}, {"--security-opt", "no-new-privileges"},
		{"--pids-limit", "256"}, {"--read-only"}, {"--memory", "1g"},
		{"--add-host", "host.docker.internal:172.17.0.1"},
		{"-v", "/host/node/services/mail:/data"}, // under the node's root, as the daemon sees it
		{"-e", "IMAP_PASSWORD"}, {"ghcr.io/x/mail:1", "--flag"},
	} {
		if !hasSeq(args, want...) {
			t.Errorf("missing %v in %v", want, args)
		}
	}
	if strings.Contains(strings.Join(args, " "), "hunter2") || !slices.Contains(env, "IMAP_PASSWORD=hunter2") {
		t.Error("the secret is on the command line, or not in its environment")
	}
	if slices.Contains(args, "-i") || slices.Contains(args, "eriphe.task") {
		t.Errorf("args = %v", args)
	}
	// Outside Docker, an http service is published on a loopback port.
	i := slices.Index(args, "-p")
	if i < 0 || !strings.HasPrefix(args[i+1], "127.0.0.1:") || !strings.HasSuffix(args[i+1], ":8080") ||
		target != strings.TrimSuffix(args[i+1], ":8080") {
		t.Errorf("publish = %v, target %s", args, target)
	}
	if st, err := os.Stat(filepath.Join(root, "services", "mail")); err != nil || !st.IsDir() {
		t.Errorf("volume folder: %v", err)
	}

	// On a shared network (the NAS), by name; stdio is attached; without
	// egress, no network at all.
	n.opt.network = "eriphe-system"
	args, _, target, _ = n.serviceArgs("mail", spec)
	if !hasSeq(args, "--network", "eriphe-system") || slices.Contains(args, "-p") || target != "eriphe-svc-mail:8080" {
		t.Errorf("args = %v, target %s", args, target)
	}
	args, _, _, _ = n.serviceArgs("time", serviceSpec{Image: "mcp/time", Transport: "stdio", Writable: true, Egress: true})
	if !hasSeq(args, "--network", "eriphe-system") || !slices.Contains(args, "-i") || slices.Contains(args, "--read-only") ||
		!hasSeq(args, "--user", "1000:1000") || slices.Contains(args, "-v") {
		t.Errorf("stdio args = %v", args)
	}
	args, _, _, _ = n.serviceArgs("time", serviceSpec{Image: "mcp/time", Transport: "stdio", Egress: false})
	if !hasSeq(args, "--network", "none") {
		t.Errorf("no egress = %v", args)
	}
	if _, _, _, err := n.serviceArgs("web", serviceSpec{Image: "x", Transport: "http", Port: 80}); err == nil {
		t.Error("http without egress nor -service-network: accepted")
	}
	n.opt.serviceNetwork = "eriphe-services"
	if args, _, _, _ := n.serviceArgs("web", serviceSpec{Image: "x", Transport: "http", Port: 80}); !hasSeq(args, "--network", "eriphe-services") {
		t.Errorf("internal network = %v", args)
	}
	for _, bad := range []serviceSpec{
		{Image: "x", Transport: "stdio", User: "0:0"}, {Image: "x", Transport: "stdio", User: "root"},
		{Image: "--privileged", Transport: "stdio"}, {Image: "x", Transport: "http", Egress: true},
	} {
		if _, _, _, err := n.serviceArgs("x", bad); err == nil {
			t.Errorf("%+v: accepted", bad)
		}
	}
}

// Stray containers of a previous run: tasks and services are cleaned
// separately, each by its own label.
func TestStrayContainers(t *testing.T) {
	docker, dir := fakeDocker(t, "true")
	n := newNode(options{taskNetwork: "eriphe-tasks", root: t.TempDir(), docker: docker})
	n.removeStrayContainers()
	n.removeStrayServices()
	raw, _ := os.ReadFile(filepath.Join(dir, "calls"))
	want := "ps -aq --filter label=eriphe.task\nrm -f c1 c2\nps -aq --filter label=eriphe.service\nrm -f c1 c2\n"
	if string(raw) != want {
		t.Fatalf("calls:\n%s", raw)
	}
}

func serviceClient(t *testing.T, url string) *mcp.ClientSession {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil).Connect(ctx,
		&mcp.StreamableClientTransport{Endpoint: url}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

func callText(t *testing.T, cs *mcp.ClientSession, tool string, args any) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		return "", err
	}
	if res.IsError || len(res.Content) == 0 {
		return "", fmt.Errorf("tool error: %+v", res.Content)
	}
	return res.Content[0].(*mcp.TextContent).Text, nil
}

func toolNames(t *testing.T, cs *mcp.ClientSession) []string {
	t.Helper()
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
	}
	slices.Sort(names)
	return names
}

// A stdio service: the node holds the process, two sessions share it, a
// crash restarts it under the same sessions, and unassigning stops it.
func TestServiceStdioBridge(t *testing.T) {
	serviceBackoffMin = 50 * time.Millisecond
	docker, dir := fakeDocker(t, "")
	n := newNode(options{taskNetwork: "eriphe-tasks", root: t.TempDir(), docker: docker})
	mux := http.NewServeMux()
	n.routes(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close) // after the clients' sessions

	n.reconcileServices([]serviceAssignment{{Name: "fake", Hash: "h1", Spec: serviceSpec{Image: "fake", Transport: "stdio", User: "1000:1000"}}})
	s := n.getService("fake")
	waitFor(t, func() bool { s.mu.Lock(); defer s.mu.Unlock(); return s.state == svcRunning })

	a := serviceClient(t, srv.URL+"/v1/services/fake/mcp")
	b := serviceClient(t, srv.URL+"/v1/services/fake/mcp")
	if init := a.InitializeResult(); init.ServerInfo.Name != "fake" || init.Instructions != "a fake server" {
		t.Errorf("initialize = %+v", init)
	}
	if got := toolNames(t, a); !slices.Equal(got, []string{"crash", "echo", "pid"}) {
		t.Fatalf("tools = %v", got)
	}
	if got, err := callText(t, b, "echo", map[string]any{"text": "hello"}); err != nil || got != "hello" {
		t.Fatalf("echo = %q, %v", got, err)
	}
	pidA, _ := callText(t, a, "pid", nil)
	pidB, _ := callText(t, b, "pid", nil)
	if pidA == "" || pidA != pidB {
		t.Fatalf("sessions do not share the process: %q %q", pidA, pidB)
	}

	// A crash: the service restarts, the same sessions go on with a new process.
	callText(t, a, "crash", nil)
	waitFor(t, func() bool { s.mu.Lock(); defer s.mu.Unlock(); return s.restarts == 1 && s.state == svcRunning })
	var pid string
	waitFor(t, func() bool { pid, _ = callText(t, b, "pid", nil); return pid != "" })
	if pid == pidA {
		t.Fatal("same process after a crash")
	}
	if got, err := callText(t, a, "echo", map[string]any{"text": "again"}); err != nil || got != "again" {
		t.Fatalf("echo after restart = %q, %v", got, err)
	}
	if s.lastErr != "" {
		t.Errorf("error kept after recovery: %q", s.lastErr)
	}

	// Unassigned: stopped, and gone from the endpoint.
	n.reconcileServices(nil)
	waitFor(t, func() bool { return n.getService("fake") == nil })
	<-s.done
	if s.state != svcStopped {
		t.Errorf("state = %s", s.state)
	}
	if resp, _ := http.Get(srv.URL + "/v1/services/fake/mcp"); resp.StatusCode != http.StatusNotFound {
		t.Errorf("endpoint after stop: %s", resp.Status)
	}
	calls, _ := os.ReadFile(filepath.Join(dir, "calls"))
	if !strings.Contains(string(calls), "stop -t 10 eriphe-svc-fake") {
		t.Errorf("calls:\n%s", calls)
	}
}

// An http service: the node publishes its port and proxies to it.
func TestServiceHTTPProxy(t *testing.T) {
	docker, _ := fakeDocker(t, "")
	n := newNode(options{taskNetwork: "eriphe-tasks", root: t.TempDir(), docker: docker})
	mux := http.NewServeMux()
	n.routes(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close) // after the clients' sessions

	if resp, _ := http.Get(srv.URL + "/v1/services/web/mcp"); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown service: %s", resp.Status)
	}
	n.reconcileServices([]serviceAssignment{{Name: "web", Spec: serviceSpec{Image: "web", Transport: "http", Port: 8080, Path: "/mcp", Egress: true}}})
	s := n.getService("web")
	waitFor(t, func() bool { s.mu.Lock(); defer s.mu.Unlock(); return s.state == svcRunning })
	cs := serviceClient(t, srv.URL+"/v1/services/web/mcp")
	if got := toolNames(t, cs); !slices.Equal(got, []string{"crash", "echo", "pid"}) {
		t.Fatalf("tools = %v", got)
	}
	if got, err := callText(t, cs, "echo", map[string]any{"text": "via http"}); err != nil || got != "via http" {
		t.Fatalf("echo = %q, %v", got, err)
	}
	n.reconcileServices(nil)
	<-s.done
}

// A service that keeps failing restarts forever, ever more slowly, and
// says why.
func TestServiceBackoff(t *testing.T) {
	serviceBackoffMin, serviceBackoffMax = 40*time.Millisecond, 160*time.Millisecond
	defer func() { serviceBackoffMin, serviceBackoffMax = time.Second, 5*time.Minute }()
	docker, dir := fakeDocker(t, "fail")
	n := newNode(options{taskNetwork: "eriphe-tasks", root: t.TempDir(), docker: docker})
	n.reconcileServices([]serviceAssignment{{Name: "bad", Spec: serviceSpec{Image: "bad", Transport: "stdio"}}})
	s := n.getService("bad")
	waitFor(t, func() bool { s.mu.Lock(); defer s.mu.Unlock(); return s.restarts >= 6 })
	s.mu.Lock()
	state, msg := s.state, s.lastErr
	s.mu.Unlock()
	if state != svcFailed && state != svcStarting || !strings.Contains(msg, "boom") {
		t.Errorf("state %s, error %q", state, msg)
	}
	n.reconcileServices(nil)
	<-s.done

	raw, _ := os.ReadFile(filepath.Join(dir, "runs"))
	var at []float64
	for _, l := range strings.Fields(string(raw)) {
		f, _ := strconv.ParseFloat(l, 64)
		at = append(at, f)
	}
	if len(at) < 6 {
		t.Fatalf("runs = %v", at)
	}
	first, later := at[2]-at[1], at[5]-at[4] // 80 ms, then capped at 160 ms
	if later < first*1.4 || later > 1.5 {
		t.Errorf("gaps %.3f then %.3f: no backoff, or no cap", first, later)
	}
}

// An http container that exits before it opens its port, or before the
// container engine gives it an address, fails with what it printed.
func TestServiceHTTPNeverUp(t *testing.T) {
	docker, _ := fakeDocker(t, "fail")
	apple, _ := fakeCLI(t, `case "$1" in
ls) printf '[]' ;;
run) echo boom >&2; exit 1 ;;
esac`)
	for _, tc := range []struct {
		opt  options
		want string
	}{
		{options{docker: docker, taskNetwork: "eriphe-tasks"}, "exit status 1: boom"},
		{options{docker: apple, engine: engineContainer}, "the container exited before it had an address: boom"},
	} {
		tc.opt.root = t.TempDir()
		n := newNode(tc.opt)
		s := &service{name: "web", n: n, spec: serviceSpec{Image: "web", Transport: "http", Port: 8080, Egress: true}}
		if err := s.runOnce(context.Background()); err == nil || err.Error() != tc.want {
			t.Errorf("%s: %v, want %q", tc.opt.engine, err, tc.want)
		}
	}
}

// ERIPHE_LIVE_SERVICE_IMAGE=mcp/time go test -run LiveService -v runs a real
// stdio MCP server image through the reconciler and the bridge, with the
// local Docker.
func TestLiveService(t *testing.T) {
	image := os.Getenv("ERIPHE_LIVE_SERVICE_IMAGE")
	if image == "" {
		t.Skip("set ERIPHE_LIVE_SERVICE_IMAGE, e.g. mcp/time")
	}
	root := t.TempDir()
	n := newNode(options{taskNetwork: "eriphe-tasks", root: root, hostRoot: root, docker: "docker", serviceMemory: "256m"})
	n.removeStrayServices()
	mux := http.NewServeMux()
	n.routes(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	n.reconcileServices([]serviceAssignment{{Name: "live", Spec: serviceSpec{Image: image, Transport: "stdio",
		User: "1000:1000", Volume: true, Egress: false}}})
	s := n.getService("live")
	t.Cleanup(func() { n.reconcileServices(nil); <-s.done })
	deadline := time.Now().Add(3 * time.Minute)
	for {
		s.mu.Lock()
		state, msg := s.state, s.lastErr
		s.mu.Unlock()
		if state == svcRunning {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("state %s: %s", state, msg)
		}
		time.Sleep(200 * time.Millisecond)
	}
	cs := serviceClient(t, srv.URL+"/v1/services/live/mcp")
	tools := toolNames(t, cs)
	t.Logf("server %+v, tools %v", cs.InitializeResult().ServerInfo, tools)
	if len(tools) == 0 {
		t.Fatal("no tools")
	}
}
