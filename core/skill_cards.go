package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"regexp"
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
//   - With can: [start-session], its last message may carry a work session
//     to open, as a fenced ```session block of start_session's input. Its
//     plan starts at once when every step only reads, writes on a branch of
//     its repository or calls catalog tools (each call still under the
//     grid): the skill's approval stands for it. Otherwise the plan waits for
//     its card. The session is the run's: the events of its key go to its
//     lead while it is open.
//   - With urgent: <fixed text>, a last message that starts with [urgent]
//     sends that fixed text over the quiet hours and the daily cap, once per
//     run, as an urgent rule does: nothing the run read enters it.

const (
	cardRunCalls   = "runcalls"
	runCallsWait   = "Pas exécutée maintenant : eriphe-core la garde, telle quelle, pour une carte que l'utilisateur verra à la fin de ton tour ; s'il dit oui, elle sera exécutée. N'insiste pas et continue ; dis-le dans ton message final."
	urgentSkillWhy = "Ton skill « %s » est urgent : tu as approuvé ce message pour être prévenu à toute heure."
)

var sessionBlockRe = regexp.MustCompile("(?s)```session\\s*\\n(.*?)```")

func init() {
	extraColumns = append(extraColumns, `ALTER TABLE skill_runs ADD COLUMN session INTEGER NOT NULL DEFAULT 0`)
}

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

// sessionOfMessage takes a ```session block out of a run's last message.
func sessionOfMessage(text string) (*sessionIn, string, error) {
	m := sessionBlockRe.FindStringSubmatchIndex(text)
	if m == nil {
		return nil, text, nil
	}
	rest := strings.TrimSpace(text[:m[0]] + text[m[1]:])
	var in sessionIn
	if err := json.Unmarshal([]byte(text[m[2]:m[3]]), &in); err != nil {
		return nil, rest, fmt.Errorf("its session block is not start_session's JSON: %w", err)
	}
	return &in, rest, nil
}

// runSession opens the session a run asked for, if its skill may.
func (c *core) runSession(ctx context.Context, r *skillRun, tr *skillTrigger, in *sessionIn) string {
	if tr == nil || !containsStr(tr.Can, canSession) {
		return "a session was asked, but the skill may not open one (can: [start-session])"
	}
	var taint []string
	if why := c.taskResultTaint(r.Task); why != "" {
		taint = []string{why}
	}
	s, err := c.openSession(*in, taint, "skill:"+r.Skill)
	if err != nil {
		return "the session could not open: " + err.Error()
	}
	c.store.db.Exec(`UPDATE skill_runs SET session = ? WHERE id = ?`, s.ID, r.ID)
	if !safePlan(in.Steps) {
		return fmt.Sprintf("session %d opened; its plan waits for its card", s.ID)
	}
	var ask int64
	if c.store.db.QueryRow(`SELECT id FROM session_asks WHERE session = ? AND kind = ? AND state = ?`, s.ID, askPlan, askPending).Scan(&ask) == nil {
		if err := c.decideSessionAsk(ctx, ask, true); err != nil {
			return fmt.Sprintf("session %d opened; its plan waits for its card (%v)", s.ID, err)
		}
	}
	return fmt.Sprintf("session %d opened and started", s.ID)
}

// safePlan: every step only reads, writes on a branch of its repository or
// calls catalog tools (each call under the grid). A folder lent beyond
// read, a credential, a file: the plan waits for the user.
func safePlan(steps []stepSpec) bool {
	for _, st := range steps {
		for _, rr := range st.Resources {
			switch rr.Kind {
			case "repo":
				if rr.Access == "write" && rr.Branch == "" {
					return false
				}
			case "tool":
			case "folder", "drive-folder":
				if rr.Access != "" && rr.Access != "read" {
					return false
				}
			default:
				return false
			}
		}
	}
	return true
}

// runUrgent sends a skill's fixed urgent text, once per run.
func (c *core) runUrgent(r *skillRun, tr *skillTrigger) {
	if tr == nil || tr.Urgent == "" || c.store.get(fmt.Sprintf("skill_urgent:%d", r.ID)) != "" {
		return
	}
	c.store.set(fmt.Sprintf("skill_urgent:%d", r.ID), "1")
	n := &notification{Text: tr.Urgent, Why: fmt.Sprintf(urgentSkillWhy, r.Skill), Ref: "skill:" + r.Skill, Source: notifyFromUrgent}
	if _, err := c.notify(n, notifyItem{Level: levelNow, Ref: n.Ref, Asked: true, Urgent: true}); err != nil {
		log.Printf("skill %s: urgent: %v", r.Skill, err)
	}
}

// liveSession is the run's session while it is open.
func (c *core) liveSession(r *skillRun) (Session, bool) {
	var id int64
	c.store.db.QueryRow(`SELECT session FROM skill_runs WHERE id = ?`, r.ID).Scan(&id)
	if id == 0 {
		return Session{}, false
	}
	s, err := c.session(id)
	if err != nil {
		return Session{}, false
	}
	switch s.State {
	case sessProposed, sessRunning, sessPaused:
		return s, true
	}
	return Session{}, false
}

// toRunSession gives the events of a key to the lead of its run's session.
func (c *core) toRunSession(r *skillRun, s Session, queued []queuedEvent) {
	var events []journalEvent
	var taint []string
	for _, q := range queued {
		if e, ok := c.journalEvent(q.Event); ok {
			events = append(events, e)
			if e.External && len(taint) == 0 {
				taint = []string{"skill events: " + e.Kind}
			}
		}
	}
	c.tellLead(s, "De nouveaux événements sont arrivés pour cette session.\n"+eventsText(events), taint)
	c.dequeue(queued)
}

// skillCanText is what a run is told of what its skill allows.
func skillCanText(tr *skillTrigger) string {
	var b strings.Builder
	if containsStr(tr.Can, canSession) {
		b.WriteString("\n\nSi le travail demande plusieurs étapes (enquêter, corriger, ouvrir une PR), ouvre une session de travail : " +
			"termine ton message final par un bloc ```session contenant le JSON de start_session : " +
			`{"title", "goal", "done_when", "budget_minutes", "steps": [{"key", "title", "kind": "task", "after": [], "prompt", "agent", "resources": [{"kind": "repo", "ref": "owner/name", "access": "write", "branch": "eriphe/…"}]}]}` +
			". Une étape qui n'écrit que sur sa branche démarre seule ; la fusion et le déploiement restent à l'utilisateur.")
	}
	if tr.Urgent != "" {
		b.WriteString("\n\nSi c'est grave et que l'utilisateur doit le savoir tout de suite, même la nuit, commence ton message final par « [urgent] » : " +
			"eriphe-core lui enverra son message d'urgence, fixé à l'avance.")
	}
	return b.String()
}
