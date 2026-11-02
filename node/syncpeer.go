package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The way between this machine's Syncthing and its peers' (syncthing.go):
// through eriphe-core, on the tunnels, never directly. Syncthing listens on
// the loopback only, without discovery nor relays; it reaches each peer at
// a loopback port of this node's (tcp://127.0.0.1:<port>, its static
// address there), and the node carries the connection to eriphe-core, which
// joins it to the peer's node only if it set up a copy between the two
// machines (core/sync_relay.go). The peer's node hands it to its own
// Syncthing. Syncthing's TLS runs end to end, each side pinning the other's
// device ID: eriphe-core and this node see only ciphertext.
//
//	Syncthing → this node:  127.0.0.1:<the peer's port>
//	this node → eriphe-core: GET /v1/node/sync/{peer}, Upgrade: eriphe-sync
//	eriphe-core → the peer:  GET /v1/sync/connect (its tunnel), on to its Syncthing
//
// What this node sends into its tunnel for Syncthing is held to a rate
// (-sync-limit), so that a big copy does not hold up the tasks' and
// eriphe-core's own traffic on the same connection.

const (
	syncProto     = "eriphe-sync"
	syncPortsFile = "ports.json" // under <root>/sync: each peer's port, kept from one start to the next
)

// syncNet is this node's side of the way.
type syncNet struct {
	root  string     // <root>/sync
	local string     // Syncthing's listening address, on the loopback; empty: no Syncthing here
	limit *byteRate  // what goes into the tunnel, per second; nil: no limit
	dial  syncDialer // to eriphe-core, for a peer

	mu    sync.Mutex
	peers map[string]*peerPort
	// allowed are the peers a connection may come from: the ones
	// eriphe-core named with the copies it set up.
	allowed map[string]bool
}

// syncDialer opens the connection to eriphe-core for a peer.
type syncDialer func(ctx context.Context, peer string) (net.Conn, error)

type peerPort struct {
	l    net.Listener
	port int
}

func newSyncNet(root, local string, limit int64, dial syncDialer) *syncNet {
	sn := &syncNet{root: filepath.Join(root, "sync"), local: local, dial: dial, peers: map[string]*peerPort{}, allowed: map[string]bool{}}
	if limit > 0 {
		sn.limit = newByteRate(limit)
	}
	return sn
}

// setPeers opens a loopback port for each peer, closes those of the peers
// no longer named, and returns the ports, by peer. A peer keeps its port of
// the last start when it is free.
func (sn *syncNet) setPeers(names []string) (map[string]int, error) {
	sn.mu.Lock()
	defer sn.mu.Unlock()
	want := map[string]bool{}
	for _, n := range names {
		if validPeer(n) {
			want[n] = true
		}
	}
	for name, p := range sn.peers {
		if !want[name] {
			p.l.Close()
			delete(sn.peers, name)
		}
	}
	sn.allowed = want
	saved := sn.savedPorts()
	var errs []error
	for name := range want {
		if sn.peers[name] != nil {
			continue
		}
		l, err := listenPeer(saved[name])
		if err != nil {
			errs = append(errs, fmt.Errorf("a port for %s: %w", name, err))
			continue
		}
		p := &peerPort{l: l, port: l.Addr().(*net.TCPAddr).Port}
		sn.peers[name] = p
		go sn.accept(name, l)
	}
	out := map[string]int{}
	for name, p := range sn.peers {
		out[name] = p.port
		saved[name] = p.port // a peer named again later gets its port back
	}
	sn.savePorts(saved)
	return out, errors.Join(errs...)
}

// listenPeer listens on the loopback, at port if it is free, else at any.
func listenPeer(port int) (net.Listener, error) {
	if port > 0 {
		if l, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port))); err == nil {
			return l, nil
		}
	}
	return net.Listen("tcp", "127.0.0.1:0")
}

func (sn *syncNet) savedPorts() map[string]int {
	out := map[string]int{}
	raw, err := os.ReadFile(filepath.Join(sn.root, syncPortsFile))
	if err == nil {
		json.Unmarshal(raw, &out)
	}
	return out
}

func (sn *syncNet) savePorts(ports map[string]int) {
	if err := os.MkdirAll(sn.root, 0o700); err != nil {
		return
	}
	raw, _ := json.Marshal(ports)
	tmp := filepath.Join(sn.root, syncPortsFile+".new")
	if os.WriteFile(tmp, raw, 0o600) == nil {
		os.Rename(tmp, filepath.Join(sn.root, syncPortsFile))
	}
}

// close closes every port.
func (sn *syncNet) close() {
	sn.setPeers(nil)
}

// accept carries each connection Syncthing makes to a peer's port.
func (sn *syncNet) accept(peer string, l net.Listener) {
	for {
		c, err := l.Accept()
		if err != nil {
			return
		}
		go func() {
			defer c.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			up, err := sn.dial(ctx, peer)
			cancel()
			if err != nil {
				log.Printf("sync: no way to %s: %v", peer, err)
				return
			}
			sn.pipe(c, up, up)
		}()
	}
}

// pipe copies both ways between local (Syncthing) and tunnel until either
// ends, what goes into the tunnel at the node's rate. from is what to read
// the tunnel's side from (with what was buffered of it).
func (sn *syncNet) pipe(local, tunnel net.Conn, from io.Reader) {
	done := make(chan struct{}, 2)
	go func() { copyLimited(tunnel, local, sn.limit); done <- struct{}{} }()
	go func() { io.Copy(local, from); done <- struct{}{} }()
	<-done
	local.Close()
	tunnel.Close()
	<-done
}

// copyLimited copies src to dst, at most limit's rate.
func copyLimited(dst io.Writer, src io.Reader, limit *byteRate) {
	buf := make([]byte, 32<<10)
	for {
		n, err := src.Read(buf)
		if n > 0 {
			limit.wait(n)
			if _, werr := dst.Write(buf[:n]); werr != nil {
				return
			}
		}
		if err != nil {
			return
		}
	}
}

// handleSyncConnect takes a peer's connection from eriphe-core, on the
// tunnel, and hands it to this machine's Syncthing.
func (sn *syncNet) handleSyncConnect(w http.ResponseWriter, r *http.Request) {
	if sn == nil || sn.local == "" {
		http.Error(w, "no Syncthing on this machine", http.StatusNotFound)
		return
	}
	if !strings.EqualFold(r.Header.Get("Upgrade"), syncProto) {
		http.Error(w, "upgrade to "+syncProto+" required", http.StatusUpgradeRequired)
		return
	}
	peer := r.Header.Get("Eriphe-Sync-Peer")
	sn.mu.Lock()
	ok := sn.allowed[peer]
	sn.mu.Unlock()
	if !ok {
		log.Printf("sync: refused a connection from %q: no copy with it here", peer)
		http.Error(w, "no copy with this machine here", http.StatusForbidden)
		return
	}
	local, err := net.DialTimeout("tcp", sn.local, 5*time.Second)
	if err != nil {
		http.Error(w, "Syncthing does not answer on this machine", http.StatusBadGateway)
		return
	}
	conn, rw, err := http.NewResponseController(w).Hijack()
	if err != nil {
		local.Close()
		return
	}
	conn.SetDeadline(time.Time{})
	rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: " + syncProto + "\r\nConnection: Upgrade\r\n\r\n")
	if err := rw.Flush(); err != nil {
		conn.Close()
		local.Close()
		return
	}
	sn.pipe(local, conn, rw.Reader)
}

// coreSyncDialer opens the way to a peer at eriphe-core, with this node's
// secret, as the tunnel itself is opened (join.go).
func coreSyncDialer(s *state) syncDialer {
	return func(ctx context.Context, peer string) (net.Conn, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.base()+"/v1/node/sync/"+peer, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+s.Secret)
		req.Header.Set("Eriphe-Node", s.Name)
		req.Header.Set("Connection", "Upgrade")
		req.Header.Set("Upgrade", syncProto)
		resp, err := s.dialClient().Do(req)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != http.StatusSwitchingProtocols {
			msg, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
			resp.Body.Close()
			return nil, fmt.Errorf("%s: %s", resp.Status, bytes.TrimSpace(msg))
		}
		rwc, ok := resp.Body.(io.ReadWriteCloser)
		if !ok {
			resp.Body.Close()
			return nil, errors.New("eriphe-core did not hand over the connection")
		}
		return rwcConn{rwc}, nil
	}
}

// rwcConn is the upgraded connection as a net.Conn, for the pipe.
type rwcConn struct{ io.ReadWriteCloser }

func (rwcConn) LocalAddr() net.Addr              { return &net.TCPAddr{} }
func (rwcConn) RemoteAddr() net.Addr             { return &net.TCPAddr{} }
func (rwcConn) SetDeadline(time.Time) error      { return nil }
func (rwcConn) SetReadDeadline(time.Time) error  { return nil }
func (rwcConn) SetWriteDeadline(time.Time) error { return nil }

// checkSyncthingListen: Syncthing's address, on the loopback only.
func checkSyncthingListen(addr string) error {
	if addr == "" {
		return nil
	}
	host, port, err := net.SplitHostPort(addr)
	ip := net.ParseIP(host)
	if p, perr := strconv.Atoi(port); err != nil || perr != nil || p < 1 || p > 65535 || ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("-syncthing-listen %q: an address on the loopback, e.g. 127.0.0.1:22000", addr)
	}
	return nil
}

// validPeer: a node's name, as eriphe-core allows them.
func validPeer(name string) bool {
	if name == "" || len(name) > 32 || name[0] == '-' {
		return false
	}
	for _, r := range name {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
			return false
		}
	}
	return true
}

// byteRate is a token bucket of bytes per second, shared by every
// connection of this node's Syncthing.
type byteRate struct {
	mu     sync.Mutex
	rate   float64
	tokens float64
	last   time.Time
}

func newByteRate(perSecond int64) *byteRate {
	return &byteRate{rate: float64(perSecond), tokens: float64(perSecond), last: time.Now()}
}

// wait blocks until n bytes may go.
func (b *byteRate) wait(n int) {
	if b == nil || b.rate <= 0 {
		return
	}
	b.mu.Lock()
	now := time.Now()
	b.tokens = min(b.rate, b.tokens+now.Sub(b.last).Seconds()*b.rate)
	b.last = now
	b.tokens -= float64(n)
	var sleep time.Duration
	if b.tokens < 0 {
		sleep = time.Duration(-b.tokens / b.rate * float64(time.Second))
	}
	b.mu.Unlock()
	time.Sleep(sleep)
}
