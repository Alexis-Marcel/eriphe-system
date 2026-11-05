package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// A task's SSH accesses (core/lent_ssh.go). eriphe-core holds the key and
// signs; the node carries the rest:
//
//   - each signature: eriphe-cred, the task's SSH agent, calls
//     POST /v1/task/ssh on the task listener (task secret), and the node
//     asks POST /v1/node/tasks/{id}/ssh (node secret). No host socket is
//     mounted, so it works on every engine.
//   - the session: ssh's ProxyCommand, eriphe-cred tunnel, sends a CONNECT
//     to the relay. That one host and port is reachable, on the LAN too,
//     once eriphe-core says the access may be used now; nothing else of the
//     LAN, no other port. The relay sees encrypted bytes.
//
// The task's ssh is configured here, with no secret: the access's name, its
// account, the machine's pinned host key, and which key to offer. A task is
// root in its container and can change that file: the pin protects a task
// from another machine, not a machine from the task, and what a key can do
// is limited by eriphe-core and the machine.

// TaskSSH is an access's machine.
type TaskSSH struct {
	Target    string `json:"target"` // host:port
	User      string `json:"user"`
	HostKey   string `json:"host_key"`
	PublicKey string `json:"public_key"`
}

const (
	sshPath = "/v1/task/ssh"

	sshConfigFile = "ssh-config"
	sshKnownFile  = "ssh-known-hosts"
	sshAgentSock  = "/tmp/eriphe-ssh-agent.sock"
)

// sshAccessScript gives a task with an SSH access the real ssh (the base
// image has none, as for git), its configuration where ssh looks for it
// (the account's home, not $HOME; copied, as ssh refuses one others could
// write), and starts the agent. Nothing without such an access.
const sshAccessScript = `if [ -n "$ERIPHE_SSH_CONFIG" ]; then
  if ! command -v ssh >/dev/null 2>&1 && command -v apt-get >/dev/null 2>&1; then
    echo "eriphe: installing ssh" >&2
    { apt-get update -qq && apt-get install -y -qq openssh-client; } >&2 || echo "eriphe: could not install ssh" >&2
  fi
  sshhome=$(getent passwd "$(id -u)" 2>/dev/null | cut -d: -f6)
  sshhome=${sshhome:-$HOME}
  mkdir -p "$sshhome/.ssh" && chmod 700 "$sshhome/.ssh"
  cp "$ERIPHE_SSH_CONFIG" "$sshhome/.ssh/config" && chmod 600 "$sshhome/.ssh/config"
  rm -f "$SSH_AUTH_SOCK"
  ` + injectDir + `/bin/eriphe-cred ssh-agent >/dev/null 2>&1 &
fi`

func sshAccesses(t *Task) []TaskAccess {
	var out []TaskAccess
	for _, a := range t.Spec.Accesses {
		if a.SSH != nil {
			out = append(out, a)
		}
	}
	return out
}

// sshTargets are the host:port the task's relay opens tunnels to.
func sshTargets(t *Task) []string {
	var out []string
	for _, a := range sshAccesses(t) {
		out = append(out, a.SSH.Target)
	}
	return out
}

// sshEnv is what tells a task's ssh and eriphe-cred where to go.
func (n *node) sshEnv(t *Task) map[string]string {
	if len(sshAccesses(t)) == 0 {
		return nil
	}
	return map[string]string{
		"ERIPHE_SSH_CONFIG": runPath(sshConfigFile),
		"ERIPHE_SSH_URL":    n.callbackFor(t) + sshPath,
		"SSH_AUTH_SOCK":     sshAgentSock,
	}
}

func sshPublicFile(name string) string { return "ssh-" + name + ".pub" }

// withSSHFiles adds the task's ssh configuration to its run files.
func withSSHFiles(t *Task, files map[string][]byte) map[string][]byte {
	list := sshAccesses(t)
	if len(list) == 0 {
		return files
	}
	if files == nil {
		files = map[string][]byte{}
	}
	var config, known bytes.Buffer
	for _, a := range list {
		host, port, err := net.SplitHostPort(a.SSH.Target)
		if err != nil {
			continue
		}
		// The name and the address both lead here; the key is known by name,
		// whatever the port. Only its own key is offered.
		fmt.Fprintf(&config, "Host %s %s\n  HostName %s\n  Port %s\n  User %s\n  HostKeyAlias %s\n  IdentityFile %s\n  IdentitiesOnly yes\n\n",
			a.Name, host, host, port, a.SSH.User, a.Name, runPath(sshPublicFile(a.Name)))
		fmt.Fprintf(&known, "%s %s\n", a.Name, a.SSH.HostKey)
		files[sshPublicFile(a.Name)] = []byte(a.SSH.PublicKey + "\n")
	}
	// Whatever the host: never an unknown key, nor a prompt; the tunnel is
	// the only way there.
	fmt.Fprintf(&config, "Host *\n  ProxyCommand %s/bin/eriphe-cred tunnel %%h %%p\n  UserKnownHostsFile %s\n  GlobalKnownHostsFile /dev/null\n  StrictHostKeyChecking yes\n  UpdateHostKeys no\n  BatchMode yes\n",
		injectDir, runPath(sshKnownFile))
	files[sshConfigFile] = config.Bytes()
	files[sshKnownFile] = known.Bytes()
	return files
}

// isTunnel tells whether hostport is exactly an SSH access of the task,
// with the mode internet, as isEndpoint.
func (t *egressTask) isTunnel(hostport string) bool {
	if t.rule.Mode != egressInternet && t.rule.Mode != "" {
		return false
	}
	target := accessTarget(hostport)
	for _, e := range t.tunnels {
		if e == target {
			return true
		}
	}
	return false
}

// dialTunnel connects to an SSH access's machine, once eriphe-core says the
// task may use it now: the very address checked is dialed, as for a model
// server, on the LAN too but never the node itself nor the cloud's metadata.
func (p *egressProxy) dialTunnel(ctx context.Context, t *egressTask, hostport string) (net.Conn, error) {
	target := accessTarget(hostport)
	host, port, err := net.SplitHostPort(target)
	if err != nil {
		return nil, &egressError{http.StatusBadRequest, "bad address " + hostport}
	}
	if p.sshOpen == nil {
		return nil, refused("no way to ask eriphe-core for %s", target)
	}
	if err := p.sshOpen(ctx, t.id, target); err != nil {
		p.note(t, host, err)
		return nil, err
	}
	return p.dialModelServer(ctx, t, host, port)
}

// sshOpen asks eriphe-core whether a task may open a tunnel.
func (s *state) sshOpen(ctx context.Context, taskID, target string) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	body, _ := json.Marshal(map[string]string{"op": "connect", "target": target})
	status, answer, err := s.askSSH(ctx, taskID, body)
	switch {
	case err != nil:
		return &egressError{http.StatusBadGateway, "eriphe-core: " + err.Error()}
	case status != http.StatusOK:
		return refused("%s", strings.TrimSpace(string(answer)))
	}
	return nil
}

// askSSH relays a request to eriphe-core, under this node's name.
func (s *state) askSSH(ctx context.Context, taskID string, body []byte) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.base()+"/v1/node/tasks/"+taskID+"/ssh", bytes.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+s.Secret)
	req.Header.Set("Eriphe-Node", s.Name)
	req.Header.Set("Content-Type", "application/json")
	res, err := s.longClient().Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer res.Body.Close()
	answer, err := io.ReadAll(io.LimitReader(res.Body, 64<<10))
	return res.StatusCode, answer, err
}

// sshAsker relays to eriphe-core; replaced in tests.
type sshAsker func(ctx context.Context, taskID string, body []byte) (int, []byte, error)

func (n *node) sshHandler(s *state) http.Handler { return n.sshHandlerWith(s.askSSH) }

// sshHandlerWith serves a task's agent: what it asks of eriphe-core, for
// the keys and signatures only. Tunnels are the relay's to ask for.
func (n *node) sshHandlerWith(ask sshAsker) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t := n.taskBySecret(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		if t == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if finished(t.state()) {
			http.Error(w, "the task is over", http.StatusGone)
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, 64<<10))
		var req struct {
			Op string `json:"op"`
		}
		if err == nil {
			err = json.Unmarshal(body, &req)
		}
		if err != nil || (req.Op != "keys" && req.Op != "sign") {
			http.Error(w, "keys or sign", http.StatusBadRequest)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		status, answer, err := ask(ctx, t.ID, body)
		if err != nil {
			http.Error(w, "eriphe-core: "+err.Error(), http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		w.Write(answer)
	})
}
