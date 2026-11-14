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
// task's remember and the nightly extraction write it there; a subject
// with no thread yet gets a light one, opened by the code, no model. A
// subject whose thread ended is not reopened behind the user's back: the
// brain reopens it (reopen_thread) when the subject comes back.
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

// noteProgress writes where a subject stands as its thread's summary: the
// live thread's, else a light thread the code opens for it (one already
// ended, if done). A subject whose thread ended is refused: reopen it
// first. It returns the thread and a note for the model.
func (c *core) noteProgress(subject, text, state, by string) (int64, string, error) {
	key, err := c.subjectIs(subject, "")
	if err != nil {
		return 0, "", fmt.Errorf("subject: %w", err)
	}
	if key == "" {
		return 0, "", errors.New("a progress needs its subject (kind:slug): it is kept as the summary of that subject's thread")
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return 0, "", errors.New("text is required")
	}
	state = strings.ToLower(strings.TrimSpace(state))
	if state == "" {
		state = progressActive
	}
	if !containsStr(progressStates, state) {
		return 0, "", errors.New("the state of a progress is active, blocked, done or paused")
	}
	if scan := scanMemoryText(text, key); scan.Reject != "" {
		return 0, "", fmt.Errorf("refused: %s", scan.Reject)
	}
	summary := truncate(progressSummary(text, state), 2000)
	threadMu.Lock()
	defer threadMu.Unlock()
	if id, ok := c.liveThreadOf(key); ok {
		c.setThread(id, map[string]any{"summary": summary})
		c.logThread(id, by, "Résumé mis à jour.")
		note := fmt.Sprintf("Kept as the summary of thread %d, the one of %s.", id, key)
		if state == progressDone {
			note += " If its goal is reached, close it with close_thread."
		}
		return id, note, nil
	}
	if old, err := c.threadsWhere(`subject = ? ORDER BY id DESC LIMIT 1`, key); err == nil && len(old) == 1 {
		return 0, "", fmt.Errorf("thread %d « %s » followed %s and is %s: reopen_thread it if the subject came back, then note where it stands", old[0].ID, old[0].Title, key, old[0].State)
	}
	id, err := c.openProgressThread(key, summary, state == progressDone, time.Now())
	if err != nil {
		return 0, "", err
	}
	if state == progressDone {
		return id, fmt.Sprintf("Kept as the summary of thread %d, opened for %s and closed done.", id, key), nil
	}
	c.keepThreadSurface(id) // thread_surface.go
	return id, fmt.Sprintf("Kept as the summary of thread %d, a light thread opened for %s: you hold it.", id, key), nil
}

// openProgressThread opens a light thread for a subject that had none,
// with where it stands as its summary; done, it is closed at once. Only
// the key names it: a subject's title may be someone else's words.
func (c *core) openProgressThread(key, summary string, done bool, at time.Time) (int64, error) {
	state, ended := threadOpen, int64(0)
	if done {
		state, ended = threadDone, at.Unix()
	}
	res, err := c.store.db.Exec(`INSERT INTO threads (title, goal, mode, subject, state, summary, created, started, ended) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		truncate("Suivi de "+key, taskTitleMax), "Suivre où en est "+key+".", modeLight, key, state, summary, at.Unix(), at.Unix(), ended)
	if err != nil {
		return 0, c.liveSubjectErr(key, err)
	}
	id, _ := res.LastInsertId()
	c.logThread(id, "engine", "Thread ouvert par eriphe-core : l'avancement de "+key+" y est tenu.")
	return id, nil
}

// brainProgress is remember's kind progress: the thread's summary, never
// from a turn that read external content.
func (c *core) brainProgress(in rememberIn) (map[string]any, error) {
	if taint := c.turnTaint(); len(taint) > 0 {
		return nil, errProgressTainted
	}
	subject := in.Subject
	if subject == "" {
		subject = c.turnSubject() // the turn's thread, or the subject my message named
	}
	id, note, err := c.noteProgress(subject, in.Text, in.State, byBrain)
	if err != nil {
		return nil, err
	}
	c.turnThread(id)
	return map[string]any{"thread": id, "note": note}, nil
}

// applyProgress is an extraction's progress: its subject's thread summary,
// never from external content nor from an unclear reading.
func (c *core) applyProgress(op extractionOp, trust, ref, who string) (why, result string) {
	if trust == trustExternal {
		return "a progress drawn from external content is not kept: a thread's summary is quoted in its subject's cards", ""
	}
	if strings.EqualFold(strings.TrimSpace(op.Basis), basisAmbiguous) {
		return "an ambiguous progress is not kept", ""
	}
	if _, _, err := c.noteProgress(c.opSubject(op, ref), op.Text, op.State, who); err != nil {
		return err.Error(), ""
	}
	return "", "added"
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
	if _, err := c.linkIntentionLocked(ctx, id, x, "engine"); err != nil {
		log.Printf("thread %d: intention %d: %v", id, x.ID, err)
	}
}

// progressMoved is the kv key that says the progress memories were
// rewritten into their subjects' threads.
const progressMoved = "progress_to_threads"

// progressToThreads rewrites, once, the progress memories into their
// subjects' threads: the latest current one of each subject becomes the
// summary of its live thread, or of a light thread opened for it (closed
// done when it says done; a subject whose thread ended keeps that one's).
// Those are forgotten (purged later); any other progress row (a replaced
// one, a proposed one, one without subject) becomes a fact. The threads
// are written straight: no event, no surface, no close decision.
func (c *core) progressToThreads() error {
	if c.mem == nil || c.store.get(progressMoved) != "" {
		return nil
	}
	now := c.mem.now()
	xs, err := c.mem.list(`kind = ? AND status = 'active' AND superseded_by IS NULL AND (valid_to IS NULL OR valid_to > ?) AND subject != ''`,
		[]any{kindProgress, now.Unix()}, `recorded_at DESC, id DESC`, 10000)
	if err != nil {
		return err
	}
	done := map[string]bool{}
	var moved []int64
	for _, x := range xs {
		key := c.store.keepSubject(x.Subject, "")
		if done[key] {
			moved = append(moved, x.ID) // an older card of the same subject
			continue
		}
		done[key] = true
		summary := truncate(progressSummary(x.Text, x.State), 2000)
		if x.Trust == trustExternal {
			continue // never quoted: it stays, a fact
		}
		if id, ok := c.liveThreadOf(key); ok {
			if _, err := c.store.db.Exec(`UPDATE threads SET summary = ? WHERE id = ?`, summary, id); err != nil {
				return err
			}
		} else if old, err := c.threadsWhere(`subject = ? LIMIT 1`, key); err != nil {
			return err
		} else if len(old) == 0 {
			if _, err := c.openProgressThread(key, summary, x.State == progressDone, x.RecordedAt); err != nil {
				return err
			}
		}
		moved = append(moved, x.ID)
	}
	at := now.Unix()
	for _, id := range moved {
		if _, err := c.mem.db.Exec(`UPDATE memories SET status = 'deleted', pinned = 0, deleted_at = ?, updated_at = ? WHERE id = ?`, at, at, id); err != nil {
			return err
		}
	}
	if _, err := c.mem.db.Exec(`UPDATE memories SET kind = ?, state = '' WHERE kind = ?`, kindFact, kindProgress); err != nil {
		return err
	}
	if _, err := c.mem.db.Exec(`UPDATE learning_log SET kind = ? WHERE kind = ?`, kindFact, kindProgress); err != nil {
		return err
	}
	if len(xs) > 0 {
		log.Printf("memory: %d progress notes moved into their subjects' threads", len(moved))
	}
	c.store.set(progressMoved, "1")
	return nil
}
