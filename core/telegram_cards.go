package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"
)

// The conversation's cards on Telegram (convo_cards.go): a card goes there
// after the message it is attached to, when that message went there (a
// reply to what the user wrote from Telegram, or a notification), with Oui
// and Non buttons. A button answers its card only, through the same code
// as the workspace's, and only from the linked account (onCallback checks
// the sender before anything). "Alerts only" sends no card: a button must
// never approve what the user cannot read. A task's permission never comes
// here: that policy is unchanged.

func tgMsgKey(seq int) string    { return fmt.Sprintf("tg_msg:%d", seq) }
func tgNotifKey(id int64) string { return fmt.Sprintf("tg_notif:%d", id) }
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

// sentToTelegram tells whether a message of the brain's went to Telegram.
func (c *core) sentToTelegram(seq int) bool {
	if c.store.get(tgMsgKey(seq)) != "" {
		return true
	}
	var id int64
	if c.store.db.QueryRow(`SELECT id FROM notifications WHERE msg = ?`, seq).Scan(&id) != nil {
		return false
	}
	return c.store.get(tgNotifKey(id)) != ""
}

// telegramCards sends the cards still waiting whose message went to Telegram.
func (c *core) telegramCards() {
	t := c.tg
	if t == nil || !t.linked() || t.detail() == detailAlert {
		return
	}
	xs, err := c.cardsWhere(`seq > 0 AND tg = 0 AND state = ? AND kind != ? AND attached >= ? ORDER BY id`,
		cardPending, cardDigest, time.Now().Add(-24*time.Hour).Unix())
	if err != nil {
		return
	}
	lang := t.lang()
	for _, x := range xs {
		if !c.sentToTelegram(x.Seq) {
			continue
		}
		x, p := c.liveCard(x)
		c.store.db.Exec(`UPDATE convo_cards SET tg = 1 WHERE id = ?`, x.ID)
		if x.State != cardPending {
			continue
		}
		t.post(cardTelegramText(x, p, lang), &tgKeyboard{Rows: [][]tgButton{{
			{Text: "✓ " + tr(lang, "card.yes"), Data: cardData(x.ID, true)},
			{Text: "✕ " + tr(lang, "card.no"), Data: cardData(x.ID, false)},
		}}})
	}
}

// cardTelegramText is a card as Telegram shows it, in plain text.
func cardTelegramText(x convoCard, p proposal, lang string) string {
	text := tr(lang, "card.kind."+x.Kind) + "\n" + truncate(p.Text(lang), 600)
	if p.Why != "" {
		text += "\n" + truncate(p.Why, 300)
	}
	if p.Tainted != "" {
		text += "\n(" + tr(lang, "notif.tainted") + ")"
	}
	return text + "\n\n" + tr(lang, "card.tg.change")
}

// onCardButton answers a card from Telegram; the sender was checked.
func (t *telegram) onCardButton(ctx context.Context, token string, q *tgCallback, id int64, yes bool) {
	lang := t.lang()
	x, err := t.c.decideCard(ctx, id, yes, "telegram")
	answer := tr(lang, "card.did."+x.State)
	switch {
	case errors.Is(err, errCardDecided):
		answer = tr(lang, "card.already")
	case err != nil:
		answer = tr(lang, "card.tg.failed")
		log.Printf("telegram: card %d: %v", id, err)
	}
	if err := t.call(ctx, token, "answerCallbackQuery", map[string]any{"callback_query_id": q.ID, "text": answer}, nil); err != nil {
		log.Printf("%v", err)
	}
	// The card's message says what was decided, without its buttons.
	if q.Message != nil && q.Message.MessageID != 0 && x.State != cardPending {
		text := strings.TrimSpace(q.Message.Text) + "\n\n→ " + tr(lang, "card.state."+x.State)
		if err := t.call(ctx, token, "editMessageText", map[string]any{"chat_id": q.Message.Chat.ID, "message_id": q.Message.MessageID,
			"text": truncate(text, tgTextMax)}, nil); err != nil {
			log.Printf("%v", err)
		}
	}
}
