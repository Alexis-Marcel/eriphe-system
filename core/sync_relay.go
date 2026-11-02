package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Copies of a shared folder on the user's other machines are kept by
// Syncthing, one on each machine, next to its node (node/syncthing.go). The
// machines never reach one another directly: Syncthing listens on the
// loopback only, without discovery nor relays, and its connections go
// through eriphe-core, on the tunnels the nodes already keep open.
//
//	Syncthing A → node A:      tcp://127.0.0.1:<port of B>   (a loopback port per peer)
//	node A → eriphe-core:      GET /v1/node/sync/{B}, Upgrade: eriphe-sync   (node secret, TLS)
//	eriphe-core → node B:      GET /v1/sync/connect, Upgrade: eriphe-sync    (B's tunnel)
//	node B → Syncthing B:      its listening address, on the loopback
//
// eriphe-core joins the two only if it set up a copy between them (a row
// of folder_copies, whichever way): any other pair is refused, and the
// streams of a pair are cut when its last copy goes. It copies bytes and
// sees nothing else: Syncthing's TLS runs end to end, each side pinning
// the other's device ID, which the nodes reported and eriphe-core handed
// over (public keys, not secrets). A node that is not sure of eriphe-core
// still talks only to the device IDs it was given, and refuses data for
// folders it did not set up.

const syncProto = "eriphe-sync"

// syncStreamsMax bounds the connections a pair of machines holds at once:
// Syncthing keeps one, two while it replaces it.
const syncStreamsMax = 4

func init() {
	extraSchema = append(extraSchema, `
CREATE TABLE IF NOT EXISTS folder_copies (
  node TEXT NOT NULL, folder TEXT NOT NULL, copy TEXT NOT NULL, created INTEGER NOT NULL,
  PRIMARY KEY (node, folder, copy));`)
}

// folderCopy is a copy of a folder shared on node, by its id there, kept on
// the machine copy.
type folderCopy struct {
	Node    string    `json:"node"`
	Folder  string    `json:"folder"`
	Copy    string    `json:"copy"`
	Created time.Time `json:"created"`
}

func (s *Store) addCopy(node, folder, copy string) error {
	_, err := s.db.Exec(`INSERT OR IGNORE INTO folder_copies (node, folder, copy, created) VALUES (?, ?, ?, ?)`,
		node, folder, copy, time.Now().Unix())
	return err
}

func (s *Store) removeCopy(node, folder, copy string) error {
	_, err := s.db.Exec(`DELETE FROM folder_copies WHERE node = ? AND folder = ? AND copy = ?`, node, folder, copy)
	return err
}

// copies are all the copies, by folder then machine.
func (s *Store) copies() ([]folderCopy, error) {
	rows, err := s.db.Query(`SELECT node, folder, copy, created FROM folder_copies ORDER BY node, folder, copy`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []folderCopy{}
	for rows.Next() {
		var fc folderCopy
		var created int64
		if err := rows.Scan(&fc.Node, &fc.Folder, &fc.Copy, &created); err != nil {
			return nil, err
		}
		fc.Created = time.Unix(created, 0)
		out = append(out, fc)
	}
	return out, rows.Err()
}

// hasCopy tells whether the machine copy keeps a copy of node's folder.
func (s *Store) hasCopy(node, folder, copy string) bool {
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM folder_copies WHERE node = ? AND folder = ? AND copy = ?`, node, folder, copy).Scan(&n)
	return n > 0
}

// syncPaired tells whether a and b share a copy, whichever holds the folder.
func (s *Store) syncPaired(a, b string) bool {
	if a == b {
		return false
	}
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM folder_copies WHERE (node = ? AND copy = ?) OR (node = ? AND copy = ?)`,
		a, b, b, a).Scan(&n)
	return n > 0
}

// syncLinks are the connections eriphe-core joins, by pair of machines.
type syncLinks struct {
	mu    sync.Mutex
	pairs map[[2]string]map[io.Closer]bool
}

func pairKey(a, b string) [2]string {
	if a > b {
		a, b = b, a
	}
	return [2]string{a, b}
}

// hold counts one more connection of a pair, unless it has its fill.
func (l *syncLinks) hold(a, b string, c io.Closer) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	k := pairKey(a, b)
	if len(l.pairs[k]) >= syncStreamsMax {
		return false
	}
	if l.pairs == nil {
		l.pairs = map[[2]string]map[io.Closer]bool{}
	}
	if l.pairs[k] == nil {
		l.pairs[k] = map[io.Closer]bool{}
	}
	l.pairs[k][c] = true
	return true
}

func (l *syncLinks) release(a, b string, c io.Closer) {
	l.mu.Lock()
	defer l.mu.Unlock()
	k := pairKey(a, b)
	delete(l.pairs[k], c)
	if len(l.pairs[k]) == 0 {
		delete(l.pairs, k)
	}
}

// cut closes every connection of a pair.
func (l *syncLinks) cut(a, b string) {
	l.mu.Lock()
	conns := l.pairs[pairKey(a, b)]
	delete(l.pairs, pairKey(a, b))
	l.mu.Unlock()
	for c := range conns {
		c.Close()
	}
}

// cutUnpaired closes the connections of the pairs that no longer share a
// copy (after one is removed).
func (c *core) cutUnpaired() {
	c.syncLinks.mu.Lock()
	var pairs [][2]string
	for k := range c.syncLinks.pairs {
		pairs = append(pairs, k)
	}
	c.syncLinks.mu.Unlock()
	for _, k := range pairs {
		if !c.store.syncPaired(k[0], k[1]) {
			log.Printf("sync: %s and %s share no copy now, their connections are cut", k[0], k[1])
			c.syncLinks.cut(k[0], k[1])
		}
	}
}

var errSyncRefused = errors.New("these machines keep no copy for each other")

// handleNodeSync joins a node's Syncthing to a peer's, through the peer's
// tunnel: GET /v1/node/sync/{peer} with an upgrade to eriphe-sync.
func (c *core) handleNodeSync(w http.ResponseWriter, r *http.Request) {
	from, status, err := c.authNode(r)
	if err != nil {
		http.Error(w, err.Error(), status)
		return
	}
	peer := r.PathValue("peer")
	if !strings.EqualFold(r.Header.Get("Upgrade"), syncProto) {
		http.Error(w, "upgrade to "+syncProto+" required", http.StatusUpgradeRequired)
		return
	}
	if !nodeName.MatchString(peer) || !c.store.syncPaired(from, peer) || c.store.nodeSuspended(peer) {
		log.Printf("sync: refused %s → %s: no copy between them", from, peer)
		http.Error(w, errSyncRefused.Error(), http.StatusForbidden)
		return
	}
	n := c.node(peer)
	if !n.connected() {
		http.Error(w, "the machine "+peer+" is not connected", http.StatusBadGateway)
		return
	}
	up, upr, err := openSync(n, from)
	if err != nil {
		log.Printf("sync: %s → %s: %v", from, peer, err)
		http.Error(w, "the machine "+peer+" does not take the connection", http.StatusBadGateway)
		return
	}
	link := &syncLink{up: up}
	if !c.syncLinks.hold(from, peer, link) {
		up.Close()
		http.Error(w, "too many connections between these machines", http.StatusTooManyRequests)
		return
	}
	defer c.syncLinks.release(from, peer, link)
	conn, rw, err := http.NewResponseController(w).Hijack()
	if err != nil {
		up.Close()
		return
	}
	if !link.join(conn) { // cut meanwhile
		return
	}
	conn.SetDeadline(time.Time{})
	rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: " + syncProto + "\r\nConnection: Upgrade\r\n\r\n")
	if err := rw.Flush(); err != nil {
		link.Close()
		return
	}
	pipeSync(&bufferedConn{Conn: conn, r: rw.Reader}, up, upr)
}

// syncLink is one joined connection: both of its sides close together.
type syncLink struct {
	mu     sync.Mutex
	up     net.Conn // the stream to the peer
	down   net.Conn // the requesting node's connection
	closed bool
}

// join adds the requesting side, unless the link was cut meanwhile.
func (l *syncLink) join(c net.Conn) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		c.Close()
		return false
	}
	l.down = c
	return true
}

func (l *syncLink) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.closed = true
	l.up.Close()
	if l.down != nil {
		l.down.Close()
	}
	return nil
}

// openSync opens a stream to node n, asks it for its Syncthing on behalf
// of from, and returns the stream once n agreed, with what it may already
// have sent after its answer.
func openSync(n *nodeClient, from string) (net.Conn, *bufio.Reader, error) {
	n.mu.Lock()
	s := n.session
	n.mu.Unlock()
	if s == nil {
		return nil, nil, errOffline
	}
	st, err := s.Open()
	if err != nil {
		return nil, nil, err
	}
	st.SetDeadline(time.Now().Add(15 * time.Second))
	fmt.Fprintf(st, "GET /v1/sync/connect HTTP/1.1\r\nHost: node\r\nConnection: Upgrade\r\nUpgrade: %s\r\nEriphe-Sync-Peer: %s\r\n\r\n", syncProto, from)
	br := bufio.NewReader(st)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		st.Close()
		return nil, nil, err
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		resp.Body.Close()
		st.Close()
		return nil, nil, fmt.Errorf("%s: %s", resp.Status, strings.TrimSpace(string(msg)))
	}
	st.SetDeadline(time.Time{})
	return st, br, nil
}

// pipeSync copies both ways until either side ends.
func pipeSync(a net.Conn, b net.Conn, br io.Reader) {
	done := make(chan struct{}, 2)
	go func() { io.Copy(b, a); done <- struct{}{} }()
	go func() { io.Copy(a, br); done <- struct{}{} }()
	<-done
	a.Close()
	b.Close()
	<-done
}
