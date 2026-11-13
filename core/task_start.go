package main

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"
)

// A task's crossings, decided at launch. What a task lent by run_task needs
// of the user's (a write to a repository not lent outright by a clean start)
// is foreseen with the same decision as the push itself (decideRepo), for
// the case its lend names (its branch, or the default branch),
// and asked once, when the task starts. The task waits meanwhile: it is kept,
// pending, with desired "held", which no node sees and no placement moves.
// The user gets one card in the conversation (convo_cards.go, kind
// taskstart): Oui records the grants (repo_grants, access_grants), and
// lets the task go; Non stops it and tells the brain. As every card, the
// brain cannot decide it.

const taskStartSchema = `CREATE TABLE IF NOT EXISTS task_starts (
	id      INTEGER PRIMARY KEY AUTOINCREMENT,
	task_id TEXT NOT NULL UNIQUE,
	needs   TEXT NOT NULL,
	tainted TEXT NOT NULL DEFAULT '',
	state   TEXT NOT NULL DEFAULT 'proposed',
	created INTEGER NOT NULL,
	decided INTEGER NOT NULL DEFAULT 0
);`

func init() { extraSchema = append(extraSchema, taskStartSchema) }

const (
	cardTaskStart = "taskstart"

	// desiredHeld: the task waits for the user's answer before it starts.
	desiredHeld = "held"

	startProposed = "proposed"
	startApproved = "approved"
	startRejected = "rejected"
)

// startNeed is one thing a task will need of the user's: a write to a
// repository, and why it is theirs.
type startNeed struct {
	Repo   string `json:"repo,omitempty"`
	Access string `json:"access,omitempty"`
	// Server, Category and Account: what the grid asks of a tool lent
	// outright, in the place of a repository (access_card.go).
	Server   string `json:"server,omitempty"`
	Category string `json:"category,omitempty"`
	Account  string `json:"account,omitempty"`
	// Folder: a folder lent to write in, Access its level, drop or manage
	// (folder_values.go).
	Folder string `json:"folder,omitempty"`
	Branch string `json:"branch,omitempty"`
	Reason string `json:"reason"` // the policy's, in English, for the log
}

// startNeeds foresees what the lent repositories will need of the user's:
// for each one lent for write, the decision its push will get.
func (c *core) startNeeds(list []ResourceRef, clean bool) []startNeed {
	repos := lentRepos(list)
	if !c.gitConnected() {
		return nil // git is not connected: a yes would give nothing
	}
	var out []startNeed
	for _, g := range repos {
		if g.Access != accessWrite {
			continue
		}
		// What the lend says it does: its work branch is Modifier, the
		// default branch (no branch given) Envoyer. Supprimer is not asked
		// ahead: a deletion goes only where it is free.
		cat := catChange
		if g.Branch == "" {
			cat = catSend
		}
		if d := decideRepo(g.Repo, cat, repoFacts{Repos: repos, Clean: clean, Case: c.repoCase}); d.Decision == decideAsk {
			out = append(out, startNeed{Repo: g.Repo, Access: accessWrite, Category: cat, Branch: g.Branch, Reason: d.Reason})
		}
	}
	return out
}

// holdTask keeps the start of a task for the user's answer. Started by the
// brain, its card goes with the brain's next message of this turn; started
// by anyone else (a thread's step), no turn will carry it: it is shown at
// once, with a message of its own (cardNow).
func (c *core) holdTask(t *TaskRow, needs []startNeed, tainted []string, byBrain bool) error {
	raw, _ := json.Marshal(needs)
	res, err := c.store.db.Exec(`INSERT INTO task_starts (task_id, needs, tainted, created) VALUES (?, ?, ?, ?)`,
		t.ID, string(raw), strings.Join(tainted, ", "), time.Now().Unix())
	if err != nil {
		return err
	}
	id, _ := res.LastInsertId()
	if byBrain {
		c.cardProposed(cardTaskStart, id)
		c.cardRaisedFor(c.turnSource(), proposalRef(cardTaskStart, id)) // in a turn about a thread (thread_turn.go)
	} else {
		c.cardNow(cardTaskStart, id, fmt.Sprintf(tr(c.userLang(), "taskstart.held.message"), t.label()), "task:"+t.ID)
	}
	log.Printf("task %s held for the user's approval: %s", t.ID, needsLog(needs))
	return nil
}

func needsLog(needs []startNeed) string {
	var parts []string
	for _, n := range needs {
		parts = append(parts, strings.TrimSpace(n.Access+" "+n.Repo+n.Server+n.Folder+" "+n.Account)+" ("+n.Reason+")")
	}
	return strings.Join(parts, "; ")
}

// taskStart is a held start as the store keeps it.
type taskStart struct {
	ID      int64
	TaskID  string
	Needs   []startNeed
	Tainted string
	State   string
}

func (c *core) taskStart(id int64) (taskStart, error) {
	var s taskStart
	var needs string
	err := c.store.db.QueryRow(`SELECT id, task_id, needs, tainted, state FROM task_starts WHERE id = ?`, id).
		Scan(&s.ID, &s.TaskID, &needs, &s.Tainted, &s.State)
	json.Unmarshal([]byte(needs), &s.Needs)
	return s, err
}

// proposedStarts are the held starts still waiting for an answer.
func (c *core) proposedStarts() []int64 {
	rows, err := c.store.db.Query(`SELECT id FROM task_starts WHERE state = ? ORDER BY id`, startProposed)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	return ids
}

// heldTask: the task still waits for its start to be decided.
func (c *core) heldTask(taskID string) bool {
	var n int
	c.store.db.QueryRow(`SELECT COUNT(*) FROM tasks WHERE id = ? AND state = ? AND desired = ?`, taskID, statePending, desiredHeld).Scan(&n)
	return n > 0
}

// taskStartProposal reads a held start as a proposal of the cards.
func (c *core) taskStartProposal(p proposal) proposal {
	s, err := c.taskStart(p.ID)
	if err != nil {
		return p
	}
	switch s.State {
	case startApproved:
		p.Outcome = cardApproved
	case startRejected:
		p.Outcome = cardRejected
	default:
		p.Pending = c.heldTask(s.TaskID) // stopped meanwhile: gone
	}
	p.Tainted = s.Tainted
	p.text = func(lang string) string { return startText(s.Needs, lang) }
	return p
}

// startText says, in a sentence per need, what will be used and why.
func startText(needs []startNeed, lang string) string {
	var lines []string
	for _, n := range needs {
		if n.Server != "" || n.Folder != "" {
			continue // said below
		}
		if n.Category == catSend {
			lines = append(lines, fmt.Sprintf(tr(lang, "taskstart.repo."+n.Category), n.Repo))
			continue
		}
		target := fmt.Sprintf(tr(lang, "taskstart.target.repo"), n.Repo)
		if n.Branch != "" {
			target = fmt.Sprintf(tr(lang, "taskstart.target.branch"), n.Branch, n.Repo)
		}
		line := fmt.Sprintf(tr(lang, "taskstart.ask"), target)
		lines = append(lines, line)
	}
	lines = append(lines, folderStartText(needs, lang)...)
	return strings.Join(append(lines, accessStartText(needs, lang)...), " ")
}

// decideTaskStart is the user's answer to a held start: yes grants what it
// needs for the rest of the task and lets it go; no stops it.
func (c *core) decideTaskStart(id int64, approve bool) error {
	s, err := c.taskStart(id)
	if err != nil {
		return errNoCard
	}
	state := startRejected
	if approve {
		state = startApproved
	}
	res, err := c.store.db.Exec(`UPDATE task_starts SET state = ?, decided = ? WHERE id = ? AND state = ?`,
		state, time.Now().Unix(), id, startProposed)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 || !c.heldTask(s.TaskID) {
		return errCardDecided
	}
	if st, ok := c.stepOfTask(s.TaskID); ok {
		defer c.keepThreadSurface(st.Thread) // its card leaves the thread's surface (thread_surface.go)
	}
	if !approve {
		c.store.updateTask(s.TaskID, map[string]any{"desired": desiredStopped, "state": stateStopped, "pending_reason": "its start was declined"})
		log.Printf("task %s: start declined by the user", s.TaskID)
		c.bump()
		return nil
	}
	for _, n := range s.Needs {
		if n.Server != "" {
			c.store.grantAccess(s.TaskID, accessNeed{n.Server, n.Category, n.Account})
			continue
		}
		if n.Folder != "" {
			continue // its mount is the task's from the start: the yes lets it go
		}
		c.store.grantRepo(s.TaskID, n.Repo, cmpOr(n.Category, catChange))
		c.store.logAccess(s.TaskID, "git", gitHost, n.Repo, n.Access, decideAllow, byUser, "", "approved at launch: "+n.Reason)
	}
	c.store.db.Exec(`UPDATE tasks SET desired = ?, pending_reason = '', updated = ? WHERE id = ? AND desired = ?`,
		desiredRunning, time.Now().Unix(), s.TaskID, desiredHeld)
	log.Printf("task %s: start approved by the user", s.TaskID)
	if t, err := c.store.task(s.TaskID); err == nil && t.Node == "" {
		c.placePending()
	}
	c.bump()
	return nil
}

// taskStartDeclined tells the brain that a start it launched was declined
// on its card (a "non" in words is told with the user's message).
func (c *core) taskStartDeclined(id int64) {
	s, err := c.taskStart(id)
	if err != nil {
		return
	}
	c.store.addMessage(roleEvent, fmt.Sprintf("%s a refusé le lancement de la tâche %s : elle ne démarre pas. Ne la relance pas telle quelle ; "+
		"dis-le en une phrase seulement s'il attend une suite.", c.conf().User, s.TaskID), false)
	c.ping()
}
