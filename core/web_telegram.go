package main

import (
	"context"
	"net/http"
	"strings"
	"time"
)

// Settings → Channels: where Eriphe reaches the user besides this
// device's notifications. Telegram's bot token, the link to the user's
// account, how much it says there, and the default channels of notify
// (telegram.go, channels.go).

func (w *web) channelRoutes(mux *http.ServeMux) {
	mux.Handle("GET /settings/channels", w.auth(w.handleChannels))
	mux.Handle("GET /channels/telegram", w.auth(w.handleTelegramState))
	mux.Handle("POST /channels/telegram/token", http.MaxBytesHandler(w.auth(w.handleTelegramToken), 4<<10))
	mux.Handle("POST /channels/telegram/remove", w.auth(w.handleTelegramRemove))
	mux.Handle("POST /channels/telegram/code", w.auth(w.handleTelegramCode))
	mux.Handle("POST /channels/telegram/unlink", w.auth(w.handleTelegramUnlink))
	mux.Handle("POST /channels/telegram/test", w.auth(w.handleTelegramTest))
	mux.Handle("POST /channels/settings", w.auth(w.handleChannelSettings))
}

// startTelegram opens the Telegram channel, polling while a bot is set.
func (w *web) startTelegram() {
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

// channelsPage is what the page shows; never the token.
type channelsPage struct {
	VaultOpen bool
	Bot       string // the bot's @username, once a token is set
	Set       bool   // a token is set
	Running   bool   // the poller runs
	Error     string // its last failure
	Linked    bool
	Name      string // the linked account
	Code      string // the code to send, while one waits
	Detail    string
	Ignored   int64
	Push      bool // the default channels
	Telegram  bool
}

func (w *web) channelsPage() channelsPage {
	t, st := w.c.tg, w.c.store
	v := channelsPage{VaultOpen: currentVault() != nil, Detail: detailFull}
	def := w.c.defaultChannels()
	v.Push, v.Telegram = containsChannel(def, chanPush), containsChannel(def, chanTelegram)
	if t == nil {
		return v
	}
	v.Set = t.token() != ""
	v.Bot = st.get(tgBotKey)
	v.Running, v.Error = t.running(), t.err()
	v.Linked, v.Name = t.linked(), st.get(tgNameKey)
	v.Detail, v.Ignored = t.detail(), t.kvInt(tgIgnoredKey)
	if v.Set && !v.Linked {
		v.Code, _ = t.pendingCode()
	}
	return v
}

func containsChannel(list, ch string) bool {
	for _, c := range strings.Split(list, ",") {
		if c == ch {
			return true
		}
	}
	return false
}

func (w *web) handleChannels(rw http.ResponseWriter, r *http.Request, s *session) {
	w.render(rw, r, s, "settings", "", page{Title: "settings.channels", Nav: "settings", Tab: "channels", Data: w.channelsPage()})
}

func (w *web) renderTelegram(rw http.ResponseWriter, r *http.Request, s *session, msg string, ok bool) {
	if msg != "" {
		rw.Header().Set("HX-Trigger", hxTrigger(msg, ok, true))
	}
	w.render(rw, r, s, "settings", "telegram-frag", page{Data: w.channelsPage()})
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

// handleChannelSettings keeps how much Telegram is told and the default channels.
func (w *web) handleChannelSettings(rw http.ResponseWriter, r *http.Request, s *session) {
	lang := w.lang(r)
	detail := r.PostFormValue("detail")
	if detail != detailFull && detail != detailAlert {
		http.Error(rw, "detail is full or alert", http.StatusBadRequest)
		return
	}
	channels, err := checkChannels(strings.Join(r.PostForm["channel"], ","))
	if err != nil || channels == "" {
		w.toast(rw, tr(lang, "channels.err.none"), false)
		return
	}
	w.c.store.set(tgDetailKey, detail)
	w.c.store.set(notifyChannelsKey, channels)
	if lang == "fr" || lang == "en" {
		w.c.store.set(tgLangKey, lang)
	}
	w.renderTelegram(rw, r, s, tr(lang, "channels.saved"), true)
}
