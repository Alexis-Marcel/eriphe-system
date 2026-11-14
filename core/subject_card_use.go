package main

import (
	"fmt"
	"log"
	"strings"
	"time"
)

// What the subject cards are worth (#512, step 8). Each card pushed to a
// model is counted, by place, and judged once its window is over: used or
// not. The judgment is a proxy, kept simple and said as it is:
//
//   - wrote: in the window after the push, the model it was pushed to
//     wrote or changed a record of the card's subject. For the brain (a
//     turn's card, a pack's), a memory, an intention or a line in a
//     thread of that subject written by "brain"; for a worker task, a
//     memory its remember wrote (source task:<id>), or the subject's
//     thread summary noted by a task; for a lead, a line written by a lead
//     in a thread of that subject (nearly always: a lead works on its
//     thread, so its count says little).
//   - answered: the card of a brain turn's subject (the turn's, or the
//     one of a pack delivered with the turn's messages; a pack's other
//     cards are no turn's) when the turn's answer, the first message after
//     it, is the brain's, and the brain did not ask again for that subject
//     in the turn (recall with its subject or naming it, read_subject of
//     it): the card was enough, or the subject did not matter to the
//     answer; it does not tell which, so it is counted apart from wrote.
//   - no: neither.
//
// The window is cardUseWindow for the brain, cardUseLongWindow for a task
// or a lead, which work longer. Counts are shown in Réglages → Sujets and
// the tasks' reliability panel; each push is logged too.

const cardPushesSchema = `CREATE TABLE IF NOT EXISTS card_pushes (
	id      INTEGER PRIMARY KEY AUTOINCREMENT,
	at      INTEGER NOT NULL,
	subject TEXT NOT NULL,
	place   TEXT NOT NULL,               -- turn, pack, task, lead
	chars   INTEGER NOT NULL,
	turn    INTEGER NOT NULL DEFAULT 0,  -- the brain's turn it went in (its last message's seq); 0 for none
	who     TEXT NOT NULL DEFAULT '',    -- the task's id, the lead's thread
	looked  INTEGER NOT NULL DEFAULT 0,  -- recall or read_subject asked for its subject in that turn
	used    TEXT NOT NULL DEFAULT ''     -- '' until judged; wrote, answered, no
);
CREATE INDEX IF NOT EXISTS card_pushes_used ON card_pushes (used, at);`

func init() { extraSchema = append(extraSchema, cardPushesSchema) }

// Where a card is pushed.
const (
	placeTurn = "turn" // the brain's turn, its subject's card
	placePack = "pack" // a fresh session's context pack
	placeTask = "task" // a worker task's first prompt
	placeLead = "lead" // a thread lead's first turn
)

var cardPlaces = []string{placeTurn, placePack, placeTask, placeLead}

// How a card pushed was judged.
const (
	cardWrote    = "wrote"
	cardAnswered = "answered"
	cardUnused   = "no"
)

const (
	cardUseWindow     = 30 * time.Minute
	cardUseLongWindow = 2 * time.Hour
	cardPushesKept    = 90 * 24 * time.Hour
)

func cardWindow(place string) time.Duration {
	if place == placeTask || place == placeLead {
		return cardUseLongWindow
	}
	return cardUseWindow
}

// cardPushed logs a card given to a model and counts it: where (place),
// in which brain turn (0 for none), to which task or lead.
func (c *core) cardPushed(x pushedCard, place string, turn int, who string) {
	where := place
	if who != "" {
		where += " " + who
	}
	log.Printf("subject card %s → %s: %d chars, %d lines, %d left out", x.Key, where, x.Stats.Chars, x.Stats.Lines, x.Stats.Dropped)
	if _, err := c.store.db.Exec(`INSERT INTO card_pushes (at, subject, place, chars, turn, who) VALUES (?, ?, ?, ?, ?, ?)`,
		time.Now().Unix(), x.Key, place, x.Stats.Chars, turn, who); err != nil {
		log.Printf("subject card %s: %v", x.Key, err)
	}
}

// turnSeq is the brain's turn now: its last message's seq.
func (c *core) turnSeq() int {
	_, to := c.turn.span()
	return to
}

// cardLookedUp notes that the brain asked again, in its turn, for these
// subjects: their cards of the turn were not enough.
func (c *core) cardLookedUp(keys ...string) {
	turn := c.turnSeq()
	if turn == 0 {
		return
	}
	for _, k := range keys {
		if k = c.store.canonSubject(subjectOf(k)); k != "" {
			c.store.db.Exec(`UPDATE card_pushes SET looked = 1 WHERE used = '' AND turn = ? AND subject = ? AND place IN (?, ?)`,
				turn, k, placeTurn, placePack)
		}
	}
}

// recallLookedUp notes what a recall asks for: its subject, and the
// known subjects its words name.
func (c *core) recallLookedUp(in recallIn) {
	keys := c.subjectsNamed(in.Query)
	if in.Subject != "" {
		keys = append(keys, in.Subject)
	}
	c.cardLookedUp(keys...)
}

// cardPush is a row of card_pushes.
type cardPush struct {
	ID      int64
	At      time.Time
	Subject string
	Place   string
	Turn    int
	Who     string
	Looked  bool
}

// judgeCardPushes judges the pushes whose window is over, and forgets the
// oldest. It runs with the controller's pass.
func (c *core) judgeCardPushes(now time.Time) {
	c.store.db.Exec(`DELETE FROM card_pushes WHERE at < ?`, now.Add(-cardPushesKept).Unix())
	rows, err := c.store.db.Query(`SELECT id, at, subject, place, turn, who, looked FROM card_pushes
		WHERE used = '' AND at <= ? ORDER BY id LIMIT 200`, now.Add(-cardUseWindow).Unix())
	if err != nil {
		return
	}
	var xs []cardPush
	for rows.Next() {
		var x cardPush
		var at int64
		if rows.Scan(&x.ID, &at, &x.Subject, &x.Place, &x.Turn, &x.Who, &x.Looked) == nil {
			x.At = time.Unix(at, 0)
			xs = append(xs, x)
		}
	}
	rows.Close() // one connection: the judgments are read after
	for _, x := range xs {
		if now.Before(x.At.Add(cardWindow(x.Place))) {
			continue
		}
		c.store.db.Exec(`UPDATE card_pushes SET used = ? WHERE id = ?`, c.judgeCard(x), x.ID)
	}
}

// judgeCard is how a card pushed was used (see the top of this file).
func (c *core) judgeCard(x cardPush) string {
	from, to := x.At.Unix(), x.At.Add(cardWindow(x.Place)).Unix()
	var n int
	threadBy := func(who string) int {
		var k int
		c.store.db.QueryRow(`SELECT COUNT(*) FROM thread_log l JOIN threads t ON t.id = l.thread
			WHERE t.subject = ? AND l.at BETWEEN ? AND ? AND l.who = ?`, x.Subject, from, to, who).Scan(&k)
		return k
	}
	switch x.Place {
	case placeTurn, placePack:
		n = threadBy(byBrain)
		if c.mem != nil {
			var m, i int
			c.mem.db.QueryRow(`SELECT COUNT(*) FROM memory_log l JOIN memories m ON m.id = l.memory_id
				WHERE m.subject = ? AND l.time BETWEEN ? AND ? AND l.who = ?`, x.Subject, from, to, byBrain).Scan(&m)
			c.mem.db.QueryRow(`SELECT COUNT(*) FROM intention_log l JOIN intentions i ON i.id = l.intention_id
				WHERE i.subject = ? AND l.time BETWEEN ? AND ? AND l.who = ?`, x.Subject, from, to, byBrain).Scan(&i)
			n += m + i
		}
	case placeTask:
		n = threadBy("task")
		if c.mem != nil {
			var m int
			c.mem.db.QueryRow(`SELECT COUNT(*) FROM memory_log l JOIN memories m ON m.id = l.memory_id
				WHERE m.subject = ? AND l.time BETWEEN ? AND ? AND l.who = 'task' AND m.source_ref = ?`, x.Subject, from, to, "task:"+x.Who).Scan(&m)
			n += m
		}
	case placeLead:
		n = threadBy("lead")
	}
	if n > 0 {
		return cardWrote
	}
	if (x.Place == placeTurn || x.Place == placePack) && x.Turn > 0 && !x.Looked {
		// The turn's answer: the first message after it is the brain's,
		// in the window.
		var role string
		var at int64
		if c.store.db.QueryRow(`SELECT role, time FROM messages WHERE seq > ? ORDER BY seq LIMIT 1`, x.Turn).Scan(&role, &at) == nil &&
			role == roleBrain && at <= to {
			return cardAnswered
		}
	}
	return cardUnused
}

// cardUse is what was pushed of the cards, and how it was used.
type cardUse struct {
	Place    string
	Pushed   int
	Wrote    int
	Answered int
	Unused   int
	Waiting  int // not judged yet
}

// Used is the cards used, one way or the other.
func (u cardUse) Used() int { return u.Wrote + u.Answered }

// Judged is the cards judged.
func (u cardUse) Judged() int { return u.Used() + u.Unused }

// cardUses are the counts per place since a time, for a subject or all
// ("").
func (s *Store) cardUses(since time.Time, subject string) []cardUse {
	where, args := `at >= ?`, []any{since.Unix()}
	if subject != "" {
		where += ` AND subject = ?`
		args = append(args, subject)
	}
	byPlace := map[string]*cardUse{}
	rows, err := s.db.Query(`SELECT place, used, COUNT(*) FROM card_pushes WHERE `+where+` GROUP BY place, used`, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()
	for rows.Next() {
		var place, used string
		var n int
		if rows.Scan(&place, &used, &n) != nil {
			continue
		}
		u := byPlace[place]
		if u == nil {
			u = &cardUse{Place: place}
			byPlace[place] = u
		}
		u.Pushed += n
		switch used {
		case cardWrote:
			u.Wrote += n
		case cardAnswered:
			u.Answered += n
		case cardUnused:
			u.Unused += n
		default:
			u.Waiting += n
		}
	}
	var out []cardUse
	for _, p := range cardPlaces {
		if u := byPlace[p]; u != nil {
			out = append(out, *u)
		}
	}
	return out
}

// String says the counts in a line, for the log and tests.
func (u cardUse) String() string {
	return fmt.Sprintf("%s %d pushed, %d wrote, %d answered, %d no, %d waiting", u.Place, u.Pushed, u.Wrote, u.Answered, u.Unused, u.Waiting)
}

// cardUsesLine says them all.
func cardUsesLine(us []cardUse) string {
	var parts []string
	for _, u := range us {
		parts = append(parts, u.String())
	}
	return strings.Join(parts, "; ")
}
