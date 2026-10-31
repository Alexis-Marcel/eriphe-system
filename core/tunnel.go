package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/hashicorp/yamux"
)

// Nodes dial eriphe-core, never the reverse, like kubelets dial the API
// server: a node needs no open port, and works behind NAT or WSL.
//
//  1. An admin creates a join token (POST /v1/join-tokens).
//  2. The node joins once with it (POST /v1/nodes/join) and gets its own secret.
//  3. The node connects with that secret (GET /v1/nodes/connect, HTTP upgrade).
//     The connection becomes a yamux session on which eriphe-core opens
//     streams and speaks the node's HTTP API.

const tunnelProto = "eriphe-tunnel"

var nodeName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

func randomSecret() string {
	b := make([]byte, 24)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func hashSecret(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// newJoinToken makes a single-use token that joins a node as name, or if
// rejoin, replaces the secret of the node registered as name. A token is
// id.secret: the id names it, only its hash is kept.
func (c *core) newJoinToken(name string, rejoin bool, ttl time.Duration) (string, time.Time, error) {
	if !nodeName.MatchString(name) {
		return "", time.Time{}, errBadNodeName
	}
	switch exists := c.store.hasNode(name); {
	case exists && !rejoin:
		return "", time.Time{}, errNodeExists
	case !exists && rejoin:
		return "", time.Time{}, errNoNode
	}
	b := make([]byte, 8)
	rand.Read(b)
	id := hex.EncodeToString(b)
	token := id + "." + randomSecret()
	expires := time.Now().Add(ttl)
	err := c.store.addJoinToken(hashSecret(token), joinToken{ID: id, Name: name, Rejoin: rejoin, Expires: expires})
	return token, expires, err
}

var (
	errBadNodeName = errors.New("a node name of lowercase letters, digits and dashes (1 to 32) is required")
	errNoNode      = errors.New("no node is registered under this name: make a join token instead")
)

// handleJoinToken creates a single-use join token for a node name (admin
// only): ?name=pc to add a node, ?name=pc&rejoin=1 to let an existing node
// join again with a new secret.
func (c *core) handleJoinToken(w http.ResponseWriter, r *http.Request) {
	ttl := 24 * time.Hour
	if d, err := time.ParseDuration(r.URL.Query().Get("ttl")); err == nil && d > 0 {
		ttl = d
	}
	name, rejoin := r.URL.Query().Get("name"), r.URL.Query().Get("rejoin") == "1"
	token, expires, err := c.newJoinToken(name, rejoin, ttl)
	switch {
	case errors.Is(err, errBadNodeName):
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	case errors.Is(err, errNodeExists):
		http.Error(w, "a node is already registered under this name: ask for a rejoin token (rejoin=1)", http.StatusConflict)
		return
	case errors.Is(err, errNoNode):
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	case err != nil:
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"token": token, "name": name, "rejoin": rejoin, "expires": expires,
		"ca_hash": c.caHash()})
}

// handleJoin registers a node with a join token and returns its own secret.
// The token decides the name: a node joins only under the name its token was
// made for, and an existing node's secret is replaced only with a rejoin
// token for it.
func (c *core) handleJoin(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name  string `json:"name"`
		Token string `json:"token"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&in); err != nil || !nodeName.MatchString(in.Name) {
		http.Error(w, "a name of lowercase letters, digits and dashes is required", http.StatusBadRequest)
		return
	}
	if r.TLS == nil && c.nodesTLSOnly() {
		http.Error(w, errNodeTLSRequired.Error(), http.StatusForbidden)
		return
	}
	secret := randomSecret()
	rejoined, err := c.store.joinNode(hashSecret(in.Token), in.Name, hashSecret(secret))
	switch {
	case errors.Is(err, errJoinToken):
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	case errors.Is(err, errJoinName):
		log.Printf("join refused: a token for another name used for %s", in.Name)
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	case errors.Is(err, errNodeExists):
		log.Printf("join refused: %s is already registered", in.Name)
		http.Error(w, err.Error(), http.StatusConflict)
		return
	case err != nil:
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	c.disconnect(in.Name)
	if rejoined {
		log.Printf("node %s joined again, with a new secret", in.Name)
	} else {
		log.Printf("node %s joined", in.Name)
	}
	writeJSON(w, http.StatusOK, map[string]string{"name": in.Name, "secret": secret})
}

// handleConnect turns a node's request into the tunnel to that node.
func (c *core) handleConnect(w http.ResponseWriter, r *http.Request) {
	name, status, err := c.authNode(r)
	if err != nil {
		http.Error(w, err.Error(), status)
		return
	}
	if !strings.EqualFold(r.Header.Get("Upgrade"), tunnelProto) {
		http.Error(w, "upgrade to "+tunnelProto+" required", http.StatusUpgradeRequired)
		return
	}
	conn, rw, err := http.NewResponseController(w).Hijack()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	conn.SetDeadline(time.Time{})
	rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: " + tunnelProto + "\r\nConnection: Upgrade\r\n\r\n")
	if err := rw.Flush(); err != nil {
		conn.Close()
		return
	}
	session, err := yamux.Client(&bufferedConn{Conn: conn, r: rw.Reader}, tunnelConfig())
	if err != nil {
		conn.Close()
		return
	}
	c.attach(name, session, r.TLS != nil && len(r.TLS.PeerCertificates) > 0)
}

// bufferedConn reads what the HTTP server may already have buffered.
type bufferedConn struct {
	net.Conn
	r *bufio.Reader
}

func (b *bufferedConn) Read(p []byte) (int, error) { return b.r.Read(p) }

func tunnelConfig() *yamux.Config {
	cfg := yamux.DefaultConfig()
	cfg.KeepAliveInterval = 15 * time.Second
	cfg.LogOutput = io.Discard
	return cfg
}

// attach makes session the way to reach a node, replacing any older one,
// and marks the node offline when it closes. withCert says whether the node
// connected with its certificate (machine_tls.go).
func (c *core) attach(name string, session *yamux.Session, withCert bool) {
	n := c.node(name)
	n.mu.Lock()
	old := n.session
	n.session, n.tls = session, withCert
	n.mu.Unlock()
	if old != nil {
		old.Close()
	}
	how := "without TLS"
	if withCert {
		how = "over TLS"
	}
	log.Printf("node %s connected from %s, %s", name, session.RemoteAddr(), how)
	go func() {
		c.noteConnect(name, time.Now()) // node_sleep.go
		c.refreshNode(context.Background(), n)
		<-session.CloseChan()
		n.mu.Lock()
		current := n.session == session
		if current {
			n.session = nil
		}
		n.mu.Unlock()
		if current {
			log.Printf("node %s disconnected", name)
			c.store.setNodeStatus(name, false, nil)
		}
	}()
}

func (c *core) disconnect(name string) {
	n := c.node(name)
	n.mu.Lock()
	s := n.session
	n.session = nil
	n.mu.Unlock()
	if s != nil {
		s.Close()
	}
}

var errOffline = errors.New("node is not connected")

// dial opens a stream to the node, for the HTTP client.
func (n *nodeClient) dial(context.Context, string, string) (net.Conn, error) {
	n.mu.Lock()
	s := n.session
	n.mu.Unlock()
	if s == nil {
		return nil, errOffline
	}
	return s.Open()
}

// handleNodes lists the registered nodes (admin only).
func (c *core) handleNodes(w http.ResponseWriter, _ *http.Request) {
	nodes, err := c.store.nodes()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// Each with its version next to the latest one (updates.go).
	type nodeVersion struct {
		NodeRow
		versionStatus
	}
	out := []nodeVersion{}
	for _, n := range nodes {
		info := parseInfo(n)
		out = append(out, nodeVersion{n, c.versionStatus("eriphe-node", info.Version, info.Install)})
	}
	writeJSON(w, http.StatusOK, out)
}

// handleDeleteNode forgets a node: it must join again (admin only).
func (c *core) handleDeleteNode(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := c.store.deleteNode(name); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	c.disconnect(name)
	w.WriteHeader(http.StatusNoContent)
}
