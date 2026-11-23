package main

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The bench's replay (#707): the node tools' calls a real brain made in a
// run of the live bench (nodes_bench_live_test.go), recorded with what
// each was given and what code answered, played again against the node
// tools of the scenario's core without a model. Code's rules on what a
// node may hold (the blocks' grammar, a card placed by its ref, nothing
// asked after an external read) answer the same today, or the test says
// which call changed.
//
// The recording is testdata/nodes_replay.json, written by the live bench
// with ERIPHE_LIVE_NODES_RECORD=testdata/nodes_replay.json. Ids, hashes
// and card refs differ from run to run: they are recorded as placeholders
// ($day, $substack, $mails and $mail for the scenario's nodes, $n1… for
// those a recorded call opened, $c1… for its cards, $hash for the last
// version, $stale for an older one), and a call that names a node code
// opened ($x1…: a task's, a view's) is not played.

// replayTools are the calls recorded: those that write a node or propose
// what a node shows.
var replayTools = []string{"node_open", "node_write", "node_edit", "node_set", "propose_view", "item_note"}

// replayCall is one recorded call.
type replayCall struct {
	Turn    int               `json:"turn"`
	Tainted bool              `json:"tainted,omitempty"`
	About   string            `json:"about,omitempty"`
	Talk    string            `json:"talk,omitempty"`
	Tool    string            `json:"tool"`
	Args    json.RawMessage   `json:"args"`
	OK      bool              `json:"ok"`
	Error   string            `json:"error,omitempty"`
	Gives   map[string]string `json:"gives,omitempty"` // what its result named: id, card, steps.<key>, each a placeholder
}

// replayNames maps real ids and refs to placeholders, and back.
type replayNames struct {
	mu   sync.Mutex
	to   map[string]string // real → placeholder
	from map[string]string // placeholder → real
	n    map[string]int    // the last number of each kind
}

func newReplayNames(seed map[string]string) *replayNames {
	r := &replayNames{to: map[string]string{}, from: map[string]string{}, n: map[string]int{}}
	for name, id := range seed {
		r.set(id, "$"+name)
	}
	return r
}

func (r *replayNames) set(real, ph string) {
	r.to[real], r.from[ph] = ph, real
}

// fresh gives a new placeholder of kind for real.
func (r *replayNames) fresh(kind, real string) string {
	if ph, ok := r.to[real]; ok {
		return ph
	}
	r.n[kind]++
	ph := fmt.Sprintf("$%s%d", kind, r.n[kind])
	r.set(real, ph)
	return ph
}

// swap replaces in v every key of names by its value, longest first: in
// strings, whole or inside text (an id, a card's ref).
func swap(v any, names map[string]string) any {
	keys := slices.Collect(maps.Keys(names))
	sort.Slice(keys, func(i, j int) bool { return len(keys[i]) > len(keys[j]) })
	var walk func(any) any
	walk = func(v any) any {
		switch x := v.(type) {
		case string:
			for _, k := range keys {
				if k != "" && strings.Contains(x, k) {
					x = strings.ReplaceAll(x, k, names[k])
				}
			}
			return x
		case []any:
			for i := range x {
				x[i] = walk(x[i])
			}
			return x
		case map[string]any:
			for k := range x {
				x[k] = walk(x[k])
			}
			return x
		}
		return v
	}
	return walk(v)
}

// recordArgs is a call's arguments with placeholders: a node id this
// recording has not seen opened is code's ($x…), expect says whether it
// named the last version.
func (r *replayNames) recordArgs(args map[string]any, lastHash func(id string) string) json.RawMessage {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, k := range []string{"id", "parent", "node"} {
		if id, ok := args[k].(string); ok && id != "" {
			if _, known := r.to[id]; !known {
				r.fresh("x", id)
			}
		}
	}
	if exp, ok := args["expect"].(string); ok {
		id, _ := args["id"].(string)
		if exp == lastHash(id) {
			args["expect"] = "$hash"
		} else {
			args["expect"] = "$stale"
		}
	}
	raw, _ := json.Marshal(swap(args, r.to))
	return raw
}

// gives names what a call's result opened or proposed.
func (r *replayNames) gives(tool string, out map[string]any) map[string]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	g := map[string]string{}
	if id, _ := out["id"].(string); id != "" && tool == "node_open" {
		g["id"] = r.fresh("n", id)
	}
	if card, _ := out["card"].(string); card != "" {
		g["card"] = r.fresh("c", card)
	}
	if steps, ok := out["steps"].(map[string]any); ok {
		for key, v := range steps {
			if id, _ := v.(string); id != "" {
				g["steps."+key] = r.fresh("n", id)
			}
		}
	}
	if len(g) == 0 {
		return nil
	}
	return g
}

// placeholder tells a real id's placeholder, "" if none.
func (r *replayNames) placeholder(id string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.to[id]
}

// TestNodesReplay plays the recorded calls again on the scenario's core.
func TestNodesReplay(t *testing.T) {
	raw, err := os.ReadFile("testdata/nodes_replay.json")
	if err != nil {
		t.Fatal(err)
	}
	var calls []replayCall
	if err := json.Unmarshal(raw, &calls); err != nil {
		t.Fatal(err)
	}
	ctx, stop := context.WithCancel(context.Background())
	t.Cleanup(stop)
	sc := newScenario(t, ctx)
	c := sc.c
	mail, err := sc.mail()
	if err != nil {
		t.Fatal(err)
	}
	seed := map[string]string{"mail": mail}
	for _, k := range []string{"day", "substack", "mails"} {
		seed[k] = sc.nodes[k]
	}
	names := newReplayNames(seed)
	sess := connectServer(t, newMCPServer(c))
	ran, refused := 0, 0
	for i, call := range calls {
		if strings.Contains(string(call.Args), "$x") || call.About == "$x" {
			continue // a node code opened in the live run: not here
		}
		var args map[string]any
		json.Unmarshal(call.Args, &args)
		if old, _ := args["old"].(string); old != "" {
			continue // a piece of text replaced: it depends on what workers and code wrote, not played
		}
		if exp, ok := args["expect"].(string); ok {
			args["expect"] = "stale"
			if exp == "$hash" {
				args["expect"] = hashOf(t, c, names.from[args["id"].(string)])
			}
		}
		args = swap(args, names.from).(map[string]any)
		c.turn.start(0, 0, 0)
		c.turn.about(names.from[call.About])
		c.turn.answersIn(names.from[call.Talk])
		if call.Tainted {
			c.turn.taint("replay")
		}
		out, msg := callReplay(t, sess, call.Tool, args)
		ran++
		if msg != "" {
			refused++
		}
		if (msg == "") != call.OK {
			t.Errorf("call %d (turn %d, %s): recorded ok=%v %q, now %q", i, call.Turn, call.Tool, call.OK, call.Error, msg)
			continue
		}
		for what, ph := range call.Gives {
			var real string
			switch {
			case what == "id" || what == "card":
				real, _ = out[what].(string)
			case strings.HasPrefix(what, "steps."):
				steps, _ := out["steps"].(map[string]any)
				real, _ = steps[strings.TrimPrefix(what, "steps.")].(string)
			}
			if real != "" {
				names.mu.Lock()
				names.set(real, ph)
				names.mu.Unlock()
			}
		}
	}
	if ran < 10 || refused == 0 {
		t.Fatalf("%d calls played, %d refused: the recording should hold a run's writes, refusals among them", ran, refused)
	}
	// What the calls left holds to the rules whatever was played: a card
	// block shows its card, no external block asks.
	for _, sum := range liveSummaries(t, c) {
		d, err := c.nodes.Read(sum.ID, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, b := range d.Node.Blocks() {
			if b.Card != "" && (b.External || !c.nodeHasCard(sum.ID, b.Card)) {
				t.Errorf("%s: block %s places %s, which the page cannot draw", sum.Title, b.Name, b.Card)
			}
			if b.External && blockAsks(b.Components) {
				t.Errorf("%s: external block %s asks", sum.Title, b.Name)
			}
		}
	}
	t.Logf("%d calls played, %d refused as recorded", ran, refused)
}

// callReplay calls a tool: its result, or the refusal's words.
func callReplay(t *testing.T, sess *mcp.ClientSession, name string, args map[string]any) (map[string]any, string) {
	t.Helper()
	res, err := sess.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		return nil, err.Error()
	}
	if res.IsError {
		return nil, resultText(res)
	}
	var out map[string]any
	raw, _ := json.Marshal(res.StructuredContent)
	json.Unmarshal(raw, &out)
	return out, ""
}
