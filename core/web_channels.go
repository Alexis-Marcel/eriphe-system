package main

import (
	"net/http"
	"strconv"
	"time"
)

// Settings → Channels: every way the user and Eriphe talk, on one page.
// The built-in channels (channels.go) with their direction, who they
// guarantee speaks and what they carry; Telegram's bot set up here, push
// leading to its devices in Notifications. Then the channels the user adds
// (channels_added.go): any app that talks with text, each with its token,
// shown once, and what the user declares of it: trusted or not, and what it
// may carry.

type channelRowView struct {
	ID      string
	Dir     string // in, out or both
	Speaker string // inbound: who speaks; "" when it only notifies
	Carries string // outbound: what it carries; "" when it only receives
	Ready   bool
	Link    string // where it is set up
}

type addedChannelView struct {
	addedChannel
	Listening bool
}

// addedChannelsView is the section of the added channels, alone so that
// each form answers with it.
type addedChannelsView struct {
	Channels []addedChannelView
	Max      bool
	Blank    addedChannel // the add form's
	Error    string
	// A token just made, shown once, with the address the app talks to.
	Token    string
	TokenFor int64
	Core     string
}

type channelsSettingsView struct {
	Rows     []channelRowView
	Telegram channelsPage
	Added    addedChannelsView
}

func (w *web) addedChannelsView(r *http.Request) addedChannelsView {
	var v addedChannelsView
	now := time.Now()
	for _, ch := range w.c.addedChannels() {
		v.Channels = append(v.Channels, addedChannelView{addedChannel: ch, Listening: w.c.out.listening(ch.ID, now)})
	}
	v.Max = len(v.Channels) >= maxChannels
	v.Core = w.coreURL(r)
	return v
}

func (w *web) channelsSettings(r *http.Request) channelsSettingsView {
	v := channelsSettingsView{Telegram: w.channelsPage(), Added: w.addedChannelsView(r)}
	for _, k := range channelKinds {
		row := channelRowView{ID: k.ID, Speaker: k.Speaker, Carries: k.Carries}
		switch k.Dir {
		case dirIn:
			row.Dir = "in"
		case dirOut:
			row.Dir = "out"
		default:
			row.Dir = "both"
		}
		switch k.ID {
		case chanWorkspace:
			row.Ready = true
		case chanPush:
			row.Ready, row.Link = k.ready(w.c), "/settings/notifications#push"
		case chanTelegram:
			row.Ready, row.Link = k.ready(w.c), "#telegram"
		}
		v.Rows = append(v.Rows, row)
	}
	return v
}

func (w *web) channelsSettingsRoutes(mux *http.ServeMux) {
	mux.Handle("GET /settings/channels", w.auth(w.handleChannels))
	mux.Handle("POST /settings/channels/add", w.auth(w.handleChannelAdd))
	mux.Handle("POST /settings/channels/{id}", w.auth(w.handleChannelSet))
	mux.Handle("POST /settings/channels/{id}/token", w.auth(w.handleChannelToken))
	mux.Handle("POST /settings/channels/{id}/remove", w.auth(w.handleChannelRemove))
}

func (w *web) handleChannels(rw http.ResponseWriter, r *http.Request, s *session) {
	w.render(rw, r, s, "settings", "", page{Title: "settings.channels", Nav: "settings", Tab: "channels", Data: w.channelsSettings(r)})
}

// added answers a form with the section of the added channels.
func (w *web) added(rw http.ResponseWriter, r *http.Request, s *session, err error, tok string, id int64) {
	v := w.addedChannelsView(r)
	if err != nil {
		v.Error = err.Error()
	}
	v.Token, v.TokenFor = tok, id
	w.render(rw, r, s, "settings", "channels-added", page{Data: v})
}

func (w *web) handleChannelAdd(rw http.ResponseWriter, r *http.Request, s *session) {
	ch, tok, err := w.c.addChannel(r.PostFormValue("name"), r.PostFormValue("trusted") == "1", r.PostFormValue("carries"))
	w.added(rw, r, s, err, tok, ch.ID)
}

func (w *web) handleChannelSet(rw http.ResponseWriter, r *http.Request, s *session) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	err := w.c.setChannel(id, r.PostFormValue("name"), r.PostFormValue("trusted") == "1", r.PostFormValue("carries"))
	w.added(rw, r, s, err, "", 0)
}

func (w *web) handleChannelToken(rw http.ResponseWriter, r *http.Request, s *session) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	tok, err := w.c.replaceChannelToken(id)
	w.added(rw, r, s, err, tok, id)
}

func (w *web) handleChannelRemove(rw http.ResponseWriter, r *http.Request, s *session) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	w.added(rw, r, s, w.c.removeChannel(id), "", 0)
}
