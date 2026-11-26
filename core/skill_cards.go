package main

import (
	"context"
	"fmt"
	"log"
	"strings"
)

// What a triggered skill's run does besides telling (skill_runs.go):
//
//   - Its calls the user must approve never hold it: as the brain's, each is
//     kept with its exact input (keepCall, demand.go: run = the run), the
//     task is told it did not run and goes on. When the run ends, they
//     wait on their cards (kind calls): on the rows of the items they act
//     on, else a block of its node. The run's note keeps their results;
//     once all ran, the brain is told how the yes went, the orb's words
//     (card_outcome.go).
//   - With urgent: <fixed text>, a run that weighs something 5 in its
//     attention list sends that fixed text over the quiet hours and the
//     daily cap, once per run, as an urgent rule does: nothing the run read
//     enters it. Its own message goes as any weight 5's, under the rules.

const urgentSkillWhy = "Ton skill « %s » est urgent : tu as approuvé ce message pour être prévenu à toute heure."

// askRunCall keeps a run's call for its card (keepCall) and answers the
// task: not run.
func (c *core) askRunCall(t *TaskRow, r *skillRun, pid, tool string, input map[string]any) {
	if c.store.answered(t.ID, pid) {
		return
	}
	k := keeper{task: t.ID, pid: pid, run: r.ID, node: c.taskNode(t.ID), taint: c.store.get(taskTaintKey(t.ID))}
	a := c.keepCall(context.Background(), k, tool, input)
	c.store.addInbox(t.ID, "permission", map[string]any{"id": pid, "allow": false, "message": c.keptSay(k, a), "by": byPolicy})
	c.store.recordDecision(t.ID, pid, tool, input, decideDeny, byPolicy, "kept for its card")
	c.bump()
}

func (c *core) runByID(id int64) *skillRun {
	r, err := scanSkillRun(c.store.db.QueryRow(`SELECT `+skillRunCols+` FROM skill_runs WHERE id = ?`, id))
	if err != nil {
		return nil
	}
	return r
}

// runCallDone keeps in the run's note how a call went, once decided.
func (c *core) runCallDone(a accessQuestion, text string) {
	c.store.db.Exec(`UPDATE skill_runs SET note = substr(note || ? , 1, ?) WHERE id = ?`, "\n· "+truncate(text, 400), skillNoteMax, a.Run)
	log.Printf("skill run %d: card %d: %s", a.Run, a.ID, truncate(text, 120))
}

// runUrgent sends a skill's fixed urgent text, once per run.
func (c *core) runUrgent(r *skillRun, tr *skillTrigger) {
	if tr == nil || tr.Urgent == "" || c.store.get(fmt.Sprintf("skill_urgent:%d", r.ID)) != "" {
		return
	}
	c.store.set(fmt.Sprintf("skill_urgent:%d", r.ID), "1")
	n := &notification{Text: tr.Urgent, Why: fmt.Sprintf(urgentSkillWhy, r.Skill), Ref: "skill:" + r.Skill, About: c.sourceAbout("task:" + r.Task),
		Source: notifyFromUrgent}
	if _, err := c.notify(n, notifyItem{Weight: weightInterrupts, Ref: n.Ref, Asked: true, Urgent: true}); err != nil {
		log.Printf("skill %s: urgent: %v", r.Skill, err)
	}
}

// skillCanText is what a run is told of what its skill allows.
func skillCanText(tr *skillTrigger) string {
	var b strings.Builder
	if tr.Urgent != "" {
		b.WriteString("\n\nSi c'est grave et que l'utilisateur doit le savoir tout de suite, même la nuit, donne-lui le poids 5 dans ta liste attention : " +
			"eriphe-core lui enverra son message d'urgence, fixé à l'avance.")
	}
	return b.String()
}
