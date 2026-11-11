package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Telegram, a bot channel's transport (bot_channel.go): the Bot API, as
// much of it as is used. The user makes a bot with BotFather; eriphe-core
// long-polls getUpdates (no port is opened), sends with sendMessage and its
// inline keyboard, and answers a button with answerCallbackQuery, then
// editMessageText once decided. The linking code also comes as
// "/start CODE", from the t.me link. Telegram bots are not end-to-end
// encrypted (a bot chat is never a secret chat): Settings says so.

const (
	tgChannel   = "telegram" // its channel's id, and its line's (channels.go)
	viaTelegram = "Telegram" // what the user writes there is marked with it
)

var (
	telegramAPI   = "https://api.telegram.org"
	tgPollTimeout = 50 // seconds a getUpdates waits for something
)

func init() {
	registerBot(botSpec{ID: tgChannel, Label: viaTelegram, Icon: "send",
		TextMax: 4000, // Telegram takes 4096 characters
		DataMax: 64,   // and 64 bytes of a button's data
		Token:   regexp.MustCompile(`^[0-9]{3,20}:[A-Za-z0-9_-]{20,80}$`), Placeholder: "123456:ABC…",
		OpenLink: func(bot, code string) string { return "https://t.me/" + bot + "?start=" + code },
		New: func(b *botChannel) botTransport {
			return &telegram{b: b, api: telegramAPI, client: &http.Client{}}
		}})
}

type telegram struct {
	b      *botChannel // its offset, in the channel's state
	api    string
	client *http.Client
}

type tgUser struct {
	ID        int64  `json:"id"`
	IsBot     bool   `json:"is_bot"`
	FirstName string `json:"first_name"`
	Username  string `json:"username"`
}

type tgChat struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
}

type tgMessage struct {
	MessageID int64   `json:"message_id"`
	From      *tgUser `json:"from"`
	Chat      tgChat  `json:"chat"`
	Text      string  `json:"text"`
}

type tgCallback struct {
	ID      string     `json:"id"`
	From    tgUser     `json:"from"`
	Data    string     `json:"data"`
	Message *tgMessage `json:"message"` // the message its button is on
}

type tgUpdate struct {
	UpdateID      int64       `json:"update_id"`
	Message       *tgMessage  `json:"message"`
	CallbackQuery *tgCallback `json:"callback_query"`
}

type tgButton struct {
	Text string `json:"text"`
	Data string `json:"callback_data"`
}

type tgKeyboard struct {
	Rows [][]tgButton `json:"inline_keyboard"`
}

type tgError struct {
	Code       int
	Desc       string
	RetryAfter int
}

func (e *tgError) Error() string { return fmt.Sprintf("telegram: %d %s", e.Code, e.Desc) }

func (e *tgError) retryAfter() time.Duration { return time.Duration(e.RetryAfter) * time.Second }

// call calls a method of the Bot API. The token is in the URL: no error
// it returns names the URL, nor the token.
func (t *telegram) call(ctx context.Context, token, method string, in, out any) error {
	body, _ := json.Marshal(in)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.api+"/bot"+token+"/"+method, bytes.NewReader(body))
	if err != nil {
		return errors.New("telegram: bad request")
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := t.client.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return errors.New("telegram: " + err.Error())
	}
	defer resp.Body.Close()
	var r struct {
		OK          bool            `json:"ok"`
		Result      json.RawMessage `json:"result"`
		ErrorCode   int             `json:"error_code"`
		Description string          `json:"description"`
		Parameters  struct {
			RetryAfter int `json:"retry_after"`
		} `json:"parameters"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&r); err != nil {
		return fmt.Errorf("telegram: %s", resp.Status)
	}
	if !r.OK {
		return &tgError{Code: r.ErrorCode, Desc: truncate(r.Description, 160), RetryAfter: r.Parameters.RetryAfter}
	}
	if out != nil {
		return json.Unmarshal(r.Result, out)
	}
	return nil
}

func (t *telegram) check(ctx context.Context, token string) (string, string, error) {
	var me tgUser
	if err := t.call(ctx, token, "getMe", map[string]any{}, &me); err != nil {
		return "", "", err
	}
	if !me.IsBot {
		return "", "", errors.New("not a bot token")
	}
	return me.Username, tgID(me.ID), nil
}

// listen is one getUpdates; the offset moves past each update handled.
func (t *telegram) listen(ctx context.Context, token string, on func(botEvent)) error {
	ctx, cancel := context.WithTimeout(ctx, time.Duration(tgPollTimeout+15)*time.Second)
	defer cancel()
	var updates []tgUpdate
	if err := t.call(ctx, token, "getUpdates", map[string]any{"offset": t.b.getInt("offset"), "timeout": tgPollTimeout,
		"allowed_updates": []string{"message", "callback_query"}}, &updates); err != nil {
		return err
	}
	for _, u := range updates {
		if ev, ok := tgEvent(u); ok {
			on(ev)
		}
		t.b.set("offset", strconv.FormatInt(u.UpdateID+1, 10))
	}
	return nil
}

// tgEvent is an update as a bot channel's event.
func tgEvent(u tgUpdate) (botEvent, bool) {
	if q := u.CallbackQuery; q != nil {
		return botEvent{Button: true, User: tgID(q.From.ID), Data: q.Data, Ref: q}, true
	}
	m := u.Message
	if m == nil {
		return botEvent{}, false
	}
	ev := botEvent{Chat: tgID(m.Chat.ID), Private: m.Chat.Type == "private", Text: m.Text}
	if m.From != nil {
		ev.User, ev.FromBot, ev.Name = tgID(m.From.ID), m.From.IsBot, m.From.FirstName
		if m.From.Username != "" {
			ev.Name = "@" + m.From.Username
		}
	}
	if text := strings.TrimSpace(m.Text); text == "/start" || strings.HasPrefix(text, "/start ") {
		ev.Start, ev.Text = true, strings.TrimSpace(strings.TrimPrefix(text, "/start"))
	}
	return ev, true
}

func tgID(id int64) string {
	if id == 0 {
		return ""
	}
	return strconv.FormatInt(id, 10)
}

func (t *telegram) send(ctx context.Context, token, chat string, m botMessage) error {
	id, err := strconv.ParseInt(chat, 10, 64)
	if err != nil {
		return errors.New("telegram: bad chat")
	}
	in := map[string]any{"chat_id": id, "text": m.Text, "link_preview_options": map[string]bool{"is_disabled": true}}
	if len(m.Buttons) > 0 {
		kb := &tgKeyboard{}
		for _, row := range m.Buttons {
			var r []tgButton
			for _, b := range row {
				r = append(r, tgButton{Text: b.Text, Data: b.Data})
			}
			kb.Rows = append(kb.Rows, r)
		}
		in["reply_markup"] = kb
	}
	return t.call(ctx, token, "sendMessage", in, nil)
}

// answer answers the callback, and once done, adds it under the message,
// which loses its buttons.
func (t *telegram) answer(ctx context.Context, token string, ev botEvent, toast, done string) error {
	q, _ := ev.Ref.(*tgCallback)
	if q == nil {
		return errors.New("telegram: not a button")
	}
	err := t.call(ctx, token, "answerCallbackQuery", map[string]any{"callback_query_id": q.ID, "text": toast}, nil)
	if q.Message != nil && q.Message.MessageID != 0 && done != "" {
		text := strings.TrimSpace(q.Message.Text) + "\n\n→ " + done
		err = errors.Join(err, t.call(ctx, token, "editMessageText", map[string]any{"chat_id": q.Message.Chat.ID,
			"message_id": q.Message.MessageID, "text": truncate(text, t.b.spec.TextMax)}, nil))
	}
	return err
}
