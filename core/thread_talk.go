package main

import (
	"fmt"
	"strconv"
	"strings"
)

// What the user said about a thread, filed under it (#556): a message of
// the user's written over a thread's surface (threadAbout) and Eriphe's
// replies of the turn it starts. The workspace shows the exchange in the
// thread's card while it goes on, then folds it into one line of the card
// (web_espace_exchange.go, Exchange.tsx); the conversation keeps it whole.
//
// Filed when it is said, not worked out again when read: what a message
// is about depends on what is live then.
//
//   - The user's message, when it is written (noteAbout), and again when
//     the brain is given it (threadsDelivered): the turn is then the
//     thread's talk (talkTurnKey).
//   - Eriphe's lines of that turn while it is still about the thread
//     (fileReply): a thread tool naming another thread ends it.
//   - off_thread unfiles the turn whole: the user's message was about
//     something else, it stays in the conversation's flow.
//
// A turn about a thread only by its event or a tool, with no message of
// the user's about it, files nothing: it is no exchange with the user.

const threadTalkSchema = `CREATE TABLE IF NOT EXISTS thread_talk (
	seq    INTEGER PRIMARY KEY,
	thread INTEGER NOT NULL,
	turn   INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS thread_talk_turn ON thread_talk(turn);`

func init() { extraSchema = append(extraSchema, threadTalkSchema) }

// talkTurnKey is the turn filed now: "<thread>:<the user's message seq>".
const talkTurnKey = "thread_talk_turn"

// fileTalk files a message under a thread, in the turn its user's message
// started.
func (c *core) fileTalk(seq int, thread int64, turn int) {
	c.store.db.Exec(`INSERT OR REPLACE INTO thread_talk (seq, thread, turn) VALUES (?, ?, ?)`, seq, thread, turn)
}

// fileUserTalk files a message of the user's just written, should it be
// about a thread.
func (c *core) fileUserTalk(seq int64) {
	if id := c.threadAbout(Message{Seq: int(seq), Role: roleUser}); id > 0 {
		c.fileTalk(int(seq), id, int(seq))
	}
}

// talkDelivered notes the turn's talk from what the brain was given: the
// thread the turn is about, when the last message naming it is the
// user's; none otherwise.
func (c *core) talkDelivered(msgs []Message, id int64) {
	turn := 0
	for _, m := range msgs {
		if c.store.get(threadEventKey(m.Seq)) != "" {
			turn = 0
		} else if n := c.threadAbout(m); n > 0 {
			turn = m.Seq
			if n != id {
				turn = 0
			}
		}
	}
	if id <= 0 || turn == 0 {
		c.store.set(talkTurnKey, "")
		return
	}
	c.fileTalk(turn, id, turn)
	c.store.set(talkTurnKey, fmt.Sprintf("%d:%d", id, turn))
}

// talkTurn is the turn filed now and its thread; false when none.
func (c *core) talkTurn() (int64, int, bool) {
	a, b, ok := strings.Cut(c.store.get(talkTurnKey), ":")
	if !ok {
		return 0, 0, false
	}
	id, err1 := strconv.ParseInt(a, 10, 64)
	turn, err2 := strconv.Atoi(b)
	if err1 != nil || err2 != nil || id <= 0 || turn <= 0 {
		return 0, 0, false
	}
	return id, turn, true
}

// talkThread is the thread the turn talks with the user about, while it
// is still about it; 0 if none.
func (c *core) talkThread() int64 {
	id, _, ok := c.talkTurn()
	if !ok || c.turn.aboutThread() != id {
		return 0
	}
	return id
}

// fileReply files a line of Eriphe's under the thread its turn talks
// about.
func (c *core) fileReply(seq int) {
	if id := c.talkThread(); id > 0 {
		_, turn, _ := c.talkTurn()
		c.fileTalk(seq, id, turn)
	}
}

// talkOff unfiles the turn: off_thread said it was about no thread.
func (c *core) talkOff() {
	if _, turn, ok := c.talkTurn(); ok {
		c.store.db.Exec(`DELETE FROM thread_talk WHERE turn = ?`, turn)
	}
	c.store.set(talkTurnKey, "")
}

// talkOf is the thread each of these messages is filed under, as its
// surface's id.
func (c *core) talkOf(seqs []int) map[int]string {
	out := map[int]string{}
	if len(seqs) == 0 {
		return out
	}
	q := strings.TrimSuffix(strings.Repeat("?,", len(seqs)), ",")
	args := make([]any, len(seqs))
	for i, s := range seqs {
		args[i] = s
	}
	rows, err := c.store.db.Query(`SELECT seq, thread FROM thread_talk WHERE seq IN (`+q+`)`, args...)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var seq int
		var id int64
		if rows.Scan(&seq, &id) == nil {
			out[seq] = threadSurfaceID(id)
		}
	}
	return out
}
