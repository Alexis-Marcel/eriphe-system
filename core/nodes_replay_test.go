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
// node may hold (the blocks' grammar, a card placed by its ref, an ask in
// its author's own words) answer the same today, or the test says which
// call changed.
//
// The recording is testdata/nodes_replay.json, written by the live bench
// with ERIPHE_LIVE_NODES_RECORD=testdata/nodes_replay.json. Ids, hashes
// and card refs differ from run to run: they are recorded as placeholders
// ($day, $substack, $mails and $mail for the scenario's nodes, $n1… for
// those a recorded call opened, $c1… for its cards, $hash for the last
// version, $stale for an older one), and a call that names a node code
// opened ($x1…: a task's) is not played.
//
// Its last turn is the bench's last: « tu peux suivre et gérer mes
// journées ? », asked at the home. Its subject opens first (node_open,
// before anything is read), then the page is written in it in that turn,
// no card: its Agenda showing the day, its choices adjustable after (ask
// buttons, in a tainted turn too), no questionnaire. Until a run records
// it, it is written by hand after a real case: the page written after a
// recall that read a mail's fact, the turn tainted. Turn 13 writes the
// mails' page at once, then proposes the skill that maintains it, its
// monitor on the same card (edited by hand too, from the views they were).

// replayTools are the calls recorded: those that write a node, or propose
// the skill that maintains one.
var replayTools = []string{"node_open", "node_write", "node_edit", "node_set", "write_skill", "item_note"}

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
	sc := newScenario(t, ctx)
	t.Cleanup(stop) // run first: the core's loops stop before its folder goes
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
			case what == "id" || what == "card" || what == "node":
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
	// block shows its card.
	for _, sum := range liveSummaries(t, c) {
		d, err := c.nodes.Read(sum.ID, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, b := range d.Node.Blocks() {
			if b.Card != "" && (b.External || !c.nodeHasCard(sum.ID, b.Card)) {
				t.Errorf("%s: block %s places %s, which the page cannot draw", sum.Title, b.Name, b.Card)
			}
		}
	}
	replayDay(t, c, calls, names)
	t.Logf("%d calls played, %d refused as recorded", ran, refused)
}

// replayDay checks the recording's last turn, the day asked to be
// followed: its first call opens the subject, untainted; the page is
// written in it in that very turn, no card, no questionnaire written
// before it: its Agenda reads the calendar and its buttons offer the
// changes, its taint marked. Nothing waits for a yes.
func replayDay(t *testing.T, c *core, calls []replayCall, names *replayNames) {
	t.Helper()
	last := calls[len(calls)-1].Turn
	var turn []replayCall
	for _, call := range calls {
		if call.Turn == last {
			turn = append(turn, call)
		}
	}
	if first := turn[0]; first.Tool != "node_open" || first.Tainted || !first.OK {
		t.Fatalf("the day's turn starts with %s (tainted %v): the subject does not open first", first.Tool, first.Tainted)
	}
	subject := names.from[turn[0].Gives["id"]]
	for _, call := range turn {
		var args map[string]any
		json.Unmarshal(call.Args, &args)
		for _, k := range []string{"body", "text", "new"} {
			if s, _ := args[k].(string); questionnaire(s) {
				t.Errorf("the day's turn: %s writes a questionnaire:\n%s", call.Tool, s)
			}
		}
	}
	d, err := c.nodes.Read(subject, 0)
	if err != nil {
		t.Fatal(err)
	}
	agenda, asks := false, false
	for _, b := range d.Node.Blocks() {
		agenda = agenda || strings.Contains(string(b.Components), `"Agenda"`) && len(b.Sources) > 0
		asks = asks || blockAsks(b.Components) && (b.External || !calls[len(calls)-1].Tainted) // marked when written after a read
	}
	if !agenda || !asks {
		t.Errorf("the day's page: agenda %v, asks marked %v:\n%s", agenda, asks, d.Raw)
	}
	if cardWaitsOn(c, subject) {
		t.Error("the day's page waits on a card")
	}
}

// questionnaire tells a text that asks the user several things before
// anything is shown: two lines or more that ask (a list item with a
// question mark), or that say what happens without an answer.
func questionnaire(text string) bool {
	n := 0
	for _, line := range strings.Split(text, "\n") {
		l := strings.ToLower(strings.TrimSpace(line))
		item := strings.HasPrefix(l, "- ") || strings.HasPrefix(l, "* ") ||
			len(l) > 2 && l[0] >= '0' && l[0] <= '9' && (l[1] == '.' || l[1] == ')')
		if item && strings.Contains(l, "?") || strings.Contains(l, "si tu ne dis rien") || strings.Contains(l, "sans réponse") {
			n++
		}
	}
	return n >= 2
}

// questionnaire tells the questions asked before anything is shown from
// a page that says its choices in a line.
func TestQuestionnaire(t *testing.T) {
	asked := "Avant de mettre quoi que ce soit en place :\n\n1. Quels comptes ? *Si tu ne dis rien : tous.*\n2. Te prévenir quand ? Sans réponse : au brief.\n"
	shown := "Ta journée, tenue à jour. Je ne te préviens qu'au brief ; dis-le pour changer.\n\n- Point produit à 11 h\n- Dentiste à 14 h 30\n"
	if !questionnaire(asked) || questionnaire(shown) {
		t.Errorf("asked %v, shown %v", questionnaire(asked), questionnaire(shown))
	}
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
