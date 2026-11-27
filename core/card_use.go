package main

import (
	"fmt"
	"log"
	"strings"
	"time"
)

// What the about cards are worth (#512, step 8). Each card pushed to a
// model is counted, by place, and judged once its window is over: used or
// not. The judgment is a proxy, kept simple and said as it is:
//
//   - wrote: in the window after the push, the model it was pushed to
//     wrote or changed a record about the card's node or entity. For the
//     brain (a turn's card, a pack's), an intention or a version of the
//     node written by "brain"; for a worker task, a version of the node
//     written by a task or a skill's run.
//   - answered: the card of what a brain turn is about (the turn's, or the
//     one of a pack delivered with the turn's messages; a pack's other
//     cards are no turn's) when the turn's answer, the first message after
//     it, is the brain's, and the brain did not ask again for it in the
//     turn (node_read of it): the card was
//     enough, or it did not matter to the answer; it does not tell which,
//     so it is counted apart from wrote.
//   - no: neither.
//
// The window is cardUseWindow for the brain, cardUseLongWindow for a task,
// which works longer. Counts are shown in
// the tasks' reliability panel; each push is logged too.

const cardPushesSchema = `CREATE TABLE IF NOT EXISTS card_pushes (
	id      INTEGER PRIMARY KEY AUTOINCREMENT,
	at      INTEGER NOT NULL,
	about   TEXT NOT NULL DEFAULT '',
	place   TEXT NOT NULL,               -- turn, pack, task
	chars   INTEGER NOT NULL,
	turn    INTEGER NOT NULL DEFAULT 0,  -- the brain's turn it went in (its last message's seq); 0 for none
	who     TEXT NOT NULL DEFAULT '',    -- the task's id
	looked  INTEGER NOT NULL DEFAULT 0,  -- node_read asked for it in that turn
	used    TEXT NOT NULL DEFAULT ''     -- '' until judged; wrote, answered, no
);
CREATE INDEX IF NOT EXISTS card_pushes_used ON card_pushes (used, at);`

func init() { extraSchema = append(extraSchema, cardPushesSchema) }

// Where a card is pushed.
const (
	placeTurn = "turn" // the brain's turn, its card
	placePack = "pack" // a fresh session's context pack
	placeTask = "task" // a worker task's first prompt
)

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
	if place == placeTask {
		return cardUseLongWindow
	}
	return cardUseWindow
}

// cardPushed logs a card given to a model and counts it: where (place),
// in which brain turn (0 for none), to which task.
func (c *core) cardPushed(x pushedCard, place string, turn int, who string) {
	where := place
	if who != "" {
		where += " " + who
	}
	log.Printf("card %s → %s: %d chars, %d lines, %d left out", x.Key, where, x.Stats.Chars, x.Stats.Lines, x.Stats.Dropped)
	if _, err := c.store.db.Exec(`INSERT INTO card_pushes (at, about, place, chars, turn, who) VALUES (?, ?, ?, ?, ?, ?)`,
		time.Now().Unix(), x.Key, place, x.Stats.Chars, turn, who); err != nil {
		log.Printf("card %s: %v", x.Key, err)
	}
}

// turnSeq is the brain's turn now: its last message's seq.
func (c *core) turnSeq() int {
	_, to := c.turn.span()
	return to
}

// cardLookedUp notes that the brain asked again, in its turn, for these
// nodes or entities: their cards of the turn were not enough.
func (c *core) cardLookedUp(keys ...string) {
	turn := c.turnSeq()
	if turn == 0 {
		return
	}
	for _, k := range keys {
		if k = c.store.resolveAbout(aboutOf(k)); k != "" {
			c.store.db.Exec(`UPDATE card_pushes SET looked = 1 WHERE used = '' AND turn = ? AND about = ? AND place IN (?, ?)`,
				turn, k, placeTurn, placePack)
		}
	}
}

// cardPush is a row of card_pushes.
type cardPush struct {
	ID     int64
	At     time.Time
	About  string
	Place  string
	Turn   int
	Who    string
	Looked bool
}

// judgeCardPushes judges the pushes whose window is over, and forgets the
// oldest. It runs with the controller's pass.
func (c *core) judgeCardPushes(now time.Time) {
	c.store.db.Exec(`DELETE FROM card_pushes WHERE at < ?`, now.Add(-cardPushesKept).Unix())
	rows, err := c.store.db.Query(`SELECT id, at, about, place, turn, who, looked FROM card_pushes
		WHERE used = '' AND at <= ? ORDER BY id LIMIT 200`, now.Add(-cardUseWindow).Unix())
	if err != nil {
		return
	}
	var xs []cardPush
	for rows.Next() {
		var x cardPush
		var at int64
		if rows.Scan(&x.ID, &at, &x.About, &x.Place, &x.Turn, &x.Who, &x.Looked) == nil {
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
	nodeBy := func(task bool) int {
		id, ok := strings.CutPrefix(x.About, refNode+":")
		if !ok || c.nodes == nil {
			return 0
		}
		vs, _ := c.nodes.Versions(id)
		k := 0
		for _, v := range vs {
			by := v.Author == byBrain
			if task {
				by = strings.HasPrefix(v.Author, "task:") || strings.HasPrefix(v.Author, "skill:")
			}
			if by && v.At.Unix() >= from && v.At.Unix() <= to {
				k++
			}
		}
		return k
	}
	switch x.Place {
	case placeTurn, placePack:
		n = nodeBy(false)
		var i int
		c.intents.db.QueryRow(`SELECT COUNT(*) FROM intention_log l JOIN intentions i ON i.id = l.intention_id
			WHERE i.about = ? AND l.time BETWEEN ? AND ? AND l.who = ?`, x.About, from, to, byBrain).Scan(&i)
		n += i
	case placeTask:
		n = nodeBy(true)
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

// cardUses are the counts per place since a time, for one about or all
// ("").
func (s *Store) cardUses(since time.Time, about string) []cardUse {
	where, args := `at >= ?`, []any{since.Unix()}
	if about != "" {
		where += ` AND about = ?`
		args = append(args, about)
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
	for _, p := range []string{placeTurn, placePack, placeTask} {
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
