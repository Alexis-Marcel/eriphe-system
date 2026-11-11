package main

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"errors"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// A bot channel: a messaging service where the user links their own
// account to a bot of theirs, and talks to Eriphe through it (channels.go).
// Everything that does not depend on the service is here, once; a service
// writes only its transport (botTransport) and its own words,
// and registers itself with registerBot. Nothing outside the transport's
// files names it.
//
//   - Setup. The user makes a bot and pastes its token in Settings →
//     Access: checked by the transport, kept in the vault (<id>-bot),
//     never shown, logged or put in an error. Eriphe then shows a one-time
//     code (botCodeTTL, botCodeTries wrong tries); the private-chat account
//     that sends it to the bot is recorded, user id and chat id. Only that
//     user is heard; any other sender, group or bot is ignored, counted and
//     never answered.
//   - Listening. The transport listens one round at a time (a long poll, a
//     connection until it drops); a failed round waits from botBackoffMin,
//     doubling to botBackoffMax (or what the service asks for), and the
//     listening runs only while a token is set.
//   - Out. What tell sends (channels.go) — a notification with its
//     👍 / 👎 / « pas maintenant » buttons, a question, a task's permission
//     with Allow / Deny — and the brain's replies to what the user wrote
//     there (channel_relay.go). "Alerts only" sends a nudge and a link
//     instead of the words, and no button that approves.
//   - In. A text from the linked account joins the conversation as the
//     user's own message, marked with the channel's label: a clean turn,
//     because the sender's id is checked. Files and voice notes are not
//     read: a short note says so.
//   - Cards. A proposal of the brain's that goes with a message sent there
//     comes with Oui / Non (bot_channel_cards.go), answered from the linked
//     account only, through the same code as the workspace's.

const (
	botCodeTTL   = 10 * time.Minute
	botCodeTries = 10 // wrong codes before the code is dropped
)

var (
	botBackoffMin = time.Second
	botBackoffMax = 5 * time.Minute
	botCodeChars  = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
)

// botSpec is a bot channel as its service registers it.
type botSpec struct {
	ID    string // the channel's id, its line's, its routes' and its keys' prefix
	Label string // its name: what the user writes there is marked with it
	Icon  string // its mark in Settings → Access: "send" or "chat"
	// TextMax is the longest message the service takes; DataMax the
	// longest data a button carries.
	TextMax, DataMax int
	Token            *regexp.Regexp // what a bot's token looks like
	Placeholder      string         // the token field's example
	// OpenLink is a link that opens the bot with the code typed, "" when
	// the service has none.
	OpenLink func(bot, code string) string
	// New is the service's transport, for that channel.
	New func(b *botChannel) botTransport
}

// botTransport is what a messaging service does; everything else is
// botChannel's. No error it returns may carry the token: botChannel strips
// it anyway.
type botTransport interface {
	// check tells whether the token is a bot's, and its name.
	check(ctx context.Context, token string) (bot string, err error)
	// listen is one round of listening: a long poll, or a connection until
	// it drops; each event is handled, in order, before it returns. A nil
	// error is a round that went well.
	listen(ctx context.Context, token string, on func(botEvent)) error
	// send sends a message to a chat.
	send(ctx context.Context, token, chat string, m botMessage) error
	// answer answers a button: a short note to the user, and once done is
	// set, the message it is on says so, without its buttons.
	answer(ctx context.Context, token string, ev botEvent, toast, done string) error
}

// botEvent is what the user did on the service.
type botEvent struct {
	Button  bool   // a button, its Data; else a message, its Text
	User    string // who did it
	Chat    string // where
	Name    string // their name, for Settings: @handle or first name
	Private bool   // in a private chat with the bot
	FromBot bool   // by a bot
	Text    string // a message's text; "" for a file, a photo, a voice note
	Start   bool   // the service's "start" command: Text is what follows it
	Data    string // a button's data
	Ref     any    // what the transport needs to answer it
}

// botMessage is what is sent: a text and its rows of buttons.
type botMessage struct {
	Text    string
	Buttons [][]botButton
}

type botButton struct {
	Text, Data string
}

// retryAfter is an error of a service that says how long to wait.
type retryAfter interface{ retryAfter() time.Duration }

// botSpecs are the bot channels, by id.
var botSpecs = map[string]botSpec{}

// registerBot registers a bot channel (from an init): its channel, routes,
// page, row in Settings → Access, and its start.
func registerBot(s botSpec) {
	botSpecs[s.ID] = s
	id := s.ID
	registerChannel(channelKind{ID: id, Label: s.Label, Dir: dirBoth, Speaker: speakerAccount, Carries: carryText,
		Line: true, Page: "/settings/access/" + id,
		ready: func(c *core) bool { return c.bot(id).linked() },
		deliver: func(c *core, n pushNote) []routeTarget {
			b := c.bot(id)
			if !b.linked() {
				return nil
			}
			b.deliver(n)
			return []routeTarget{{Step: id, Label: s.Label}}
		},
		reach: func(c *core, n pushNote, _ int64, _ routeTarget) {
			if b := c.bot(id); b != nil {
				b.deliver(n)
			}
		},
		reply:  func(c *core, m Message) { c.bot(id).reply(m) },
		card:   func(c *core, x convoCard, p proposal) { c.bot(id).card(x, p) },
		routes: func(w *web, mux *http.ServeMux) { botRoutes(w, mux, id) },
		start:  func(w *web) { startBot(w, id) },
		entry:  func(w *web) (entryRow, bool) { return botEntry(w, id) },
	})
}

// bot is that bot channel's state; nil when it does not run.
func (c *core) bot(id string) *botChannel {
	c.botsMu.Lock()
	defer c.botsMu.Unlock()
	return c.bots[id]
}

func (c *core) setBot(b *botChannel) {
	c.botsMu.Lock()
	defer c.botsMu.Unlock()
	if c.bots == nil {
		c.bots = map[string]*botChannel{}
	}
	c.bots[b.spec.ID] = b
}

// botChannel is a bot channel at work.
type botChannel struct {
	spec    botSpec
	c       *core
	tr      botTransport
	base    func() string // the workspace's address, for links; "" when none is known
	sleep   func(ctx context.Context, d time.Duration) bool
	pending sync.WaitGroup // messages on their way (tests wait for them)

	mu      sync.Mutex
	cancel  context.CancelFunc
	done    chan struct{}
	lastErr string

	qmu     sync.Mutex
	queue   []botOut // what post sends, in order
	sending bool
}

func newBotChannel(c *core, id string) *botChannel {
	s, ok := botSpecs[id]
	if !ok {
		panic("no bot channel " + id)
	}
	b := &botChannel{spec: s, c: c, base: func() string { return "" }, sleep: sleepCtx}
	b.tr = s.New(b)
	return b
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}

// The state, in the store under the channel's id; the token, in the vault.

// key is a key of the channel's in the store: <id>_<name>.
func (b *botChannel) key(name string) string { return b.spec.ID + "_" + name }
func (b *botChannel) secret() string         { return b.spec.ID + "-bot" }

func (b *botChannel) get(name string) string { return b.c.store.get(b.key(name)) }
func (b *botChannel) set(name, v string)     { b.c.store.set(b.key(name), v) }
func (b *botChannel) getInt(name string) int64 {
	n, _ := strconv.ParseInt(b.get(name), 10, 64)
	return n
}
func (b *botChannel) isUser(id string) bool { return id != "" && id == b.get("user") }
func (b *botChannel) routing() bool         { return b.c.channelRouting(b.spec.ID) }
func (b *botChannel) detail() string        { return b.c.channelDetail(b.spec.ID) }

func (b *botChannel) forget(names ...string) {
	for _, n := range names {
		b.set(n, "")
	}
}

// linked tells whether an account is linked; false for a nil channel.
func (b *botChannel) linked() bool { return b != nil && b.get("user") != "" && b.get("chat") != "" }

func (b *botChannel) token() string {
	v := currentVault()
	if v == nil {
		return ""
	}
	tok, _ := v.get(b.secret())
	return tok
}

func (b *botChannel) lang() string {
	if b.c.store.get(channelLangKey) == "en" {
		return "en"
	}
	return "fr"
}

func (b *botChannel) ignore() { b.set("ignored", strconv.FormatInt(b.getInt("ignored")+1, 10)) }

func (b *botChannel) setErr(s string) {
	b.mu.Lock()
	b.lastErr = s
	b.mu.Unlock()
}

func (b *botChannel) err() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.lastErr
}

// scrub takes the token out of a transport's error.
func scrub(err error, token string) error {
	if err == nil || token == "" || !strings.Contains(err.Error(), token) {
		return err
	}
	return errors.New(strings.ReplaceAll(err.Error(), token, "…"))
}

// setToken checks a bot's token with the service and keeps it in the
// vault. A new bot is a new chat: the link is made again.
func (b *botChannel) setToken(ctx context.Context, token string) error {
	token = strings.TrimSpace(token)
	if !b.spec.Token.MatchString(token) {
		return errors.New("not a bot token")
	}
	v := currentVault()
	if v == nil {
		return errNoVault
	}
	name, err := b.tr.check(ctx, token)
	if err != nil {
		return scrub(err, token)
	}
	if old := b.token(); old != token {
		b.stop()
		if err := v.set(b.secret(), token); err != nil {
			return err
		}
		b.forget("user", "chat", "name", "code", "offset", "ignored")
		b.c.store.set(channelRouteKey(b.spec.ID), "")
	}
	b.set("bot", name)
	log.Printf("%s: bot @%s set", b.spec.ID, name)
	b.restart()
	return nil
}

// removeToken stops the channel and forgets the bot and the link.
func (b *botChannel) removeToken() {
	b.stop()
	if v := currentVault(); v != nil {
		v.remove(b.secret())
	}
	b.forget("user", "chat", "name", "code", "offset", "bot", "ignored")
	b.c.store.set(channelRouteKey(b.spec.ID), "")
	b.setErr("")
	log.Printf("%s: bot removed", b.spec.ID)
}

func (b *botChannel) unlink() {
	b.forget("user", "chat", "name")
	b.c.store.set(channelRouteKey(b.spec.ID), "")
	log.Printf("%s: account unlinked", b.spec.ID)
}

// newCode makes the one-time code the user sends to the bot to link their
// account, valid botCodeTTL, replacing any other.
func (b *botChannel) newCode(lang string) string {
	buf := make([]byte, 8)
	rand.Read(buf)
	for i := range buf {
		buf[i] = botCodeChars[int(buf[i])%len(botCodeChars)]
	}
	code := string(buf)
	b.set("code", fmt.Sprintf("%s|%d|0", code, time.Now().Add(botCodeTTL).Unix()))
	if lang == "en" || lang == "fr" {
		b.c.store.set(channelLangKey, lang)
	}
	return code
}

// pendingCode is the code waiting to be sent, and how often a wrong one was.
func (b *botChannel) pendingCode() (code string, tries int) {
	parts := strings.Split(b.get("code"), "|")
	if len(parts) != 3 {
		return "", 0
	}
	exp, _ := strconv.ParseInt(parts[1], 10, 64)
	tries, _ = strconv.Atoi(parts[2])
	if time.Now().Unix() > exp || tries >= botCodeTries {
		return "", 0
	}
	return parts[0], tries
}

// Listening.

// restart stops the listening, and starts it again while a token is set.
func (b *botChannel) restart() {
	b.stop()
	token := b.token()
	if token == "" {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	b.mu.Lock()
	b.cancel, b.done = cancel, done
	b.mu.Unlock()
	go func() {
		defer close(done)
		b.run(ctx, token)
	}()
}

// stop stops the listening and waits for it.
func (b *botChannel) stop() {
	b.mu.Lock()
	cancel, done := b.cancel, b.done
	b.cancel, b.done = nil, nil
	b.mu.Unlock()
	if cancel != nil {
		cancel()
		<-done
	}
}

func (b *botChannel) running() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.cancel != nil
}

func (b *botChannel) run(ctx context.Context, token string) {
	var wait time.Duration
	on := func(ev botEvent) { b.handle(ctx, token, ev) }
	for {
		if wait > 0 && !b.sleep(ctx, wait) {
			return
		}
		if ctx.Err() != nil {
			return
		}
		err := b.tr.listen(ctx, token, on)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			wait = nextBackoff(wait, err) // before scrub, which keeps only the words
			err = scrub(err, token)
			b.setErr(err.Error())
			log.Printf("%v; again in %s", err, wait)
			continue
		}
		wait = 0
		b.setErr("")
	}
}

// nextBackoff is the wait after a failure: what the service asks for, else
// twice the last one, between botBackoffMin and botBackoffMax.
func nextBackoff(prev time.Duration, err error) time.Duration {
	var ra retryAfter
	if errors.As(err, &ra) && ra.retryAfter() > 0 {
		return min(ra.retryAfter(), botBackoffMax)
	}
	if prev < botBackoffMin {
		return botBackoffMin
	}
	return min(prev*2, botBackoffMax)
}

func (b *botChannel) handle(ctx context.Context, token string, ev botEvent) {
	if ev.Button {
		b.onButton(ctx, token, ev)
		return
	}
	b.onMessage(ctx, token, ev)
}

// onMessage: the linked account's text joins the conversation; a code may
// link an account; anything else is ignored, and counted.
func (b *botChannel) onMessage(ctx context.Context, token string, ev botEvent) {
	if ev.User == "" || ev.FromBot || !ev.Private {
		b.ignore()
		return
	}
	if !b.linked() {
		b.tryLink(ctx, token, ev)
		return
	}
	if !b.isUser(ev.User) {
		b.ignore()
		return
	}
	lang := b.lang()
	text := strings.TrimSpace(ev.Text)
	switch {
	case ev.Start:
		b.say(ctx, token, ev.Chat, tr(lang, "bot.already"))
	case text == "": // a photo, a voice note, a file, a sticker
		b.say(ctx, token, ev.Chat, tr(lang, "bot.textonly"))
	default:
		seq, err := b.c.store.addMessageVia(roleUser, text, b.spec.Label, false)
		if err != nil {
			log.Printf("%s: %v", b.spec.ID, err)
			return
		}
		b.c.resolveByText(ctx, int(seq)) // the sender is checked: a plain yes or no may answer a card
		b.c.ping()
	}
}

// tryLink links the account that sends the pending code.
func (b *botChannel) tryLink(ctx context.Context, token string, ev botEvent) {
	code, tries := b.pendingCode()
	if code == "" {
		b.ignore()
		return
	}
	got := strings.ToUpper(strings.TrimSpace(ev.Text))
	if subtle.ConstantTimeCompare([]byte(got), []byte(code)) != 1 {
		b.ignore()
		parts := strings.Split(b.get("code"), "|")
		b.set("code", fmt.Sprintf("%s|%s|%d", parts[0], parts[1], tries+1))
		return
	}
	st := b.c.store
	b.set("user", ev.User)
	b.set("chat", ev.Chat)
	b.set("name", truncate(ev.Name, 64))
	b.set("code", "")
	st.set(channelRouteKey(b.spec.ID), "")
	st.set(channelSeenKey(b.spec.ID), strconv.Itoa(st.lastMessageSeq()))
	log.Printf("%s: account linked", b.spec.ID)
	b.say(ctx, token, ev.Chat, tr(b.lang(), "bot.linked"))
}

// Out.

// say sends a text to a chat, now; a failure is logged.
func (b *botChannel) say(ctx context.Context, token, chat, text string) {
	if err := b.send(ctx, token, chat, botMessage{Text: text}); err != nil {
		log.Printf("%v", err)
	}
}

func (b *botChannel) send(ctx context.Context, token, chat string, m botMessage) error {
	m.Text = truncate(m.Text, b.spec.TextMax)
	return scrub(b.tr.send(ctx, token, chat, m), token)
}

// botOut is a message waiting to be sent.
type botOut struct {
	token, chat string
	m           botMessage
}

// post sends to the linked chat in the background, in order: one sender
// at a time drains the queue.
func (b *botChannel) post(m botMessage) {
	token, chat := b.token(), b.get("chat")
	if token == "" || !b.linked() {
		return
	}
	b.pending.Add(1)
	b.qmu.Lock()
	b.queue = append(b.queue, botOut{token, chat, m})
	if !b.sending {
		b.sending = true
		go b.drain()
	}
	b.qmu.Unlock()
}

func (b *botChannel) drain() {
	for {
		b.qmu.Lock()
		if len(b.queue) == 0 {
			b.sending = false
			b.qmu.Unlock()
			return
		}
		m := b.queue[0]
		b.queue = b.queue[1:]
		b.qmu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		if err := b.send(ctx, m.token, m.chat, m.m); err != nil {
			log.Printf("%v", err)
		}
		cancel()
		b.pending.Done()
	}
}

// deliver sends what tell sends: the words and the buttons, or a nudge.
func (b *botChannel) deliver(n pushNote) {
	if n.Waiting && b.routing() {
		return // the user writes from there: the brain's reply goes there already
	}
	b.c.channelNoteSent(b.spec.ID, n.Notif) // its message's cards follow it there (channel_relay.go)
	b.post(b.compose(n))
}

func (b *botChannel) compose(n pushNote) botMessage {
	lang := b.lang()
	link := b.link(n.URL)
	if b.detail() == detailAlert {
		return botMessage{Text: joinLines(tr(lang, "bot.alert"), link)}
	}
	body := n.Body[lang]
	if body == "" {
		body = n.Body["fr"]
	}
	if n.Why != "" {
		body += "\n" + tr(lang, "notif.why") + " " + n.Why
	}
	m := botMessage{Text: joinLines(truncate(body, b.spec.TextMax-300), link)}
	if n.Perm != nil && len(permData(*n.Perm, true)) <= b.spec.DataMax {
		m.Buttons = [][]botButton{{
			{Text: "✓ " + tr(lang, "perm.allow"), Data: permData(*n.Perm, true)},
			{Text: "✕ " + tr(lang, "perm.deny"), Data: permData(*n.Perm, false)},
		}}
		return m
	}
	if n.Notif <= 0 {
		return m
	}
	var row []botButton
	for _, fb := range feedbacks {
		row = append(row, botButton{Text: tr(lang, "notif.fb."+fb), Data: feedbackData(n.Notif, fb)})
	}
	m.Buttons = [][]botButton{row}
	return m
}

// link is a workspace page's address, when the workspace's is known.
func (b *botChannel) link(path string) string {
	base := strings.TrimRight(b.base(), "/")
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
func (b *botChannel) test(ctx context.Context) error {
	token := b.token()
	if token == "" || !b.linked() {
		return errors.New("not linked")
	}
	return b.send(ctx, token, b.get("chat"), botMessage{Text: joinLines(tr(b.lang(), "bot.test"), b.link("/"))})
}

// reply sends the brain's message that answers what the user wrote from
// the channel (channel_relay.go).
func (b *botChannel) reply(m Message) {
	if b.detail() == detailAlert {
		b.post(botMessage{Text: joinLines(tr(b.lang(), "bot.replied"), b.link(fmt.Sprintf("/#m%d", m.Seq)))})
		return
	}
	b.post(botMessage{Text: m.Text})
}
