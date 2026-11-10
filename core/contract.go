package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"slices"
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
		return errors.New("give run (a non-empty shell command) or exists (a path), not both")
	}
	if ch.Dir != "" && ch.Run == "" {
		return errors.New("dir is where run runs: an exists check takes none (give its path in exists)")
	}
	if ch.Timeout < 0 || ch.Timeout > maxCheckTimeout {
		return fmt.Errorf("timeout is at most %d seconds", maxCheckTimeout)
	}
	if ch.Timeout == 0 {
		ch.Timeout = checkTimeout
	}
	// Only the task's own files: /work, or relative to where it works.
	for _, f := range []struct{ field, p string }{{"exists", ch.Exists}, {"dir", ch.Dir}} {
		if err := checkWorkPath(f.field, f.p); err != nil {
			return err
		}
	}
	return nil
}

// checkWorkPath says what is certainly wrong with a check's path: outside
// /work, climbing with "..", or not a single line. Whether a folder
// exists is only known once the repository is cloned: the node says so
// then (a config failure, which the brain fixes).
func checkWorkPath(field, p string) error {
	switch {
	case p == "":
		return nil
	case strings.ContainsAny(p, "\x00\n\r"):
		return fmt.Errorf("%s is one path, on one line", field)
	case slices.Contains(strings.Split(p, "/"), ".."):
		return fmt.Errorf("%s %q climbs with \"..\": give a path relative to the task's working directory (/work/repo with a repository, else /work), or absolute under /work", field, p)
	case !underWork(p):
		return fmt.Errorf("%s %q is outside /work: give a path relative to the task's working directory (/work/repo with a repository, else /work), or absolute under /work", field, p)
	}
	return nil
}

// checkLent says which check names, by an absolute path, what the task is
// not lent: the repository's /work/repo without a repository, or a folder
// of /work/folders that is not one of its folders.
func (k *taskContract) checkLent(repo bool, folders []string) error {
	for i, ch := range k.Checks {
		for _, f := range []struct{ field, p string }{{"exists", ch.Exists}, {"dir", ch.Dir}} {
			if !path.IsAbs(f.p) {
				continue
			}
			c := path.Clean(f.p)
			if !repo && (c == "/work/repo" || strings.HasPrefix(c, "/work/repo/")) {
				return fmt.Errorf("contract: check %d: %s %s is in /work/repo, but no repository is lent to this task (resources {kind: repo})", i+1, f.field, f.p)
			}
			if rest, ok := strings.CutPrefix(c, "/work/folders/"); ok {
				name, _, _ := strings.Cut(rest, "/")
				if !slices.Contains(folders, name) {
					lent := "none"
					if len(folders) > 0 {
						lent = strings.Join(folders, ", ")
					}
					return fmt.Errorf("contract: check %d: %s %s names the folder %q, which is not lent to this task (lent: %s)", i+1, f.field, f.p, name, lent)
				}
			}
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

// fixedChecksReply is the agent's next turn when the brain fixed the
// checks and gave no reply of its own.
const fixedChecksReply = "Eriphe fixed the checks of your task's contract (one could not run). Make sure the work is done, then end your turn with your receipt: the checks run again when it ends."

// fixChecks replaces the checks of an idle task's contract, when one could
// not run (a misconfigured contract): checked like at the start, kept, and
// sent to its node with the next turn, after which they run. The rest of
// the contract stays.
func (c *core) fixChecks(ctx context.Context, id string, checks []taskCheck, reply string) (string, error) {
	t, err := c.store.task(id)
	if err != nil {
		return "", err
	}
	k := c.store.contract(id)
	if k == nil {
		return "", errors.New("the task has no contract: its checks are given with run_task")
	}
	if t.State != stateIdle {
		return "", fmt.Errorf("the task is %s: its checks are fixed once its turn is over (idle)", t.State)
	}
	fixed := *k
	fixed.Checks = checks
	if err := fixed.normalize(); err != nil {
		return "", err
	}
	spec, err := c.store.taskSpec(id)
	if err != nil {
		return "", err
	}
	var folders []string
	if t.Folders != "" {
		folders = strings.Split(t.Folders, ",")
	}
	if err := fixed.checkLent(spec.Repo != "", folders); err != nil {
		return "", err
	}
	if err := c.turnsLeft(id); err != nil {
		return "", err
	}
	if err := c.store.setContract(id, &fixed); err != nil {
		return "", err
	}
	// A restarted task gets them from its spec.
	spec.Checks = fixed.Checks
	if err := c.store.setSpec(id, spec); err != nil {
		return "", err
	}
	if strings.TrimSpace(reply) == "" {
		reply = fixedChecksReply
	}
	reply += "\n\n" + fixed.instruction() // its standing instruction has the old ones
	if err := c.store.addInbox(id, "message", map[string]any{"text": reply, "checks": fixed.Checks}); err != nil {
		return "", err
	}
	c.store.updateTask(id, map[string]any{"state": stateRunning})
	c.bump()
	return "checks fixed; they run when the task's next turn ends", nil
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
