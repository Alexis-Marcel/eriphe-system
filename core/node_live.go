package main

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Alexis-Marcel/eriphe-system/core/nodefile"
	"github.com/Alexis-Marcel/eriphe-system/core/nodestore"
)

// Live blocks (#631): a block's sources are read by code, without a
// model, as the brain's free reads are (sourceWhy): a tool with fixed
// arguments, a server's resource, or a monitor the user approved.
//
//   - The sources are the index's (node_sources): those of every node not
//     archived. Each one's reads are kept here, in core.db (node_reads):
//     its last good value, when, why the last read failed or waits, and
//     when it is read next.
//   - A tool or a resource is read at once when it is new or changed, then
//     every few minutes (every, 5 by default), and at a resource's update.
//     A monitor's value is its latest number, the last ones, whether it is
//     past its limit or failing, and its last events by kind and numbers
//     only: written again at each of its looks (monitorLooked).
//   - A read that fails or waits keeps the last good value: a block is
//     never blanked by a failure.
//   - The values are kept here only, never in the node's file: a read
//     makes no version, and the page draws the block from them (an
//     archived node's page too: its reads are kept until its source goes).
//   - A block that reads a source of a server not trusted is external,
//     checked at each read: code marks it, never unmarks it.

const nodeReadsSchema = `CREATE TABLE IF NOT EXISTS node_reads (
	node    TEXT NOT NULL,
	block   TEXT NOT NULL,
	name    TEXT NOT NULL,
	spec    TEXT NOT NULL,              -- the source as read: one changed is read again at once
	value   TEXT NOT NULL DEFAULT '',   -- its last good read, JSON
	read    INTEGER NOT NULL DEFAULT 0, -- when
	error   TEXT NOT NULL DEFAULT '',   -- why the last read failed
	waiting TEXT NOT NULL DEFAULT '',   -- why the last read was not made (sourceWhy)
	next_at INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (node, block, name)
);`

func init() { extraSchema = append(extraSchema, nodeReadsSchema) }

const (
	// nodeByCode is the author of code's own writes of a node.
	nodeByCode = "eriphe"
	// nodeEventsShown are the events of a monitor a block shows.
	nodeEventsShown = 5
	// nodeLiveTick is how often the sources are looked at.
	nodeLiveTick = 15 * time.Second
)

// nodeRead is a source's reads, as kept.
type nodeRead struct {
	nodestore.LiveSource
	Value   any // nil if none yet
	Read    int64
	Error   string
	Waiting string
	NextAt  int64
	spec    string
}

// nodeLive holds the reads under way: one pass a node at a time; one
// asked meanwhile reads it again after.
type nodeLive struct {
	busy sync.Map // node id → *again
	wg   sync.WaitGroup
}

// again is a node being read: more is set when a read is asked meanwhile.
type again struct {
	mu   sync.Mutex
	more bool
}

func sourceSpec(src nodefile.Source) string {
	raw, _ := json.Marshal(src)
	return string(raw)
}

// callOf is a tool's or a resource's source as a read takes it.
func callOf(src nodefile.Source) sourceCall {
	return sourceCall{Tool: src.Tool, Args: src.Args, Resource: src.Resource, URI: src.URI}
}

// sourceServer is the server a source reads.
func (c *core) sourceServer(src nodefile.Source) string {
	switch {
	case src.Monitor != "":
		if m, err := c.monitorNamed(src.Monitor); err == nil {
			return m.Spec.Server
		}
		return ""
	case src.Resource != "":
		return src.Resource
	}
	server, _, _ := strings.Cut(src.Tool, "/")
	return server
}

// runNodeSources is the live blocks' loop.
func (c *core) runNodeSources(ctx context.Context) {
	if c.nodes == nil {
		return
	}
	tick := time.NewTicker(nodeLiveTick)
	defer tick.Stop()
	for {
		c.nodeSourcesDue(ctx, time.Now())
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// nodeReads are the reads kept of a live node's sources, by block then
// name: what code acts on (attention, cards, skills, freshness). None for
// an archived node.
func (c *core) nodeReads(node string) []nodeRead {
	return c.readsOf(node, c.nodes.Sources)
}

// keptReads are nodeReads, an archived node's too, read no more: what
// its page and node_read show.
func (c *core) keptReads(node string) []nodeRead {
	return c.readsOf(node, c.nodes.KeptSources)
}

func (c *core) readsOf(node string, sources func(string) ([]nodestore.LiveSource, error)) []nodeRead {
	srcs, err := sources(node)
	if err != nil || len(srcs) == 0 {
		return nil
	}
	return readsWith(srcs, c.keptReadsOf(`WHERE node = ?`, node)[node])
}

// keptReadsOf are the reads kept in node_reads, by node, then by block
// and name.
func (c *core) keptReadsOf(where string, args ...any) map[string]map[string]nodeRead {
	out := map[string]map[string]nodeRead{}
	rows, err := c.store.db.Query(`SELECT node, block, name, spec, value, read, error, waiting, next_at FROM node_reads `+where, args...)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var r nodeRead
		var node, value string
		if rows.Scan(&node, &r.Block, &r.Name, &r.spec, &value, &r.Read, &r.Error, &r.Waiting, &r.NextAt) != nil {
			continue
		}
		if value != "" {
			json.Unmarshal([]byte(value), &r.Value)
		}
		if out[node] == nil {
			out[node] = map[string]nodeRead{}
		}
		out[node][r.Block+"\x00"+r.Name] = r
	}
	return out
}

// allReads are the reads kept of every node.
func (c *core) allReads() map[string]map[string]nodeRead { return c.keptReadsOf(``) }

// readsWith are a node's sources with the reads kept of them, by block
// then name; a source new, or changed since, from nothing.
func readsWith(srcs []nodestore.LiveSource, kept map[string]nodeRead) []nodeRead {
	if len(srcs) == 0 {
		return nil
	}
	out := make([]nodeRead, 0, len(srcs))
	for _, s := range srcs {
		r, ok := kept[s.Block+"\x00"+s.Name]
		if !ok || r.spec != sourceSpec(s.Source) {
			r = nodeRead{} // new, or changed: read at once, from nothing
		}
		r.LiveSource, r.spec = s, sourceSpec(s.Source)
		out = append(out, r)
	}
	return out
}

// nodeSourcesDue reads the sources whose time came, in the background,
// and forgets the reads of sources no node has any more.
func (c *core) nodeSourcesDue(ctx context.Context, now time.Time) {
	srcs, err := c.nodes.Sources("")
	if err != nil {
		log.Printf("nodes: sources: %v", err)
		return
	}
	nodes := map[string]bool{}
	var ids []string
	for _, s := range srcs {
		nodes[s.ID] = true
		if !slices.Contains(ids, s.ID) {
			ids = append(ids, s.ID)
		}
	}
	c.forgetReads()
	c.forgetGlances(nodes)
	c.followNodeResources()
	for _, id := range ids {
		due := false
		for _, r := range c.nodeReads(id) {
			due = due || r.Source.Monitor != "" || r.spec == "" || r.Read == 0 && r.Error == "" && r.Waiting == "" || r.NextAt <= now.Unix()
		}
		if due {
			c.readNodeLater(ctx, id, nil)
		}
	}
}

// forgetReads drops the reads of the sources no node has any more; an
// archived node's are kept, for its page.
func (c *core) forgetReads() {
	kept, err := c.nodes.KeptSources("")
	if err != nil {
		return
	}
	live := map[string]bool{}
	for _, s := range kept {
		live[s.ID+"\x00"+s.Block+"\x00"+s.Name] = true
	}
	rows, err := c.store.db.Query(`SELECT node, block, name FROM node_reads`)
	if err != nil {
		return
	}
	var gone [][3]string
	for rows.Next() {
		var k [3]string
		if rows.Scan(&k[0], &k[1], &k[2]) == nil && !live[k[0]+"\x00"+k[1]+"\x00"+k[2]] {
			gone = append(gone, k)
		}
	}
	rows.Close()
	for _, k := range gone {
		c.store.db.Exec(`DELETE FROM node_reads WHERE node = ? AND block = ? AND name = ?`, k[0], k[1], k[2])
	}
}

// readNodeLater reads a node's sources (those named in only, if given) in
// the background.
func (c *core) readNodeLater(ctx context.Context, node string, only []string) {
	c.live.wg.Add(1)
	go func() {
		defer c.live.wg.Done()
		c.readNode(ctx, node, only, time.Now())
	}()
}

// readNode reads a node's sources that are due (those named in only, all
// of them, if given). One read of a node at a time: one asked meanwhile reads all
// of its sources after.
func (c *core) readNode(ctx context.Context, node string, only []string, now time.Time) {
	a := &again{}
	if v, busy := c.live.busy.LoadOrStore(node, a); busy {
		x := v.(*again)
		x.mu.Lock()
		x.more = true
		x.mu.Unlock()
		return
	}
	defer c.live.busy.Delete(node)
	force := only != nil
	for {
		c.readNodeOnce(ctx, node, only, force, now)
		a.mu.Lock()
		more := a.more
		a.more = false
		a.mu.Unlock()
		if !more {
			return
		}
		only, force, now = nil, true, time.Now()
	}
}

func (c *core) readNodeOnce(ctx context.Context, node string, only []string, force bool, now time.Time) {
	reads := c.nodeReads(node)
	srcs := map[string]sourceCall{}
	for _, r := range reads {
		key := r.Block + "/" + r.Name
		switch {
		case r.Source.Monitor != "":
			continue
		case only != nil && !slices.Contains(only, r.Name):
			continue
		case !force && r.spec != "" && r.Read > 0 && r.NextAt > now.Unix():
			continue
		}
		srcs[key] = callOf(r.Source)
	}
	got := c.readSources(ctx, srcs, nil)
	was := make([]string, len(reads))
	for i, r := range reads {
		if r.Value != nil {
			was[i] = valueHash(r.Value)
		}
	}
	for i, r := range reads {
		if r.Source.Monitor != "" {
			reads[i] = c.monitorRead(r, now)
			continue
		}
		res, ok := got[r.Block+"/"+r.Name]
		if !ok {
			continue
		}
		r.NextAt = now.Add(time.Duration(cmp.Or(r.Source.Every, sourceEveryDef)) * time.Minute).Unix()
		r.Error, r.Waiting = res.err, res.why
		if res.err == "" && res.why == "" {
			r.Value, r.Read = res.value, now.Unix()
		}
		reads[i] = r
	}
	for _, r := range reads {
		value := ""
		if r.Value != nil {
			raw, _ := json.Marshal(r.Value)
			value = string(raw)
		}
		c.store.db.Exec(`INSERT INTO node_reads (node, block, name, spec, value, read, error, waiting, next_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (node, block, name) DO UPDATE SET spec = excluded.spec, value = excluded.value, read = excluded.read,
			  error = excluded.error, waiting = excluded.waiting, next_at = excluded.next_at`,
			node, r.Block, r.Name, r.spec, value, r.Read, r.Error, r.Waiting, r.NextAt)
	}
	// The pages that show it get the new values at once (graph_feed.go),
	// and the items a call named before this read began as it says them:
	// gone, or back (source_fresh.go).
	for i, r := range reads {
		_, read := got[r.Block+"/"+r.Name]
		settled := read && c.sourceReadSince(node, r.Name, now)
		if settled || r.Value != nil && valueHash(r.Value) != was[i] {
			c.graphSaid(graphEvent{Kind: graphEvSource, Node: node, Block: r.Block, Name: r.Name})
		}
	}
	c.noteAttention(node, reads)   // node_attention.go
	c.noteGlance(node, reads, now) // node_glance.go
	c.markUntrusted(node)
}

// valueHash is a value's hash, to tell a value changed.
func valueHash(v any) string {
	raw, _ := json.Marshal(v)
	h := sha256.Sum256(raw)
	return hex.EncodeToString(h[:])
}

// markUntrusted marks external the blocks of a node that read a server
// not trusted: one version of code's, once, none when there is none.
func (c *core) markUntrusted(node string) {
	for try := 0; try < 3; try++ {
		d, err := c.nodes.Read(node, 0)
		if err != nil || !d.Node.Meta.Archived.IsZero() {
			return
		}
		var mark []string
		for _, b := range d.Node.Blocks() {
			if !b.External && c.untrustedSources(b) {
				mark = append(mark, b.Name)
			}
		}
		if len(mark) == 0 {
			return
		}
		_, err = c.nodes.Change(node, d.Version.Hash, nodeByCode, func(n *nodefile.Node) error {
			for _, block := range mark {
				n.SetBlockExternal(block, true)
			}
			return nil
		})
		if errors.Is(err, nodestore.ErrConflict) {
			continue
		}
		if err != nil {
			log.Printf("node %s: blocks not marked external: %v", node, err)
		}
		return
	}
}

// monitorRead is a monitor's value, from its last looks: its number and
// the last ones, whether it is past its limit or failing, and its last
// events by kind and numbers only (never a changed value, a new item or
// an error's text, which may be someone else's words).
func (c *core) monitorRead(r nodeRead, now time.Time) nodeRead {
	m, err := c.monitorNamed(r.Source.Monitor)
	switch {
	case err != nil:
		r.Waiting = "no monitor " + r.Source.Monitor
		return r
	case m.State != monitorOn:
		r.Waiting = "the monitor " + m.Name + " is " + m.State
		return r
	}
	r.Waiting, r.Error = m.Look.Waiting, m.Look.Error
	points := []any{}
	var last any
	for _, p := range m.Look.Points {
		points = append(points, map[string]any{"at": time.Unix(p.At, 0).Format(time.RFC3339), "v": p.V})
		last = p.V
	}
	events := []any{}
	for _, e := range c.monitorEvents(m.Name, nodeEventsShown) {
		ev := map[string]any{"kind": strings.TrimPrefix(e.Kind, "monitor."), "at": e.At.Format(time.RFC3339)}
		if e.Kind == evMonitorCrossed {
			for _, k := range []string{"value", "above", "below", "holds"} {
				if v, ok := e.Payload[k]; ok {
					ev[k] = v
				}
			}
		}
		events = append(events, ev)
	}
	v := map[string]any{"name": m.Name, "value": last, "points": points, "past": m.Look.Hit, "failing": m.Look.Failed, "events": events}
	if r.Error == "" && r.Waiting == "" || r.Value == nil {
		r.Value, r.Read = v, now.Unix()
	}
	return r
}

// nodesFollowing are the nodes not archived whose blocks read a monitor.
func (c *core) nodesFollowing(monitor string) []string {
	if c.nodes == nil {
		return nil
	}
	srcs, _ := c.nodes.Sources("")
	var out []string
	for _, s := range srcs {
		if s.Source.Monitor == monitor && !slices.Contains(out, s.ID) {
			out = append(out, s.ID)
		}
	}
	return out
}

// monitorLooked writes again the blocks that read a monitor, once its
// look is saved: its number, its events, its state.
func (c *core) monitorLooked(name string) {
	for _, id := range c.nodesFollowing(name) {
		c.readNodeLater(context.Background(), id, []string{}) // its monitors only: no tool nor resource is named
	}
}

// monitorRaise raises the nodes that read a monitor when it crosses its
// limit or fails: each comes to the user, in code's words, without a
// model speaking. A skill reacts to its events as before.
func (c *core) monitorRaise(m *monitor, kind string, p map[string]any) {
	holds, _ := p["holds"].(bool)
	if kind != evMonitorFailed && !(kind == evMonitorCrossed && holds) {
		return
	}
	key := "node.monitor.failed"
	if kind == evMonitorCrossed {
		key = "node.monitor.crossed"
	}
	for _, id := range c.nodesFollowing(m.Name) {
		lang := c.userLang()
		title, ext := c.titleOfNode(id) // shown, marked when it may be someone else's words; its id when none
		c.attend(attentionItem{Ref: nodeRef(id), Weight: weightBlocksATask, Why: fmt.Sprintf(tr(lang, key), m.Name, cmp.Or(markedTitle(title, ext, lang), id)),
			Source: "monitor:" + m.Name, tainted: !c.trustsServer(m.Spec.Server) || ext})
	}
}

// nodeRef is how a record names a node.
func nodeRef(id string) string { return "node:" + id }

// nodeResourceUpdated is the pool's hook: a server said a resource
// changed. Its state changed: the blocks that read it read it again,
// whatever they read of it, a tool or a resource (source_fresh.go).
func (c *core) nodeResourceUpdated(server, uri string) {
	if c.nodes == nil {
		return
	}
	c.serverSaid(server)
}

// followNodeResources asks each server for the updates of the resources
// the nodes read, and of all its resources when the nodes read its
// tools: what it says changed is read again (source_fresh.go).
func (c *core) followNodeResources() {
	if c.pool == nil {
		return
	}
	want := map[string][]string{}
	for name := range c.conf().Servers {
		want[name] = nil
	}
	for server, uris := range c.nodeResources() {
		want[server] = append(want[server], uris...)
	}
	for _, server := range c.toolServers() {
		if u := c.pool.get(server); u != nil {
			want[server] = append(want[server], u.resourceURIs()...)
		}
	}
	for name, uris := range want {
		if u := c.pool.get(name); u != nil {
			u.follow(uris)
		}
	}
}

// nodeResources are the resources the nodes read, by server.
func (c *core) nodeResources() map[string][]string {
	out := map[string][]string{}
	if c.nodes == nil {
		return out
	}
	srcs, _ := c.nodes.Sources("")
	for _, s := range srcs {
		if s.Source.Resource != "" && !slices.Contains(out[s.Source.Resource], s.Source.URI) {
			out[s.Source.Resource] = append(out[s.Source.Resource], s.Source.URI)
		}
	}
	return out
}

// toolServers are the servers whose tools the nodes read.
func (c *core) toolServers() []string {
	var out []string
	if c.nodes == nil {
		return out
	}
	srcs, _ := c.nodes.Sources("")
	for _, s := range srcs {
		if server, _, ok := strings.Cut(s.Source.Tool, "/"); ok && !slices.Contains(out, server) {
			out = append(out, server)
		}
	}
	return out
}

// nodeValueShown is the most bytes of one source's value node_read gives
// (its JSON): one longer is said to be so, never cut in the middle.
const nodeValueShown = 16 << 10

// nodeValues are the last good values of a node's sources as node_read
// gives them, by block then source (nil when none), and whether one of
// them comes from a server not trusted: its words may be someone else's,
// whether or not its block was marked yet.
func (c *core) nodeValues(n *nodefile.Node) (map[string]map[string]any, bool) {
	var out map[string]map[string]any
	for _, r := range c.keptReads(n.Meta.ID) {
		if r.Value == nil {
			continue
		}
		if out == nil {
			out = map[string]map[string]any{}
		}
		if out[r.Block] == nil {
			out[r.Block] = map[string]any{}
		}
		var v any = r.Value
		if raw, _ := json.Marshal(r.Value); len(raw) > nodeValueShown {
			v = fmt.Sprintf("(%d KiB: too long to give here, the page shows it)", len(raw)>>10)
		}
		out[r.Block][r.Name] = v
	}
	untrusted := false
	for _, b := range n.Blocks() {
		untrusted = untrusted || out[b.Name] != nil && c.untrustedSources(b)
	}
	return out, untrusted
}

// nodeValue is the last good value of a node's source, by its name (in
// any of its blocks), nil if none.
func (c *core) nodeValue(node, name string) (nodeRead, bool) {
	for _, r := range c.nodeReads(node) {
		if r.Name == name {
			return r, true
		}
	}
	return nodeRead{}, false
}
