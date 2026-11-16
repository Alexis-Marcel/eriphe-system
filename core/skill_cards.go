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
//   - Lent eriphe-threads, it acts on threads with tools (task_threads.go),
//     never with its message.
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
	var id int64
	if c.store.db.QueryRow(`SELECT id FROM access_asks WHERE run = ? AND call LIKE ? AND input = ? AND state = ?`,
		r.ID, tool+" %", string(raw), startProposed).Scan(&id) != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		fields, _ := json.Marshal(cardFields(c.callContext(ctx, tool, input), input))
		cancel()
		n := c.callNeed(tool, input, c.pool.facts(tool, input))
		c.store.db.Exec(`INSERT INTO access_asks (task_id, permission_id, server, category, account, call, input, tainted, created, run)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, t.ID, pid, n.Server, n.Category, n.Account, tool+" "+string(fields), string(raw),
			c.store.get(taskTaintKey(t.ID)), time.Now().Unix(), r.ID)
	}
	c.store.addInbox(t.ID, "permission", map[string]any{"id": pid, "allow": false, "message": runCallsWait, "by": byPolicy})
	c.store.recordDecision(t.ID, pid, tool, input, decideDeny, byPolicy, "kept for the run's card")
	c.bump()
}

// runAsks are a run's kept calls.
func (c *core) runAsks(run int64) []accessQuestion {
	rows, err := c.store.db.Query(`SELECT id FROM access_asks WHERE run = ? ORDER BY id`, run)
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
	p.text = func(lang string) string { return fmt.Sprintf(tr(lang, "card.runcalls"), r.Skill, n) }
	p.fields = func(lang string) []cardField {
		var fs []cardField
		for _, a := range asks {
			var lines []string
			for _, f := range a.fields() {
				lines = append(lines, f.Name+" : "+f.Value)
			}
			fs = append(fs, cardField{Name: callVerb(a.Call, lang), Value: truncate(strings.Join(lines, "\n"), cardValueMax)})
		}
		return fs
	}
	p.Fields = p.fields("fr")
	return p
}

// runsWithCalls are the runs over whose kept calls still wait.
func (c *core) runsWithCalls() []int64 {
	rows, err := c.store.db.Query(`SELECT DISTINCT a.run FROM access_asks a JOIN skill_runs r ON r.id = a.run
		WHERE a.run != 0 AND a.state = ? AND r.state != ? ORDER BY a.run`, startProposed, skillRunOpen)
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
	n := &notification{Text: tr.Urgent, Why: fmt.Sprintf(urgentSkillWhy, r.Skill), Ref: "skill:" + r.Skill, Subject: c.sourceSubject("task:" + r.Task),
		Source: notifyFromUrgent}
	if _, err := c.notify(n, notifyItem{Weight: weightInterrupts, Ref: n.Ref, Asked: true, Urgent: true}); err != nil {
		log.Printf("skill %s: urgent: %v", r.Skill, err)
	}
}

// skillCanText is what a run is told of what its skill allows, and of
// its thread, if it has one (skillThread).
func skillCanText(tr *skillTrigger, th *Thread, sources []string) string {
	var b strings.Builder
	if containsStr(tr.Tools, threadsServer) {
		if th != nil {
			fmt.Fprintf(&b, "\n\nTon fil : le thread %d « %s » (%s). Tu agis dessus avec les outils de %s, sans redonner son id : "+
				"note chaque élément de ses sources (note_item), dis s'il attend l'utilisateur ou fixe une échéance (set_thread), fais-le remonter s'il le faut (set_thread raise), "+
				"ajoute ce que l'utilisateur doit faire ou décider (add_step : user, deadline, ou call : une action qu'il approuve sur sa carte). Tu ne le clos jamais.",
				th.ID, th.Title, th.Subject, threadsServer)
			if len(sources) > 0 {
				fmt.Fprintf(&b, " Ses sources qui listent des éléments : %s ; un élément s'y note par son id, celui que donnent ses événements.", strings.Join(sources, ", "))
			}
		} else {
			b.WriteString("\n\nTu agis sur les fils avec les outils de " + threadsServer + " : trouve celui d'un sujet (find_thread) avant d'en ouvrir un (open_thread).")
		}
	}
	if tr.Urgent != "" {
		b.WriteString("\n\nSi c'est grave et que l'utilisateur doit le savoir tout de suite, même la nuit, donne-lui le poids 5 dans ta liste attention : " +
			"eriphe-core lui enverra son message d'urgence, fixé à l'avance.")
	}
	if containsStr(tr.Tools, workspaceServer) {
		b.WriteString("\n\nTu tiens la surface de ce skill dans l'espace de travail de l'utilisateur (" + workspaceServer + ") : " +
			"retrouve la tienne avec workspace_read (yours), réécris-la avec surface_write et le même id, garde-la (lifetime kept) ; " +
			"catalog_read donne les composants avant la première. Un événement " + evSurfaceStale + " à son sujet demande de la réécrire.")
	}
	return b.String()
}
