package main

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

// A bot channel's part of the workspace (bot_channel.go): its page in
// Settings → Access (/settings/access/<id>, templates/bot.html), the panel
// it polls while a code waits (/channels/<id>), its token, code, unlinking,
// test and removal, and its row in the list once a bot is set.

func botRoutes(w *web, mux *http.ServeMux, id string) {
	h := func(f func(*botChannel, http.ResponseWriter, *http.Request, *session)) http.Handler {
		return w.auth(func(rw http.ResponseWriter, r *http.Request, s *session) {
			b := w.c.bot(id)
			if b == nil {
				w.toast(rw, tr(w.lang(r), "bot.err.off"), false)
				return
			}
			f(b, rw, r, s)
		})
	}
	mux.Handle("GET /settings/access/"+id, h(w.handleAccessBot))
	mux.Handle("GET /channels/"+id, h(w.handleBotState))
	mux.Handle("POST /channels/"+id+"/token", http.MaxBytesHandler(h(w.handleBotToken), 4<<10))
	mux.Handle("POST /channels/"+id+"/remove", h(w.handleBotRemove))
	mux.Handle("POST /channels/"+id+"/code", h(w.handleBotCode))
	mux.Handle("POST /channels/"+id+"/unlink", h(w.handleBotUnlink))
	mux.Handle("POST /channels/"+id+"/test", h(w.handleBotTest))
}

// startBot opens a bot channel, listening while a bot is set.
func startBot(w *web, id string) {
	b := newBotChannel(w.c, id)
	b.base = func() string {
		if a := w.address(); a != "" {
			return a
		}
		if w.c.remote != nil {
			if v, ok := w.c.remote.ready(); ok {
				return v.URL()
			}
		}
		return ""
	}
	w.c.setBot(b)
	b.restart()
}

// botEntry is its row in Settings → Access, once a bot is set.
func botEntry(w *web, id string) (entryRow, bool) {
	b := w.c.bot(id)
	if b == nil {
		return entryRow{}, false
	}
	p := b.page()
	if !p.Set {
		return entryRow{}, false
	}
	row := entryRow{Kind: id, URL: "/settings/access/" + id, Speaker: speakerAccount, Carries: carryText,
		State: "wait", Label: "acc.bot.notlinked"}
	if p.Bot != "" {
		row.Detail = "@" + p.Bot
	}
	switch {
	case p.Error != "":
		row.State, row.Label = "bad", "bot.failing"
	case p.Linked:
		row.State, row.Label = "on", "acc.bot.linked"
	}
	return row, true
}

// botPage is what the page shows; never the token.
type botPage struct {
	ID, Label   string
	Placeholder string
	VaultOpen   bool
	Bot         string // the bot's @name, once a token is set
	Set         bool   // a token is set
	Running     bool   // it listens
	Error       string // its last failure
	Linked      bool
	Name        string // the linked account
	Code        string // the code to send, while one waits
	Open        string // a link that opens the bot with the code
	Ignored     int64
}

func (b *botChannel) page() botPage {
	v := botPage{ID: b.spec.ID, Label: b.spec.Label, Placeholder: b.spec.Placeholder, VaultOpen: currentVault() != nil}
	v.Set = b.token() != ""
	v.Bot = b.get("bot")
	v.Running, v.Error = b.running(), b.err()
	v.Linked, v.Name = b.linked(), b.get("name")
	v.Ignored = b.getInt("ignored")
	if v.Set && !v.Linked {
		v.Code, _ = b.pendingCode()
		if v.Code != "" && v.Bot != "" && b.spec.OpenLink != nil {
			v.Open = b.spec.OpenLink(v.Bot, v.Code)
		}
	}
	return v
}

func (w *web) handleAccessBot(b *botChannel, rw http.ResponseWriter, r *http.Request, s *session) {
	w.accessRender(rw, r, s, "access-bot", b.page())
}

func (w *web) renderBot(b *botChannel, rw http.ResponseWriter, r *http.Request, s *session, msg string, ok bool) {
	if msg != "" {
		rw.Header().Set("HX-Trigger", hxTrigger(msg, ok, true))
	}
	w.render(rw, r, s, "settings", "bot-frag", page{Data: b.page()})
}

// handleBotState is the panel, polled while a code waits.
func (w *web) handleBotState(b *botChannel, rw http.ResponseWriter, r *http.Request, s *session) {
	w.renderBot(b, rw, r, s, "", true)
}

func (w *web) handleBotToken(b *botChannel, rw http.ResponseWriter, r *http.Request, s *session) {
	lang := w.lang(r)
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	if err := b.setToken(ctx, r.PostFormValue("token")); err != nil {
		if err == errNoVault {
			w.toast(rw, tr(lang, "bot.err.vault"), false)
		} else {
			w.toast(rw, fmt.Sprintf(tr(lang, "bot.err.token"), b.spec.Label), false)
		}
		return
	}
	w.renderBot(b, rw, r, s, tr(lang, "bot.saved"), true)
}

func (w *web) handleBotRemove(b *botChannel, rw http.ResponseWriter, r *http.Request, s *session) {
	b.removeToken()
	w.renderBot(b, rw, r, s, tr(w.lang(r), "bot.removed"), true)
}

func (w *web) handleBotCode(b *botChannel, rw http.ResponseWriter, r *http.Request, s *session) {
	if b.token() == "" {
		w.toast(rw, tr(w.lang(r), "bot.err.notoken"), false)
		return
	}
	b.newCode(w.lang(r))
	w.renderBot(b, rw, r, s, "", true)
}

func (w *web) handleBotUnlink(b *botChannel, rw http.ResponseWriter, r *http.Request, s *session) {
	b.unlink()
	w.renderBot(b, rw, r, s, tr(w.lang(r), "bot.unlinked"), true)
}

func (w *web) handleBotTest(b *botChannel, rw http.ResponseWriter, r *http.Request, _ *session) {
	lang := w.lang(r)
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	if b.test(ctx) != nil {
		w.toast(rw, tr(lang, "bot.test.failed"), false)
		return
	}
	w.toast(rw, fmt.Sprintf(tr(lang, "bot.test.sent"), b.spec.Label), true)
}
