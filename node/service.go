package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Services are local MCP servers eriphe-core assigns to this node, like a
// kubelet's long-lived pods: the node keeps each one's container running,
// restarting it with a backoff, and serves it as a streamable HTTP MCP
// endpoint through the tunnel, /v1/services/{name}/mcp (service_mcp.go).
// Their containers carry the label eriphe.service, task containers
// eriphe.task: neither cleanup touches the other's.

// serviceAssignment is one service as eriphe-core wants it, its secrets
// included (in memory only).
type serviceAssignment struct {
	Name string      `json:"name"`
	Hash string      `json:"hash"`
	Spec serviceSpec `json:"spec"`
}

type serviceSpec struct {
	Image     string            `json:"image"`
	Transport string            `json:"transport"` // stdio or http
	Port      int               `json:"port,omitempty"`
	Path      string            `json:"path,omitempty"`
	Args      []string          `json:"args,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
	User      string            `json:"user"`
	Writable  bool              `json:"writable,omitempty"`
	Volume    bool              `json:"volume,omitempty"`
	Egress    bool              `json:"egress"`
}

// Service states, as reported.
const (
	svcStarting = "starting"
	svcRunning  = "running"
	svcFailed   = "failed" // it exited or never came up; it restarts after a backoff
	svcStopped  = "stopped"
)

// Restart backoff: doubled from the first to the last, back to the first
// once a container has run for a while. There is no maximum of restarts.
var (
	serviceBackoffMin = time.Second
	serviceBackoffMax = 5 * time.Minute
	serviceStable     = 2 * time.Minute
	serviceStartup    = 5 * time.Minute // to pull the image and answer initialize, or open its port
)

type service struct {
	name string
	spec serviceSpec
	key  string // hash of the spec with its secrets: a new one restarts the container
	n    *node

	cancel context.CancelFunc
	done   chan struct{}

	mu       sync.Mutex
	state    string
	restarts int
	lastErr  string
	reported serviceReport
	target   string // http: where the container answers, once it does

	bridge *bridge // stdio: outlives its containers, so sessions survive restarts
}

type serviceReport struct {
	State    string `json:"state"`
	Restarts int    `json:"restarts"`
	Error    string `json:"error,omitempty"`
}

func serviceKey(spec serviceSpec) string {
	raw, _ := json.Marshal(spec)
	h := sha256.Sum256(raw)
	return hex.EncodeToString(h[:])
}

func serviceContainer(name string) string { return "eriphe-svc-" + name }

func (n *node) serviceDir(name string) string { return filepath.Join(n.opt.root, "services", name) }

func (n *node) getService(name string) *service {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.services[name]
}

// reconcileServices starts, restarts or stops services to match list.
func (n *node) reconcileServices(list []serviceAssignment) {
	wanted := map[string]bool{}
	for _, a := range list {
		if !validServiceName(a.Name) {
			log.Printf("service %q: bad name, ignored", a.Name)
			continue
		}
		wanted[a.Name] = true
		key := serviceKey(a.Spec)
		s := n.getService(a.Name)
		if s != nil && s.key == key {
			continue
		}
		// A new spec, or a new secret: the new container starts once the old one is gone.
		n.startService(a, key, s)
	}
	n.mu.Lock()
	var gone []*service
	for name, s := range n.services {
		if !wanted[name] {
			gone = append(gone, s)
			delete(n.services, name)
			n.retired[name] = s // until its container is gone, should it come back
		}
	}
	n.mu.Unlock()
	for _, s := range gone {
		go func() {
			s.stop()
			n.mu.Lock()
			if n.retired[s.name] == s {
				delete(n.retired, s.name)
			}
			n.mu.Unlock()
		}()
	}
}

func validServiceName(s string) bool {
	if s == "" || len(s) > 32 || s[0] == '-' {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
			return false
		}
	}
	return true
}

// startService starts a service, after prev (the same service, an older
// spec) is stopped. A stdio service keeps prev's bridge, and its sessions.
func (n *node) startService(a serviceAssignment, key string, prev *service) {
	ctx, cancel := context.WithCancel(context.Background())
	s := &service{name: a.Name, spec: a.Spec, key: key, n: n, cancel: cancel, done: make(chan struct{}),
		state: svcStarting}
	n.mu.Lock()
	if prev == nil {
		prev = n.retired[a.Name]
	}
	n.mu.Unlock()
	if prev != nil {
		s.restarts = prev.restarts
		if prev.spec.Transport == a.Spec.Transport {
			s.bridge = prev.bridge
		}
	}
	if a.Spec.Transport == "stdio" && s.bridge == nil {
		s.bridge = newBridge(a.Name)
	}
	n.mu.Lock()
	n.services[a.Name] = s
	n.mu.Unlock()
	go func() {
		if prev != nil {
			prev.stop()
		}
		log.Printf("service %s: starting %s", a.Name, a.Spec.Image)
		s.loop(ctx)
	}()
	n.poke()
}

// stop stops the service's container and waits for it.
func (s *service) stop() {
	s.cancel()
	<-s.done
}

func (s *service) set(state, msg string) {
	s.mu.Lock()
	s.state = state
	if msg != "" || state == svcRunning {
		s.lastErr = msg
	}
	if state == svcFailed {
		s.restarts++
	}
	s.mu.Unlock()
	s.n.poke()
}

// loop keeps the container running until the service is stopped.
func (s *service) loop(ctx context.Context) {
	defer close(s.done)
	backoff := serviceBackoffMin
	for {
		if ctx.Err() != nil { // stopped before it started
			s.set(svcStopped, "")
			return
		}
		started := time.Now()
		err := s.runOnce(ctx)
		if ctx.Err() != nil {
			s.set(svcStopped, "")
			return
		}
		if err == nil {
			err = errors.New("the container exited")
		}
		if time.Since(started) > serviceStable {
			backoff = serviceBackoffMin
		}
		log.Printf("service %s: %v; restarting in %s", s.name, err, backoff)
		s.set(svcFailed, err.Error())
		select {
		case <-ctx.Done():
			s.set(svcStopped, "")
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, serviceBackoffMax)
		s.set(svcStarting, "")
	}
}

// runOnce runs one container of the service until it exits or ctx ends.
func (s *service) runOnce(ctx context.Context) error {
	args, env, target, err := s.n.serviceArgs(s.name, s.spec)
	if err != nil {
		return err
	}
	r, err := s.start(args, env)
	if err != nil {
		return err
	}
	// Stopping: docker stop, the CLI going away does not stop a container.
	stopped := make(chan struct{})
	defer close(stopped)
	go func() {
		select {
		case <-ctx.Done():
			s.n.stopServiceContainer(s.name)
		case <-stopped:
		}
	}()
	if s.spec.Transport == "stdio" {
		return s.serveStdio(ctx, r)
	}
	return s.serveHTTP(ctx, r, target)
}

// serviceRun is one container of a service, started: a stdio one with its
// input and output, what it prints on stderr (and stdout, over http).
type serviceRun struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	stderr *tailBuffer
}

// start starts the container's command.
func (s *service) start(args, env []string) (*serviceRun, error) {
	r := &serviceRun{cmd: exec.Command(s.n.opt.docker, args...), stderr: &tailBuffer{}}
	r.cmd.Env = env
	r.cmd.Stderr = r.stderr
	var err error
	if s.spec.Transport == "stdio" {
		if r.stdin, err = r.cmd.StdinPipe(); err != nil {
			return nil, err
		}
		if r.stdout, err = r.cmd.StdoutPipe(); err != nil {
			return nil, err
		}
	} else {
		r.cmd.Stdout = r.stderr
	}
	if err := r.cmd.Start(); err != nil {
		return nil, err
	}
	return r, nil
}

// fail adds to err the end of what the container printed.
func (r *serviceRun) fail(err error) error {
	if msg := strings.TrimSpace(tail(r.stderr.String(), 1000)); msg != "" {
		err = fmt.Errorf("%w: %s", err, msg)
	}
	return err
}

// serveStdio bridges the MCP sessions to a stdio container until either
// ends.
func (s *service) serveStdio(ctx context.Context, r *serviceRun) error {
	err := s.bridge.serve(ctx, r.stdout, r.stdin, func() { s.set(svcRunning, "") })
	// The connection is over: make sure the container is too.
	s.n.stopServiceContainer(s.name)
	werr := r.cmd.Wait()
	if ctx.Err() != nil {
		return nil
	}
	if werr != nil {
		return r.fail(werr)
	}
	return r.fail(err)
}

// serveHTTP waits for an http container to open its port, then serves its
// address until it exits.
func (s *service) serveHTTP(ctx context.Context, r *serviceRun, target string) error {
	exited := make(chan error, 1)
	go func() { exited <- r.cmd.Wait() }()
	if target == "" && s.n.apple() {
		// The container engine: its address, on its own network.
		ip, err := s.n.appleAddress(ctx, serviceContainer(s.name), exited)
		if err != nil {
			if s.giveUp(ctx, exited) {
				return nil
			}
			return r.fail(err)
		}
		target = net.JoinHostPort(ip, strconv.Itoa(s.spec.Port))
	}
	if err := waitPort(ctx, target, exited); err != nil {
		if s.giveUp(ctx, exited) {
			return nil
		}
		select {
		case werr := <-exited:
			if werr != nil {
				err = werr
			}
		case <-time.After(15 * time.Second):
		}
		return r.fail(err)
	}
	s.mu.Lock()
	s.target = "http://" + target + s.spec.Path
	s.mu.Unlock()
	s.set(svcRunning, "")
	err := <-exited
	s.mu.Lock()
	s.target = ""
	s.mu.Unlock()
	if ctx.Err() != nil {
		return nil
	}
	return r.fail(err)
}

// giveUp stops a container that did not come up; true when the service is
// being stopped, once the container is gone.
func (s *service) giveUp(ctx context.Context, exited chan error) bool {
	s.n.stopServiceContainer(s.name)
	if ctx.Err() != nil {
		<-exited
		return true
	}
	return false
}

// waitPort waits until addr answers HTTP (any status: a published port
// accepts connections before the container listens), the container exits
// or the startup time is over.
func waitPort(ctx context.Context, addr string, exited chan error) error {
	deadline := time.After(serviceStartup)
	probe := &http.Client{Timeout: 2 * time.Second}
	for {
		resp, err := probe.Get("http://" + addr + "/")
		if err == nil {
			resp.Body.Close()
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-exited:
			exited <- err
			return errors.New("the container exited before opening its port")
		case <-deadline:
			return fmt.Errorf("nothing listens on %s after %s", addr, serviceStartup)
		case <-time.After(300 * time.Millisecond):
		}
	}
}

func (n *node) stopServiceContainer(name string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	exec.CommandContext(ctx, n.opt.docker, "stop", "-t", "10", serviceContainer(name)).Run()
}

// serviceArgs is the docker run command of a service, the environment of
// that command (secrets are passed by name, -e NAME, never on a command
// line) and, for http, the address the node reaches the container on.
func (n *node) serviceArgs(name string, spec serviceSpec) (args, env []string, target string, err error) {
	user := spec.User
	if user == "" {
		user = fmt.Sprintf("%d:%d", taskUID, taskGID)
	}
	uid, gid, err := parseUser(user)
	if err != nil {
		return nil, nil, "", err
	}
	if spec.Image == "" || strings.HasPrefix(spec.Image, "-") {
		return nil, nil, "", fmt.Errorf("bad image %q", spec.Image)
	}
	if n.apple() {
		return n.appleServiceArgs(name, spec, user, uid, gid)
	}
	args = []string{"run", "--rm", "--name", serviceContainer(name), "--label", "eriphe.service=" + name}
	if spec.Transport == "stdio" {
		args = append(args, "-i")
	}
	args = append(args, "--user", user, "--cap-drop", "ALL", "--security-opt", "no-new-privileges",
		"--pids-limit", "256", "--tmpfs", "/tmp:rw,exec,nosuid,nodev,size=256m")
	if !spec.Writable {
		args = append(args, "--read-only")
	}
	if n.opt.serviceMemory != "" {
		args = append(args, "--memory", n.opt.serviceMemory)
	}

	// The network: none without egress, unless an http service needs one to
	// be reached, then the node's internal network. The node reaches an http
	// container by name on a network it shares (the NAS), or else on a
	// loopback port it publishes (a node outside Docker: WSL, a Mac).
	network := n.opt.network
	switch {
	case !spec.Egress && spec.Transport == "stdio":
		network = "none"
	case !spec.Egress:
		if n.opt.serviceNetwork == "" {
			return nil, nil, "", errors.New("an http service without egress needs the node's -service-network (an internal Docker network the node is on)")
		}
		network = n.opt.serviceNetwork
	}
	// Never the tasks' network: they would reach the service directly.
	if spec.Transport == "http" && dockerNetwork(network) == dockerNetwork(n.opt.taskNetwork) {
		return nil, nil, "", errSharedBridge
	}
	if network != "" {
		args = append(args, "--network", network)
	} else {
		args = append(args, "--add-host", "host.docker.internal:"+n.hostGateway(""))
	}
	if spec.Transport == "http" {
		if spec.Port < 1 || spec.Port > 65535 {
			return nil, nil, "", errors.New("an http service needs its port")
		}
		if network != "" {
			target = net.JoinHostPort(serviceContainer(name), strconv.Itoa(spec.Port))
		} else {
			port, err := freePort()
			if err != nil {
				return nil, nil, "", err
			}
			args = append(args, "-p", fmt.Sprintf("127.0.0.1:%d:%d", port, spec.Port))
			target = net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
		}
	}

	if spec.Volume {
		dir := n.serviceDir(name)
		mount, err := n.bindMount(dir, "/data")
		if err != nil {
			return nil, nil, "", err
		}
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, nil, "", err
		}
		os.Chown(dir, uid, gid)
		args = append(args, mount...)
	}
	env = os.Environ()
	for k, v := range spec.Env {
		args = append(args, "-e", k)
		env = append(env, k+"="+v)
	}
	args = append(args, spec.Image)
	args = append(args, spec.Args...)
	return args, env, target, nil
}

func parseUser(s string) (uid, gid int, err error) {
	u, g, hasGroup := strings.Cut(s, ":")
	uid, err = strconv.Atoi(u)
	if err == nil && hasGroup {
		gid, err = strconv.Atoi(g)
	} else {
		gid = uid
	}
	if err != nil || uid <= 0 || gid < 0 {
		return 0, 0, fmt.Errorf("bad service user %q: uid:gid in numbers, never root", s)
	}
	return uid, gid, nil
}

// freePort is a loopback port free now, for a published http service.
func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// removeStrayServices removes service containers left by a previous run of
// the node; the services start again from their assignments.
func (n *node) removeStrayServices() {
	n.removeLabelled("eriphe.service")
}

// removeLabelled removes the containers carrying a label, whatever its value.
func (n *node) removeLabelled(label string) {
	if n.apple() {
		n.appleRemoveLabelled(label)
		return
	}
	out, err := exec.Command(n.opt.docker, "ps", "-aq", "--filter", "label="+label).Output()
	if err != nil || len(strings.TrimSpace(string(out))) == 0 {
		return
	}
	args := append([]string{"rm", "-f"}, strings.Fields(string(out))...)
	exec.Command(n.opt.docker, args...).Run()
}

// reportServices tells eriphe-core what changed in each service.
func (n *node) reportServices(c *coreClient) {
	n.mu.Lock()
	list := make([]*service, 0, len(n.services))
	for _, s := range n.services {
		list = append(list, s)
	}
	n.mu.Unlock()
	for _, s := range list {
		s.mu.Lock()
		r := serviceReport{State: s.state, Restarts: s.restarts, Error: s.lastErr}
		same := r == s.reported
		s.mu.Unlock()
		if same {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		err := c.do(ctx, http.MethodPost, "/v1/node/services/"+s.name+"/report", r, nil)
		cancel()
		if err != nil && !errors.Is(err, errUnknownTask) {
			log.Printf("report service %s: %v", s.name, err)
			continue
		}
		s.mu.Lock()
		s.reported = r
		s.mu.Unlock()
	}
}
