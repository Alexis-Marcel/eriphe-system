package main

import (
	"context"
	"encoding/json"
	"sync"
	"time"
)

// The messages received, for receive: the last inboxKeep, in memory. Each
// has a sequence number that starts from the clock at each start, so one
// is always above those of a previous run: a reader that asks for what
// follows an old one gets everything kept.

const inboxKeep = 500

// message is what someone sent to the account: a text (Text, or none for a
// file, a sticker or a voice note: Attachment), or a reaction to one of
// its messages. A sync message (the account's other devices), a receipt
// or a typing note is not one.
type message struct {
	Seq        int64     `json:"seq"`
	From       string    `json:"from"`             // the sender's ACI (UUID), else their number
	Number     string    `json:"number,omitempty"` // their number, when Signal shows it
	Name       string    `json:"name,omitempty"`   // their profile name
	Group      bool      `json:"group,omitempty"`  // in a group, not a private chat
	Text       string    `json:"text,omitempty"`
	Attachment bool      `json:"attachment,omitempty"`
	Reaction   *reaction `json:"reaction,omitempty"`
	Timestamp  int64     `json:"timestamp"` // when it was sent, Signal's id of it
}

type reaction struct {
	Emoji  string `json:"emoji"`
	Target int64  `json:"target"` // the timestamp of the message reacted to
	Remove bool   `json:"remove,omitempty"`
}

type inbox struct {
	mu   sync.Mutex
	seq  int64
	all  []message
	wake chan struct{} // closed when a message comes
}

func newInbox() *inbox {
	return &inbox{seq: time.Now().UnixMicro(), wake: make(chan struct{})}
}

// envelope is signal-cli's, as much of it as is used.
type envelope struct {
	Source       string `json:"source"`
	SourceNumber string `json:"sourceNumber"`
	SourceUUID   string `json:"sourceUuid"`
	SourceName   string `json:"sourceName"`
	Timestamp    int64  `json:"timestamp"`
	DataMessage  *struct {
		Message     string            `json:"message"`
		GroupInfo   json.RawMessage   `json:"groupInfo"`
		Attachments []json.RawMessage `json:"attachments"`
		Sticker     json.RawMessage   `json:"sticker"`
		Reaction    *struct {
			Emoji               string `json:"emoji"`
			IsRemove            bool   `json:"isRemove"`
			TargetSentTimestamp int64  `json:"targetSentTimestamp"`
		} `json:"reaction"`
	} `json:"dataMessage"`
}

// add keeps a "receive" notification's message, when it is one: its params
// are {envelope, account}, or {subscription, result: {envelope, account}}.
func (b *inbox) add(params json.RawMessage) {
	var p struct {
		Envelope *envelope `json:"envelope"`
		Result   *struct {
			Envelope *envelope `json:"envelope"`
		} `json:"result"`
	}
	if json.Unmarshal(params, &p) != nil {
		return
	}
	e := p.Envelope
	if e == nil && p.Result != nil {
		e = p.Result.Envelope
	}
	if e == nil || e.DataMessage == nil {
		return
	}
	d := e.DataMessage
	m := message{From: e.SourceUUID, Number: e.SourceNumber, Name: e.SourceName, Timestamp: e.Timestamp,
		Group: len(d.GroupInfo) > 0 && string(d.GroupInfo) != "null", Text: d.Message,
		Attachment: len(d.Attachments) > 0 || (len(d.Sticker) > 0 && string(d.Sticker) != "null")}
	if m.From == "" {
		m.From = e.Source
	}
	if r := d.Reaction; r != nil {
		m.Reaction = &reaction{Emoji: r.Emoji, Target: r.TargetSentTimestamp, Remove: r.IsRemove}
	} else if m.Text == "" && !m.Attachment {
		return // a profile key, a timer, a deletion
	}
	if m.From == "" {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.seq++
	m.Seq = b.seq
	b.all = append(b.all, m)
	if len(b.all) > inboxKeep {
		b.all = b.all[len(b.all)-inboxKeep:]
	}
	close(b.wake)
	b.wake = make(chan struct{})
}

// after is what came after seq, up to max, waiting up to wait for
// something when nothing did; last is the sequence number to ask after
// next.
func (b *inbox) after(ctx context.Context, seq int64, max int, wait time.Duration) (out []message, last int64) {
	deadline := time.NewTimer(wait)
	defer deadline.Stop()
	for {
		b.mu.Lock()
		if seq > b.seq {
			seq = 0 // from a run whose clock was ahead: everything kept
		}
		for _, m := range b.all {
			if m.Seq > seq && len(out) < max {
				out = append(out, m)
			}
		}
		wake := b.wake
		b.mu.Unlock()
		if len(out) > 0 {
			return out, out[len(out)-1].Seq
		}
		select {
		case <-wake:
		case <-deadline.C:
			return nil, seq
		case <-ctx.Done():
			return nil, seq
		}
	}
}
