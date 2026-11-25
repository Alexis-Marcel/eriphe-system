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

// The nodes' bench (#634, #707): the epic's scenario (#625) played against
// a real model as the brain, on the scenario's test core (espace_scenario_
// test.go): its faked tools, its faked device, eriphe-core whole. The
// brain is started by eriphe-core as on an install (its spec: the built-in
// prompt, its tools, its free reads), and run here by the local Claude
// Code CLI (`claude` signed in on this machine), in stream-json as a
// device runs it, its MCP servers the device API of the test core, its
// permissions asked through a test-only approve tool as a device asks
// them. A skill's run (the view's skill) is run by the same model, the
// same way; the other workers are faked: a task writes a short result in
// its node and reports done, the first step held until the scenario
// finishes it, the plan's first try failing once.
//
//	ERIPHE_LIVE_NODES=1 go test -run TestNodesBenchLive -timeout 0 -v .
//
// ERIPHE_LIVE_NODES_RUNS (1 by default) plays it that many times,
// ERIPHE_LIVE_NODES_MODEL chooses the model (sonnet by default),
// ERIPHE_LIVE_NODES_CLI the command (claude), ERIPHE_LIVE_NODES_RECORD a
// file the first run's node writes are recorded in, for the offline
// replay (nodes_replay_test.go). The CLI runs in a temporary directory,
// without this machine's settings, CLAUDE.md, memory or MCP servers, and
// with no harness tool but the search for deferred tools: nothing reaches
// the web or the shell, nothing but the test core.
//
// It measures behaviour, never words: each run is scored from what
// eriphe-core holds and from the brain's calls to its tools, and printed
// as a table (benchMetrics), one column per run. The test fails when a
// run breaks one of the bars (benchBars): a lost reply, a duplicate node,
// more than one write refused for its grammar or the catalog, a view that
// is not valid, more than benchTurns turns of the user's, a page asked for
// (the mails, the day) not shown first: its subject not opened before any
// read, no draft and card in its turn, or questions.
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
	var all []*benchScore
	for i := range runs {
		t.Run(fmt.Sprintf("run%d", i+1), func(t *testing.T) {
			record := ""
			if i == 0 {
				record = os.Getenv("ERIPHE_LIVE_NODES_RECORD")
			}
			s := playNodesBench(t, cli, model, record)
			all = append(all, s)
			for _, bar := range s.broken() {
				t.Errorf("run %d: %s", i+1, bar)
			}
		})
	}
	t.Logf("%s, %d run(s):\n%s", model, len(all), benchTable(all))
}

// benchMetrics are the table's rows, in order: each with what is
// expected. A run fills them (benchScore.set).
var benchMetrics = []struct{ key, want string }{
	{"lost replies", "0"},
	{"duplicate nodes", "0"},
	{"refused writes: grammar, catalog", "≤ 1"},
	{"refused writes: taint, stale", "-"},
	{"refused by the budget", "-"},
	{"view valid", "yes"},
	{"user turns", "≤ 15"},
	{"brain turns", "-"},
	{"wake lines filed by the net", "-"},
	{"stuck steps", "0"},
	{"light: one call, no node", "yes"},
	{"quick question: no node", "yes"},
	{"turn_home on the agenda", "yes"},
	{"agenda: day page written, shown", "yes"},
	{"work cut in steps", "≥ 2"},
	{"step failure: settled", "retry/replan"},
	{"tainted turn: tainted", "yes"},
	{"tainted turn: title marked", "yes"},
	{"run_task under the turn's node", "yes"},
	{"reply to Sam on its card", "yes"},
	{"child reused, none opened", "yes"},
	{"plus court: new version", "yes"},
	{"plan done on its word", "yes"},
	{"archive left to the user", "yes"},
	{"aside: child, not shown", "yes"},
	{"node_set parent", "yes"},
	{"links written / resolved", "n / n"},
	{"glance set", "yes"},
	{"watch: subject first", "yes"},
	{"watch: first node (s)", "-"},
	{"watch: card in the first turn", "yes"},
	{"watch: skill homed on its page", "yes"},
	{"watch: no questionnaire", "yes"},
	{"watch: changes offered (ask)", "-"},
	{"view approved", "yes"},
	{"rework: task launched on the page", "yes"},
	{"rework: brain's own writes", "≤ 1"},
	{"rework: answered in (s)", "≤ 20"},
	{"view: bilan lead", "yes"},
	{"view: items, sections", "yes"},
	{"view: batch", "yes"},
	{"skill: item_note", "≥ 1"},
	{"skill: card on a row", "-"},
	{"day: subject first", "yes"},
	{"day: first node (s)", "-"},
	{"day: card in the first turn", "yes"},
	{"day: skill homed on its page", "yes"},
	{"day: no questionnaire", "yes"},
	{"day: changes offered (ask)", "-"},
	{"day: short titles", "yes"},
	{"cost, time", "-"},
}

// benchTurns is the most turns of the user's a run plays.
const benchTurns = 15

// benchScore is what a run measured: its table's values, and the faults
// behind each, with their turn.
type benchScore struct {
	vals  map[string]string
	off   map[string]bool // a value that is not what is expected
	notes []string        // the faults, and what is worth reading

	lost, dups, grammar int
	viewValid           bool
	turns               int
	notShown            []string // pages asked for, not shown first (scoreShownFirst)
}

func (s *benchScore) set(key, val string, ok bool) {
	if s.vals == nil {
		s.vals, s.off = map[string]string{}, map[string]bool{}
	}
	s.vals[key], s.off[key] = val, !ok
}

func (s *benchScore) yes(key string, ok bool, why string) {
	s.set(key, yesNo(ok), ok)
	if !ok && why != "" {
		s.note(key + ": " + why)
	}
}

func (s *benchScore) note(x string) { s.notes = append(s.notes, x) }

func yesNo(ok bool) string {
	if ok {
		return "yes"
	}
	return "no"
}

// broken are the bars the run broke.
func (s *benchScore) broken() []string {
	var out []string
	if s.lost > 0 {
		out = append(out, fmt.Sprintf("%d lost replies", s.lost))
	}
	if s.dups > 0 {
		out = append(out, fmt.Sprintf("%d duplicate nodes", s.dups))
	}
	if s.grammar > 1 {
		out = append(out, fmt.Sprintf("%d writes refused for their grammar or the catalog", s.grammar))
	}
	if !s.viewValid {
		out = append(out, "the view is not valid")
	}
	for _, what := range s.notShown {
		out = append(out, what+": not shown first")
	}
	if s.turns > benchTurns {
		out = append(out, fmt.Sprintf("%d turns of the user's", s.turns))
	}
	return out
}

// benchTable is the runs' table, in Markdown, then each run's notes.
func benchTable(runs []*benchScore) string {
	var b strings.Builder
	b.WriteString("| metric | expected |")
	for i := range runs {
		fmt.Fprintf(&b, " run %d |", i+1)
	}
	b.WriteString("\n|---|---|" + strings.Repeat("---|", len(runs)) + "\n")
	for _, m := range benchMetrics {
		fmt.Fprintf(&b, "| %s | %s |", m.key, m.want)
		for _, s := range runs {
			v := cmpOr(s.vals[m.key], "?")
			if s.off[m.key] {
				v = "**" + v + "**"
			}
			fmt.Fprintf(&b, " %s |", v)
		}
		b.WriteString("\n")
	}
	for i, s := range runs {
		fmt.Fprintf(&b, "\nrun %d:\n", i+1)
		for _, n := range s.notes {
			b.WriteString("- " + n + "\n")
		}
	}
	return b.String()
}

// liveCall is one call of the brain's to eriphe-core's tools, as code
// took it: the turn of the user's it came in, what the turn was then.
type liveCall struct {
	turn          int
	tool          string
	args          map[string]any
	tainted       []string
	about, talk   string
	err           string
	out           map[string]any
	stale         bool // a write on a version older than the last
	recorded      *replayCall
	sentAt, endAt time.Time
}

// Refusals that are not the brain's grammar: what the turn read, or a
// version it did not read.
func (x liveCall) taintOrStale() bool {
	return x.stale || strings.Contains(x.err, "external content") || strings.Contains(x.err, "newer version") ||
		strings.Contains(x.err, "stale") || strings.Contains(x.err, "is not the last")
}

// benchWrites are the calls whose refusal is a refused write.
var benchWrites = []string{"node_open", "node_write", "node_edit", "node_set", "write_skill", "item_note", "node_settle"}

// playNodesBench plays the scenario once, on a core of its own.
func playNodesBench(t *testing.T, cli, model, record string) *benchScore {
	ctx, stop := context.WithCancel(context.Background())
	live := &liveBrain{cli: cli, model: model, dir: t.TempDir(), sess: map[string]*liveSession{}}
	start := time.Now()
	sc := newScenarioWith(t, ctx, live)
	// The CLI goes first: the test core's servers close once it is gone.
	t.Cleanup(func() {
		stop()
		live.close()
	})
	go sc.c.runSkills(ctx) // the view's skill runs on its monitor's event
	b := &bench{t: t, sc: sc, s: &benchScore{}, allowed: map[string]bool{}}
	for _, id := range sc.nodes {
		b.allowed[id] = true // the seed
	}
	b.tap()
	b.quiet()
	day, substack := sc.nodes["day"], sc.nodes["substack"]

	// 1. One call, at once; no node opens, none is read first: said at
	// the home, the call is filed in no node (traceBrainCall).
	b.say("Allume la lumière du bureau.", "", turnNoNode)
	lit := slices.Contains(sc.tools.litRooms(), "bureau")
	trace, _ := sc.c.nodes.Trace(day)
	b.s.yes("light: one call, no node", lit && len(b.born) == 0 && !strings.Contains(string(trace), "maison/allumer"),
		fmt.Sprintf("lit %v, nodes born %q", lit, b.born))

	// 1b. A quick question at the home: answered in the orb, no node.
	b.say("Il est quelle heure à Tokyo ?", "", turnNoNode)
	b.s.yes("quick question: no node", len(b.born) == 0 && !b.called("node_open"), fmt.Sprintf("nodes born %q", b.born))

	// 2. On « Substack »: a work, its steps « Style » then « Plan ».
	b.say("Analyse le style des articles qui marchent, puis propose-moi un plan du premier article.", substack, turnWork)
	work, steps := b.work(substack)
	b.s.set("work cut in steps", strconv.Itoa(len(steps)), len(steps) >= 2)

	// 3. A question said on « Substack » about the day: turn_home, then
	// its answer written in « Ma journée » and shown.
	v := b.version(day)
	b.say("Qu'est-ce que j'ai de prévu aujourd'hui ?", substack, turnNoNode)
	b.s.yes("turn_home on the agenda", b.called("turn_home"), "not called")
	shown, _ := sc.c.lastShown()
	b.s.yes("agenda: day page written, shown", (b.version(day) > v || b.launchedOn(day)) && shown.Node == day,
		fmt.Sprintf("version %d → %d, shown %q", v, b.version(day), shown.Node))
	if b.version(day) == v && !b.launchedOn(day) {
		b.lost("agenda: « Ma journée » not written")
	}

	// 4. The urgent mail: a reply, the production, a research in the
	// background. The mail is someone else's words: the turn is tainted.
	mail, err := sc.mail()
	if err != nil {
		t.Fatal(err)
	}
	b.allowed[mail] = true
	b.names.mu.Lock()
	b.names.set(mail, "$mail") // the replay makes it too
	b.names.mu.Unlock()
	b.quiet()
	b.say("Réponds que je regarde dans l'heure, ouvre-moi la prod, et lance en arrière-plan une recherche sur l'erreur.", mail, turnWork)
	b.scoreTainted(mail)
	// The user says yes to what waits (the send, a start).
	cards := b.approveAll()
	b.quiet()
	sent := sc.tools.sentTo("sam@example.org")
	b.s.yes("reply to Sam on its card", sent > 0, fmt.Sprintf("nothing sent after the cards said yes (%v)", cards))
	sc.finishFirst()
	b.settle(work)

	// 5. Back on « Substack »: where the article stands. Its work exists:
	// read, none opened.
	b.say("Où en est le premier article ?", substack, turnNoNode)
	b.s.yes("child reused, none opened", len(b.born) == 0 && b.readAny(append([]string{work}, steps...)), fmt.Sprintf("born %q", b.born))

	// 6-7. Said to the plan's page: a new version of it, then done.
	plan := b.plan(work, steps)
	if plan == "" {
		b.lost("plan: no plan node to talk to")
	} else {
		v := b.version(plan)
		b.say("Plus court, et garde le titre 2.", plan, turnNoNode)
		// A new version, written or asked of a task on the plan.
		b.s.yes("plus court: new version", b.version(plan) > v || b.launchedOn(plan), "")
		if b.version(plan) == v && !b.launchedOn(plan) {
			b.lost("plus court: no new version of the plan")
		}
		b.say("Ce plan me va.", plan, turnNoNode)
		p, _ := sc.c.nodes.Get(plan)
		w, _ := sc.c.nodes.Get(work)
		b.s.yes("plan done on its word", !p.Done.IsZero() || !w.Done.IsZero(), "neither the plan nor its work is done")
	}

	// 8. The archive is the user's: Eriphe proposes it, the user does it.
	b.say("C'est réglé avec Sam, on peut archiver.", mail, turnNoNode)
	m, _ := sc.c.nodes.Get(mail)
	b.s.yes("archive left to the user", m.Archived.IsZero(), "archived by someone else than the user")

	// 9-10. On the side: a research under « Substack », the bar where it
	// is; then put under the article's work.
	before, _ := sc.c.lastShown()
	b.say("Lance à part une recherche sur les newsletters qui marchent.", substack, turnWork)
	after, _ := sc.c.lastShown()
	aside := b.bornUnder(substack)
	b.s.yes("aside: child, not shown", aside != "" && after.Seq == before.Seq || aside != "" && after.Node != aside,
		fmt.Sprintf("born %q, shown %q", b.born, after.Node))
	if aside != "" && work != "" {
		b.say("Cette recherche sert au premier article : range-la sous lui.", aside, turnNoNode)
		sum, _ := sc.c.nodes.Get(aside)
		b.s.yes("node_set parent", sum.Parent == work || slices.Contains(steps, sum.Parent), fmt.Sprintf("parent %q, work %q", sum.Parent, work))
	} else {
		b.s.set("node_set parent", "not played", false)
	}

	// 11. A figure to watch: a glance on the source that reads it.
	b.say("Garde un œil sur la consommation de la maison : je veux voir quand elle dépasse 10 kW.", "", turnWork)
	b.s.yes("glance set", b.glance("maison/conso"), "no glance reads maison/conso")

	// 12. Setting up a mail watch shows it first: its subject opens before
	// anything is read, then the skill that maintains it with its card in
	// the same turn, with sensible defaults, its choices adjustable after
	// (ask buttons, in a tainted turn too), never a questionnaire before.
	b.say("Tu peux garder un œil sur mes mails, m'aider à les gérer et me proposer des actions à faire.", "", turnWork)
	b.scoreShownFirst("watch", "")
	view := b.approveView()
	b.s.yes("view approved", view != "", "no view approved")
	b.scoreView(view)
	b.runSkill(view)

	// 12b. Rework the page (#770): the brain orchestrates. It launches a
	// task on the page in a short turn and answers in a line, rewriting
	// nothing itself; its budget would refuse a whole page.
	if view != "" {
		b.say("Refais cette page : regroupe les mails par expéditeur, le plus pressé en haut.", view, turnNoNode)
		b.scoreRework(view)
	} else {
		b.s.set("rework: task launched on the page", "no view", false)
	}

	// 13. The day asked to be followed, at the home, as on a fresh install
	// (the seed's day page archived first): its subject opens first, then
	// the skill that maintains it, its card in that turn, an Agenda on it, no
	// questionnaire, no title made of the sentence. The last turn: the
	// replay checks it again (nodes_replay_test.go).
	if err := sc.c.archiveNode(ctx, day, byUser); err != nil {
		b.note("day: " + err.Error())
	}
	const followDay = "Tu peux suivre et gérer mes journées ?"
	b.say(followDay, "", turnWork)
	b.scoreShownFirst("day", followDay)

	b.end(work) // before the archive: the whole graph is scored
	if err := sc.c.archiveNode(ctx, mail, byUser); err != nil {
		b.note("archive: " + err.Error())
	}
	b.quiet()
	b.filed()
	cost, turns := live.spent()
	b.s.turns = b.turn
	b.s.set("user turns", strconv.Itoa(b.turn), b.turn <= benchTurns)
	b.s.set("brain turns", strconv.Itoa(turns), true)
	b.s.set("cost, time", fmt.Sprintf("$%.2f, %s", cost, time.Since(start).Round(time.Second)), true)
	b.scoreCalls(record)
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
	s       *benchScore
	allowed map[string]bool // the nodes that may be: the seed, the mail, those born of new work
	counted map[int]bool    // the answers already counted as filed by the net
	turn    int             // the user's turns so far
	saidAt  time.Time       // when the last turn's message was said
	born    []string        // the titles born in the last turn
	bornIDs []string

	reopened, sameTitle, netFiled, unfiled, unanswered []string
	wakeFiled                                          int

	mu    sync.Mutex
	calls []liveCall
	names *replayNames
}

func (b *bench) note(s string) { b.s.note(s) }

func (b *bench) lost(why string) { b.unfiled = append(b.unfiled, why) }

// tap records each call of the brain's to eriphe-core's tools as code
// takes it: what the turn was when it came, and what it answered.
func (b *bench) tap() {
	c := b.sc.c
	seed := map[string]string{}
	for k, id := range b.sc.nodes {
		seed[k] = id
	}
	b.names = newReplayNames(seed)
	c.self.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			call, ok := req.(*mcp.CallToolRequest)
			if !ok {
				return next(ctx, method, req)
			}
			var args map[string]any
			json.Unmarshal(call.Params.Arguments, &args)
			x := liveCall{tool: call.Params.Name, args: args, tainted: c.turnTaint(), about: c.turn.aboutNode(), talk: c.turn.talkNode(), sentAt: time.Now()}
			if exp, _ := args["expect"].(string); exp != "" {
				x.stale = exp != b.lastHash(fmt.Sprint(args["id"]))
			}
			b.mu.Lock()
			x.turn = b.turn
			b.mu.Unlock()
			var rec *replayCall
			if slices.Contains(replayTools, x.tool) {
				clone := map[string]any{}
				json.Unmarshal(call.Params.Arguments, &clone)
				rec = &replayCall{Turn: x.turn, Tainted: len(x.tainted) > 0, About: b.names.placeholder(x.about), Talk: b.names.placeholder(x.talk),
					Tool: x.tool, Args: b.names.recordArgs(clone, b.lastHash)}
				if x.about != "" && rec.About == "" || x.talk != "" && rec.Talk == "" {
					rec.About, rec.Talk = "$x", "$x" // a node code opened: not played
				}
			}
			res, err := next(ctx, method, req)
			if r, ok := res.(*mcp.CallToolResult); ok && r.IsError {
				x.err = resultText(r)
			} else if err != nil {
				x.err = err.Error()
			} else if ok {
				raw, _ := json.Marshal(r.StructuredContent)
				json.Unmarshal(raw, &x.out)
			}
			if rec != nil {
				rec.OK, rec.Error = x.err == "", truncate(x.err, 300)
				if rec.OK {
					rec.Gives = b.names.gives(x.tool, x.out)
				}
				x.recorded = rec
			}
			x.endAt = time.Now()
			b.mu.Lock()
			b.calls = append(b.calls, x)
			b.mu.Unlock()
			return res, err
		}
	})
}

func (b *bench) lastHash(id string) string {
	d, err := b.sc.c.nodes.Read(id, 0)
	if err != nil {
		return ""
	}
	return d.Version.Hash
}

// turnCalls are the brain's calls in the user's turn n (and the wakes
// that came before the next).
func (b *bench) turnCalls(n int) []liveCall {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []liveCall
	for _, x := range b.calls {
		if x.turn == n {
			out = append(out, x)
		}
	}
	return out
}

// called tells the last turn called tool.
func (b *bench) called(tool string) bool {
	return slices.ContainsFunc(b.turnCalls(b.turn), func(x liveCall) bool { return x.tool == tool })
}

// launchedOn tells the last turn launched a task for node: run_task on it
// or under it, or on the turn's node when it names none.
func (b *bench) launchedOn(node string) bool {
	return slices.ContainsFunc(b.turnCalls(b.turn), func(x liveCall) bool {
		if x.tool != "run_task" || x.err != "" {
			return false
		}
		on, _ := x.args["node"].(string)
		if on == "" {
			on = cmpOr(x.talk, x.about)
		}
		return on != "" && b.under(on, node)
	})
}

// scoreRework scores « rework this page » said on it: a task launched on
// it, the brain's own writes one at most, its answer within 20 s (to the
// second the conversation keeps).
func (b *bench) scoreRework(page string) {
	writes := 0
	for _, x := range b.turnCalls(b.turn) {
		if x.err == "" && (x.tool == "node_write" || x.tool == "node_edit" || x.tool == "item_note") {
			writes++
		}
	}
	b.s.yes("rework: task launched on the page", b.launchedOn(page), "no run_task on the page")
	b.s.set("rework: brain's own writes", strconv.Itoa(writes), writes <= 1)
	answered := "-"
	ok := false
	if msgs, _ := b.sc.c.store.queryMessages(`SELECT `+messageCols+` FROM messages WHERE role = ? AND seq > (SELECT MAX(seq) FROM messages WHERE role = ?) ORDER BY seq LIMIT 1`, roleBrain, roleUser); len(msgs) > 0 {
		took := msgs[0].Time.Sub(b.saidAt)
		answered, ok = fmt.Sprintf("%.1f", took.Seconds()), took <= 20*time.Second
	}
	b.s.set("rework: answered in (s)", answered, ok)
}

// readAny tells the last turn read one of ids.
func (b *bench) readAny(ids []string) bool {
	return slices.ContainsFunc(b.turnCalls(b.turn), func(x liveCall) bool {
		return x.tool == "node_read" && slices.Contains(ids, fmt.Sprint(x.args["id"]))
	})
}

// say is the user's message, said to node ("" for home) as the bar
// sends it; it returns once the brain and what it started are quiet.
func (b *bench) say(text, node, may string) {
	c := b.sc.c
	b.mu.Lock()
	b.turn++
	b.saidAt = time.Now()
	b.mu.Unlock()
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
			if m.Seq == msgs[0].Seq || b.answersUser(m) {
				b.netFiled = append(b.netFiled, fmt.Sprintf("%s: %d runes filed in %s", turn, runeLen(m.Text), m.Filed))
			} else {
				b.s.note(fmt.Sprintf("a wake after « %s »: %d runes filed in %s", turn, runeLen(m.Text), m.Filed))
				b.wakeFiled++
			}
		}
	}
	if len(msgs) == 0 {
		b.unanswered = append(b.unanswered, turn)
	}
	b.born, b.bornIDs = nil, nil
	for id, sum := range b.liveNodes() {
		if _, ok := before[id]; ok {
			continue
		}
		if b.codes(id) {
			continue // a task's node or a step: code opened it
		}
		b.born = append(b.born, sum.Title)
		b.bornIDs = append(b.bornIDs, id)
		b.allowed[id] = true // counted here, once
	}
	if may == turnNoNode && len(b.born) > 0 {
		b.reopened = append(b.reopened, fmt.Sprintf("%s: %q", turn, b.born))
	}
	if may == turnWork {
		for id := range b.liveNodes() {
			b.allowed[id] = true // what the work's wakes open after counts as its own
		}
	}
	var tools []string
	for _, x := range b.turnCalls(b.turn) {
		t := x.tool
		if q, _ := x.args["query"].(string); q != "" {
			t += fmt.Sprintf("(%q)", q)
		}
		if x.err != "" {
			t += "(refused)"
		}
		tools = append(tools, t)
	}
	b.t.Logf("%d. « %s » → %s; nodes born %q; calls %v; taint %q; catalog %v", b.turn, text, truncate(strings.Join(said, " / "), 400), b.born, tools, c.turnTaint(), b.sc.live.calls())
}

// answersUser tells a message of the brain's that answers the user's
// turn, not a wake that came after it: it was said before the turn's
// first event was delivered. Code fixes where an answer belongs at
// delivery (talkOf): a wake's answer belongs to its task's node.
func (b *bench) answersUser(m Message) bool {
	var n int
	b.sc.c.store.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE role = ? AND seq < ? AND seq > (SELECT MAX(seq) FROM messages WHERE role = ? AND seq < ?)`,
		roleEvent, m.Seq, roleUser, m.Seq).Scan(&n)
	return n == 0
}

// liveNodes are the graph's nodes now.
func (b *bench) liveNodes() map[string]nodestore.Summary {
	out := map[string]nodestore.Summary{}
	for _, s := range liveSummaries(b.t, b.sc.c) {
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

// viewMade tells a node a skill maintains.
func (b *bench) viewMade(id string) bool { return len(b.sc.c.maintainers(id)) > 0 }

// lastAnswer is the brain's last message.
func (b *bench) lastAnswer() (Message, bool) {
	msgs, _ := b.sc.c.store.queryMessages(`SELECT `+messageCols+` FROM messages WHERE role = ? ORDER BY seq DESC LIMIT 1`, roleBrain)
	if len(msgs) == 0 {
		return Message{}, false
	}
	return msgs[0], true
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

// bornUnder is the node the last turn opened below parent, "" if none.
func (b *bench) bornUnder(parent string) string {
	for _, id := range b.bornIDs {
		if id != parent && b.under(id, parent) {
			return id
		}
	}
	return ""
}

// plan is the node holding the plan: the step so titled (the last
// accepted, after a replan), else the last step, else the work itself.
func (b *bench) plan(work string, steps []string) string {
	_, now := b.work(work) // a replan's steps
	if len(now) == 0 {
		_, now = b.work(b.parentOf(work))
	}
	if len(now) > 0 {
		steps = now
	}
	plan := ""
	for _, id := range steps {
		if sum, _ := b.sc.c.nodes.Get(id); strings.Contains(strings.ToLower(sum.Title), "plan") {
			if st, ok := b.sc.c.nodeStep(id); ok && st.Status == stepAccepted || plan == "" {
				plan = id
			}
		}
	}
	if plan != "" {
		return plan
	}
	if len(steps) > 0 {
		return steps[len(steps)-1]
	}
	return work
}

func (b *bench) parentOf(id string) string {
	sum, _ := b.sc.c.nodes.Get(id)
	return sum.Parent
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

// settle waits for the work to settle: no step waiting or running, and
// none failed or blocked that the brain has still to decide on (the
// plan's first try fails: the brain is woken, settles it, and the step
// runs again). It scores how the failure was settled.
func (b *bench) settle(work string) {
	if work == "" {
		b.s.set("step failure: settled", "no work", false)
		return
	}
	for end := time.Now().Add(6 * time.Minute); time.Now().Before(end); time.Sleep(time.Second) {
		b.quiet()
		var moving, open int
		b.sc.c.store.db.QueryRow(`SELECT COUNT(*) FROM node_steps WHERE parent = ? AND status IN ('waiting', 'running')`, work).Scan(&moving)
		b.sc.c.store.db.QueryRow(`SELECT COUNT(*) FROM node_steps WHERE parent = ? AND status IN ('failed', 'blocked')`, work).Scan(&open)
		var state string
		b.sc.c.store.db.QueryRow(`SELECT state FROM node_works WHERE parent = ?`, work).Scan(&state)
		if moving == 0 && state != "running" && state != "checking" && (open == 0 || !b.sc.failedOnce()) {
			break
		}
	}
	var outcomes []string
	b.mu.Lock()
	for _, x := range b.calls {
		if x.tool == "node_settle" && x.err == "" {
			outcomes = append(outcomes, fmt.Sprint(x.args["outcome"]))
		}
	}
	b.mu.Unlock()
	var accepted int
	b.sc.c.store.db.QueryRow(`SELECT COUNT(*) FROM node_steps WHERE parent = ? AND status = ?`, work, stepAccepted).Scan(&accepted)
	ok := slices.ContainsFunc(outcomes, func(o string) bool { return o == settleRetry || o == settleReplan }) && accepted >= 2
	val := cmpOr(strings.Join(outcomes, ", "), "none")
	if !b.sc.failedOnce() {
		val, ok = "no failure", false
	}
	b.s.set("step failure: settled", val, ok)
	if !ok {
		b.note(fmt.Sprintf("step failure: settled with %q, %d steps accepted", outcomes, accepted))
	}
}

// scoreTainted scores the turn that read the urgent mail: tainted, the
// production's node opened in it marked, the research's task under the
// mail.
func (b *bench) scoreTainted(mail string) {
	tainted := false
	for _, x := range b.turnCalls(b.turn) {
		tainted = tainted || len(x.tainted) > 0
	}
	b.s.yes("tainted turn: tainted", tainted || len(b.sc.c.turnTaint()) > 0, "nothing read was someone else's")
	marked, opened := true, 0
	for _, id := range b.bornIDs {
		sum, _ := b.sc.c.nodes.Get(id)
		opened++
		if !sum.TitleExternal() {
			marked = false
			b.note(fmt.Sprintf("tainted turn: %q opened unmarked", sum.Title))
		}
	}
	b.s.yes("tainted turn: title marked", opened > 0 && marked, "")
	var ids []string
	if rows, err := b.sc.c.store.db.Query(`SELECT node FROM task_nodes`); err == nil {
		for rows.Next() {
			var id string
			rows.Scan(&id)
			ids = append(ids, id)
		}
		rows.Close() // one connection: closed before the next query
	}
	var under, tasks int
	for _, id := range ids {
		if _, ok := b.sc.c.nodeStep(id); ok {
			continue
		}
		tasks++
		if b.under(id, mail) {
			under++
		}
	}
	b.s.yes("run_task under the turn's node", under > 0, fmt.Sprintf("%d task nodes, %d under the mail", tasks, under))
	if !b.shows("prod/erreurs") {
		b.note("the production is shown in no node and proposed in no view")
	}
}

// shows tells a live block reads tool (a node's, or a view proposed).
func (b *bench) shows(tool string) bool {
	srcs, _ := b.sc.c.nodes.Sources("")
	for _, s := range srcs {
		if s.Source.Tool == tool {
			return true
		}
	}
	return false
}

// glance tells a node's source reads tool with a glance.
func (b *bench) glance(tool string) bool {
	srcs, _ := b.sc.c.nodes.Sources("")
	return slices.ContainsFunc(srcs, func(s nodestore.LiveSource) bool { return s.Source.Tool == tool && s.Source.Glance != nil })
}

// scoreShownFirst scores a page asked for, kept up to date, in the last
// turn (its rows are what's « … »): its subject reached first, before
// anything is read (node_find alone may come before: node_open, or
// node_show of the node that holds it), and how long it took to appear;
// its view proposed in that very turn as a draft linked to the subject; no
// questionnaire written in it nor said, the changes offered by ask
// buttons; with the user's sentence, no title made of it. A page not
// proposed, a subject not first, or questions asked first, break a bar.
func (b *bench) scoreShownFirst(what, sentence string) {
	proposed, beside, asked, asks := false, false, false, false
	first, firstAt := "", time.Duration(0)
	var titles []string
	for _, x := range b.turnCalls(b.turn) {
		if first == "" && x.tool != "node_find" && x.tool != "turn_home" {
			first = x.tool
			if x.err == "" && (x.tool == "node_open" || x.tool == "node_show") {
				firstAt = x.endAt.Sub(b.saidAt)
			}
		}
		if x.tool == "write_skill" && x.err == "" {
			proposed = true
			if node, _ := x.args["node"].(string); node != "" {
				_, err := b.sc.c.nodes.Get(node)
				beside = beside || err == nil // the page it maintains, written already
			}
		}
		if !slices.Contains(benchWrites, x.tool) || x.err != "" {
			continue
		}
		for _, k := range []string{"body", "text", "new"} {
			s, _ := x.args[k].(string)
			asked = asked || questionnaire(s)
			asks = asks || strings.Contains(s, `"ask"`)
		}
		if v, ok := x.args["value"]; ok {
			raw, _ := json.Marshal(v)
			asks = asks || strings.Contains(string(raw), `"ask"`)
		}
		if t, _ := x.args["title"].(string); t != "" && (x.tool == "node_open" || x.tool == "node_set") {
			titles = append(titles, t)
		}
	}
	if said, ok := b.lastAnswer(); ok && questionnaire(said.Text) {
		asked = true
	}
	subjectFirst := firstAt > 0
	b.s.yes(what+": subject first", subjectFirst, fmt.Sprintf("the turn's first call is %q", first))
	if subjectFirst {
		b.s.set(what+": first node (s)", fmt.Sprintf("%.1f", firstAt.Seconds()), true)
	} else {
		b.s.set(what+": first node (s)", "-", true)
	}
	b.s.yes(what+": card in the first turn", proposed, "no skill proposed to maintain it in the turn it was asked")
	b.s.yes(what+": skill homed on its page", beside, "the skill names no page it maintains")
	b.s.yes(what+": no questionnaire", !asked, "questions asked before the page was shown")
	b.s.set(what+": changes offered (ask)", yesNo(asks), true)
	if !proposed || asked || !subjectFirst {
		b.s.notShown = append(b.s.notShown, what)
	}
	if sentence == "" {
		return
	}
	short := !slices.ContainsFunc(titles, func(t string) bool {
		return strings.Contains(t, "?") || strings.EqualFold(strings.TrimSpace(t), strings.TrimSpace(sentence))
	})
	b.s.yes(what+": short titles", short, fmt.Sprintf("titled %q", titles))
}

// approveView says yes on the card of the skill that maintains the page
// asked for: its home node, "" if none.
func (b *bench) approveView() string {
	c := b.sc.c
	var node string
	var card *proposal
	for _, p := range c.pendingProposals() {
		var pending string
		c.store.db.QueryRow(`SELECT pending FROM skills WHERE rowid = ?`, p.ID).Scan(&pending)
		if v, err := c.store.skillVersion(pending); p.Kind == cardSkill && err == nil && v.Manifest.Trigger != nil && v.Manifest.Trigger.Node != "" {
			node, card = v.Manifest.Trigger.Node, &p
		}
	}
	if card == nil {
		b.note("view: no skill's card maintaining a page waits")
		return ""
	}
	xs, err := c.cardsWhere(`ref = ? AND state = ?`, proposalRef(cardSkill, card.ID), cardPending)
	if err != nil || len(xs) == 0 {
		b.note("view: its card does not wait")
		return ""
	}
	if _, err := c.decideCard(context.Background(), xs[0].ID, true, "web"); err != nil {
		b.note("view: " + err.Error())
		return ""
	}
	b.quiet()
	if !b.viewMade(node) {
		return ""
	}
	b.allowed[node] = true
	return node
}

// scoreView scores the view's page: its bilan (the text before its first
// block), a block whose source lists items in sections (a List filtered
// by a group), a batch (a Fold over a List). The view is valid with its
// lead, items and sections.
func (b *bench) scoreView(view string) {
	if view == "" {
		for _, k := range []string{"view: bilan lead", "view: items, sections", "view: batch"} {
			b.s.set(k, "no view", false)
		}
		return
	}
	d, err := b.sc.c.nodes.Read(view, 0)
	if err != nil {
		b.t.Fatal(err)
	}
	lead := false
	for _, p := range d.Node.Parts() {
		if p.Block != nil {
			break
		}
		lead = lead || strings.TrimSpace(p.Text) != ""
	}
	items, sections, batch := false, false, false
	for _, bl := range d.Node.Blocks() {
		for _, src := range bl.Sources {
			items = items || src.Items != nil
		}
		var list []map[string]any
		json.Unmarshal(bl.Components, &list)
		for _, comp := range list {
			switch comp["component"] {
			case "List":
				ch, _ := comp["children"].(map[string]any)
				path, _ := ch["path"].(string)
				sections = sections || strings.Contains(path, "[_group=")
			case "Fold":
				batch = batch || comp["list"] != nil
			}
		}
	}
	b.s.yes("view: bilan lead", lead, "no text before the first block")
	b.s.yes("view: items, sections", items && sections, fmt.Sprintf("items %v, sections %v", items, sections))
	b.s.yes("view: batch", batch, "no Fold with list")
	b.s.viewValid = lead && items && sections
	b.s.yes("view valid", b.s.viewValid, "")
}

// runSkill brings a new mail to the view's monitor and lets its skill
// run on it, as the same model: it scores the items it noted and the
// cards it left on their rows.
func (b *bench) runSkill(view string) {
	c := b.sc.c
	if view == "" {
		b.s.set("skill: item_note", "no view", false)
		b.s.set("skill: card on a row", "no view", true)
		return
	}
	ms, _ := c.monitors()
	var ours []*monitor
	for _, m := range ms {
		if m.State == monitorOn {
			ours = append(ours, m)
		}
	}
	if len(ours) == 0 {
		b.s.set("skill: item_note", "no monitor", false)
		b.s.set("skill: card on a row", "no monitor", true)
		return
	}
	now := time.Now()
	for _, m := range ours {
		c.lookMonitor(b.sc.ctx, m, now) // the baseline
	}
	f := b.sc.tools
	f.mu.Lock()
	f.arrived = append(f.arrived, map[string]any{"id": "m3", "subject": "Devis terrasse", "from": "Sam <sam@example.org>", "date": "2026-11-20T11:30:00Z",
		"body": "Bonjour,\n\nVoici le devis pour la terrasse : 4 200 €, pose comprise. Peux-tu me dire avant vendredi si on lance la commande ?\n\nSam"})
	f.mu.Unlock()
	ms, _ = c.monitors()
	for _, m := range ms {
		if m.State == monitorOn {
			c.lookMonitor(b.sc.ctx, m, now.Add(time.Second))
		}
	}
	// The event waits in its skill's queue for the skill's period (every):
	// the bench lets that time pass at once, then the run starts, and ends.
	var queued int
	for end := time.Now().Add(10 * time.Second); time.Now().Before(end) && queued == 0; time.Sleep(200 * time.Millisecond) {
		c.store.db.QueryRow(`SELECT COUNT(*) FROM skill_queue`).Scan(&queued)
	}
	if queued == 0 {
		var why []string
		for _, m := range ours {
			if m, err := c.monitorNamed(m.Name); err == nil {
				why = append(why, fmt.Sprintf("%s %s %s/%s cond %s: known %v, seen %d, error %q, waiting %q", m.Name, m.State, m.Spec.Server, m.Spec.Tool, m.Spec.Cond, m.Look.Known, len(m.Look.Seen), m.Look.Error, m.Look.Waiting))
			}
		}
		for _, sk := range c.triggeredSkills() {
			why = append(why, fmt.Sprintf("skill %s when %+v", sk.Name, *sk.Trigger))
		}
		b.note(fmt.Sprintf("skill: no event queued for the new mail: %q", why))
	}
	c.store.db.Exec(`UPDATE skill_queue SET at = at - 86400`)
	c.skillRuns.wake()
	for end := time.Now().Add(5 * time.Minute); time.Now().Before(end); time.Sleep(time.Second) {
		var runs, open int
		c.store.db.QueryRow(`SELECT COUNT(*), COUNT(CASE WHEN state IN ('running', 'waiting') THEN 1 END) FROM skill_runs`).Scan(&runs, &open)
		if runs > 0 && open == 0 && !b.sc.live.busy() {
			break
		}
	}
	b.quiet()
	notes, _ := c.nodeItems(view)
	var by []string
	for _, n := range notes {
		if n.Author != byBrain {
			by = append(by, n.Item)
		}
	}
	b.s.set("skill: item_note", strconv.Itoa(len(by)), len(by) >= 1)
	rows := 0
	if p, err := b.sc.web.w.nodePageOf(view, 0, "fr"); err == nil {
		for _, part := range p.Parts {
			if part.Block == nil {
				continue
			}
			for _, l := range part.Block.Lists {
				for _, it := range l.Items {
					if it.Card != "" {
						rows++
					}
				}
			}
		}
	}
	b.s.set("skill: card on a row", strconv.Itoa(rows), true)
	var state, note string
	c.store.db.QueryRow(`SELECT state, note FROM skill_runs ORDER BY id DESC LIMIT 1`).Scan(&state, &note)
	b.note(fmt.Sprintf("skill run: %q %q; notes on %q", state, truncate(note, 200), by))
}

// end scores what the run left: stuck steps, nodes of one title, the
// links written, lost and duplicate counts.
func (b *bench) end(work string) {
	rows, err := b.sc.c.store.db.Query(`SELECT node, key, status FROM node_steps`)
	if err != nil {
		b.t.Fatal(err)
	}
	var stuck []string
	for rows.Next() {
		var node, key, status string
		rows.Scan(&node, &key, &status)
		if status != stepAccepted && status != stepDropped {
			stuck = append(stuck, fmt.Sprintf("%s: %s", key, status))
		}
	}
	rows.Close()
	b.s.set("stuck steps", strconv.Itoa(len(stuck)), len(stuck) == 0)
	if len(stuck) > 0 {
		b.note(fmt.Sprintf("stuck steps: %q", stuck))
	}
	// A node born in a turn no message of the user's started (a card's
	// yes, a task's end), out of any new work.
	for id, sum := range b.liveNodes() {
		if !b.allowed[id] && !b.codes(id) && !b.viewMade(id) && sum.OpenedBy != nodefile.OpenedSkill { // a skill's run opens its children
			b.reopened = append(b.reopened, fmt.Sprintf("between turns: %q", sum.Title))
			b.allowed[id] = true
		}
	}
	titles := map[string][]string{}
	written, resolved := 0, 0
	for id, sum := range b.liveNodes() {
		k := strings.ToLower(strings.TrimSpace(sum.Title))
		if !b.codes(id) { // a task's node may say its task's title, below the subject it works for
			titles[k] = append(titles[k], id)
		}
		d, err := b.sc.c.nodes.Read(id, 0)
		if err != nil {
			continue
		}
		var names []string
		for _, l := range nodefile.WikiLinks(d.Node.Text()) {
			names = append(names, l.Name)
		}
		found, _ := b.sc.c.nodes.Resolve(id, names)
		written += len(names)
		for _, n := range names {
			if _, ok := found[n]; ok {
				resolved++
			} else {
				b.note(fmt.Sprintf("link [[%s]] in %q names no node", n, sum.Title))
			}
		}
	}
	b.s.set("links written / resolved", fmt.Sprintf("%d / %d", written, resolved), written > 0 && written == resolved)
	for k, ids := range titles {
		if len(ids) > 1 {
			b.sameTitle = append(b.sameTitle, fmt.Sprintf("%q ×%d", k, len(ids)))
		}
	}
	var waiting []string
	for _, sum := range b.liveNodes() {
		if b.sc.c.nodeStatus(sum) == nodeWaits {
			waiting = append(waiting, sum.Title)
		}
	}
	if len(waiting) > 0 {
		b.note(fmt.Sprintf("waiting for the user at the end: %q", waiting))
	}
}

// filed counts every answer code's safety net filed, those of the turns
// a wake started included, and closes the lost and duplicate counts.
func (b *bench) filed() {
	msgs, _ := b.sc.c.store.queryMessages(`SELECT `+messageCols+` FROM messages WHERE role = ? AND filed != '' ORDER BY seq`, roleBrain)
	for _, m := range msgs {
		if !b.counted[m.Seq] {
			b.note(fmt.Sprintf("a wake's turn: %d runes filed in %s", runeLen(m.Text), m.Filed))
			b.wakeFiled++
		}
	}
	lost := append(append(append([]string{}, b.netFiled...), b.unanswered...), b.unfiled...)
	b.s.lost = len(lost)
	b.s.set("lost replies", strconv.Itoa(len(lost)), len(lost) == 0)
	for _, l := range lost {
		b.note("lost: " + l)
	}
	dups := append(append([]string{}, b.reopened...), b.sameTitle...)
	b.s.dups = len(dups)
	b.s.set("duplicate nodes", strconv.Itoa(len(dups)), len(dups) == 0)
	for _, d := range dups {
		b.note("duplicate: " + d)
	}
	b.s.set("wake lines filed by the net", strconv.Itoa(b.wakeFiled), true)
}

// scoreCalls scores the brain's refused writes, and records its node
// writes for the offline replay when record names a file.
func (b *bench) scoreCalls(record string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	grammar, other, budget := 0, 0, 0
	var recorded []replayCall
	for _, x := range b.calls {
		if x.recorded != nil {
			recorded = append(recorded, *x.recorded)
		}
		if strings.HasPrefix(x.err, "Refused") {
			budget++ // the brain's budget (brain_budget.go), any tool
			b.s.note(fmt.Sprintf("refused by the budget, turn %d, %s: %s", x.turn, x.tool, truncate(x.err, 120)))
			continue
		}
		if x.err == "" || !slices.Contains(benchWrites, x.tool) {
			continue
		}
		if x.taintOrStale() {
			other++
		} else {
			grammar++
		}
		b.s.note(fmt.Sprintf("refused, turn %d, %s: %s", x.turn, x.tool, truncate(x.err, 240)))
	}
	b.s.grammar = grammar
	b.s.set("refused writes: grammar, catalog", strconv.Itoa(grammar), grammar <= 1)
	b.s.set("refused writes: taint, stale", strconv.Itoa(other), true)
	b.s.set("refused by the budget", strconv.Itoa(budget), true)
	if record == "" {
		return
	}
	raw, _ := json.MarshalIndent(recorded, "", "  ")
	if err := os.WriteFile(record, append(raw, '\n'), 0o644); err != nil {
		b.t.Errorf("record: %v", err)
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

// sentTo counts the mails sent to to: written to it, or a reply to the
// mail it wrote (m2, the urgent one).
func (f *scenarioTools) sentTo(to string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, s := range f.sent {
		if strings.Contains(strings.ToLower(s), to) {
			n++
		}
	}
	for _, id := range f.replied {
		if id == "m2" {
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

// failedOnce tells the plan's first try has failed.
func (sc *scenario) failedOnce() bool {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	return sc.failed
}

// liveWork is a faked worker for any work but a skill's run: a first step
// (one that comes after none) says where it is and waits for the scenario
// to finish it; the first try of a plan fails, once in a run; then each
// writes a short result that fits its title in its node (a plan of three
// titles, the production's error, an analysis) and reports done.
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
	title := strings.ToLower(t.Title)
	if first {
		sc.call(id, nodesServer, "progress", map[string]any{"text": "3 articles lus"})
		sc.waitFinish("first")
	}
	body := "Ce qui ressort : des phrases courtes, un exemple par idée, un titre qui promet.\n"
	summary := "Fait : " + t.Title + "."
	switch {
	case strings.Contains(title, "plan"):
		sc.mu.Lock()
		fail := !sc.failed
		sc.failed = true
		sc.mu.Unlock()
		if fail {
			sc.failedReceipt(id, "Je n'ai pas pu écrire le plan : l'analyse du style ne m'est pas parvenue.")
			return
		}
		body = "1. Pourquoi ce métier\n2. Ce qui a changé en dix ans\n3. Ce qui vient\n"
		summary = "Le plan en trois titres."
	case strings.Contains(title, "erreur") || strings.Contains(title, "500") || strings.Contains(title, "prod"):
		body = "La file des paiements sature depuis la mise à jour de 10 h : ses appels expirent et l'app renvoie des 500. Revenir à la version d'hier les arrête.\n"
		summary = "La cause : la mise à jour de 10 h sature la file des paiements ; revenir à la version d'hier corrige les 500."
	case strings.Contains(title, "newsletter"):
		body = "Les newsletters qui marchent : un sujet par numéro, un envoi fixe chaque semaine, une voix personnelle.\n"
		summary = "Trois traits des newsletters qui marchent."
	}
	if out, err := sc.call(id, nodesServer, "node_read", map[string]any{}); err == nil {
		hash, _ := out["hash"].(string)
		sc.call(id, nodesServer, "node_write", map[string]any{"expect": hash, "body": body})
	}
	sc.done(id, summary)
}

// failedReceipt ends a worker's turn with a receipt that says it is blocked.
func (sc *scenario) failedReceipt(id, text string) {
	receipt := "\n\n```json\n" + `{"receipt": {"status": "blocked", "summary": "` + text + `"}}` + "\n```"
	sc.report(id, deviceReport{State: stateIdle, Events: sc.events(id,
		EventRow{Type: "text", Data: map[string]any{"text": text + receipt}},
		EventRow{Type: "turn_end", Data: map[string]any{"subtype": "success"}})})
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

// start runs the CLI for a brain task or a skill's run, from its spec as
// its device would.
func (l *liveBrain) start(a deviceAssignment) (*liveSession, error) {
	spec := a.Spec
	if t, err := l.sc.c.store.task(a.ID); err == nil && t.Kind == "brain" && !strings.Contains(spec.AppendSystemPrompt, "## Les nœuds") {
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
	// Of its harness's tools, only the search for deferred tools: nothing
	// reaches the web or this machine's shell, the bench's world is the
	// test core.
	var harness []string
	if slices.Contains(spec.HarnessTools, "ToolSearch") {
		harness = []string{"ToolSearch"}
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
