package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

func snapshotOf(items ...attentionItem) compositionSnapshot {
	s := compositionSnapshot{Moment: momentChange, Now: time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)}
	for i, it := range items {
		if it.Time.IsZero() {
			it.Time = s.Now.Add(-time.Duration(i+1) * time.Minute)
		}
		kind, _, _ := strings.Cut(it.Ref, ":")
		s.Items = append(s.Items, snapshotItem{attentionItem: it, Kind: kind})
	}
	return s
}

func refsOf(items []compositionItem) string {
	var out []string
	for _, it := range items {
		out = append(out, fmt.Sprintf("%s/%d", it.Ref, it.Weight))
	}
	return strings.Join(out, ",")
}

// The fallback composes by weight, then by time: the heaviest as modules,
// the rest as chips.
func TestFallbackComposition(t *testing.T) {
	var items []attentionItem
	for i := range compositionItemsMax + compositionChipsMax + 2 {
		items = append(items, attentionItem{Ref: "mail:" + strconv.Itoa(i), Weight: 1 + i%3, Why: strings.Repeat("y", 90)})
	}
	items = append(items, attentionItem{Ref: "card:5", Weight: 5, Why: "Approve the payment"})
	comp := fallbackComposition(snapshotOf(items...))
	if comp.By != fallbackBy || len(comp.Items) != compositionItemsMax || len(comp.Chips) != compositionChipsMax {
		t.Fatalf("composition %+v", comp)
	}
	if comp.Items[0].Ref != "card:5" || comp.Items[1].Weight != 3 || comp.Items[len(comp.Items)-1].Weight != 3 || comp.Ambient != "urgent" || comp.Mode != modeSide {
		t.Errorf("order %s, ambient %s", refsOf(comp.Items), comp.Ambient)
	}
	// Of equal weight, the latest first.
	if comp.Items[1].Ref != "mail:2" || runeLen(comp.Items[1].Why) > compositionWhyMax+1 {
		t.Errorf("second %+v", comp.Items[1])
	}
	if empty := fallbackComposition(snapshotOf()); empty.Mode != modeCenter || len(empty.Items) != 0 || empty.Ambient != "calm" {
		t.Errorf("empty %+v", empty)
	}
}

// A burst of changes is composed once it settles, or at most maxWait
// after it began.
func TestDebouncer(t *testing.T) {
	d := debouncer{wait: 3 * time.Second, maxWait: 10 * time.Second}
	t0 := time.Unix(1_000_000, 0)
	if d.due(t0) {
		t.Fatal("due with no change")
	}
	d.changed(t0)
	d.changed(t0.Add(2 * time.Second))
	if d.due(t0.Add(4 * time.Second)) {
		t.Fatal("due 2 s after the last change")
	}
	if !d.due(t0.Add(5*time.Second)) || d.due(t0.Add(6*time.Second)) {
		t.Fatal("not due once settled, or due twice")
	}
	// A queue that never settles is composed all the same.
	start := t0.Add(time.Minute)
	for i := range 11 {
		now := start.Add(time.Duration(i) * time.Second)
		d.changed(now)
		if d.due(now) != (i == 10) {
			t.Fatalf("at %d s: due %v", i, !(i == 10))
		}
	}
}

// composerCore is a core with the composer on, its task started and idle.
func composerCore(t *testing.T) (*catalogCore, string) {
	t.Helper()
	c := brainCore(t, &BrainConfig{})
	if err := c.setComposer(true, ""); err != nil {
		t.Fatal(err)
	}
	c.tendAssistants(context.Background(), "tok")
	id := c.assistantStatus(composerAssistant).Task
	if id == "" {
		t.Fatal("the composer did not start")
	}
	c.store.updateTask(id, map[string]any{"state": stateIdle})
	old := snapshotTitles
	snapshotTitles = 200 * time.Millisecond
	t.Cleanup(func() { snapshotTitles = old })
	return c, id
}

// answerComposer waits for the composer's turn, as its node would get it,
// then writes its answer and ends the turn.
func answerComposer(t *testing.T, c *catalogCore, id, answer string) string {
	t.Helper()
	ctx := context.Background()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		c.tendAssistants(ctx, "tok")
		items, _ := c.store.inbox(id)
		if len(items) > 0 {
			turn, _ := items[0].Data["text"].(string)
			c.store.ackInbox(id, items[len(items)-1].Seq)
			task, _ := c.store.task(id)
			seq := task.LastSeq
			c.store.addEvents(id, []EventRow{
				{Seq: seq + 1, Time: time.Now(), Type: "text", Data: map[string]any{"text": answer}},
				{Seq: seq + 2, Time: time.Now(), Type: "turn_end", Data: map[string]any{"subtype": "success"}},
			})
			c.store.updateTask(id, map[string]any{"state": stateIdle})
			c.tendAssistants(ctx, "tok")
			return turn
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the composer was never given its turn")
	return ""
}

// The composer is a persistent assistant of its own role: no tool, no
// power, low effort, a fresh session each task. Each composition is one
// of its turns; what it answers is checked, and kept.
func TestComposerRole(t *testing.T) {
	c, id := composerCore(t)
	a := c.conf().Assistants[composerAssistant]
	if a.Role != assistantRoleComposer || a.Agent != "sonnet" || a.Disabled || len(a.powers()) != 0 {
		t.Fatalf("composer %+v", a)
	}
	task, _ := c.store.task(id)
	spec, _ := c.store.taskSpec(id)
	if task.Kind != assistantRoleComposer || task.Prompt != composerStartPrompt || !strings.Contains(spec.AppendSystemPrompt, "Eriphe — compositeur") ||
		strings.Contains(spec.AppendSystemPrompt, "{{") || len(spec.AllowedTools) != 0 {
		t.Fatalf("task %+v, spec %+v", task, spec)
	}
	if err := c.fetchAssistant(task, &spec); err != nil || len(spec.Servers) != 0 || len(spec.MCPServers) != 0 ||
		len(spec.HarnessTools) != 1 || spec.HarnessTools[0] != "" || spec.AgentEnv["CLAUDE_CODE_EFFORT_LEVEL"] != "low" {
		t.Fatalf("fetched %+v, %v", spec, err)
	}

	c.attend(attentionItem{Ref: "mail:7", Weight: 2, Why: "A reply from example.org", Source: "skill:mail"})
	c.attend(attentionItem{Ref: "session:3", Weight: 4, Why: "Its plan waits for Sam", Source: "lead"})
	done := make(chan workspaceChoice, 1)
	go func() { done <- c.recompose(momentChange, "") }()
	turn := answerComposer(t, c, id, `{"eriphe":"Le plan attend ton feu vert.","items":[{"id":"q-session-3","size":"l","why":"Plan à valider"},{"id":"s-404","size":"xl","why":"invented"}],"missing":["l'agenda de demain"],"stale":["q-mail-7"]}`)
	if !strings.Contains(turn, "Ids permis : q-session-3, q-mail-7") || !strings.Contains(turn, "raison « Its plan waits for Sam »") {
		t.Errorf("turn %q", turn)
	}
	ch := <-done
	if ch.By != composedBy || len(ch.Items) != 1 || ch.Items[0] != (choiceItem{ID: "q-session-3", Size: sizeL, Why: "Plan à valider"}) || ch.Eriphe != "Le plan attend ton feu vert." {
		t.Fatalf("choice %+v", ch)
	}
	// Laid out at once: what it chose in front, what it left folded.
	if l := c.currentLayout(); len(l.Items) != 1 || l.Items[0].ID != "q-session-3" || l.Items[0].Size != sizeL ||
		len(l.Chips) != 1 || l.Chips[0].ID != "q-mail-7" || l.Eriphe != ch.Eriphe || l.By != composedBy {
		t.Fatalf("layout %+v", l)
	}
	// Its signals are in the journal.
	if n := countEvents(t, c.core, evSurfaceMissing); n != 1 {
		t.Errorf("%d surface.missing", n)
	}
	if n := countEvents(t, c.core, evSurfaceStale); n != 1 {
		t.Errorf("%d surface.stale", n)
	}
	// Kept in the store: a restart lays it out at once.
	c.layout = layoutState{}
	if last := c.lastChoice(); last == nil || last.By != composedBy || last.Eriphe != ch.Eriphe {
		t.Fatalf("after a restart %+v", last)
	}

	// A remark is a turn too, with the user's words.
	go func() { done <- c.recompose(momentRemark, "Moins de mails, s'il te plaît") }()
	turn = answerComposer(t, c, id, "pas du JSON")
	if !strings.Contains(turn, "Moins de mails") {
		t.Errorf("remark turn %q", turn)
	}
	// Invalid: chosen by weight, laid out by weight.
	if ch := <-done; ch.By != fallbackBy || ch.Moment != momentRemark {
		t.Fatalf("fallback %+v", ch)
	}
	if l := c.currentLayout(); len(l.Items) != 2 || l.Items[0].ID != "q-session-3" || l.Items[1].ID != "q-mail-7" || l.By != fallbackBy {
		t.Fatalf("layout by weight %+v", l)
	}

	// Its next task starts a fresh session, never resuming the last.
	c.updateAssistant(composerAssistant, func(st *AssistantStatus) { st.Session, st.SessionRuntime = "s-1", "claude-code" })
	c.store.updateTask(id, map[string]any{"state": stateStopped})
	noWait(c.core, composerAssistant)
	c.tendAssistants(context.Background(), "tok")
	next := c.assistantStatus(composerAssistant).Task
	if spec, _ := c.store.taskSpec(next); next == id || spec.Resume != "" {
		t.Fatalf("next task %s resumes %q", next, spec.Resume)
	}

	// Off: its task stops and the fallback composes, nothing is asked.
	if err := c.setComposer(false, ""); err != nil {
		t.Fatal(err)
	}
	if c.composerReady() {
		t.Fatal("ready while off")
	}
	if ch := c.recompose(momentChange, ""); ch.By != fallbackBy {
		t.Fatalf("off: %+v", ch)
	}
	if items, _ := c.store.inbox(next); len(items) != 0 {
		t.Fatalf("asked while off: %+v", items)
	}
}

// A composer slower than its timeout: the fallback composes, and its late
// answer is dropped. Nothing else waits on it.
func TestComposerTimeout(t *testing.T) {
	c, id := composerCore(t)
	old := composerTimeout
	composerTimeout = 300 * time.Millisecond
	t.Cleanup(func() { composerTimeout = old })
	c.attend(attentionItem{Ref: "mail:1", Weight: 3, Why: "A proposal"})
	done := make(chan workspaceChoice, 1)
	start := time.Now()
	go func() { done <- c.recompose(momentChange, "") }()
	// Its turn is delivered, but it does not answer in time.
	ctx := context.Background()
	for deadline := time.Now().Add(3 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		c.tendAssistants(ctx, "tok")
		if items, _ := c.store.inbox(id); len(items) > 0 {
			c.store.ackInbox(id, items[len(items)-1].Seq)
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("never delivered")
		}
	}
	ch := <-done
	if l := c.currentLayout(); ch.By != fallbackBy || len(l.Items) != 1 || l.Items[0].ID != "q-mail-1" || time.Since(start) > 3*time.Second {
		t.Fatalf("choice %+v, layout %+v after %s", ch, l, time.Since(start))
	}
	// Its answer comes later: no one waits for it anymore.
	task, _ := c.store.task(id)
	c.store.addEvents(id, []EventRow{
		{Seq: task.LastSeq + 1, Time: time.Now(), Type: "text", Data: map[string]any{"text": `{"eriphe":"Trop tard.","items":[{"id":"q-mail-1","size":"xl","why":"x"}]}`}},
		{Seq: task.LastSeq + 2, Time: time.Now(), Type: "turn_end", Data: map[string]any{"subtype": "success"}},
	})
	c.store.updateTask(id, map[string]any{"state": stateIdle})
	c.tendAssistants(ctx, "tok")
	if last := c.lastChoice(); last.By != fallbackBy {
		t.Fatalf("a late answer was kept: %+v", last)
	}
	c.composer.mu.Lock()
	defer c.composer.mu.Unlock()
	if c.composer.pending != nil || c.composer.inflight != nil {
		t.Fatal("a request left behind")
	}
}

// Without the composer, a composition is made by weight at once.
func TestComposerOff(t *testing.T) {
	c := brainCore(t, &BrainConfig{})
	c.attend(attentionItem{Ref: "mail:1", Weight: 2, Why: "A note"})
	if c.composerReady() {
		t.Fatal("ready without a composer")
	}
	if comp := c.currentComposition(); comp.By != fallbackBy || refsOf(comp.Items) != "mail:1/2" || c.lastComposition() != nil {
		t.Fatalf("composition %+v", comp)
	}
	if ch := c.recompose(momentChange, ""); ch.By != fallbackBy || c.lastComposition() == nil || c.lastChoice() == nil {
		t.Fatalf("choice %+v", ch)
	}
	if l := c.currentLayout(); len(l.Items) != 1 || l.Items[0].ID != "q-mail-1" {
		t.Fatalf("layout %+v", l)
	}
	if n := len(tasksOfKind(t, c.core, assistantRoleComposer)); n != 0 {
		t.Fatalf("%d composer tasks", n)
	}
}

// The workspace reads the composition behind its session, and the event
// stream says when it changes; Settings turns the composer on.
func TestWebComposition(t *testing.T) {
	tw := newTestWeb(t)
	tw.setup()
	anon := &testWeb{t: t, srv: tw.srv, hc: &http.Client{Transport: tw.hc.Transport, CheckRedirect: tw.hc.CheckRedirect}}
	if code, _, _ := anon.do("GET", "/api/composition", nil, nil); code != http.StatusUnauthorized {
		t.Fatalf("composition without a session: %d", code)
	}
	tw.c.attend(attentionItem{Ref: "mail:7", Weight: 2, Why: "a reply from example.org", Say: "Sam's friend wrote back"})
	code, body, h := tw.do("GET", "/api/composition", nil, nil)
	var comp map[string]any
	if code != 200 || !strings.HasPrefix(h.Get("Content-Type"), "application/json") || json.Unmarshal([]byte(body), &comp) != nil {
		t.Fatalf("composition: %d %s", code, body)
	}
	for _, k := range []string{"mode", "eriphe", "items", "chips", "at", "by"} {
		if _, ok := comp[k]; !ok {
			t.Errorf("no %q in %s", k, body)
		}
	}
	if comp["by"] != fallbackBy || !strings.Contains(body, `"ref":"mail:7","weight":2`) || !strings.Contains(body, `"say":"Sam's friend wrote back"`) {
		t.Fatalf("composition %s", body)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", tw.srv.URL+"/events", nil)
	resp, err := tw.hc.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	go func() {
		time.Sleep(300 * time.Millisecond)
		tw.c.keepComposition(composition{Mode: modeSide, Eriphe: "Un mail de ton ami.", Items: []compositionItem{{Ref: "mail:7", Weight: 3, Why: "Il répond"}},
			Chips: []string{}, At: time.Now(), By: composedBy})
	}()
	sc := bufio.NewScanner(resp.Body)
	event := ""
	for sc.Scan() {
		line := sc.Text()
		if v, ok := strings.CutPrefix(line, "event: "); ok {
			event = v
		}
		if data, ok := strings.CutPrefix(line, "data: "); ok && event == "composition" {
			var got composition
			if json.Unmarshal([]byte(data), &got) != nil {
				t.Fatalf("composition event %q", data)
			}
			if got.Eriphe == "Un mail de ton ami." && got.By == composedBy {
				return
			}
		}
	}
	t.Fatalf("no composition event: %v", sc.Err())
}

func TestWebComposerSettings(t *testing.T) {
	tw, c, csrf := catalogWeb(t, func(cfg *Config) { cfg.Brain = &BrainConfig{} })
	code, body, _ := tw.do("GET", "/settings/agents", nil, nil)
	if code != 200 || !strings.Contains(body, `id="composer"`) || !strings.Contains(body, "Compositeur de l") {
		t.Fatalf("page: %d", code)
	}
	if _, _, h := tw.post("/settings/composer", url.Values{"on": {"1"}, "agent": {"sonnet"}}, csrf); h.Get("HX-Redirect") != "/settings/agents?composer=1" {
		t.Fatalf("save: %v", h)
	}
	if a, ok := c.conf().Assistants[composerAssistant]; !ok || a.Disabled || a.Agent != "sonnet" || a.Role != assistantRoleComposer {
		t.Fatalf("composer %+v", a)
	}
	// Its own panel, not one of the brain's helpers.
	_, body, _ = tw.do("GET", "/settings/agents?composer=1", nil, nil)
	if strings.Contains(body, `id="assistant-composer"`) || !strings.Contains(body, "Compositeur enregistré.") {
		t.Fatal("the composer among the helpers, or no flash")
	}
	if _, _, h := tw.post("/settings/composer", url.Values{"agent": {"sonnet"}}, csrf); h.Get("HX-Redirect") == "" {
		t.Fatal("not turned off")
	}
	if a := c.conf().Assistants[composerAssistant]; !a.Disabled || a.Agent != "sonnet" {
		t.Fatalf("off: %+v", a)
	}
	if _, _, h := tw.post("/settings/composer", url.Values{"on": {"1"}, "agent": {"nope"}}, csrf); h.Get("HX-Redirect") != "" {
		t.Fatal("an unknown agent saved")
	}
}

// countEvents is how many events of a kind the journal holds.
func countEvents(t *testing.T, c *core, kind string) int {
	t.Helper()
	var n int
	if err := c.store.db.QueryRow(`SELECT COUNT(*) FROM event_journal WHERE kind = ?`, kind).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
