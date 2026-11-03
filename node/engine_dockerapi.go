package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// dockerAPI is the Docker daemon's API, as eriphe-node engine speaks it:
// over its socket, without the docker CLI, the few calls the engine makes.
// Paths carry no API version: the daemon's own.
type dockerAPI struct {
	dial   func(ctx context.Context) (net.Conn, error)
	client *http.Client
}

func newDockerAPI(dial func(ctx context.Context) (net.Conn, error)) *dockerAPI {
	tr := &http.Transport{
		DialContext:  func(ctx context.Context, _, _ string) (net.Conn, error) { return dial(ctx) },
		MaxIdleConns: 8, IdleConnTimeout: time.Minute,
	}
	return &dockerAPI{dial: dial, client: &http.Client{Transport: tr}}
}

// dockerError is the daemon's answer to a call it refused.
type dockerError struct {
	status int
	msg    string
}

func (e *dockerError) Error() string { return e.msg }

func isStatus(err error, status int) bool {
	var de *dockerError
	return errors.As(err, &de) && de.status == status
}

// call makes a call: body as JSON, the answer decoded into out (when not
// nil), any status but 2xx an error.
func (d *dockerAPI) call(ctx context.Context, method, path string, q url.Values, body, out any) error {
	resp, err := d.send(ctx, method, path, q, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if out == nil {
		io.Copy(io.Discard, resp.Body)
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// send makes a call and returns the answer, when it is a 2xx.
func (d *dockerAPI) send(ctx context.Context, method, path string, q url.Values, body any) (*http.Response, error) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rd = bytes.NewReader(b)
	}
	u := "http://docker" + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		defer resp.Body.Close()
		return nil, readDockerError(resp)
	}
	return resp, nil
}

func readDockerError(resp *http.Response) error {
	var e struct{ Message string }
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if json.Unmarshal(raw, &e) != nil || e.Message == "" {
		e.Message = strings.TrimSpace(string(raw))
	}
	if e.Message == "" {
		e.Message = resp.Status
	}
	return &dockerError{resp.StatusCode, e.Message}
}

// hijack makes a call whose answer is a stream (attach, exec start): the
// connection, and what was read of the stream already.
func (d *dockerAPI) hijack(ctx context.Context, path string, q url.Values, body any) (net.Conn, *bufio.Reader, error) {
	conn, err := d.dial(ctx)
	if err != nil {
		return nil, nil, err
	}
	var b []byte
	if body != nil {
		if b, err = json.Marshal(body); err != nil {
			conn.Close()
			return nil, nil, err
		}
	}
	u := "http://docker" + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, _ := http.NewRequest(http.MethodPost, u, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "tcp")
	if err := req.Write(conn); err != nil {
		conn.Close()
		return nil, nil, err
	}
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, req)
	if err != nil {
		conn.Close()
		return nil, nil, err
	}
	if resp.StatusCode != http.StatusSwitchingProtocols && resp.StatusCode != http.StatusOK {
		defer conn.Close()
		return nil, nil, readDockerError(resp)
	}
	return conn, br, nil
}

// Containers, as the API describes them: only the fields the engine sets.
type apiContainer struct {
	Image        string              `json:"Image"`
	User         string              `json:"User,omitempty"`
	Env          []string            `json:"Env,omitempty"`
	Cmd          []string            `json:"Cmd,omitempty"`
	Entrypoint   []string            `json:"Entrypoint,omitempty"`
	Labels       map[string]string   `json:"Labels,omitempty"`
	AttachStdin  bool                `json:"AttachStdin"`
	AttachStdout bool                `json:"AttachStdout"`
	AttachStderr bool                `json:"AttachStderr"`
	OpenStdin    bool                `json:"OpenStdin"`
	StdinOnce    bool                `json:"StdinOnce"`
	ExposedPorts map[string]struct{} `json:"ExposedPorts,omitempty"`
	Healthcheck  *apiHealthcheck     `json:"Healthcheck,omitempty"`
	HostConfig   apiHostConfig       `json:"HostConfig"`
}

type apiHealthcheck struct {
	Test []string `json:"Test"`
}

type apiHostConfig struct {
	Binds          []string                    `json:"Binds,omitempty"`
	Mounts         []apiMount                  `json:"Mounts,omitempty"`
	Tmpfs          map[string]string           `json:"Tmpfs,omitempty"`
	NetworkMode    string                      `json:"NetworkMode,omitempty"`
	PortBindings   map[string][]apiPortBinding `json:"PortBindings,omitempty"`
	AutoRemove     bool                        `json:"AutoRemove"`
	CapAdd         []string                    `json:"CapAdd,omitempty"`
	CapDrop        []string                    `json:"CapDrop"`
	SecurityOpt    []string                    `json:"SecurityOpt"`
	ExtraHosts     []string                    `json:"ExtraHosts,omitempty"`
	GroupAdd       []string                    `json:"GroupAdd,omitempty"`
	ReadonlyRootfs bool                        `json:"ReadonlyRootfs"`
	Memory         int64                       `json:"Memory,omitempty"`
	NanoCpus       int64                       `json:"NanoCpus,omitempty"`
	PidsLimit      int64                       `json:"PidsLimit,omitempty"`
}

type apiMount struct {
	Type     string `json:"Type"`
	Source   string `json:"Source"`
	Target   string `json:"Target"`
	ReadOnly bool   `json:"ReadOnly,omitempty"`
}

type apiPortBinding struct {
	HostIP   string `json:"HostIp"`
	HostPort string `json:"HostPort"`
}

// create creates a container, pulling its image first when the daemon does
// not have it and pull says so (docker run's default; --pull never else).
func (d *dockerAPI) create(ctx context.Context, name string, c apiContainer, pull bool) error {
	q := url.Values{"name": {name}}
	err := d.call(ctx, http.MethodPost, "/containers/create", q, c, nil)
	if pull && isStatus(err, http.StatusNotFound) && strings.Contains(strings.ToLower(err.Error()), "no such image") {
		if err = d.pull(ctx, c.Image); err == nil {
			err = d.call(ctx, http.MethodPost, "/containers/create", q, c, nil)
		}
	}
	return err
}

// connect adds a created container to one more network.
func (d *dockerAPI) connect(ctx context.Context, network, container string) error {
	return d.call(ctx, http.MethodPost, "/networks/"+url.PathEscape(network)+"/connect", nil,
		map[string]string{"Container": container}, nil)
}

// remove removes a container, running or not.
func (d *dockerAPI) remove(ctx context.Context, container string) error {
	return d.call(ctx, http.MethodDelete, "/containers/"+url.PathEscape(container), url.Values{"force": {"1"}}, nil, nil)
}

// stop stops a container, killing it after timeout seconds.
func (d *dockerAPI) stop(ctx context.Context, container string, timeout int) error {
	err := d.call(ctx, http.MethodPost, "/containers/"+url.PathEscape(container)+"/stop", url.Values{"t": {fmt.Sprint(timeout)}}, nil, nil)
	if isStatus(err, http.StatusNotModified) {
		return nil
	}
	return err
}

// inspected is what the engine reads of a container.
type inspected struct {
	ID     string `json:"Id"`
	Name   string `json:"Name"`
	Config struct {
		Labels map[string]string `json:"Labels"`
	} `json:"Config"`
	State struct {
		Running bool `json:"Running"`
	} `json:"State"`
}

func (d *dockerAPI) inspect(ctx context.Context, container string) (inspected, error) {
	var i inspected
	err := d.call(ctx, http.MethodGet, "/containers/"+url.PathEscape(container)+"/json", nil, nil, &i)
	return i, err
}

// run runs a created container to its end, its output to out (stdout and
// stderr frames), stdin its input when not nil: attached, waited for, then
// started, so that nothing of it is missed. The container removes itself
// (AutoRemove): its wait is for its removal, as docker run --rm's.
func (d *dockerAPI) run(ctx context.Context, container string, stdin io.Reader, out func(kind byte, p []byte)) (int, error) {
	q := url.Values{"stream": {"1"}, "stdout": {"1"}, "stderr": {"1"}}
	if stdin != nil {
		q.Set("stdin", "1")
	}
	conn, br, err := d.hijack(ctx, "/containers/"+url.PathEscape(container)+"/attach", q, nil)
	if err != nil {
		d.remove(context.Background(), container)
		return -1, err
	}
	defer conn.Close()
	waited, err := d.send(ctx, http.MethodPost, "/containers/"+url.PathEscape(container)+"/wait", url.Values{"condition": {"removed"}}, nil)
	if err != nil {
		d.remove(context.Background(), container)
		return -1, err
	}
	defer waited.Body.Close()
	if err := d.call(ctx, http.MethodPost, "/containers/"+url.PathEscape(container)+"/start", nil, nil, nil); err != nil {
		d.remove(context.Background(), container)
		return -1, err
	}
	if stdin != nil {
		go func() {
			io.Copy(conn, stdin)
			closeWrite(conn)
		}()
	}
	relayFrames(br, out)
	var w struct {
		StatusCode int
		Error      *struct{ Message string }
	}
	if err := json.NewDecoder(waited.Body).Decode(&w); err != nil {
		return -1, fmt.Errorf("waiting for %s: %v", container, err)
	}
	if w.Error != nil && w.Error.Message != "" {
		return w.StatusCode, errors.New(w.Error.Message)
	}
	return w.StatusCode, nil
}

// relayFrames passes a container's stdout and stderr frames on, until its
// stream ends.
func relayFrames(r io.Reader, out func(kind byte, p []byte)) {
	for {
		kind, p, err := readFrame(r)
		if err != nil {
			return
		}
		if kind == frameStdout || kind == frameStderr {
			out(kind, p)
		}
	}
}

// execIn runs cmd in a running container, its output (stdout and stderr,
// as they come) to out: its exit code.
func (d *dockerAPI) execIn(ctx context.Context, container string, env, cmd []string, out func(kind byte, p []byte)) (int, error) {
	var created struct {
		ID string `json:"Id"`
	}
	err := d.call(ctx, http.MethodPost, "/containers/"+url.PathEscape(container)+"/exec", nil, map[string]any{
		"AttachStdout": true, "AttachStderr": true, "Env": env, "Cmd": cmd,
	}, &created)
	if err != nil {
		return -1, err
	}
	conn, br, err := d.hijack(ctx, "/exec/"+url.PathEscape(created.ID)+"/start", nil, map[string]any{"Detach": false, "Tty": false})
	if err != nil {
		return -1, err
	}
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	relayFrames(br, out)
	conn.Close()
	if ctx.Err() != nil {
		return -1, ctx.Err()
	}
	var state struct {
		Running  bool
		ExitCode int
	}
	if err := d.call(ctx, http.MethodGet, "/exec/"+url.PathEscape(created.ID)+"/json", nil, nil, &state); err != nil {
		return -1, err
	}
	if state.Running {
		return -1, errors.New("its output ended before it did")
	}
	return state.ExitCode, nil
}

// pull pulls an image: the daemon reports a failure in its stream, not in
// its status.
func (d *dockerAPI) pull(ctx context.Context, image string) error {
	name, tag := splitImage(image)
	resp, err := d.send(ctx, http.MethodPost, "/images/create", url.Values{"fromImage": {name}, "tag": {tag}}, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	dec := json.NewDecoder(resp.Body)
	for {
		var m struct {
			Error string `json:"error"`
		}
		if err := dec.Decode(&m); err == io.EOF {
			return nil
		} else if err != nil {
			return err
		}
		if m.Error != "" {
			return errors.New(m.Error)
		}
	}
}

// splitImage is an image's name and its tag or digest (latest if none):
// the daemon pulls every tag when it is given none.
func splitImage(image string) (name, tag string) {
	if i := strings.Index(image, "@"); i >= 0 {
		return image[:i], image[i+1:]
	}
	if i := strings.LastIndex(image, ":"); i > strings.LastIndex(image, "/") {
		return image[:i], image[i+1:]
	}
	return image, "latest"
}

// listLabelled is the ids of the containers carrying a label, whatever its
// value.
func (d *dockerAPI) listLabelled(ctx context.Context, label string) ([]string, error) {
	f, _ := json.Marshal(map[string][]string{"label": {label}})
	var list []struct {
		ID string `json:"Id"`
	}
	if err := d.call(ctx, http.MethodGet, "/containers/json", url.Values{"all": {"1"}, "filters": {string(f)}}, nil, &list); err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(list))
	for _, c := range list {
		ids = append(ids, c.ID)
	}
	return ids, nil
}

// apiNetwork is what the engine reads of a network.
type apiNetwork struct {
	Internal bool `json:"Internal"`
	IPAM     struct {
		Config []struct {
			Subnet  string `json:"Subnet"`
			Gateway string `json:"Gateway"`
		} `json:"Config"`
	} `json:"IPAM"`
}

func (d *dockerAPI) network(ctx context.Context, name string) (apiNetwork, error) {
	var n apiNetwork
	err := d.call(ctx, http.MethodGet, "/networks/"+url.PathEscape(name), nil, nil, &n)
	return n, err
}
