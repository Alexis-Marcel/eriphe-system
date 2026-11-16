package main

import (
	"cmp"
	"database/sql"
	"strings"
)

// The conversation per thread (#613). One brain, one queue: a message is a
// turn like any other, and each message of the conversation belongs to a
// thread, or to the home (0), in its thread column.
//
//   - A message of the user's written while a thread is open (its surface,
//     a child's inside its parent, or a surface about a live thread)
//     belongs to it: surfaceThread, resolved when it is written. The brain
//     reads it named (messageRef: « · fil N « titre » »).
//   - The brain's answer belongs to the thread of what it answers, fixed
//     at delivery (talkOf): the last message of the user's, else the last
//     event of a thread; the home for anything else. Never the thread the
//     turn names later (a tool on another thread): that one is for cards.
//     A failure of the turn is shown there too.
//   - An event of a thread (a step moved, a card of it decided) belongs to
//     it.
//   - When Eriphe puts a thread on stage in its answer to the user
//     (workspace_focus), the turn's exchange goes with it (talkMoves): the
//     question was about that thread, and the column shows it there.
//   - The workspace's column shows the exchange of the thread open, the
//     home's when none is (web_espace_exchange.go). The conversation page
//     keeps them all, in order.
//
// Messages written before the column existed belong to the home.

func init() {
	schemaFixes = append(schemaFixes, func(db *sql.DB) error {
		if err := addColumn(db, "messages", "thread", "INTEGER NOT NULL DEFAULT 0"); err != nil {
			return err
		}
		_, err := db.Exec(`CREATE INDEX IF NOT EXISTS messages_thread ON messages (thread, seq)`)
		return err
	})
}

// surfaceThread is the thread a surface stands for: a thread's own surface
// (thread-<id>), or the live thread a surface is about; 0 for none.
func (c *core) surfaceThread(id string) int64 {
	if n, ok := threadOfSurface(id); ok {
		if _, err := c.thread(n); err == nil {
			return n
		}
		return 0
	}
	if s, err := c.surface(id); err == nil && s.About != "" {
		if n, ok := c.liveThreadOf(s.About); ok {
			return n
		}
	}
	return 0
}

// openThreadOf is the thread of the ref the page sends with a message
// (surface:<id>, the surface open); 0 for none.
func (c *core) openThreadOf(ref string) int64 {
	id, ok := strings.CutPrefix(strings.TrimSpace(ref), surfaceRefPrefix)
	if !ok || !surfaceIDRe.MatchString(id) {
		return 0
	}
	return c.surfaceThread(id)
}

// threadOpen is the thread open on the workspace, as its last layout says:
// the surface open inside the one on stage (a child's), else the one on
// stage; 0 for none, the home.
func (c *core) threadOpen() int64 {
	st := &c.layout
	st.mu.Lock()
	open := cmp.Or(st.last.Inside, st.last.Focus)
	st.mu.Unlock()
	if open == "" {
		return 0
	}
	return c.surfaceThread(open)
}

// talkOf is the thread the answer to a delivery belongs to: the last
// message of the user's, whatever its thread (0: the home); without one,
// the last event of a thread; else the home.
func talkOf(msgs []Message) int64 {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == roleUser {
			return msgs[i].Thread
		}
	}
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Thread > 0 {
			return msgs[i].Thread
		}
	}
	return 0
}

// answersIn sets the thread the turn's answer belongs to.
func (t *brainTurn) answersIn(id int64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.talk != id {
		t.talk = id
		t.save()
	}
}

// talkThread is the thread the turn's answer belongs to; 0 for the home.
func (t *brainTurn) talkThread() int64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.talk
}

// talkMoves moves the turn's exchange to a thread Eriphe put on stage in
// its answer: the user's messages it answers, and what it said and failed
// to say since; what it says next goes there too.
func (c *core) talkMoves(id int64) {
	from, to := c.turn.span()
	if id <= 0 || from == 0 || c.turn.talkThread() == id {
		return
	}
	c.store.db.Exec(`UPDATE messages SET thread = ? WHERE seq >= ? AND ((role = ? AND seq <= ?) OR role IN (?, ?))`,
		id, from, roleUser, to, roleBrain, roleError)
	c.turn.answersIn(id)
}
