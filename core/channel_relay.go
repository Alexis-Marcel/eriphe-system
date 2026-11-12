package main

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// What goes back to a channel the user writes from (channels.go: a kind
// with reply): the brain's messages that answer what the user wrote there,
// and the cards that go with them, with Oui and Non. The same for every
// such channel; each says how it sends them.
//
//   - Replies: those after a message of the user's from the channel, up to
//     their next message from anywhere else. A message that went through
//     notify was decided there.
//   - Cards: a card goes there after the message it is attached to, when
//     that message went there (a reply, or a notification), and a card a
//     task waits on goes there whatever its message did. Not with "alerts
//     only": a button must never approve what the user cannot read.
//   - How much a channel is told is the user's, per channel: the text, or
//     only that something waits, with a link (channelDetail).
//
// A channel the user writes from is trusted (its sender is the user's
// account, checked): a button there answers a card as the workspace does.

const (
	detailFull  = "full"
	detailAlert = "alert"

	channelLangKey = "channel_lang" // the language of what the channels send: the user's last in the settings
)

func channelDetailKey(ch string) string       { return "channel_detail:" + ch }
func channelSeenKey(ch string) string         { return "channel_seen:" + ch }  // the last message channelReplies read
func channelRouteKey(ch string) string        { return "channel_route:" + ch } // "1" while the user's last message came from it
func channelMsgKey(ch string, seq int) string { return fmt.Sprintf("channel_msg:%s:%d", ch, seq) }
func channelNotifKey(ch string, id int64) string {
	return fmt.Sprintf("channel_notif:%s:%d", ch, id)
}
func channelCardSentKey(ch string, id int64) string { return fmt.Sprintf("channel_card:%s:%d", ch, id) }

// channelDetail is how much a channel is told: full (the text) or alert.
func (c *core) channelDetail(ch string) string {
	if c.store.get(channelDetailKey(ch)) == detailAlert {
		return detailAlert
	}
	return detailFull
}

func (c *core) setChannelDetail(ch, detail string) error {
	if detail != detailFull && detail != detailAlert {
		return fmt.Errorf("detail is %s or %s", detailFull, detailAlert)
	}
	return c.store.set(channelDetailKey(ch), detail)
}

// textChannels are the channels with a detail: those that carry text
// besides push, which is the user's own devices.
func textChannels() []channelKind {
	var out []channelKind
	for _, k := range channelKinds {
		if k.ID != chanPush && k.Carries == carryText {
			out = append(out, k)
		}
	}
	return out
}

// textChannelIDs are their ids.
func textChannelIDs() []string {
	var out []string
	for _, k := range textChannels() {
		out = append(out, k.ID)
	}
	return out
}

// channelRouting tells whether the user writes from that channel now: the
// brain's reply goes there already.
func (c *core) channelRouting(ch string) bool { return c.store.get(channelRouteKey(ch)) == "1" }

// channelNoteSent marks a notification sent on a channel: its message's
// cards follow it there.
func (c *core) channelNoteSent(ch string, notif int64) {
	if notif > 0 {
		c.store.set(channelNotifKey(ch, notif), "1")
	}
}

// sentOn tells whether a message of the brain's went to that channel.
func (c *core) sentOn(ch string, seq int) bool {
	if c.store.get(channelMsgKey(ch, seq)) != "" {
		return true
	}
	var id int64
	if c.store.db.QueryRow(`SELECT id FROM notifications WHERE msg = ?`, seq).Scan(&id) != nil {
		return false
	}
	return c.store.get(channelNotifKey(ch, id)) != ""
}

// channelReplies sends each channel the brain's messages that answer what
// the user wrote there.
func (c *core) channelReplies() {
	for _, k := range channelKinds {
		if k.reply != nil && k.ready != nil && k.ready(c) {
			c.channelRepliesOn(k)
		}
	}
}

func (c *core) channelRepliesOn(k channelKind) {
	st := c.store
	seen, err := strconv.Atoi(st.get(channelSeenKey(k.ID)))
	if err != nil {
		st.set(channelSeenKey(k.ID), strconv.Itoa(st.lastMessageSeq())) // from now on
		return
	}
	c.relayMu.Lock() // a brain message being written is marked first (brainNotify)
	defer c.relayMu.Unlock()
	msgs, err := st.messagesAfter(seen, 50)
	if err != nil || len(msgs) == 0 {
		return
	}
	route := c.channelRouting(k.ID)
	for _, m := range msgs {
		switch m.Role {
		case roleUser:
			route = m.Via == k.Label
		case roleBrain:
			if route && strings.TrimSpace(m.Text) != "" && st.get(notifiedKey(m.Seq)) == "" {
				st.set(channelMsgKey(k.ID, m.Seq), "1") // its cards follow it there
				k.reply(c, m)
			}
		}
	}
	if route {
		st.set(channelRouteKey(k.ID), "1")
	} else {
		st.set(channelRouteKey(k.ID), "")
	}
	st.set(channelSeenKey(k.ID), strconv.Itoa(msgs[len(msgs)-1].Seq))
}

// blocksATask tells whether a card stops a task, or a work session, until
// it is answered: it goes to the channels whatever its message did.
func blocksATask(kind string) bool {
	return kind == cardTaskStart || kind == cardAccess || kind == cardSession
}

// channelCards sends each channel the cards still waiting whose message
// went there.
func (c *core) channelCards() {
	var on []channelKind
	for _, k := range channelKinds {
		if k.card != nil && k.ready != nil && k.ready(c) && c.channelDetail(k.ID) == detailFull {
			on = append(on, k)
		}
	}
	if len(on) == 0 {
		return
	}
	xs, err := c.cardsWhere(`seq > 0 AND state = ? AND kind != ? AND attached >= ? ORDER BY id`,
		cardPending, cardDigest, time.Now().Add(-24*time.Hour).Unix())
	if err != nil {
		return
	}
	for _, k := range on {
		for _, x := range xs {
			if c.store.get(channelCardSentKey(k.ID, x.ID)) != "" || !blocksATask(x.Kind) && !c.sentOn(k.ID, x.Seq) {
				continue
			}
			x, p := c.liveCard(x)
			c.store.set(channelCardSentKey(k.ID, x.ID), "1")
			if x.State == cardPending {
				k.card(c, x, p)
			}
		}
	}
}

// cardText is a card in plain text, for a channel that shows it with Oui
// and Non buttons.
func cardText(x convoCard, p proposal, lang string) string {
	text := tr(lang, "card.kind."+x.Kind) + "\n" + truncate(p.Text(lang), 600)
	for _, f := range p.FieldsIn(lang) {
		text += "\n" + f.Name + " : " + truncate(oneLine(f.Value), 200)
	}
	if p.Why != "" {
		text += "\n" + truncate(p.Why, 300)
	}
	if p.Tainted != "" {
		text += "\n(" + tr(lang, "notif.tainted") + ")"
	}
	return text + "\n\n" + tr(lang, "card.channel.change")
}

// errPermGone: the permission no longer waits (answered elsewhere, or the
// task moved on).
var errPermGone = errors.New("the permission no longer waits")

// answerPermission answers a task's permission from a channel the user
// writes from: the user's own answer, as in the workspace.
func (c *core) answerPermission(ctx context.Context, p notePerm, allow bool) error {
	st, err := c.status(p.Task, 0)
	if err != nil {
		return err
	}
	if !hasPending(st, p.ID) {
		return errPermGone
	}
	_, err = c.answer(ctx, p.Task, p.ID, "", &allow, byUser)
	return err
}
