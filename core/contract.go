package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strings"
)

// A task's contract: what the brain says must be true at the end, before
// the agent starts. It is optional (a search needs none) and structured,
// not prose. What maps to code is enforced by code: the checks the node
// runs when a turn ends, and the budget eriphe-core keeps. The objective,
// the constraints and the evidence are advice: shown to the agent and on
// the card, and asked for in the receipt; a constraint that is an access is
// already enforced by the resources and the policy. Approval is never the
// contract's: the policy decides it, never the model.

type taskContract struct {
	Objective   string      `json:"objective" jsonschema:"one sentence: what must be true at the end (required)"`
	Constraints []string    `json:"constraints,omitempty" jsonschema:"what the agent must not do or change, in its words (advice; access is enforced by the resources)"`
	Checks      []taskCheck `json:"checks,omitempty" jsonschema:"deterministic checks the node runs in the task's container when a turn ends, cheapest first: only what a check sees pass counts as verified"`
	Evidence    []string    `json:"evidence,omitempty" jsonschema:"what the receipt must show (a PR link, test output…)"`
	Verify      string      `json:"verify,omitempty" jsonschema:"none, auto (default) or always: whether an independent review may run (recorded, not used yet)"`
	Budget      *taskBudget `json:"budget,omitempty" jsonschema:"limits eriphe-core enforces: it stops the task beyond them"`
}

type taskCheck struct {
	Name    string `json:"name" jsonschema:"short name, e.g. build, tests, report"`
	Run     string `json:"run,omitempty" jsonschema:"a shell command, passing when it exits 0"`
	Exists  string `json:"exists,omitempty" jsonschema:"a file or folder that must be there, under /work (or relative to the task's working directory)"`
	Dir     string `json:"dir,omitempty" jsonschema:"where run runs: relative to the task's working directory, or under /work"`
	Timeout int    `json:"timeout,omitempty" jsonschema:"seconds, default 300, at most 3600"`
}

type taskBudget struct {
	Minutes  int `json:"minutes,omitempty" jsonschema:"wall-clock minutes from the start"`
	Attempts int `json:"attempts,omitempty" jsonschema:"automatic retries: a failed check sent back, a passing failure retried (default 2)"`
	Turns    int `json:"turns,omitempty" jsonschema:"agent turns"`
	Tokens   int `json:"tokens,omitempty" jsonschema:"input and output tokens, where the agent's harness reports them"`
}

// The contract's limits.
const (
	maxChecks       = 10
	maxCheckTimeout = 3600
	checkTimeout    = 300
)

var verifyModes = map[string]bool{"none": true, "auto": true, "always": true}

// normalize checks a contract and fills its defaults.
func (k *taskContract) normalize() error {
	k.Objective = strings.TrimSpace(k.Objective)
	if k.Objective == "" {
		return errors.New("contract: objective is required")
	}
	if len([]rune(k.Objective)) > 500 {
		return errors.New("contract: objective is one sentence (at most 500 characters)")
	}
	if len(k.Constraints) > 20 || len(k.Evidence) > 20 {
		return errors.New("contract: at most 20 constraints and 20 evidence items")
	}
	if k.Verify == "" {
		k.Verify = "auto"
	}
	if !verifyModes[k.Verify] {
		return fmt.Errorf("contract: verify is none, auto or always, not %q", k.Verify)
	}
	if len(k.Checks) > maxChecks {
		return fmt.Errorf("contract: at most %d checks", maxChecks)
	}
	seen := map[string]bool{}
	for i := range k.Checks {
		ch := &k.Checks[i]
		if err := ch.normalize(); err != nil {
			return fmt.Errorf("contract: check %d: %w", i+1, err)
		}
		if seen[ch.Name] {
			return fmt.Errorf("contract: two checks named %q", ch.Name)
		}
		seen[ch.Name] = true
	}
	if b := k.Budget; b != nil {
		if b.Minutes < 0 || b.Attempts < 0 || b.Turns < 0 || b.Tokens < 0 {
			return errors.New("contract: a budget is never negative")
		}
		if b.Minutes > 7*24*60 || b.Attempts > 5 || b.Turns > 500 {
			return errors.New("contract: budget too large (at most 7 days, 5 attempts, 500 turns)")
		}
	}
	return nil
}

// defaultAttempts is a contract's automatic retries when its budget gives none.
const defaultAttempts = 2

// attempts is how many automatic retries the contract allows.
func (k *taskContract) attempts() int {
	if k.Budget != nil && k.Budget.Attempts > 0 {
		return k.Budget.Attempts
	}
	return defaultAttempts
}

func (ch *taskCheck) normalize() error {
	ch.Name = strings.TrimSpace(ch.Name)
	ch.Run, ch.Exists, ch.Dir = strings.TrimSpace(ch.Run), strings.TrimSpace(ch.Exists), strings.TrimSpace(ch.Dir)
	if ch.Name == "" || len(ch.Name) > 40 || strings.ContainsAny(ch.Name, "\n\r`") {
		return errors.New("a name of at most 40 characters is required")
	}
	if (ch.Run == "") == (ch.Exists == "") {
		return errors.New("give run or exists, not both")
	}
	if ch.Timeout < 0 || ch.Timeout > maxCheckTimeout {
		return fmt.Errorf("timeout is at most %d seconds", maxCheckTimeout)
	}
	if ch.Timeout == 0 {
		ch.Timeout = checkTimeout
	}
	// Only the task's own files: /work, or relative to where it works.
	for _, p := range []string{ch.Exists, ch.Dir} {
		if p != "" && !underWork(p) {
			return fmt.Errorf("%s is outside /work", p)
		}
	}
	return nil
}

// underWork tells whether a path stays in the task's /work: absolute under
// it, or relative without climbing out.
func underWork(p string) bool {
	if path.IsAbs(p) {
		c := path.Clean(p)
		return c == "/work" || strings.HasPrefix(c, "/work/")
	}
	c := path.Clean(p)
	return c != ".." && !strings.HasPrefix(c, "../")
}

// instruction is the contract as the agent reads it, after its standing
// instruction: short, a map rather than a manual.
func (k *taskContract) instruction() string {
	var b strings.Builder
	b.WriteString("## Task contract\nObjective: " + k.Objective)
	if len(k.Constraints) > 0 {
		b.WriteString("\nConstraints:\n- " + strings.Join(k.Constraints, "\n- "))
	}
	if len(k.Checks) > 0 {
		b.WriteString("\nChecks Eriphe runs itself in your container when your turn ends; a failed one comes back to you (only what they see pass counts as verified):")
		for _, ch := range k.Checks {
			if ch.Run != "" {
				fmt.Fprintf(&b, "\n- %s: `%s`", ch.Name, ch.Run)
				if ch.Dir != "" {
					fmt.Fprintf(&b, " in %s", ch.Dir)
				}
				fmt.Fprintf(&b, " exits 0 within %ds", ch.Timeout)
			} else {
				fmt.Fprintf(&b, "\n- %s: %s exists", ch.Name, ch.Exists)
			}
		}
	}
	if len(k.Evidence) > 0 {
		b.WriteString("\nEvidence your receipt must give:\n- " + strings.Join(k.Evidence, "\n- "))
	}
	if bu := k.Budget; bu != nil {
		var parts []string
		if bu.Minutes > 0 {
			parts = append(parts, fmt.Sprintf("%d minutes", bu.Minutes))
		}
		if bu.Turns > 0 {
			parts = append(parts, fmt.Sprintf("%d turns", bu.Turns))
		}
		if bu.Tokens > 0 {
			parts = append(parts, fmt.Sprintf("%d tokens", bu.Tokens))
		}
		if len(parts) > 0 {
			b.WriteString("\nBudget: " + strings.Join(parts, ", ") + ". Eriphe stops the task beyond it: if you run short, stop and say what is left in your receipt (status partial).")
		}
	}
	return b.String()
}

func init() {
	extraColumns = append(extraColumns, `ALTER TABLE task_harness ADD COLUMN contract TEXT NOT NULL DEFAULT ''`)
}

// contract is a task's contract, nil without one.
func (s *Store) contract(id string) *taskContract {
	var raw string
	s.db.QueryRow(`SELECT contract FROM task_harness WHERE task_id = ?`, id).Scan(&raw)
	if raw == "" {
		return nil
	}
	var k taskContract
	if json.Unmarshal([]byte(raw), &k) != nil {
		return nil
	}
	return &k
}

func (s *Store) setContract(id string, k *taskContract) error {
	raw, _ := json.Marshal(k)
	return s.setHarness(id, map[string]any{"contract": string(raw)})
}
