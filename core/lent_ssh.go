package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/netip"
	"regexp"
	"strings"
	"syscall"
	"time"

	"golang.org/x/crypto/ssh"
)

// An SSH access: a machine a task configures with the real ssh program, and
// so scp, rsync or Ansible over it. Its key never leaves eriphe-core:
// adding the access makes an Ed25519 pair, the private key goes straight to
// the vault, and the page shows the public one for the machine's
// authorized_keys. One key per access: removing that line, or the access,
// ends it.
//
// In the task, eriphe-cred is the SSH agent (SSH_AUTH_SOCK) and each
// signature comes here, through the task's node:
//
//	ssh → eriphe-cred → the node (POST /v1/task/ssh) → eriphe-core (signs)
//
//	POST /v1/node/tasks/{id}/ssh   {op: keys | sign | connect, ...}   (node secret)
//
// The session's bytes go the other way, from the node: eriphe-cred tunnels
// them through the relay, to the access's exact host and port
// (node/egress_ssh.go), which may be on the node's LAN. The relay asks here
// before it opens one (connect). Only a login signature is given, for the
// access's own account: the key signs nothing else, whatever the task asks.
//
// The access pins the machine's host key, recorded by the test connection
// or pasted: it cannot be lent without one. That protects the task from
// another machine, not the machine from the task: what a task does once
// connected is everything the remote account can do, and Eriphe sees the
// logins, not the commands. The page says so.

const (
	accessSSH = "ssh"

	sshDefaultPort = "22"

	// sshTestTimeout is how long the test connection may take.
	sshTestTimeout = 15 * time.Second

	sshOpKeys    = "keys"
	sshOpSign    = "sign"
	sshOpConnect = "connect"

	// sshUserAuthRequest is SSH_MSG_USERAUTH_REQUEST (RFC 4252).
	sshUserAuthRequest = 50
)

var accessSSHUser = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]*$`)

func (a Access) isSSH() bool { return a.Kind == accessSSH }

// sshTarget is the access's one host and port, compared as the relay does.
func (a Access) sshTarget() string {
	if len(a.Hosts) != 1 {
		return ""
	}
	t, err := targetOn(a.Hosts[0], sshDefaultPort)
	if err != nil {
		return ""
	}
	return t
}

// normalHostKey is a host key as it is pinned, "ssh-ed25519 AAAA...", from
// that, an authorized_keys line or a line of ssh-keyscan.
func normalHostKey(s string) (string, error) {
	f := strings.Fields(s)
	for i, w := range f { // ssh-keyscan starts with the host
		if strings.HasPrefix(w, "ssh-") || strings.HasPrefix(w, "ecdsa-") {
			f = f[i:]
			break
		}
	}
	if len(f) < 2 {
		return "", errors.New("not a public key")
	}
	raw, err := base64.StdEncoding.DecodeString(f[1])
	if err != nil {
		return "", err
	}
	k, err := ssh.ParsePublicKey(raw)
	if err != nil {
		return "", err
	}
	return hostKeyLine(k), nil
}

func hostKeyLine(k ssh.PublicKey) string {
	return k.Type() + " " + base64.StdEncoding.EncodeToString(k.Marshal())
}

func (a Access) checkSSH(name string) error {
	if a.Secret == "" {
		return accessRefusal("lent.err.secret", "", name, "it needs a key")
	}
	if len(a.Hosts) != 1 {
		return accessRefusal("lent.err.onehost", "", name, "it reaches one machine")
	}
	t, err := targetOn(a.Hosts[0], sshDefaultPort)
	if err != nil {
		return accessRefusal("lent.err.sshhost", a.Hosts[0], name, "%w", err)
	}
	host, _, _ := net.SplitHostPort(t)
	if ip, err := netip.ParseAddr(host); err == nil && !sshAddr(ip) {
		return accessRefusal("lent.err.sshhost", host, name, "%s cannot be reached by a task: loopback, link-local or multicast", host)
	}
	if !accessSSHUser.MatchString(a.User) {
		return accessRefusal("lent.err.user", a.User, name, "%q is not an account name", a.User)
	}
	if a.HostKey != "" {
		if k, err := normalHostKey(a.HostKey); err != nil || k != a.HostKey {
			return accessRefusal("lent.err.hostkey", "", name, "its host key is not a public key")
		}
	}
	if a.Env != "" || a.Header != "" || len(a.Rules) > 0 {
		return fmt.Errorf("access %s: SSH has no variable, header nor rules", name)
	}
	if a.Value != "" && a.Value != gridFree && a.Value != gridApproval {
		return fmt.Errorf("access %s: its value is free or approval, not %q", name, a.Value)
	}
	return nil
}

// sshAddr is an address an SSH access may name: unicast, private included,
// as the relay dials it (endpointAddr, node/egress.go): never the loopback,
// link-local (the cloud's metadata), multicast nor unspecified.
func sshAddr(a netip.Addr) bool {
	a = a.Unmap()
	return a.IsValid() && a.IsGlobalUnicast() && !(a.Is4() && a.As4()[0] == 0)
}

func sshControl(_, address string, _ syscall.RawConn) error {
	ap, err := netip.ParseAddrPort(address)
	if err != nil || !sshAddr(ap.Addr()) {
		return fmt.Errorf("%s: not an address for an SSH access", address)
	}
	return nil
}

// newSSHKey makes a private key as the vault keeps it.
func newSSHKey(comment string) (string, error) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", err
	}
	block, err := ssh.MarshalPrivateKey(priv, comment)
	if err != nil {
		return "", err
	}
	return string(pem.EncodeToMemory(block)), nil
}

func (a Access) sshSigner() (ssh.Signer, error) {
	raw, err := readSecret(a.Secret)
	if err != nil {
		return nil, err
	}
	return ssh.ParsePrivateKey([]byte(raw))
}

// sshPublicKey is the line for the machine's authorized_keys.
func (a Access) sshPublicKey(name string) (string, error) {
	s, err := a.sshSigner()
	if err != nil {
		return "", err
	}
	return hostKeyLine(s.PublicKey()) + " eriphe-" + name, nil
}

// sshLogin is what a signature is for, read from the data to sign.
type sshLogin struct {
	User, Algorithm string
	Key             []byte
}

// parseSSHLogin reads what a client signs to log in (RFC 4252, section 7),
// and nothing else: a key lent to log in signs no commit, no file.
func parseSSHLogin(data []byte) (sshLogin, error) {
	var m struct {
		Session   string
		Type      byte
		User      string
		Service   string
		Method    string
		Signed    bool
		Algorithm string
		Key       []byte
	}
	if err := ssh.Unmarshal(data, &m); err != nil {
		return sshLogin{}, errors.New("this is not a login")
	}
	if m.Type != sshUserAuthRequest || m.Service != "ssh-connection" || m.Method != "publickey" || !m.Signed {
		return sshLogin{}, errors.New("this is not a login")
	}
	return sshLogin{User: m.User, Algorithm: m.Algorithm, Key: m.Key}, nil
}

// sshRequest is what a node asks for a task.
type sshRequest struct {
	Op     string `json:"op"`
	Key    []byte `json:"key,omitempty"`    // sign: the public key, as the agent lists it
	Data   []byte `json:"data,omitempty"`   // sign: what to sign
	Target string `json:"target,omitempty"` // connect: host:port
}

type sshKey struct {
	Key     []byte `json:"key"`
	Comment string `json:"comment"`
}

// sshAnswer is eriphe-core's answer: the keys, or a signature in the
// agent's format.
type sshAnswer struct {
	Keys      []sshKey `json:"keys,omitempty"`
	Signature []byte   `json:"signature,omitempty"`
}

// lentSSH are the SSH accesses lent to a task, by name.
func (c *core) lentSSH(t *TaskRow) map[string]Access {
	out := map[string]Access{}
	conf := c.conf()
	for _, e := range t.Resources {
		if a, ok := conf.Accesses[e.Ref]; e.Kind == resAccess && ok && a.isSSH() {
			out[e.Ref] = a
		}
	}
	return out
}

// taskSSHHandler serves /v1/node/tasks/{id}/ssh, for nodes.
func (c *core) taskSSHHandler() http.Handler {
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
		var req sshRequest
		if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		var a sshAnswer
		switch req.Op {
		case sshOpKeys:
			a.Keys = c.sshKeys(t)
		case sshOpSign:
			a.Signature, err = c.sshSign(t, req.Key, req.Data)
		case sshOpConnect:
			err = c.sshConnect(t, req.Target)
		default:
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusForbidden)
			return
		}
		writeJSON(w, http.StatusOK, a)
	})
}

// sshKeys are the public keys a task may use now.
func (c *core) sshKeys(t *TaskRow) []sshKey {
	out := []sshKey{}
	for name, a := range c.lentSSH(t) {
		if c.accessUsable(t, name) != nil {
			continue
		}
		s, err := a.sshSigner()
		if err != nil {
			log.Printf("access: %s: %v", name, err)
			continue
		}
		out = append(out, sshKey{Key: s.PublicKey().Marshal(), Comment: name})
	}
	return out
}

// sshUse logs a use of an access, and tells why it was refused, if it was.
func (c *core) sshUse(t *TaskRow, name, host, what string, why error) error {
	decision, reason := decideAllow, what
	if why != nil {
		decision, reason = decideDeny, truncate(oneLine(why.Error()), 300)
		log.Printf("access: task %s: %s on %s refused: %s", t.ID, what, host, reason)
	}
	c.store.logCredential(t.ID, accessLogTool, host, name, accessSSH, decision, byPolicy, "", reason)
	return why
}

// sshSign signs a login with the key of an access lent to the task.
func (c *core) sshSign(t *TaskRow, key, data []byte) ([]byte, error) {
	for name, a := range c.lentSSH(t) {
		s, err := a.sshSigner()
		if err != nil {
			log.Printf("access: %s: %v", name, err)
			continue
		}
		if !bytes.Equal(s.PublicKey().Marshal(), key) {
			continue
		}
		target := a.sshTarget()
		if err := c.accessUsable(t, name); err != nil {
			return nil, c.sshUse(t, name, target, "login", err)
		}
		login, err := parseSSHLogin(data)
		if err != nil {
			return nil, c.sshUse(t, name, target, "signature", fmt.Errorf("the access %s signs logins only: %w", name, err))
		}
		if !bytes.Equal(login.Key, key) {
			return nil, c.sshUse(t, name, target, "signature", fmt.Errorf("the access %s signs for its own key only", name))
		}
		if login.User != a.User {
			return nil, c.sshUse(t, name, target, "login", fmt.Errorf("the access %s logs in as %s, not as another account", name, a.User))
		}
		sig, err := s.Sign(rand.Reader, data)
		if err != nil {
			return nil, c.sshUse(t, name, target, "login", errors.New("the signature failed"))
		}
		c.sshUse(t, name, target, "login as "+a.User, nil)
		return ssh.Marshal(sig), nil
	}
	return nil, errors.New("no SSH access of this task has this key")
}

// sshConnect tells whether the relay may open a tunnel to a target for the
// task: an access lent to it goes there, and it may use it now.
func (c *core) sshConnect(t *TaskRow, target string) error {
	for name, a := range c.lentSSH(t) {
		if a.sshTarget() != target {
			continue
		}
		return c.sshUse(t, name, target, "connection", c.accessUsable(t, name))
	}
	return errors.New("no SSH access of this task goes to " + target)
}

// testSSH connects once to the access's machine from eriphe-core, with its
// key: that confirms the key is in place. A host key not pinned yet is
// recorded then, and a pinned one must be the machine's. A machine only a
// node reaches fails here: its host key can be pasted on the page.
func (c *core) testSSH(ctx context.Context, name string) (hostKey string, err error) {
	a, ok := c.conf().Accesses[name]
	if !ok || !a.isSSH() {
		return "", errNoAccess(name)
	}
	signer, err := a.sshSigner()
	if err != nil {
		return "", err
	}
	config := &ssh.ClientConfig{User: a.User, Auth: []ssh.AuthMethod{ssh.PublicKeys(signer)}, Timeout: sshTestTimeout,
		HostKeyCallback: func(_ string, _ net.Addr, k ssh.PublicKey) error {
			hostKey = hostKeyLine(k)
			if a.HostKey != "" && a.HostKey != hostKey {
				return errSSHHostKey
			}
			return nil
		}}
	if a.HostKey != "" {
		if k, _, _, _, err := ssh.ParseAuthorizedKey([]byte(a.HostKey)); err == nil {
			config.HostKeyAlgorithms = []string{k.Type()}
		}
	}
	ctx, cancel := context.WithTimeout(ctx, sshTestTimeout)
	defer cancel()
	target := a.sshTarget()
	conn, err := sshDial(ctx, target)
	if err != nil {
		return "", fmt.Errorf("%w: %v", errSSHUnreachable, err)
	}
	defer conn.Close()
	if d, ok := ctx.Deadline(); ok {
		conn.SetDeadline(d)
	}
	sc, chans, reqs, err := ssh.NewClientConn(conn, target, config)
	if err != nil {
		switch {
		case errors.Is(err, errSSHHostKey):
			return hostKey, errSSHHostKey
		case strings.Contains(err.Error(), "unable to authenticate"):
			return "", errSSHRefused
		}
		return "", fmt.Errorf("%w: %v", errSSHUnreachable, err)
	}
	go ssh.DiscardRequests(reqs)
	go func() {
		for ch := range chans {
			ch.Reject(ssh.Prohibited, "")
		}
	}()
	sc.Close()
	if a.HostKey == "" {
		if err := c.pinSSHHostKey(name, hostKey); err != nil {
			return "", err
		}
	}
	return hostKey, nil
}

// sshDial reaches a machine from eriphe-core; replaced in tests.
var sshDial = func(ctx context.Context, target string) (net.Conn, error) {
	return (&net.Dialer{Control: sshControl}).DialContext(ctx, "tcp", target)
}

var (
	errSSHHostKey     = errors.New("the machine's host key is not the one pinned")
	errSSHRefused     = errors.New("the machine refused the key: is it in authorized_keys?")
	errSSHUnreachable = errors.New("eriphe-core cannot reach the machine")
)

// pinSSHHostKey records a workspace access's host key.
func (c *core) pinSSHHostKey(name, key string) error {
	c.catalogMu.Lock()
	defer c.catalogMu.Unlock()
	a, ok := c.dyn.Accesses[name]
	if !ok {
		return errReadOnly(kindAccess, name) // the file's: its host_key is written there
	}
	a.HostKey = key
	d := c.dyn.clone()
	d.Accesses[name] = a
	return c.commit(d, change{persist: func() error { return c.store.putCatalog(kindAccess, name, a) },
		event: fmt.Sprintf("Catalogue : l'accès SSH « %s » est prêt, sa clé de machine est enregistrée.", name)})
}
