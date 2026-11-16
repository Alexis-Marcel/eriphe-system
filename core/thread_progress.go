package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"
)

// The thread holds its subject's state (#512, step 6). Where a subject
// stands is never a memory: it is the summary of the subject's thread, one
// text, written over as the subject moves. remember (kind progress), a
// task's remember and the nightly extraction write it there. Code never
// opens a thread for it: with no live thread, a progress is refused
// ("open_thread first"), and the extraction drops it. A thread that ended
// is not reopened behind the user's back: the brain reopens it
// (reopen_thread) when the subject comes back.
//
// What the code links on its own: an open intention of the thread's
// subject becomes its deadline step (followIntentions), a card born in a
// turn about the thread shows in its timeline (cardProposed). Closing a
// thread on its goal writes a dated decision memory from its summary
// (closeDecision). link_thread is left for the exceptions: what belongs to
// a thread of another subject.

// kindProgress is what remember calls where a subject stands: written as
// its thread's summary, never kept as a memory.
const kindProgress = "progress"

// Where a subject stands, as remember says it.
const (
	progressActive  = "active"
	progressBlocked = "blocked"
	progressDone    = "done"
	progressPaused  = "paused"
)

var progressStates = []string{progressActive, progressBlocked, progressDone, progressPaused}

// progressSummary is a thread's summary from a progress note: its state
// said first when it is not under way.
func progressSummary(text, state string) string {
	switch state {
	case progressBlocked:
		return "Bloqué. " + text
	case progressPaused:
		return "En pause. " + text
	case progressDone:
		return "Terminé. " + text
	}
	return text
}

// errProgressTainted refuses a progress note drawn from someone else's
// words: a thread's summary is quoted in every card of its subject.
var errProgressTainted = errors.New("not kept: where a subject stands is its thread's summary, quoted to you in every card of the subject, so it is never drawn from external content; write it from the user's words or your own, or tell the user")

// errNoProgressThread refuses a progress with no thread: code never opens
// one for it.
var errNoProgressThread = errors.New("where a matter stands is its thread's summary, and none follows it: open_thread first (it gives back the one that exists), then note where it stands")

// progressThread is the thread a progress is written on: the live thread
// of its subject, else the one given (the turn's, the task's step's). An
// ended thread is refused (reopen it first), and so is none at all: code
// never opens a thread for a progress.
func (c *core) progressThread(subject string, given int64) (int64, error) {
	if subject != "" {
		key, err := c.subjectLookup(subject)
		if err != nil {
			return 0, fmt.Errorf("subject: %w", err)
		}
		if id, ok := c.liveThreadOf(key); ok {
			return id, nil
		}
		if old, err := c.threadsWhere(`subject = ? ORDER BY id DESC LIMIT 1`, key); err == nil && len(old) == 1 {
			return 0, fmt.Errorf("thread %d « %s » followed %s and is %s: reopen_thread it if the matter came back, then note where it stands", old[0].ID, old[0].Title, key, old[0].State)
		}
		return 0, errNoProgressThread
	}
	if given <= 0 {
		return 0, errNoProgressThread
	}
	s, err := c.thread(given)
	if err != nil {
		return 0, errNoProgressThread
	}
	if !s.active() {
		return 0, fmt.Errorf("thread %d « %s » is %s: reopen_thread it if the matter came back, then note where it stands", s.ID, s.Title, s.State)
	}
	return s.ID, nil
}

// noteProgress writes where a matter stands as its thread's summary. It
// returns a note for the model.
func (c *core) noteProgress(id int64, text, state, by string) (string, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", errors.New("text is required")
	}
	state = strings.ToLower(strings.TrimSpace(state))
	if state == "" {
		state = progressActive
	}
	if !containsStr(progressStates, state) {
		return "", errors.New("the state of a progress is active, blocked, done or paused")
	}
	s, err := c.thread(id)
	if err != nil {
		return "", err
	}
	if scan := scanMemoryText(text, s.Subject); scan.Reject != "" {
		return "", fmt.Errorf("refused: %s", scan.Reject)
	}
	threadMu.Lock()
	defer threadMu.Unlock()
	c.setThread(id, map[string]any{"summary": truncate(progressSummary(text, state), 2000)})
	c.logThread(id, by, "Résumé mis à jour.")
	note := fmt.Sprintf("Kept as the summary of thread %d « %s ».", id, s.Title)
	if state == progressDone {
		note += " If its goal is reached, close it with close_thread."
	}
	return note, nil
}

// brainProgress is remember's kind progress: the summary of its subject's
// thread, else of the turn's; from a turn that read external content only
// when that summary is never quoted anyway (threadExternal).
func (c *core) brainProgress(in rememberIn) (map[string]any, error) {
	id, err := c.progressThread(in.Subject, c.turn.aboutThread())
	if err != nil {
		return nil, err
	}
	if taint := c.turnTaint(); len(taint) > 0 && !c.summaryUnquoted(id) {
		return nil, errProgressTainted
	}
	note, err := c.noteProgress(id, in.Text, in.State, byBrain)
	if err != nil {
		return nil, err
	}
	c.turnThread(id)
	return map[string]any{"thread": id, "note": note}, nil
}

// applyProgress is an extraction's progress: its subject's thread summary,
// never from external content nor from an unclear reading; with no live
// thread, dropped.
func (c *core) applyProgress(op extractionOp, trust, ref string) (why, result string) {
	if trust == trustExternal {
		return "a progress drawn from external content is not kept: a thread's summary is quoted in its subject's cards", ""
	}
	if strings.EqualFold(strings.TrimSpace(op.Basis), basisAmbiguous) {
		return "an ambiguous progress is not kept", ""
	}
	id, err := c.progressThread(c.opSubject(op, ref), c.refThread(ref))
	if err != nil {
		return err.Error(), ""
	}
	if _, err := c.noteProgress(id, op.Text, op.State, "extraction"); err != nil {
		return err.Error(), ""
	}
	return "", "added"
}

// refThread is the thread of the first task step a source ref names
// (task:<id>, comma separated); 0 if none.
func (c *core) refThread(ref string) int64 {
	for _, p := range strings.Split(ref, ",") {
		if id, ok := strings.CutPrefix(strings.TrimSpace(p), "task:"); ok {
			if st, ok := c.stepOfTask(id); ok {
				return st.Thread
			}
		}
	}
	return 0
}

// extractionThreads are the live threads the extraction is shown, with
// their summary: where each subject stands, what its progress replaces. A
// thread whose words may be someone else's is listed, its summary not.
func (c *core) extractionThreads() string {
	ts, err := c.threadsWhere(activeThreads + ` AND subject != '' ORDER BY id DESC LIMIT 30`)
	if err != nil || len(ts) == 0 {
		return "(none)\n"
	}
	var b strings.Builder
	for _, s := range ts {
		sum := "(no summary yet)"
		switch {
		case c.threadExternal(s):
			sum = "(summary not shown: it may carry someone else's words)"
		case strings.TrimSpace(s.Summary) != "":
			sum = oneLine(truncate(s.Summary, 600))
		}
		fmt.Fprintf(&b, "- thread=%d subject=%q state=%s: %s\n", s.ID, s.Subject, s.State, sum)
	}
	return b.String()
}

// summaryUnquoted tells whether a thread's summary is never quoted
// (threadExternal): its steps read external content already, so a wake it
// tainted may still say where it stands.
func (c *core) summaryUnquoted(id int64) bool {
	s, err := c.thread(id)
	return err == nil && c.threadExternal(s)
}

// threadExternal tells whether what a thread says may carry what someone
// else wrote: its plan, or what its steps read.
func (c *core) threadExternal(s Thread) bool {
	if s.Tainted != "" {
		return true
	}
	steps, _ := c.steps(s.ID)
	for _, st := range steps {
		if st.Tainted != "" || (st.Task != "" && c.taskResultTaint(st.Task) != "") {
			return true
		}
	}
	return false
}

// srcThread is a memory a thread wrote: the decision of its close.
const srcThread = "thread"

// closeDecision writes, when a thread closes on its goal, a dated decision
// memory of its subject from its summary; one it wrote at an earlier close
// is superseded. A thread whose words may be someone else's writes it
// external: named, never quoted.
func (c *core) closeDecision(id int64) {
	if c.mem == nil {
		return
	}
	s, err := c.thread(id)
	if err != nil || strings.TrimSpace(s.Summary) == "" {
		return
	}
	text := truncate(fmt.Sprintf("Fil « %s » clos le %s : %s", s.Title, time.Now().Format("2006-01-02"), oneLine(s.Summary)), 600)
	scan := scanMemoryText(text, s.Subject)
	if scan.Reject != "" {
		return
	}
	ref := threadRef(id)
	x := &Memory{Kind: kindDecision, Subject: s.Subject, Text: text, SourceKind: srcThread, SourceRef: ref,
		Trust: trustFirstParty, Status: memActive, Flags: strings.Join(scan.Flags, ",")}
	if c.threadExternal(s) {
		x.Trust = trustExternal
	}
	if old, _ := c.mem.list(`status = 'active' AND kind = ? AND source_ref = ? AND superseded_by IS NULL`, []any{kindDecision, ref}, `id DESC`, 1); len(old) == 1 {
		x.Supersedes = old[0].ID
	}
	if err := c.mem.add(x, "thread"); err != nil {
		log.Printf("thread %d: its decision: %v", id, err)
	}
}

// intentionStepKey is the key of the deadline step an intention becomes.
func intentionStepKey(id int64) string { return fmt.Sprintf("intention-%d", id) }

// linkIntentionLocked makes an intention a deadline step of a thread, due
// when it is; threadMu is held.
func (c *core) linkIntentionLocked(ctx context.Context, id int64, it *Intention, by string) (stepSpec, error) {
	sp := stepSpec{Key: intentionStepKey(it.ID), Title: truncate(oneLine(it.Text), taskTitleMax), Kind: stepDeadline, Intention: it.ID}
	have, _ := c.steps(id)
	specs := []stepSpec{sp}
	if err := checkPlan(have, specs); err != nil {
		return sp, err
	}
	if err := c.insertSteps(id, specs, false); err != nil {
		return sp, err
	}
	if it.Trust == trustExternal {
		c.setStep(id, sp.Key, map[string]any{"tainted": taintIntention}) // its words are someone else's
	}
	c.logThread(id, by, fmt.Sprintf("Intention %d liée : « %s ».", it.ID, sp.Title))
	c.advanceLocked(ctx, id)
	return sp, nil
}

// followIntentions makes each open intention whose subject has a live
// light thread a deadline step of it, once: a step it was (even dropped)
// is never added again. It runs with the engine's pass.
func (c *core) followIntentions(ctx context.Context) {
	if c.mem == nil {
		return
	}
	xs, err := c.mem.intentions(intentOpen, intentSnoozed)
	if err != nil {
		return
	}
	for _, x := range xs {
		key := c.store.canonSubject(subjectOf(x.Subject))
		if key == "" {
			continue
		}
		id, ok := c.liveThreadOf(key)
		if !ok {
			continue
		}
		c.followIntention(ctx, id, x)
	}
}

func (c *core) followIntention(ctx context.Context, id int64, x *Intention) {
	threadMu.Lock()
	defer threadMu.Unlock()
	s, err := c.thread(id)
	if err != nil || s.led() || !s.active() {
		return // a led thread's plan is the user's
	}
	if _, err := c.step(id, intentionStepKey(x.ID)); err == nil {
		return
	}
	c.linkIntentionLocked(ctx, id, x, "engine") // refused (its limits): tried again at the next pass, quietly
}
