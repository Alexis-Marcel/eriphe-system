package main

import (
	"context"
	"encoding/json"
	"errors"
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

// itemCall puts a call a run kept (call, the id its answer gave) on an
// item of its node (item_note's call): it acts on that item without
// naming it (an event made from a mail). Only a call of this very run,
// still waiting, on no other item; the item one its node's source lists.
// Its card then waits on the item's row, its words naming the item.
func (c *core) itemCall(task, node, source, item string, call int64) error {
	r := c.runOfTask(task)
	if r == nil {
		return errors.New("call: only a skill's run keeps calls for a card")
	}
	a, err := c.accessAskOf(call)
	if err != nil || a.Run != r.ID {
		return fmt.Errorf("call: %d is not a call this run kept", call)
	}
	if a.State != startProposed {
		return fmt.Errorf("call: %d was already decided", call)
	}
	if a.Item != "" && (a.Source != source || a.Item != item) {
		return fmt.Errorf("call: %d is on another item (%s)", call, a.Item)
	}
	label := c.listedItem(node, source, item)
	if label == nil {
		return fmt.Errorf("call: %s is not an item the source %s of your node lists", item, source)
	}
	label.Source = source
	raw, _ := json.Marshal([]itemLabel{*label})
	// On the card its other calls on that item are on, else its own.
	batch := a.Batch
	if b := c.runItemBatch(r.ID, node, source, "", item); b != 0 && b != a.Batch {
		batch = b
	}
	c.store.db.Exec(`UPDATE access_asks SET node = ?, source = ?, item = ?, labels = ?, batch = ? WHERE id = ?`, node, source, item, string(raw), batch, call)
	if batch != a.Batch && len(c.batchAsks(a.Batch)) == 0 {
		c.store.db.Exec(`DELETE FROM node_cards WHERE ref = ?`, proposalRef(cardCalls, a.Batch))
	}
	c.cardRaisedFor("", proposalRef(cardCalls, batch))
	return nil
}

// listedItem is the item a source of node lists now, by its id, as a
// card names it; nil when it lists none so named.
func (c *core) listedItem(node, source, item string) *itemLabel {
	var label *itemLabel
	for _, rd := range c.nodeReads(node) {
		if rd.Name != source || rd.Source.Items == nil {
			continue
		}
		for _, it := range sourceItems(rd.Name, rd.Source, rd.Value, 0) {
			if id, _ := it["id"].(string); id == item {
				l := itemLabel{ID: id}
				if t, _ := it["title"].(string); t != id {
					l.Title = t
				}
				l.Detail, _ = it["detail"].(string)
				label = &l
			}
		}
	}
	return label
}

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
