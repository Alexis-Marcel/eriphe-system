package main

import (
	"net/http"
	"net/url"
	"strings"
)

// Settings → Tools → Home Assistant: the house both ways, on one page.
// Eriphe drives the house through Home Assistant's MCP server, a tool of
// the catalog like any other (the home-assistant preset); the house's
// voice talks to Eriphe through POST /v1/voice, with a token of its own
// that can only talk to the brain (voice.go).

type homeAssistantView struct {
	Tools []serverView // the catalog's tools that are a Home Assistant
	Voice bool         // Home Assistant has a voice token
}

// homeAssistantURL tells an address of Home Assistant's MCP server: the
// MCP Server integration serves /api/mcp, whatever the host.
func homeAssistantURL(u string) bool {
	p, err := url.Parse(u)
	return err == nil && p.Host != "" && strings.TrimSuffix(p.Path, "/") == "/api/mcp"
}

func (w *web) homeAssistant() homeAssistantView {
	v := homeAssistantView{Voice: w.c.store.get(voiceTokenKey) != ""}
	for _, sv := range w.servers() {
		if sv.Kind != "local" && homeAssistantURL(sv.URL) {
			v.Tools = append(v.Tools, sv)
		}
	}
	return v
}

func (w *web) handleHomeAssistant(rw http.ResponseWriter, r *http.Request, s *session) {
	w.render(rw, r, s, "settings", "", page{Title: "ha.title", Nav: "settings", Tab: "tools", View: "home-assistant",
		Data: w.homeAssistant()})
}

type voiceTokenView struct {
	Token string
	Core  string
}

// handleVoiceToken makes Home Assistant's voice token, shown once; a new
// one replaces the previous one.
func (w *web) handleVoiceToken(rw http.ResponseWriter, r *http.Request, s *session) {
	tok, err := w.c.newVoiceToken()
	if err != nil {
		w.toast(rw, tr(w.lang(r), "err.internal"), false)
		return
	}
	w.render(rw, r, s, "settings", "voice-token", page{Data: voiceTokenView{Token: tok, Core: w.coreURL(r)}})
}
