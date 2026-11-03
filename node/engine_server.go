package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

// eriphe-node engine: the node's engine, in a container of its own next to
// it, the only one that holds the Docker socket. It serves the engine's
// verbs (engine.go) to the node over a versioned API (engineAPI), the way
// Kubernetes' CRI serves the kubelet, and builds each container itself
// from a specification that says what is wanted, never how: a task, a
// service, a folder helper, a wake. Nothing in a request can express a
// privilege, a capability, a security option, a device, a host namespace,
// or a network or a folder of the node's choice: the hardening is the
// engine's (the same as the docker CLI engine's, engine_docker.go), and
// what a container may get is the operator's, in the engine's flags, the
// way Nomad's Docker driver has it (allow_privileged, volumes): the places
// folders may be mounted from, the networks, the node's own image (the one
// that may run on the host's network, to wake a machine, and as a folder
// helper). The node is not trusted: a compromised one gets its own tasks,
// nothing more.
//
// Folders are checked on their real path, resolved in the host's / when
// the engine sees it read-only (-host-root), and the resolved path is what
// the daemon mounts. What it cannot prevent is a race: a directory
// replaced by a symlink between its check and the daemon's mount.

// engineAPI is the version of the API, the prefix of its paths: a node and
// an engine that do not speak the same say so.
const engineAPI = "/v1"

// engineServer is eriphe-node engine.
type engineServer struct {
	d      *dockerAPI
	policy *dockerPolicy // bindFrom, bindFile, resolve: where folders may come from
	// nodeImage is the node's own image: wake-on-LAN and folder helpers.
	nodeImage string
	// The networks: the tasks', eriphe-core's and the services' with egress
	// (system), the http services' without egress; and the tasks' cache.
	taskNetwork, network, serviceNetwork string
	cacheVolume                          string
	allowFrom                            string // the only host answered, by name
	lookup                               func(string) ([]string, error)
}

// The requests, as the node sends them: a field not here is refused.
type (
	wireMount struct {
		Src      string `json:"src"`
		Dst      string `json:"dst"`
		ReadOnly bool   `json:"read_only,omitempty"`
	}
	wireTask struct {
		Task          string            `json:"task"`
		Image         string            `json:"image"`
		SystemNetwork bool              `json:"system_network,omitempty"`
		Mounts        []wireMount       `json:"mounts,omitempty"`
		Groups        []int             `json:"groups,omitempty"`
		Env           map[string]string `json:"env,omitempty"`
		Entrypoint    string            `json:"entrypoint"`
		Cmd           []string          `json:"cmd,omitempty"`
		Memory        string            `json:"memory,omitempty"`
		CPUs          string            `json:"cpus,omitempty"`
	}
	wireService struct {
		Service  string            `json:"service"`
		Image    string            `json:"image"`
		Args     []string          `json:"args,omitempty"`
		User     string            `json:"user"`
		Stdio    bool              `json:"stdio,omitempty"`
		Port     int               `json:"port,omitempty"`
		Writable bool              `json:"writable,omitempty"`
		Egress   bool              `json:"egress,omitempty"`
		Mounts   []wireMount       `json:"mounts,omitempty"`
		Env      map[string]string `json:"env,omitempty"`
		Memory   string            `json:"memory,omitempty"`
	}
	wireHelper struct {
		Op     string      `json:"op"`
		Mounts []wireMount `json:"mounts,omitempty"`
		Stdin  []byte      `json:"stdin,omitempty"`
		User   string      `json:"user,omitempty"`
	}
	wireHelperResult struct {
		Stdout []byte `json:"stdout"`
		Stderr []byte `json:"stderr"`
		Exit   int    `json:"exit"`
	}
	wireExec struct {
		Task   string   `json:"task"`
		Env    []string `json:"env,omitempty"`
		Setup  string   `json:"setup,omitempty"`
		Script string   `json:"script"`
		Name   string   `json:"name"`
		Args   []string `json:"args,omitempty"`
		Max    int      `json:"max"`
	}
	wireExecResult struct {
		Output string `json:"output"`
		Exit   int    `json:"exit"`
	}
	wireContainer struct {
		Container string `json:"container"`
	}
	wireLabel struct {
		Label string `json:"label"`
	}
	wireImage struct {
		Image string `json:"image"`
	}
	wireInfo struct {
		Docker string   `json:"docker"`
		Images []string `json:"images"`
	}
	wireSubnets struct {
		Subnets []string `json:"subnets"`
	}
	// wireConfig is the engine's settings the node goes by.
	wireConfig struct {
		Version        string `json:"version"`
		TaskNetwork    string `json:"task_network"`
		Network        string `json:"network"`
		ServiceNetwork string `json:"service_network"`
		CacheVolume    string `json:"cache_volume"`
	}
)

const (
	taskPrefix    = "eriphe-task-"
	servicePrefix = "eriphe-svc-"
	nodeBinary    = "/usr/local/bin/eriphe-node"
)

var (
	engineID  = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}$`)
	helperOp  = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
	imageName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_./:@-]{0,254}$`)
	envName   = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)
)

func (s *engineServer) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+engineAPI+"/config", s.handleConfig)
	mux.HandleFunc("POST "+engineAPI+"/tasks", s.handleTask)
	mux.HandleFunc("POST "+engineAPI+"/exec", s.handleExec)
	mux.HandleFunc("POST "+engineAPI+"/stop", s.handleStop)
	mux.HandleFunc("POST "+engineAPI+"/services", s.handleService)
	mux.HandleFunc("POST "+engineAPI+"/remove-stray", s.handleRemoveStray)
	mux.HandleFunc("POST "+engineAPI+"/helpers", s.handleHelper)
	mux.HandleFunc("POST "+engineAPI+"/wake", s.handleWake)
	mux.HandleFunc("POST "+engineAPI+"/pull", s.handlePull)
	mux.HandleFunc("GET "+engineAPI+"/info", s.handleInfo)
	mux.HandleFunc("POST "+engineAPI+"/networks/ensure", s.handleEnsureNetwork)
	mux.HandleFunc("GET "+engineAPI+"/networks/tasks", s.handleTaskSubnets)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		s.refuse(w, r, http.StatusNotFound, fmt.Errorf("no %s %s in this engine's API %s: the node and eriphe-node engine must be of the same version", r.Method, r.URL.Path, engineAPI))
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Eriphe-Engine-API", strings.TrimPrefix(engineAPI, "/"))
		if err := checkPeer(r, s.allowFrom, s.lookup); err != nil {
			s.refuse(w, r, http.StatusForbidden, err)
			return
		}
		mux.ServeHTTP(w, r)
	})
}

// refuse answers why a request is not served, and logs it: never its body,
// which may carry a task's secrets.
func (s *engineServer) refuse(w http.ResponseWriter, r *http.Request, status int, err error) {
	log.Printf("refused %s %s: %v", r.Method, r.URL.Path, err)
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

// decode reads a request's body into v: a field v does not have is an
// error, so that nothing the engine does not know is silently dropped.
func decode(w http.ResponseWriter, r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("bad request: %v", err)
	}
	return nil
}

func (s *engineServer) handleConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, wireConfig{Version: version, TaskNetwork: s.taskNetwork, Network: s.network,
		ServiceNetwork: s.serviceNetwork, CacheVolume: s.cacheVolume})
}

// mounts checks a container's folders against the policy: each from an
// allowed place, read-only where that place is, resolved.
func (s *engineServer) mounts(ms []wireMount) ([]apiMount, error) {
	out := make([]apiMount, 0, len(ms))
	for _, m := range ms {
		if !filepath.IsAbs(m.Dst) || filepath.Clean(m.Dst) != m.Dst || m.Dst == "/" {
			return nil, fmt.Errorf("mount target %q is not an absolute path", m.Dst)
		}
		real, ro, err := s.policy.bindSource(m.Src)
		if err != nil {
			return nil, err
		}
		if ro && !m.ReadOnly {
			return nil, fmt.Errorf("%s is shared read-only", m.Src)
		}
		out = append(out, apiMount{Type: "bind", Source: real, Target: m.Dst, ReadOnly: m.ReadOnly})
	}
	return out, nil
}

// envList is an environment as the API takes it, checked.
func envList(env map[string]string) ([]string, error) {
	out := make([]string, 0, len(env))
	for k, v := range env {
		if !envName.MatchString(k) || strings.ContainsRune(v, 0) {
			return nil, fmt.Errorf("bad environment variable %q", k)
		}
		out = append(out, k+"="+v)
	}
	sort.Strings(out)
	return out, nil
}

// checkImage refuses what is not an image's name.
func checkImage(image string) error {
	if !imageName.MatchString(image) {
		return fmt.Errorf("bad image %q", image)
	}
	return nil
}

// checkUser allows uid:gid, numbers, never root.
func checkUser(user string) error {
	u, g, ok := strings.Cut(user, ":")
	uid, uerr := strconv.Atoi(u)
	_, gerr := strconv.Atoi(g)
	if !ok || uerr != nil || gerr != nil || uid <= 0 {
		return fmt.Errorf("user %q: uid:gid, not root", user)
	}
	return nil
}

// memory and nanoCPUs read the node's limits.
func memory(s string) (int64, error) {
	if s == "" {
		return 0, nil
	}
	return parseBytes(s)
}

func nanoCPUs(s string) (int64, error) {
	if s == "" {
		return 0, nil
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || f <= 0 || f > 4096 {
		return 0, fmt.Errorf("cpus %q: a number such as 4 or 1.5", s)
	}
	return int64(f * 1e9), nil
}

// hostGateway is the address containers on a network reach the host on
// (engine_docker.go's).
func (s *engineServer) hostGateway(ctx context.Context, network string) string {
	if n, err := s.d.network(ctx, dockerNetwork(network)); err == nil {
		for _, c := range n.IPAM.Config {
			if net.ParseIP(c.Gateway) != nil {
				return c.Gateway
			}
		}
	}
	return "host-gateway"
}

// taskContainer is a task's container (engine_docker.go's taskArgs, and
// its hardening), and the network it joins too, if any.
func (s *engineServer) taskContainer(ctx context.Context, t wireTask) (apiContainer, string, error) {
	if !engineID.MatchString(t.Task) {
		return apiContainer{}, "", fmt.Errorf("bad task %q", t.Task)
	}
	if err := checkImage(t.Image); err != nil {
		return apiContainer{}, "", err
	}
	mounts, err := s.mounts(t.Mounts)
	if err != nil {
		return apiContainer{}, "", err
	}
	env, err := envList(t.Env)
	if err != nil {
		return apiContainer{}, "", err
	}
	mem, err := memory(t.Memory)
	if err != nil {
		return apiContainer{}, "", err
	}
	cpus, err := nanoCPUs(t.CPUs)
	if err != nil {
		return apiContainer{}, "", err
	}
	extra := ""
	if t.SystemNetwork {
		if s.network == "" {
			return apiContainer{}, "", errors.New("this engine has no system network (-network)")
		}
		extra = s.network
	}
	c := apiContainer{Image: t.Image, User: "0:0", Env: env, Cmd: t.Cmd, Labels: map[string]string{"eriphe.task": t.Task},
		AttachStdin: true, AttachStdout: true, AttachStderr: true, OpenStdin: true, StdinOnce: true,
		// An image's own health check is for its standalone use, not for tasks.
		Healthcheck: &apiHealthcheck{Test: []string{"NONE"}},
		HostConfig: apiHostConfig{Mounts: mounts, NetworkMode: dockerNetwork(s.taskNetwork), AutoRemove: true,
			CapDrop: []string{"ALL"}, CapAdd: []string{"CHOWN", "DAC_OVERRIDE", "FOWNER"}, SecurityOpt: []string{"no-new-privileges"},
			PidsLimit: 1024, Tmpfs: map[string]string{"/tmp": "rw,exec,nosuid,nodev,size=2g"},
			ExtraHosts: []string{"host.docker.internal:" + s.hostGateway(ctx, s.taskNetwork)},
			Memory:     mem, NanoCpus: cpus}}
	if t.Entrypoint != "" {
		c.Entrypoint = []string{t.Entrypoint}
	}
	if s.cacheVolume != "" {
		c.HostConfig.Binds = []string{s.cacheVolume + ":/cache"}
	}
	for _, g := range t.Groups {
		if g < 0 {
			return apiContainer{}, "", fmt.Errorf("bad group %d", g)
		}
		c.HostConfig.GroupAdd = append(c.HostConfig.GroupAdd, strconv.Itoa(g))
	}
	return c, extra, nil
}

func (s *engineServer) handleTask(w http.ResponseWriter, r *http.Request) {
	var t wireTask
	if err := decode(w, r, &t); err != nil {
		s.refuse(w, r, http.StatusBadRequest, err)
		return
	}
	c, extra, err := s.taskContainer(r.Context(), t)
	if err != nil {
		s.refuse(w, r, http.StatusForbidden, err)
		return
	}
	s.serveStream(w, r, taskPrefix+t.Task, c, extra, nil)
}

// serviceContainer is a service's container (engine_docker.go's
// serviceArgs), and, for http, the address the node reaches it on.
func (s *engineServer) serviceContainer(ctx context.Context, sv wireService) (apiContainer, string, error) {
	if !engineID.MatchString(sv.Service) {
		return apiContainer{}, "", fmt.Errorf("bad service %q", sv.Service)
	}
	if err := checkImage(sv.Image); err != nil {
		return apiContainer{}, "", err
	}
	if err := checkUser(sv.User); err != nil {
		return apiContainer{}, "", err
	}
	if !sv.Stdio && (sv.Port < 1 || sv.Port > 65535) {
		return apiContainer{}, "", errors.New("an http service needs its port")
	}
	mounts, err := s.mounts(sv.Mounts)
	if err != nil {
		return apiContainer{}, "", err
	}
	env, err := envList(sv.Env)
	if err != nil {
		return apiContainer{}, "", err
	}
	mem, err := memory(sv.Memory)
	if err != nil {
		return apiContainer{}, "", err
	}
	name := servicePrefix + sv.Service
	c := apiContainer{Image: sv.Image, User: sv.User, Env: env, Cmd: sv.Args, Labels: map[string]string{"eriphe.service": sv.Service},
		AttachStdin: sv.Stdio, OpenStdin: sv.Stdio, StdinOnce: sv.Stdio, AttachStdout: true, AttachStderr: true,
		HostConfig: apiHostConfig{Mounts: mounts, AutoRemove: true, CapDrop: []string{"ALL"}, SecurityOpt: []string{"no-new-privileges"},
			PidsLimit: 256, Tmpfs: map[string]string{"/tmp": "rw,exec,nosuid,nodev,size=256m"}, ReadonlyRootfs: !sv.Writable, Memory: mem}}

	// The network: none without egress, unless an http service needs one to
	// be reached, then the internal service network; never the tasks'.
	network := s.network
	switch {
	case !sv.Egress && sv.Stdio:
		network = "none"
	case !sv.Egress:
		if s.serviceNetwork == "" {
			return apiContainer{}, "", errors.New("an http service without egress needs the engine's -service-network")
		}
		network = s.serviceNetwork
	}
	if !sv.Stdio && dockerNetwork(network) == dockerNetwork(s.taskNetwork) {
		return apiContainer{}, "", errSharedBridge
	}
	target := ""
	if network != "" {
		c.HostConfig.NetworkMode = network
	} else {
		c.HostConfig.ExtraHosts = []string{"host.docker.internal:" + s.hostGateway(ctx, "")}
	}
	if !sv.Stdio {
		if network != "" {
			target = net.JoinHostPort(name, strconv.Itoa(sv.Port))
		} else {
			port, err := freePort()
			if err != nil {
				return apiContainer{}, "", err
			}
			p := strconv.Itoa(sv.Port) + "/tcp"
			c.ExposedPorts = map[string]struct{}{p: {}}
			c.HostConfig.PortBindings = map[string][]apiPortBinding{p: {{HostIP: "127.0.0.1", HostPort: strconv.Itoa(port)}}}
			target = net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
		}
	}
	return c, target, nil
}

func (s *engineServer) handleService(w http.ResponseWriter, r *http.Request) {
	var sv wireService
	if err := decode(w, r, &sv); err != nil {
		s.refuse(w, r, http.StatusBadRequest, err)
		return
	}
	c, target, err := s.serviceContainer(r.Context(), sv)
	if err != nil {
		s.refuse(w, r, http.StatusForbidden, err)
		return
	}
	s.serveStream(w, r, servicePrefix+sv.Service, c, "", http.Header{"Eriphe-Target": {target}})
}

// serveStream creates a container and runs it on a connection of its own:
// its input from the node, its output and its end to it (engine_stream.go).
// The container outlives the node's connection, as it outlived the docker
// CLI: it is stopped by a stop.
func (s *engineServer) serveStream(w http.ResponseWriter, r *http.Request, name string, c apiContainer, extra string, header http.Header) {
	if !strings.EqualFold(r.Header.Get("Upgrade"), "eriphe-engine") {
		s.refuse(w, r, http.StatusBadRequest, errors.New("a container's stream needs Upgrade: eriphe-engine"))
		return
	}
	if err := s.d.create(r.Context(), name, c, true); err != nil {
		s.refuse(w, r, http.StatusBadGateway, fmt.Errorf("create %s: %v", name, err))
		return
	}
	if extra != "" {
		if err := s.d.connect(r.Context(), extra, name); err != nil {
			s.d.remove(context.Background(), name)
			s.refuse(w, r, http.StatusBadGateway, fmt.Errorf("connect %s to %s: %v", name, extra, err))
			return
		}
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		s.d.remove(context.Background(), name)
		s.refuse(w, r, http.StatusInternalServerError, errors.New("cannot hijack"))
		return
	}
	conn, buf, err := hj.Hijack()
	if err != nil {
		s.d.remove(context.Background(), name)
		return
	}
	defer conn.Close()
	head := "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: eriphe-engine\r\n"
	for k, vs := range header {
		for _, v := range vs {
			head += k + ": " + v + "\r\n"
		}
	}
	io.WriteString(conn, head+"\r\n")
	out := &frameWriter{w: conn}
	var stdin io.Reader
	if c.OpenStdin {
		pr, pw := io.Pipe()
		stdin = pr
		go relayStdin(buf.Reader, pw)
	}
	exit, err := s.d.run(context.Background(), name, stdin, func(kind byte, p []byte) { out.frame(kind, p) })
	end := streamEnd{Exit: exit}
	if err != nil {
		end.Error = err.Error()
	}
	b, _ := json.Marshal(end)
	out.frame(frameEnd, b)
}

// relayStdin passes the node's input frames to a container, until the
// empty one or the node's going away.
func relayStdin(r *bufio.Reader, w *io.PipeWriter) {
	defer w.Close()
	for {
		kind, p, err := readFrame(r)
		if err != nil || (kind == frameStdin && len(p) == 0) {
			return
		}
		if kind != frameStdin {
			continue
		}
		if _, err := w.Write(p); err != nil {
			return
		}
	}
}

// nodeContainer checks that a container is the node's task or service of
// that name: its label says so. found is false when there is no such
// container.
func (s *engineServer) nodeContainer(ctx context.Context, container string) (i inspected, found bool, err error) {
	label, id := "", ""
	switch {
	case strings.HasPrefix(container, taskPrefix):
		label, id = "eriphe.task", strings.TrimPrefix(container, taskPrefix)
	case strings.HasPrefix(container, servicePrefix):
		label, id = "eriphe.service", strings.TrimPrefix(container, servicePrefix)
	}
	if label == "" || !engineID.MatchString(id) {
		return i, false, fmt.Errorf("%q is not a task's or a service's container", container)
	}
	i, err = s.d.inspect(ctx, container)
	if isStatus(err, http.StatusNotFound) {
		return i, false, nil
	}
	if err != nil {
		return i, false, err
	}
	if v, ok := i.Config.Labels[label]; !ok || v != id {
		return i, true, fmt.Errorf("%s is not the node's (no %s=%s)", container, label, id)
	}
	return i, true, nil
}

// handleExec runs a script in a task's running container: that task's
// only, never a service's, a helper's or a wake's, nor any other of the
// machine. Its output and its exit code apart: a script that could not
// run is an error, never a failed script.
func (s *engineServer) handleExec(w http.ResponseWriter, r *http.Request) {
	var e wireExec
	if err := decode(w, r, &e); err != nil {
		s.refuse(w, r, http.StatusBadRequest, err)
		return
	}
	if !engineID.MatchString(e.Task) {
		s.refuse(w, r, http.StatusForbidden, fmt.Errorf("bad task %q", e.Task))
		return
	}
	for _, kv := range e.Env {
		if k, _, ok := strings.Cut(kv, "="); !ok || !envName.MatchString(k) || strings.ContainsRune(kv, 0) {
			s.refuse(w, r, http.StatusForbidden, fmt.Errorf("bad environment variable %q", k))
			return
		}
	}
	container := taskPrefix + e.Task
	i, found, err := s.nodeContainer(r.Context(), container)
	switch {
	case err != nil:
		s.refuse(w, r, http.StatusForbidden, err)
		return
	case !found:
		s.refuse(w, r, http.StatusNotFound, fmt.Errorf("no container %s", container))
		return
	case !i.State.Running:
		s.refuse(w, r, http.StatusConflict, fmt.Errorf("%s is not running", container))
		return
	}
	script := "(\n" + e.Script + "\n)"
	if e.Setup != "" {
		script = e.Setup + "\n" + script
	}
	out := &tailBuffer{}
	exit, err := s.d.execIn(r.Context(), container, e.Env, append([]string{"sh", "-c", script, e.Name}, e.Args...),
		func(_ byte, p []byte) { out.Write(p) })
	if err != nil {
		s.refuse(w, r, http.StatusBadGateway, fmt.Errorf("exec in %s: %v", container, err))
		return
	}
	writeJSON(w, http.StatusOK, wireExecResult{Output: tail(out.String(), max(e.Max, 0)), Exit: exit})
}

func (s *engineServer) handleStop(w http.ResponseWriter, r *http.Request) {
	var c wireContainer
	if err := decode(w, r, &c); err != nil {
		s.refuse(w, r, http.StatusBadRequest, err)
		return
	}
	_, found, err := s.nodeContainer(r.Context(), c.Container)
	if err != nil {
		s.refuse(w, r, http.StatusForbidden, err)
		return
	}
	if found {
		if err := s.d.stop(r.Context(), c.Container, 10); err != nil && !isStatus(err, http.StatusNotFound) {
			s.refuse(w, r, http.StatusBadGateway, err)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleRemoveStray removes the containers of one of the node's labels,
// whatever its value.
func (s *engineServer) handleRemoveStray(w http.ResponseWriter, r *http.Request) {
	var l wireLabel
	if err := decode(w, r, &l); err != nil {
		s.refuse(w, r, http.StatusBadRequest, err)
		return
	}
	if !slices.Contains(nodeLabels, l.Label) {
		s.refuse(w, r, http.StatusForbidden, fmt.Errorf("%q is not a label of the node's containers", l.Label))
		return
	}
	ids, err := s.d.listLabelled(r.Context(), l.Label)
	if err != nil {
		s.refuse(w, r, http.StatusBadGateway, err)
		return
	}
	for _, id := range ids {
		s.d.remove(r.Context(), id)
	}
	w.WriteHeader(http.StatusNoContent)
}

// helperContainer is a folder helper's container (helper.go): the node's
// own image running eriphe-node folder-helper, no network, a read-only
// root, its folders checked.
func (s *engineServer) helperContainer(h wireHelper) (apiContainer, error) {
	if s.nodeImage == "" {
		return apiContainer{}, errors.New("this engine runs no helper (no -node-image)")
	}
	if !helperOp.MatchString(h.Op) {
		return apiContainer{}, fmt.Errorf("bad helper %q", h.Op)
	}
	mounts, err := s.mounts(h.Mounts)
	if err != nil {
		return apiContainer{}, err
	}
	stdin := h.Stdin != nil
	c := apiContainer{Image: s.nodeImage, Entrypoint: []string{nodeBinary}, Cmd: []string{"folder-helper"},
		Labels:      map[string]string{"eriphe.helper": h.Op},
		AttachStdin: stdin, OpenStdin: stdin, StdinOnce: stdin, AttachStdout: true, AttachStderr: true,
		HostConfig: apiHostConfig{Mounts: mounts, NetworkMode: "none", ReadonlyRootfs: true, AutoRemove: true,
			CapDrop: []string{"ALL"}, SecurityOpt: []string{"no-new-privileges"}, PidsLimit: 64}}
	if h.User == "" {
		c.User = "0:0"
		c.HostConfig.CapAdd = []string{"CHOWN", "DAC_OVERRIDE", "FOWNER"}
	} else {
		if err := checkUser(h.User); err != nil {
			return apiContainer{}, err
		}
		c.User = h.User
	}
	return c, nil
}

func (s *engineServer) handleHelper(w http.ResponseWriter, r *http.Request) {
	var h wireHelper
	if err := decode(w, r, &h); err != nil {
		s.refuse(w, r, http.StatusBadRequest, err)
		return
	}
	c, err := s.helperContainer(h)
	if err != nil {
		s.refuse(w, r, http.StatusForbidden, err)
		return
	}
	name := "eriphe-helper-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	if err := s.d.create(r.Context(), name, c, false); err != nil {
		s.refuse(w, r, http.StatusBadGateway, fmt.Errorf("create a helper: %v", err))
		return
	}
	var stdin io.Reader
	if h.Stdin != nil {
		stdin = bytes.NewReader(h.Stdin)
	}
	var res wireHelperResult
	var stdout, stderr bytes.Buffer
	res.Exit, err = s.d.run(r.Context(), name, stdin, func(kind byte, p []byte) {
		if kind == frameStdout {
			stdout.Write(p)
		} else {
			stderr.Write(p)
		}
	})
	if err != nil {
		s.refuse(w, r, http.StatusBadGateway, fmt.Errorf("run a helper: %v", err))
		return
	}
	res.Stdout, res.Stderr = stdout.Bytes(), stderr.Bytes()
	writeJSON(w, http.StatusOK, res)
}

// handleWake sends Wake-on-LAN packets from the host's network: the node's
// own image, the one container allowed there, running wake-send only,
// nothing mounted, no capability, read-only.
func (s *engineServer) handleWake(w http.ResponseWriter, r *http.Request) {
	var in wakeInfo
	if err := decode(w, r, &in); err != nil {
		s.refuse(w, r, http.StatusBadRequest, err)
		return
	}
	if s.nodeImage == "" {
		s.refuse(w, r, http.StatusForbidden, errors.New("this engine wakes no machine (no -node-image)"))
		return
	}
	macs, err := parseMACs(strings.Join(in.MACs, ","))
	if err == nil && len(macs) == 0 {
		err = errors.New("no MAC address")
	}
	if err == nil {
		in.Broadcast, err = parseBroadcast(in.Broadcast)
	}
	if err != nil {
		s.refuse(w, r, http.StatusForbidden, err)
		return
	}
	cmd := []string{"wake-send", "-mac", strings.Join(macs, ",")}
	if in.Broadcast != "" {
		cmd = append(cmd, "-broadcast", in.Broadcast)
	}
	c := apiContainer{Image: s.nodeImage, Entrypoint: []string{nodeBinary}, Cmd: cmd, Labels: map[string]string{"eriphe.wake": "1"},
		AttachStdout: true, AttachStderr: true,
		HostConfig: apiHostConfig{NetworkMode: "host", ReadonlyRootfs: true, AutoRemove: true,
			CapDrop: []string{"ALL"}, SecurityOpt: []string{"no-new-privileges:true"}}}
	name := "eriphe-wake-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	if err := s.d.create(r.Context(), name, c, false); err != nil {
		s.refuse(w, r, http.StatusBadGateway, fmt.Errorf("create the wake: %v", err))
		return
	}
	var out tailBuffer
	exit, err := s.d.run(r.Context(), name, nil, func(_ byte, p []byte) { out.Write(p) })
	if err == nil && exit != 0 {
		err = fmt.Errorf("exit status %d: %s", exit, strings.TrimSpace(out.String()))
	}
	if err != nil {
		s.refuse(w, r, http.StatusBadGateway, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *engineServer) handlePull(w http.ResponseWriter, r *http.Request) {
	var in wireImage
	if err := decode(w, r, &in); err != nil {
		s.refuse(w, r, http.StatusBadRequest, err)
		return
	}
	if err := checkImage(in.Image); err != nil {
		s.refuse(w, r, http.StatusForbidden, err)
		return
	}
	if err := s.d.pull(r.Context(), in.Image); err != nil {
		s.refuse(w, r, http.StatusBadGateway, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *engineServer) handleInfo(w http.ResponseWriter, r *http.Request) {
	var v struct{ Version string }
	if err := s.d.call(r.Context(), http.MethodGet, "/version", nil, nil, &v); err != nil {
		s.refuse(w, r, http.StatusBadGateway, err)
		return
	}
	var list []struct{ RepoTags []string }
	s.d.call(r.Context(), http.MethodGet, "/images/json", nil, nil, &list)
	images := []string{}
	for _, img := range list {
		for _, tag := range img.RepoTags {
			if tag != "" && !strings.Contains(tag, "<none>") && len(images) < maxImages {
				images = append(images, tag)
			}
		}
	}
	writeJSON(w, http.StatusOK, wireInfo{Docker: v.Version, Images: images})
}

// handleEnsureNetwork creates the task network if it does not exist
// (engine_docker.go's ensureNetwork).
func (s *engineServer) handleEnsureNetwork(w http.ResponseWriter, r *http.Request) {
	if err := s.ensureNetwork(r.Context()); err != nil {
		s.refuse(w, r, http.StatusBadGateway, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *engineServer) ensureNetwork(ctx context.Context) error {
	name := dockerNetwork(s.taskNetwork)
	if name == defaultBridge {
		log.Printf("warning: tasks on Docker's default bridge have direct egress, the LAN included: give -task-network")
		return nil
	}
	n, err := s.d.network(ctx, name)
	if err == nil {
		if !n.Internal {
			log.Printf("warning: the task network %s is not internal: tasks can bypass the egress relay and reach the LAN. "+
				"Recreate it with no task running: docker network rm %s (this engine creates it again), or internal: true in compose", name, name)
		}
		return nil
	}
	if !isStatus(err, http.StatusNotFound) {
		return fmt.Errorf("the task network %s: %v", name, err)
	}
	err = s.d.call(ctx, http.MethodPost, "/networks/create", nil, map[string]any{
		"Name": name, "Driver": "bridge", "Internal": true,
		"Options": map[string]string{"com.docker.network.bridge.enable_icc": "false"},
		"Labels":  map[string]string{"eriphe.network": "tasks"},
	}, nil)
	if err != nil {
		return fmt.Errorf("the task network %s does not exist and cannot be created (%v)", name, err)
	}
	log.Printf("created the task network %s", name)
	return nil
}

func (s *engineServer) handleTaskSubnets(w http.ResponseWriter, r *http.Request) {
	n, err := s.d.network(r.Context(), dockerNetwork(s.taskNetwork))
	if err != nil {
		s.refuse(w, r, http.StatusBadGateway, err)
		return
	}
	out := wireSubnets{Subnets: []string{}}
	for _, c := range n.IPAM.Config {
		if c.Subnet != "" {
			out.Subnets = append(out.Subnets, c.Subnet)
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// checkPeer answers one host only, by name: allowFrom's addresses; any
// when allowFrom is empty.
func checkPeer(r *http.Request, allowFrom string, lookup func(string) ([]string, error)) error {
	if allowFrom == "" {
		return nil
	}
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	addrs, err := lookup(allowFrom)
	if err == nil && slices.Contains(addrs, host) {
		return nil
	}
	return fmt.Errorf("%s is not %s", host, allowFrom)
}

// runEngine is `eriphe-node engine`.
func runEngine(args []string) {
	fs := flag.NewFlagSet("engine", flag.ExitOnError)
	listen := fs.String("listen", ":8792", "address the node reaches the engine on")
	socket := fs.String("socket", "/var/run/docker.sock", "the Docker daemon's socket")
	allowFrom := fs.String("allow-from", "", "the only host answered, by name (eriphe-node); empty: any")
	bindFrom := fs.String("bind-from", "", "comma-separated places folders may be mounted from: the node's root and the shared places, as the Docker daemon sees them, PATH:ro for one read-only")
	bindFile := fs.String("bind-file", "", "a file of more places folders may be mounted from, one a line (PATH or PATH:ro), read again at each request (eriphe-launcher's folders shared)")
	hostRoot := fs.String("host-root", "", "where the host's / is seen here, read-only: folders are resolved in it (empty: at their paths here)")
	nodeImage := fs.String("node-image", "", "the node's own image: the one run on the host's network to wake a machine, and as a folder helper (empty: neither)")
	s := &engineServer{lookup: net.LookupHost}
	fs.StringVar(&s.taskNetwork, "task-network", "eriphe-tasks", "Docker network for task containers, apart from eriphe-core and the services (created if missing)")
	fs.StringVar(&s.network, "network", "", "Docker network of eriphe-core, shared with the node: the brain's tasks and the services with egress join it (empty: Docker's default bridge for the services, none for the tasks)")
	fs.StringVar(&s.serviceNetwork, "service-network", "", "internal Docker network, shared with the node, for http services without egress")
	fs.StringVar(&s.cacheVolume, "cache-volume", "eriphe-cache", "Docker volume for the tasks' tool and package caches (empty: none)")
	fs.Parse(args)
	if err := checkNetworks(options{taskNetwork: s.taskNetwork, network: s.network, serviceNetwork: s.serviceNetwork}); err != nil {
		fmt.Fprintln(os.Stderr, "engine:", err)
		os.Exit(2)
	}
	s.allowFrom, s.nodeImage = *allowFrom, *nodeImage
	s.policy = &dockerPolicy{bindFile: *bindFile, resolve: filepath.EvalSymlinks}
	if *hostRoot != "" {
		root := *hostRoot
		s.policy.resolve = func(p string) (string, error) { return resolveIn(root, p) }
	}
	for _, b := range strings.Split(*bindFrom, ",") {
		if b = strings.TrimSpace(b); b != "" {
			path, ro := strings.CutSuffix(b, ":ro")
			if path = filepath.Clean(path); ro {
				path += ":ro"
			}
			s.policy.bindFrom = append(s.policy.bindFrom, path)
		}
	}
	sock := *socket
	s.d = newDockerAPI(func(ctx context.Context) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", sock)
	})
	log.Printf("eriphe-node engine %s (API %s) on %s: folders from %v %s, task network %s, network %s, service network %s",
		version, engineAPI, *listen, s.policy.bindFrom, *bindFile, s.taskNetwork, orNone(s.network), orNone(s.serviceNetwork))
	srv := &http.Server{Addr: *listen, Handler: s.handler(), ReadHeaderTimeout: 10 * time.Second}
	if err := srv.ListenAndServe(); err != nil {
		fmt.Fprintln(os.Stderr, "engine:", err)
		os.Exit(1)
	}
}
