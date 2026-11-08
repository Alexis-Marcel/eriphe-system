package main

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// An assistant's powers are its role's, or fewer, and hold together; an
// assistant other than the brain reports by default and may be given
// notify, never the brain's.
func TestAssistantPowers(t *testing.T) {
	c := brainCore(t, &BrainConfig{})
	if err := c.putAssistant("prod", Assistant{Role: assistantRoleAssistant, Triggers: []string{"schedule:every 15m"}}); err != nil {
		t.Fatal(err)
	}
	a := c.conf().Assistants["prod"]
	if !a.can(powerReports) || a.can(powerNotifies) || a.can(powerTalksToUser) || a.can(powerLaunchesTasks) {
		t.Fatalf("default powers = %v", a.powers())
	}
	if err := c.putAssistant("prod", Assistant{Role: assistantRoleAssistant, Powers: []string{powerReports, powerNotifies}}); err != nil {
		t.Fatalf("notify: %v", err)
	}
	if a := c.conf().Assistants["prod"]; !a.can(powerNotifies) {
		t.Fatalf("powers = %v", a.powers())
	}
	// Other powers are other tools: its task starts again.
	if c.assistantSpecHash(c.conf().Assistants["prod"]) == c.assistantSpecHash(Assistant{Name: "prod", Role: assistantRoleAssistant}) {
		t.Fatal("its powers changed, its fingerprint did not")
	}
	for _, p := range []string{powerTalksToUser, powerLaunchesTasks, powerReceivesConversation, "flies"} {
		if err := c.putAssistant("prod", Assistant{Role: assistantRoleAssistant, Powers: []string{p}}); err == nil {
			t.Errorf("power %s given to an assistant", p)
		}
	}
	// The brain may have fewer powers, but they still hold together.
	if err := c.putAssistant(brainAssistant, Assistant{Powers: []string{powerReceivesConversation}}); err == nil ||
		!strings.Contains(err.Error(), "needs") {
		t.Fatalf("the conversation without speaking: %v", err)
	}
	if !theBrain(c.core).can(powerLaunchesTasks) {
		t.Fatal("the brain lost its powers")
	}
}

// Its triggers are of kinds its role allows, with an argument its card
// accepts.
func TestAssistantTriggers(t *testing.T) {
	c := brainCore(t, &BrainConfig{})
	for tr, ok := range map[string]bool{
		"schedule:every 15m":            true,
		"schedule:toutes les 2 h":       true,
		"schedule:weekdays 08:00":       true,
		"schedule:every 2h 08:00-20:00": true,
		"schedule:every 1m":             false, // too often
		"schedule:every 2d":             false,
		"schedule":                      false, // when?
		"schedule:":                     false,
		triggerConversation:             false, // the brain's
		triggerClock:                    false,
		"tool:mail":                     false, // not yet
	} {
		err := c.putAssistant("prod", Assistant{Role: assistantRoleAssistant, Triggers: []string{tr}})
		if (err == nil) != ok {
			t.Errorf("%q: %v", tr, err)
		}
	}
	if err := c.putAssistant(brainAssistant, Assistant{Triggers: []string{triggerConversation, "schedule:every 15m"}}); err == nil {
		t.Error("a schedule for the brain")
	}
	if err := c.putAssistant(brainAssistant, Assistant{Triggers: []string{"conversation:x"}}); err == nil {
		t.Error("an argument to the conversation")
	}
}

func TestParseWake(t *testing.T) {
	loc := time.FixedZone("test", 3600)
	now := time.Date(2026, 11, 9, 7, 30, 0, 0, loc) // a Monday
	for in, want := range map[string]time.Time{
		"every 15m":       now.Add(15 * time.Minute),
		"Toutes les 3 h":  now.Add(3 * time.Hour),
		"every 30 min":    now.Add(30 * time.Minute),
		"weekdays 08:00":  time.Date(2026, 11, 9, 8, 0, 0, 0, loc),
		"samedi 09:30":    time.Date(2026, 11, 14, 9, 30, 0, 0, loc),
		"weekdays 07:00,": time.Date(2026, 11, 10, 7, 0, 0, 0, loc),
	} {
		w, err := parseWake(in)
		if err != nil {
			t.Errorf("%q: %v", in, err)
			continue
		}
		if got := w.next(now); !got.Equal(want) {
			t.Errorf("%q: next %s, want %s", in, got, want)
		}
	}
	for _, in := range []string{"", "every 4m", "every 25h", "sometimes"} {
		if _, err := parseWake(in); err == nil {
			t.Errorf("%q accepted", in)
		}
	}
}

// startAssistantTask starts an assistant's task with the controller and
// leaves it idle.
func startAssistantTask(t *testing.T, c *catalogCore, a Assistant) string {
	t.Helper()
	if err := c.putAssistant(a.Name, a); err != nil {
		t.Fatal(err)
	}
	c.tendAssistants(context.Background(), "tok")
	id := c.assistantStatus(a.Name).Task
	if id == "" {
		t.Fatal("not started")
	}
	c.store.updateTask(id, map[string]any{"state": stateIdle})
	return id
}

// An assistant runs as a task with its frame and mission, its own tools
// relayed by its node, and the free reads among its tools allowed.
func TestAssistantTask(t *testing.T) {
	c := brainCore(t, &BrainConfig{})
	id := startAssistantTask(t, c, Assistant{Name: "prod", Role: assistantRoleAssistant, Instructions: "Surveille la prod de {{user}}.",
		Triggers: []string{"schedule:every 15m"}})
	task, _ := c.store.task(id)
	if task.Kind != assistantRoleAssistant || task.Prompt != assistantStartPrompt {
		t.Fatalf("task = %+v", task)
	}
	spec, _ := c.store.taskSpec(id)
	if !strings.Contains(spec.AppendSystemPrompt, "Tu es prod, un assistant d'Eriphe") || !strings.Contains(spec.AppendSystemPrompt, "## Ta mission\n\nSurveille la prod de") ||
		!slices.Contains(spec.AllowedTools, "mcp__"+selfServer) || spec.Profile != "assistant-prod" {
		t.Fatalf("spec = %+v", spec)
	}
	if err := c.fetchAssistant(task, &spec); err != nil || !slices.Contains(spec.Servers, selfServer) || spec.SystemNetwork ||
		!slices.Equal(spec.HarnessTools, []string{"ToolSearch"}) {
		t.Fatalf("fetched = %+v, %v", spec, err)
	}
	// Placed like the brain: it lives on an always-on node.
	if !residentKind(assistantRoleAssistant) {
		t.Fatal("an assistant woken by a schedule is not resident")
	}
}

// Its own tools are its powers': report by default, notify when given.
func TestAssistantServer(t *testing.T) {
	c := brainCore(t, &BrainConfig{})
	names := func(a Assistant) []string {
		ctx := context.Background()
		ct, st := mcp.NewInMemoryTransports()
		if _, err := c.assistantServer(a, nil).Connect(ctx, st, nil); err != nil {
			t.Fatal(err)
		}
		sess, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil).Connect(ctx, ct, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer sess.Close()
		res, err := sess.ListTools(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, x := range res.Tools {
			out = append(out, x.Name)
		}
		slices.Sort(out)
		return out
	}
	if got := names(Assistant{Name: "prod", Role: assistantRoleAssistant}); !slices.Equal(got, []string{"report"}) {
		t.Fatalf("tools = %v", got)
	}
	if got := names(Assistant{Name: "prod", Role: assistantRoleAssistant, Powers: []string{powerReports, powerNotifies}}); !slices.Equal(got, []string{"notify", "report"}) {
		t.Fatalf("tools = %v", got)
	}
	// A role without these powers has none, and its node relays nothing.
	if hasOwnTools(Assistant{Role: roleTest}) {
		t.Fatal("own tools without powers")
	}
}

// A schedule wakes an idle assistant when it is due, once for the times
// it missed.
func TestDeliverSchedule(t *testing.T) {
	c := brainCore(t, &BrainConfig{})
	ctx := context.Background()
	id := startAssistantTask(t, c, Assistant{Name: "prod", Role: assistantRoleAssistant, Triggers: []string{"schedule:every 15m"}})
	a := c.conf().Assistants["prod"]
	inbox := func() []InboxItem {
		items, _ := c.store.inbox(id)
		return items
	}
	before := len(inbox())
	// Seen for the first time: set to its next time, nothing delivered.
	if err := c.tendAssistant(ctx, a, "tok"); err != nil {
		t.Fatal(err)
	}
	at := c.assistantStatus("prod").Wakes["schedule:every 15m"]
	if at < time.Now().Add(14*time.Minute).Unix() || len(inbox()) != before {
		t.Fatalf("wakes at %d, inbox %+v", at, inbox())
	}
	// Due (and long overdue): one wake, then its next time from now.
	c.updateAssistant("prod", func(st *AssistantStatus) { st.Wakes["schedule:every 15m"] = time.Now().Add(-time.Hour).Unix() })
	if err := c.tendAssistant(ctx, a, "tok"); err != nil {
		t.Fatal(err)
	}
	items := inbox()
	if len(items) != before+1 || !strings.Contains(fmt.Sprint(items[len(items)-1].Data), "[réveil : horaire « every 15m »]") {
		t.Fatalf("inbox = %+v", items)
	}
	if at := c.assistantStatus("prod").Wakes["schedule:every 15m"]; at < time.Now().Add(14*time.Minute).Unix() {
		t.Fatalf("next wake %d", at)
	}
	// A removed schedule is forgotten.
	c.putAssistant("prod", Assistant{Role: assistantRoleAssistant, Triggers: []string{"schedule:weekdays 08:00"}})
	c.store.updateTask(id, map[string]any{"state": stateIdle})
	c.tendAssistant(ctx, c.conf().Assistants["prod"], "tok")
	if w := c.assistantStatus("prod").Wakes; len(w) != 1 || w["schedule:weekdays 08:00"] == 0 {
		t.Fatalf("wakes = %v", w)
	}
}

// A report is an event for the brain, tainted once the assistant's
// session read external content; the taint sticks for the session.
func TestAssistantReport(t *testing.T) {
	c := brainCore(t, &BrainConfig{})
	id := startAssistantTask(t, c, Assistant{Name: "prod", Role: assistantRoleAssistant, DisplayName: "Vigie", Triggers: []string{"schedule:every 15m"}})
	a := c.conf().Assistants["prod"]
	report := func(text string) Message {
		t.Helper()
		if _, err := c.assistantReport(a, reportIn{Text: text}); err != nil {
			t.Fatal(err)
		}
		msgs, _ := c.store.undelivered()
		if len(msgs) == 0 {
			t.Fatal("no event for the brain")
		}
		m := msgs[len(msgs)-1]
		c.store.markDelivered(m.Seq)
		return m
	}
	m := report("La prod répond en 2 s depuis 10 min.")
	if m.Role != roleEvent || m.Text != "Rapport de l'assistant Vigie : La prod répond en 2 s depuis 10 min." || c.store.get(taintKey(m.Seq)) != "" {
		t.Fatalf("report = %+v, taint %q", m, c.store.get(taintKey(m.Seq)))
	}
	if _, err := c.assistantReport(a, reportIn{Text: "  "}); err == nil {
		t.Fatal("an empty report")
	}
	// It reads a mail: tainted, and still after a later turn reads nothing.
	c.store.addEvents(id, []EventRow{{Seq: 1, Time: time.Now(), Type: "tool_use", Data: map[string]any{"name": "mcp__mail__read_message"}},
		{Seq: 2, Time: time.Now(), Type: "tool_use", Data: map[string]any{"name": "mcp__eriphe-core__report"}}})
	m = report("Un mail dit que la prod est tombée.")
	if got := c.store.get(taintKey(m.Seq)); got != "assistant:prod" {
		t.Fatalf("taint = %q", got)
	}
	if st := c.assistantStatus("prod"); !slices.Equal(st.Tainted, []string{"mcp__mail__read_message"}) || st.TaintSeq != 2 {
		t.Fatalf("status = %+v", st)
	}
	if got := c.store.get(taintKey(report("Rien de neuf.").Seq)); got == "" {
		t.Fatal("the taint cleared within the session")
	}
	// A fresh session starts clean; a resumed one keeps it.
	assistantRoles[assistantRoleAssistant].started(c.core, a, nil, true)
	if len(c.assistantStatus("prod").Tainted) == 0 {
		t.Fatal("a resumed session forgot what it read")
	}
	assistantRoles[assistantRoleAssistant].started(c.core, a, nil, false)
	if st := c.assistantStatus("prod"); len(st.Tainted) != 0 || st.TaintSeq != 0 {
		t.Fatalf("a new session = %+v", st)
	}
}

// An assistant's notify goes through the brain's rules, says who wrote it,
// and stays out of the conversation.
func TestAssistantNotify(t *testing.T) {
	c := brainCore(t, &BrainConfig{})
	startAssistantTask(t, c, Assistant{Name: "prod", Role: assistantRoleAssistant, DisplayName: "Vigie", Powers: []string{powerReports, powerNotifies},
		Triggers: []string{"schedule:every 15m"}})
	a := c.conf().Assistants["prod"]
	last := c.store.lastMessageSeq()
	out, err := c.assistantNotify(a, notifyIn{Text: "La prod est tombée.", Level: levelBrief, Why: "Grafana le dit."})
	if err != nil {
		t.Fatal(err)
	}
	var source, why string
	c.store.db.QueryRow(`SELECT source, why FROM notifications WHERE id = ?`, out["id"]).Scan(&source, &why)
	if source != notifyFromAssistant || why != "Vigie : Grafana le dit." || c.store.lastMessageSeq() != last {
		t.Fatalf("source %q, why %q, messages %d > %d", source, why, c.store.lastMessageSeq(), last)
	}
}

// The page shows what an assistant may do and what wakes it.
func TestWebAssistantPowers(t *testing.T) {
	tw, c, _ := catalogWeb(t, func(cfg *Config) { cfg.Brain = &BrainConfig{} })
	if err := c.putAssistant("prod", Assistant{Role: assistantRoleAssistant, Disabled: true, Powers: []string{powerReports, powerNotifies},
		Triggers: []string{"schedule:every 15m"}, State: testState}); err != nil {
		t.Fatal(err)
	}
	c.setState(c.conf().Assistants["prod"], setStateIn{Values: map[string]any{"prod": "ok", "note": "Lent.",
		"mails": []any{map[string]any{"id": "m1"}}}}, []string{"mcp__mail__read_message"}, time.Now())
	code, body, _ := tw.do("GET", "/settings/agents", nil, nil)
	for _, want := range []string{`id="assistant-prod"`, "rend compte à Eriphe", "te prévient", "horaire : every 15m",
		"Ce qu&#39;il sait", `<span class="strong">ok</span>`, "lignes : 1", "lu dehors", `<span class="mono">incidents</span> <span class="muted">inconnu</span>`} {
		if code != 200 || !strings.Contains(body, want) {
			t.Errorf("page %d without %q", code, want)
		}
	}
}
