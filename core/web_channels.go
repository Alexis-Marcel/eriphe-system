package main

import (
	"net/http"
	"slices"
	"strings"
)

// Every channel's part of the workspace: the routes and the start of the
// channels eriphe-core talks to (channels.go), and, in Settings →
// Notifications, the default channels of notify and how much each channel
// that carries text is told (channel_relay.go).

func (w *web) channelRoutes(mux *http.ServeMux) {
	for _, k := range channelKinds {
		if k.routes != nil {
			k.routes(w, mux)
		}
	}
	mux.Handle("POST /channels/settings", w.auth(w.handleChannelSettings))
}

// startChannels starts the channels that run on their own (a bot's poller).
func (w *web) startChannels() {
	for _, k := range channelKinds {
		if k.start != nil {
			k.start(w)
		}
	}
}

// channelChoice is a channel in the settings: whether notify goes there by
// default, and how much it is told.
type channelChoice struct {
	ID, Label string
	On        bool
	Detail    string // full or alert; "" for push
}

type channelsPage struct {
	Channels []channelChoice
}

func (w *web) channelsPage() channelsPage {
	def := w.c.defaultChannels()
	var v channelsPage
	for _, id := range sendChannels() {
		k, _ := channelKindOf(id)
		ch := channelChoice{ID: id, Label: k.Label, On: containsChannel(def, id)}
		if slices.ContainsFunc(textChannels(), func(t channelKind) bool { return t.ID == id }) {
			ch.Detail = w.c.channelDetail(id)
		}
		v.Channels = append(v.Channels, ch)
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

// handleChannelSettings keeps the default channels and how much each is told.
func (w *web) handleChannelSettings(rw http.ResponseWriter, r *http.Request, s *session) {
	lang := w.lang(r)
	r.ParseForm()
	channels, err := checkChannels(strings.Join(r.PostForm["channel"], ","))
	if err != nil || channels == "" {
		w.toast(rw, tr(lang, "channels.err.none"), false)
		return
	}
	details := map[string]string{}
	for _, k := range textChannels() {
		if d := r.PostFormValue("detail-" + k.ID); d != "" {
			if d != detailFull && d != detailAlert {
				http.Error(rw, "detail is full or alert", http.StatusBadRequest)
				return
			}
			details[k.ID] = d
		}
	}
	for id, d := range details {
		w.c.setChannelDetail(id, d)
	}
	w.c.store.set(notifyChannelsKey, channels)
	if lang == "fr" || lang == "en" {
		w.c.store.set(channelLangKey, lang)
	}
	rw.Header().Set("HX-Trigger", hxTrigger(tr(lang, "channels.saved"), true, true))
	w.render(rw, r, s, "settings", "channels-frag", page{Data: w.channelsPage()})
}
