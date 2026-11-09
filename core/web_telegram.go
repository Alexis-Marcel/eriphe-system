package main

import (
	"context"
	"net/http"
	"time"
)

// Telegram's bot token and the link to the user's account, in Settings →
// Access (telegram.go). How much it says there and the default channels
// are every channel's (web_channels.go).

func telegramRoutes(w *web, mux *http.ServeMux) {
	mux.Handle("GET /settings/access/telegram", w.auth(w.handleAccessTelegram))
	mux.Handle("GET /channels/telegram", w.auth(w.handleTelegramState))
	mux.Handle("POST /channels/telegram/token", http.MaxBytesHandler(w.auth(w.handleTelegramToken), 4<<10))
	mux.Handle("POST /channels/telegram/remove", w.auth(w.handleTelegramRemove))
	mux.Handle("POST /channels/telegram/code", w.auth(w.handleTelegramCode))
	mux.Handle("POST /channels/telegram/unlink", w.auth(w.handleTelegramUnlink))
	mux.Handle("POST /channels/telegram/test", w.auth(w.handleTelegramTest))
}

func (w *web) handleAccessTelegram(rw http.ResponseWriter, r *http.Request, s *session) {
	w.accessRender(rw, r, s, "access-telegram", w.telegramPage())
}

// telegramEntry is its row in Settings → Access, once a bot is set.
func telegramEntry(w *web) (entryRow, bool) {
	tg := w.telegramPage()
	if !tg.Set {
		return entryRow{}, false
	}
	row := entryRow{Kind: tgChannel, URL: "/settings/access/telegram", Speaker: speakerAccount, Carries: carryText,
		State: "wait", Label: "acc.tg.notlinked"}
	if tg.Bot != "" {
		row.Detail = "@" + tg.Bot
	}
	switch {
	case tg.Error != "":
		row.State, row.Label = "bad", "tg.failing"
	case tg.Linked:
		row.State, row.Label = "on", "acc.tg.linked"
	}
	return row, true
}

// startTelegram opens the Telegram channel, polling while a bot is set.
func startTelegram(w *web) {
	t := newTelegram(w.c)
	t.base = func() string {
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
	w.c.tg = t
	t.restart()
}

// telegramPage is what the page shows; never the token.
type telegramPage struct {
	VaultOpen bool
	Bot       string // the bot's @username, once a token is set
	Set       bool   // a token is set
	Running   bool   // the poller runs
	Error     string // its last failure
	Linked    bool
	Name      string // the linked account
	Code      string // the code to send, while one waits
	Ignored   int64
}

func (w *web) telegramPage() telegramPage {
	t, st := w.c.tg, w.c.store
	v := telegramPage{VaultOpen: currentVault() != nil}
	if t == nil {
		return v
	}
	v.Set = t.token() != ""
	v.Bot = st.get(tgBotKey)
	v.Running, v.Error = t.running(), t.err()
	v.Linked, v.Name = t.linked(), st.get(tgNameKey)
	v.Ignored = t.kvInt(tgIgnoredKey)
	if v.Set && !v.Linked {
		v.Code, _ = t.pendingCode()
	}
	return v
}

func (w *web) renderTelegram(rw http.ResponseWriter, r *http.Request, s *session, msg string, ok bool) {
	if msg != "" {
		rw.Header().Set("HX-Trigger", hxTrigger(msg, ok, true))
	}
	w.render(rw, r, s, "settings", "telegram-frag", page{Data: w.telegramPage()})
}

// handleTelegramState is the Telegram panel, polled while a code waits.
func (w *web) handleTelegramState(rw http.ResponseWriter, r *http.Request, s *session) {
	w.renderTelegram(rw, r, s, "", true)
}

func (w *web) handleTelegramToken(rw http.ResponseWriter, r *http.Request, s *session) {
	lang := w.lang(r)
	if w.c.tg == nil {
		w.toast(rw, tr(lang, "tg.err.off"), false)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	if err := w.c.tg.setToken(ctx, r.PostFormValue("token")); err != nil {
		key := "tg.err.token"
		if err == errNoVault {
			key = "tg.err.vault"
		}
		w.toast(rw, tr(lang, key), false)
		return
	}
	w.renderTelegram(rw, r, s, tr(lang, "tg.saved"), true)
}

func (w *web) handleTelegramRemove(rw http.ResponseWriter, r *http.Request, s *session) {
	if w.c.tg != nil {
		w.c.tg.removeToken()
	}
	w.renderTelegram(rw, r, s, tr(w.lang(r), "tg.removed"), true)
}

func (w *web) handleTelegramCode(rw http.ResponseWriter, r *http.Request, s *session) {
	if w.c.tg == nil || w.c.tg.token() == "" {
		w.toast(rw, tr(w.lang(r), "tg.err.notoken"), false)
		return
	}
	w.c.tg.newCode(w.lang(r))
	w.renderTelegram(rw, r, s, "", true)
}

func (w *web) handleTelegramUnlink(rw http.ResponseWriter, r *http.Request, s *session) {
	if w.c.tg != nil {
		w.c.tg.unlink()
	}
	w.renderTelegram(rw, r, s, tr(w.lang(r), "tg.unlinked"), true)
}

func (w *web) handleTelegramTest(rw http.ResponseWriter, r *http.Request, _ *session) {
	lang := w.lang(r)
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	if w.c.tg == nil || w.c.tg.test(ctx) != nil {
		w.toast(rw, tr(lang, "tg.test.failed"), false)
		return
	}
	w.toast(rw, tr(lang, "tg.test.sent"), true)
}
