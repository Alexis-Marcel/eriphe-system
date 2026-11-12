package main

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The plan's yes gives the session its lead, a temporary assistant woken
// by its steps; the brain no longer judges them, it tells the lead; the
// lead goes when the session ends.
func TestSessionLead(t *testing.T) {
	c, s := sessionCore(t)
	ctx := context.Background()
	c.decideCard(ctx, sessionCard(t, c, askPlan).ID, true, "web")
	s, _ = c.session(s.ID)
	a, ok := c.conf().Assistants[s.Lead]
	if !ok || a.Role != assistantRoleLead || leadSession(a) != s.ID || a.can(powerLaunchesTasks) || a.can(powerTalksToUser) {
		t.Fatalf("lead %q: %+v", s.Lead, a)
	}
	if !strings.Contains(leadInstructions(c, a, ""), "Find a name for the shop.") {
		t.Error("the lead does not know its goal")
	}
	if got := leadToolNames(t, c, a); !slices.Equal(got, []string{"amend_session", "catalog_read", "end_session", "report", "session", "settle_step", "surface_remove", "surface_write", "workspace_read"}) {
		t.Errorf("lead tools %v", got)
	}

	// A step's report is the lead's, not the brain's.
	before := lastEvent(t, c)
	reports(t, c, stepOf(t, c, s, "names"), receiptOf("Lumen, Rivet.", "names"))
	if lastEvent(t, c) != before {
		t.Errorf("the brain heard a step's report: %q", lastEvent(t, c))
	}
	if out, isErr := callBrainTool(t, c, "settle_step", map[string]any{"session_id": s.ID, "step": "names", "verdict": "accept"}); !isErr {
		t.Errorf("the brain judged a step of a session with a lead: %v", out)
	}
	if out, isErr := callBrainTool(t, c, "tell_lead", map[string]any{"session_id": s.ID, "text": "Sam prefers short names."}); isErr {
		t.Fatalf("tell_lead: %v", out)
	}

	// It reads all of it at its next wake, in one turn.
	c.store.insertTask(&TaskRow{ID: "t-lead", Node: "nas", Agent: "sonnet", Runtime: "claude-code", Prompt: "x", State: stateIdle, Kind: assistantRoleLead})
	started, err := c.deliverLead(ctx, a, &TaskRow{ID: "t-lead"})
	if err != nil || !started {
		t.Fatalf("deliver: %v %v", started, err)
	}
	items, _ := c.store.inbox("t-lead")
	if len(items) != 1 {
		t.Fatalf("inbox %+v", items)
	}
	text := items[0].Data["text"].(string)
	if !strings.Contains(text, "settle_step") || !strings.Contains(text, "De la part d'Eriphe : Sam prefers short names.") {
		t.Errorf("the lead read %q", text)
	}
	if again, _ := c.deliverLead(ctx, a, &TaskRow{ID: "t-lead"}); again {
		t.Error("delivered twice")
	}

	if err := c.stopSession(ctx, s.ID, "brain"); err != nil {
		t.Fatal(err)
	}
	if _, ok := c.conf().Assistants[s.Lead]; ok {
		t.Error("the lead outlived its session")
	}
}

func leadToolNames(t *testing.T, c *core, a Assistant) []string {
	t.Helper()
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

// A session with a done_when closes only once a fresh agent, lent
// read-only what the steps wrote, says the objective holds; twice not,
// and the lead must report.
func TestFinalCheck(t *testing.T) {
	c := startCore(t)
	ctx := context.Background()
	repo := ResourceRef{Kind: resRepo, Ref: "owner/site", Access: accessWrite, Branch: "eriphe/form"}
	s, err := c.openSession(sessionIn{Title: "Form", Goal: "Fix the contact form.", DoneWhen: "the form sends a mail",
		Steps: []stepSpec{{Key: "fix", Title: "Fix", Prompt: "Fix the form.", Resources: []ResourceRef{repo}}}}, nil, "brain")
	if err != nil {
		t.Fatal(err)
	}
	c.decideCard(ctx, sessionCard(t, c, askPlan).ID, true, "web")
	reports(t, c, stepOf(t, c, s, "fix"), receiptOf("Fixed.", "fixed"))
	if _, err := c.settleStep(ctx, s.ID, "fix", verdictAccept, "", "", "lead"); err != nil {
		t.Fatal(err)
	}
	for i := range finalChecks {
		note, err := c.finishSession(ctx, s.ID, "The form is fixed on eriphe/form.", "lead")
		if err != nil || !strings.Contains(note, "fresh agent") {
			t.Fatalf("finish %d: %q %v", i+1, note, err)
		}
		final := stepOf(t, c, s, finalStep)
		task, _ := c.store.task(final.Task)
		if final.Status != stepRunning || task == nil || !strings.Contains(task.Prompt, "the form sends a mail") {
			t.Fatalf("final check %d: %+v", i+1, final)
		}
		if len(task.Resources) != 1 || task.Resources[0].Access != accessRead || task.Resources[0].Branch != "eriphe/form" {
			t.Errorf("the judge is lent %+v, want the branch read-only", task.Resources)
		}
		reports(t, c, final, "No mail is sent.\n\n```json\n"+`{"receipt": {"status": "partial", "summary": "no mail", "not_verified": ["mail"]}}`+"\n```")
		if s, _ = c.session(s.ID); s.State != sessRunning {
			t.Fatalf("closed against the check: %s", s.State)
		}
		if msg := told(t, c, s); !strings.Contains(msg, "pas atteint") {
			t.Errorf("the lead heard %q", msg)
		}
	}
	if _, err := c.finishSession(ctx, s.ID, "again", "lead"); err == nil || !strings.Contains(err.Error(), "report") {
		t.Errorf("a third check: %v", err)
	}
}

// A lead's report may carry an attention block: its good refs land in the
// attention queue under the lead's name, the bad ones are dropped and said.
func TestLeadReportAttention(t *testing.T) {
	c, s := sessionCore(t)
	ctx := context.Background()
	c.decideCard(ctx, sessionCard(t, c, askPlan).ID, true, "web")
	s, _ = c.session(s.ID)
	a := c.conf().Assistants[s.Lead]
	ct, st := mcp.NewInMemoryTransports()
	if _, err := c.assistantServer(a, nil).Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	sess, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	ref := fmt.Sprintf("session:%d", s.ID)
	res, err := sess.CallTool(ctx, &mcp.CallToolParams{Name: "report", Arguments: map[string]any{
		"text": "Le plan a besoin d'un choix de Sam sur le nom.",
		"attention": []map[string]any{
			{"refs": []string{ref, "shop:1"}, "weight": 4, "why": "un choix de nom attend Sam", "say": "Ta session attend ton choix."},
			{"refs": []string{"task:t-1"}, "weight": 9, "why": "too heavy"},
		},
	}})
	if err != nil || res.IsError {
		t.Fatalf("report: %v %+v", err, res)
	}
	it := attentionOf(c, ref)
	if it == nil || it.Weight != 4 || it.Source != "assistant:"+s.Lead || it.Say != "Ta session attend ton choix." {
		t.Fatalf("item %+v in %+v", it, c.attentionOpen())
	}
	if attentionOf(c, "shop:1") != nil || attentionOf(c, "task:t-1") != nil {
		t.Errorf("a bad ref or weight was kept: %+v", c.attentionOpen())
	}
	if raw, _ := json.Marshal(res.StructuredContent); !strings.Contains(string(raw), "dropped") {
		t.Errorf("the lead is not told what was dropped: %s", raw)
	}
	if !strings.Contains(lastEvent(t, c), "choix de Sam") {
		t.Errorf("the brain did not get the report: %q", lastEvent(t, c))
	}
}
