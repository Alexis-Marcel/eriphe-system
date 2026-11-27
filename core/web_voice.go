package main

import (
	"errors"
	"io"
	"log"
	"net/http"
	"strings"
)

// Settings → Voice (voice.go): who hears the user. One form: a service
// (Groq, OpenAI, a server of one's own) or nothing, its key, its model and
// the language spoken; a test lists what the service serves. The workspace
// then shows a microphone in the message field, and sends what it records
// to POST /api/voice/transcribe, which answers the text: the user reads it
// in the field and sends it, as if typed.

func (w *web) voiceRoutes(mux *webMux) {
	mux.Handle("GET /settings/voice", w.auth(w.handleVoice))
	mux.Handle("POST /settings/voice", w.auth(w.handleSaveVoice))
	mux.Handle("POST /settings/voice/test", w.auth(w.handleTestVoice))
	mux.Handle("POST /api/voice/transcribe", http.MaxBytesHandler(w.auth(w.handleTranscribe), maxVoiceAudio+64<<10))
}

type voiceView struct {
	Listen  voiceListen
	Presets []voicePreset
	Server  string // the address of a server of one's own, as typed
	Error   string
	Saved   bool
	Dropped bool // the key kept was for another address, and was removed
	NoVault bool // a key cannot be kept
}

func (w *web) voiceView() voiceView {
	v := voiceView{Listen: w.c.voiceListen(), Presets: voicePresets, NoVault: currentVault() == nil}
	if v.Listen.Preset == "server" {
		v.Server = v.Listen.BaseURL
	}
	if v.Listen.Preset == "" {
		v.Listen.Language = "fr"
	}
	return v
}

func (w *web) handleVoice(rw http.ResponseWriter, r *http.Request, s *session) {
	v := w.voiceView()
	v.Saved = r.URL.Query().Get("saved") != ""
	v.Dropped = r.URL.Query().Get("dropped") != ""
	w.render(rw, r, s, "settings", "", page{Title: "settings.voice", Nav: "settings", Tab: "voice", Data: v})
}

// voiceForm reads the form: the preset's address and model unless the
// user changed the model, or typed the address of their own server.
func voiceForm(r *http.Request) (voiceListen, string) {
	l := voiceListen{Preset: r.PostFormValue("preset")}
	if l.Preset == "" || l.Preset == "off" {
		return voiceListen{}, ""
	}
	p, _ := voicePresetOf(l.Preset)
	l.BaseURL, l.Model = p.BaseURL, p.Model
	if l.Preset == "server" {
		l.BaseURL = strings.TrimRight(strings.TrimSpace(r.PostFormValue("base_url")), "/")
	}
	if m := strings.TrimSpace(r.PostFormValue("model_" + l.Preset)); m != "" {
		l.Model = m
	}
	l.Language = strings.TrimSpace(r.PostFormValue("language"))
	if l.Language == "auto" {
		l.Language = ""
	}
	return l, pastedKey(r.PostFormValue("key"))
}

func (w *web) handleSaveVoice(rw http.ResponseWriter, r *http.Request, s *session) {
	lang := w.lang(r)
	old := w.c.voiceListen()
	l, key := voiceForm(r)
	fail := func(k string) {
		v := w.voiceView()
		v.Listen, v.Error = l, tr(lang, k)
		if l.Preset == "server" {
			v.Server = l.BaseURL
		}
		w.render(rw, r, s, "settings", "voice-form", page{Data: v})
	}
	if k := l.check(); k != "" {
		fail(k)
		return
	}
	if p, _ := voicePresetOf(l.Preset); l.Preset != "" && !p.Keyless && key == "" && !old.keyFor(l) {
		fail("voice.err.nokey")
		return
	}
	if err := w.c.setVoiceListen(l, key); err != nil {
		if errors.Is(err, errNoVoiceVault) {
			fail("voice.err.novault")
			return
		}
		log.Printf("web: voice: %v", err)
		fail("err.internal")
		return
	}
	log.Printf("web: voice: listen %q", l.Preset) // never the key
	to := "/settings/voice?saved=1"
	if key == "" && old.Key && l.Preset != "" && !old.keyFor(l) {
		to += "&dropped=1" // the key kept was for another address: it is gone, and the page says so
	}
	rw.Header().Set("HX-Redirect", to)
	rw.WriteHeader(http.StatusOK)
}

// handleTestVoice asks the service of the form for its models (GET
// <base>/models), with the key typed or the one kept.
func (w *web) handleTestVoice(rw http.ResponseWriter, r *http.Request, s *session) {
	lang := w.lang(r)
	show := func(v testView) { w.render(rw, r, s, "settings", "tool-test", page{Data: v}) }
	if !w.testLimit.allow(s.ID, w.now()) {
		show(testView{State: "err", Text: tr(lang, "test.ratelimit")})
		return
	}
	old := w.c.voiceListen()
	l, key := voiceForm(r)
	if l.Preset == "" {
		show(testView{State: "err", Text: tr(lang, "voice.err.preset")})
		return
	}
	if checkBaseURL(l.BaseURL) != nil { // the model is not needed to try the service
		show(testView{State: "err", Text: tr(lang, "voice.err.baseurl")})
		return
	}
	if key == "" && old.keyFor(l) {
		key, _ = vaultSecret(voiceListenKey)
	}
	v := checkSelfHosted(r.Context(), lang, l.BaseURL, key)
	v.Models = nil // the list of a cloud service is long, and not all transcribe
	if v.State == "ok" {
		v.Text = tr(lang, "voice.test.ok")
	}
	log.Printf("web: voice test (%s): %s", l.Preset, v.State) // never the key
	show(v)
}

// handleTranscribe turns what the workspace recorded into text: a
// multipart form with "audio". It answers {"text"} or {"error"}, the
// error in the user's words.
func (w *web) handleTranscribe(rw http.ResponseWriter, r *http.Request, s *session) {
	lang := w.lang(r)
	fail := func(status int, k string) {
		writeJSON(rw, status, map[string]string{"error": tr(lang, k)})
	}
	if !w.c.voiceListen().on() {
		fail(http.StatusNotFound, "voice.fail.off")
		return
	}
	f, h, err := r.FormFile("audio")
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			fail(http.StatusRequestEntityTooLarge, "voice.fail.toolong")
			return
		}
		fail(http.StatusBadRequest, "voice.fail.noaudio")
		return
	}
	defer f.Close()
	audio, err := io.ReadAll(io.LimitReader(f, maxVoiceAudio+1))
	if err != nil || len(audio) == 0 {
		fail(http.StatusBadRequest, "voice.fail.noaudio")
		return
	}
	if len(audio) > maxVoiceAudio {
		fail(http.StatusRequestEntityTooLarge, "voice.fail.toolong")
		return
	}
	mime := h.Header.Get("Content-Type")
	if t := r.PostFormValue("type"); t != "" {
		mime = t // the recorder's own, with its codec
	}
	text, err := w.c.transcribe(r.Context(), audio, mime, w.c.voicePrompt())
	if err != nil {
		var ve *voiceError
		if errors.As(err, &ve) {
			log.Printf("web: voice: %v", ve) // never the audio, the text nor the key
			status := http.StatusBadGateway
			if ve.Key == "voice.fail.format" {
				status = http.StatusUnsupportedMediaType
			}
			fail(status, ve.Key)
			return
		}
		log.Printf("web: voice: %v", err)
		fail(http.StatusInternalServerError, "err.internal")
		return
	}
	writeJSON(rw, http.StatusOK, map[string]string{"text": text})
}
