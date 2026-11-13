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

// The thread engine: code, without a model. It starts the steps whose
// predecessors are accepted, passes them their outputs, asks the cards of
// the checkpoints, follows the steps' tasks, and holds the limits. The
// lead (the brain, for now) judges; the user decides on the cards.

// threadMu makes the engine's changes one at a time: a step's end, a
// lead's verdict and a card's answer at the same moment move a thread
// once each.
var threadMu sync.Mutex

// threadIn is a thread as the brain opens it.
type threadIn struct {
	Title    string     `json:"title" jsonschema:"what the user reads for the thread, in a few words in their language: Refonte du service photos"`
	Goal     string     `json:"goal" jsonschema:"its real outcome, in one or two sentences: what must have happened for it to be done (merged and deployed, not only reviewed)"`
	Subject  string     `json:"subject,omitempty" jsonschema:"the matter it follows, as a ref to find it again: project:<name>, pr:<owner>/<repo>#<n>, mail:<id>, event:<id>"`
	Mode     string     `json:"mode,omitempty" jsonschema:"light (the default without steps: you hold it, no card, no budget) or led (the default with steps: a plan the user approves on a card, a lead, a budget)"`
	DoneWhen string     `json:"done_when,omitempty" jsonschema:"led: what must be true at the end, checkable: the objective the thread is judged on"`
	Budget   int        `json:"budget_minutes,omitempty" jsonschema:"led: minutes of running time it may take, 60 by default; past it the engine pauses and asks the user"`
	Steps    []stepSpec `json:"steps,omitempty" jsonschema:"led: the plan, each step with its key, title, kind, the steps it comes after, and for a task its prompt, agent, resources and contract"`
}

// threadSubjectMax is the longest subject ref kept.
const threadSubjectMax = 200

// openThread records a thread. A light one is open at once: no card, no
// budget. A led one asks the user to approve its plan. taint is why the
// turn that opens it may carry what someone else wrote.
func (c *core) openThread(in threadIn, taint []string, by string) (Thread, error) {
	in.Title, in.Goal, in.DoneWhen = oneLine(in.Title), strings.TrimSpace(in.Goal), strings.TrimSpace(in.DoneWhen)
	in.Subject, in.Mode = strings.TrimSpace(in.Subject), strings.ToLower(strings.TrimSpace(in.Mode))
	if in.Mode == "" {
		in.Mode = modeLight
		if len(in.Steps) > 0 {
			in.Mode = modeLed
		}
	}
	switch {
	case in.Title == "":
		return Thread{}, errors.New("a title is needed")
	case in.Goal == "":
		return Thread{}, errors.New("a goal is needed")
	case len(in.Subject) > threadSubjectMax || strings.ContainsAny(in.Subject, " \n\t"):
		return Thread{}, fmt.Errorf("subject: a ref without spaces, %d characters at most (project:site, pr:owner/repo#12)", threadSubjectMax)
	case in.Mode != modeLight && in.Mode != modeLed:
		return Thread{}, fmt.Errorf("mode %q: light or led", in.Mode)
	}
	if in.Mode == modeLight {
		if len(in.Steps) > 0 || in.Budget != 0 || in.DoneWhen != "" {
			return Thread{}, errors.New("a light thread has no plan, budget nor done_when: open it led for planned, autonomous work")
		}
		res, err := c.store.db.Exec(`INSERT INTO threads (title, goal, mode, subject, state, tainted, created, started) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			truncate(in.Title, taskTitleMax), in.Goal, modeLight, in.Subject, threadOpen, strings.Join(taint, ", "), time.Now().Unix(), time.Now().Unix())
		if err != nil {
			return Thread{}, err
		}
		id, _ := res.LastInsertId()
		c.logThread(id, by, "Thread ouvert : "+in.Goal)
		c.keepThreadSurface(id) // thread_surface.go
		return c.thread(id)
	}
	switch {
	case len(in.Steps) == 0:
		return Thread{}, errors.New("a plan of one step at least is needed")
	case in.Budget < 0 || in.Budget > maxBudget:
		return Thread{}, fmt.Errorf("budget_minutes: 1 to %d", maxBudget)
	}
	if in.Budget == 0 {
		in.Budget = defaultBudget
	}
	if err := checkPlan(nil, in.Steps); err != nil {
		return Thread{}, err
	}
	for _, st := range in.Steps {
		if err := c.checkStepAgent(st); err != nil {
			return Thread{}, err
		}
	}
	res, err := c.store.db.Exec(`INSERT INTO threads (title, goal, done_when, mode, subject, state, phase, budget, tainted, created) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		truncate(in.Title, taskTitleMax), in.Goal, in.DoneWhen, modeLed, in.Subject, stateOf(phaseProposed), phaseProposed, in.Budget, strings.Join(taint, ", "), time.Now().Unix())
	if err != nil {
		return Thread{}, err
	}
	id, _ := res.LastInsertId()
	if err := c.insertSteps(id, in.Steps, true); err != nil {
		return Thread{}, err
	}
	c.logThread(id, by, fmt.Sprintf("Thread ouvert : %s (%d étapes, budget %d min).", in.Goal, len(in.Steps), in.Budget))
	if _, err := c.ask(id, askPlan, "", "", nil, by); err != nil {
		return Thread{}, err
	}
	return c.thread(id)
}

// checkStepAgent tells whether a task step's agent is one.
func (c *core) checkStepAgent(st stepSpec) error {
	if !st.runsTask() {
		return nil
	}
	if _, _, err := c.conf().agent(st.Agent); err != nil {
		return fmt.Errorf("step %s: %w", st.Key, err)
	}
	return nil
}

// advanceThread starts what may start in a running thread, and tells
// the lead once when it is complete or stuck.
func (c *core) advanceThread(ctx context.Context, id int64) {
	threadMu.Lock()
	defer threadMu.Unlock()
	c.advanceLocked(ctx, id)
}

func (c *core) advanceLocked(ctx context.Context, id int64) {
	s, err := c.thread(id)
	switch {
	case err != nil:
		return
	case !s.led() && !s.active():
		return
	case s.led() && s.Phase != phaseRunning:
		return
	case s.led() && s.overBudget(time.Now()):
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
			log.Printf("thread %d: step %s: %v", id, st.Key, err)
			c.setStep(id, st.Key, map[string]any{"status": stepBlocked, "note": truncate(err.Error(), threadTextMax), "ended": time.Now().Unix()})
			c.logThread(id, "engine", fmt.Sprintf("L'étape « %s » n'a pas pu démarrer : %v", st.Title, err))
			fix := "corrige le plan avec amend_thread"
			if !s.led() {
				fix = "ajoute une autre étape avec add_step"
			}
			c.tellLead(s, fmt.Sprintf("L'étape « %s » (%s) n'a pas pu démarrer : %v\nRéessaie-la avec settle_step (retry), %s, ou arrête le thread.", st.Title, st.Key, err, fix), nil)
			status[st.Key] = stepBlocked
			continue
		}
		steps[i].Status, status[st.Key] = stepRunning, stepRunning
		writing = append(writing, st.scopes...)
		busy = true
		c.setThread(id, map[string]any{"told": ""})
		s.told = ""
	}
	if busy || !s.led() {
		return // a light thread's holder hears of each step: nothing more to tell
	}
	complete := !slices.ContainsFunc(steps, func(st Step) bool {
		return st.Key != finalStep && st.Status != stepAccepted && st.Status != stepDropped
	})
	switch {
	case complete && s.told != "complete":
		c.setThread(id, map[string]any{"told": "complete"})
		c.logThread(id, "engine", "Toutes les étapes sont acceptées.")
		c.tellLead(s, "Toutes les étapes sont acceptées. Clos le thread avec close_thread et un résumé pour "+c.conf().User+closeNote(s), nil)
	case !complete && s.told != "stalled":
		var stuck []string
		for _, st := range steps {
			if st.Key != finalStep && (st.Status == stepRejected || st.Status == stepBlocked) {
				stuck = append(stuck, fmt.Sprintf("« %s » (%s, %s)", st.Title, st.Key, st.Status))
			}
		}
		c.setThread(id, map[string]any{"told": "stalled"})
		c.logThread(id, "engine", "Plus rien ne peut avancer : "+strings.Join(stuck, ", ")+".")
		c.tellLead(s, "Plus rien ne peut avancer : "+strings.Join(stuck, ", ")+".\nRéessaie une étape (settle_step retry), modifie le plan (amend_thread), ou arrête le thread (stop_thread) et dis-le à "+c.conf().User+".", nil)
	}
}

func closeNote(s Thread) string {
	if s.DoneWhen == "" {
		return "."
	}
	return " : un agent neuf vérifiera d'abord l'objectif (« " + s.DoneWhen + " »)."
}

// startStep starts one step: its task, its question to the user, its
// call, or its wait (thread_steps.go).
func (c *core) startStep(ctx context.Context, s Thread, st Step, steps []Step) error {
	now := time.Now().Unix()
	if other, err := c.startOther(s, st); other {
		return err
	}
	switch st.Kind {
	case stepApproval:
		if _, err := c.ask(s.ID, askApproval, st.Key, "", nil, "engine"); err != nil {
			return err
		}
		c.logThread(s.ID, "engine", fmt.Sprintf("L'étape « %s » attend la réponse de %s sur une carte.", st.Title, c.conf().User))
		return c.setStep(s.ID, st.Key, map[string]any{"status": stepRunning, "started": now})
	case stepChoice:
		if err := c.setStep(s.ID, st.Key, map[string]any{"status": stepRunning, "started": now}); err != nil {
			return err
		}
		c.logThread(s.ID, "engine", fmt.Sprintf("L'étape « %s » attend le choix de %s.", st.Title, c.conf().User))
		if !s.led() && len(st.After) == 0 {
			return nil // the brain just added it: it asks
		}
		c.tellBrain(s, fmt.Sprintf("L'étape « %s » (%s) attend le choix de %s : %s\nOptions : %s.\nPose-lui la question ; sa réponse s'enregistre avec choose_step, ou sur la page du thread.",
			st.Title, st.Key, c.conf().User, st.Question, strings.Join(st.Options, " · ")), nil)
		return nil
	}
	t, err := c.launchStep(ctx, s, st, steps, "")
	if err != nil {
		return err
	}
	c.logThread(s.ID, "engine", fmt.Sprintf("L'étape « %s » démarre : tâche %s.", st.Title, t.ID))
	return c.setStep(s.ID, st.Key, map[string]any{"status": stepRunning, "task": t.ID, "started": now, "failed": false, "evidence": ""})
}

// launchStep starts a task step's task, with the thread's goal and the
// outputs of the steps it comes after; again is what the lead asks of a
// retry. A step in the plan the user approved starts without asking again;
// one added since asks its own start card.
func (c *core) launchStep(ctx context.Context, s Thread, st Step, steps []Step, again string) (*TaskRow, error) {
	var b strings.Builder
	b.WriteString(strings.TrimSpace(st.Prompt))
	fmt.Fprintf(&b, "\n\n## Thread\nThis is one step of a work thread. Its goal: %s", s.Goal)
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
	planned := st.Planned && s.Phase == phaseRunning // a plan the user approved, still running: never one reopened light
	r := runRequest{Title: st.Title, Prompt: b.String(), Resources: st.Resources, Agent: st.Agent, Contract: st.Contract,
		askStart: true, cleanStart: planned, approvedInAdvance: planned}
	if !planned {
		r.taint = taint
	}
	t, err := c.runTask(ctx, r)
	if err != nil {
		return nil, err
	}
	if planned {
		c.grantLent(t) // the user approved it with the plan (access_card.go)
		c.store.set(lentByUserKey(t.ID), "1")
	} else if len(taint) > 0 {
		c.store.set(taskTaintKey(t.ID), "started on a tainted turn: "+s.Tainted)
	}
	return t, nil
}

// threadTaskChanged follows a step's task: its turn's end is a report,
// judged by the lead on its evidence; its end before is a blocked step.
// It tells whether the task was a step's (the brain then hears of it
// from the thread, not as a task of its own).
func (c *core) threadTaskChanged(id, state string, outcome turnOutcome) bool {
	st, ok := c.stepOfTask(id)
	if !ok {
		return false
	}
	if state == stateWaitingPermission {
		return false // a permission is the user's, told as any task's
	}
	threadMu.Lock()
	defer threadMu.Unlock()
	if st.Status != stepRunning {
		return true // judged already, or stopped by the engine
	}
	s, err := c.thread(st.Thread)
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
			c.judgeFinal(s, st, r, strings.TrimSpace(tst.LastText)) // thread_lead.go
			return true
		}
		if st.Kind == stepCheck {
			c.judgeCheck(s, st, r, strings.TrimSpace(tst.LastText), taint) // thread_steps.go
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
		c.logThread(s.ID, "engine", fmt.Sprintf("L'étape « %s » a rendu son travail (%s).", st.Title, word))
		text := fmt.Sprintf("L'étape « %s » (%s, tâche %s) a rendu son travail : %s.\n%s\nSon message : %s\nJuge-la avec settle_step : accept (avec son résultat pour les étapes suivantes, si son message ne suffit pas), retry (en disant quoi corriger ; %d essai(s) restant(s)) ou reject.",
			st.Title, st.Key, id, word, evidence, truncate(output, 1200), max(0, stepRetries-st.Tries))
		c.tellLead(s, text, taint)
	case stateStopped, stateFailed, stateLost:
		if c.store.get("brain_stopped:"+id) != "" {
			return true // stopped by the engine or the lead
		}
		note := c.endedNote(tst.Task)
		c.setStep(s.ID, st.Key, map[string]any{"status": stepBlocked, "note": truncate(state+note, threadTextMax), "ended": time.Now().Unix()})
		c.logThread(s.ID, "engine", fmt.Sprintf("L'étape « %s » s'est arrêtée (%s).", st.Title, state))
		c.tellLead(s, fmt.Sprintf("L'étape « %s » (%s, tâche %s) s'est arrêtée : %s.%s\nRéessaie-la avec settle_step (retry, %d essai(s) restant(s)), rejette-la, ou modifie le plan.",
			st.Title, st.Key, id, state, note, max(0, stepRetries-st.Tries)), taint)
		c.advanceLocked(ctx, s.ID)
	}
	return true
}

// tellLead tells the thread's lead what happened: its own lead, or the
// brain when it has none. taint is why it may carry what someone else
// wrote.
func (c *core) tellLead(s Thread, text string, taint []string) {
	if c.hasLead(s) {
		c.queueLead(s, text, taint) // thread_lead.go
		return
	}
	c.tellBrain(s, text, taint)
}

// tellBrain tells the brain, as an event of the conversation: what the
// user must hear or answer.
func (c *core) tellBrain(s Thread, text string, taint []string) {
	line := fmt.Sprintf("[thread %d « %s »] %s", s.ID, s.Title, text)
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
	threadMu.Lock()
	defer threadMu.Unlock()
	s, err := c.thread(id)
	if err != nil {
		return Step{}, err
	}
	if !s.active() || s.Phase == phaseProposed {
		return Step{}, fmt.Errorf("the thread is %s: nothing to judge", s.Look())
	}
	st, err := c.step(id, key)
	if err != nil {
		return Step{}, err
	}
	note = strings.TrimSpace(note)
	now := time.Now().Unix()
	switch st.Kind {
	case stepTask:
	case stepUser, stepDeadline:
		return c.settleWait(ctx, s, st, verdict, note, by)
	case stepCall, stepCheck:
		if verdict == verdictRetry {
			return c.restartStep(ctx, s, st, note, by)
		}
		return st, fmt.Errorf("step %s is a %s: its outcome settles it, not you; one blocked or rejected may be retried", key, st.Kind)
	default:
		return st, fmt.Errorf("step %s is a %s: %s answers it, not you", key, st.Kind, c.conf().User)
	}
	switch verdict {
	case verdictAccept:
		switch {
		case st.Failed:
			return st, errors.New("its checks failed: a report is only a claim. Retry it, saying what to fix, or reject it")
		case st.Status != stepReported && st.Status != stepVerified:
			return st, fmt.Errorf("step %s is %s: only a step that reported its work is accepted", key, st.Status)
		}
		fields := map[string]any{"status": stepAccepted, "note": truncate(note, threadTextMax), "ended": now}
		if out := strings.TrimSpace(output); out != "" {
			fields["output"] = truncate(out, 8000)
		}
		c.setStep(id, key, fields)
		c.stopStepTask(ctx, st)
		c.logThread(id, by, fmt.Sprintf("Étape « %s » acceptée.%s", st.Title, withNote(note)))
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
		c.logThread(id, by, fmt.Sprintf("Étape « %s » à reprendre : %s", st.Title, note))
	case verdictReject:
		if st.done() || st.Status == stepWaiting {
			return st, fmt.Errorf("step %s is %s: nothing to reject", key, st.Status)
		}
		c.setStep(id, key, map[string]any{"status": stepRejected, "note": truncate(note, threadTextMax), "ended": now})
		c.stopStepTask(ctx, st)
		c.logThread(id, by, fmt.Sprintf("Étape « %s » rejetée.%s", st.Title, withNote(note)))
	default:
		return st, fmt.Errorf("verdict %q: accept, retry or reject", verdict)
	}
	c.advanceLocked(ctx, id)
	return c.step(id, key)
}

// settleWait is the holder's word on a step that waits: a user step it
// sees done (its evidence in note), a deadline no longer to wait for.
func (c *core) settleWait(ctx context.Context, s Thread, st Step, verdict, note, by string) (Step, error) {
	if st.Status != stepRunning {
		return st, fmt.Errorf("step %s is %s: it waits for nothing", st.Key, st.Status)
	}
	status := stepAccepted
	switch {
	case verdict == verdictReject:
		status = stepRejected
	case verdict != verdictAccept:
		return st, fmt.Errorf("verdict %q: a %s step is accepted or rejected", verdict, st.Kind)
	case st.Kind == stepUser && note == "":
		return st, fmt.Errorf("say in note what shows it is done: otherwise %s answers it", c.conf().User)
	}
	c.setStep(s.ID, st.Key, map[string]any{"status": status, "note": truncate(note, threadTextMax), "ended": time.Now().Unix()})
	c.logThread(s.ID, by, fmt.Sprintf("Étape « %s » : %s.%s", st.Title, stepWord(status), withNote(note)))
	c.advanceLocked(ctx, s.ID)
	return c.step(s.ID, st.Key)
}

// restartStep starts again a call or a check that was blocked or
// rejected: its call asks a new card, its task is a new one.
func (c *core) restartStep(ctx context.Context, s Thread, st Step, note, by string) (Step, error) {
	switch {
	case st.Status != stepBlocked && st.Status != stepRejected:
		return st, fmt.Errorf("step %s is %s: nothing to retry", st.Key, st.Status)
	case st.Tries >= stepRetries:
		return st, fmt.Errorf("step %s was retried %d times, the limit", st.Key, st.Tries)
	}
	c.stopStepTask(ctx, st)
	c.setStep(s.ID, st.Key, map[string]any{"status": stepWaiting, "tries": st.Tries + 1, "card": "", "task": "", "failed": false,
		"evidence": "", "output": "", "tainted": "", "note": truncate(note, threadTextMax), "ended": 0})
	c.logThread(s.ID, by, fmt.Sprintf("Étape « %s » reprise.%s", st.Title, withNote(note)))
	c.advanceLocked(ctx, s.ID)
	return c.step(s.ID, st.Key)
}

func withNote(note string) string {
	if note == "" {
		return ""
	}
	return " " + note
}

// retryStep asks a step again: its idle task gets the lead's note as its
// next turn; a task that ended, or past its turns, gives way to a new one.
func (c *core) retryStep(ctx context.Context, s Thread, st Step, note string) error {
	fields := map[string]any{"status": stepRunning, "tries": st.Tries + 1, "failed": false, "evidence": "", "note": truncate(note, threadTextMax), "ended": 0}
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

// amendIn is a change of a thread's plan.
type amendIn struct {
	Add  []stepSpec `json:"add,omitempty" jsonschema:"steps to add, as open_thread's; each added step asks its own start card"`
	Drop []string   `json:"drop,omitempty" jsonschema:"keys of steps to take out, that have not started"`
}

// amendThread changes a running thread's plan: within the limit at
// once, past it on a card for the user.
func (c *core) amendThread(ctx context.Context, id int64, in amendIn, why, by string) (string, error) {
	threadMu.Lock()
	defer threadMu.Unlock()
	s, err := c.thread(id)
	if err != nil {
		return "", err
	}
	if s.Phase != phaseRunning && s.Phase != phasePaused {
		return "", fmt.Errorf("the thread is %s: its plan cannot change", s.Look())
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
		c.logThread(id, by, "Changement du plan demandé à "+c.conf().User+" : "+why)
		return fmt.Sprintf("The plan already changed %d times: %s is asked on a card. You are told of the answer.", s.Amends, c.conf().User), nil
	}
	if err := c.applyAmend(id, in, false); err != nil {
		return "", err
	}
	c.setThread(id, map[string]any{"amends": s.Amends + 1, "told": ""})
	c.logThread(id, by, "Plan modifié : "+why)
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
	threadMu.Lock()
	defer threadMu.Unlock()
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
	c.logThread(id, by, fmt.Sprintf("Choix pour « %s » : %s.", st.Title, st.Options[i]))
	c.advanceLocked(ctx, id)
	return c.step(id, key)
}

// stopThread stops a thread: its tasks stop, its cards are withdrawn.
func (c *core) stopThread(ctx context.Context, id int64, by string) error {
	threadMu.Lock()
	defer threadMu.Unlock()
	s, err := c.thread(id)
	if err != nil {
		return err
	}
	if !s.active() {
		return fmt.Errorf("the thread is %s already", s.State)
	}
	if !s.led() {
		c.endLight(ctx, s, threadDropped, "")
		c.logThread(id, by, "Thread abandonné.")
		return nil
	}
	steps, _ := c.steps(id)
	for _, st := range steps {
		if st.Status == stepRunning || st.Status == stepReported || st.Status == stepVerified {
			c.stopStepTask(ctx, st)
			c.setStep(id, st.Key, map[string]any{"status": stepBlocked, "note": "thread stopped", "ended": time.Now().Unix()})
		}
	}
	c.closeLocked(s, phaseStopped, "")
	c.logThread(id, by, "Thread arrêté.")
	return nil
}

// closeLight closes a light thread whose goal is reached: done, with how
// as its summary.
func (c *core) closeLight(id int64, summary, by string) error {
	threadMu.Lock()
	defer threadMu.Unlock()
	s, err := c.thread(id)
	if err != nil {
		return err
	}
	if s.led() {
		return errors.New("a led thread closes through its engine")
	}
	if !s.active() {
		return fmt.Errorf("the thread is %s already", s.State)
	}
	if summary = strings.TrimSpace(summary); summary == "" {
		return errors.New("a summary is needed: how its goal was reached. A thread closes only on its goal; to give up, stop it")
	}
	c.endLight(context.Background(), s, threadDone, summary)
	c.logThread(id, by, "Thread clos : "+summary)
	return nil
}

// endLight ends a light thread: done or dropped; what still waits or
// runs in it ends too.
func (c *core) endLight(ctx context.Context, s Thread, state, summary string) {
	c.endSteps(ctx, s) // thread_steps.go
	fields := map[string]any{"state": state, "ended": time.Now().Unix()}
	if summary != "" {
		fields["summary"] = truncate(summary, 2000)
	}
	c.setThread(s.ID, fields)
	c.dropThreadSurfaces(s.ID) // surface_authors.go
	c.emitLogged(journalEvent{Kind: evThreadEnded, Key: fmt.Sprintf("thread:%d", s.ID), Source: "thread",
		Payload: map[string]any{"thread": s.ID, "state": state}})
}

// reopenThread opens again a thread that ended, when its subject comes
// back: light, held by the brain, its steps and log kept. A led one's
// lead went with its end; its engine does not start again.
func (c *core) reopenThread(id int64, why, by string) (Thread, error) {
	threadMu.Lock()
	defer threadMu.Unlock()
	s, err := c.thread(id)
	if err != nil {
		return s, err
	}
	if s.active() {
		return s, fmt.Errorf("the thread is %s: it has not ended", s.State)
	}
	if why = oneLine(why); why == "" {
		return s, errors.New("why is needed: what brings its subject back")
	}
	c.endSteps(context.Background(), s) // what its old plan left undone never starts by itself
	c.setThread(id, map[string]any{"mode": modeLight, "phase": "", "state": threadOpen, "held": "", "lead": "", "told": "", "since": 0, "ended": 0})
	c.logThread(id, by, "Thread rouvert : "+why)
	return c.thread(id)
}

// closeLocked ends a led thread in phase state.
func (c *core) closeLocked(s Thread, state, summary string) {
	now := time.Now()
	fields := map[string]any{"phase": state, "ended": now.Unix(), "held": "", "spent": int64(s.elapsed(now) / time.Second), "since": 0}
	if summary != "" {
		fields["summary"] = summary
	}
	c.setThread(s.ID, fields)
	c.endSteps(context.Background(), s) // thread_steps.go
	c.dropLead(s)                       // thread_lead.go
	c.dropThreadSurfaces(s.ID)          // surface_authors.go
	c.emitLogged(journalEvent{Kind: evThreadEnded, Key: fmt.Sprintf("thread:%d", s.ID), Source: "thread",
		Payload: map[string]any{"thread": s.ID, "state": stateOf(state), "phase": state}})
}

// pauseThread stops starting steps; those running finish.
func (c *core) pauseThread(id int64, by string) error {
	threadMu.Lock()
	defer threadMu.Unlock()
	s, err := c.thread(id)
	if err != nil {
		return err
	}
	if s.Phase != phaseRunning {
		return fmt.Errorf("the thread is %s: only a running one pauses", s.Look())
	}
	c.pauseLocked(s, "")
	c.logThread(id, by, "Thread en pause.")
	return nil
}

func (c *core) pauseLocked(s Thread, held string) {
	now := time.Now()
	c.setThread(s.ID, map[string]any{"phase": phasePaused, "held": held, "spent": int64(s.elapsed(now) / time.Second), "since": 0})
}

// resumeThread runs a paused thread again. One the budget paused needs
// the user: their resume gives it more time.
func (c *core) resumeThread(ctx context.Context, id int64, by string) error {
	threadMu.Lock()
	defer threadMu.Unlock()
	s, err := c.thread(id)
	if err != nil {
		return err
	}
	if s.Phase != phasePaused {
		return fmt.Errorf("the thread is %s: only a paused one resumes", s.Look())
	}
	fields := map[string]any{"phase": phaseRunning, "held": "", "since": time.Now().Unix()}
	if s.Held == heldBudget {
		if by != byUser {
			return fmt.Errorf("its budget is spent: %s gives it more time, on its card or its page", c.conf().User)
		}
		fields["budget"] = spentMinutes(s) + budgetMore(s)
		c.store.db.Exec(`UPDATE thread_asks SET state = ?, decided = ? WHERE thread = ? AND kind = ? AND state = ?`, askApproved, time.Now().Unix(), id, askBudget, askPending)
	}
	c.setThread(id, fields)
	c.logThread(id, by, "Thread repris.")
	c.advanceLocked(ctx, id)
	return nil
}

const heldBudget = "budget"

// spentMinutes is the time a thread ran, in whole minutes: more time
// counts from there.
func spentMinutes(s Thread) int {
	return int((s.elapsed(time.Now()) + time.Minute - 1) / time.Minute)
}

// budgetMore is the time a budget's extension gives: half the budget, 30
// minutes at least.
func budgetMore(s Thread) int { return max(30, s.Budget/2) }

// holdForBudget pauses a thread past its budget and asks the user for
// more time; the steps running finish.
func (c *core) holdForBudget(s Thread) {
	c.pauseLocked(s, heldBudget)
	c.ask(s.ID, askBudget, "", "", map[string]int{"minutes": budgetMore(s)}, "engine")
	c.logThread(s.ID, "engine", fmt.Sprintf("Budget de %d min atteint : thread en pause, %s est interrogé sur une carte.", s.Budget, c.conf().User))
	text := fmt.Sprintf("Le budget de %d min est atteint : rien de nouveau ne démarre, les étapes en cours finissent. Une carte demande à %s s'il donne %d min de plus.",
		s.Budget, c.conf().User, budgetMore(s))
	c.tellBrain(s, text, nil)
	if c.hasLead(s) {
		c.queueLead(s, text, nil)
	}
}

// threadsTick is the engine's pass, every 15 seconds: the deadlines that
// came, the budgets, the steps whose task ended without being told
// (eriphe-core restarted), what may start, and the surface of an active
// thread that has none.
func (c *core) threadsTick(now time.Time) {
	c.threadDeadlines(now) // thread_steps.go
	c.keepThreadSurfaces() // thread_surface.go
	ss, err := c.threadsWhere(activeThreads + ` ORDER BY id DESC LIMIT 50`)
	if err != nil {
		return
	}
	ctx := context.Background()
	for _, s := range ss {
		if s.led() && s.Phase != phaseRunning {
			continue
		}
		steps, _ := c.steps(s.ID)
		for _, st := range steps {
			if st.Status != stepRunning || st.Task == "" {
				continue
			}
			if t, err := c.store.task(st.Task); err == nil && finished(t.State) {
				c.threadTaskChanged(t.ID, t.State, turnOutcome{})
			}
		}
		c.advanceThread(ctx, s.ID)
	}
}

// The cards.

// threadProposal reads what a thread asks, for its card.
func (c *core) threadProposal(p proposal) proposal {
	a, err := c.threadAsk(p.ID)
	if err != nil {
		return p
	}
	s, err := c.thread(a.Thread)
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
			return fmt.Sprintf(tr(lang, "thread.card.plan"), s.Title, strings.TrimRight(s.Goal, ". "), len(p.Fields), s.Budget)
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
		p.text = func(lang string) string { return fmt.Sprintf(tr(lang, "thread.card.amend"), s.Title) }
	case askBudget:
		var more struct{ Minutes int }
		json.Unmarshal(a.Payload, &more)
		p.text = func(lang string) string {
			return fmt.Sprintf(tr(lang, "thread.card.budget"), s.Title, s.Budget, more.Minutes)
		}
	case askApproval:
		st, _ := c.step(s.ID, a.Step)
		p.text = func(lang string) string { return fmt.Sprintf(tr(lang, "thread.card.approval"), s.Title, st.Question) }
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
		case stepApproval, stepUser:
			parts = append(parts, st.Question)
		case stepCall:
			parts = append(parts, callVerb(st.Tool, "fr"))
		case stepCheck:
			if st.Tool != "" {
				parts = append(parts, callVerb(st.Tool, "fr"))
			} else {
				parts = append(parts, orStr(st.Agent, "agent"))
			}
		case stepDeadline:
			parts = append(parts, orStr(st.Due, fmt.Sprintf("intention %d", st.Intention)))
		}
		if len(st.After) > 0 {
			parts = append(parts, "← "+strings.Join(st.After, ", "))
		}
		out = append(out, cardField{Name: fmt.Sprintf("%d. %s", len(out)+1, st.Title), Value: strings.Join(parts, " · ")})
	}
	return out
}

// decideThreadAsk applies the user's answer to what a thread asks.
func (c *core) decideThreadAsk(ctx context.Context, id int64, approve bool) error {
	threadMu.Lock()
	defer threadMu.Unlock()
	a, err := c.threadAsk(id)
	if err != nil {
		return err
	}
	s, err := c.thread(a.Thread)
	if err != nil {
		return err
	}
	state := askRejected
	if approve {
		state = askApproved
	}
	c.store.db.Exec(`UPDATE thread_asks SET state = ?, decided = ? WHERE id = ?`, state, time.Now().Unix(), id)
	c.keepThreadSurface(s.ID) // its card leaves the thread's surface
	user := c.conf().User
	switch a.Kind {
	case askPlan:
		if !approve {
			c.closeLocked(s, phaseDeclined, "")
			c.logThread(s.ID, "user", "Plan refusé.")
			c.tellBrain(s, user+" a refusé le plan : le thread est clos. Demande-lui ce qu'il faut changer avant d'en ouvrir un autre.", nil)
			return nil
		}
		now := time.Now().Unix()
		c.setThread(s.ID, map[string]any{"phase": phaseRunning, "started": now, "since": now})
		c.logThread(s.ID, "user", "Plan approuvé.")
		c.createLead(s) // thread_lead.go
		if s, err = c.thread(s.ID); err == nil && !c.hasLead(s) {
			c.tellBrain(s, user+" a approuvé le plan : les premières étapes démarrent. Tu seras prévenu de chaque étape.", nil)
		}
	case askAmend:
		var in amendIn
		json.Unmarshal(a.Payload, &in)
		if !approve {
			c.logThread(s.ID, "user", "Changement du plan refusé.")
			c.tellLead(s, user+" a refusé le changement du plan ("+a.Why+").", nil)
			return nil
		}
		if err := c.checkAmend(s.ID, &in); err != nil {
			c.tellLead(s, user+" a approuvé le changement du plan, mais il ne tient plus : "+err.Error(), nil)
			return nil
		}
		c.applyAmend(s.ID, in, true)
		c.setThread(s.ID, map[string]any{"told": ""})
		c.logThread(s.ID, "user", "Changement du plan approuvé.")
		c.tellLead(s, user+" a approuvé le changement du plan : ses étapes démarrent sans autre carte.", nil)
	case askBudget:
		if !approve {
			c.logThread(s.ID, "user", "Pas de temps en plus.")
			c.tellLead(s, user+" ne donne pas plus de temps : le thread reste en pause. Clos-le avec un résumé de ce qui est fait, ou arrête-le.", nil)
			return nil
		}
		var more struct{ Minutes int }
		json.Unmarshal(a.Payload, &more)
		c.setThread(s.ID, map[string]any{"phase": phaseRunning, "held": "", "budget": spentMinutes(s) + more.Minutes, "since": time.Now().Unix()})
		c.logThread(s.ID, "user", fmt.Sprintf("%d min de plus.", more.Minutes))
		c.tellLead(s, fmt.Sprintf("%s donne %d min de plus : le thread reprend.", user, more.Minutes), nil)
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
		c.logThread(s.ID, "user", fmt.Sprintf("« %s » : %s.", st.Title, word))
		c.tellLead(s, fmt.Sprintf("%s a répondu %s à « %s ».", user, word, st.Question), nil)
	}
	c.advanceLocked(ctx, s.ID)
	return nil
}
