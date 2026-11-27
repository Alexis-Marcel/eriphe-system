package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"
)

// Eriphe's voice (Settings → Voice, web_voice.go): who turns the user's
// speech into text. It is not a tool: the brain never calls it, the
// workspace does, as it would type; so it has no access policy, and the
// provider's key here is not the key of a tool of the same service. It is
// set like the brain's provider: a service and its key, or the address of a
// server of one's own. The one adapter speaks the OpenAI audio API, which
// Groq, OpenAI and the self-hosted Whisper servers (Speaches,
// faster-whisper-server…) serve: a service that does not is put behind one
// that does.
//
// The setting is a kv entry (voiceListenSetting), its key a vault secret
// (voiceListenKey): not a catalog entry, so no "provider." prefix, which
// the catalog sweeps when no entry names it (dropOrphanSecrets).

const (
	voiceListenSetting = "voice_listen"
	voiceListenKey     = "voice.listen.key"
	// maxVoiceAudio is the most audio one dictation sends: a few minutes
	// of compressed speech. OpenAI's own limit is 25 MB.
	maxVoiceAudio = 10 << 20
	// voiceTimeout bounds one transcription, the provider's answer included.
	voiceTimeout = 60 * time.Second
	// voicePromptRunes bounds the vocabulary given with the audio, well
	// under the 224 tokens of prompt Whisper takes (Groq's and OpenAI's
	// documentation): OpenAI rejects the whole request past its limit.
	voicePromptRunes = 400
)

// A voice service the page offers: its address and model filled in.
type voicePreset struct {
	ID, Name, BaseURL, Model, KeyPage string
	Keyless                           bool // a server of one's own may need no key
}

// voicePresets were checked against each service's documentation on
// 2026-11-27: https://console.groq.com/docs/speech-to-text (the endpoint,
// whisper-large-v3-turbo, WebM and M4A/MP4 read) and
// https://developers.openai.com/api/docs/guides/speech-to-text (the same
// formats; gpt-4o-transcribe still served, deprecated for gpt-transcribe
// and shut down on 2027-02-26: the user may change the model). "server" is
// a Whisper server of one's own, on the network.
var voicePresets = []voicePreset{
	{ID: "groq", Name: "Groq", BaseURL: "https://api.groq.com/openai/v1", Model: "whisper-large-v3-turbo", KeyPage: "https://console.groq.com/keys"},
	{ID: "openai", Name: "OpenAI", BaseURL: "https://api.openai.com/v1", Model: "gpt-4o-transcribe", KeyPage: "https://platform.openai.com/api-keys"},
	{ID: "server", Keyless: true},
}

func voicePresetOf(id string) (voicePreset, bool) {
	for _, p := range voicePresets {
		if p.ID == id {
			return p, true
		}
	}
	return voicePreset{}, false
}

// voiceListen is how Eriphe hears: the service, its address and model, the
// language spoken. Preset "" is off.
type voiceListen struct {
	Preset   string `json:"preset"`
	BaseURL  string `json:"base_url"`
	Model    string `json:"model"`
	Language string `json:"language,omitempty"` // ISO 639-1; "" lets the service guess
	Key      bool   `json:"key,omitempty"`      // a key is in the vault (voiceListenKey)
}

func (v voiceListen) on() bool { return v.Preset != "" && v.BaseURL != "" && v.Model != "" }

// keyFor tells whether the key kept goes with the setting l: the same
// service at the same address. A key never goes to an address it was not
// given for.
func (v voiceListen) keyFor(l voiceListen) bool {
	return v.Key && l.Preset != "" && v.Preset == l.Preset && v.BaseURL == l.BaseURL
}

// check tells what is wrong with a setting, as a text key of the page.
func (v voiceListen) check() string {
	if v.Preset == "" {
		return ""
	}
	if _, ok := voicePresetOf(v.Preset); !ok {
		return "voice.err.preset"
	}
	if checkBaseURL(v.BaseURL) != nil {
		return "voice.err.baseurl"
	}
	if v.Model == "" || len(v.Model) > 100 || strings.ContainsFunc(v.Model, func(r rune) bool { return !configSafe(r) || r == ' ' }) {
		return "voice.err.model"
	}
	if v.Language != "" && !isLanguage(v.Language) {
		return "voice.err.language"
	}
	return ""
}

func isLanguage(s string) bool {
	if len(s) != 2 {
		return false
	}
	for _, r := range s {
		if r < 'a' || r > 'z' {
			return false
		}
	}
	return true
}

// voiceListen is the setting, off when none is saved or it does not read.
func (c *core) voiceListen() voiceListen {
	var v voiceListen
	if raw := c.store.get(voiceListenSetting); raw != "" {
		if json.Unmarshal([]byte(raw), &v) != nil {
			return voiceListen{}
		}
	}
	return v
}

// errNoVoiceVault: a key cannot be kept, the vault is not open.
var errNoVoiceVault = errors.New("the vault is not open")

// setVoiceListen saves the setting. key is a key typed in: "" keeps the
// one kept, if the service stays the same; another service, or off, drops
// it.
func (c *core) setVoiceListen(v voiceListen, key string) error {
	old := c.voiceListen()
	vault := currentVault()
	switch {
	case key != "":
		if vault == nil {
			return errNoVoiceVault
		}
		if err := vault.set(voiceListenKey, key); err != nil {
			return err
		}
		v.Key = true
	case old.keyFor(v):
		v.Key = true
	default:
		v.Key = false
	}
	if !v.Key && vault != nil {
		vault.remove(voiceListenKey) // nothing names it any more
	}
	if v.Preset == "" {
		return c.store.set(voiceListenSetting, "")
	}
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return c.store.set(voiceListenSetting, string(b))
}

// voiceDial dials the voice service: the transport's own, but for tests,
// whose fake answers on the loopback the setting refuses.
var voiceDial func(ctx context.Context, network, addr string) (net.Conn, error)

func voiceClient() *http.Client {
	t := http.DefaultTransport.(*http.Transport).Clone()
	if voiceDial != nil {
		t.DialContext = voiceDial
	}
	// A redirect is not followed: the key would go along.
	return &http.Client{Transport: t, Timeout: voiceTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

// voiceError is why a transcription failed, as a text key of the
// workspace: the user reads it under the field.
type voiceError struct {
	Key    string // voice.fail.*
	Detail string // for the log, never the audio, the text nor the key
}

func (e *voiceError) Error() string { return e.Key + ": " + e.Detail }

// audioExt is the file name the service reads the audio's format from:
// the browsers record WebM (Chrome, Firefox) or MP4 (Safari).
func audioExt(mime string) (string, bool) {
	mime, _, _ = strings.Cut(strings.ToLower(strings.TrimSpace(mime)), ";")
	switch mime {
	case "audio/webm", "video/webm":
		return "webm", true
	case "audio/mp4", "audio/m4a", "audio/x-m4a", "video/mp4", "audio/aac":
		return "m4a", true
	case "audio/ogg", "audio/opus":
		return "ogg", true
	case "audio/wav", "audio/x-wav", "audio/wave":
		return "wav", true
	case "audio/mpeg", "audio/mp3":
		return "mp3", true
	}
	return "", false
}

// transcribe turns speech into text: POST <base>/audio/transcriptions,
// the OpenAI audio API. prompt is the words to expect (the assistant's
// name, the nodes' titles), so that the names come out right.
func (c *core) transcribe(ctx context.Context, audio []byte, mime, prompt string) (string, error) {
	v := c.voiceListen()
	if !v.on() {
		return "", &voiceError{Key: "voice.fail.off"}
	}
	ext, ok := audioExt(mime)
	if !ok {
		return "", &voiceError{Key: "voice.fail.format", Detail: mime}
	}
	key := ""
	if v.Key {
		var err error
		if key, err = vaultSecret(voiceListenKey); err != nil {
			return "", &voiceError{Key: "voice.fail.nokey", Detail: err.Error()}
		}
	}
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", "speech."+ext)
	if err != nil {
		return "", err
	}
	fw.Write(audio)
	mw.WriteField("model", v.Model)
	mw.WriteField("response_format", "json")
	if v.Language != "" {
		mw.WriteField("language", v.Language)
	}
	if prompt != "" {
		mw.WriteField("prompt", prompt)
	}
	if err := mw.Close(); err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, v.BaseURL+"/audio/transcriptions", &body)
	if err != nil {
		return "", &voiceError{Key: "voice.fail.unreachable", Detail: err.Error()}
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := voiceClient().Do(req)
	if err != nil {
		k := "voice.fail.unreachable"
		if errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil {
			k = "voice.fail.timeout"
		}
		return "", &voiceError{Key: k, Detail: err.Error()}
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return "", &voiceError{Key: "voice.fail.auth", Detail: resp.Status}
	case resp.StatusCode == http.StatusTooManyRequests:
		return "", &voiceError{Key: "voice.fail.busy", Detail: resp.Status}
	case resp.StatusCode != http.StatusOK:
		return "", &voiceError{Key: "voice.fail.service", Detail: fmt.Sprintf("%s: %s", resp.Status, truncate(string(raw), 200))}
	}
	var out struct {
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &out) != nil {
		return "", &voiceError{Key: "voice.fail.service", Detail: "not the expected JSON"}
	}
	return strings.TrimSpace(out.Text), nil
}

// voicePrompt is the vocabulary given with the audio: the assistant's name
// and the titles of the nodes changed last, the names the user says most.
func (c *core) voicePrompt() string {
	words := []string{assistantName()}
	if c.nodes != nil {
		if recent, err := c.nodes.Recent(false, 40); err == nil {
			for _, n := range recent {
				if t := strings.TrimSpace(n.Title); t != "" {
					words = append(words, t)
				}
			}
		}
	}
	return promptOf(words)
}

// promptOf joins the words, the first ones first, within voicePromptRunes.
func promptOf(words []string) string {
	var b strings.Builder
	for _, w := range words {
		if utf8.RuneCountInString(b.String())+utf8.RuneCountInString(w)+2 > voicePromptRunes {
			break
		}
		if b.Len() > 0 {
			b.WriteString(", ")
		}
		b.WriteString(w)
	}
	if b.Len() > 0 {
		b.WriteString(".")
	}
	return b.String()
}
