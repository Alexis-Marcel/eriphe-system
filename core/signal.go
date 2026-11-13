package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"html/template"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
)

// Signal, a bot channel's transport (bot_channel.go). Signal has no bots:
// Eriphe has an account of its own, with a number of its own (a landline
// works: Signal verifies it by a voice call), held by eriphe-signal
// (servers/signal): the channel's own service (botSpec.Service,
// channel-signal), run while Signal is on, outside the catalog: a node
// runs it, on amd64 (its signal-cli is x86_64 only), with a volume for the
// account's keys; the channel alone calls it (b.svc). There is no token:
// the number registered stands for it.
//
//   - Setup (Settings → Access → Signal, sgSetup): turn Signal on (the
//     service is added), the number and SMS or call, Signal's captcha when
//     it asks for one, the code and the number's PIN, and a PIN to lock it.
//     Then the bot channel's linking, as for the others.
//   - listen is one receive: a long poll of up to sgPollWait, from the last
//     seq seen. A message in a group is not the user's chat.
//   - Signal has no buttons. A message with buttons ends with a legend that
//     gives each an emoji (sgReactions, by position: 👍 Oui · 👎 Non); a
//     reaction with it to that message is the tap. What each sent
//     message's emojis stand for is kept (sgKeep of them, in the channel's
//     state, by the message's timestamp), and dropped once decided.
//   - answer sends the note as a message: a sent message cannot lose its
//     legend.
//   - Removing the channel unregisters the number and deletes its data in
//     the service, then the service.
//
// Messages are end-to-end encrypted between the user's phone and
// signal-cli, which keeps them, and the account's keys, on the node:
// Settings says so.

const (
	sgChannel = "signal"
	viaSignal = "Signal"
	sgKeep    = 50 // sent messages whose reactions are still taps
	sgImage   = "ghcr.io/alexis-marcel/eriphe-signal"
)

var (
	sgPollWait  = 50 // seconds a receive waits for something
	sgReactions = []string{"👍", "👎", "⏳"}
)

func init() {
	registerBot(botSpec{ID: sgChannel, Label: viaSignal, Icon: "chat",
		TextMax: 2000, // longer is sent as a file by Signal
		DataMax: 100,  // the data stays here: any length would do
		// One of Eriphe's own images; signal-cli's native build is x86_64.
		Service:  &RunSpec{Image: sgImage, Transport: transportStdio, Volume: true, Arch: "amd64", Own: true},
		Handle:   func(number string) string { return number },
		OpenLink: func(number, _ string) string { return "https://signal.me/#p/" + number },
		Account:  func(b *botChannel) string { return b.get("bot") },
		Setup:    sgSetup,
		Routes:   sgRoutes,
		Removed:  sgRemoved,
		New: func(b *botChannel) botTransport {
			return &signal{b: b}
		}})
}

type signal struct {
	b  *botChannel // the number, the offset and the buttons, in the channel's state
	mu sync.Mutex  // the buttons
}

// The service.

// tool calls one of the service's tools, and decodes its answer.
func (s *signal) tool(ctx context.Context, name string, in, out any) error {
	if err := s.b.svc.call(ctx, name, in, out); err != nil {
		return errors.New("signal: " + err.Error())
	}
	return nil
}

// sgDone is a setup tool's answer: whether it worked, why not, and
// whether Signal asks for a captcha first.
type sgDone struct {
	OK      bool   `json:"ok"`
	Error   string `json:"error"`
	Captcha bool   `json:"captcha"`
}

// status is the number registered in the service, "" before.
func (s *signal) status(ctx context.Context) (string, error) {
	var st struct {
		Number string `json:"number"`
	}
	err := s.tool(ctx, "status", struct{}{}, &st)
	return st.Number, err
}

func (s *signal) check(context.Context, string) (string, string, error) {
	return "", "", errors.New("signal: no token, a number registered")
}

// receiveOut is what receive gives.
type sgMessage struct {
	Seq        int64  `json:"seq"`
	From       string `json:"from"`
	Number     string `json:"number"`
	Name       string `json:"name"`
	Group      bool   `json:"group"`
	Text       string `json:"text"`
	Attachment bool   `json:"attachment"`
	Reaction   *struct {
		Emoji  string `json:"emoji"`
		Target int64  `json:"target"`
		Remove bool   `json:"remove"`
	} `json:"reaction"`
}

// listen is one receive, from the last seq seen.
func (s *signal) listen(ctx context.Context, _ string, on func(botEvent)) error {
	ctx, cancel := context.WithTimeout(ctx, time.Duration(sgPollWait+30)*time.Second)
	defer cancel()
	var r struct {
		Messages []sgMessage `json:"messages"`
		Last     int64       `json:"last"`
	}
	if err := s.tool(ctx, "receive", map[string]any{"after": s.b.getInt("offset"), "wait": sgPollWait}, &r); err != nil {
		return err
	}
	for _, m := range r.Messages {
		if ev, ok := s.event(m); ok {
			on(ev)
		}
	}
	s.b.set("offset", strconv.FormatInt(r.Last, 10))
	return nil
}

// event is a message as a bot channel's event: a reaction with one of a
// message's emojis is its button.
func (s *signal) event(m sgMessage) (botEvent, bool) {
	chat := m.Number
	if chat == "" {
		chat = m.From
	}
	if x := m.Reaction; x != nil {
		if x.Remove || m.Group {
			return botEvent{}, false
		}
		data := s.tapped(x.Target, x.Emoji)
		return botEvent{Button: true, User: m.From, Chat: chat, Data: data, Ref: x.Target}, data != ""
	}
	name := m.Name
	if name == "" {
		name = m.Number
	}
	return botEvent{User: m.From, Chat: chat, Name: name, Private: !m.Group, Text: m.Text}, true
}

func (s *signal) send(ctx context.Context, _ string, chat string, m botMessage) error {
	text, taps := sgLegend(m)
	var out struct {
		Timestamp int64 `json:"timestamp"`
	}
	if err := s.tool(ctx, "send", map[string]any{"to": chat, "text": text}, &out); err != nil {
		return err
	}
	if len(taps) > 0 && out.Timestamp > 0 {
		s.keep(out.Timestamp, taps)
	}
	return nil
}

// sgLegend is a message's text with its buttons' legend, and what each
// emoji stands for.
func sgLegend(m botMessage) (string, map[string]string) {
	var parts []string
	taps := map[string]string{}
	i := 0
	for _, row := range m.Buttons {
		for _, b := range row {
			if i == len(sgReactions) {
				break
			}
			e := sgReactions[i]
			taps[e] = b.Data
			label := strings.TrimLeftFunc(b.Text, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
			parts = append(parts, e+" "+label)
			i++
		}
	}
	if len(parts) == 0 {
		return m.Text, nil
	}
	return m.Text + "\n\n" + strings.Join(parts, " · "), taps
}

// sgSent is a message's emojis and what they stand for.
type sgSent struct {
	TS   int64             `json:"ts"`
	Taps map[string]string `json:"taps"`
}

func (s *signal) kept() []sgSent {
	var all []sgSent
	json.Unmarshal([]byte(s.b.get("buttons")), &all)
	return all
}

func (s *signal) setKept(all []sgSent) {
	if len(all) > sgKeep {
		all = all[len(all)-sgKeep:]
	}
	raw, _ := json.Marshal(all)
	s.b.set("buttons", string(raw))
}

func (s *signal) keep(ts int64, taps map[string]string) {
	s.mu.Lock() // sends are one at a time, but a tap may drop one meanwhile
	defer s.mu.Unlock()
	s.setKept(append(s.kept(), sgSent{TS: ts, Taps: taps}))
}

// tapped is what that emoji stands for on that message, "" if nothing.
func (s *signal) tapped(ts int64, emoji string) string {
	emoji = strings.Map(func(r rune) rune { // 👍🏽 is 👍
		if r == 0xfe0f || (r >= 0x1f3fb && r <= 0x1f3ff) {
			return -1
		}
		return r
	}, emoji)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, k := range s.kept() {
		if k.TS == ts {
			return k.Taps[emoji]
		}
	}
	return ""
}

func (s *signal) drop(ts int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	all := s.kept()
	for i, k := range all {
		if k.TS == ts {
			s.setKept(append(all[:i], all[i+1:]...))
			return
		}
	}
}

// answer sends the note; once decided, the message's reactions are no
// longer taps.
func (s *signal) answer(ctx context.Context, token string, ev botEvent, toast, done string) error {
	if ts, _ := ev.Ref.(int64); done != "" {
		s.drop(ts)
	}
	note := toast
	if note == "" {
		note = done
	}
	if note == "" {
		return nil
	}
	return s.send(ctx, token, ev.Chat, botMessage{Text: "→ " + note})
}

// Setup: the page's part while no number is registered, and its routes.

func sgOf(b *botChannel) *signal { return b.tr.(*signal) }

// sgView is what the setup shows.
type sgView struct {
	Added   bool   // the service is on
	Up      bool   // it answers
	Error   string // why it does not, or what Signal said last
	Pending string // the number waiting for its code
	Captcha bool   // Signal asked for a captcha
}

func sgSetup(b *botChannel, lang string) template.HTML {
	s := sgOf(b)
	v := sgView{Added: b.serviceOn(), Pending: b.get("pending"), Error: b.get("setup_error"), Captcha: b.get("captcha") != ""}
	if v.Added {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		number, err := s.status(ctx)
		cancel()
		switch {
		case err != nil:
			v.Error = err.Error()
		case number != "":
			s.registered(number) // registered elsewhere: the channel takes it
		default:
			v.Up = true
		}
	}
	var out bytes.Buffer
	t, _ := sgTemplate.Clone()
	if err := t.Funcs(templateFuncs(lang)).Execute(&out, v); err != nil {
		log.Printf("signal: %v", err)
	}
	return template.HTML(out.String())
}

// registered takes the number registered: the channel listens with it.
func (s *signal) registered(number string) {
	b := s.b
	b.forget("pending", "captcha", "setup_error", "offset", "buttons")
	b.set("bot", number)
	b.set("bot_id", number)
	log.Printf("signal: number registered")
	b.restart()
}

var sgTemplate = template.Must(template.New("signal-setup").Funcs(templateFuncs("fr")).Parse(`
{{if not .Added}}
<p class="small">{{t "signal.how"}}</p>
<form hx-post="/channels/signal/add" hx-target="#signal-panel"><button class="btn btn-primary sm" type="submit">{{t "signal.add"}}</button></form>
{{else if not .Up}}
<div hx-get="/channels/signal" hx-trigger="every 5s" hx-target="#signal-panel">
<p class="small">{{t "signal.starting"}}</p>
{{if .Error}}<p class="small muted">{{.Error}}</p>{{end}}
</div>
<button class="btn btn-danger-ghost sm" type="button" hx-post="/channels/signal/remove" hx-target="#signal-panel" hx-confirm="{{t "signal.removeconfirm"}}">{{t "signal.remove"}}</button>
{{else if .Pending}}
<p class="small">{{tf "signal.code.lead" .Pending}}</p>
{{if .Error}}<p class="notice">{{.Error}}</p>{{end}}
<form class="stack-form" hx-post="/channels/signal/verify" hx-target="#signal-panel" hx-disabled-elt="find button">
  <label class="field"><span>{{t "signal.code"}}</span><input name="code" inputmode="numeric" autocomplete="one-time-code" required maxlength="12"></label>
  <label class="field"><span>{{t "signal.pin.old"}}</span><input type="password" name="pin" autocomplete="off" maxlength="64"></label>
  <label class="field"><span>{{t "signal.pin.new"}}</span><input type="password" name="lock" autocomplete="new-password" maxlength="64"></label>
  <p class="small muted">{{t "signal.pin.hint"}}</p>
  <div class="actions"><button class="btn btn-primary sm" type="submit">{{t "signal.verify"}}</button>
  <button class="btn btn-ghost sm" type="button" hx-post="/channels/signal/restart" hx-target="#signal-panel">{{t "signal.again"}}</button></div>
</form>
{{else}}
<p class="small">{{t "signal.number.lead"}}</p>
{{if .Error}}<p class="notice">{{.Error}}</p>{{end}}
<form class="stack-form" hx-post="/channels/signal/register" hx-target="#signal-panel" hx-disabled-elt="find button">
  <label class="field"><span>{{t "signal.number"}}</span><input name="number" type="tel" required maxlength="20" placeholder="+33123456789"></label>
  <label class="check"><input type="checkbox" name="voice" value="1"> {{t "signal.voice"}}</label>
  {{if .Captcha}}
  <p class="small">{{t "signal.captcha.lead"}} <a href="https://signalcaptchas.org/registration/generate.html" target="_blank" rel="noopener">signalcaptchas.org</a></p>
  <label class="field"><span>{{t "signal.captcha"}}</span><input name="captcha" required autocomplete="off" placeholder="signalcaptcha://…"></label>
  {{end}}
  <div class="actions"><button class="btn btn-primary sm" type="submit">{{t "signal.register"}}</button>
  <button class="btn btn-danger-ghost sm" type="button" hx-post="/channels/signal/remove" hx-target="#signal-panel" hx-confirm="{{t "signal.removeconfirm"}}">{{t "signal.remove"}}</button></div>
</form>
{{end}}`))

func sgRoutes(w *web, mux *webMux, h func(botHandler) http.Handler) {
	base := "POST /channels/" + sgChannel + "/"
	mux.Handle(base+"add", h(w.handleSignalAdd))
	mux.Handle(base+"register", http.MaxBytesHandler(h(w.handleSignalRegister), 8<<10))
	mux.Handle(base+"verify", http.MaxBytesHandler(h(w.handleSignalVerify), 4<<10))
	mux.Handle(base+"restart", h(w.handleSignalRestart))
}

func (w *web) handleSignalAdd(b *botChannel, rw http.ResponseWriter, r *http.Request, s *session) {
	if err := b.setService(true); err != nil {
		log.Printf("signal: %v", err)
		w.toast(rw, tr(w.lang(r), "signal.err.add"), false)
		return
	}
	w.renderBot(b, rw, r, s, "", true)
}

// setupErr keeps what went wrong for the setup to show, "" when nothing.
func (b *botChannel) setupErr(d sgDone, err error) {
	switch {
	case err != nil:
		b.set("setup_error", err.Error())
	case !d.OK:
		b.set("setup_error", truncate(d.Error, 300))
	default:
		b.set("setup_error", "")
	}
}

func (w *web) handleSignalRegister(b *botChannel, rw http.ResponseWriter, r *http.Request, s *session) {
	number := strings.ReplaceAll(strings.TrimSpace(r.PostFormValue("number")), " ", "")
	in := map[string]any{"number": number, "voice": r.PostFormValue("voice") == "1"}
	if c := strings.TrimSpace(r.PostFormValue("captcha")); c != "" {
		in["captcha"] = c
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
	defer cancel()
	var d sgDone
	err := sgOf(b).tool(ctx, "register", in, &d)
	b.setupErr(d, err)
	switch {
	case err == nil && d.OK:
		b.set("pending", number)
		b.set("captcha", "")
	case d.Captcha:
		b.set("captcha", "1")
		if in["captcha"] == nil {
			b.set("setup_error", "") // asked for, not failed
		}
	}
	w.renderBot(b, rw, r, s, "", true)
}

func (w *web) handleSignalVerify(b *botChannel, rw http.ResponseWriter, r *http.Request, s *session) {
	sg := sgOf(b)
	number := b.get("pending")
	if number == "" {
		w.renderBot(b, rw, r, s, "", true)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
	defer cancel()
	in := map[string]any{"number": number, "code": strings.TrimSpace(r.PostFormValue("code"))}
	if pin := r.PostFormValue("pin"); pin != "" {
		in["pin"] = pin
	}
	var d sgDone
	err := sg.tool(ctx, "verify", in, &d)
	b.setupErr(d, err)
	if err != nil || !d.OK {
		w.renderBot(b, rw, r, s, "", true)
		return
	}
	msg, ok := tr(w.lang(r), "signal.registered"), true
	if lock := r.PostFormValue("lock"); lock != "" {
		var p sgDone
		if err := sg.tool(ctx, "set_pin", map[string]any{"pin": lock}, &p); err != nil || !p.OK {
			msg, ok = tr(w.lang(r), "signal.err.pin"), false
		}
	}
	sg.registered(number)
	w.renderBot(b, rw, r, s, msg, ok)
}

func (w *web) handleSignalRestart(b *botChannel, rw http.ResponseWriter, r *http.Request, s *session) {
	b.forget("pending", "captcha", "setup_error")
	w.renderBot(b, rw, r, s, "", true)
}

// sgRemoved unregisters the number and deletes its data in the service,
// then turns the service off.
func sgRemoved(b *botChannel) {
	if !b.serviceOn() {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if b.get("bot") != "" {
		var d sgDone
		if err := sgOf(b).tool(ctx, "remove", struct{}{}, &d); err != nil || !d.OK {
			log.Printf("signal: the number was not removed: %v %s", err, d.Error)
		}
	}
	if err := b.setService(false); err != nil {
		log.Printf("signal: %v", err)
	}
	b.forget("pending", "captcha", "setup_error")
}
