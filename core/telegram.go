package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Telegram, a channel of notify the user can answer from. The user makes a
// bot with BotFather and pastes its token in Settings → Channels (kept in
// the vault, never shown or logged again); eriphe-core shows a one-time
// code, the user sends it to the bot, and the Telegram account that sent
// it is the only one ever heard. Everything else is ignored and counted,
// never answered.
//
// No port is opened: eriphe-core long-polls getUpdates, with a growing
// wait after a failure, and only while a token is set.
//
//   - Out: what tell sends (channels.go) — a notification with its
//     👍 / 👎 / « pas maintenant » buttons, a question or a permission that
//     waits for the user — and the brain's replies to what the user wrote
//     from Telegram. "Alerts only" sends a nudge and a link instead of the
//     words: Telegram bots are not end-to-end encrypted.
//   - In: a text from the linked account joins the conversation as the
//     user's own message, marked "· Telegram"; it is the user speaking
//     (a clean turn) because the sender's id is checked. Files and voice
//     notes are not read: a short note says so.
//   - A proposal of the brain's (a rule, a memory, a watch…) that goes
//     with a message sent there comes as a card with Oui / Non buttons,
//     which answer that proposal alone, from the linked account only
//     (telegram_cards.go). A permission of a task's is never approvable
//     from Telegram, a user_only one least of all: the message says what
//     waits and links to the workspace. The other buttons are a
//     notification's feedback.

const (
	telegramSecret = "telegram-bot" // the bot's token, in the vault

	tgUserKey    = "telegram_user"    // the linked account's id
	tgChatKey    = "telegram_chat"    // its private chat with the bot
	tgNameKey    = "telegram_name"    // its @username or first name, for Settings
	tgBotKey     = "telegram_bot"     // the bot's @username
	tgCodeKey    = "telegram_code"    // the linking code: CODE|expiry|tries
	tgOffsetKey  = "telegram_offset"  // the next update to read
	tgIgnoredKey = "telegram_ignored" // what came from anyone else, counted
	tgDetailKey  = "telegram_detail"  // full or alert
	tgLangKey    = "telegram_lang"    // the language of what it sends
	tgSeenKey    = "telegram_seen"    // the last message telegramReplies read
	tgRouteKey   = "telegram_route"   // "1" while the user's last message came from Telegram

	viaTelegram = "Telegram"

	detailFull  = "full"
	detailAlert = "alert"

	tgCodeTTL   = 10 * time.Minute
	tgCodeTries = 10   // wrong codes before the code is dropped
	tgTextMax   = 4000 // Telegram takes 4096 characters
)

var (
	telegramAPI   = "https://api.telegram.org"
	tgPollTimeout = 50 // seconds a getUpdates waits for something
	tgBackoffMin  = time.Second
	tgBackoffMax  = 5 * time.Minute
	tgTokenRe     = regexp.MustCompile(`^[0-9]{3,20}:[A-Za-z0-9_-]{20,80}$`)
	tgCodeChars   = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
)

type telegram struct {
	c       *core
	api     string
	client  *http.Client
	base    func() string // the workspace's address, for links; "" when none is known
	sleep   func(ctx context.Context, d time.Duration) bool
	pending sync.WaitGroup // messages on their way (tests wait for them)

	mu      sync.Mutex
	cancel  context.CancelFunc
	done    chan struct{}
	lastErr string

	qmu     sync.Mutex
	queue   []tgOut // what post sends, in order
	sending bool
}

func newTelegram(c *core) *telegram {
	return &telegram{c: c, api: telegramAPI, client: &http.Client{}, base: func() string { return "" }, sleep: sleepCtx}
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}

// The Bot API, as much of it as is used.

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
		return scrubError(err, token)
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
		desc := strings.ReplaceAll(truncate(r.Description, 160), token, "…")
		return &tgError{Code: r.ErrorCode, Desc: desc, RetryAfter: r.Parameters.RetryAfter}
	}
	if out != nil {
		return json.Unmarshal(r.Result, out)
	}
	return nil
}

func scrubError(err error, token string) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		err = ue.Err
	}
	msg := err.Error()
	if token != "" {
		msg = strings.ReplaceAll(msg, token, "…")
	}
	return errors.New("telegram: " + msg)
}

// The state, in the store; the token, in the vault.

func (t *telegram) token() string {
	v := currentVault()
	if v == nil {
		return ""
	}
	tok, _ := v.get(telegramSecret)
	return tok
}

func (t *telegram) kvInt(key string) int64 {
	n, _ := strconv.ParseInt(t.c.store.get(key), 10, 64)
	return n
}

func (t *telegram) linked() bool         { return t.kvInt(tgUserKey) != 0 && t.kvInt(tgChatKey) != 0 }
func (t *telegram) isUser(id int64) bool { return id != 0 && id == t.kvInt(tgUserKey) }
func (t *telegram) routing() bool        { return t.c.store.get(tgRouteKey) == "1" }

func (t *telegram) detail() string {
	if t.c.store.get(tgDetailKey) == detailAlert {
		return detailAlert
	}
	return detailFull
}

func (t *telegram) lang() string {
	if t.c.store.get(tgLangKey) == "en" {
		return "en"
	}
	return "fr"
}

func (t *telegram) ignore() {
	t.c.store.set(tgIgnoredKey, strconv.FormatInt(t.kvInt(tgIgnoredKey)+1, 10))
}

func (t *telegram) setErr(s string) {
	t.mu.Lock()
	t.lastErr = s
	t.mu.Unlock()
}

func (t *telegram) err() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.lastErr
}

// setToken checks a bot's token with Telegram and keeps it in the vault. A
// new bot is a new chat: the link is made again.
func (t *telegram) setToken(ctx context.Context, token string) error {
	token = strings.TrimSpace(token)
	if !tgTokenRe.MatchString(token) {
		return errors.New("not a bot token")
	}
	v := currentVault()
	if v == nil {
		return errNoVault
	}
	var me tgUser
	if err := t.call(ctx, token, "getMe", map[string]any{}, &me); err != nil {
		return err
	}
	if !me.IsBot {
		return errors.New("not a bot token")
	}
	if old := t.token(); old != token {
		t.stop()
		if err := v.set(telegramSecret, token); err != nil {
			return err
		}
		t.forget(tgUserKey, tgChatKey, tgNameKey, tgCodeKey, tgOffsetKey, tgRouteKey, tgIgnoredKey)
	}
	t.c.store.set(tgBotKey, me.Username)
	log.Printf("telegram: bot @%s set", me.Username)
	t.restart()
	return nil
}

// removeToken stops the channel and forgets the bot and the link.
func (t *telegram) removeToken() {
	t.stop()
	if v := currentVault(); v != nil {
		v.remove(telegramSecret)
	}
	t.forget(tgUserKey, tgChatKey, tgNameKey, tgCodeKey, tgOffsetKey, tgRouteKey, tgBotKey, tgIgnoredKey)
	t.setErr("")
	log.Printf("telegram: bot removed")
}

func (t *telegram) unlink() {
	t.forget(tgUserKey, tgChatKey, tgNameKey, tgRouteKey)
	log.Printf("telegram: account unlinked")
}

func (t *telegram) forget(keys ...string) {
	for _, k := range keys {
		t.c.store.set(k, "")
	}
}

// newCode makes the one-time code the user sends to the bot to link their
// account, valid tgCodeTTL, replacing any other.
func (t *telegram) newCode(lang string) string {
	b := make([]byte, 8)
	rand.Read(b)
	for i := range b {
		b[i] = tgCodeChars[int(b[i])%len(tgCodeChars)]
	}
	code := string(b)
	t.c.store.set(tgCodeKey, fmt.Sprintf("%s|%d|0", code, time.Now().Add(tgCodeTTL).Unix()))
	if lang == "en" || lang == "fr" {
		t.c.store.set(tgLangKey, lang)
	}
	return code
}

// pendingCode is the code waiting to be sent, and how often a wrong one was.
func (t *telegram) pendingCode() (code string, tries int) {
	parts := strings.Split(t.c.store.get(tgCodeKey), "|")
	if len(parts) != 3 {
		return "", 0
	}
	exp, _ := strconv.ParseInt(parts[1], 10, 64)
	tries, _ = strconv.Atoi(parts[2])
	if time.Now().Unix() > exp || tries >= tgCodeTries {
		return "", 0
	}
	return parts[0], tries
}

// The poller.

// restart stops the poller, and starts it again while a token is set.
func (t *telegram) restart() {
	t.stop()
	token := t.token()
	if token == "" {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	t.mu.Lock()
	t.cancel, t.done = cancel, done
	t.mu.Unlock()
	go func() {
		defer close(done)
		t.poll(ctx, token)
	}()
}

// stop stops the poller and waits for it.
func (t *telegram) stop() {
	t.mu.Lock()
	cancel, done := t.cancel, t.done
	t.cancel, t.done = nil, nil
	t.mu.Unlock()
	if cancel != nil {
		cancel()
		<-done
	}
}

func (t *telegram) running() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.cancel != nil
}

func (t *telegram) poll(ctx context.Context, token string) {
	var wait time.Duration
	for {
		if wait > 0 && !t.sleep(ctx, wait) {
			return
		}
		if ctx.Err() != nil {
			return
		}
		updates, err := t.getUpdates(ctx, token)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			wait = nextBackoff(wait, err)
			t.setErr(err.Error())
			log.Printf("%v; again in %s", err, wait)
			continue
		}
		wait = 0
		t.setErr("")
		for _, u := range updates {
			t.handle(ctx, token, u)
			t.c.store.set(tgOffsetKey, strconv.FormatInt(u.UpdateID+1, 10))
		}
	}
}

// nextBackoff is the wait after a failure: what Telegram asks for, else
// twice the last one, between tgBackoffMin and tgBackoffMax.
func nextBackoff(prev time.Duration, err error) time.Duration {
	var te *tgError
	if errors.As(err, &te) && te.RetryAfter > 0 {
		return min(time.Duration(te.RetryAfter)*time.Second, tgBackoffMax)
	}
	if prev < tgBackoffMin {
		return tgBackoffMin
	}
	return min(prev*2, tgBackoffMax)
}

func (t *telegram) getUpdates(ctx context.Context, token string) ([]tgUpdate, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Duration(tgPollTimeout+15)*time.Second)
	defer cancel()
	var out []tgUpdate
	err := t.call(ctx, token, "getUpdates", map[string]any{"offset": t.kvInt(tgOffsetKey), "timeout": tgPollTimeout,
		"allowed_updates": []string{"message", "callback_query"}}, &out)
	return out, err
}

func (t *telegram) handle(ctx context.Context, token string, u tgUpdate) {
	switch {
	case u.CallbackQuery != nil:
		t.onCallback(ctx, token, u.CallbackQuery)
	case u.Message != nil:
		t.onMessage(ctx, token, u.Message)
	}
}

// onMessage: the linked account's text joins the conversation; a code may
// link an account; anything else is ignored, and counted.
func (t *telegram) onMessage(ctx context.Context, token string, m *tgMessage) {
	if m.From == nil || m.From.IsBot || m.Chat.Type != "private" {
		t.ignore()
		return
	}
	if !t.linked() {
		t.tryLink(ctx, token, m)
		return
	}
	if !t.isUser(m.From.ID) {
		t.ignore()
		return
	}
	lang := t.lang()
	text := strings.TrimSpace(m.Text)
	switch {
	case text == "": // a photo, a voice note, a file, a sticker
		t.send(ctx, token, m.Chat.ID, tr(lang, "tg.textonly"), nil)
	case text == "/start" || strings.HasPrefix(text, "/start "):
		t.send(ctx, token, m.Chat.ID, tr(lang, "tg.already"), nil)
	default:
		seq, err := t.c.store.addMessageVia(roleUser, text, viaTelegram, false)
		if err != nil {
			log.Printf("telegram: %v", err)
			return
		}
		t.c.resolveByText(ctx, int(seq)) // the sender is checked: a plain yes or no may answer a card
		t.c.ping()
	}
}

// tryLink links the account that sends the pending code, "/start CODE" too.
func (t *telegram) tryLink(ctx context.Context, token string, m *tgMessage) {
	code, tries := t.pendingCode()
	if code == "" {
		t.ignore()
		return
	}
	got := strings.ToUpper(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(m.Text), "/start")))
	if subtle.ConstantTimeCompare([]byte(got), []byte(code)) != 1 {
		t.ignore()
		parts := strings.Split(t.c.store.get(tgCodeKey), "|")
		t.c.store.set(tgCodeKey, fmt.Sprintf("%s|%s|%d", parts[0], parts[1], tries+1))
		return
	}
	name := m.From.FirstName
	if m.From.Username != "" {
		name = "@" + m.From.Username
	}
	st := t.c.store
	st.set(tgUserKey, strconv.FormatInt(m.From.ID, 10))
	st.set(tgChatKey, strconv.FormatInt(m.Chat.ID, 10))
	st.set(tgNameKey, truncate(name, 64))
	st.set(tgCodeKey, "")
	st.set(tgRouteKey, "")
	st.set(tgSeenKey, strconv.Itoa(st.lastMessageSeq()))
	log.Printf("telegram: account linked")
	t.send(ctx, token, m.Chat.ID, tr(t.lang(), "tg.linked"), nil)
}

// onCallback: a notification's button, from the linked account only. No
// other button exists: nothing is approved from Telegram.
func (t *telegram) onCallback(ctx context.Context, token string, q *tgCallback) {
	if !t.isUser(q.From.ID) {
		t.ignore()
		return
	}
	lang, answer := t.lang(), ""
	if id, yes, ok := parseCardData(q.Data); ok {
		t.onCardButton(ctx, token, q, id, yes)
		return
	}
	if id, fb, ok := parseFeedbackData(q.Data); ok {
		if _, err := t.c.notificationFeedback(id, fb); err == nil {
			answer = tr(lang, "notif.fb.thanks")
		} else {
			answer = tr(lang, "notif.fb.failed")
		}
	}
	if err := t.call(ctx, token, "answerCallbackQuery", map[string]any{"callback_query_id": q.ID, "text": answer}, nil); err != nil {
		log.Printf("%v", err)
	}
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

// Out.

func (t *telegram) send(ctx context.Context, token string, chat int64, text string, kb *tgKeyboard) error {
	in := map[string]any{"chat_id": chat, "text": truncate(text, tgTextMax), "link_preview_options": map[string]bool{"is_disabled": true}}
	if kb != nil {
		in["reply_markup"] = kb
	}
	return t.call(ctx, token, "sendMessage", in, nil)
}

// tgOut is a message waiting to be sent.
type tgOut struct {
	token string
	chat  int64
	text  string
	kb    *tgKeyboard
}

// post sends to the linked chat in the background, in order: one sender
// at a time drains the queue.
func (t *telegram) post(text string, kb *tgKeyboard) {
	token, chat := t.token(), t.kvInt(tgChatKey)
	if token == "" || chat == 0 || !t.linked() {
		return
	}
	t.pending.Add(1)
	t.qmu.Lock()
	t.queue = append(t.queue, tgOut{token, chat, text, kb})
	if !t.sending {
		t.sending = true
		go t.drain()
	}
	t.qmu.Unlock()
}

func (t *telegram) drain() {
	for {
		t.qmu.Lock()
		if len(t.queue) == 0 {
			t.sending = false
			t.qmu.Unlock()
			return
		}
		m := t.queue[0]
		t.queue = t.queue[1:]
		t.qmu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		if err := t.send(ctx, m.token, m.chat, m.text, m.kb); err != nil {
			log.Printf("%v", err)
		}
		cancel()
		t.pending.Done()
	}
}

// deliver sends what tell sends: the words and the buttons, or a nudge.
func (t *telegram) deliver(n pushNote) {
	if n.Waiting && t.routing() {
		return // the user writes from Telegram: the brain's reply goes there already
	}
	if n.Notif > 0 {
		t.c.store.set(tgNotifKey(n.Notif), "1") // its message's cards follow it there (telegramCards)
	}
	t.post(t.compose(n))
}

func (t *telegram) compose(n pushNote) (string, *tgKeyboard) {
	lang := t.lang()
	link := t.link(n.URL)
	if t.detail() == detailAlert {
		return joinLines(tr(lang, "tg.alert"), link), nil
	}
	body := n.Body[lang]
	if body == "" {
		body = n.Body["fr"]
	}
	if n.Why != "" {
		body += "\n" + tr(lang, "notif.why") + " " + n.Why
	}
	text := joinLines(truncate(body, tgTextMax-300), link)
	if n.Notif <= 0 {
		return text, nil
	}
	var row []tgButton
	for _, fb := range feedbacks {
		row = append(row, tgButton{Text: tr(lang, "notif.fb."+fb), Data: feedbackData(n.Notif, fb)})
	}
	return text, &tgKeyboard{Rows: [][]tgButton{row}}
}

// link is a workspace page's address, when the workspace's is known.
func (t *telegram) link(path string) string {
	base := strings.TrimRight(t.base(), "/")
	if base == "" || path == "" {
		return ""
	}
	return base + path
}

func joinLines(a, b string) string {
	if b == "" {
		return a
	}
	return a + "\n\n" + b
}

// test sends a test message to the linked chat, now.
func (t *telegram) test(ctx context.Context) error {
	token, chat := t.token(), t.kvInt(tgChatKey)
	if token == "" || !t.linked() {
		return errors.New("not linked")
	}
	return t.send(ctx, token, chat, joinLines(tr(t.lang(), "tg.test"), t.link("/")), nil)
}

// telegramReplies sends to Telegram the brain's messages that answer what
// the user wrote there: those after a message of theirs from Telegram, up
// to their next message from anywhere else. A message that went through
// notify was decided there.
func (c *core) telegramReplies() {
	t := c.tg
	if t == nil || !t.linked() {
		return
	}
	st := c.store
	seen, err := strconv.Atoi(st.get(tgSeenKey))
	if err != nil {
		st.set(tgSeenKey, strconv.Itoa(st.lastMessageSeq())) // from now on
		return
	}
	c.relayMu.Lock() // a brain message being written is marked first (brainNotify)
	defer c.relayMu.Unlock()
	msgs, err := st.messagesAfter(seen, 50)
	if err != nil || len(msgs) == 0 {
		return
	}
	route := t.routing()
	for _, m := range msgs {
		switch m.Role {
		case roleUser:
			route = m.Via == viaTelegram
		case roleBrain:
			if route && strings.TrimSpace(m.Text) != "" && st.get(notifiedKey(m.Seq)) == "" {
				t.reply(m)
			}
		}
	}
	if route {
		st.set(tgRouteKey, "1")
	} else {
		st.set(tgRouteKey, "")
	}
	st.set(tgSeenKey, strconv.Itoa(msgs[len(msgs)-1].Seq))
}

func (t *telegram) reply(m Message) {
	t.c.store.set(tgMsgKey(m.Seq), "1") // its cards follow it there (telegramCards)
	if t.detail() == detailAlert {
		t.post(joinLines(tr(t.lang(), "tg.replied"), t.link(fmt.Sprintf("/#m%d", m.Seq))), nil)
		return
	}
	t.post(m.Text, nil)
}
