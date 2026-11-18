package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Alexis-Marcel/eriphe-system/core/nodefile"
	"github.com/Alexis-Marcel/eriphe-system/core/nodestore"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The nodes' bench (#634): the epic's scenario (#625) played against a
// real model as the brain, on the scenario's test core (espace_scenario_
// test.go): its four faked tools, its faked device, eriphe-core whole. The
// brain is started by eriphe-core as on an install (its spec: the built-in
// prompt, its tools, its free reads), and run here by the local Claude
// Code CLI (`claude` signed in on this machine), in stream-json as a
// device runs it, its MCP servers the device API of the test core, its
// permissions asked through a test-only approve tool as a device asks
// them. The workers are faked: any task writes a short result in its node
// and reports done, the first step held until the scenario finishes it.
//
//	ERIPHE_LIVE_NODES=1 go test -run TestNodesBenchLive -timeout 0 -v .
//
// ERIPHE_LIVE_NODES_RUNS (1 by default) plays it that many times,
// ERIPHE_LIVE_NODES_MODEL picks the model (sonnet by default),
// ERIPHE_LIVE_NODES_CLI the command (claude). The CLI runs in a temporary
// directory, without this machine's settings, CLAUDE.md, memory or MCP
// servers, and with none of its harness tools but the search for deferred
// tools: nothing reaches the web, nothing but the test core.
//
// Each run is scored from what eriphe-core holds, never from words:
//
//   - lost replies: an answer code's safety net had to file (the brain
//     left a long answer in the conversation alone), a turn of the user's
//     with no answer, or a question's answer written in no node (the
//     day's agenda not in « Ma journée », « plus court » not a new
//     version of the plan);
//   - duplicate nodes: a node the brain opened in a turn about a thing
//     that already has one (the light, the agenda, the article's state,
//     the plan's changes, the archive), or two live nodes of one title;
//   - stuck steps: a step neither accepted nor dropped once the work
//     settled;
//   - views only shown: a standing node the brain opened itself rather
//     than proposing it (propose_view's card), or the reply to the mail
//     not proposed on a card (no « Envoyer ? »).
//
// The test fails when a run loses a reply or duplicates a node (#634's
// bar); every number is logged, run by run.
func TestNodesBenchLive(t *testing.T) {
	if os.Getenv("ERIPHE_LIVE_NODES") == "" {
		t.Skip("set ERIPHE_LIVE_NODES=1 to play the epic's scenario on a real model")
	}
	runs := 1
	if v := os.Getenv("ERIPHE_LIVE_NODES_RUNS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			t.Fatalf("ERIPHE_LIVE_NODES_RUNS=%q", v)
		}
		runs = n
	}
	model := cmpOr(os.Getenv("ERIPHE_LIVE_NODES_MODEL"), "sonnet")
	cli := cmpOr(os.Getenv("ERIPHE_LIVE_NODES_CLI"), "claude")
	if _, err := exec.LookPath(cli); err != nil {
		t.Fatalf("%s: %v", cli, err)
	}
	var all []benchScore
	for i := range runs {
		t.Run(fmt.Sprintf("run%d", i+1), func(t *testing.T) {
			s := playNodesBench(t, cli, model)
			t.Logf("run %d (%s): %s", i+1, model, s)
			all = append(all, s)
			if s.lost() > 0 || s.duplicates() > 0 {
				t.Errorf("run %d: %d lost replies, %d duplicate nodes", i+1, s.lost(), s.duplicates())
			}
		})
	}
	for i, s := range all {
		t.Logf("summary run %d: %s", i+1, s)
	}
}

// benchScore is what a run measured, each fault with its turn.
type benchScore struct {
	netFiled, unanswered, unfiled []string // lost replies
	reopened, sameTitle           []string // duplicate nodes
	stuck                         []string // stuck steps
	selfView, unproposed          []string // views only shown
	notes                         []string // what is worth reading, no fault
	turns                         int
	cost                          float64
	took                          time.Duration
}

func (s benchScore) lost() int       { return len(s.netFiled) + len(s.unanswered) + len(s.unfiled) }
func (s benchScore) duplicates() int { return len(s.reopened) + len(s.sameTitle) }

func (s benchScore) String() string {
	list := func(xs []string) string {
		if len(xs) == 0 {
			return "0"
		}
		return fmt.Sprintf("%d %q", len(xs), xs)
	}
	return fmt.Sprintf("lost replies %d (net filed %s, unanswered %s, unfiled %s); duplicate nodes %d (reopened %s, same title %s); "+
		"stuck steps %s; views only shown %d (self-made %s, unproposed %s); brain turns %d, $%.2f, %s; notes %q",
		s.lost(), list(s.netFiled), list(s.unanswered), list(s.unfiled), s.duplicates(), list(s.reopened), list(s.sameTitle),
		list(s.stuck), len(s.selfView)+len(s.unproposed), list(s.selfView), list(s.unproposed), s.turns, s.cost, s.took.Round(time.Second), s.notes)
}

// playNodesBench plays the scenario once, on a core of its own.
func playNodesBench(t *testing.T, cli, model string) benchScore {
	ctx, stop := context.WithCancel(context.Background())
	live := &liveBrain{cli: cli, model: model, dir: t.TempDir(), sess: map[string]*liveSession{}}
	start := time.Now()
	sc := newScenarioWith(t, ctx, live)
	// The CLI goes first: the test core's servers close once it is gone.
	t.Cleanup(func() {
		stop()
		live.close()
	})
	b := &bench{t: t, sc: sc, allowed: map[string]bool{}}
	for _, id := range sc.nodes {
		b.allowed[id] = true // the seed
	}
	b.quiet()
	day, substack := sc.nodes["day"], sc.nodes["substack"]

	// 2. One call; no node opens; the call goes into « Ma journée »'s trace.
	b.say("Allume la lumière du bureau.", "", turnNoNode)
	if !slices.Contains(sc.tools.litRooms(), "bureau") {
		b.note("the light was not lit")
	}
	if trace, _ := sc.c.nodes.Trace(day); !strings.Contains(string(trace), "maison/allumer") {
		b.note("the light's call is not in « Ma journée »'s trace")
	}

	// 3-4. On « Substack »: a work, its steps « Style » then « Plan ».
	b.say("Analyse le style des articles qui marchent, puis propose-moi un plan du premier article.", substack, turnWork)
	work, steps := b.work(substack)
	if len(steps) < 2 {
		b.note(fmt.Sprintf("the work has %d steps", len(steps)))
	}

	// 5. A question said on « Substack »: its answer goes into « Ma journée ».
	v := b.version(day)
	b.say("Qu'est-ce que j'ai de prévu aujourd'hui ?", substack, turnNoNode)
	if b.version(day) == v {
		b.s.unfiled = append(b.s.unfiled, "agenda: « Ma journée » not written")
	}

	// 6-8. The urgent mail; a reply, the production, a research.
	mail, err := sc.mail()
	if err != nil {
		t.Fatal(err)
	}
	b.allowed[mail] = true
	b.quiet()
	b.say("Réponds que je regarde dans l'heure, ouvre-moi la prod, et lance en arrière-plan une recherche sur l'erreur.", mail, turnWork)
	// 9. The user says yes to what waits (the send, a start, a view).
	if !b.shows("prod/erreurs") {
		b.note("the production is shown in no node and proposed in no view")
	}
	cards := b.approveAll()
	b.quiet()
	if sent := sc.tools.sentTo("sam@example.org"); sent == 0 {
		b.s.unproposed = append(b.s.unproposed, fmt.Sprintf("reply: no mail sent to Sam after the cards said yes (%v)", cards))
	}
	sc.finishFirst()
	b.settle(work)

	// 10. Back on « Substack »: where the article stands.
	b.say("Où en est le premier article ?", substack, turnNoNode)

	// 11-12. Said to the plan's page: a new version of it.
	plan := b.plan(work, steps)
	if plan == "" {
		b.s.unfiled = append(b.s.unfiled, "plan: no plan node to talk to")
	} else {
		v := b.version(plan)
		b.say("Plus court, et garde le titre 2.", plan, turnNoNode)
		if b.version(plan) == v {
			b.s.unfiled = append(b.s.unfiled, "plus court: no new version of the plan")
		}
		b.say("Ce plan me va.", plan, turnNoNode)
		if sum, _ := sc.c.nodes.Get(plan); sum.Done.IsZero() {
			if w, _ := sc.c.nodes.Get(work); w.Done.IsZero() {
				b.note("the plan, said to suit, is not done")
			}
		}
	}

	// 13. The archive is the user's: Eriphe proposes it, the user does it.
	b.say("C'est réglé avec Sam, on peut archiver.", mail, turnNoNode)
	if sum, _ := sc.c.nodes.Get(mail); !sum.Archived.IsZero() {
		b.note("the mail was archived by someone else than the user")
	}
	b.end(work) // before the archive: the whole graph is scored
	if err := sc.c.archiveNode(ctx, mail, byUser); err != nil {
		b.note("archive: " + err.Error())
	}
	b.quiet()
	b.filed() // the turns no message of the user's started, the last included
	b.s.cost, b.s.turns = live.spent()
	b.s.took = time.Since(start)
	return b.s
}

// What a turn of the user's may create.
const (
	turnNoNode = "none" // a thing that has its node already, or none
	turnWork   = "work" // new work: nodes may be born
)

// bench drives one run: the user's turns, the cards, and the scoring.
type bench struct {
	t       *testing.T
	sc      *scenario
	s       benchScore
	allowed map[string]bool // the nodes that may be: the seed, the mail, those born of new work
	counted map[int]bool    // the answers already counted as filed by the net
}

func (b *bench) note(s string) { b.s.notes = append(b.s.notes, s) }

// say is the user's message, said to node ("" for home) as the console
// sends it; it returns once the brain and what it started are quiet.
func (b *bench) say(text, node, may string) {
	c := b.sc.c
	before := b.liveNodes()
	seq, err := c.store.addUserMessage(text, nil, node)
	if err != nil {
		b.t.Fatal(err)
	}
	c.ping()
	b.quiet()
	turn := truncate(text, 30)
	msgs, _ := c.store.queryMessages(`SELECT `+messageCols+` FROM messages WHERE role = ? AND seq > ? ORDER BY seq`, roleBrain, seq)
	var said []string
	for _, m := range msgs {
		said = append(said, m.Text)
		if m.Filed != "" && !b.counted[m.Seq] {
			if b.counted == nil {
				b.counted = map[int]bool{}
			}
			b.counted[m.Seq] = true
			b.s.netFiled = append(b.s.netFiled, fmt.Sprintf("%s: %d runes filed in %s", turn, runeLen(m.Text), m.Filed))
		}
	}
	if len(msgs) == 0 {
		b.s.unanswered = append(b.s.unanswered, turn)
	}
	var born []string
	for id, sum := range b.liveNodes() {
		if _, ok := before[id]; ok {
			continue
		}
		if b.codes(id) {
			continue // a task's node or a step: code opened it
		}
		born = append(born, sum.Title)
		b.allowed[id] = true // counted here, once
		if sum.Lifetime == nodefile.Standing && !b.viewMade(id) {
			b.s.selfView = append(b.s.selfView, fmt.Sprintf("%s: %s", turn, sum.Title))
		}
	}
	if may == turnNoNode && len(born) > 0 {
		b.s.reopened = append(b.s.reopened, fmt.Sprintf("%s: %q", turn, born))
	}
	if may == turnWork {
		for id := range b.liveNodes() {
			b.allowed[id] = true // what the work's wakes open after counts as its own
		}
	}
	b.t.Logf("« %s » → %s; nodes born %q; calls %v", text, truncate(strings.Join(said, " / "), 400), born, b.sc.live.calls())
}

// liveNodes are the graph's nodes now.
func (b *bench) liveNodes() map[string]nodestore.Summary {
	all, err := b.sc.c.nodes.Graph()
	if err != nil {
		b.t.Fatal(err)
	}
	out := map[string]nodestore.Summary{}
	for _, s := range all {
		out[s.ID] = s
	}
	return out
}

// codes tells a node code opened: a task's own, or a step.
func (b *bench) codes(id string) bool {
	var n int
	b.sc.c.store.db.QueryRow(`SELECT (SELECT COUNT(*) FROM task_nodes WHERE node = ?) + (SELECT COUNT(*) FROM node_steps WHERE node = ?)`, id, id).Scan(&n)
	return n > 0
}

// viewMade tells a standing node a view's card created.
func (b *bench) viewMade(id string) bool {
	var n int
	b.sc.c.store.db.QueryRow(`SELECT COUNT(*) FROM view_proposals WHERE node = ? AND state = ?`, id, viewApproved).Scan(&n)
	return n > 0
}

// shows tells a live block reads tool (a node's, or a view proposed).
func (b *bench) shows(tool string) bool {
	srcs, _ := b.sc.c.nodes.Sources("")
	for _, s := range srcs {
		if s.Source.Tool == tool {
			return true
		}
	}
	var n int
	b.sc.c.store.db.QueryRow(`SELECT COUNT(*) FROM view_proposals WHERE body LIKE ?`, "%"+tool+"%").Scan(&n)
	return n > 0
}

func (b *bench) version(id string) int {
	sum, _ := b.sc.c.nodes.Get(id)
	return sum.Version
}

// work is the work the brain opened under parent, and its steps in order.
func (b *bench) work(parent string) (string, []string) {
	rows, err := b.sc.c.store.db.Query(`SELECT s.parent, s.node FROM node_steps s ORDER BY s.parent, s.n`)
	if err != nil {
		b.t.Fatal(err)
	}
	defer rows.Close()
	var work string
	var steps []string
	for rows.Next() {
		var p, n string
		rows.Scan(&p, &n)
		if !b.under(p, parent) {
			continue
		}
		if work == "" {
			work = p
		}
		if p == work {
			steps = append(steps, n)
		}
	}
	return work, steps
}

// under tells id is parent or below it.
func (b *bench) under(id, parent string) bool {
	for i := 0; id != "" && i < 10; i++ {
		if id == parent {
			return true
		}
		sum, err := b.sc.c.nodes.Get(id)
		if err != nil {
			return false
		}
		id = sum.Parent
	}
	return false
}

// plan is the node holding the plan: the step so titled, else the last
// step, else the work itself.
func (b *bench) plan(work string, steps []string) string {
	for _, id := range steps {
		if sum, _ := b.sc.c.nodes.Get(id); strings.Contains(strings.ToLower(sum.Title), "plan") {
			return id
		}
	}
	if len(steps) > 0 {
		return steps[len(steps)-1]
	}
	return work
}

// approveAll says yes on every card that waits, as the user does on
// « Envoyer ? »: it gives their kinds.
func (b *bench) approveAll() []string {
	c := b.sc.c
	var kinds []string
	for range 3 {
		rows, err := c.store.db.Query(`SELECT id, kind FROM convo_cards WHERE state = ?`, cardPending)
		if err != nil {
			b.t.Fatal(err)
		}
		type card struct {
			id   int64
			kind string
		}
		var cs []card
		for rows.Next() {
			var x card
			rows.Scan(&x.id, &x.kind)
			cs = append(cs, x)
		}
		rows.Close()
		if len(cs) == 0 {
			break
		}
		for _, x := range cs {
			if _, err := c.decideCard(context.Background(), x.id, true, "web"); err != nil {
				b.note(fmt.Sprintf("card %d %s: %v", x.id, x.kind, err))
				continue
			}
			kinds = append(kinds, x.kind)
		}
		b.quiet()
	}
	return kinds
}

// settle waits for the work to settle: no step waiting or running.
func (b *bench) settle(work string) {
	if work == "" {
		return
	}
	for end := time.Now().Add(4 * time.Minute); time.Now().Before(end); {
		var n int
		b.sc.c.store.db.QueryRow(`SELECT COUNT(*) FROM node_steps WHERE parent = ? AND status IN ('waiting', 'running')`, work).Scan(&n)
		var state string
		b.sc.c.store.db.QueryRow(`SELECT state FROM node_works WHERE parent = ?`, work).Scan(&state)
		if n == 0 && state != "running" && state != "checking" {
			break
		}
		time.Sleep(time.Second)
	}
	b.quiet()
}

// end scores what the run left: stuck steps, nodes of one title.
func (b *bench) end(work string) {
	rows, err := b.sc.c.store.db.Query(`SELECT node, key, status FROM node_steps`)
	if err != nil {
		b.t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var node, key, status string
		rows.Scan(&node, &key, &status)
		if status != "accepted" && status != "dropped" {
			b.s.stuck = append(b.s.stuck, fmt.Sprintf("%s: %s", key, status))
		}
	}
	// A node born in a turn no message of the user's started (a card's
	// yes, a task's end), out of any new work.
	for id, sum := range b.liveNodes() {
		if !b.allowed[id] && !b.codes(id) && !b.viewMade(id) {
			b.s.reopened = append(b.s.reopened, fmt.Sprintf("between turns: %q", sum.Title))
			b.allowed[id] = true
		}
	}
	titles := map[string][]string{}
	for id, sum := range b.liveNodes() {
		k := strings.ToLower(strings.TrimSpace(sum.Title))
		titles[k] = append(titles[k], id)
	}
	for k, ids := range titles {
		if len(ids) > 1 {
			b.s.sameTitle = append(b.s.sameTitle, fmt.Sprintf("%q ×%d", k, len(ids)))
		}
	}
	var waiting []string
	for _, sum := range b.liveNodes() {
		if sum.State == nodestore.Waiting {
			waiting = append(waiting, sum.Title)
		}
	}
	if len(waiting) > 0 {
		b.note(fmt.Sprintf("waiting for the user at the end: %q", waiting))
	}
}

// filed counts every answer code's safety net filed, those of the turns
// a wake started included.
func (b *bench) filed() {
	msgs, _ := b.sc.c.store.queryMessages(`SELECT `+messageCols+` FROM messages WHERE role = ? AND filed != '' ORDER BY seq`, roleBrain)
	for _, m := range msgs {
		if !b.counted[m.Seq] {
			b.s.netFiled = append(b.s.netFiled, fmt.Sprintf("a wake's turn: %d runes filed in %s", runeLen(m.Text), m.Filed))
		}
	}
}

// quiet waits until the brain has nothing left to do: no message for it,
// its session idle, for a few seconds in a row.
func (b *bench) quiet() {
	c := b.sc.c
	calm := 0
	for end := time.Now().Add(6 * time.Minute); time.Now().Before(end); time.Sleep(time.Second) {
		msgs, _ := c.store.undelivered()
		if len(msgs) == 0 && !b.sc.live.busy() {
			calm++
			if calm >= 5 {
				return
			}
			continue
		}
		calm = 0
	}
	b.t.Errorf("the brain is still busy after 6 minutes: the turn is scored half done")
}

// The faked tools' record.

func (f *scenarioTools) litRooms() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.lit)
}

func (f *scenarioTools) sentTo(to string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, s := range f.sent {
		if strings.HasPrefix(strings.ToLower(s), to+":") {
			n++
		}
	}
	return n
}

// The live workers.

// finishFirst lets the first steps finish.
func (sc *scenario) finishFirst() {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	ch, ok := sc.finish["first"]
	if !ok {
		ch = make(chan struct{})
		sc.finish["first"] = ch
	}
	select {
	case <-ch:
	default:
		close(ch)
	}
}

// liveWork is a faked worker for any work: a first step (one that comes
// after none) says where it is and waits for the scenario to finish it;
// then each writes a short result in its node (a plan of three titles when
// its title says a plan) and reports done.
func (sc *scenario) liveWork(id string, t *TaskRow) {
	first := false
	for range 20 {
		var after string
		err := sc.c.store.db.QueryRow(`SELECT after FROM node_steps WHERE task = ?`, id).Scan(&after)
		if err == nil {
			first = after == ""
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	title := t.Title
	if first {
		sc.call(id, nodesServer, "progress", map[string]any{"text": "3 articles lus"})
		sc.waitFinish("first")
	}
	body := "Ce qui ressort : des phrases courtes, un exemple par idée, un titre qui promet.\n"
	summary := "Fait : " + title + "."
	if strings.Contains(strings.ToLower(title), "plan") {
		body = "1. Pourquoi ce métier\n2. Ce qui a changé en dix ans\n3. Ce qui vient\n"
		summary = "Le plan en trois titres."
	}
	if out, err := sc.call(id, nodesServer, "node_read", map[string]any{}); err == nil {
		hash, _ := out["hash"].(string)
		sc.call(id, nodesServer, "node_write", map[string]any{"expect": hash, "body": body})
	}
	sc.done(id, summary)
}

// The live brain.

// liveBrain runs the brain's tasks with the local Claude Code CLI, as a
// device runs claude-code (device/runtime_claude.go): one process a task,
// a turn on stdin per inbox message, its events reported to eriphe-core.
type liveBrain struct {
	cli, model, dir string
	sc              *scenario
	perm            *httptest.Server

	mu    sync.Mutex
	sess  map[string]*liveSession
	cost  float64
	turns int
	used  []string // the tools called since calls was last asked
}

type liveSession struct {
	id    string
	cmd   *exec.Cmd
	stdin io.WriteCloser

	mu        sync.Mutex
	busy      bool
	delivered int
	inbox     []InboxItem
	answered  map[string]bool // permissions answered
	cost      float64         // the CLI's cost so far, cumulative in a session

	report sync.Mutex // one report at a time, numbered in order
}

// serve starts the permission tool, the device's approve.
func (l *liveBrain) serve(sc *scenario) {
	l.sc = sc
	type req struct {
		ToolName  string         `json:"tool_name"`
		Input     map[string]any `json:"input"`
		ToolUseID string         `json:"tool_use_id,omitempty"`
	}
	h := mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		id := r.PathValue("id")
		s := mcp.NewServer(&mcp.Implementation{Name: "eriphe-device", Version: "0"}, nil)
		mcp.AddTool(s, &mcp.Tool{Name: "approve", Description: "Ask Eriphe whether a tool call may run"},
			func(ctx context.Context, _ *mcp.CallToolRequest, in req) (*mcp.CallToolResult, any, error) {
				allow, msg := l.decide(ctx, id, in.ToolName, in.Input)
				decision := map[string]any{"behavior": "allow", "updatedInput": in.Input}
				if !allow {
					decision = map[string]any{"behavior": "deny", "message": cmpOr(msg, "Denied.")}
				}
				raw, _ := json.Marshal(decision)
				return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(raw)}}}, nil, nil
			})
		return s
	}, &mcp.StreamableHTTPOptions{Stateless: true, DisableLocalhostProtection: true})
	mux := http.NewServeMux()
	mux.Handle("/perm/{id}/mcp", h)
	l.perm = httptest.NewServer(mux)
}

// decide reports a permission as a device does, and waits for eriphe-core's
// answer in the task's inbox.
func (l *liveBrain) decide(ctx context.Context, task, tool string, input map[string]any) (bool, string) {
	s := l.session(task)
	if s == nil {
		return false, "no such task"
	}
	pid := newTaskID()
	l.emit(s, stateWaitingPermission, 0, EventRow{Type: "permission", Data: map[string]any{"id": pid, "tool_name": tool, "input": input}})
	for i := 0; i < 1200 && ctx.Err() == nil; i++ {
		items, _ := l.sc.c.store.inbox(task)
		for _, it := range items {
			if it.Kind == "permission" && fmt.Sprint(it.Data["id"]) == pid {
				allow, _ := it.Data["allow"].(bool)
				msg, _ := it.Data["message"].(string)
				// Its answer is handled: acknowledged, as a device does,
				// unless a message before it still waits for its turn.
				ack := it.Seq
				s.mu.Lock()
				s.answered[pid] = true
				for _, x := range items {
					if x.Kind == "message" && x.Seq < it.Seq && x.Seq > s.delivered {
						ack = 0
					}
				}
				if ack > 0 {
					s.delivered = max(s.delivered, ack)
				}
				s.mu.Unlock()
				l.emit(s, stateRunning, ack, EventRow{Type: "permission_answer", Data: map[string]any{"id": pid, "allow": allow, "message": msg}})
				return allow, msg
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false, "No answer."
}

func (l *liveBrain) session(id string) *liveSession {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.sess[id]
}

// emit reports a task's state and events, numbered after its last.
func (l *liveBrain) emit(s *liveSession, state string, delivered int, evs ...EventRow) {
	s.report.Lock()
	defer s.report.Unlock()
	l.sc.report(s.id, deviceReport{State: state, Delivered: delivered, Events: l.sc.events(s.id, evs...)})
}

// assign is the brain's assignment: its session starts once; its inbox is
// delivered a turn at a time.
func (l *liveBrain) assign(a deviceAssignment) {
	if a.Desired == desiredStopped {
		if s := l.session(a.ID); s != nil {
			s.stdin.Close()
		}
		l.sc.report(a.ID, deviceReport{State: stateStopped})
		return
	}
	s := l.session(a.ID)
	if s == nil {
		var err error
		if s, err = l.start(a); err != nil {
			log.Printf("bench: brain %s: %v", a.ID, err)
			l.sc.report(a.ID, deviceReport{State: stateFailed})
			return
		}
	}
	s.mu.Lock()
	s.inbox = a.Inbox
	s.mu.Unlock()
	l.deliver(s)
}

// start runs the CLI for a brain task, from its spec as its device would.
func (l *liveBrain) start(a deviceAssignment) (*liveSession, error) {
	spec := a.Spec
	if !strings.Contains(spec.AppendSystemPrompt, "## Les nœuds") {
		return nil, fmt.Errorf("the brain's spec carries no built-in prompt: the bench would measure another")
	}
	dir := filepath.Join(l.dir, a.ID)
	work := filepath.Join(dir, "work")
	if err := os.MkdirAll(work, 0o700); err != nil {
		return nil, err
	}
	headers := map[string]string{"Authorization": "Bearer device-secret", "Eriphe-Device": "pc"}
	servers := map[string]any{"eriphe_device": map[string]any{"type": "http", "url": l.perm.URL + "/perm/" + a.ID + "/mcp"}}
	for _, name := range spec.Servers {
		def := map[string]any{"type": "http", "url": l.sc.dev + "/v1/device/tasks/" + a.ID + "/mcp/" + name, "headers": headers}
		if slices.Contains(spec.AlwaysLoad, name) {
			def["alwaysLoad"] = true
		}
		servers[name] = def
	}
	raw, _ := json.Marshal(map[string]any{"mcpServers": servers})
	mcpFile, promptFile := filepath.Join(dir, "mcp.json"), filepath.Join(dir, "system-prompt.md")
	os.WriteFile(mcpFile, raw, 0o600)
	os.WriteFile(promptFile, []byte(spec.AppendSystemPrompt), 0o600)
	// Of its harness's tools, none that reaches the web: the bench's world
	// is the test core.
	var harness []string
	for _, h := range spec.HarnessTools {
		if h != "WebSearch" && h != "WebFetch" {
			harness = append(harness, h)
		}
	}
	args := []string{"-p", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose",
		"--mcp-config", mcpFile, "--strict-mcp-config", "--setting-sources", "",
		"--permission-prompt-tool", "mcp__eriphe_device__approve", "--permission-mode", "default",
		"--model", cmpOr(spec.Model, l.model), "--append-system-prompt-file", promptFile,
		"--tools", strings.Join(harness, ","), "--disable-slash-commands", "--no-session-persistence"}
	if len(spec.AllowedTools) > 0 {
		args = append(args, "--allowedTools", strings.Join(spec.AllowedTools, ","))
	}
	cmd := exec.Command(l.cli, args...)
	cmd.Dir = work
	cmd.Env = append(os.Environ(), "CLAUDE_CODE_DISABLE_AUTO_MEMORY=1", "CLAUDE_CODE_DISABLE_CLAUDE_MDS=1",
		"MCP_TOOL_TIMEOUT=3600000", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "DISABLE_AUTOUPDATER=1")
	// Its effort, never the provider's token the test core lends (a fake).
	for k, v := range spec.AgentEnv {
		if strings.Contains(k, "EFFORT") {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	s := &liveSession{id: a.ID, cmd: cmd, stdin: stdin, answered: map[string]bool{}}
	l.mu.Lock()
	l.sess[a.ID] = s
	l.mu.Unlock()
	l.emit(s, stateRunning, 0)
	go l.read(s, stdout)
	if spec.Prompt != "" {
		l.send(s, spec.Prompt, 0)
	}
	return s, nil
}

// send writes a turn; delivered is the inbox item it is.
func (l *liveBrain) send(s *liveSession, text string, delivered int) bool {
	msg, _ := json.Marshal(map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": text}})
	s.mu.Lock()
	if s.busy {
		s.mu.Unlock()
		return false
	}
	if _, err := s.stdin.Write(append(msg, '\n')); err != nil {
		s.mu.Unlock()
		return false
	}
	s.busy = true
	if delivered > 0 {
		s.delivered = delivered
	}
	s.mu.Unlock()
	l.emit(s, stateRunning, delivered)
	return true
}

// deliver hands the next message of the inbox to an idle session.
func (l *liveBrain) deliver(s *liveSession) {
	s.mu.Lock()
	items, done, busy := s.inbox, s.delivered, s.busy
	s.mu.Unlock()
	if busy {
		return
	}
	for _, it := range items {
		if it.Seq <= done || it.Kind != "message" {
			continue
		}
		text, _ := it.Data["text"].(string)
		l.send(s, text, it.Seq)
		return
	}
}

// read turns the CLI's stream into the device's events.
func (l *liveBrain) read(s *liveSession, stdout io.Reader) {
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 1<<20), 32<<20)
	for sc.Scan() {
		var ev map[string]any
		if json.Unmarshal(sc.Bytes(), &ev) != nil {
			continue
		}
		switch ev["type"] {
		case "assistant":
			if parent, _ := ev["parent_tool_use_id"].(string); parent != "" {
				continue
			}
			msg, _ := ev["message"].(map[string]any)
			blocks, _ := msg["content"].([]any)
			var evs []EventRow
			for _, b := range blocks {
				bm, _ := b.(map[string]any)
				switch bm["type"] {
				case "text":
					evs = append(evs, EventRow{Type: "text", Data: map[string]any{"text": bm["text"]}})
				case "tool_use":
					name, _ := bm["name"].(string)
					l.mu.Lock()
					l.used = append(l.used, strings.TrimPrefix(strings.TrimPrefix(name, "mcp__"), selfServer+"__"))
					l.mu.Unlock()
					evs = append(evs, EventRow{Type: "tool_use", Data: map[string]any{"name": name, "input": bm["input"]}})
				}
			}
			if len(evs) > 0 {
				l.emit(s, stateRunning, 0, evs...)
			}
		case "result":
			text, _ := ev["result"].(string)
			data := map[string]any{"subtype": ev["subtype"], "result": text}
			if isErr, _ := ev["is_error"].(bool); isErr {
				data["is_error"] = true
				log.Printf("bench: brain turn failed: %.300s", text)
			}
			cost, _ := ev["total_cost_usd"].(float64)
			l.mu.Lock()
			if cost >= s.cost {
				l.cost += cost - s.cost
			} else {
				l.cost += cost // a per-turn figure
			}
			s.cost = cost
			l.turns++
			l.mu.Unlock()
			s.mu.Lock()
			s.busy = false
			s.mu.Unlock()
			l.emit(s, stateIdle, 0, EventRow{Type: "turn_end", Data: data})
			l.deliver(s)
		}
	}
	s.mu.Lock()
	s.busy = false
	s.mu.Unlock()
}

// busy tells whether a brain's turn is running.
func (l *liveBrain) busy() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, s := range l.sess {
		s.mu.Lock()
		b := s.busy
		s.mu.Unlock()
		if b {
			return true
		}
	}
	return false
}

// calls are the tools the brain called since the last ask.
func (l *liveBrain) calls() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := l.used
	l.used = nil
	return out
}

// spent is what the brain's turns cost: in dollars (the CLI's estimate,
// cumulative per session) and in turns.
func (l *liveBrain) spent() (float64, int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.cost, l.turns
}

func (l *liveBrain) close() {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, s := range l.sess {
		s.stdin.Close()
		if s.cmd.Process != nil {
			s.cmd.Process.Kill()
		}
		s.cmd.Wait()
	}
	if l.perm != nil {
		l.perm.Close()
	}
}
