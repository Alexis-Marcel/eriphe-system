package main

import "sync"

// The brain's draft: the message it is writing, as its node reports it
// (Stream), shown live in the workspace. It is kept in memory only, never
// in the conversation: the message is stored once, whole, as a text event
// (relayBrainOutput), and the draft it completes ends with it.
//
// A draft knows the last message of the conversation when it was written.
// Its node reports the whole message and the end of the draft together,
// and eriphe-core stores the message before it changes the draft: a draft
// older than a message a viewer has is one that message replaced.
type brainDraft struct {
	mu      sync.Mutex
	text    string
	after   int           // the last message when it was written
	changed chan struct{} // closed and replaced on every change
}

func (d *brainDraft) set(text string, after int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.changed == nil {
		d.changed = make(chan struct{})
	}
	if text == d.text && after == d.after {
		return
	}
	d.text, d.after = text, after
	close(d.changed)
	d.changed = make(chan struct{})
}

// get returns the draft, the last message when it was written, and a
// channel closed at its next change.
func (d *brainDraft) get() (string, int, <-chan struct{}) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.changed == nil {
		d.changed = make(chan struct{})
	}
	return d.text, d.after, d.changed
}

// brainWrote takes what the brain's node reports: the whole messages among
// its new events first, then the draft that follows them.
func (c *core) brainWrote(a Assistant, t *TaskRow, events bool, draft string) {
	c.relayMu.Lock()
	defer c.relayMu.Unlock()
	if events {
		c.copyBrainOutput(a, t)
	}
	if toolCallStart(draft) {
		draft = "" // a tool call being written as text is not shown (brain_toolcall.go)
	}
	c.draft.set(draft, c.store.lastMessageSeq())
}
