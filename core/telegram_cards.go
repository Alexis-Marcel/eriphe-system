package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
)

// Buttons on Telegram: a card of the conversation's (convo_cards.go), sent
// after the message it is attached to (channel_relay.go), with Oui and
// Non; and a task's permission that waits, with Allow and Deny. A button
// answers its card or its permission only, through the same code as the
// workspace's, and only from the linked account (onCallback checks the
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

// permData is a permission's button: pm:<task>:<permission>:<y|n>. Telegram
// takes 64 bytes of data: a longer one has no buttons (compose).
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

// card sends a card with its Oui and Non.
func (t *telegram) card(x convoCard, p proposal) {
	lang := t.lang()
	t.post(cardText(x, p, lang), &tgKeyboard{Rows: [][]tgButton{{
		{Text: "✓ " + tr(lang, "card.yes"), Data: cardData(x.ID, true)},
		{Text: "✕ " + tr(lang, "card.no"), Data: cardData(x.ID, false)},
	}}})
}

// onCardButton answers a card from Telegram; the sender was checked.
func (t *telegram) onCardButton(ctx context.Context, token string, q *tgCallback, id int64, yes bool) {
	lang := t.lang()
	x, err := t.c.decideCard(ctx, id, yes, tgChannel)
	answer := tr(lang, "card.did."+x.State)
	switch {
	case errors.Is(err, errCardDecided):
		answer = tr(lang, "card.already")
	case err != nil:
		answer = tr(lang, "card.channel.failed")
		log.Printf("telegram: card %d: %v", id, err)
	}
	t.answered(ctx, token, q, answer, x.State != cardPending, tr(lang, "card.state."+x.State))
}

// onPermButton answers a task's permission from Telegram; the sender was
// checked.
func (t *telegram) onPermButton(ctx context.Context, token string, q *tgCallback, p notePerm, allow bool) {
	lang := t.lang()
	err := t.c.answerPermission(ctx, p, allow)
	answer, state := tr(lang, "perm.denied"), tr(lang, "perm.denied")
	if allow {
		answer, state = tr(lang, "perm.allowed"), tr(lang, "perm.allowed")
	}
	switch {
	case errors.Is(err, errPermGone):
		answer = tr(lang, "perm.changed")
	case err != nil:
		answer = tr(lang, "card.channel.failed")
		log.Printf("telegram: permission %s/%s: %v", p.Task, p.ID, err)
	}
	if err == nil {
		t.c.ping()
	}
	t.answered(ctx, token, q, answer, err == nil, state)
}

// answered tells the button's answer, and once decided, says so under the
// message, without its buttons.
func (t *telegram) answered(ctx context.Context, token string, q *tgCallback, answer string, decided bool, state string) {
	if err := t.call(ctx, token, "answerCallbackQuery", map[string]any{"callback_query_id": q.ID, "text": answer}, nil); err != nil {
		log.Printf("%v", err)
	}
	if q.Message != nil && q.Message.MessageID != 0 && decided {
		text := strings.TrimSpace(q.Message.Text) + "\n\n→ " + state
		if err := t.call(ctx, token, "editMessageText", map[string]any{"chat_id": q.Message.Chat.ID, "message_id": q.Message.MessageID,
			"text": truncate(text, tgTextMax)}, nil); err != nil {
			log.Printf("%v", err)
		}
	}
}
