package main

import (
	"context"
	"encoding/json"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Live sources kept fresh after a change (node_live.go reads them on a
// period): what changes a server's state is followed at once by a read
// of every live source that reads that server, so that a view never
// shows what was just deleted, archived or answered.
//
//   - A call that ran well and may change something (its tool is not
//     marked readOnlyHint) is a change of its server: the brain's own
//     calls and its tasks' (a skill's run's too, proxy.go), and a card's
//     yes (catalogCall). A read never is.
//   - The sources of its server are read again, every node's: those of
//     the account the call names (its "account" argument) and those that
//     name none; all of them when the call names none. The calls of a
//     moment are gathered: one read, a second after the last call (three
//     at most after the first).
//   - Nothing is shown done ahead of the read: an item is done only when
//     its note says so (item_notes.go), which code writes after a card's
//     yes ran on it; the read shows what its server says.
//   - A conversation's call on items of a view wakes its skill, as the
//     user's own gestures do (wakeNodeSkills): its bilan follows. A
//     task's never does (a skill's run would wake itself).
//   - A server that says one of its resources changed
//     (notifications/resources/updated) changed: its sources are read
//     again the same way, once every freshSaid at most. eriphe-core follows every resource of a server
//     whose tools a node reads, for this.
//   - What changes elsewhere (the phone's mail app) and no server tells:
//     a node's sources older than a minute are read again when its page
//     is opened, and every node's when the user comes back
//     (user.returned); the period stays.

var (
	// freshQuiet is how long a re-read waits for the next call.
	freshQuiet = time.Second
	// freshMaxWait is how long after the first call it waits at most.
	freshMaxWait = 3 * time.Second
	// freshStale is how old a source may be when its page is opened, or
	// the user comes back, and not be read again.
	freshStale = time.Minute
	// freshSaid is the least time between two reads of a server's
	// sources on its own word (notifications/resources/updated).
	freshSaid = 15 * time.Second
)

// sourceFresh is what is waiting to be read again, and when each node was
// last looked at for staleness.
type sourceFresh struct {
	mu     sync.Mutex
	asks   map[string]map[string]bool // server → the accounts changed; "" all of them
	first  time.Time
	timer  *time.Timer
	looked map[string]time.Time // node → its last staleness check
	said   map[string]time.Time // server → its last read on its own word
	later  map[string]bool      // server → a read on its word waits for its floor
}

// serverSaid is a server that said one of its resources changed: its
// sources are read again as after a call, once every freshSaid at most
// (a server may say it often); one said meanwhile is read at the end.
func (c *core) serverSaid(server string) {
	f := &c.fresh
	f.mu.Lock()
	if f.said == nil {
		f.said, f.later = map[string]time.Time{}, map[string]bool{}
	}
	now := time.Now()
	if wait := freshSaid - now.Sub(f.said[server]); wait > 0 {
		if !f.later[server] {
			f.later[server] = true
			time.AfterFunc(wait, func() {
				f.mu.Lock()
				delete(f.later, server)
				f.said[server] = time.Now()
				f.mu.Unlock()
				c.readServerSoon(server, "")
			})
		}
		f.mu.Unlock()
		return
	}
	f.said[server] = now
	f.mu.Unlock()
	c.readServerSoon(server, "")
}

// onlyReads tells a call of tool t (nil when unknown) that only reads: its
// own annotations mark it read-only, or its access policy counts it in
// Read (the user may file an unmarked tool there).
func onlyReads(t *mcp.Tool, f catalogFacts) bool {
	return f.read || t != nil && t.Annotations != nil && t.Annotations.ReadOnlyHint
}

// sourcesChanged is a call that ran well on server with input: unless it
// only reads (onlyReads), its server's sources are read again soon, the
// pages showing the items it named are told, and the nodes whose items it
// named are returned.
func (c *core) sourcesChanged(server string, reads bool, input map[string]any) []string {
	if c.nodes == nil || reads {
		return nil
	}
	account, _ := input["account"].(string)
	named := c.itemsNamed(server, account, input)
	var nodes []string
	for _, key := range slices.Sorted(maps.Keys(named)) {
		node, source := splitFreshKey(key)
		c.graphSaid(graphEvent{Kind: graphEvSource, Node: node, Name: source})
		if !slices.Contains(nodes, node) {
			nodes = append(nodes, node)
		}
	}
	c.readServerSoon(server, account)
	return nodes
}

// wakeViews wakes the skills of the views whose items a call of the
// conversation named (sourcesChanged's nodes).
func (c *core) wakeViews(nodes []string) {
	for _, id := range nodes {
		c.wakeNodeSkills(id, "call")
	}
}

// viewsNamed are the nodes whose items a call kept as JSON names, by
// the tool's Claude Code name.
func (c *core) viewsNamed(name, raw string) []string {
	server, _, ok := splitToolName(name)
	var input map[string]any
	if !ok || c.nodes == nil || json.Unmarshal([]byte(raw), &input) != nil {
		return nil
	}
	account, _ := input["account"].(string)
	var out []string
	for _, key := range slices.Sorted(maps.Keys(c.itemsNamed(server, account, input))) {
		if node, _ := splitFreshKey(key); !slices.Contains(out, node) {
			out = append(out, node)
		}
	}
	return out
}

func freshKey(node, source string) string { return node + "\x00" + source }

func splitFreshKey(key string) (node, source string) {
	node, source, _ = strings.Cut(key, "\x00")
	return node, source
}

// sourceAccount is the account a source's arguments name, "" when none.
func sourceAccount(args map[string]any) string {
	a, _ := args["account"].(string)
	return a
}

// itemsNamed are the items, among those the live sources of server list
// now (of account's, and those that name none, when account is given),
// whose id is exactly a value of input, by node and source.
func (c *core) itemsNamed(server, account string, input map[string]any) map[string][]string {
	values := map[string]bool{}
	inputValues(input, values)
	out := map[string][]string{}
	if len(values) == 0 {
		return out
	}
	for _, id := range c.nodesReading(server, account) {
		for _, r := range c.nodeReads(id) {
			if r.Source.Items == nil || r.Source.Monitor != "" || c.sourceServer(r.Source) != server || !accountMatches(account, sourceAccount(r.Source.Args)) {
				continue
			}
			for _, it := range sourceItems(r.Name, r.Source, r.Value, 0) {
				item, _ := it["id"].(string)
				key := freshKey(id, r.Name)
				if item != "" && values[item] && !slices.Contains(out[key], item) {
					out[key] = append(out[key], item)
				}
			}
		}
	}
	return out
}

// accountMatches tells whether a source of account had may have changed
// by a call on account changed: either names none, or both the same.
func accountMatches(changed, had string) bool {
	return changed == "" || had == "" || changed == had
}

// sourcesReading are the live sources of the nodes not archived whose
// tools or resources read server, the names by node: those of the
// accounts given and those that name none ("" among them: all).
func (c *core) sourcesReading(server string, accounts map[string]bool) map[string][]string {
	out := map[string][]string{}
	srcs, _ := c.nodes.Sources("")
	for _, s := range srcs {
		if s.Source.Monitor != "" || c.sourceServer(s.Source) != server {
			continue
		}
		if had := sourceAccount(s.Source.Args); !accounts[""] && had != "" && !accounts[had] {
			continue
		}
		if !slices.Contains(out[s.ID], s.Name) {
			out[s.ID] = append(out[s.ID], s.Name)
		}
	}
	return out
}

// nodesReading are the nodes whose sources read server (account's, and
// those that name none, when given).
func (c *core) nodesReading(server, account string) []string {
	return slices.Sorted(maps.Keys(c.sourcesReading(server, map[string]bool{account: true})))
}

// readServerSoon has server's sources (account's, "" all) read again once
// its calls pause: a second after the last, three at most after the
// first.
func (c *core) readServerSoon(server, account string) {
	f := &c.fresh
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.asks == nil {
		f.asks = map[string]map[string]bool{}
	}
	if f.asks[server] == nil {
		f.asks[server] = map[string]bool{}
	}
	f.asks[server][account] = true
	now := time.Now()
	if f.timer == nil {
		f.first = now
		c.live.wg.Add(1) // waited for as the reads are
		f.timer = time.AfterFunc(freshQuiet, c.readChanged)
		return
	}
	if wait := min(freshQuiet, freshMaxWait-now.Sub(f.first)); wait > 0 && f.timer.Stop() {
		f.timer.Reset(wait)
	}
}

// readChanged reads again the sources of the servers that changed.
func (c *core) readChanged() {
	defer c.live.wg.Done()
	f := &c.fresh
	f.mu.Lock()
	asks := f.asks
	f.asks, f.timer = nil, nil
	f.mu.Unlock()
	byNode := map[string][]string{}
	for _, server := range slices.Sorted(maps.Keys(asks)) {
		for id, names := range c.sourcesReading(server, asks[server]) {
			for _, n := range names {
				if !slices.Contains(byNode[id], n) {
					byNode[id] = append(byNode[id], n)
				}
			}
		}
	}
	for _, id := range slices.Sorted(maps.Keys(byNode)) {
		c.readNodeLater(context.Background(), id, byNode[id])
	}
}

// readStale reads again, in the background, a node's tools and resources
// read more than freshStale ago: its page opened, or the user back. Once
// a freshStale per node at most (a source that fails keeps its old read).
func (c *core) readStale(node string, now time.Time) {
	if c.nodes == nil {
		return
	}
	var names []string
	for _, r := range c.nodeReads(node) {
		if r.Source.Monitor == "" && r.Read > 0 && now.Sub(time.Unix(r.Read, 0)) > freshStale && !slices.Contains(names, r.Name) {
			names = append(names, r.Name)
		}
	}
	if len(names) == 0 {
		return
	}
	f := &c.fresh
	f.mu.Lock()
	if at, ok := f.looked[node]; ok && now.Sub(at) < freshStale {
		f.mu.Unlock()
		return
	}
	if f.looked == nil {
		f.looked = map[string]time.Time{}
	}
	f.looked[node] = now
	f.mu.Unlock()
	c.readNodeLater(context.Background(), node, names)
}

// readAllStale is readStale for every node that reads something: the
// user is back.
func (c *core) readAllStale(now time.Time) {
	if c.nodes == nil {
		return
	}
	srcs, _ := c.nodes.Sources("")
	var ids []string
	for _, s := range srcs {
		if !slices.Contains(ids, s.ID) {
			ids = append(ids, s.ID)
		}
	}
	for _, id := range ids {
		c.readStale(id, now)
	}
}
