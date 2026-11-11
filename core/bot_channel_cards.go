package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
)

// Buttons on a bot channel (bot_channel.go): a card of the conversation's
// (convo_cards.go), sent after the message it is attached to
// (channel_relay.go), with Oui and Non; a task's permission that waits,
// with Allow and Deny; and a notification's feedback. A button answers its
// card, its permission or its notification only, through the same code as
// the workspace's, and only from the linked account (onButton checks the
// sender before anything): the user's own answer. "Alerts only" sends no
// card and no permission's buttons: a button must never approve what the
// user cannot read.

func cardData(id int64, yes bool) string {
	if yes {
		return fmt.Sprintf("pc:%d:y", id)
	}
	return fmt.Sprintf("pc:%d:n", id)
}

// parseCardData reads a card's button: pc:<card id>:<y|n>.
func parseCardData(s string) (int64, bool, bool) {
	parts := strings.Split(s, ":")
	if len(parts) != 3 || parts[0] != "pc" || (parts[2] != "y" && parts[2] != "n") {
		return 0, false, false
	}
	id, err := strconv.ParseInt(parts[1], 10, 64)
	return id, parts[2] == "y", err == nil && id > 0
}

// permData is a permission's button: pm:<task>:<permission>:<y|n>. One
// longer than the service's DataMax has no buttons (compose).
func permData(p notePerm, allow bool) string {
	a := "n"
	if allow {
		a = "y"
	}
	return "pm:" + p.Task + ":" + p.ID + ":" + a
}

func parsePermData(s string) (notePerm, bool, bool) {
	parts := strings.Split(s, ":")
	if len(parts) != 4 || parts[0] != "pm" || parts[1] == "" || parts[2] == "" || (parts[3] != "y" && parts[3] != "n") {
		return notePerm{}, false, false
	}
	return notePerm{Task: parts[1], ID: parts[2]}, parts[3] == "y", true
}

// A feedback button's data: fb:<notification id>:<up|down|later>.
func feedbackData(id int64, fb string) string { return fmt.Sprintf("fb:%d:%s", id, fb) }

func parseFeedbackData(s string) (int64, string, bool) {
	parts := strings.Split(s, ":")
	if len(parts) != 3 || parts[0] != "fb" || !containsStr(feedbacks, parts[2]) {
		return 0, "", false
	}
	id, err := strconv.ParseInt(parts[1], 10, 64)
	return id, parts[2], err == nil && id > 0
}

// card sends a card with its Oui and Non.
func (b *botChannel) card(x convoCard, p proposal) {
	lang := b.lang()
	b.post(botMessage{Text: cardText(x, p, lang), Buttons: [][]botButton{{
		{Text: "✓ " + tr(lang, "card.yes"), Data: cardData(x.ID, true)},
		{Text: "✕ " + tr(lang, "card.no"), Data: cardData(x.ID, false)},
	}}})
}

// onButton: a button, from the linked account only: a card's, a task's
// permission's, or a notification's feedback.
func (b *botChannel) onButton(ctx context.Context, token string, ev botEvent) {
	if !b.isUser(ev.User) {
		b.ignore()
		return
	}
	if id, yes, ok := parseCardData(ev.Data); ok {
		b.onCardButton(ctx, token, ev, id, yes)
		return
	}
	if p, allow, ok := parsePermData(ev.Data); ok {
		b.onPermButton(ctx, token, ev, p, allow)
		return
	}
	answer := ""
	if id, fb, ok := parseFeedbackData(ev.Data); ok {
		if _, err := b.c.notificationFeedback(id, fb); err == nil {
			answer = tr(b.lang(), "notif.fb.thanks")
		} else {
			answer = tr(b.lang(), "notif.fb.failed")
		}
	}
	b.answered(ctx, token, ev, answer, "")
}

// onCardButton answers a card; the sender was checked.
func (b *botChannel) onCardButton(ctx context.Context, token string, ev botEvent, id int64, yes bool) {
	lang := b.lang()
	x, err := b.c.decideCard(ctx, id, yes, b.spec.ID)
	answer := tr(lang, "card.did."+x.State)
	switch {
	case errors.Is(err, errCardDecided):
		answer = tr(lang, "card.already")
	case err != nil:
		answer = tr(lang, "card.channel.failed")
		log.Printf("%s: card %d: %v", b.spec.ID, id, err)
	}
	done := ""
	if x.State != cardPending {
		done = tr(lang, "card.state."+x.State)
	}
	b.answered(ctx, token, ev, answer, done)
}

// onPermButton answers a task's permission; the sender was checked.
func (b *botChannel) onPermButton(ctx context.Context, token string, ev botEvent, p notePerm, allow bool) {
	lang := b.lang()
	err := b.c.answerPermission(ctx, p, allow)
	answer := tr(lang, "perm.denied")
	if allow {
		answer = tr(lang, "perm.allowed")
	}
	done := answer
	switch {
	case errors.Is(err, errPermGone):
		answer = tr(lang, "perm.changed")
	case err != nil:
		answer = tr(lang, "card.channel.failed")
		log.Printf("%s: permission %s/%s: %v", b.spec.ID, p.Task, p.ID, err)
	}
	if err != nil {
		done = ""
	} else {
		b.c.ping()
	}
	b.answered(ctx, token, ev, answer, done)
}

// answered tells the button's answer, and once decided (done), says so on
// the message, without its buttons.
func (b *botChannel) answered(ctx context.Context, token string, ev botEvent, answer, done string) {
	if err := b.tr.answer(ctx, token, ev, answer, done); err != nil {
		log.Printf("%v", scrub(err, token))
	}
}
