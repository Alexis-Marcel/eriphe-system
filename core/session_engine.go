package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"slices"
	"strings"
	"sync"
	"time"
)

// The session engine: code, without a model. It starts the steps whose
// predecessors are accepted, passes them their outputs, asks the cards of
// the checkpoints, follows the steps' tasks, and holds the limits. The
// lead (the brain, for now) judges; the user decides on the cards.

// sessionMu makes the engine's changes one at a time: a step's end, a
// lead's verdict and a card's answer at the same moment move a session
// once each.
var sessionMu sync.Mutex

// sessionIn is a session as the brain opens it.
type sessionIn struct {
	Title    string     `json:"title" jsonschema:"what the user reads for the session, in a few words in their language: Refonte du service photos"`
	Goal     string     `json:"goal" jsonschema:"what the session is for, in one or two sentences"`
	DoneWhen string     `json:"done_when,omitempty" jsonschema:"what must be true at the end, checkable: the objective the session is judged on"`
	Budget   int        `json:"budget_minutes,omitempty" jsonschema:"minutes of running time it may take, 60 by default; past it the engine pauses and asks the user"`
	Steps    []stepSpec `json:"steps" jsonschema:"the plan: each step with its key, title, kind, the steps it comes after, and for a task its prompt, agent, resources and contract"`
}

// openSession records a session and asks the user to approve its plan.
// taint is why the turn that opens it may carry what someone else wrote.
func (c *core) openSession(in sessionIn, taint []string, by string) (Session, error) {
	in.Title, in.Goal, in.DoneWhen = oneLine(in.Title), strings.TrimSpace(in.Goal), strings.TrimSpace(in.DoneWhen)
	switch {
	case in.Title == "":
		return Session{}, errors.New("a title is needed")
	case in.Goal == "":
		return Session{}, errors.New("a goal is needed")
	case len(in.Steps) == 0:
		return Session{}, errors.New("a plan of one step at least is needed")
	case in.Budget < 0 || in.Budget > maxBudget:
		return Session{}, fmt.Errorf("budget_minutes: 1 to %d", maxBudget)
	}
	if in.Budget == 0 {
		in.Budget = defaultBudget
	}
	if err := checkPlan(nil, in.Steps); err != nil {
		return Session{}, err
	}
	for _, st := range in.Steps {
		if err := c.checkStepAgent(st); err != nil {
			return Session{}, err
		}
	}
	res, err := c.store.db.Exec(`INSERT INTO sessions (title, goal, done_when, state, budget, tainted, created) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		truncate(in.Title, taskTitleMax), in.Goal, in.DoneWhen, sessProposed, in.Budget, strings.Join(taint, ", "), time.Now().Unix())
	if err != nil {
		return Session{}, err
	}
	id, _ := res.LastInsertId()
	if err := c.insertSteps(id, in.Steps, true); err != nil {
		return Session{}, err
	}
	c.logSession(id, by, fmt.Sprintf("Session ouverte : %s (%d étapes, budget %d min).", in.Goal, len(in.Steps), in.Budget))
	if _, err := c.ask(id, askPlan, "", "", nil, by); err != nil {
		return Session{}, err
	}
	return c.session(id)
}

// checkStepAgent tells whether a task step's agent is one.
func (c *core) checkStepAgent(st stepSpec) error {
	if st.Kind != stepTask {
		return nil
	}
	if _, _, err := c.conf().agent(st.Agent); err != nil {
		return fmt.Errorf("step %s: %w", st.Key, err)
	}
	return nil
}

// advanceSession starts what may start in a running session, and tells
// the lead once when it is complete or stuck.
func (c *core) advanceSession(ctx context.Context, id int64) {
	sessionMu.Lock()
	defer sessionMu.Unlock()
	c.advanceLocked(ctx, id)
}

func (c *core) advanceLocked(ctx context.Context, id int64) {
	s, err := c.session(id)
	if err != nil || s.State != sessRunning {
		return
	}
	if s.overBudget(time.Now()) {
		c.holdForBudget(s)
		return
	}
	steps, err := c.steps(id)
	if err != nil {
		return
	}
	status := map[string]string{}
	var writing []string // the scopes of the steps that run
	for _, st := range steps {
		status[st.Key] = st.Status
		if st.Status == stepRunning {
			writing = append(writing, st.scopes...)
		}
	}
	busy := false // something runs or waits for a judgment
	for i, st := range steps {
		switch st.Status {
		case stepRunning, stepReported, stepVerified:
			busy = true
			continue
		case stepWaiting:
		default:
			continue
		}
		if slices.ContainsFunc(st.After, func(k string) bool { return status[k] != stepAccepted }) {
			continue
		}
		if slices.ContainsFunc(st.scopes, func(sc string) bool { return slices.Contains(writing, sc) }) {
			busy = true // its turn to write comes
			continue
		}
		if err := c.startStep(ctx, s, st, steps); err != nil {
			log.Printf("session %d: step %s: %v", id, st.Key, err)
			c.setStep(id, st.Key, map[string]any{"status": stepBlocked, "note": truncate(err.Error(), sessionTextMax), "ended": time.Now().Unix()})
			c.logSession(id, "engine", fmt.Sprintf("L'étape « %s » n'a pas pu démarrer : %v", st.Title, err))
			c.tellLead(s, fmt.Sprintf("L'étape « %s » (%s) n'a pas pu démarrer : %v\nRéessaie-la avec settle_step (retry), corrige le plan avec amend_session, ou arrête la session.", st.Title, st.Key, err), nil)
			status[st.Key] = stepBlocked
			continue
		}
		steps[i].Status, status[st.Key] = stepRunning, stepRunning
		writing = append(writing, st.scopes...)
		busy = true
		c.setSession(id, map[string]any{"told": ""})
		s.told = ""
	}
	if busy {
		return
	}
	complete := !slices.ContainsFunc(steps, func(st Step) bool {
		return st.Key != finalStep && st.Status != stepAccepted && st.Status != stepDropped
	})
	switch {
	case complete && s.told != "complete":
		c.setSession(id, map[string]any{"told": "complete"})
		c.logSession(id, "engine", "Toutes les étapes sont acceptées.")
		c.tellLead(s, "Toutes les étapes sont acceptées. Clos la session avec end_session et un résumé pour "+c.conf().User+closeNote(s), nil)
	case !complete && s.told != "stalled":
		var stuck []string
		for _, st := range steps {
			if st.Key != finalStep && (st.Status == stepRejected || st.Status == stepBlocked) {
				stuck = append(stuck, fmt.Sprintf("« %s » (%s, %s)", st.Title, st.Key, st.Status))
			}
		}
		c.setSession(id, map[string]any{"told": "stalled"})
		c.logSession(id, "engine", "Plus rien ne peut avancer : "+strings.Join(stuck, ", ")+".")
		c.tellLead(s, "Plus rien ne peut avancer : "+strings.Join(stuck, ", ")+".\nRéessaie une étape (settle_step retry), modifie le plan (amend_session), ou arrête la session (stop_session) et dis-le à "+c.conf().User+".", nil)
	}
}

func closeNote(s Session) string {
	if s.DoneWhen == "" {
		return "."
	}
	return " : un agent neuf vérifiera d'abord l'objectif (« " + s.DoneWhen + " »)."
}

// startStep starts one step: its task, or its question to the user.
func (c *core) startStep(ctx context.Context, s Session, st Step, steps []Step) error {
	now := time.Now().Unix()
	switch st.Kind {
	case stepApproval:
		if _, err := c.ask(s.ID, askApproval, st.Key, "", nil, "engine"); err != nil {
			return err
		}
		c.logSession(s.ID, "engine", fmt.Sprintf("L'étape « %s » attend la réponse de %s sur une carte.", st.Title, c.conf().User))
		return c.setStep(s.ID, st.Key, map[string]any{"status": stepRunning, "started": now})
	case stepChoice:
		if err := c.setStep(s.ID, st.Key, map[string]any{"status": stepRunning, "started": now}); err != nil {
			return err
		}
		c.logSession(s.ID, "engine", fmt.Sprintf("L'étape « %s » attend le choix de %s.", st.Title, c.conf().User))
		c.tellBrain(s, fmt.Sprintf("L'étape « %s » (%s) attend le choix de %s : %s\nOptions : %s.\nPose-lui la question ; sa réponse s'enregistre avec choose_step, ou sur la page de la session.",
			st.Title, st.Key, c.conf().User, st.Question, strings.Join(st.Options, " · ")), nil)
		return nil
	}
	t, err := c.launchStep(ctx, s, st, steps, "")
	if err != nil {
		return err
	}
	c.logSession(s.ID, "engine", fmt.Sprintf("L'étape « %s » démarre : tâche %s.", st.Title, t.ID))
	return c.setStep(s.ID, st.Key, map[string]any{"status": stepRunning, "task": t.ID, "started": now, "failed": false, "evidence": ""})
}

// launchStep starts a task step's task, with the session's goal and the
// outputs of the steps it comes after; again is what the lead asks of a
// retry. A step in the plan the user approved starts without asking again;
// one added since asks its own start card.
func (c *core) launchStep(ctx context.Context, s Session, st Step, steps []Step, again string) (*TaskRow, error) {
	var b strings.Builder
	b.WriteString(strings.TrimSpace(st.Prompt))
	fmt.Fprintf(&b, "\n\n## Session\nThis is one step of a work session. Its goal: %s", s.Goal)
	if s.DoneWhen != "" {
		fmt.Fprintf(&b, "\nDone when: %s", s.DoneWhen)
	}
	for _, k := range st.After {
		i := slices.IndexFunc(steps, func(x Step) bool { return x.Key == k })
		if i < 0 || steps[i].Output == "" {
			continue
		}
		fmt.Fprintf(&b, "\n\n### Output of the step « %s »\n%s", steps[i].Title, truncate(steps[i].Output, stepOutputMax))
	}
	if again != "" {
		fmt.Fprintf(&b, "\n\n## Retry\nA previous attempt at this step was not accepted. What to fix: %s", again)
	}
	var taint []string
	if s.Tainted != "" {
		taint = strings.Split(s.Tainted, ", ")
	}
	r := runRequest{Title: st.Title, Prompt: b.String(), Resources: st.Resources, Agent: st.Agent, Contract: st.Contract,
		askStart: true, cleanStart: st.Planned, approvedInAdvance: st.Planned}
	if !st.Planned {
		r.taint = taint
	}
	t, err := c.runTask(ctx, r)
	if err != nil {
		return nil, err
	}
	if st.Planned {
		c.grantLent(t) // the user approved it with the plan (access_card.go)
		c.store.set(lentByUserKey(t.ID), "1")
	} else if len(taint) > 0 {
		c.store.set(taskTaintKey(t.ID), "started on a tainted turn: "+s.Tainted)
	}
	return t, nil
}

// sessionTaskChanged follows a step's task: its turn's end is a report,
// judged by the lead on its evidence; its end before is a blocked step.
// It tells whether the task was a step's (the brain then hears of it
// from the session, not as a task of its own).
func (c *core) sessionTaskChanged(id, state string, outcome turnOutcome) bool {
	st, ok := c.stepOfTask(id)
	if !ok {
		return false
	}
	if state == stateWaitingPermission {
		return false // a permission is the user's, told as any task's
	}
	sessionMu.Lock()
	defer sessionMu.Unlock()
	if st.Status != stepRunning {
		return true // judged already, or stopped by the engine
	}
	s, err := c.session(st.Session)
	if err != nil {
		return true
	}
	ctx := context.Background()
	tst, err := c.status(id, 0)
	if err != nil {
		return true
	}
	var taint []string
	if why := c.taskResultTaint(id); why != "" {
		taint = append(taint, why) // what it read (task_taint.go)
	}
	switch state {
	case stateIdle:
		r := outcome.receipt
		if r == nil {
			r = tst.Receipt
		}
		if st.Key == finalStep {
			c.judgeFinal(s, st, r, strings.TrimSpace(tst.LastText)) // session_lead.go
			return true
		}
		failed := !r.missing() && (len(r.failedChecks()) > 0 || r.Unsupported)
		verified := !r.missing() && len(r.Checks) > 0 && !failed
		status, word := stepReported, "non vérifiée : aucun contrôle ne l'a regardée"
		switch {
		case failed:
			word = "ses contrôles ne passent pas : elle ne peut pas être acceptée"
		case verified:
			status, word = stepVerified, "ses contrôles passent"
		}
		evidence := "Sans reçu lisible."
		if !r.missing() {
			evidence = r.brainText()
		}
		output := strings.TrimSpace(tst.LastText)
		if output == "" && !r.missing() {
			output = r.Summary
		}
		c.setStep(s.ID, st.Key, map[string]any{"status": status, "failed": failed, "evidence": truncate(evidence, 4000), "output": truncate(output, 8000)})
		c.logSession(s.ID, "engine", fmt.Sprintf("L'étape « %s » a rendu son travail (%s).", st.Title, word))
		text := fmt.Sprintf("L'étape « %s » (%s, tâche %s) a rendu son travail : %s.\n%s\nSon message : %s\nJuge-la avec settle_step : accept (avec son résultat pour les étapes suivantes, si son message ne suffit pas), retry (en disant quoi corriger ; %d essai(s) restant(s)) ou reject.",
			st.Title, st.Key, id, word, evidence, truncate(output, 1200), max(0, stepRetries-st.Tries))
		c.tellLead(s, text, taint)
	case stateStopped, stateFailed, stateLost:
		if c.store.get("brain_stopped:"+id) != "" {
			return true // stopped by the engine or the lead
		}
		note := c.endedNote(tst.Task)
		c.setStep(s.ID, st.Key, map[string]any{"status": stepBlocked, "note": truncate(state+note, sessionTextMax), "ended": time.Now().Unix()})
		c.logSession(s.ID, "engine", fmt.Sprintf("L'étape « %s » s'est arrêtée (%s).", st.Title, state))
		c.tellLead(s, fmt.Sprintf("L'étape « %s » (%s, tâche %s) s'est arrêtée : %s.%s\nRéessaie-la avec settle_step (retry, %d essai(s) restant(s)), rejette-la, ou modifie le plan.",
			st.Title, st.Key, id, state, note, max(0, stepRetries-st.Tries)), taint)
		c.advanceLocked(ctx, s.ID)
	}
	return true
}

// tellLead tells the session's lead what happened: its own lead, or the
// brain when it has none. taint is why it may carry what someone else
// wrote.
func (c *core) tellLead(s Session, text string, taint []string) {
	if c.hasLead(s) {
		c.queueLead(s, text, taint) // session_lead.go
		return
	}
	c.tellBrain(s, text, taint)
}

// tellBrain tells the brain, as an event of the conversation: what the
// user must hear or answer.
func (c *core) tellBrain(s Session, text string, taint []string) {
	line := fmt.Sprintf("[session %d « %s »] %s", s.ID, s.Title, text)
	seq, err := c.store.addMessage(roleEvent, line, true)
	if err != nil {
		return
	}
	if len(taint) > 0 {
		c.store.set(taintKey(int(seq)), strings.Join(taint, ", "))
	}
	c.store.undeliver(int(seq), int(seq))
	c.ping()
}

// The lead's decisions.

// Verdicts on a step.
const (
	verdictAccept = "accept"
	verdictRetry  = "retry"
	verdictReject = "reject"
)

// settleStep is the lead's verdict on a step that reported, or that
// stopped: accept (never one whose checks failed), retry (within the
// limit) or reject.
func (c *core) settleStep(ctx context.Context, id int64, key, verdict, note, output, by string) (Step, error) {
	sessionMu.Lock()
	defer sessionMu.Unlock()
	s, err := c.session(id)
	if err != nil {
		return Step{}, err
	}
	if !s.active() || s.State == sessProposed {
		return Step{}, fmt.Errorf("the session is %s: nothing to judge", s.State)
	}
	st, err := c.step(id, key)
	if err != nil {
		return Step{}, err
	}
	if st.Kind != stepTask {
		return st, fmt.Errorf("step %s is a %s: %s answers it, not you", key, st.Kind, c.conf().User)
	}
	note = strings.TrimSpace(note)
	now := time.Now().Unix()
	switch verdict {
	case verdictAccept:
		switch {
		case st.Failed:
			return st, errors.New("its checks failed: a report is only a claim. Retry it, saying what to fix, or reject it")
		case st.Status != stepReported && st.Status != stepVerified:
			return st, fmt.Errorf("step %s is %s: only a step that reported its work is accepted", key, st.Status)
		}
		fields := map[string]any{"status": stepAccepted, "note": truncate(note, sessionTextMax), "ended": now}
		if out := strings.TrimSpace(output); out != "" {
			fields["output"] = truncate(out, 8000)
		}
		c.setStep(id, key, fields)
		c.stopStepTask(ctx, st)
		c.logSession(id, by, fmt.Sprintf("Étape « %s » acceptée.%s", st.Title, withNote(note)))
	case verdictRetry:
		switch {
		case note == "":
			return st, errors.New("a retry says what to fix, in note")
		case st.Tries >= stepRetries:
			return st, fmt.Errorf("step %s was retried %d times, the limit: reject it, change the plan, or ask %s", key, st.Tries, c.conf().User)
		case st.Status != stepReported && st.Status != stepVerified && st.Status != stepBlocked && st.Status != stepRejected:
			return st, fmt.Errorf("step %s is %s: nothing to retry", key, st.Status)
		}
		if err := c.retryStep(ctx, s, st, note); err != nil {
			return st, err
		}
		c.logSession(id, by, fmt.Sprintf("Étape « %s » à reprendre : %s", st.Title, note))
	case verdictReject:
		if st.done() || st.Status == stepWaiting {
			return st, fmt.Errorf("step %s is %s: nothing to reject", key, st.Status)
		}
		c.setStep(id, key, map[string]any{"status": stepRejected, "note": truncate(note, sessionTextMax), "ended": now})
		c.stopStepTask(ctx, st)
		c.logSession(id, by, fmt.Sprintf("Étape « %s » rejetée.%s", st.Title, withNote(note)))
	default:
		return st, fmt.Errorf("verdict %q: accept, retry or reject", verdict)
	}
	c.advanceLocked(ctx, id)
	return c.step(id, key)
}

func withNote(note string) string {
	if note == "" {
		return ""
	}
	return " " + note
}

// retryStep asks a step again: its idle task gets the lead's note as its
// next turn; a task that ended, or past its turns, gives way to a new one.
func (c *core) retryStep(ctx context.Context, s Session, st Step, note string) error {
	fields := map[string]any{"status": stepRunning, "tries": st.Tries + 1, "failed": false, "evidence": "", "note": truncate(note, sessionTextMax), "ended": 0}
	if t, err := c.store.task(st.Task); err == nil && t.State == stateIdle {
		if _, err := c.answer(ctx, t.ID, "", "Ton travail n'est pas accepté tel quel. À corriger : "+note, nil, byBrain); err == nil {
			return c.setStep(s.ID, st.Key, fields)
		}
	}
	steps, _ := c.steps(s.ID)
	c.stopStepTask(ctx, st)
	t, err := c.launchStep(ctx, s, st, steps, note)
	if err != nil {
		return err
	}
	fields["task"], fields["started"] = t.ID, time.Now().Unix()
	return c.setStep(s.ID, st.Key, fields)
}

// stopStepTask stops a step's task if it still runs, without telling.
func (c *core) stopStepTask(ctx context.Context, st Step) {
	if st.Task == "" {
		return
	}
	if t, err := c.store.task(st.Task); err == nil && !finished(t.State) {
		c.stopByBrain(ctx, t.ID)
	}
}

// amendIn is a change of a session's plan.
type amendIn struct {
	Add  []stepSpec `json:"add,omitempty" jsonschema:"steps to add, as start_session's; each added step asks its own start card"`
	Drop []string   `json:"drop,omitempty" jsonschema:"keys of steps to take out, that have not started"`
}

// amendSession changes a running session's plan: within the limit at
// once, past it on a card for the user.
func (c *core) amendSession(ctx context.Context, id int64, in amendIn, why, by string) (string, error) {
	sessionMu.Lock()
	defer sessionMu.Unlock()
	s, err := c.session(id)
	if err != nil {
		return "", err
	}
	if s.State != sessRunning && s.State != sessPaused {
		return "", fmt.Errorf("the session is %s: its plan cannot change", s.State)
	}
	why = strings.TrimSpace(why)
	if why == "" {
		return "", errors.New("say why the plan changes, in why")
	}
	if len(in.Add) == 0 && len(in.Drop) == 0 {
		return "", errors.New("nothing to change: add or drop steps")
	}
	if err := c.checkAmend(id, &in); err != nil {
		return "", err
	}
	if s.Amends >= planAmends {
		if _, err := c.ask(id, askAmend, "", why, in, by); err != nil {
			return "", err
		}
		c.logSession(id, by, "Changement du plan demandé à "+c.conf().User+" : "+why)
		return fmt.Sprintf("The plan already changed %d times: %s is asked on a card. You are told of the answer.", s.Amends, c.conf().User), nil
	}
	if err := c.applyAmend(id, in, false); err != nil {
		return "", err
	}
	c.setSession(id, map[string]any{"amends": s.Amends + 1, "told": ""})
	c.logSession(id, by, "Plan modifié : "+why)
	c.advanceLocked(ctx, id)
	return fmt.Sprintf("Plan changed (%d of %d changes without asking). Added steps ask their own start card.", s.Amends+1, planAmends), nil
}

// checkAmend checks a change against the plan as it is.
func (c *core) checkAmend(id int64, in *amendIn) error {
	steps, err := c.steps(id)
	if err != nil {
		return err
	}
	var kept []Step
	for _, st := range steps {
		if slices.Contains(in.Drop, st.Key) {
			if st.Status != stepWaiting {
				return fmt.Errorf("step %s is %s: only a step that has not started is dropped", st.Key, st.Status)
			}
			continue
		}
		kept = append(kept, st)
	}
	for _, k := range in.Drop {
		if !slices.ContainsFunc(steps, func(st Step) bool { return st.Key == k }) {
			return fmt.Errorf("no step %q", k)
		}
	}
	if err := checkPlan(kept, in.Add); err != nil {
		return err
	}
	for _, st := range in.Add {
		if err := c.checkStepAgent(st); err != nil {
			return err
		}
	}
	return nil
}

// applyAmend applies a checked change; planned when the user approved it.
func (c *core) applyAmend(id int64, in amendIn, planned bool) error {
	for _, k := range in.Drop {
		c.setStep(id, k, map[string]any{"status": stepDropped, "ended": time.Now().Unix()})
	}
	return c.insertSteps(id, in.Add, planned)
}

// chooseStep records the user's pick for a choice step.
func (c *core) chooseStep(ctx context.Context, id int64, key, option, by string) (Step, error) {
	sessionMu.Lock()
	defer sessionMu.Unlock()
	st, err := c.step(id, key)
	if err != nil {
		return st, err
	}
	if st.Kind != stepChoice || st.Status != stepRunning {
		return st, fmt.Errorf("step %s does not wait for a choice", key)
	}
	i := slices.IndexFunc(st.Options, func(o string) bool { return strings.EqualFold(o, strings.TrimSpace(option)) })
	if i < 0 {
		return st, fmt.Errorf("%q is not one of its options: %s", option, strings.Join(st.Options, " · "))
	}
	c.setStep(id, key, map[string]any{"status": stepAccepted, "output": st.Options[i], "ended": time.Now().Unix()})
	c.logSession(id, by, fmt.Sprintf("Choix pour « %s » : %s.", st.Title, st.Options[i]))
	c.advanceLocked(ctx, id)
	return c.step(id, key)
}

// stopSession stops a session: its tasks stop, its cards are withdrawn.
func (c *core) stopSession(ctx context.Context, id int64, by string) error {
	sessionMu.Lock()
	defer sessionMu.Unlock()
	s, err := c.session(id)
	if err != nil {
		return err
	}
	if !s.active() {
		return fmt.Errorf("the session is %s already", s.State)
	}
	steps, _ := c.steps(id)
	for _, st := range steps {
		if st.Status == stepRunning || st.Status == stepReported || st.Status == stepVerified {
			c.stopStepTask(ctx, st)
			c.setStep(id, st.Key, map[string]any{"status": stepBlocked, "note": "session stopped", "ended": time.Now().Unix()})
		}
	}
	c.closeLocked(s, sessStopped, "")
	c.logSession(id, by, "Session arrêtée.")
	return nil
}

func (c *core) closeLocked(s Session, state, summary string) {
	now := time.Now()
	fields := map[string]any{"state": state, "ended": now.Unix(), "held": "", "spent": int64(s.elapsed(now) / time.Second), "since": 0}
	if summary != "" {
		fields["summary"] = summary
	}
	c.setSession(s.ID, fields)
	c.closeAsks(s.ID)
	c.dropLead(s)               // session_lead.go
	c.dropSessionSurfaces(s.ID) // surface_authors.go
	c.emitLogged(journalEvent{Kind: evSessionEnded, Key: fmt.Sprintf("session:%d", s.ID), Source: "session",
		Payload: map[string]any{"session": s.ID, "state": state}})
}

// pauseSession stops starting steps; those running finish.
func (c *core) pauseSession(id int64, by string) error {
	sessionMu.Lock()
	defer sessionMu.Unlock()
	s, err := c.session(id)
	if err != nil {
		return err
	}
	if s.State != sessRunning {
		return fmt.Errorf("the session is %s: only a running one pauses", s.State)
	}
	c.pauseLocked(s, "")
	c.logSession(id, by, "Session en pause.")
	return nil
}

func (c *core) pauseLocked(s Session, held string) {
	now := time.Now()
	c.setSession(s.ID, map[string]any{"state": sessPaused, "held": held, "spent": int64(s.elapsed(now) / time.Second), "since": 0})
}

// resumeSession runs a paused session again. One the budget paused needs
// the user: their resume gives it more time.
func (c *core) resumeSession(ctx context.Context, id int64, by string) error {
	sessionMu.Lock()
	defer sessionMu.Unlock()
	s, err := c.session(id)
	if err != nil {
		return err
	}
	if s.State != sessPaused {
		return fmt.Errorf("the session is %s: only a paused one resumes", s.State)
	}
	fields := map[string]any{"state": sessRunning, "held": "", "since": time.Now().Unix()}
	if s.Held == heldBudget {
		if by != byUser {
			return fmt.Errorf("its budget is spent: %s gives it more time, on its card or its page", c.conf().User)
		}
		fields["budget"] = spentMinutes(s) + budgetMore(s)
		c.store.db.Exec(`UPDATE session_asks SET state = ?, decided = ? WHERE session = ? AND kind = ? AND state = ?`, askApproved, time.Now().Unix(), id, askBudget, askPending)
	}
	c.setSession(id, fields)
	c.logSession(id, by, "Session reprise.")
	c.advanceLocked(ctx, id)
	return nil
}

const heldBudget = "budget"

// spentMinutes is the time a session ran, in whole minutes: more time
// counts from there.
func spentMinutes(s Session) int {
	return int((s.elapsed(time.Now()) + time.Minute - 1) / time.Minute)
}

// budgetMore is the time a budget's extension gives: half the budget, 30
// minutes at least.
func budgetMore(s Session) int { return max(30, s.Budget/2) }

// holdForBudget pauses a session past its budget and asks the user for
// more time; the steps running finish.
func (c *core) holdForBudget(s Session) {
	c.pauseLocked(s, heldBudget)
	c.ask(s.ID, askBudget, "", "", map[string]int{"minutes": budgetMore(s)}, "engine")
	c.logSession(s.ID, "engine", fmt.Sprintf("Budget de %d min atteint : session en pause, %s est interrogé sur une carte.", s.Budget, c.conf().User))
	text := fmt.Sprintf("Le budget de %d min est atteint : rien de nouveau ne démarre, les étapes en cours finissent. Une carte demande à %s s'il donne %d min de plus.",
		s.Budget, c.conf().User, budgetMore(s))
	c.tellBrain(s, text, nil)
	if c.hasLead(s) {
		c.queueLead(s, text, nil)
	}
}

// sessionsTick is the engine's pass, every 15 seconds: the budgets, the
// steps whose task ended without being told (eriphe-core restarted), and
// what may start.
func (c *core) sessionsTick(now time.Time) {
	ss, err := c.sessions(true, 50)
	if err != nil {
		return
	}
	ctx := context.Background()
	for _, s := range ss {
		if s.State != sessRunning {
			continue
		}
		steps, _ := c.steps(s.ID)
		for _, st := range steps {
			if st.Status != stepRunning || st.Task == "" {
				continue
			}
			if t, err := c.store.task(st.Task); err == nil && finished(t.State) {
				c.sessionTaskChanged(t.ID, t.State, turnOutcome{})
			}
		}
		c.advanceSession(ctx, s.ID)
	}
}

// The cards.

// sessionProposal reads what a session asks, for its card.
func (c *core) sessionProposal(p proposal) proposal {
	a, err := c.sessionAsk(p.ID)
	if err != nil {
		return p
	}
	s, err := c.session(a.Session)
	if err != nil {
		return p
	}
	p.Pending = a.State == askPending && s.active()
	switch a.State {
	case askApproved:
		p.Outcome = cardApproved
	case askRejected:
		p.Outcome = cardRejected
	}
	p.Why = a.Why
	if a.Kind == askPlan || a.Kind == askAmend {
		p.Tainted = s.Tainted
	}
	switch a.Kind {
	case askPlan:
		steps, _ := c.steps(s.ID)
		p.Fields = stepFields(steps, func(st Step) bool { return st.Planned })
		p.text = func(lang string) string {
			return fmt.Sprintf(tr(lang, "session.card.plan"), s.Title, strings.TrimRight(s.Goal, ". "), len(p.Fields), s.Budget)
		}
	case askAmend:
		var in amendIn
		json.Unmarshal(a.Payload, &in)
		var add []Step
		for _, sp := range in.Add {
			add = append(add, Step{stepSpec: sp})
		}
		p.Fields = stepFields(add, nil)
		if len(in.Drop) > 0 {
			p.Fields = append(p.Fields, cardField{Name: "−", Value: strings.Join(in.Drop, ", ")})
		}
		p.text = func(lang string) string { return fmt.Sprintf(tr(lang, "session.card.amend"), s.Title) }
	case askBudget:
		var more struct{ Minutes int }
		json.Unmarshal(a.Payload, &more)
		p.text = func(lang string) string {
			return fmt.Sprintf(tr(lang, "session.card.budget"), s.Title, s.Budget, more.Minutes)
		}
	case askApproval:
		st, _ := c.step(s.ID, a.Step)
		p.text = func(lang string) string { return fmt.Sprintf(tr(lang, "session.card.approval"), s.Title, st.Question) }
	}
	return p
}

// stepFields are steps as a card lists them: their title, then their
// agent, what they are lent and what they come after.
func stepFields(steps []Step, keep func(Step) bool) []cardField {
	var out []cardField
	for _, st := range steps {
		if keep != nil && !keep(st) {
			continue
		}
		var parts []string
		switch st.Kind {
		case stepTask:
			parts = append(parts, orStr(st.Agent, "agent"))
			for _, r := range st.Resources {
				parts = append(parts, r.String())
			}
		case stepChoice:
			parts = append(parts, st.Question+" ("+strings.Join(st.Options, " · ")+")")
		case stepApproval:
			parts = append(parts, st.Question)
		}
		if len(st.After) > 0 {
			parts = append(parts, "← "+strings.Join(st.After, ", "))
		}
		out = append(out, cardField{Name: fmt.Sprintf("%d. %s", len(out)+1, st.Title), Value: strings.Join(parts, " · ")})
	}
	return out
}

// decideSessionAsk applies the user's answer to what a session asks.
func (c *core) decideSessionAsk(ctx context.Context, id int64, approve bool) error {
	sessionMu.Lock()
	defer sessionMu.Unlock()
	a, err := c.sessionAsk(id)
	if err != nil {
		return err
	}
	s, err := c.session(a.Session)
	if err != nil {
		return err
	}
	state := askRejected
	if approve {
		state = askApproved
	}
	c.store.db.Exec(`UPDATE session_asks SET state = ?, decided = ? WHERE id = ?`, state, time.Now().Unix(), id)
	user := c.conf().User
	switch a.Kind {
	case askPlan:
		if !approve {
			c.closeLocked(s, sessDeclined, "")
			c.logSession(s.ID, "user", "Plan refusé.")
			c.tellBrain(s, user+" a refusé le plan : la session est close. Demande-lui ce qu'il faut changer avant d'en ouvrir une autre.", nil)
			return nil
		}
		now := time.Now().Unix()
		c.setSession(s.ID, map[string]any{"state": sessRunning, "started": now, "since": now})
		c.logSession(s.ID, "user", "Plan approuvé.")
		c.createLead(s) // session_lead.go
		if s, err = c.session(s.ID); err == nil && !c.hasLead(s) {
			c.tellBrain(s, user+" a approuvé le plan : les premières étapes démarrent. Tu seras prévenu de chaque étape.", nil)
		}
	case askAmend:
		var in amendIn
		json.Unmarshal(a.Payload, &in)
		if !approve {
			c.logSession(s.ID, "user", "Changement du plan refusé.")
			c.tellLead(s, user+" a refusé le changement du plan ("+a.Why+").", nil)
			return nil
		}
		if err := c.checkAmend(s.ID, &in); err != nil {
			c.tellLead(s, user+" a approuvé le changement du plan, mais il ne tient plus : "+err.Error(), nil)
			return nil
		}
		c.applyAmend(s.ID, in, true)
		c.setSession(s.ID, map[string]any{"told": ""})
		c.logSession(s.ID, "user", "Changement du plan approuvé.")
		c.tellLead(s, user+" a approuvé le changement du plan : ses étapes démarrent sans autre carte.", nil)
	case askBudget:
		if !approve {
			c.logSession(s.ID, "user", "Pas de temps en plus.")
			c.tellLead(s, user+" ne donne pas plus de temps : la session reste en pause. Clos-la avec un résumé de ce qui est fait, ou arrête-la.", nil)
			return nil
		}
		var more struct{ Minutes int }
		json.Unmarshal(a.Payload, &more)
		c.setSession(s.ID, map[string]any{"state": sessRunning, "held": "", "budget": spentMinutes(s) + more.Minutes, "since": time.Now().Unix()})
		c.logSession(s.ID, "user", fmt.Sprintf("%d min de plus.", more.Minutes))
		c.tellLead(s, fmt.Sprintf("%s donne %d min de plus : la session reprend.", user, more.Minutes), nil)
	case askApproval:
		st, err := c.step(s.ID, a.Step)
		if err != nil || st.Status != stepRunning {
			return nil
		}
		status, word := stepRejected, "non"
		if approve {
			status, word = stepAccepted, "oui"
		}
		c.setStep(s.ID, st.Key, map[string]any{"status": status, "output": word, "ended": time.Now().Unix()})
		c.logSession(s.ID, "user", fmt.Sprintf("« %s » : %s.", st.Title, word))
		c.tellLead(s, fmt.Sprintf("%s a répondu %s à « %s ».", user, word, st.Question), nil)
	}
	c.advanceLocked(ctx, s.ID)
	return nil
}
