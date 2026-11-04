package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/netip"
	"os"
	"os/signal"
	"slices"
	"strings"
	"syscall"
	"time"
)

// remoteEngine is eriphe-node engine (engine_server.go), reached at
// -engine-url: the node never speaks to Docker itself. It says what it
// wants, the engine builds the containers; the networks and the cache
// volume are the engine's (loadEngineConfig).
type remoteEngine struct {
	o    *options
	nets *taskNets // the tasks' own networks, when the engine makes them
}

// remoteClient reaches the engine directly, never through a proxy of the
// environment.
var remoteClient = &http.Client{Transport: &http.Transport{Proxy: nil, MaxIdleConns: 8, IdleConnTimeout: time.Minute}}

func (e remoteEngine) traits() engineTraits { return engineTraits{ownNetworks: e.o.engineTaskNets} }

// beginTask has the engine make the task's own network, with the node on
// it (engine_tasknet.go), when it makes them; the tasks share one
// otherwise. The node's address on it is the one its task reaches.
func (e remoteEngine) beginTask(taskID string) (string, string, error) {
	if !e.o.engineTaskNets {
		return "", "", nil
	}
	var out wireTaskNetResult
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if err := e.call(ctx, http.MethodPost, "/networks/task", wireTaskNet{Task: taskID}, &out); err != nil {
		return "", "", err
	}
	subnet, err1 := netip.ParsePrefix(out.Subnet)
	node, err2 := netip.ParseAddr(out.Node)
	if err := errors.Join(err1, err2); err != nil || !subnet.Contains(node) {
		e.endTask(out.Network)
		return "", "", fmt.Errorf("eriphe-node engine: the task's network %s: subnet %q, node %q", out.Network, out.Subnet, out.Node)
	}
	if e.nets != nil {
		e.nets.add(out.Network, subnet, node)
	}
	return out.Network, out.Node, nil
}

// endTask has the engine remove the task's network once its container is
// gone.
func (e remoteEngine) endTask(network string) {
	task, ok := strings.CutPrefix(network, taskNetPrefix)
	if !ok {
		return
	}
	if e.nets != nil {
		e.nets.remove(network)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if err := e.call(ctx, http.MethodPost, "/networks/task/end", wireTaskNet{Task: task}, nil); err != nil {
		log.Printf("task %s: its network: %v", task, err)
	}
}

func (e remoteEngine) mountable(string) error { return nil }

func (e remoteEngine) url(path string) string {
	return strings.TrimRight(e.o.engineURL, "/") + engineAPI + path
}

// call makes a call to the engine: in as JSON, its answer into out.
func (e remoteEngine) call(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, e.url(path), body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := remoteClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return engineError(resp)
	}
	if out == nil {
		io.Copy(io.Discard, resp.Body)
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// engineError is why the engine refused a call.
func engineError(resp *http.Response) error {
	var e struct{ Error string }
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if json.Unmarshal(raw, &e) != nil || e.Error == "" {
		e.Error = strings.TrimSpace(resp.Status + " " + string(raw))
	}
	if resp.StatusCode == http.StatusConflict {
		return fmt.Errorf("eriphe-node engine: %s (%w)", e.Error, errEngineBusy)
	}
	return fmt.Errorf("eriphe-node engine: %s", e.Error)
}

// errEngineBusy is a container the engine cannot make yet: one of its
// folders is under a place another task can write (mountClash).
var errEngineBusy = errors.New("not yet")

// wireMounts are mounts as the engine takes them: at their paths for the
// Docker daemon.
func (e remoteEngine) wireMounts(ms []mount) []wireMount {
	out := make([]wireMount, 0, len(ms))
	for _, m := range ms {
		src := m.src
		if m.inRoot {
			src = dockerEngine{e.o}.hostPath(m.src)
		}
		out = append(out, wireMount{Src: src, Dst: m.dst, ReadOnly: m.readOnly})
	}
	return out
}

func (e remoteEngine) runTask(spec taskRunSpec) (*engineRun, error) {
	if spec.name != containerName(&Task{ID: spec.taskID}) {
		return nil, fmt.Errorf("a task's container is %s, not %s", containerName(&Task{ID: spec.taskID}), spec.name)
	}
	r, _, err := e.stream("/tasks", wireTask{Task: spec.taskID, Image: spec.image, SystemNetwork: spec.systemNetwork, Untrusted: spec.untrusted,
		Mounts: e.wireMounts(spec.mounts), Groups: spec.groups, Env: spec.env, Entrypoint: spec.entrypoint, Cmd: spec.cmd,
		Memory: e.o.taskMemory, CPUs: e.o.taskCPUs}, true, spec.stderr)
	return r, err
}

func (e remoteEngine) runService(spec serviceRunSpec) (*engineRun, string, error) {
	if spec.name != serviceContainer(spec.service) {
		return nil, "", fmt.Errorf("a service's container is %s, not %s", serviceContainer(spec.service), spec.name)
	}
	r, h, err := e.stream("/services", wireService{Service: spec.service, Image: spec.image, Args: spec.args, User: spec.user,
		Stdio: spec.stdio, Port: spec.port, Writable: spec.writable, Egress: spec.egress, Mounts: e.wireMounts(spec.mounts),
		Env: spec.env, Memory: e.o.serviceMemory}, spec.stdio, spec.stderr)
	if err != nil {
		return nil, "", err
	}
	return r, h.Get("Eriphe-Target"), nil
}

func (e remoteEngine) serviceAddress(context.Context, string, chan error) (string, error) {
	return "", errors.New("no address for the service")
}

// stream starts a container on a connection of its own (engine_stream.go):
// with pipe, its stdin and stdout are the run's; without, its stdout goes
// to stderr too, as startCLI's.
func (e remoteEngine) stream(path string, in any, pipe bool, stderr io.Writer) (*engineRun, http.Header, error) {
	b, err := json.Marshal(in)
	if err != nil {
		return nil, nil, err
	}
	req, err := http.NewRequest(http.MethodPost, e.url(path), bytes.NewReader(b))
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "eriphe-engine")
	resp, err := remoteClient.Do(req)
	if err != nil {
		return nil, nil, err
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		defer resp.Body.Close()
		return nil, nil, engineError(resp)
	}
	conn, ok := resp.Body.(io.ReadWriteCloser)
	if !ok {
		resp.Body.Close()
		return nil, nil, errors.New("eriphe-node engine: no stream")
	}
	if stderr == nil {
		stderr = io.Discard
	}
	stdout, pw := io.Pipe()
	done := make(chan struct{})
	var end *streamEnd
	var lost error
	go func() {
		defer close(done)
		defer conn.Close()
		defer pw.Close()
		for {
			kind, p, err := readFrame(conn)
			if err != nil {
				lost = err
				return
			}
			switch {
			case kind == frameStdout && pipe:
				pw.Write(p)
			case kind == frameStdout, kind == frameStderr:
				stderr.Write(p)
			case kind == frameEnd:
				end = &streamEnd{}
				if err := json.Unmarshal(p, end); err != nil {
					end.Exit, end.Error = -1, err.Error()
				}
				return
			}
		}
	}()
	r := &engineRun{wait: func() error {
		<-done
		switch {
		case end == nil:
			return fmt.Errorf("lost eriphe-node engine: %v", lost)
		case end.Error != "":
			return errors.New(end.Error)
		case end.Exit != 0:
			return fmt.Errorf("exit status %d", end.Exit)
		}
		return nil
	}}
	if pipe {
		r.stdin, r.stdout = &stdinWriter{f: &frameWriter{w: conn}}, stdout
	} else {
		go io.Copy(io.Discard, stdout)
	}
	return r, resp.Header, nil
}

// execSync runs a script in a task's container: the engine runs it in that
// task's only, and tells its exit code apart from its own failures.
func (e remoteEngine) execSync(ctx context.Context, container string, spec execSpec) (execResult, error) {
	task, ok := strings.CutPrefix(container, "eriphe-task-")
	if !ok {
		return execResult{exit: -1}, fmt.Errorf("%s is not a task's container", container)
	}
	var res wireExecResult
	err := e.call(ctx, http.MethodPost, "/exec", wireExec{Task: task, Env: spec.env, Setup: spec.setup, Script: spec.script,
		Name: spec.name, Args: spec.args, Max: spec.max}, &res)
	if ctx.Err() != nil {
		return execResult{exit: -1}, errTimedOut
	}
	if err != nil {
		return execResult{exit: -1}, fmt.Errorf("it did not run in the container (%v)", err)
	}
	return execResult{res.Output, res.Exit}, nil
}

func (e remoteEngine) stop(ctx context.Context, container string) {
	if err := e.call(ctx, http.MethodPost, "/stop", wireContainer{Container: container}, nil); err != nil {
		log.Printf("stop %s: %v", container, err)
	}
}

func (e remoteEngine) removeStray(label string) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if err := e.call(ctx, http.MethodPost, "/remove-stray", wireLabel{Label: label}, nil); err != nil {
		log.Printf("remove the containers %s: %v", label, err)
	}
}

// runHelper runs a folder helper (helper.go), the only helper the engine
// runs: the node's own image, which the engine knows.
func (e remoteEngine) runHelper(ctx context.Context, spec helperRunSpec) ([]byte, []byte, error) {
	op, ok := strings.CutPrefix(spec.label, "eriphe.helper=")
	if !ok || spec.entrypoint != nodeBinary || !slices.Equal(spec.args, []string{"folder-helper"}) {
		return nil, nil, errors.New("eriphe-node engine runs folder helpers only")
	}
	var res wireHelperResult
	if err := e.call(ctx, http.MethodPost, "/helpers", wireHelper{Op: op, Mounts: e.wireMounts(spec.mounts), Stdin: spec.stdin, User: spec.user}, &res); err != nil {
		return nil, nil, err
	}
	if res.Exit != 0 {
		return res.Stdout, res.Stderr, fmt.Errorf("exit status %d", res.Exit)
	}
	return res.Stdout, res.Stderr, nil
}

func (e remoteEngine) wake(ctx context.Context, w wakeInfo) error {
	return e.call(ctx, http.MethodPost, "/wake", w, nil)
}

func (e remoteEngine) pull(ctx context.Context, image string) error {
	return e.call(ctx, http.MethodPost, "/pull", wireImage{Image: image}, nil)
}

func (e remoteEngine) info(ctx context.Context) engineInfo {
	var in wireInfo
	if err := e.call(ctx, http.MethodGet, "/info", nil, &in); err != nil {
		return engineInfo{images: []string{}}
	}
	if in.Images == nil {
		in.Images = []string{}
	}
	return engineInfo{docker: in.Docker, version: in.Docker, images: in.Images}
}

func (e remoteEngine) ensureNetwork() error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	return e.call(ctx, http.MethodPost, "/networks/ensure", nil, nil)
}

func (e remoteEngine) taskSubnets() ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var out wireSubnets
	err := e.call(ctx, http.MethodGet, "/networks/tasks", nil, &out)
	return out.Subnets, err
}

// loadEngineConfig takes the engine's networks and cache volume: the
// operator's, not the node's flags. The engine may still be starting: it
// is asked for a while.
func loadEngineConfig(o *options, tries int, every time.Duration) error {
	var c wireConfig
	var err error
	for i := 0; i < tries; i++ {
		if i > 0 {
			time.Sleep(every)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		err = remoteEngine{o: o}.call(ctx, http.MethodGet, "/config", nil, &c)
		cancel()
		if err == nil {
			o.taskNetwork, o.network, o.serviceNetwork, o.cacheVolume = c.TaskNetwork, c.Network, c.ServiceNetwork, c.CacheVolume
			o.engineTaskNets = c.TaskNetworks
			if c.Version != version {
				log.Printf("warning: eriphe-node engine is %s, this node %s", c.Version, version)
			}
			return nil
		}
	}
	return fmt.Errorf("eriphe-node engine at %s: %v", o.engineURL, err)
}

// withoutEngine: a node that cannot reach its container engine at start (an
// install whose layout is older than this build, such as a Linux one still
// on the socket proxy of before eriphe-node engine) connects to eriphe-core
// all the same, with no runtime to offer, so that it says so and its
// updater, which waits for it to connect, can bring the layout that gives
// it one. It looks for the engine again, and starts again once it is there.
func (n *node) withoutEngine(s *state, why error) {
	log.Printf("no container engine here: %v; connected all the same, with no task to run, until the install brings one (install/README.md, The machines update themselves)", why)
	n.noEngine = true // its tasks wait (sync.go)
	api := http.NewServeMux()
	n.routes(api)
	go serve(s, api)
	go n.sync(s)
	untilTrue(n.engineBack, engineRetry)
	log.Print("the container engine is here now: eriphe-node starts again")
	os.Exit(1)
}

// untilTrue returns once ok, asked every so often.
func untilTrue(ok func() bool, every time.Duration) {
	for !ok() {
		time.Sleep(every)
	}
}

// engineRetry is how often a node without its engine looks for it again.
var engineRetry = 30 * time.Second

// engineBack: the engine answers now, as the node needs it at start.
func (n *node) engineBack() bool {
	if n.opt.engineURL != "" {
		o := n.opt
		return loadEngineConfig(&o, 1, 0) == nil
	}
	return n.engine().ensureNetwork() == nil
}

// proxyGone stands in the container of the socket proxy of an install from
// before eriphe-node engine: its updater makes it again from this image,
// with the node's, before the new layout that replaces it applies. It runs
// nothing, so that it neither starts a node of its own nor restarts in a
// loop, and stops when told.
func proxyGone() {
	log.Print("eriphe-node docker-proxy is gone: eriphe-node engine replaces it, which this install's next layout brings (install/README.md, The machines update themselves)")
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGTERM, os.Interrupt)
	<-stop
}
