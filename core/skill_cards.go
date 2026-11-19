package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"
)

// What a triggered skill's run does besides telling (skill_runs.go):
//
//   - Its calls the user must approve never hold it: as the brain's, each is
//     kept with its exact input (access_asks, run = the run), the task is
//     told it did not run and goes on. When the run ends, they show on one
//     card (kind runcalls), with its message: each action and its
//     parameters; the yes runs them all as kept, the no none. The brain is
//     not told of their results: the run's note keeps them.
//   - With urgent: <fixed text>, a run that weighs something 5 in its
//     attention list sends that fixed text over the quiet hours and the
//     daily cap, once per run, as an urgent rule does: nothing the run read
//     enters it. Its own message goes as any weight 5's, under the rules.

const (
	cardRunCalls   = "runcalls"
	runCallsWait   = "Pas exécutée maintenant : eriphe-core la garde, telle quelle, pour une carte que l'utilisateur verra à la fin de ton tour ; s'il dit oui, elle sera exécutée. N'insiste pas et continue ; dis-le dans ton message final."
	urgentSkillWhy = "Ton skill « %s » est urgent : tu as approuvé ce message pour être prévenu à toute heure."
)

// askRunCall keeps a run's call for its card and answers the task: not run.
func (c *core) askRunCall(t *TaskRow, r *skillRun, pid, tool string, input map[string]any) {
	if c.store.answered(t.ID, pid) {
		return
	}
	raw, _ := json.Marshal(input)
	// Its node: the item's when it acts on one of its node's items, else
	// its run's node (item_cards.go).
	node := c.taskNode(t.ID)
	source, item, onItem := c.callItem(node, tool, input)
	var id int64
	switch {
	case onItem && c.itemAskWaiting(node, source, item, tool, string(raw)) != 0:
		// The same call on the same item waits already: its card.
	case !onItem && c.store.db.QueryRow(`SELECT id FROM access_asks WHERE run = ? AND batch = 0 AND call LIKE ? AND input = ? AND state = ?`,
		r.ID, tool+" %", string(raw), startProposed).Scan(&id) == nil:
	default:
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		fields, _ := json.Marshal(cardFields(c.callContext(ctx, tool, input), input))
		cancel()
		n := c.callNeed(tool, input, c.pool.facts(tool, input))
		labels := ""
		if ls := c.callItemLabels(node, tool, input); len(ls) > 0 {
			b, _ := json.Marshal(ls)
			labels = string(b)
		}
		res, err := c.store.db.Exec(`INSERT INTO access_asks (task_id, permission_id, server, category, account, call, input, tainted, created, run, node, source, item, labels)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, t.ID, pid, n.Server, n.Category, n.Account, tool+" "+string(fields), string(raw),
			c.store.get(taskTaintKey(t.ID)), time.Now().Unix(), r.ID, node, source, item, labels)
		if id, _ := rowsID(res, err); onItem && id > 0 {
			// One card per item: the one its other calls on it are on.
			batch := c.runItemBatch(r.ID, node, source, item)
			if batch == 0 {
				batch = id
			}
			c.store.db.Exec(`UPDATE access_asks SET batch = ? WHERE id = ?`, batch, id)
		}
	}
	c.store.addInbox(t.ID, "permission", map[string]any{"id": pid, "allow": false, "message": runCallsWait, "by": byPolicy})
	c.store.recordDecision(t.ID, pid, tool, input, decideDeny, byPolicy, "kept for the run's card")
	c.bump()
}

// runAsks are a run's kept calls on no item: those on one are its item's
// card (item_cards.go).
func (c *core) runAsks(run int64) []accessQuestion {
	rows, err := c.store.db.Query(`SELECT id FROM access_asks WHERE run = ? AND batch = 0 ORDER BY id`, run)
	if err != nil {
		return nil
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	var out []accessQuestion
	for _, id := range ids {
		if a, err := c.accessAskOf(id); err == nil {
			out = append(out, a)
		}
	}
	return out
}

func (c *core) runByID(id int64) *skillRun {
	r, err := scanSkillRun(c.store.db.QueryRow(`SELECT `+skillRunCols+` FROM skill_runs WHERE id = ?`, id))
	if err != nil {
		return nil
	}
	return r
}

// runCallsProposal is a run's card: pending while one of its calls waits,
// once the run is over.
func (c *core) runCallsProposal(p proposal) proposal {
	r := c.runByID(p.ID)
	if r == nil {
		return p
	}
	asks := c.runAsks(r.ID)
	approved := false
	for _, a := range asks {
		switch a.State {
		case startProposed:
			p.Pending = r.State != skillRunOpen
		case startApproved:
			approved = true
		}
	}
	p.Outcome = cardRejected
	if approved {
		p.Outcome = cardApproved
	}
	if r.External {
		p.Tainted = c.store.get(taskTaintKey(r.Task))
	}
	p.Why = "Skill « " + r.Skill + " »"
	n := len(asks)
	// Its words read plainly (call_words.go): one call is its sentence,
	// several the skill's; the exact calls under Détails.
	p.text = func(lang string) string {
		if n == 1 {
			text, _ := c.callsWords(asks, lang)
			return text
		}
		return fmt.Sprintf(tr(lang, "card.runcalls"), r.Skill, n)
	}
	p.summary = func(lang string) []string {
		_, lines := c.callsWords(asks, lang)
		return lines
	}
	var fs []cardField
	for _, a := range asks {
		fs = append(fs, callDetail(a))
	}
	p.Fields = fs
	return p
}

// runsWithCalls are the runs over whose kept calls still wait.
func (c *core) runsWithCalls() []int64 {
	rows, err := c.store.db.Query(`SELECT DISTINCT a.run FROM access_asks a JOIN skill_runs r ON r.id = a.run
		WHERE a.run != 0 AND a.batch = 0 AND a.state = ? AND r.state != ? ORDER BY a.run`, startProposed, skillRunOpen)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			out = append(out, id)
		}
	}
	return out
}

// decideRunCalls answers every call of the run still waiting.
func (c *core) decideRunCalls(ctx context.Context, run int64, approve bool) error {
	for _, a := range c.runAsks(run) {
		if a.State != startProposed {
			continue
		}
		if err := c.decideAccess(ctx, a.ID, approve); err != nil && err != errCardDecided {
			return err
		}
	}
	return nil
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
