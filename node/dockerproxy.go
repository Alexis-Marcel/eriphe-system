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
	"net/http/httputil"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

// The Docker proxy: eriphe-node docker-proxy, in a container of its own
// next to the node (install.sh). It holds the Docker socket, the node never
// does; the node reaches Docker through it (DOCKER_HOST), and it forwards
// only the calls the node makes, the way node/container.go, service.go and
// wake.go make them. A container is created only if its whole request is
// one of those: an allowlist of fields, every other one empty, so that no
// privileged container, added capability, device, host namespace, weakened
// security option or bind mount outside the node's root and the shared
// places gets through, whatever a compromised node asks. A container is
// started, attached to, waited for, stopped or removed only if it carries
// one of the node's labels: the other containers of the machine are not
// the node's to touch.
//
// Bind mounts are checked on their real path: the proxy sees the node's
// root and the shared places read-only, at the same paths as the Docker
// daemon, resolves their symlinks and hands the daemon the resolved path.
// What it cannot prevent is a race: a directory replaced by a symlink
// between its check and the daemon's mount.

// dockerPolicy is what the proxy allows.
type dockerPolicy struct {
	bindFrom  []string        // bind mounts only from these places, or under them
	networks  map[string]bool // the networks containers may join
	wakeImage string          // the node's own image: the one container on the host's network
	resolve   func(string) (string, error)
}

// nodeLabels are the labels of the containers the node makes: a container
// without one is not created, nor touched.
var nodeLabels = []string{"eriphe.task", "eriphe.service", "eriphe.wake"}

// taskCaps are the capabilities a container may get back: a task's
// (container.go, hardening).
var taskCaps = map[string]bool{"CHOWN": true, "DAC_OVERRIDE": true, "FOWNER": true}

var (
	apiVersion  = regexp.MustCompile(`^/v1\.[0-9]+/`)
	containerOp = regexp.MustCompile(`^/containers/([a-zA-Z0-9][a-zA-Z0-9_.-]*)/(attach|start|wait|stop)$`)
	containerRm = regexp.MustCompile(`^/containers/([a-zA-Z0-9][a-zA-Z0-9_.-]*)$`)
	imageInfo   = regexp.MustCompile(`^/images/[a-zA-Z0-9][a-zA-Z0-9_./:@-]*/json$`)
	networkInfo = regexp.MustCompile(`^/networks/([a-zA-Z0-9][a-zA-Z0-9_.-]*)$`)
	volumeName  = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`)
)

// call is what the proxy makes of a request: refused (err), forwarded as
// is, forwarded once its container is checked (target), or a create whose
// body is checked.
type call struct {
	target string // a container the request acts on
	create bool
}

// classify allows a request by its method and path, without the API version.
func (p *dockerPolicy) classify(r *http.Request) (call, error) {
	if r.URL.RawPath != "" {
		return call{}, errors.New("escaped path")
	}
	pth := r.URL.Path
	if loc := apiVersion.FindStringIndex(pth); loc != nil {
		pth = pth[loc[1]-1:]
	}
	if path.Clean(pth) != pth {
		return call{}, errors.New("unclean path")
	}
	// No body but a create's: an old API's start took a HostConfig.
	if r.Method != http.MethodPost || pth != "/containers/create" {
		if r.ContentLength > 0 || len(r.TransferEncoding) > 0 {
			return call{}, errors.New("unexpected body")
		}
	}
	switch r.Method {
	case http.MethodHead, http.MethodGet:
		switch {
		case pth == "/_ping":
			return call{}, nil
		case r.Method == http.MethodHead:
		case pth == "/version", pth == "/containers/json", pth == "/images/json", imageInfo.MatchString(pth):
			return call{}, nil
		case networkInfo.MatchString(pth):
			if n := networkInfo.FindStringSubmatch(pth)[1]; n == "bridge" || p.networks[n] {
				return call{}, nil
			}
		}
	case http.MethodPost:
		switch {
		case pth == "/containers/create":
			return call{create: true}, nil
		case pth == "/images/create":
			// A pull only: fromSrc imports any file as an image of any name.
			q := r.URL.Query()
			if q.Get("fromImage") != "" && !q.Has("fromSrc") {
				return call{}, nil
			}
		case containerOp.MatchString(pth):
			return call{target: containerOp.FindStringSubmatch(pth)[1]}, nil
		}
	case http.MethodDelete:
		if m := containerRm.FindStringSubmatch(pth); m != nil {
			return call{target: m[1]}, nil
		}
	}
	return call{}, errors.New("not a call eriphe-node makes")
}

// checkCreate checks a container's create request, and returns it with its
// bind mounts' sources resolved.
func (p *dockerPolicy) checkCreate(body []byte) ([]byte, error) {
	var c map[string]any
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber() // numbers go back to the daemon as they came
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("bad body: %v", err)
	}
	hc, _ := c["HostConfig"].(map[string]any)
	if hc == nil {
		return nil, errors.New("no HostConfig")
	}
	for k, v := range c {
		if !configFields[k] && !zero(v) {
			return nil, fmt.Errorf("%s is not allowed", k)
		}
	}
	for k, v := range hc {
		if !hostFields[k] && !zero(v) {
			return nil, fmt.Errorf("HostConfig.%s is not allowed", k)
		}
	}
	labels, _ := c["Labels"].(map[string]any)
	if !slices.ContainsFunc(nodeLabels, func(l string) bool { _, ok := labels[l]; return ok }) {
		return nil, errors.New("not a container of eriphe-node (no eriphe.task, eriphe.service or eriphe.wake label)")
	}
	// Hardened as the node always does it.
	if !slices.ContainsFunc(stringsOf(hc["CapDrop"]), func(s string) bool { return strings.EqualFold(s, "ALL") }) {
		return nil, errors.New("CapDrop must be ALL")
	}
	for _, cp := range stringsOf(hc["CapAdd"]) {
		if !taskCaps[strings.TrimPrefix(strings.ToUpper(cp), "CAP_")] {
			return nil, fmt.Errorf("capability %s is not allowed", cp)
		}
	}
	nnp := false
	for _, o := range stringsOf(hc["SecurityOpt"]) {
		switch o {
		case "no-new-privileges", "no-new-privileges:true", "no-new-privileges=true":
			nnp = true
		default:
			return nil, fmt.Errorf("security option %s is not allowed", o)
		}
	}
	if !nnp {
		return nil, errors.New("no-new-privileges is required")
	}
	if hc["MaskedPaths"] != nil || hc["ReadonlyPaths"] != nil {
		return nil, errors.New("MaskedPaths and ReadonlyPaths are the daemon's")
	}
	if rp, ok := hc["RestartPolicy"].(map[string]any); ok && !zero(rp) && !(rp["Name"] == "no" && zero(rp["MaximumRetryCount"])) {
		return nil, errors.New("RestartPolicy is not allowed")
	}
	if err := p.checkPorts(hc["PortBindings"]); err != nil {
		return nil, err
	}
	if err := p.checkNetworks(c, hc); err != nil {
		return nil, err
	}
	if err := p.checkMounts(hc); err != nil {
		return nil, err
	}
	return json.Marshal(c)
}

// configFields and hostFields are the fields of a create request the node
// may set; any other must be empty. Some are checked further (checkCreate).
var configFields = setOf("Hostname", "Domainname", "User", "AttachStdin", "AttachStdout", "AttachStderr",
	"ExposedPorts", "Tty", "OpenStdin", "StdinOnce", "Env", "Cmd", "Healthcheck", "ArgsEscaped", "Image",
	"Volumes", "WorkingDir", "Entrypoint", "NetworkDisabled", "Labels", "StopSignal", "StopTimeout", "Shell",
	"HostConfig", "NetworkingConfig")

var hostFields = setOf("Binds", "Mounts", "Tmpfs", "NetworkMode", "PortBindings", "RestartPolicy", "AutoRemove",
	"CapAdd", "CapDrop", "SecurityOpt", "ExtraHosts", "GroupAdd", "ReadonlyRootfs", "Init", "ConsoleSize",
	"Memory", "MemorySwap", "MemoryReservation", "MemorySwappiness", "NanoCpus", "CpuShares", "CpuPeriod",
	"CpuQuota", "PidsLimit", "ShmSize", "MaskedPaths", "ReadonlyPaths")

func setOf(names ...string) map[string]bool {
	m := map[string]bool{}
	for _, n := range names {
		m[n] = true
	}
	return m
}

// zero tells whether a JSON value is empty: what the docker CLI sends for
// what it is not asked.
func zero(v any) bool {
	switch v := v.(type) {
	case nil:
		return true
	case bool:
		return !v
	case string:
		return v == ""
	case json.Number:
		return v.String() == "0"
	case []any:
		for _, e := range v {
			if !zero(e) {
				return false
			}
		}
		return true
	case map[string]any:
		for _, e := range v {
			if !zero(e) {
				return false
			}
		}
		return true
	}
	return false
}

// stringsOf is a JSON array of strings; anything else in it is kept as
// text, to be refused by its checker.
func stringsOf(v any) []string {
	a, _ := v.([]any)
	out := make([]string, 0, len(a))
	for _, e := range a {
		s, ok := e.(string)
		if !ok {
			s = fmt.Sprint(e)
		}
		out = append(out, s)
	}
	return out
}

// checkPorts allows ports published on the loopback only (a service, when
// the node runs outside Docker).
func (p *dockerPolicy) checkPorts(v any) error {
	ports, _ := v.(map[string]any)
	for port, bs := range ports {
		list, _ := bs.([]any)
		for _, b := range list {
			m, _ := b.(map[string]any)
			if ip := net.ParseIP(fmt.Sprint(m["HostIp"])); ip == nil || !ip.IsLoopback() {
				return fmt.Errorf("port %s may be published on the loopback only", port)
			}
		}
	}
	return nil
}

// checkNetworks allows the node's networks, none, and the host's for one
// container only: the node's own image sending wake-on-LAN packets (wake.go).
func (p *dockerPolicy) checkNetworks(c, hc map[string]any) error {
	mode, _ := hc["NetworkMode"].(string)
	if mode == "host" {
		if p.wakeImage == "" || c["Image"] != p.wakeImage {
			return errors.New("the host's network is for the node's own image")
		}
		labels, _ := c["Labels"].(map[string]any)
		cmd := stringsOf(c["Cmd"])
		if _, ok := labels["eriphe.wake"]; !ok || !slices.Equal(stringsOf(c["Entrypoint"]), []string{"/usr/local/bin/eriphe-node"}) ||
			len(cmd) == 0 || cmd[0] != "wake-send" || !zero(hc["Binds"]) || !zero(hc["Mounts"]) || !zero(hc["CapAdd"]) || hc["ReadonlyRootfs"] != true {
			return errors.New("the host's network is for sending wake-on-LAN packets only")
		}
		return nil
	}
	if mode != "" && mode != "none" && mode != "default" && mode != "bridge" && !p.networks[mode] {
		return fmt.Errorf("network %s is not allowed", mode)
	}
	if nc, ok := c["NetworkingConfig"].(map[string]any); ok {
		eps, _ := nc["EndpointsConfig"].(map[string]any)
		for name := range eps {
			if !p.networks[name] {
				return fmt.Errorf("network %s is not allowed", name)
			}
		}
	}
	return nil
}

// checkMounts allows bind mounts from the allowed places only, resolved,
// named volumes without a driver's options (they could bind any path), and
// tmpfs.
func (p *dockerPolicy) checkMounts(hc map[string]any) error {
	binds, _ := hc["Binds"].([]any)
	for i, b := range binds {
		s, _ := b.(string)
		parts := strings.Split(s, ":")
		if len(parts) < 2 || len(parts) > 3 {
			return fmt.Errorf("bind %q is not src:dst[:ro|rw]", s)
		}
		if len(parts) == 3 && parts[2] != "ro" && parts[2] != "rw" {
			return fmt.Errorf("bind %q: option %s is not allowed", s, parts[2])
		}
		if !strings.HasPrefix(parts[0], "/") {
			if !volumeName.MatchString(parts[0]) {
				return fmt.Errorf("bind %q: bad volume name", s)
			}
			continue
		}
		real, err := p.bindSource(parts[0])
		if err != nil {
			return err
		}
		parts[0] = real
		binds[i] = strings.Join(parts, ":")
	}
	mounts, _ := hc["Mounts"].([]any)
	for _, raw := range mounts {
		m, _ := raw.(map[string]any)
		for k, v := range m {
			if !slices.Contains([]string{"Type", "Source", "Target", "ReadOnly", "Consistency", "VolumeOptions", "TmpfsOptions"}, k) && !zero(v) {
				return fmt.Errorf("mount option %s is not allowed", k)
			}
		}
		switch m["Type"] {
		case "bind":
			src, _ := m["Source"].(string)
			real, err := p.bindSource(src)
			if err != nil {
				return err
			}
			m["Source"] = real
		case "volume":
			if src, _ := m["Source"].(string); src != "" && !volumeName.MatchString(src) {
				return fmt.Errorf("volume %q: bad name", src)
			}
			if vo, ok := m["VolumeOptions"].(map[string]any); ok && !zero(vo["DriverConfig"]) {
				return errors.New("a volume's driver options are not allowed")
			}
		case "tmpfs":
		default:
			return fmt.Errorf("mount type %v is not allowed", m["Type"])
		}
	}
	return nil
}

// bindSource is a bind mount's source resolved, if it is one of the allowed
// places or under one.
func (p *dockerPolicy) bindSource(src string) (string, error) {
	if !filepath.IsAbs(src) {
		return "", fmt.Errorf("bind source %q is not absolute", src)
	}
	real, err := p.resolve(src)
	if err != nil {
		return "", fmt.Errorf("bind source %s cannot be resolved", src)
	}
	for _, from := range p.bindFrom {
		if r, err := p.resolve(from); err == nil && within(r, real) {
			return real, nil
		}
	}
	return "", fmt.Errorf("bind source %s is outside the allowed places", src)
}

// dockerProxy forwards the allowed calls to the Docker socket.
type dockerProxy struct {
	policy    *dockerPolicy
	socket    string
	allowFrom string // the only host it answers, by name
	lookup    func(string) ([]string, error)
	client    *http.Client
	rp        *httputil.ReverseProxy
}

func newDockerProxy(policy *dockerPolicy, socket, allowFrom string) *dockerProxy {
	dial := func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}
	tr := &http.Transport{DialContext: dial, MaxIdleConns: 8, IdleConnTimeout: time.Minute}
	p := &dockerProxy{policy: policy, socket: socket, allowFrom: allowFrom, lookup: net.LookupHost,
		client: &http.Client{Transport: tr, Timeout: 30 * time.Second}}
	p.rp = &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out.URL.Scheme, pr.Out.URL.Host, pr.Out.Host = "http", "docker", "docker"
		},
		Transport:     tr,
		FlushInterval: -1, // wait and pull answer as things happen
	}
	return p
}

func (p *dockerProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if err := p.checkPeer(r); err != nil {
		p.refuse(w, r, http.StatusForbidden, err)
		return
	}
	c, err := p.policy.classify(r)
	if err != nil {
		p.refuse(w, r, http.StatusForbidden, err)
		return
	}
	if c.create {
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
		if err == nil {
			body, err = p.policy.checkCreate(body)
		}
		if err != nil {
			p.refuse(w, r, http.StatusForbidden, err)
			return
		}
		r.Body, r.ContentLength = io.NopCloser(bytes.NewReader(body)), int64(len(body))
		r.TransferEncoding = nil
		r.Header.Del("Content-Length")
	}
	if c.target != "" {
		if status, err := p.nodeContainer(r.Context(), c.target); err != nil {
			p.refuse(w, r, status, err)
			return
		}
	}
	if strings.EqualFold(r.Header.Get("Upgrade"), "tcp") {
		p.hijack(w, r) // attach: the container's stdin and stdout
		return
	}
	p.rp.ServeHTTP(w, r)
}

// checkPeer answers the node only: the address allowFrom resolves to.
func (p *dockerProxy) checkPeer(r *http.Request) error {
	if p.allowFrom == "" {
		return nil
	}
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	addrs, err := p.lookup(p.allowFrom)
	if err == nil && slices.Contains(addrs, host) {
		return nil
	}
	return fmt.Errorf("%s is not %s", host, p.allowFrom)
}

// nodeContainer checks that a container carries one of the node's labels.
func (p *dockerProxy) nodeContainer(ctx context.Context, id string) (int, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://docker/containers/"+url.PathEscape(id)+"/json", nil)
	resp, err := p.client.Do(req)
	if err != nil {
		return http.StatusBadGateway, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return http.StatusNotFound, fmt.Errorf("no such container: %s", id)
	}
	var info struct {
		Config struct{ Labels map[string]string }
	}
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil || resp.StatusCode != http.StatusOK {
		return http.StatusBadGateway, fmt.Errorf("cannot inspect %s (%s)", id, resp.Status)
	}
	for _, l := range nodeLabels {
		if _, ok := info.Config.Labels[l]; ok {
			return 0, nil
		}
	}
	return http.StatusForbidden, fmt.Errorf("%s is not a container of eriphe-node", id)
}

// hijack forwards an upgraded connection (docker run -i's attach) byte for
// byte: the request, then both ways until the daemon's side ends. The
// client's end of input (stdin closed) is passed on as a half-close.
func (p *dockerProxy) hijack(w http.ResponseWriter, r *http.Request) {
	back, err := net.Dial("unix", p.socket)
	if err != nil {
		p.refuse(w, r, http.StatusBadGateway, err)
		return
	}
	defer back.Close()
	hj, ok := w.(http.Hijacker)
	if !ok {
		p.refuse(w, r, http.StatusInternalServerError, errors.New("cannot hijack"))
		return
	}
	front, buf, err := hj.Hijack()
	if err != nil {
		return
	}
	defer front.Close()
	r.URL.Scheme, r.URL.Host, r.Host = "", "", "docker"
	r.RequestURI = ""
	if err := r.Write(back); err != nil {
		return
	}
	go func() {
		io.Copy(back, io.MultiReader(io.LimitReader(buf.Reader, int64(buf.Reader.Buffered())), front))
		closeWrite(back)
	}()
	io.Copy(front, bufio.NewReader(back))
}

func closeWrite(c net.Conn) {
	if cw, ok := c.(interface{ CloseWrite() error }); ok {
		cw.CloseWrite()
	}
}

// refuse answers as the daemon does, so that the docker CLI shows why.
func (p *dockerProxy) refuse(w http.ResponseWriter, r *http.Request, status int, err error) {
	log.Printf("blocked %s %s: %v", r.Method, r.URL.Path, err)
	writeJSON(w, status, map[string]string{"message": "eriphe-docker-proxy: " + err.Error()})
}

// runDockerProxy is `eriphe-node docker-proxy`.
func runDockerProxy(args []string) {
	fs := flag.NewFlagSet("docker-proxy", flag.ExitOnError)
	listen := fs.String("listen", ":2375", "address the node reaches the proxy on")
	socket := fs.String("socket", "/var/run/docker.sock", "the Docker daemon's socket")
	allowFrom := fs.String("allow-from", "", "the only host answered, by name (eriphe-node); empty: any")
	bindFrom := fs.String("bind-from", "", "comma-separated places bind mounts may come from: the node's root and the shared places, seen here at the same paths")
	networks := fs.String("networks", "eriphe-system,eriphe-tasks", "comma-separated networks containers may join, besides none and the default bridge")
	wakeImage := fs.String("wake-image", "", "the node's own image, the one allowed on the host's network (wake-on-LAN)")
	fs.Parse(args)
	policy := &dockerPolicy{networks: map[string]bool{}, wakeImage: *wakeImage, resolve: filepath.EvalSymlinks}
	for _, b := range strings.Split(*bindFrom, ",") {
		if b = strings.TrimSpace(b); b != "" {
			policy.bindFrom = append(policy.bindFrom, filepath.Clean(b))
		}
	}
	for _, n := range strings.Split(*networks, ",") {
		if n = strings.TrimSpace(n); n != "" {
			policy.networks[n] = true
		}
	}
	log.Printf("eriphe-docker-proxy %s on %s: bind mounts from %v, networks %s", version, *listen, policy.bindFrom, *networks)
	srv := &http.Server{Addr: *listen, Handler: newDockerProxy(policy, *socket, *allowFrom), ReadHeaderTimeout: 10 * time.Second}
	if err := srv.ListenAndServe(); err != nil {
		fmt.Fprintln(os.Stderr, "docker-proxy:", err)
		os.Exit(1)
	}
}
