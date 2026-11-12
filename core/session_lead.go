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

// The session lead: a temporary assistant, one per running session. It
// is born when the user approves the plan and goes when the session
// ends. Woken by what happens to its steps (the session trigger), it
// judges them on their evidence, amends the plan within the limits and
// closes the session; it reports to the brain what the user must know or
// decide. It never talks to the user nor starts a task: the brain is the
// only voice, the engine the only one that starts steps.
//
// The final objective check: a session with a done_when is closed only
// once a fresh agent, lent read-only what the steps wrote, says it holds.

//go:embed lead-prompt.md
var leadPrompt string

const (
	assistantRoleLead = "lead"
	triggerSession    = "session" // session:<id>
	finalStep         = "final-check"
	finalChecks       = 2 // final checks a session may ask before its lead reports

	leadStartPrompt = "Tu viens de démarrer. Lis la session avec `session` : tu seras réveillé à chaque étape. Réponds seulement « [rien] »."
)

const sessionInboxSchema = `CREATE TABLE IF NOT EXISTS session_inbox (
	id        INTEGER PRIMARY KEY AUTOINCREMENT,
	session   INTEGER NOT NULL,
	text      TEXT NOT NULL,
	taint     TEXT NOT NULL DEFAULT '',
	delivered INTEGER NOT NULL DEFAULT 0
);`

func init() {
	extraSchema = append(extraSchema, sessionInboxSchema)
	assistantTriggers[triggerSession] = assistantTrigger{
		check: func(arg string) error {
			if n, err := strconv.ParseInt(arg, 10, 64); err != nil || n <= 0 {
				return errors.New("a session trigger names its session's id")
			}
			return nil
		},
		deliver: (*core).deliverLead,
	}
	assistantRoles[assistantRoleLead] = assistantRole{
		Powers:       []string{powerReports},
		Wakes:        []string{triggerSession},
		StartPrompt:  leadStartPrompt,
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

// leadName is the name of a session's lead.
func leadName(session int64) string { return "lead-" + strconv.FormatInt(session, 10) }

// leadSession is the session a lead leads, from its trigger.
func leadSession(a Assistant) int64 {
	for _, t := range a.triggers() {
		if triggerKind(t) == triggerSession {
			_, arg, _ := strings.Cut(t, ":")
			n, _ := strconv.ParseInt(strings.TrimSpace(arg), 10, 64)
			return n
		}
	}
	return 0
}

// leadInstructions are its role's frame, then its session as planned.
func leadInstructions(c *core, a Assistant, _ string) string {
	frame := standingInstructions(leadPrompt, c.conf().User, a.displayName())
	s, err := c.session(leadSession(a))
	if err != nil {
		return frame
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n## Ta session\n\n%d « %s »\nBut : %s\n", frame, s.ID, s.Title, s.Goal)
	if s.DoneWhen != "" {
		fmt.Fprintf(&b, "Fini quand : %s\n", s.DoneWhen)
	}
	fmt.Fprintf(&b, "Budget : %d min.\n", s.Budget)
	return b.String()
}

// createLead gives a session its lead. Without an agent able to run one,
// the brain stays its lead.
func (c *core) createLead(s Session) {
	name := leadName(s.ID)
	a := Assistant{Role: assistantRoleLead, DisplayName: fmt.Sprintf("Chef de session %d", s.ID), Triggers: []string{triggerSession + ":" + strconv.FormatInt(s.ID, 10)}}
	if firstAgentFor(c.conf()) == "" {
		return
	}
	if err := c.putAssistant(name, a); err != nil {
		log.Printf("session %d: no lead: %v", s.ID, err)
		return
	}
	c.setSession(s.ID, map[string]any{"lead": name})
	c.logSession(s.ID, "engine", "Le chef de session démarre.")
}

// dropLead removes a session's lead: its task stops.
func (c *core) dropLead(s Session) {
	if s.Lead == "" {
		return
	}
	if err := c.deleteAssistant(s.Lead); err != nil && !isNotFound(err) {
		log.Printf("session %d: lead %s: %v", s.ID, s.Lead, err)
	}
}

func isNotFound(err error) bool {
	var ce *catalogError
	return errors.As(err, &ce) && ce.Status == 404
}

// hasLead tells whether a session's lead is there to read what it is told.
func (c *core) hasLead(s Session) bool {
	if s.Lead == "" {
		return false
	}
	a, ok := c.conf().Assistants[s.Lead]
	return ok && !a.Disabled
}

// deliverLead gives an idle lead what happened to its session.
func (c *core) deliverLead(ctx context.Context, a Assistant, t *TaskRow) (bool, error) {
	id := leadSession(a)
	rows, err := c.store.db.Query(`SELECT id, text, taint FROM session_inbox WHERE session = ? AND delivered = 0 ORDER BY id`, id)
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
		// What it read taints its session, and so its reports.
		c.updateAssistant(a.Name, func(st *AssistantStatus) {
			for _, x := range taints {
				if !slices.Contains(st.Tainted, x) {
					st.Tainted = append(st.Tainted, x)
				}
			}
		})
	}
	text := nowLine(c.notifyTime()) + "\n\n" + strings.Join(texts, "\n\n")
	if _, err := c.answer(ctx, t.ID, "", text, nil, byPolicy); err != nil {
		return false, err
	}
	for _, n := range ids {
		c.store.db.Exec(`UPDATE session_inbox SET delivered = ? WHERE id = ?`, time.Now().Unix(), n)
	}
	return true, nil
}

// leadOnly refuses the brain a lead's decision on a session that has one.
func (c *core) leadOnly(id int64) error {
	s, err := c.session(id)
	if err != nil {
		return err
	}
	if c.hasLead(s) {
		return fmt.Errorf("session %d has its lead (%s): it judges the steps and changes the plan. Tell it what %s wants with tell_lead", id, s.Lead, c.conf().User)
	}
	return nil
}

// tellLeadFromBrain passes the lead what the brain relays of the user.
func (c *core) tellLeadFromBrain(id int64, text string, taint []string) error {
	s, err := c.session(id)
	if err != nil {
		return err
	}
	if !s.active() {
		return fmt.Errorf("the session is %s", s.State)
	}
	if !c.hasLead(s) {
		return errors.New("this session has no lead of its own: you lead it")
	}
	if text = strings.TrimSpace(text); text == "" {
		return errors.New("text is needed")
	}
	c.logSession(id, "brain", "Au chef : "+text)
	c.queueLead(s, "De la part d'Eriphe : "+text, taint)
	return nil
}

func (c *core) queueLead(s Session, text string, taint []string) {
	c.store.db.Exec(`INSERT INTO session_inbox (session, text, taint) VALUES (?, ?, ?)`, s.ID, text, strings.Join(taint, ", "))
	c.ping()
}

// The lead's tools: its session's, on its own MCP server.
func registerLeadTools(c *core, s *mcp.Server, a Assistant) {
	id := leadSession(a)
	by := "lead"
	c.registerLeadSurfaceTools(s, a) // its session's surfaces (workspace_tools.go)
	tool(s, "session", "Your session as it is now: its goal, done_when, state, budget and time spent, each step (status, task, evidence, output, your last note) and the recent log.",
		func(ctx context.Context, _ struct{}) (sessionView, error) { return c.sessionView(id, 30) })
	tool(s, "settle_step", "Judge a step that reported its work or stopped, on its evidence: accept, retry (at most 2 per step; say what to fix) or reject. A step whose checks failed cannot be accepted.",
		func(ctx context.Context, in leadSettleIn) (Step, error) {
			return c.settleStep(ctx, id, in.Step, in.Verdict, in.Note, in.Output, by)
		})
	tool(s, "amend_session", "Change the plan: add steps (key, title, kind, after, prompt, agent, resources, contract, question, options), drop steps that have not started, with why. Twice without asking; past that the user is asked on a card.",
		func(ctx context.Context, in leadAmendIn) (map[string]any, error) {
			note, err := c.amendSession(ctx, id, amendIn{Add: in.Add, Drop: in.Drop}, in.Why, by)
			return map[string]any{"note": note}, err
		})
	tool(s, "end_session", "Close the session once every step is accepted, rejected, dropped or blocked, with a summary for the user. With a done_when, a fresh agent checks the objective first; you are told of its verdict.",
		func(ctx context.Context, in leadEndIn) (map[string]any, error) {
			note, err := c.finishSession(ctx, id, in.Summary, by)
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
	Summary string `json:"summary" jsonschema:"what the session did, where its results are and what is left, for the user, in French"`
}

// The final objective check.

// finishSession closes a session whose steps are all over: at once
// without a done_when, else once a fresh agent checked the objective.
func (c *core) finishSession(ctx context.Context, id int64, summary, by string) (string, error) {
	sessionMu.Lock()
	defer sessionMu.Unlock()
	s, err := c.session(id)
	if err != nil {
		return "", err
	}
	if s.State != sessRunning && s.State != sessPaused {
		return "", fmt.Errorf("the session is %s", s.State)
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
			return "", fmt.Errorf("step %s is %s: judge it first, or stop the session", st.Key, st.Status)
		}
	}
	if s.DoneWhen == "" {
		c.closeLocked(s, sessDone, summary)
		c.logSession(id, by, "Session close : "+summary)
		c.tellBrain(s, "Session close. Résumé du chef : "+summary+"\nDis-le à "+c.conf().User+" en une fois.", nil)
		return "Closed.", nil
	}
	if final != nil && (final.Status == stepRunning || final.Status == stepWaiting) {
		return "The objective is being checked already: you are told of the verdict.", nil
	}
	if final != nil && final.Tries+1 >= finalChecks {
		return "", fmt.Errorf("the objective was checked %d times and does not hold: report to Eriphe what is missing, for %s to decide", finalChecks, c.conf().User)
	}
	c.setSession(id, map[string]any{"summary": summary})
	s.Summary = summary
	spec := c.finalSpec(s, steps)
	if final == nil {
		if err := c.insertSteps(id, []stepSpec{spec}, true); err != nil {
			return "", err
		}
	} else {
		c.setStep(id, finalStep, map[string]any{"status": stepWaiting, "tries": final.Tries + 1, "failed": false, "evidence": "", "task": ""})
	}
	c.logSession(id, by, "Fin demandée : un agent neuf vérifie l'objectif.")
	c.advanceLocked(ctx, id)
	return "A fresh agent checks the objective now: you are told of its verdict.", nil
}

// finalSpec is the final check's step: a fresh agent, lent read-only what
// the steps wrote, given the goal and what each step produced.
func (c *core) finalSpec(s Session, steps []Step) stepSpec {
	var b strings.Builder
	fmt.Fprintf(&b, "You are a fresh judge, independent from the agents who did the work. Check whether this work session reached its objective.\n\nGoal: %s\nDone when: %s\n\n", s.Goal, s.DoneWhen)
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
// session closes, or its lead hears what is missing.
func (c *core) judgeFinal(s Session, st Step, r *checkedReceipt, last string) {
	now := time.Now().Unix()
	held := !r.missing() && r.Status == "done" && len(r.failedChecks()) == 0 && !r.Unsupported
	evidence := "Sans reçu lisible : " + truncate(last, 600)
	if !r.missing() {
		evidence = r.brainText()
	}
	if held {
		c.setStep(s.ID, st.Key, map[string]any{"status": stepAccepted, "evidence": truncate(evidence, 4000), "output": truncate(last, 8000), "ended": now})
		c.stopStepTask(context.Background(), st)
		c.closeLocked(s, sessDone, s.Summary)
		c.logSession(s.ID, "engine", "Objectif vérifié par un agent neuf : session close.")
		c.tellBrain(s, "Session close : un agent neuf a vérifié l'objectif (« "+s.DoneWhen+" »).\nRésumé du chef : "+s.Summary+"\nDis-le à "+c.conf().User+" en une fois.", nil)
		return
	}
	c.setStep(s.ID, st.Key, map[string]any{"status": stepRejected, "evidence": truncate(evidence, 4000), "output": truncate(last, 8000), "ended": now})
	c.stopStepTask(context.Background(), st)
	c.logSession(s.ID, "engine", "L'objectif n'est pas atteint selon le contrôle final.")
	c.tellLead(s, "Le contrôle final dit que l'objectif n'est pas atteint :\n"+evidence+"\nComplète le plan (amend_session) puis clos à nouveau, ou dis à Eriphe ce qui manque.", nil)
}
