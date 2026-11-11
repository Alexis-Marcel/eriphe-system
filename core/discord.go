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
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// Discord, a bot channel's transport (bot_channel.go). The user makes an
// application with a bot in Discord's Developer Portal, pastes the bot's
// token, and adds the bot to one of their servers: Discord lets a bot and
// a user write to each other in direct messages only when they share one.
// eriphe-core opens no port: it keeps a Gateway connection (a WebSocket it
// dials) for what the user writes and the buttons they tap, and uses the
// REST API to send. Direct messages with a bot are not end-to-end
// encrypted: Settings says so.
//
//   - listen is one Gateway connection, until it drops: Hello, a heartbeat
//     at the interval Discord asks, Identify with the DIRECT_MESSAGES intent
//     (a DM's content comes without the privileged message content intent),
//     or Resume when a session is still open. Op 7 (reconnect) ends the round
//     well, to resume at once; op 9 and the close codes say whether the
//     session can be resumed; 4004 (the token) and 4014 (the intents) end
//     it with an error.
//   - MESSAGE_CREATE in a DM is the user writing; INTERACTION_CREATE (a
//     button: Discord sends them over the Gateway while the application has
//     no interactions endpoint URL) is a tap. The bot's own messages are not
//     events.
//   - send posts to the DM channel, with buttons as components, and no
//     mention ever pings anyone.
//   - answer answers a tap within Discord's 3 seconds: the message updated
//     without its buttons once decided, else an ephemeral note, else a
//     plain acknowledgement.

const (
	dcChannel  = "discord"
	viaDiscord = "Discord"

	dcIntents = 1 << 12 // DIRECT_MESSAGES
)

var (
	discordAPI     = "https://discord.com/api/v10"
	discordGateway = "wss://gateway.discord.gg"
	dcHelloWait    = 30 * time.Second // a connection that does not say Hello is dropped
)

func init() {
	registerBot(botSpec{ID: dcChannel, Label: viaDiscord, Icon: "chat",
		TextMax: 2000, // Discord takes 2000 characters
		DataMax: 100,  // and a 100-character custom_id
		Token:   regexp.MustCompile(`^[A-Za-z0-9_-]{18,}\.[A-Za-z0-9_-]{4,}\.[A-Za-z0-9_-]{20,}$`), Placeholder: "MTIz….Gx….abc…",
		Invite: func(id string) string {
			return "https://discord.com/oauth2/authorize?client_id=" + url.QueryEscape(id) + "&scope=bot&permissions=0"
		},
		New: func(b *botChannel) botTransport {
			return &discord{api: discordAPI, gateway: discordGateway, client: &http.Client{}}
		}})
}

type discord struct {
	api, gateway string
	client       *http.Client

	mu        sync.Mutex
	self      string // the bot's own user id, from READY
	session   string // a session that can be resumed
	resumeURL string
	seq       int64 // the last event's sequence number
}

// The REST API, as much of it as is used.

type dcUser struct {
	ID         string `json:"id"`
	Username   string `json:"username"`
	GlobalName string `json:"global_name"`
	Bot        bool   `json:"bot"`
}

type dcMessage struct {
	ID        string `json:"id"`
	ChannelID string `json:"channel_id"`
	GuildID   string `json:"guild_id"`
	Author    dcUser `json:"author"`
	Content   string `json:"content"`
}

type dcInteraction struct {
	ID        string  `json:"id"`
	Token     string  `json:"token"`
	Type      int     `json:"type"` // 3: a message component
	ChannelID string  `json:"channel_id"`
	GuildID   string  `json:"guild_id"`
	User      *dcUser `json:"user"` // in a DM
	Member    *struct {
		User dcUser `json:"user"`
	} `json:"member"` // in a server
	Data struct {
		CustomID string `json:"custom_id"`
	} `json:"data"`
	Message *dcMessage `json:"message"` // the message its button is on
}

type dcError struct {
	Status     int
	Message    string
	RetryAfter float64
}

func (e *dcError) Error() string { return fmt.Sprintf("discord: %d %s", e.Status, e.Message) }

func (e *dcError) retryAfter() time.Duration {
	return time.Duration(e.RetryAfter * float64(time.Second))
}

// call calls the REST API. The token goes in a header, never in the URL;
// an interaction's callback needs none.
func (d *discord) call(ctx context.Context, token, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		raw, _ := json.Marshal(in)
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, d.api+path, body)
	if err != nil {
		return errors.New("discord: bad request")
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bot "+token)
	}
	req.Header.Set("User-Agent", "DiscordBot (https://github.com/Alexis-Marcel/eriphe-system, 1)")
	resp, err := d.client.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return errors.New("discord: " + err.Error())
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode >= 300 {
		var e struct {
			Message    string  `json:"message"`
			RetryAfter float64 `json:"retry_after"`
		}
		json.Unmarshal(raw, &e)
		if e.Message == "" {
			e.Message = http.StatusText(resp.StatusCode)
		}
		return &dcError{Status: resp.StatusCode, Message: truncate(e.Message, 160), RetryAfter: e.RetryAfter}
	}
	if out != nil && len(raw) > 0 {
		return json.Unmarshal(raw, out)
	}
	return nil
}

func (d *discord) check(ctx context.Context, token string) (string, string, error) {
	var me dcUser
	if err := d.call(ctx, token, http.MethodGet, "/users/@me", nil, &me); err != nil {
		return "", "", err
	}
	if !me.Bot {
		return "", "", errors.New("not a bot token")
	}
	d.mu.Lock()
	d.self, d.session, d.resumeURL, d.seq = me.ID, "", "", 0 // a new bot, a new session
	d.mu.Unlock()
	return me.Username, me.ID, nil
}

// The Gateway.

type dcPayload struct {
	Op int             `json:"op"`
	D  json.RawMessage `json:"d"`
	S  *int64          `json:"s,omitempty"`
	T  string          `json:"t,omitempty"`
}

// errResume ends a round that may resume at once.
var errResume = errors.New("discord: reconnect")

func (d *discord) listen(ctx context.Context, token string, on func(botEvent)) error {
	d.mu.Lock()
	session, resumeURL, seq := d.session, d.resumeURL, d.seq
	d.mu.Unlock()
	base := d.gateway
	if session != "" && resumeURL != "" {
		base = resumeURL
	}
	conn, _, err := websocket.Dial(ctx, strings.TrimRight(base, "/")+"/?v=10&encoding=json", &websocket.DialOptions{HTTPClient: d.client})
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return errors.New("discord: gateway: " + err.Error())
	}
	defer conn.CloseNow()
	conn.SetReadLimit(8 << 20)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Hello: the heartbeat's interval.
	helloCtx, helloDone := context.WithTimeout(ctx, dcHelloWait)
	hello, err := d.read(helloCtx, conn)
	helloDone()
	if err != nil {
		return err
	}
	var h struct {
		Interval float64 `json:"heartbeat_interval"`
	}
	if hello.Op != 10 || json.Unmarshal(hello.D, &h) != nil || h.Interval <= 0 {
		return errors.New("discord: gateway: no hello")
	}
	interval := time.Duration(h.Interval * float64(time.Millisecond))

	if session != "" {
		err = d.write(ctx, conn, 6, map[string]any{"token": token, "session_id": session, "seq": seq})
	} else {
		err = d.write(ctx, conn, 2, map[string]any{"token": token, "intents": dcIntents,
			"properties": map[string]string{"os": "linux", "browser": "eriphe", "device": "eriphe"}})
	}
	if err != nil {
		return err
	}

	acked := make(chan struct{}, 1)
	beat := make(chan struct{}, 1) // Discord asks for one now (op 1)
	go d.heartbeat(ctx, conn, interval, acked, beat)

	for {
		p, err := d.read(ctx, conn)
		if err != nil {
			return err
		}
		if p.S != nil {
			d.mu.Lock()
			d.seq = *p.S
			d.mu.Unlock()
		}
		switch p.Op {
		case 0:
			d.dispatch(p, on)
		case 1:
			select {
			case beat <- struct{}{}:
			default:
			}
		case 7: // reconnect, and resume
			conn.Close(4000, "reconnect")
			return nil
		case 9: // invalid session
			var resumable bool
			json.Unmarshal(p.D, &resumable)
			if !resumable {
				d.forgetSession()
			}
			conn.Close(4000, "invalid session")
			return errResume // wait a little, as Discord asks, then identify
		case 11:
			select {
			case acked <- struct{}{}:
			default:
			}
		}
	}
}

// heartbeat sends one every interval, the first after a part of it (as
// Discord asks), and drops the connection when one is not acknowledged.
func (d *discord) heartbeat(ctx context.Context, conn *websocket.Conn, interval time.Duration, acked, beat chan struct{}) {
	wait := interval / 4
	waiting := false
	for {
		select {
		case <-ctx.Done():
			return
		case <-beat:
		case <-time.After(wait):
			if waiting {
				select {
				case <-acked:
				default:
					conn.Close(4000, "no heartbeat ack")
					return
				}
			}
		}
		wait = interval
		d.mu.Lock()
		var s any
		if d.seq > 0 {
			s = d.seq
		}
		d.mu.Unlock()
		if d.write(ctx, conn, 1, s) != nil {
			return
		}
		waiting = true
	}
}

func (d *discord) read(ctx context.Context, conn *websocket.Conn) (dcPayload, error) {
	_, raw, err := conn.Read(ctx)
	if err != nil {
		return dcPayload{}, d.closed(err)
	}
	var p dcPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return dcPayload{}, errors.New("discord: gateway: bad payload")
	}
	return p, nil
}

func (d *discord) write(ctx context.Context, conn *websocket.Conn, op int, data any) error {
	raw, _ := json.Marshal(map[string]any{"op": op, "d": data})
	if err := conn.Write(ctx, websocket.MessageText, raw); err != nil {
		return d.closed(err)
	}
	return nil
}

// closed is a dropped connection as an error, the session forgotten when
// Discord says it cannot be resumed.
func (d *discord) closed(err error) error {
	code := websocket.CloseStatus(err)
	switch code {
	case -1:
		return errors.New("discord: gateway: connection lost")
	case 4004:
		d.forgetSession()
		return errors.New("discord: gateway: the token was refused")
	case 4014:
		d.forgetSession()
		return errors.New("discord: gateway: the intents were refused")
	case 4007, 4009, 4010, 4011, 4012, 4013:
		d.forgetSession()
	}
	return fmt.Errorf("discord: gateway: closed (%d)", code)
}

func (d *discord) forgetSession() {
	d.mu.Lock()
	d.session, d.resumeURL, d.seq = "", "", 0
	d.mu.Unlock()
}

// dispatch reads an event: READY keeps the session, a DM and a button are
// the bot channel's.
func (d *discord) dispatch(p dcPayload, on func(botEvent)) {
	switch p.T {
	case "READY":
		var r struct {
			SessionID string `json:"session_id"`
			ResumeURL string `json:"resume_gateway_url"`
			User      dcUser `json:"user"`
		}
		if json.Unmarshal(p.D, &r) == nil {
			d.mu.Lock()
			d.session, d.resumeURL, d.self = r.SessionID, r.ResumeURL, r.User.ID
			d.mu.Unlock()
		}
	case "MESSAGE_CREATE":
		var m dcMessage
		if json.Unmarshal(p.D, &m) != nil {
			return
		}
		d.mu.Lock()
		self := d.self
		d.mu.Unlock()
		if m.Author.ID == self {
			return // its own
		}
		name := m.Author.GlobalName
		if m.Author.Username != "" {
			name = "@" + m.Author.Username
		}
		on(botEvent{User: m.Author.ID, Chat: m.ChannelID, Name: name, Private: m.GuildID == "",
			FromBot: m.Author.Bot, Text: m.Content})
	case "INTERACTION_CREATE":
		var x dcInteraction
		if json.Unmarshal(p.D, &x) != nil || x.Type != 3 {
			return
		}
		user := ""
		switch {
		case x.User != nil:
			user = x.User.ID
		case x.Member != nil:
			user = x.Member.User.ID
		}
		on(botEvent{Button: true, User: user, Chat: x.ChannelID, Private: x.GuildID == "", Data: x.Data.CustomID, Ref: &x})
	}
}

// Out.

func (d *discord) send(ctx context.Context, token, chat string, m botMessage) error {
	in := map[string]any{"content": m.Text, "allowed_mentions": map[string]any{"parse": []string{}},
		"flags": 1 << 2} // SUPPRESS_EMBEDS: no link preview
	if len(m.Buttons) > 0 {
		in["components"] = dcComponents(m.Buttons)
	}
	return d.call(ctx, token, http.MethodPost, "/channels/"+url.PathEscape(chat)+"/messages", in, nil)
}

// dcComponents are rows of buttons: an action row each, five at most.
func dcComponents(rows [][]botButton) []any {
	var out []any
	for _, row := range rows {
		var buttons []any
		for _, b := range row[:min(len(row), 5)] {
			buttons = append(buttons, map[string]any{"type": 2, "style": 2, "label": truncate(b.Text, 80), "custom_id": b.Data})
		}
		out = append(out, map[string]any{"type": 1, "components": buttons})
	}
	return out
}

// answer answers a tap: the message, without its buttons, says what was
// done; else a note only the user sees; else just that it was received.
func (d *discord) answer(ctx context.Context, _ string, ev botEvent, toast, done string) error {
	x, _ := ev.Ref.(*dcInteraction)
	if x == nil {
		return errors.New("discord: not a button")
	}
	var in map[string]any
	switch {
	case done != "":
		text := "→ " + done
		if x.Message != nil {
			text = strings.TrimSpace(x.Message.Content) + "\n\n" + text
		}
		in = map[string]any{"type": 7, "data": map[string]any{"content": truncate(text, 2000), "components": []any{},
			"allowed_mentions": map[string]any{"parse": []string{}}}}
	case toast != "":
		in = map[string]any{"type": 4, "data": map[string]any{"content": toast, "flags": 1 << 6}} // EPHEMERAL
	default:
		in = map[string]any{"type": 6} // DEFERRED_UPDATE_MESSAGE
	}
	// The interaction's token is in the path: it lives 15 minutes and is
	// not the bot's.
	return d.call(ctx, "", http.MethodPost, "/interactions/"+url.PathEscape(x.ID)+"/"+url.PathEscape(x.Token)+"/callback", in, nil)
}
