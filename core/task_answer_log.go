package main

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

// What Eriphe settles for an assistant shows in the thread (#554, #564).
//
// A task's question reaches the brain, not the user: the brain answers it
// with answer_task and the user never saw it. When the task is a step of
// a thread, code writes what the brain did on the thread's log, in plain
// words, and keeps it as a fact (thread_answers: a kind and its
// parameters, never a sentence), which the thread's surface words in the
// user's language: a line of its timeline under the step concerned, and
// one of its header's facts. Display only: nothing here answers a card,
// grants or releases anything. A release is kept as what it is, the brain
// asking the user on a card, then the user's own answer on it, never as a
// decision taken for them.
//
// The task's question is never copied: it is the task's output, possibly
// external content. Eriphe's reply is the brain's own words; written on a
// turn that read external content, it is kept with that taint and the
// surface says « extérieur » where it shows (thread_surface.go).

const taskAnswerSchema = `CREATE TABLE IF NOT EXISTS thread_answers (
	id      INTEGER PRIMARY KEY AUTOINCREMENT,
	thread  INTEGER NOT NULL,
	step    TEXT NOT NULL,
	at      INTEGER NOT NULL,
	kind    TEXT NOT NULL,              -- reply, checks, release, released, kept
	refs    TEXT NOT NULL DEFAULT '',   -- the checks' names, the tools released, one per line
	reply   TEXT NOT NULL DEFAULT '',   -- Eriphe's reply to the assistant
	tainted TEXT NOT NULL DEFAULT ''    -- why the reply may carry what someone else wrote
);
CREATE INDEX IF NOT EXISTS thread_answers_thread ON thread_answers (thread, id);`

func init() { extraSchema = append(extraSchema, taskAnswerSchema) }

// What was settled for a thread's task, its kinds.
const (
	answerReply    = "reply"    // Eriphe replied to the assistant's question
	answerChecks   = "checks"   // Eriphe fixed its contract's checks (and replied)
	answerRelease  = "release"  // Eriphe asked the user, on a card, to release tools
	answerReleased = "released" // the user released them on the card
	answerKept     = "kept"     // the user refused on the card
)

const answerReplyMax = 160 // runes of a reply the surface shows

// taskAnswer is what was settled for a step's task, as kept.
type taskAnswer struct {
	ID      int64
	At      time.Time
	Kind    string
	Refs    []string
	Reply   string
	Tainted string
}

// text words an answer in the user's language, without its step.
func (a taskAnswer) text(lang string) string {
	refs := strings.Join(a.Refs, ", ")
	reply := truncate(oneLine(a.Reply), answerReplyMax)
	switch a.Kind {
	case answerReply:
		return fmt.Sprintf(tr(lang, "thread.answer.reply"), reply)
	case answerChecks:
		if reply != "" {
			return fmt.Sprintf(tr(lang, "thread.answer.checksreply"), refs, reply)
		}
		return fmt.Sprintf(tr(lang, "thread.answer.checks"), refs)
	case answerRelease, answerReleased, answerKept:
		return fmt.Sprintf(tr(lang, "thread.answer."+a.Kind), refs)
	}
	return ""
}

// stepAnswers are what was settled for a thread's tasks, by the step's
// key, oldest first.
func (c *core) stepAnswers(thread int64) map[string][]taskAnswer {
	out := map[string][]taskAnswer{}
	rows, err := c.store.db.Query(`SELECT id, step, at, kind, refs, reply, tainted FROM thread_answers WHERE thread = ? ORDER BY id`, thread)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var a taskAnswer
		var step, refs string
		var at int64
		if rows.Scan(&a.ID, &step, &at, &a.Kind, &refs, &a.Reply, &a.Tainted) != nil {
			continue
		}
		a.At = time.Unix(at, 0)
		if refs != "" {
			a.Refs = strings.Split(refs, "\n")
		}
		out[step] = append(out[step], a)
	}
	return out
}

// noteTaskAnswer keeps what was settled for the task, if it is a thread's
// step, then writes its line on the thread's log, which writes the
// thread's surface again; who says who did it (brain, user).
func (c *core) noteTaskAnswer(task, who, line string, a taskAnswer) {
	st, ok := c.stepOfTask(task)
	if !ok {
		return
	}
	c.store.db.Exec(`INSERT INTO thread_answers (thread, step, at, kind, refs, reply, tainted) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		st.Thread, st.Key, time.Now().Unix(), a.Kind, strings.Join(a.Refs, "\n"), strings.TrimSpace(a.Reply), a.Tainted)
	c.logThread(st.Thread, who, fmt.Sprintf("Étape « %s » : %s", st.Title, line))
}

// brainTaint is why what the brain writes now may carry what someone else
// wrote: its turn's taint.
func (c *core) brainTaint() string { return strings.Join(c.turnTaint(), ", ") }

// logBrainReply is answer_task's reply, sent to the task.
func (c *core) logBrainReply(task, reply string) {
	c.noteTaskAnswer(task, byBrain, "question de l'assistant → Eriphe a répondu : "+strings.TrimSpace(reply),
		taskAnswer{Kind: answerReply, Reply: reply, Tainted: c.brainTaint()})
}

// logBrainChecks is answer_task's fix of a contract's checks.
func (c *core) logBrainChecks(task string, checks []taskCheck, reply string) {
	names := make([]string, 0, len(checks))
	for _, k := range checks {
		names = append(names, k.Name)
	}
	line := "Eriphe a corrigé les contrôles de l'assistant (" + strings.Join(names, ", ") + ")"
	if reply = strings.TrimSpace(reply); reply != "" {
		line += " et lui a répondu : " + reply
	} else {
		line += "."
	}
	c.noteTaskAnswer(task, byBrain, line, taskAnswer{Kind: answerChecks, Refs: names, Reply: reply, Tainted: c.brainTaint()})
}

// logReleaseAsked is answer_task's release: an ask on a card, nothing
// more. Its refs are the task's held tools, checked by releaseTools.
func (c *core) logReleaseAsked(task string, refs []string) {
	refs = slices.Clone(refs)
	for i := range refs {
		refs[i] = strings.TrimSpace(refs[i]) // as the card names them
	}
	c.noteTaskAnswer(task, byBrain, fmt.Sprintf("Eriphe a demandé à %s, sur une carte, de débloquer %s : rien n'est débloqué avant sa réponse.",
		c.conf().User, strings.Join(refs, ", ")), taskAnswer{Kind: answerRelease, Refs: refs})
}

// logReleaseDecided is the user's answer on that card.
func (c *core) logReleaseDecided(task string, refs []string, approve bool) {
	word, kind := "a refusé de débloquer", answerKept
	if approve {
		word, kind = "a débloqué", answerReleased
	}
	c.noteTaskAnswer(task, byUser, fmt.Sprintf("%s %s %s sur la carte.", c.conf().User, word, strings.Join(refs, ", ")),
		taskAnswer{Kind: kind, Refs: slices.Clone(refs)})
}
