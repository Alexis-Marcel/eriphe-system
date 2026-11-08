package main

import (
	"net/http"
	"time"
)

// Settings → Channels: every way the user and Eriphe talk, on one page,
// each with its direction, who it guarantees speaks and what it carries
// (channels.go). Telegram and the house's voice are set up here; push, whose
// devices are the router's (Où te prévenir), is set up in Notifications.

type channelRowView struct {
	ID      string
	Dir     string // in, out or both
	Speaker string // inbound: who speaks; "" when it only notifies
	Carries string // outbound: what it carries; "" when it only receives
	Ready   bool
	Link    string // where it is set up
}

type channelsSettingsView struct {
	Rows     []channelRowView
	Telegram channelsPage
	Voice    voiceChannelView
}

// voiceChannelView is the voice's state: its token, whether Home
// Assistant's integration listens for what to say, and the speakers mapped.
type voiceChannelView struct {
	Token     bool
	Listening bool
	Speakers  bool
}

func (w *web) channelsSettings() channelsSettingsView {
	v := channelsSettingsView{Telegram: w.channelsPage()}
	v.Voice = voiceChannelView{
		Token:     w.c.store.get(voiceTokenKey) != "",
		Listening: w.c.voice.listening(time.Now()),
		Speakers:  w.c.haVoiceReady(),
	}
	for _, k := range channelKinds {
		row := channelRowView{ID: k.ID, Speaker: k.Speaker, Carries: k.Carries, Link: "#" + k.ID}
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
			row.Ready, row.Link = true, ""
		case chanPush:
			row.Ready, row.Link = k.ready(w.c), "/settings/notifications#push"
		case chanTelegram:
			row.Ready = k.ready(w.c)
		case chanVoice:
			row.Ready = v.Voice.Token
		}
		v.Rows = append(v.Rows, row)
	}
	return v
}

func (w *web) handleChannels(rw http.ResponseWriter, r *http.Request, s *session) {
	w.render(rw, r, s, "settings", "", page{Title: "settings.channels", Nav: "settings", Tab: "channels", Data: w.channelsSettings()})
}
