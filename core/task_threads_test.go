package main

import (
	"context"
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// taskThreadsSession connects to eriphe-threads as a task sees it.
func taskThreadsSession(t *testing.T, c *core, task string) *mcp.ClientSession {
	t.Helper()
	tk, err := c.store.task(task)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	ct, st := mcp.NewInMemoryTransports()
	if _, err := c.taskThreadsServer(tk, c.taskGrant(tk)).Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	sess, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sess.Close() })
	return sess
}

// callTaskThreads calls one of eriphe-threads' tools as a task.
func callTaskThreads(t *testing.T, c *core, task, name string, args map[string]any) (map[string]any, bool) {
	t.Helper()
	res, err := taskThreadsSession(t, c, task).CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	raw, _ := json.Marshal(res.StructuredContent)
	json.Unmarshal(raw, &out)
	if res.IsError && len(res.Content) > 0 {
		if tc, ok := res.Content[0].(*mcp.TextContent); ok {
			out = map[string]any{"error": tc.Text}
		}
	}
	return out, res.IsError
}

// runThreadOf is the thread code gave a run.
func runThreadOf(c *core, r *skillRun) int64 {
	var id int64
	c.store.db.QueryRow(`SELECT thread FROM skill_runs WHERE id = ?`, r.ID).Scan(&id)
	return id
}

// alertsWatcher is a core with a monitor of new alerts and a skill lent
// eriphe-threads that watches them.
func alertsWatcher(t *testing.T) (*core, *fakeMonitor, *time.Time) {
	t.Helper()
	c, fm, now := pollCore(t)
	c.store.registerNode("pc", hashSecret("pc-secret"))
	c.store.setNodeStatus("pc", true, []byte(`{"runtimes":["claude-code"]}`))
	c.subscribeEvents(c.queueSkillEvent)
	fm.mu.Lock()
	fm.rules = []map[string]any{{"uid": "a", "name": "5xx", "state": "firing"}}
	fm.mu.Unlock()
	approvedMonitor(t, c, "alertes", monitorSpec{Server: "monitor", Tool: "alerts", Every: 1, Cond: condNew, Path: "data.rules", ID: "uid"})
	addTriggered(t, c, "---\nname: veille-alertes\ndescription: Trie les alertes de la prod.\nwhen: monitor.new key=alertes\ntools: [eriphe-threads, monitor]\n---\nNote chaque alerte sur ton fil.\n")
	lookAt(c, *now) // the baseline
	return c, fm, now
}

// A watcher's first run gets the thread of its alias, opened by code,
// standing, titled by the skill, carrying its monitor's list; it notes
// each of its three alerts there; its next run finds the same thread. It
// sees no tool to settle, close, stop or approve; a run on external events
// writes tainted notes.
func TestSkillTendsItsThread(t *testing.T) {
	c, fm, now := alertsWatcher(t)
	fm.mu.Lock()
	fm.rules = append(fm.rules, map[string]any{"uid": "b", "name": "down"}, map[string]any{"uid": "c", "name": "certs"}, map[string]any{"uid": "d", "name": "disk"})
	fm.mu.Unlock()
	lookAt(c, now.Add(time.Minute))
	lookAt(c, now.Add(2*time.Minute))
	if es := eventsOf(t, c, evMonitorNew); len(es) != 3 {
		t.Fatalf("events %+v", es)
	}
	c.tickSkills(context.Background(), now.Add(2*time.Minute))
	rs := runsOfSkill(t, c, "veille-alertes")
	if len(rs) != 1 || rs[0].Task == "" || len(rs[0].Events) != 3 {
		t.Fatalf("runs %+v", rs)
	}
	r := rs[0]
	id := runThreadOf(c, r)
	th, err := c.thread(id)
	if err != nil || th.Ends != endsStanding || th.Subject != "topic:alerte" || th.Title != "Veille alertes" || th.Tainted != "" || th.led() {
		t.Fatalf("thread %d %+v %v", id, th, err)
	}
	srcs := c.threadSources(id)
	if len(srcs) != 1 || srcs[0].Name != "alertes" || srcs[0].Tool != "monitor/alerts" || srcs[0].Items == nil || srcs[0].Items.ID != "uid" {
		t.Fatalf("sources %+v", srcs)
	}
	task, _ := c.store.task(r.Task)
	if !strings.Contains(task.Prompt, "Ton fil") || !strings.Contains(task.Prompt, "« Veille alertes »") || !strings.Contains(task.Prompt, "alertes") {
		t.Fatalf("the run is not told its thread:\n%s", task.Prompt)
	}

	// What it may call: never settle, close, stop, reopen nor approve.
	tools, err := taskThreadsSession(t, c, r.Task).ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, x := range tools.Tools {
		names = append(names, x.Name)
	}
	slices.Sort(names)
	if strings.Join(names, ",") != "add_step,find_thread,link_thread,note_item,open_thread,read_thread,set_thread" {
		t.Fatalf("tools %v", names)
	}

	tainted := c.taskResultTaint(r.Task) != ""
	if !tainted {
		t.Fatal("a run on the monitor's external events is not tainted")
	}
	for _, uid := range []string{"b", "c", "d"} {
		if out, isErr := callTaskThreads(t, c, r.Task, "note_item", map[string]any{"source": "alertes", "item": uid, "note": "Alerte " + uid, "state": "look"}); isErr {
			t.Fatalf("note_item %s: %v", uid, out)
		}
	}
	notes := c.itemNotes(id)
	if len(notes) != 3 {
		t.Fatalf("notes %+v", notes)
	}
	for _, n := range notes {
		if n.Author != "skill:veille-alertes" || n.Tainted == "" {
			t.Errorf("note %+v: its author or its taint", n)
		}
	}
	c.surf.refreshing.Wait()
	if row := itemRows(t, c, id)["b"]; row["note"] != "Alerte b" || row["external"] != true || row["by"] != "Noté par le skill « veille-alertes »" {
		t.Fatalf("row %v", row)
	}
	if r = endTurn(t, c, r.Task, "Trois alertes notées."); r.State == skillRunOpen {
		t.Fatalf("run %+v", r)
	}

	// The next run: the same thread, no other.
	fm.mu.Lock()
	fm.rules = append(fm.rules, map[string]any{"uid": "e", "name": "dns"})
	fm.mu.Unlock()
	lookAt(c, now.Add(3*time.Minute))
	lookAt(c, now.Add(4*time.Minute))
	c.tickSkills(context.Background(), now.Add(4*time.Minute))
	rs = runsOfSkill(t, c, "veille-alertes")
	if len(rs) != 2 || runThreadOf(c, rs[0]) != id {
		t.Fatalf("runs %+v: not its thread %d", rs, id)
	}
	if ss, _ := c.threadsWhere(`subject = ?`, "topic:alerte"); len(ss) != 1 {
		t.Fatalf("threads %+v", ss)
	}
	if len(c.threadSources(id)) != 1 {
		t.Fatalf("the source again: %+v", c.threadSources(id))
	}
	if out, isErr := callTaskThreads(t, c, rs[0].Task, "note_item", map[string]any{"source": "alertes", "item": "e", "note": "DNS"}); isErr {
		t.Fatalf("note_item: %v", out)
	}
	if len(c.itemNotes(id)) != 4 {
		t.Fatalf("notes %+v", c.itemNotes(id))
	}
}

// A run's writes carry its taint, read at each call: clean on events of
// its own, tainted once it read external content.
func TestSkillThreadWritesTaint(t *testing.T) {
	c, _, now := alertsWatcher(t)
	c.emit(journalEvent{Kind: evMonitorNew, Key: "alertes", Source: "monitor:alertes", At: *now, Payload: map[string]any{"id": "a"}})
	c.tickSkills(context.Background(), *now)
	r := runsOfSkill(t, c, "veille-alertes")[0]
	id := runThreadOf(c, r)
	if c.taskResultTaint(r.Task) != "" || id == 0 {
		t.Fatalf("run %+v, thread %d", r, id)
	}
	if out, isErr := callTaskThreads(t, c, r.Task, "note_item", map[string]any{"source": "alertes", "item": "a", "note": "5xx"}); isErr {
		t.Fatal(out)
	}
	// It finds a thread opened on someone else's words: that taints it.
	other, _ := c.openThread(threadIn{Title: "Facture", Goal: "Payée.", Subject: "topic:facture"}, []string{"mail"}, byBrain)
	if out, isErr := callTaskThreads(t, c, r.Task, "find_thread", map[string]any{"subject": "topic:facture"}); isErr || c.taskResultTaint(r.Task) == "" {
		t.Fatalf("found %v %d: the run is not tainted", out, other.ID)
	}
	if out, isErr := callTaskThreads(t, c, r.Task, "note_item", map[string]any{"source": "alertes", "item": "b", "note": "down"}); isErr {
		t.Fatal(out)
	}
	byItem := map[string]itemNote{}
	for _, n := range c.itemNotes(id) {
		byItem[n.Item] = n
	}
	if byItem["a"].Tainted != "" || byItem["b"].Tainted == "" {
		t.Fatalf("notes %+v", byItem)
	}
}

// A task's steps: what the user does, a deadline, a call that is always a
// card, a free read included; never a task nor a check. Its call is one
// of its tools. A raise goes to the attention queue as the skill's, a
// tainted one held to 3. A led thread it opens waits for its plan's card.
func TestSkillThreadSteps(t *testing.T) {
	c, _, now := alertsWatcher(t)
	c.emit(journalEvent{Kind: evMonitorNew, Key: "alertes", Source: "monitor:alertes", At: *now, Payload: map[string]any{"id": "a"}})
	c.tickSkills(context.Background(), *now)
	r := runsOfSkill(t, c, "veille-alertes")[0]
	id := runThreadOf(c, r)
	call := func(name string, args map[string]any) (map[string]any, bool) {
		return callTaskThreads(t, c, r.Task, name, args)
	}
	for _, bad := range []map[string]any{
		{"step": map[string]any{"key": "fix", "title": "Corriger", "kind": "task", "prompt": "Corrige."}},
		{"step": map[string]any{"key": "chk", "title": "Vérifier", "kind": "check", "tool": "mcp__monitor__status", "input": map[string]any{}}},
		{"step": map[string]any{"key": "page", "title": "Lire", "kind": "call", "tool": "mcp__other__page", "input": map[string]any{}}},
	} {
		if out, isErr := call("add_step", bad); !isErr {
			t.Errorf("%v accepted: %v", bad, out)
		}
	}
	if out, isErr := call("add_step", map[string]any{"step": map[string]any{"key": "status", "title": "Lire l'état", "kind": "call",
		"tool": "mcp__monitor__status", "input": map[string]any{"service": "web"}}}); isErr {
		t.Fatal(out)
	}
	st, _ := c.thread(id)
	if step := stepOf(t, c, st, "status"); step.Card == "" || step.Status != stepRunning {
		t.Fatalf("a free read a task added ran without its card: %+v", step)
	}
	if out, isErr := call("add_step", map[string]any{"step": map[string]any{"key": "ack", "title": "Acquitter", "kind": "user", "question": "Acquitte l'alerte."}}); isErr {
		t.Fatal(out)
	}
	if out, isErr := call("set_thread", map[string]any{"state": "waiting", "deadline": "2026-11-05T18:00"}); isErr {
		t.Fatal(out)
	}
	if out, isErr := call("set_thread", map[string]any{"deadline": "2026-11-06"}); isErr {
		t.Fatal(out)
	}
	st, _ = c.thread(id)
	if dl := stepOf(t, c, st, threadDeadlineStep); st.State != threadWaiting || dl.Kind != stepDeadline || dl.Due != "2026-11-06" {
		t.Fatalf("thread %+v, deadline %+v", st, dl)
	}
	for _, bad := range []map[string]any{{"state": "done"}, {"state": "dropped"}, {}, {"raise": map[string]any{"weight": 9, "why": "x"}}} {
		if out, isErr := call("set_thread", bad); !isErr {
			t.Errorf("%v accepted: %v", bad, out)
		}
	}
	if out, isErr := call("set_thread", map[string]any{"raise": map[string]any{"weight": 4, "why": "la prod tombe"}}); isErr {
		t.Fatal(out)
	}
	it := attentionOf(c, threadRef(id))
	if it == nil || it.Source != "skill:veille-alertes" || it.Weight != 4 {
		t.Fatalf("item %+v", it)
	}
	// Tainted: held to 3.
	c.taskRead(r.Task, "mcp__monitor__alerts")
	other, _ := c.openThread(threadIn{Title: "Certificats", Goal: "Renouvelés.", Subject: "topic:certs"}, nil, byBrain)
	if out, isErr := call("set_thread", map[string]any{"thread_id": other.ID, "raise": map[string]any{"weight": 5, "why": "expirent"}}); isErr {
		t.Fatal(out)
	}
	if it := attentionOf(c, threadRef(other.ID)); it == nil || it.Weight > weightProposal {
		t.Fatalf("a tainted raise %+v", it)
	}

	// A led thread waits for its card, opened by a tainted run.
	out, isErr := call("open_thread", map[string]any{"title": "Corriger le DNS", "goal": "Le DNS répond.", "subject": "project:dns",
		"steps": []any{map[string]any{"key": "fix", "title": "Corriger", "prompt": "Corrige."}}})
	if isErr {
		t.Fatal(out)
	}
	led, _ := c.findThreads("project:dns", "", true, 1)
	if len(led) != 1 || led[0].Phase != phaseProposed || led[0].Tainted == "" {
		t.Fatalf("led %+v", led)
	}
	if _, ok := runCard(c, cardThread); !ok {
		t.Fatal("its plan's card is not shown")
	}
	// Its own subject again: that thread, not a second; never a day.
	if out, _ := call("open_thread", map[string]any{"title": "Alertes", "goal": "x", "subject": "topic:alertes"}); !strings.Contains(out["note"].(string), "Not a new thread") {
		t.Fatalf("again %v", out)
	}
	if _, isErr := call("open_thread", map[string]any{"title": "Jour", "goal": "x", "subject": "topic:jour", "ends": "day"}); !isErr {
		t.Error("a day thread opened by a task")
	}
}

// A grant of some of its tools offers those only; a task not lent it
// does not reach it.
func TestTaskThreadsGrant(t *testing.T) {
	if got := taskThreadsTools([]string{threadsServer + ".note_item", "monitor"}); len(got) != 1 || got[0] != claudeToolName(threadsServer, "note_item") {
		t.Fatalf("tools %v", got)
	}
	if got := taskThreadsTools([]string{"monitor"}); len(got) != 0 {
		t.Fatalf("tools %v", got)
	}
	if !providedServer(threadsServer) || !slices.Contains(reserved, threadsServer) {
		t.Fatal("not provided")
	}
}

// A skill raising its thread (its receipt's thread:<id>) raises that
// thread's tile, whatever the weight, and puts nothing in a tile of the
// queue; the user opening that tile then leaving it ends the raise, and
// the tile goes back. From a tainted run, the raise is held to 3.
func TestSkillRaisesThreadTile(t *testing.T) {
	c, _, now := alertsWatcher(t)
	raise := func(key string, weight int, tainted bool) (int64, *skillRun) {
		c.emit(journalEvent{Kind: evMonitorNew, Key: "alertes", Source: "monitor:alertes", At: *now, Payload: map[string]any{"id": key}})
		c.tickSkills(context.Background(), *now)
		r := runsOfSkill(t, c, "veille-alertes")[0]
		id := runThreadOf(c, r)
		if tainted {
			c.taskRead(r.Task, "mail/read") // what someone else wrote
		}
		endTurn(t, c, r.Task, "La prod renvoie des 5xx.\n```json\n"+
			`{"receipt": {"status": "done", "attention": [{"refs": ["thread:`+strconv.FormatInt(id, 10)+`"], "weight": `+strconv.Itoa(weight)+`, "why": "des 5xx"}]}}`+"\n```")
		*now = now.Add(time.Hour)
		return id, r
	}
	id, _ := raise("a", 4, false)
	sid := threadSurfaceID(id)
	if it := attentionOf(c, threadRef(id)); it == nil || it.Weight != 4 || it.Source != "skill:veille-alertes" || it.tainted {
		t.Fatalf("item %+v", it)
	}
	c.syncQueueSurfaces()
	s, _ := c.surface(sid)
	if s.Weight < queueSurfaceWeight(4) {
		t.Fatalf("tile weight %d", s.Weight)
	}
	for _, e := range c.queuePlan(time.Now()) {
		if e.it.Ref == threadRef(id) && (!e.about || e.surface != sid) {
			t.Fatalf("placed %+v", e)
		}
	}
	all, _ := c.surfaces()
	for _, x := range all {
		if strings.HasPrefix(x.ID, queueSurfacePrefix) {
			t.Errorf("a tile of the queue: %s %q", x.ID, x.Title)
		}
	}

	// Opened, then left: seen.
	c.focusSurface(sid, time.Now())
	c.queueSweep(time.Now())
	if attentionOf(c, threadRef(id)) == nil {
		t.Fatal("ended while the user reads it")
	}
	c.focusSurface("", time.Now())
	c.queueSweep(time.Now())
	if it := attentionOf(c, threadRef(id)); it != nil {
		t.Fatalf("still raised %+v", it)
	}
	if s, _ = c.surface(sid); s.Weight >= queueSurfaceWeight(4) {
		t.Fatalf("tile weight %d after", s.Weight)
	}

	// A tainted run: held to 3.
	if again, r := raise("b", 5, true); again != id || c.taskResultTaint(r.Task) == "" {
		t.Fatalf("run %+v on thread %d", r, again)
	}
	if it := attentionOf(c, threadRef(id)); it == nil || it.Weight != 3 || !it.tainted {
		t.Fatalf("tainted item %+v", it)
	}
	c.syncQueueSurfaces()
	if s, _ = c.surface(sid); s.Weight >= queueSurfaceWeight(4) {
		t.Fatalf("tainted tile weight %d", s.Weight)
	}
}

// A skill writes no surface: eriphe-workspace is refused in its tools.
func TestSkillNoWorkspace(t *testing.T) {
	if _, err := parseSkillMD([]byte("---\nname: x\ndescription: y\nwhen: monitor.new\ntools: [eriphe-workspace]\n---\nz\n")); err == nil {
		t.Fatal("a skill lent the workspace")
	}
	if !strings.Contains(skillVoice("fr", true), "note_item") || strings.Contains(skillVoice("fr", false), "note_item") {
		t.Fatal("the voice")
	}
}
