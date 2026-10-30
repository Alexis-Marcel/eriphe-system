package main

import (
	"log"
	"strconv"
)

// When the brain fails, the conversation says so with an error card
// (parts.html, "brain-error"): a message of its own role, which the brain
// never reads, holding the raw error that the card words (problems.go).
// One card stands for a run of failures: a new one comes only after a
// message of mine or a retry. While a card is open, the conversation does
// not show the brain as thinking. "Try again" gives the brain the turn
// that failed once more, and starts it again at once if it is down.

const brainRetryKey = "brain_retry" // the last message's seq when "Try again" was asked

// brainFailed puts an error card in the conversation, unless one is open.
// via is where it failed: "turn" or "start".
func (c *core) brainFailed(raw, via string) {
	if c.brainErrorOpen() != 0 {
		return
	}
	log.Printf("brain: failed (%s): %s", via, truncate(raw, 300))
	c.store.addMessageVia(roleError, truncate(raw, 4000), via, true)
}

// brainErrorOpen is the error card the user has not answered yet, by a
// message or a retry: its seq, or 0.
func (c *core) brainErrorOpen() int {
	var errSeq, userSeq int
	c.store.db.QueryRow(`SELECT COALESCE(MAX(seq), 0) FROM messages WHERE role = ?`, roleError).Scan(&errSeq)
	if errSeq == 0 {
		return 0
	}
	c.store.db.QueryRow(`SELECT COALESCE(MAX(seq), 0) FROM messages WHERE role = ?`, roleUser).Scan(&userSeq)
	retried, _ := strconv.Atoi(c.store.get(brainRetryKey))
	if errSeq > userSeq && errSeq > retried {
		return errSeq
	}
	return 0
}

// lastFailure is the raw words of a task's last failure: an error it
// reported, else why it never ran.
func (c *core) lastFailure(id string) string {
	if id == "" {
		return ""
	}
	events, _ := c.store.events(id, 50)
	for i := len(events) - 1; i >= 0; i-- {
		if raw, bad := turnFailure(events[i]); bad && raw != "" {
			return raw
		}
	}
	if t, err := c.store.task(id); err == nil {
		return t.PendingReason
	}
	return ""
}

// retryBrain gives the brain once more what it got since its last answer,
// up to the last failure, and lets its controller start it again now.
func (c *core) retryBrain() {
	var errSeq, answered int
	c.store.db.QueryRow(`SELECT COALESCE(MAX(seq), 0) FROM messages WHERE role = ?`, roleError).Scan(&errSeq)
	c.store.db.QueryRow(`SELECT COALESCE(MAX(seq), 0) FROM messages WHERE role = ? AND seq < ?`, roleBrain, errSeq).Scan(&answered)
	if errSeq > 0 {
		c.store.db.Exec(`UPDATE messages SET delivered = 0 WHERE seq > ? AND seq < ? AND role IN (?, ?)`,
			answered, errSeq, roleUser, roleEvent)
	}
	c.store.set(brainRetryKey, strconv.Itoa(c.store.lastMessageSeq()))
	if b := c.conf().brainEntry(); b != nil {
		c.updateAssistant(b.Name, func(st *AssistantStatus) { st.NextStart = 0 })
	}
	log.Printf("brain: retry asked")
	c.ping()
}
