package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestContractNormalize(t *testing.T) {
	k := &taskContract{Objective: " The form sends mail ", Checks: []taskCheck{
		{Name: "tests", Run: "go test ./...", Dir: "core"},
		{Name: "report", Exists: "/work/folders/Invoices/Eriphe/report.pdf"},
	}}
	if err := k.normalize(); err != nil {
		t.Fatal(err)
	}
	if k.Objective != "The form sends mail" || k.Verify != "auto" || k.Checks[0].Timeout != checkTimeout {
		t.Errorf("normalized %+v", k)
	}
	for name, bad := range map[string]taskContract{
		"no objective":   {},
		"verify":         {Objective: "x", Verify: "sometimes"},
		"both":           {Objective: "x", Checks: []taskCheck{{Name: "a", Run: "true", Exists: "/work/a"}}},
		"neither":        {Objective: "x", Checks: []taskCheck{{Name: "a"}}},
		"no name":        {Objective: "x", Checks: []taskCheck{{Run: "true"}}},
		"twice":          {Objective: "x", Checks: []taskCheck{{Name: "a", Run: "true"}, {Name: "a", Run: "false"}}},
		"outside":        {Objective: "x", Checks: []taskCheck{{Name: "a", Exists: "/etc/passwd"}}},
		"climbs out":     {Objective: "x", Checks: []taskCheck{{Name: "a", Run: "ls", Dir: "../.."}}},
		"long timeout":   {Objective: "x", Checks: []taskCheck{{Name: "a", Run: "true", Timeout: 7200}}},
		"negative":       {Objective: "x", Budget: &taskBudget{Minutes: -1}},
		"too many tries": {Objective: "x", Budget: &taskBudget{Attempts: 9}},
	} {
		if err := bad.normalize(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// A worker's contract is kept, given to its agent after its standing
// instruction, and returned by task_status; no other kind of task takes one.
func TestRunTaskContract(t *testing.T) {
	c := newCatalogCore(t)
	c.store.registerNode("pc", hashSecret("node-secret"))
	c.store.setNodeStatus("pc", true, []byte(`{"runtimes":["claude-code","opencode"]}`))
	k := &taskContract{Objective: "The contact form sends mail", Constraints: []string{"keep the public API"},
		Checks:   []taskCheck{{Name: "tests", Run: "go test ./...", Dir: "core"}, {Name: "report", Exists: "out/report.pdf"}},
		Evidence: []string{"the PR link"}, Budget: &taskBudget{Minutes: 60, Turns: 30}}
	task, err := c.runTask(context.Background(), runRequest{Prompt: "fix the form", Contract: k})
	if err != nil {
		t.Fatal(err)
	}
	var raw string
	c.store.db.QueryRow(`SELECT spec FROM tasks WHERE id = ?`, task.ID).Scan(&raw)
	var spec nodeTaskSpec
	json.Unmarshal([]byte(raw), &spec)
	if !strings.HasPrefix(spec.AppendSystemPrompt, workerVoice(defaultDisplayName)+"\n\n## Task contract\nObjective: The contact form sends mail") {
		t.Errorf("system prompt %q", spec.AppendSystemPrompt)
	}
	for _, want := range []string{"- keep the public API", "- tests: `go test ./...` in core exits 0 within 300s", "- report: out/report.pdf exists",
		"- the PR link", "Budget: 60 minutes, 30 turns."} {
		if !strings.Contains(spec.AppendSystemPrompt, want) {
			t.Errorf("system prompt lacks %q", want)
		}
	}
	st, err := c.status(task.ID, 0)
	if err != nil || st.Contract == nil || st.Contract.Objective != "The contact form sends mail" || st.Contract.Verify != "auto" {
		t.Errorf("status contract %+v %v", st.Contract, err)
	}
	if _, err := c.runTask(context.Background(), runRequest{Prompt: "x", Contract: &taskContract{}}); err == nil {
		t.Error("a contract without objective was accepted")
	}
	if _, err := c.startTask(context.Background(), "memory", runRequest{Prompt: "x", Contract: k}, nodeTaskSpec{}); err == nil {
		t.Error("a non-worker took a contract")
	}
	// Without a contract, nothing changes.
	plain, _ := c.runTask(context.Background(), runRequest{Prompt: "look"})
	if st, _ := c.status(plain.ID, 0); st.Contract != nil {
		t.Errorf("contract %+v", st.Contract)
	}
}

// run_task's schema teaches the contract, and has no approval in it.
func TestRunTaskContractSchema(t *testing.T) {
	c := newCatalogCore(t)
	cs := connectTo(t, newMCPServer(c))
	tools, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tl := range tools.Tools {
		if tl.Name != "run_task" {
			continue
		}
		raw, _ := json.Marshal(tl.InputSchema)
		s := string(raw)
		for _, want := range []string{`"contract"`, `"objective"`, `"checks"`, `"exists"`, `"budget"`, `"attempts"`, "the policy decides"} {
			if !strings.Contains(s, want) {
				t.Errorf("schema lacks %s", want)
			}
		}
		if strings.Contains(s, `"approval"`) {
			t.Error("the model may set approval")
		}
		return
	}
	t.Fatal("no run_task")
}

func TestContractOnDrawer(t *testing.T) {
	tw := newTestWeb(t)
	tw.setup()
	tw.c.store.insertTask(&TaskRow{ID: "t-1", Node: "pc", Agent: "small", Runtime: "claude-code", Prompt: "Fix it", State: stateRunning})
	tw.c.store.setContract("t-1", &taskContract{Objective: "The form sends mail", Checks: []taskCheck{{Name: "tests", Run: "go test ./..."}},
		Evidence: []string{"the PR link"}, Budget: &taskBudget{Minutes: 45}})
	_, body, _ := tw.do("GET", "/tasks/t-1", nil, map[string]string{"HX-Request": "true"})
	for _, want := range []string{"Contrat", "Objectif :", "The form sends mail", "go test ./...", "Preuves attendues", "the PR link", "45 min"} {
		if !strings.Contains(body, want) {
			t.Errorf("drawer lacks %q", want)
		}
	}
}
