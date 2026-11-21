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
//     calls and its tasks' (a skill's run's too, proxy.go), a card's yes,
//     an item's Oui and a view's action (catalogCall). A read never is.
//   - The sources of its server are read again, every node's: those of
//     the account the call names (its "account" argument) and those that
//     name none; all of them when the call names none. The calls of a
//     moment are gathered: one read, a second after the last call (three
//     at most after the first).
//   - The items of those sources whose ids the call named are shown done
//     at once (_state, list_sections.go: a List that keeps what is not
//     done lets them go), until the next read of their source that began
//     after the call: an item it still lists comes back. Nothing is kept:
//     never more than a couple of minutes, and not across a restart.
//   - A conversation's call on items of a view wakes its skill, as the
//     user's own gestures do (wakeNodeSkills): its bilan follows. A
//     task's never does (a skill's run would wake itself).
//   - A server that says one of its resources changed
//     (notifications/resources/updated) changed: its sources are read
//     again the same way. eriphe-core follows every resource of a server
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
	// leavingKept is how long an item the call named stays done when no
	// read of its source comes.
	leavingKept = 2 * time.Minute
)

// sourceFresh is what is waiting to be read again, the items shown done
// until then, and when each node was last looked at for staleness.
type sourceFresh struct {
	mu      sync.Mutex
	asks    map[string]map[string]bool // server → the accounts changed; "" all of them
	first   time.Time
	timer   *time.Timer
	leaving map[string]map[string]time.Time // node\x00source → item → when the call named it
	looked  map[string]time.Time            // node → its last staleness check
}

// onlyReads tells a tool its own annotations mark read-only.
func onlyReads(t *mcp.Tool) bool {
	return t != nil && t.Annotations != nil && t.Annotations.ReadOnlyHint
}

// sourcesChanged is a call that ran well on server's tool t (nil when
// unknown) with input: unless it only reads, the items it named are shown
// done, its server's sources are read again soon, and the nodes whose
// items it named are returned.
func (c *core) sourcesChanged(server string, t *mcp.Tool, input map[string]any) []string {
	if c.nodes == nil || onlyReads(t) {
		return nil
	}
	account, _ := input["account"].(string)
	named := c.itemsNamed(server, account, input)
	if len(named) > 0 {
		now := time.Now()
		f := &c.fresh
		f.mu.Lock()
		if f.leaving == nil {
			f.leaving = map[string]map[string]time.Time{}
		}
		for key, ids := range named {
			if f.leaving[key] == nil {
				f.leaving[key] = map[string]time.Time{}
			}
			for _, id := range ids {
				f.leaving[key][id] = now
			}
		}
		f.mu.Unlock()
	}
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

// leavingItems are the items of a node's source shown done until it is
// read again.
func (c *core) leavingItems(node, source string) map[string]bool {
	f := &c.fresh
	f.mu.Lock()
	defer f.mu.Unlock()
	key := freshKey(node, source)
	out := map[string]bool{}
	for id, at := range f.leaving[key] {
		if time.Since(at) > leavingKept {
			delete(f.leaving[key], id)
			continue
		}
		out[id] = true
	}
	if len(f.leaving[key]) == 0 {
		delete(f.leaving, key)
	}
	return out
}

// sourceReadSince is a read of a node's source that began at began: the
// items a call named before it are shown as it says, gone or back. It
// tells whether there were some.
func (c *core) sourceReadSince(node, source string, began time.Time) bool {
	f := &c.fresh
	f.mu.Lock()
	defer f.mu.Unlock()
	key := freshKey(node, source)
	had := false
	for id, at := range f.leaving[key] {
		if at.Before(began) {
			delete(f.leaving[key], id)
			had = true
		}
	}
	if len(f.leaving[key]) == 0 {
		delete(f.leaving, key)
	}
	return had
}

// readStale reads again, in the background, a node's tools and resources
// read more than freshStale ago: its page opened, or the user back. Once
// a freshStale per node at most.
func (c *core) readStale(node string, now time.Time) {
	if c.nodes == nil {
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
	var names []string
	for _, r := range c.nodeReads(node) {
		if r.Source.Monitor == "" && r.Read > 0 && now.Sub(time.Unix(r.Read, 0)) > freshStale && !slices.Contains(names, r.Name) {
			names = append(names, r.Name)
		}
	}
	if len(names) > 0 {
		c.readNodeLater(context.Background(), node, names)
	}
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
