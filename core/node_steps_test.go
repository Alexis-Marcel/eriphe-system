package main

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Alexis-Marcel/eriphe-system/core/nodestore"
)

// graphCore is startCore with the graph open: a device, the GitHub tool on
// owner/repo and owner/site (their changes free), and nodes.
func graphCore(t *testing.T) *core {
	t.Helper()
	c := startCore(t)
	openGraph(t, c)
	return c
}

// withGraph opens an empty graph for a core, before its pool runs: the
// pool's servers read c.nodes as soon as they list their tools, so a core
// whose pool is running is given its graph by its helper, never after.
func withGraph(t *testing.T) func(*core) {
	return func(c *core) { openGraph(t, c) }
}

// openGraph opens an empty graph for c, whose pool does not run yet (see
// withGraph).
func openGraph(t *testing.T, c *core) {
	t.Helper()
	dir := t.TempDir()
	s, _, err := nodestore.Open(filepath.Join(dir, "nodes"), filepath.Join(dir, "nodes.db"), nodestore.Options{Refuse: refuseSecret})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	t.Cleanup(c.reviewing.Wait) // before it closes
	c.nodes = s
}

// workNode is a node to cut in steps, with a goal or none.
func workNode(t *testing.T, c *core, goal string) string {
	t.Helper()
	d, err := c.nodes.Create(nodestore.New{Title: "Le site", Goal: goal, Author: "brain"})
	if err != nil {
		t.Fatal(err)
	}
	return d.Node.Meta.ID
}

// stepNode is a step's node by its key.
func stepNode(t *testing.T, ids map[string]string, key string) string {
	t.Helper()
	id, ok := ids[key]
	if !ok {
		t.Fatalf("no step %s in %v", key, ids)
	}
	return id
}

// stepNow is a step as the engine holds it.
func stepNow(t *testing.T, c *core, node string) Step {
	t.Helper()
	st, ok := c.nodeStep(node)
	if !ok {
		t.Fatalf("no step %s", node)
	}
	return st
}

// endStepTurn ends a task's turn with a message and a receipt of that status,
// and the checks the device ran, as notifyBrain is told of it.
func endStepTurn(t *testing.T, c *core, task, text, status string, checks ...map[string]any) {
	t.Helper()
	tk, err := c.store.task(task)
	if err != nil {
		t.Fatal(err)
	}
	seq := tk.LastSeq
	receipt := "\n\n```json\n" + `{"receipt": {"status": "` + status + `", "summary": "` + text + `"}}` + "\n```"
	events := []EventRow{ev(seq+1, "text", map[string]any{"text": text + receipt}), ev(seq+2, "turn_end", map[string]any{"subtype": "success"})}
	for i, ch := range checks {
		events = append(events, ev(seq+3+i, "check", ch))
	}
	idleTask(t, c, task, events...)
}

// wakes are the brain's wakes for a work: its events that name a node.
func wakes(c *core) []string {
	msgs, _ := c.store.messagesAfter(0, 500)
	var out []string
	for _, m := range msgs {
		if m.Role == roleEvent && !m.UserAct && strings.HasPrefix(m.Text, "[nœud ") { // the work's, not what came of a card
			out = append(out, m.Text)
		}
	}
	return out
}

// startCards are the task start cards waiting.
func startCards(t *testing.T, c *core) int {
	t.Helper()
	xs, err := c.cardsWhere(`kind = ? AND state = ?`, cardTaskStart, cardPending)
	if err != nil {
		t.Fatal(err)
	}
	return len(xs)
}

// Three steps, two in parallel then one after both, run to the end with
// no turn of the brain: each child node is opened with what it comes
// after, each task starts as the ones before it are accepted on their
// proof, is given what they produced; once all are accepted the work is
// done and its result, the last step, waits for the user until they, or
// Eriphe on their word, say it is done.
func TestStepsRunWithoutTheBrain(t *testing.T) {
	c := graphCore(t)
	ctx := context.Background()
	parent := workNode(t, c, "")
	ids, err := c.openSteps(ctx, parent, []stepSpec{
		{Key: "names", Title: "Chercher des noms", Prompt: "Propose dix noms."},
		{Key: "market", Title: "Étudier le marché", Prompt: "Liste les boutiques proches."},
		{Key: "check", Title: "Vérifier les noms", Prompt: "Vérifie quels noms sont libres.", After: []string{"names", "market"}},
	}, 0, stepOpener{author: "brain"})
	if err != nil {
		t.Fatal(err)
	}
	names, market, check := stepNode(t, ids, "names"), stepNode(t, ids, "market"), stepNode(t, ids, "check")
	for _, id := range []string{names, market, check} {
		sum, err := c.nodes.Get(id)
		if err != nil || sum.Parent != parent {
			t.Fatalf("step node %s: %+v %v", id, sum, err)
		}
	}
	a, b, k := stepNow(t, c, names), stepNow(t, c, market), stepNow(t, c, check)
	if !slices.Equal(k.After, []string{names, market}) {
		t.Fatalf("check comes after %v", k.After)
	}
	if d, _ := c.nodes.Read(check, 0); strings.Contains(string(d.Raw), "\nafter:") {
		t.Fatalf("its file says what code keeps:\n%s", d.Raw)
	}
	if a.Status != stepRunning || b.Status != stepRunning || k.Status != stepWaiting || a.Task == "" || b.Task == "" || k.Task != "" {
		t.Fatalf("at the start: %s %s %s", a.Status, b.Status, k.Status)
	}
	// Each task works in its step's node.
	if c.taskNode(a.Task) != names || c.taskNode(b.Task) != market {
		t.Fatalf("task nodes %q %q", c.taskNode(a.Task), c.taskNode(b.Task))
	}
	if s := nodeStatusNow(t, c, names); s != nodeRunning {
		t.Errorf("a running step's node is %s", s)
	}

	endStepTurn(t, c, a.Task, "Lumen, Aster, Rivet", "done")
	if st := stepNow(t, c, names); st.Status != stepAccepted || !strings.Contains(st.Output, "Lumen") {
		t.Fatalf("names: %+v", st)
	}
	if st := stepNow(t, c, check); st.Status != stepWaiting {
		t.Fatalf("check started before market: %s", st.Status)
	}
	endStepTurn(t, c, b.Task, "Trois boutiques", "done")
	k = stepNow(t, c, check)
	if k.Status != stepRunning || k.Task == "" {
		t.Fatalf("check: %+v", k)
	}
	spec, _ := c.store.taskSpec(k.Task)
	if !strings.Contains(spec.Prompt, "Lumen, Aster, Rivet") || !strings.Contains(spec.Prompt, "Trois boutiques") {
		t.Errorf("check is not given what came before:\n%s", spec.Prompt)
	}
	endStepTurn(t, c, k.Task, "Lumen est libre", "done")

	if w, _ := c.work(parent); w.State != workDone {
		t.Fatalf("work %s", w.State)
	}
	if s := nodeStatusNow(t, c, parent); s != nodeDone {
		t.Errorf("the parent is %s", s)
	}
	if s := nodeStatusNow(t, c, names); s != nodeDone {
		t.Errorf("an accepted step's node is %s", s)
	}
	if s := nodeStatusNow(t, c, check); s != nodeWaits {
		t.Errorf("the work's result is %s", s)
	}
	// The user's word stands: the result said done stays done.
	sum, _ := c.nodes.Get(check)
	if _, err := c.setNodeDone(sum, true, byUser); err != nil {
		t.Fatal(err)
	}
	if s := nodeStatusNow(t, c, check); s != nodeDone {
		t.Errorf("a result said done is %s", s)
	}
	// Had the last step been dropped, the ones before it would be the result.
	c.setStep(check, map[string]any{"status": stepDropped})
	if s := nodeStatusNow(t, c, names); s != nodeWaits {
		t.Errorf("the result, its sequel dropped, is %s", s)
	}

	if w := wakes(c); len(w) != 0 {
		t.Fatalf("the brain was woken: %q", w)
	}
	msgs, _ := c.store.messagesAfter(0, 500)
	for _, m := range msgs {
		if m.Role == roleEvent && (strings.Contains(m.Text, a.Task) || strings.Contains(m.Text, k.Task)) {
			t.Errorf("a step's task told the brain: %q", m.Text)
		}
	}
	if trace, _ := c.nodes.Trace(parent); !strings.Contains(string(trace), "acceptée sur sa preuve") {
		t.Errorf("the parent's trace:\n%s", trace)
	}
}

// A step whose proof fails wakes the brain once, with what to decide;
// node_settle retry asks it again with what to fix, and its proof then
// accepts it. A step whose checks failed is never accepted by the brain.
func TestFailedProofWakesOnce(t *testing.T) {
	c := graphCore(t)
	ctx := context.Background()
	parent := workNode(t, c, "")
	ids, err := c.openSteps(ctx, parent, []stepSpec{{Key: "page", Title: "Écrire la page", Prompt: "Écris la page d'accueil."}}, 0, stepOpener{author: "brain"})
	if err != nil {
		t.Fatal(err)
	}
	page := stepNode(t, ids, "page")
	st := stepNow(t, c, page)
	endStepTurn(t, c, st.Task, "Page écrite", "done", map[string]any{"name": "tests", "ok": false, "run": "go test ./...", "exit_code": 1, "output": "FAIL"})
	if st = stepNow(t, c, page); st.Status != stepFailed || !st.Failed {
		t.Fatalf("a failed check: %+v", st)
	}
	w := wakes(c)
	if len(w) != 1 || !strings.Contains(w[0], "[nœud "+parent+" « Le site »]") || !strings.Contains(w[0], page) || !strings.Contains(w[0], "node_settle") {
		t.Fatalf("wakes %q", w)
	}
	// The same end told again wakes nothing more.
	c.stepTaskChanged(st.Task, stateIdle, turnOutcome{})
	c.stepsTick()
	if len(wakes(c)) != 1 {
		t.Fatalf("woken again: %q", wakes(c))
	}

	settle := func(args map[string]any) (map[string]any, bool) {
		t.Helper()
		return callBrainTool(t, c, "node_settle", args)
	}
	if out, isErr := settle(map[string]any{"id": parent, "step": page, "outcome": "accept"}); !isErr {
		t.Fatalf("a failed check accepted: %v", out)
	}
	if out, isErr := settle(map[string]any{"id": parent, "step": page, "outcome": "retry"}); !isErr {
		t.Fatalf("a retry with nothing to fix: %v", out)
	}
	if out, isErr := settle(map[string]any{"id": parent, "step": page, "outcome": "retry", "note": "Fais passer les tests."}); isErr {
		t.Fatalf("retry: %v", out)
	}
	st = stepNow(t, c, page)
	if st.Status != stepRunning || st.Tries != 1 {
		t.Fatalf("retried: %+v", st)
	}
	// Its task, idle, is told what to fix as its next turn.
	items, _ := c.store.inbox(st.Task)
	if len(items) == 0 || !strings.Contains(fmt.Sprint(items[len(items)-1].Data["text"]), "Fais passer les tests.") {
		t.Fatalf("not told what to fix: %+v", items)
	}
	endStepTurn(t, c, st.Task, "Tests verts", "done", map[string]any{"name": "tests", "ok": true, "run": "go test ./..."})
	if st = stepNow(t, c, page); st.Status != stepAccepted {
		t.Fatalf("after the retry: %+v", st)
	}
	if w, _ := c.work(parent); w.State != workDone || len(wakes(c)) != 1 {
		t.Fatalf("work %s, wakes %q", w.State, wakes(c))
	}
}

// A step whose receipt is not done may be accepted by the brain as it is;
// one dropped takes the steps after it along; replan leaves the work
// replaced by the new steps given with it, which run it again.
func TestSettleAcceptDropReplan(t *testing.T) {
	c := graphCore(t)
	ctx := context.Background()
	parent := workNode(t, c, "")
	ids, err := c.openSteps(ctx, parent, []stepSpec{
		{Key: "a", Title: "A", Prompt: "Fais A."},
		{Key: "b", Title: "B", Prompt: "Fais B.", After: []string{"a"}},
		{Key: "c", Title: "C", Prompt: "Fais C.", After: []string{"b"}},
	}, 0, stepOpener{author: "brain"})
	if err != nil {
		t.Fatal(err)
	}
	a := stepNow(t, c, ids["a"])
	endStepTurn(t, c, a.Task, "A à moitié", "partial")
	if _, err := c.settleStep(ctx, parent, ids["a"], settleAccept, "Assez pour la suite."); err != nil {
		t.Fatal(err)
	}
	b := stepNow(t, c, ids["b"])
	if stepNow(t, c, ids["a"]).Status != stepAccepted || b.Status != stepRunning {
		t.Fatalf("after accept: %s", b.Status)
	}
	// A replan gives its new steps: one refused changes nothing.
	if _, err := c.settleStep(ctx, parent, ids["b"], settleReplan, "Autre chemin."); err == nil {
		t.Error("a replan without its steps")
	}
	if _, err := c.replan(ctx, parent, ids["b"], "Autre chemin.", []stepSpec{{Key: "b2", Title: "B autrement", Prompt: "Fais B autrement.", After: []string{ids["c"]}}}, nil); err == nil {
		t.Error("a new step after one the replan drops")
	}
	if stepNow(t, c, ids["b"]).Status != stepRunning || stepNow(t, c, ids["c"]).Status != stepWaiting {
		t.Fatal("a refused replan dropped steps")
	}
	note, err := c.replan(ctx, parent, ids["b"], "Autre chemin.", []stepSpec{{Key: "b2", Title: "B autrement", Prompt: "Fais B autrement.", After: []string{ids["a"]}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if stepNow(t, c, ids["b"]).Status != stepDropped || stepNow(t, c, ids["c"]).Status != stepDropped {
		t.Fatal("replan left the steps after it")
	}
	b2, ok := c.stepOfTask(stepKeyTask(t, c, parent, "b2"))
	if w, _ := c.work(parent); !ok || w.State != workRunning || b2.Status != stepRunning {
		t.Fatalf("the new plan (%s): work %s, step %s", note, w.State, b2.Status)
	}
	endStepTurn(t, c, b2.Task, "B fait", "done")
	if w, _ := c.work(parent); w.State != workDone {
		t.Fatalf("work %s", w.State)
	}
	if _, err := c.settleStep(ctx, parent, ids["c"], "maybe", ""); err == nil {
		t.Error("an outcome that is none")
	}
}

// A step that cannot start, or whose task stops, is blocked and wakes the
// brain; two steps writing the same repository never run at once.
func TestBlockedStepAndWriteTurns(t *testing.T) {
	c := graphCore(t)
	ctx := context.Background()
	parent := workNode(t, c, "")
	write := []ResourceRef{{Kind: resRepo, Ref: "owner/repo", Access: accessWrite}}
	ids, err := c.openSteps(ctx, parent, []stepSpec{
		{Key: "one", Title: "Un", Prompt: "Change un.", Resources: write},
		{Key: "two", Title: "Deux", Prompt: "Change deux.", Resources: write},
	}, 0, stepOpener{author: "brain"})
	if err != nil {
		t.Fatal(err)
	}
	one, two := stepNow(t, c, ids["one"]), stepNow(t, c, ids["two"])
	if one.Status != stepRunning || two.Status != stepWaiting {
		t.Fatalf("both write owner/repo at once: %s %s", one.Status, two.Status)
	}
	c.store.updateTask(one.Task, map[string]any{"state": stateFailed})
	c.notifyBrain(one.Task, stateFailed)
	if st := stepNow(t, c, ids["one"]); st.Status != stepBlocked {
		t.Fatalf("a failed task's step: %s", st.Status)
	}
	if w := wakes(c); len(w) != 1 || !strings.Contains(w[0], "s'est arrêtée") {
		t.Fatalf("wakes %q", w)
	}
	// Blocked, it no longer writes: the other takes its turn.
	if st := stepNow(t, c, ids["two"]); st.Status != stepRunning {
		t.Fatalf("two: %s", st.Status)
	}
}

// What a step opened by a skill's run may start without a card: what the
// skill declares (its tools, its repositories), lent at what the user's
// rights leave free, on a clean run. An external event, a tainted opener,
// a repository it does not declare or a case on approval: a card.
func TestSkillStepsStartWithoutCard(t *testing.T) {
	c := graphCore(t)
	ctx := context.Background()
	repoTool(t, c, "acme", accessGrid{Read: gridFree, Change: gridApproval, Delete: gridApproval, Send: gridApproval})
	c.skillRuns.mu.Lock()
	c.skillRuns.cache = map[string]skillManifest{"sha1": {Name: "veille", Uses: skillUses{Repos: []string{"owner/repo", "acme/app"}}, Trigger: &skillTrigger{}}}
	c.skillRuns.mu.Unlock()
	run := func(id string, external bool) {
		t.Helper()
		c.store.insertTask(&TaskRow{ID: id, Kind: "worker", Device: "nas", Agent: "sonnet", Runtime: "claude-code", Prompt: "Veille", State: stateRunning})
		if _, err := c.store.db.Exec(`INSERT INTO skill_runs (skill, sha, task, state, external, created) VALUES ('veille', 'sha1', ?, ?, ?, ?)`,
			id, skillRunOpen, external, time.Now().Unix()); err != nil {
			t.Fatal(err)
		}
	}
	open := func(key, repo string, by stepOpener) (Step, error) {
		t.Helper()
		parent := workNode(t, c, "")
		ids, err := c.openSteps(ctx, parent, []stepSpec{{Key: key, Title: "Corriger", Prompt: "Corrige le bug.",
			Resources: []ResourceRef{{Kind: resRepo, Ref: repo, Access: accessWrite, Branch: "eriphe/fix"}}}}, 0, by)
		if err != nil {
			return Step{}, err
		}
		return stepNow(t, c, ids[key]), nil
	}
	started := func(st Step) bool {
		t.Helper()
		tk, err := c.store.task(st.Task)
		return err == nil && c.startState(tk)["start"] == "started"
	}

	run("t-clean", false)
	st, err := open("fix", "owner/repo", stepOpener{author: "skill:veille", task: "t-clean"})
	if err != nil {
		t.Fatal(err)
	}
	if !st.Clean || st.Skill != "veille@sha1" || !started(st) || startCards(t, c) != 0 {
		xs, _ := c.cardsWhere(`state = ?`, cardPending)
		t.Fatalf("a clean, declared, free step: %+v, %d cards %+v", st, startCards(t, c), xs)
	}

	for name, tc := range map[string]struct {
		run      string
		external bool
		repo     string
		tainted  []string
	}{
		"an external event":     {"t-ext", true, "owner/repo", nil},
		"a repository not said": {"t-other", false, "owner/site", nil},
		"a case on approval":    {"t-acme", false, "acme/app", nil},
		"a tainted opener":      {"t-taint", false, "owner/repo", []string{"mail:7"}},
	} {
		run(tc.run, tc.external)
		before := startCards(t, c)
		st, err := open("fix", tc.repo, stepOpener{author: "skill:veille", task: tc.run, tainted: tc.tainted})
		if err != nil {
			if tc.tainted == nil {
				t.Errorf("%s: %v", name, err)
			}
			continue // a tainted opener may not lend a write at all
		}
		if started(st) || startCards(t, c) != before+1 {
			t.Errorf("%s: started without a card (clean %v, %d cards)", name, st.Clean, startCards(t, c))
		}
	}

	// A step the brain opens asks its card too.
	before := startCards(t, c)
	if st, err := open("fix", "owner/repo", stepOpener{author: "brain"}); err != nil || started(st) || startCards(t, c) != before+1 {
		t.Errorf("the brain's step: %+v %v", st, err)
	}
	// A step of a clean step borrows the same, on what the skill declares.
	parentStep := st
	if sub, err := open("sub", "owner/repo", stepOpener{author: "task:" + parentStep.Task, task: parentStep.Task}); err != nil || !sub.Clean || !started(sub) {
		t.Errorf("a step's own step: %+v %v", sub, err)
	}
}

// The budget holds the work: nothing new starts, the brain is woken once;
// node_settle accept on the work gives it more time and it goes on.
func TestBudgetHoldsTheWork(t *testing.T) {
	c := graphCore(t)
	ctx := context.Background()
	parent := workNode(t, c, "")
	ids, err := c.openSteps(ctx, parent, []stepSpec{
		{Key: "a", Title: "A", Prompt: "Fais A."},
		{Key: "b", Title: "B", Prompt: "Fais B.", After: []string{"a"}},
	}, 30, stepOpener{author: "brain"})
	if err != nil {
		t.Fatal(err)
	}
	c.store.db.Exec(`UPDATE node_works SET since = since - 3600 WHERE parent = ?`, parent)
	a := stepNow(t, c, ids["a"])
	endStepTurn(t, c, a.Task, "A fait", "done")
	if w, _ := c.work(parent); w.State != workHeld || stepNow(t, c, ids["b"]).Status != stepWaiting {
		t.Fatalf("past its budget: work %s, b %s", w.State, stepNow(t, c, ids["b"]).Status)
	}
	if w := wakes(c); len(w) != 1 || !strings.Contains(w[0], "budget") {
		t.Fatalf("wakes %q", w)
	}
	c.stepsTick()
	if len(wakes(c)) != 1 {
		t.Fatal("woken again by the tick")
	}
	if out, isErr := callBrainTool(t, c, "node_settle", map[string]any{"id": parent, "outcome": "accept", "note": "Il reste peu."}); isErr {
		t.Fatalf("more time: %v", out)
	}
	if w, _ := c.work(parent); w.State != workRunning || stepNow(t, c, ids["b"]).Status != stepRunning {
		t.Fatalf("after more time: work %s", w.State)
	}
	// Dropped, the work stops with its steps.
	if _, err := c.settleStep(ctx, parent, "", settleDrop, ""); err != nil {
		t.Fatal(err)
	}
	if w, _ := c.work(parent); w.State != workStopped || stepNow(t, c, ids["b"]).Status != stepDropped {
		t.Fatalf("dropped: work %s", w.State)
	}
}

// A work with a goal ends with a fresh agent's check, in a child node of
// the work, lent read-only what the steps wrote; it holds or wakes the
// brain once.
func TestFinalCheck(t *testing.T) {
	c := graphCore(t)
	ctx := context.Background()
	parent := workNode(t, c, "La page d'accueil est en ligne.")
	ids, err := c.openSteps(ctx, parent, []stepSpec{{Key: "page", Title: "Écrire la page", Prompt: "Écris la page.",
		Resources: []ResourceRef{{Kind: resRepo, Ref: "owner/site", Access: accessWrite}}}}, 0, stepOpener{author: "brain"})
	if err != nil {
		t.Fatal(err)
	}
	// The brain's step asks its card: approved, it starts.
	xs, _ := c.cardsWhere(`kind = ? AND state = ?`, cardTaskStart, cardPending)
	if len(xs) != 1 {
		t.Fatalf("start cards %+v", xs)
	}
	if _, err := c.decideCard(ctx, xs[0].ID, true, "web"); err != nil {
		t.Fatal(err)
	}
	st := stepNow(t, c, ids["page"])
	endStepTurn(t, c, st.Task, "Page écrite", "done")
	w, _ := c.work(parent)
	if w.State != workChecking || w.Final == "" {
		t.Fatalf("work %+v", w)
	}
	final, _ := c.store.task(w.Final)
	if final.Title != "Contrôle de l'objectif" || len(final.Resources) != 1 || final.Resources[0].Access != accessRead {
		t.Fatalf("the final check: %+v", final)
	}
	if sum, _ := c.nodes.Get(c.taskNode(final.ID)); sum.Parent != parent {
		t.Fatalf("its node: %+v", sum)
	}
	if tk, _ := c.store.task(w.Final); c.startState(tk)["start"] != "started" {
		t.Errorf("a read-only check asked a card: %v", c.startState(tk))
	}
	endStepTurn(t, c, w.Final, "Pas encore en ligne", "partial")
	if w, _ = c.work(parent); w.State != workFailed || len(wakes(c)) != 1 {
		t.Fatalf("a check that does not hold: %s, %q", w.State, wakes(c))
	}
	if _, err := c.settleStep(ctx, parent, "", settleRetry, ""); err != nil {
		t.Fatal(err)
	}
	w, _ = c.work(parent)
	endStepTurn(t, c, w.Final, "En ligne", "done")
	if w, _ = c.work(parent); w.State != workDone || w.Checks != 2 {
		t.Fatalf("after the second check: %+v", w)
	}
	// A replan on the work itself adds steps, after those it keeps: it runs again.
	if _, err := c.replan(ctx, parent, "", "Une page de plus.", []stepSpec{{Key: "faq", Title: "FAQ", Prompt: "Écris la FAQ.", After: []string{ids["page"]}}}, nil); err != nil {
		t.Fatal(err)
	}
	if w, _ = c.work(parent); w.State != workRunning {
		t.Fatalf("after a replan: %s", w.State)
	}
	if st, ok := c.stepOfTask(stepKeyTask(t, c, parent, "faq")); !ok || st.Status != stepRunning {
		t.Fatalf("the step added: %+v", st)
	}
}

// What the engine holds is in core.db: a core started again on the same
// data chooses a step's task that ended its turn meanwhile, accepts it on
// its proof and starts the next.
func TestStepsResumeAfterRestart(t *testing.T) {
	c := graphCore(t)
	ctx := context.Background()
	parent := workNode(t, c, "")
	ids, err := c.openSteps(ctx, parent, []stepSpec{
		{Key: "a", Title: "A", Prompt: "Fais A."},
		{Key: "b", Title: "B", Prompt: "Fais B.", After: []string{"a"}},
	}, 0, stepOpener{author: "brain"})
	if err != nil {
		t.Fatal(err)
	}
	a := stepNow(t, c, ids["a"])
	// Its turn ends while eriphe-core is down: nobody is told.
	receipt := "\n\n```json\n" + `{"receipt": {"status": "done", "summary": "A fait"}}` + "\n```"
	c.store.addEvents(a.Task, []EventRow{ev(1, "text", map[string]any{"text": "A fait" + receipt}), ev(2, "turn_end", map[string]any{"subtype": "success"})})
	c.store.updateTask(a.Task, map[string]any{"state": stateIdle})

	again := newCore(c.conf(), c.store)
	again.nodes = c.nodes
	again.stepsTick()
	if st := stepNow(t, again, ids["a"]); st.Status != stepAccepted {
		t.Fatalf("a after the restart: %s", st.Status)
	}
	if st := stepNow(t, again, ids["b"]); st.Status != stepRunning || st.Task == "" {
		t.Fatalf("b after the restart: %+v", st)
	}
	if len(wakes(again)) != 0 {
		t.Fatalf("wakes %q", wakes(again))
	}
}

// A plan holds together: keys unique, what a step comes after exists, no
// cycle, a prompt each; it comes back in an order where each step follows
// those it needs.
func TestStepPlanChecked(t *testing.T) {
	task := func(key string, after ...string) stepSpec {
		return stepSpec{Key: key, Title: "Step " + key, Prompt: "do " + key, After: after}
	}
	for name, steps := range map[string][]stepSpec{
		"none":      nil,
		"duplicate": {task("a"), task("a")},
		"unknown":   {task("a", "z")},
		"self":      {task("a", "a")},
		"cycle":     {task("a", "c"), task("b", "a"), task("c", "b")},
		"no prompt": {{Key: "a", Title: "A"}},
		"bad key":   {{Key: "A B", Title: "A", Prompt: "x"}},
	} {
		if _, err := checkPlan(nil, steps); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	plan, err := checkPlan(nil, []stepSpec{task("c", "a", "b"), task("a"), task("b", "a")})
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for _, s := range plan {
		keys = append(keys, s.Key)
	}
	if strings.Join(keys, ",") != "a,b,c" {
		t.Errorf("order %v", keys)
	}
}

// A task opens steps through node_open: a child of its node with the goal,
// its steps under it; a plan refused opens nothing.
func TestNodeOpenWithSteps(t *testing.T) {
	c := graphCore(t)
	task, err := c.runTask(context.Background(), runRequest{Title: "Refonte", Prompt: "Refais le site."})
	if err != nil {
		t.Fatal(err)
	}
	mine := c.taskNode(task.ID)
	if _, msg := callNodes(t, c, task.ID, "node_open", map[string]any{"title": "Pages", "goal": "Les pages sont écrites.",
		"steps": []any{map[string]any{"key": "a", "title": "A", "prompt": "Fais A.", "after": []any{"z"}}}}); !strings.Contains(msg, "no step of this plan") {
		t.Fatalf("a bad plan: %q", msg)
	}
	if kids, _ := c.nodes.Children(mine); len(kids) != 0 {
		t.Fatalf("a refused plan opened %d nodes", len(kids))
	}
	out := mustNodes(t, c, task.ID, "node_open", map[string]any{"title": "Pages", "goal": "Les pages sont écrites.", "budget_minutes": 45,
		"steps": []any{map[string]any{"key": "home", "title": "Accueil", "prompt": "Écris l'accueil."}}})
	id := out["id"].(string)
	steps, _ := out["steps"].(map[string]any)
	home, _ := steps["home"].(string)
	st := stepNow(t, c, home)
	if st.Parent != id || st.Opener != "task:"+task.ID || st.Status != stepRunning {
		t.Fatalf("its step: %+v", st)
	}
	if w, _ := c.work(id); w.Budget != 45 {
		t.Errorf("budget %d", w.Budget)
	}
}

// stepKeyTask is the task of a work's step, by its key.
func stepKeyTask(t *testing.T, c *core, parent, key string) string {
	t.Helper()
	steps, _ := c.workSteps(parent)
	for _, st := range steps {
		if st.Spec.Key == key {
			return st.Task
		}
	}
	t.Fatalf("no step %s", key)
	return ""
}
