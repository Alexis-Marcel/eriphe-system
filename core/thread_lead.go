package main

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"log"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The thread lead: a temporary assistant, one per running thread. It
// is born when the user approves the plan and goes when the thread
// ends. Woken by what happens to its steps (the thread trigger), it
// judges them on their evidence, amends the plan within the limits and
// closes the thread; it reports to the brain what the user must know or
// decide. It never talks to the user nor starts a task: the brain is the
// only voice, the engine the only one that starts steps.
//
// The final objective check: a thread with a done_when is closed only
// once a fresh agent, lent read-only what the steps wrote, says it holds.

//go:embed lead-prompt.md
var leadPrompt string

const (
	assistantRoleLead = "lead"
	triggerThread     = "thread" // thread:<id>
	finalStep         = "final-check"
	finalChecks       = 2 // final checks a thread may ask before its lead reports

	leadStartPrompt = "Tu viens de démarrer. Lis le thread avec `thread` : tu seras réveillé à chaque étape. Sa surface dans l'espace, eriphe-core l'écrit et la tient à jour. Puis réponds seulement « [rien] »."
)

const threadInboxSchema = `CREATE TABLE IF NOT EXISTS thread_inbox (
	id        INTEGER PRIMARY KEY AUTOINCREMENT,
	thread   INTEGER NOT NULL,
	text      TEXT NOT NULL,
	taint     TEXT NOT NULL DEFAULT '',
	delivered INTEGER NOT NULL DEFAULT 0
);`

func init() {
	extraSchema = append(extraSchema, threadInboxSchema)
	assistantTriggers[triggerThread] = assistantTrigger{
		check: func(arg string) error {
			if n, err := strconv.ParseInt(arg, 10, 64); err != nil || n <= 0 {
				return errors.New("a thread trigger names its thread's id")
			}
			return nil
		},
		deliver: (*core).deliverLead,
	}
	assistantRoles[assistantRoleLead] = assistantRole{
		Powers:      []string{powerReports},
		Wakes:       []string{triggerThread},
		StartPrompt: leadStartPrompt,
		firstPrompt: func(c *core, a Assistant, _ bool) string {
			first := langLine(c.userLang()) + "\n\n" + leadStartPrompt // its surface is written in this turn
			// What is known of its thread's subject (subject_card.go).
			id := leadThread(a)
			if card := c.startCard(c.sourceSubject(threadRef(id)), fmt.Sprintf("lead of thread %d", id)); card != "" {
				first += "\n\n" + card
			}
			return first
		},
		HarnessTools: []string{"ToolSearch"},
		instructions: leadInstructions,
		ownTools:     registerLeadTools,
		started: func(c *core, a Assistant, _ *TaskRow, resumed bool) {
			c.updateAssistant(a.Name, func(st *AssistantStatus) {
				st.TaintSeq = 0
				if !resumed {
					st.Tainted = nil
				}
			})
		},
	}
}

// leadName is the name of a thread's lead.
func leadName(thread int64) string { return "lead-" + strconv.FormatInt(thread, 10) }

// leadThread is the thread a lead leads, from its trigger.
func leadThread(a Assistant) int64 {
	for _, t := range a.triggers() {
		if triggerKind(t) == triggerThread {
			_, arg, _ := strings.Cut(t, ":")
			n, _ := strconv.ParseInt(strings.TrimSpace(arg), 10, 64)
			return n
		}
	}
	return 0
}

// leadInstructions are its role's frame, then its thread as planned.
func leadInstructions(c *core, a Assistant, _ string) string {
	frame := standingInstructions(leadPrompt, c.conf().User, a.displayName())
	s, err := c.thread(leadThread(a))
	if err != nil {
		return frame
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n## Ton thread\n\n%d « %s »\nBut : %s\n", frame, s.ID, s.Title, s.Goal)
	if s.DoneWhen != "" {
		fmt.Fprintf(&b, "Fini quand : %s\n", s.DoneWhen)
	}
	fmt.Fprintf(&b, "Budget : %d min.\n", s.Budget)
	return b.String()
}

// createLead gives a thread its lead. Without an agent able to run one,
// the brain stays its lead.
func (c *core) createLead(s Thread) {
	name := leadName(s.ID)
	a := Assistant{Role: assistantRoleLead, DisplayName: fmt.Sprintf("Chef du thread %d", s.ID), Triggers: []string{triggerThread + ":" + strconv.FormatInt(s.ID, 10)}}
	if firstAgentFor(c.conf()) == "" {
		return
	}
	if err := c.putAssistant(name, a); err != nil {
		log.Printf("thread %d: no lead: %v", s.ID, err)
		return
	}
	c.setThread(s.ID, map[string]any{"lead": name})
	c.logThread(s.ID, "engine", "Le chef du thread démarre.")
}

// dropLead removes a thread's lead: its task stops.
func (c *core) dropLead(s Thread) {
	if s.Lead == "" {
		return
	}
	if err := c.deleteAssistant(s.Lead); err != nil && !isNotFound(err) {
		log.Printf("thread %d: lead %s: %v", s.ID, s.Lead, err)
	}
}

func isNotFound(err error) bool {
	var ce *catalogError
	return errors.As(err, &ce) && ce.Status == 404
}

// hasLead tells whether a thread's lead is there to read what it is told.
func (c *core) hasLead(s Thread) bool {
	if s.Lead == "" {
		return false
	}
	a, ok := c.conf().Assistants[s.Lead]
	return ok && !a.Disabled
}

// deliverLead gives an idle lead what happened to its thread.
func (c *core) deliverLead(ctx context.Context, a Assistant, t *TaskRow) (bool, error) {
	id := leadThread(a)
	rows, err := c.store.db.Query(`SELECT id, text, taint FROM thread_inbox WHERE thread = ? AND delivered = 0 ORDER BY id`, id)
	if err != nil {
		return false, err
	}
	var ids []int64
	var texts, taints []string
	for rows.Next() {
		var n int64
		var text, taint string
		if rows.Scan(&n, &text, &taint) == nil {
			ids, texts = append(ids, n), append(texts, text)
			if taint != "" {
				taints = append(taints, taint)
			}
		}
	}
	rows.Close()
	if len(ids) == 0 {
		return false, nil
	}
	if len(taints) > 0 {
		// What it read taints its thread, and so its reports.
		c.updateAssistant(a.Name, func(st *AssistantStatus) {
			for _, x := range taints {
				if !slices.Contains(st.Tainted, x) {
					st.Tainted = append(st.Tainted, x)
				}
			}
		})
	}
	text := nowLine(c.notifyTime()) + "\n" + langLine(c.userLang()) + "\n\n" + strings.Join(texts, "\n\n")
	if _, err := c.answer(ctx, t.ID, "", text, nil, byPolicy); err != nil {
		return false, err
	}
	for _, n := range ids {
		c.store.db.Exec(`UPDATE thread_inbox SET delivered = ? WHERE id = ?`, time.Now().Unix(), n)
	}
	return true, nil
}

// leadOnly refuses the brain a lead's decision on a thread that has one.
func (c *core) leadOnly(id int64) error {
	s, err := c.thread(id)
	if err != nil {
		return err
	}
	if !s.led() {
		return fmt.Errorf("thread %d is light: add its steps with add_step", id)
	}
	if c.hasLead(s) {
		return fmt.Errorf("thread %d has its lead (%s): it judges the steps and changes the plan. Tell it what %s wants with tell_lead", id, s.Lead, c.conf().User)
	}
	return nil
}

// tellLeadFromBrain passes the lead what the brain relays of the user.
func (c *core) tellLeadFromBrain(id int64, text string, taint []string) error {
	s, err := c.thread(id)
	if err != nil {
		return err
	}
	if !s.active() {
		return fmt.Errorf("the thread is %s", s.Look())
	}
	if !c.hasLead(s) {
		return errors.New("this thread has no lead of its own: you lead it")
	}
	if text = strings.TrimSpace(text); text == "" {
		return errors.New("text is needed")
	}
	c.logThread(id, "brain", "Au chef : "+text)
	c.queueLead(s, "De la part d'Eriphe : "+text, taint)
	return nil
}

func (c *core) queueLead(s Thread, text string, taint []string) {
	c.store.db.Exec(`INSERT INTO thread_inbox (thread, text, taint) VALUES (?, ?, ?)`, s.ID, text, strings.Join(taint, ", "))
	c.ping()
}

// The lead's tools: its thread's, on its own MCP server.
func registerLeadTools(c *core, s *mcp.Server, a Assistant) {
	id := leadThread(a)
	by := "lead"
	c.registerLeadSurfaceTools(s, a) // its thread's surfaces (workspace_tools.go)
	tool(s, "thread", "Your thread as it is now: its goal, done_when, state, budget and time spent, each step (status, task, evidence, output, your last note) and the recent log.",
		func(ctx context.Context, _ struct{}) (threadView, error) { return c.threadView(id, 30) })
	tool(s, "settle_step", "Judge a step that reported its work or stopped, on its evidence: accept, retry (at most 2 per step; say what to fix) or reject. A step whose checks failed cannot be accepted.",
		func(ctx context.Context, in leadSettleIn) (Step, error) {
			return c.settleStep(ctx, id, in.Step, in.Verdict, in.Note, in.Output, by)
		})
	tool(s, "amend_thread", "Change the plan: add steps (key, title, kind, after, prompt, agent, resources, contract, question, options), drop steps that have not started, with why. Twice without asking; past that the user is asked on a card.",
		func(ctx context.Context, in leadAmendIn) (map[string]any, error) {
			note, err := c.amendThread(ctx, id, amendIn{Add: in.Add, Drop: in.Drop}, in.Why, by)
			return map[string]any{"note": note}, err
		})
	tool(s, "close_thread", "Close the thread once every step is accepted, rejected, dropped or blocked, with a summary for the user. With a done_when, a fresh agent checks the objective first; you are told of its verdict.",
		func(ctx context.Context, in leadEndIn) (map[string]any, error) {
			note, err := c.finishThread(ctx, id, in.Summary, by)
			return map[string]any{"note": note}, err
		})
}

type leadSettleIn struct {
	Step    string `json:"step" jsonschema:"the step's key"`
	Verdict string `json:"verdict" jsonschema:"accept, retry or reject"`
	Note    string `json:"note,omitempty" jsonschema:"why; for a retry, what to fix: the agent reads it"`
	Output  string `json:"output,omitempty" jsonschema:"accept: what the step produced, for the steps after it, when its message is not enough"`
}

type leadAmendIn struct {
	Why  string     `json:"why" jsonschema:"why the plan changes, in a sentence the user reads"`
	Add  []stepSpec `json:"add,omitempty"`
	Drop []string   `json:"drop,omitempty"`
}

type leadEndIn struct {
	Summary string `json:"summary" jsonschema:"what the thread did, where its results are and what is left, for the user, in the user's language"`
}

// The final objective check.

// finishThread closs a thread whose steps are all over: at once
// without a done_when, else once a fresh agent checked the objective.
func (c *core) finishThread(ctx context.Context, id int64, summary, by string) (string, error) {
	threadMu.Lock()
	defer threadMu.Unlock()
	s, err := c.thread(id)
	if err != nil {
		return "", err
	}
	if s.Phase != phaseRunning && s.Phase != phasePaused {
		return "", fmt.Errorf("the thread is %s", s.Look())
	}
	if summary = strings.TrimSpace(summary); summary == "" {
		return "", errors.New("a summary for the user is needed")
	}
	steps, _ := c.steps(id)
	var final *Step
	for i, st := range steps {
		if st.Key == finalStep {
			final = &steps[i]
			continue
		}
		if !st.done() && st.Status != stepBlocked {
			return "", fmt.Errorf("step %s is %s: judge it first, or stop the thread", st.Key, st.Status)
		}
	}
	if s.DoneWhen == "" {
		c.closeLocked(s, phaseDone, summary)
		c.logThread(id, by, "Thread clos : "+summary)
		c.tellBrain(s, "Thread clos. Résumé du chef : "+summary+"\nDis-le à "+c.conf().User+" en une fois.", nil)
		return "Closed.", nil
	}
	if final != nil && (final.Status == stepRunning || final.Status == stepWaiting) {
		return "The objective is being checked already: you are told of the verdict.", nil
	}
	if final != nil && final.Tries+1 >= finalChecks {
		return "", fmt.Errorf("the objective was checked %d times and does not hold: report to Eriphe what is missing, for %s to decide", finalChecks, c.conf().User)
	}
	c.setThread(id, map[string]any{"summary": summary})
	s.Summary = summary
	spec := c.finalSpec(s, steps)
	if final == nil {
		if err := c.insertSteps(id, []stepSpec{spec}, true); err != nil {
			return "", err
		}
	} else {
		c.setStep(id, finalStep, map[string]any{"status": stepWaiting, "tries": final.Tries + 1, "failed": false, "evidence": "", "task": ""})
	}
	c.logThread(id, by, "Fin demandée : un agent neuf vérifie l'objectif.")
	c.advanceLocked(ctx, id)
	return "A fresh agent checks the objective now: you are told of its verdict.", nil
}

// finalSpec is the final check's step: a fresh agent, lent read-only what
// the steps wrote, given the goal and what each step produced.
func (c *core) finalSpec(s Thread, steps []Step) stepSpec {
	var b strings.Builder
	fmt.Fprintf(&b, "You are a fresh judge, independent from the agents who did the work. Check whether this work thread reached its objective.\n\nGoal: %s\nDone when: %s\n\n", s.Goal, s.DoneWhen)
	b.WriteString("Inspect what you are lent (read-only) yourself; what the steps say below is only their claim.\n")
	var lent []ResourceRef
	seen := map[string]bool{}
	for _, st := range steps {
		if st.Key == finalStep || st.Status == stepDropped {
			continue
		}
		fmt.Fprintf(&b, "\n### Step « %s » (%s)\n%s\n", st.Title, st.Status, truncate(orStr(st.Output, st.Evidence), 1500))
		for _, r := range st.Resources {
			if r.Kind != resRepo && r.Kind != resFolder && r.Kind != resPlace {
				continue
			}
			r.Access, r.Hold = accessRead, false
			if k := r.key() + "@" + r.Branch; !seen[k] {
				seen[k] = true
				lent = append(lent, r)
			}
		}
	}
	b.WriteString("\nYour receipt's status is done only if the done-when holds, with how you checked it in verified; otherwise partial, with what is missing in not_verified.")
	return stepSpec{Key: finalStep, Title: "Contrôle de l'objectif", Kind: stepTask, Prompt: b.String(), Resources: lent}
}

// judgeFinal reads the final check's verdict: the objective holds and the
// thread closs, or its lead hears what is missing.
func (c *core) judgeFinal(s Thread, st Step, r *checkedReceipt, last string) {
	now := time.Now().Unix()
	held := !r.missing() && r.Status == "done" && len(r.failedChecks()) == 0 && !r.Unsupported
	evidence := "Sans reçu lisible : " + truncate(last, 600)
	if !r.missing() {
		evidence = r.brainText()
	}
	if held {
		c.setStep(s.ID, st.Key, map[string]any{"status": stepAccepted, "evidence": truncate(evidence, 4000), "output": truncate(last, 8000), "ended": now})
		c.stopStepTask(context.Background(), st)
		c.closeLocked(s, phaseDone, s.Summary)
		c.logThread(s.ID, "engine", "Objectif vérifié par un agent neuf : thread clos.")
		c.tellBrain(s, "Thread clos : un agent neuf a vérifié l'objectif (« "+s.DoneWhen+" »).\nRésumé du chef : "+s.Summary+"\nDis-le à "+c.conf().User+" en une fois.", nil)
		return
	}
	c.setStep(s.ID, st.Key, map[string]any{"status": stepRejected, "evidence": truncate(evidence, 4000), "output": truncate(last, 8000), "ended": now})
	c.stopStepTask(context.Background(), st)
	c.logThread(s.ID, "engine", "L'objectif n'est pas atteint selon le contrôle final.")
	c.tellLead(s, "Le contrôle final dit que l'objectif n'est pas atteint :\n"+evidence+"\nComplète le plan (amend_thread) puis clos à nouveau, ou dis à Eriphe ce qui manque.", nil)
}
