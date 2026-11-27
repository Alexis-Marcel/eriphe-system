package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
)

// fakeWhisper is a service of the OpenAI audio API: it says what it
// received, and answers what the test sets.
type fakeWhisper struct {
	mu                            sync.Mutex
	path, auth, model, lang, file string
	prompt                        string
	audio                         []byte
	status                        int
	answer                        string
	srv                           *httptest.Server
}

func newFakeWhisper(t *testing.T) *fakeWhisper {
	f := &fakeWhisper{status: http.StatusOK, answer: `{"text":" Bonjour Eriphe. "}`}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.path, f.auth = r.URL.Path, r.Header.Get("Authorization")
		if r.URL.Path == "/v1/models" {
			if f.status != http.StatusOK {
				w.WriteHeader(f.status)
				return
			}
			w.Write([]byte(`{"object":"list","data":[{"id":"whisper-large-v3-turbo","object":"model"}]}`))
			return
		}
		if err := r.ParseMultipartForm(1 << 20); err == nil {
			f.model, f.lang, f.prompt = r.FormValue("model"), r.FormValue("language"), r.FormValue("prompt")
			if file, h, err := r.FormFile("file"); err == nil {
				f.file = h.Filename
				f.audio, _ = io.ReadAll(file)
				file.Close()
			}
		}
		w.WriteHeader(f.status)
		w.Write([]byte(f.answer))
	}))
	t.Cleanup(f.srv.Close)
	// The setting's address is on the LAN; the fake answers on the loopback.
	dial := func(ctx context.Context, network, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, network, f.srv.Listener.Addr().String())
	}
	voiceDial, selfHostedDial = dial, dial
	t.Cleanup(func() { voiceDial, selfHostedDial = nil, nil })
	return f
}

func voiceWeb(t *testing.T) (*testWeb, string) {
	tw := newTestWeb(t)
	csrf := tw.setup()
	openTestVault(t, tw.c.store)
	return tw, csrf
}

func TestVoiceSettings(t *testing.T) {
	tw, csrf := voiceWeb(t)
	code, body, _ := tw.do("GET", "/settings/voice", nil, nil)
	if code != 200 {
		t.Fatalf("GET /settings/voice: %d", code)
	}
	for _, want := range []string{`href="/settings/voice" aria-current="page"`, "couter", `value="groq"`, `value="server"`,
		"whisper-large-v3-turbo", "https://console.groq.com/keys", `name="preset" value="off" checked`} {
		if !strings.Contains(body, want) {
			t.Errorf("the page lacks %q", want)
		}
	}

	// Groq needs its key.
	_, body, _ = tw.post("/settings/voice", url.Values{"preset": {"groq"}, "language": {"fr"}}, csrf)
	if !strings.Contains(body, "Il faut la clé du service") || tw.c.voiceListen().on() {
		t.Fatalf("saved without a key: %v", tw.c.voiceListen())
	}
	_, _, h := tw.post("/settings/voice", url.Values{"preset": {"groq"}, "key": {" gsk_test "}, "model_groq": {"whisper-large-v3-turbo"}, "language": {"fr"}}, csrf)
	if h.Get("HX-Redirect") != "/settings/voice?saved=1" {
		t.Fatalf("not saved: %v", h)
	}
	l := tw.c.voiceListen()
	if !l.on() || l.BaseURL != "https://api.groq.com/openai/v1" || l.Model != "whisper-large-v3-turbo" || l.Language != "fr" || !l.Key {
		t.Fatalf("setting: %+v", l)
	}
	if k, _ := vaultSecret(voiceListenKey); k != "gsk_test" {
		t.Fatalf("key kept: %q", k)
	}
	if raw := tw.c.store.get(voiceListenSetting); strings.Contains(raw, "gsk_test") {
		t.Fatal("the key is in the setting")
	}
	// Saved again without the key: the key stays; the page never shows it.
	tw.post("/settings/voice", url.Values{"preset": {"groq"}, "language": {"auto"}}, csrf)
	if l := tw.c.voiceListen(); !l.Key || l.Language != "" {
		t.Fatalf("after a save without key: %+v", l)
	}
	_, body, _ = tw.do("GET", "/settings/voice?saved=1", nil, nil)
	if strings.Contains(body, "gsk_test") || !strings.Contains(body, "le micro est dans le champ") {
		t.Error("the page shows the key, or not that it was saved")
	}

	// A server of one's own: on the network, not localhost; no key needed.
	_, body, _ = tw.post("/settings/voice", url.Values{"preset": {"server"}, "base_url": {"http://localhost:8000/v1"}, "model_server": {"large-v3"}}, csrf)
	if !strings.Contains(body, "Cette adresse ne va pas") {
		t.Error("localhost was taken")
	}
	_, _, h = tw.post("/settings/voice", url.Values{"preset": {"server"}, "base_url": {"http://192.168.1.20:8000/v1/"}, "model_server": {"large-v3"}}, csrf)
	if h.Get("HX-Redirect") == "" {
		t.Fatal("the server was not saved")
	}
	if l := tw.c.voiceListen(); l.BaseURL != "http://192.168.1.20:8000/v1" || l.Model != "large-v3" || l.Key {
		t.Fatalf("server setting: %+v", l)
	}
	if _, err := vaultSecret(voiceListenKey); err == nil {
		t.Error("another service kept Groq's key")
	}

	// Off.
	tw.post("/settings/voice", url.Values{"preset": {"off"}}, csrf)
	if tw.c.voiceListen().on() || tw.c.store.get(voiceListenSetting) != "" {
		t.Error("not off")
	}
}

func TestVoiceTest(t *testing.T) {
	f := newFakeWhisper(t)
	tw, csrf := voiceWeb(t)
	_, body, _ := tw.post("/settings/voice/test", url.Values{"preset": {"server"}, "base_url": {"http://192.168.1.20:8000/v1"}, "model_server": {"large-v3"}}, csrf)
	if !strings.Contains(body, "Le service répond") || f.path != "/v1/models" || f.auth != "" {
		t.Fatalf("test: %s (path %s)", body, f.path)
	}
	f.status = http.StatusUnauthorized
	_, body, _ = tw.post("/settings/voice/test", url.Values{"preset": {"server"}, "base_url": {"http://192.168.1.20:8000/v1"}, "key": {"bad"}}, csrf)
	if strings.Contains(body, "Le service répond") || f.auth != "Bearer bad" {
		t.Fatalf("a refused key passed: %s", body)
	}
}

// A key goes only to the address it was given for: the test does not send
// it elsewhere, and a save at another address removes it and says so.
func TestVoiceKeyStaysWithItsAddress(t *testing.T) {
	f := newFakeWhisper(t)
	tw, csrf := voiceWeb(t)
	old := url.Values{"preset": {"server"}, "base_url": {"http://192.168.1.20:8000/v1"}, "model_server": {"large-v3"}}
	form := url.Values{"key": {"sk-home"}}
	for k, v := range old {
		form[k] = v
	}
	tw.post("/settings/voice", form, csrf)
	if !tw.c.voiceListen().Key {
		t.Fatal("the key was not kept")
	}
	moved := url.Values{"preset": {"server"}, "base_url": {"http://192.168.1.30:8000/v1"}, "model_server": {"large-v3"}}
	tw.post("/settings/voice/test", moved, csrf)
	if f.auth != "" {
		t.Fatalf("the key went to another address: %q", f.auth)
	}
	tw.post("/settings/voice/test", old, csrf)
	if f.auth != "Bearer sk-home" {
		t.Fatalf("the key did not go to its own address: %q", f.auth)
	}
	_, _, h := tw.post("/settings/voice", moved, csrf)
	if h.Get("HX-Redirect") != "/settings/voice?saved=1&dropped=1" || tw.c.voiceListen().Key {
		t.Fatalf("moved: %v, key %v", h.Get("HX-Redirect"), tw.c.voiceListen().Key)
	}
	if _, body, _ := tw.do("GET", "/settings/voice?saved=1&dropped=1", nil, nil); !strings.Contains(body, "a été retirée") {
		t.Error("the page does not say the key was removed")
	}
}

func TestVoicePromptBounded(t *testing.T) {
	long := []string{"Eriphe"}
	for range 200 {
		long = append(long, "Une page au titre assez long")
	}
	p := promptOf(long)
	if n := len([]rune(p)); n > voicePromptRunes+1 || !strings.HasPrefix(p, "Eriphe, Une page") || !strings.HasSuffix(p, "long.") {
		t.Fatalf("prompt (%d): %q", n, p)
	}
}

// postAudio sends a recording as the workspace does.
func postAudio(tw *testWeb, csrf, typ string, audio []byte) (int, map[string]string) {
	var b bytes.Buffer
	mw := multipart.NewWriter(&b)
	fw, _ := mw.CreateFormFile("audio", "speech")
	fw.Write(audio)
	if typ != "" {
		mw.WriteField("type", typ)
	}
	mw.Close()
	req, _ := http.NewRequest("POST", tw.srv.URL+"/api/voice/transcribe", &b)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("X-CSRF-Token", csrf)
	resp, err := tw.hc.Do(req)
	if err != nil {
		tw.t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]string
	json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func TestVoiceTranscribe(t *testing.T) {
	f := newFakeWhisper(t)
	tw, csrf := voiceWeb(t)
	audio := []byte("webm audio bytes")

	if code, out := postAudio(tw, csrf, "audio/webm;codecs=opus", audio); code != http.StatusNotFound || !strings.Contains(out["error"], "Réglages › Voix") {
		t.Fatalf("off: %d %v", code, out)
	}
	if err := tw.c.setVoiceListen(voiceListen{Preset: "server", BaseURL: "http://192.168.1.20:8000/v1", Model: "whisper-large-v3-turbo", Language: "fr"}, "gsk_test"); err != nil {
		t.Fatal(err)
	}
	code, out := postAudio(tw, csrf, "audio/webm;codecs=opus", audio)
	if code != 200 || out["text"] != "Bonjour Eriphe." {
		t.Fatalf("transcribe: %d %v", code, out)
	}
	if f.path != "/v1/audio/transcriptions" || f.auth != "Bearer gsk_test" || f.model != "whisper-large-v3-turbo" ||
		f.lang != "fr" || f.file != "speech.webm" || !bytes.Equal(f.audio, audio) || !strings.Contains(f.prompt, assistantName()) {
		t.Fatalf("sent: path %s auth %s model %s lang %s file %s prompt %q", f.path, f.auth, f.model, f.lang, f.file, f.prompt)
	}
	// Safari records MP4.
	if code, _ := postAudio(tw, csrf, "audio/mp4", audio); code != 200 || f.file != "speech.m4a" {
		t.Fatalf("mp4: %d %s", code, f.file)
	}
	if code, out := postAudio(tw, csrf, "image/png", audio); code != http.StatusUnsupportedMediaType || out["error"] == "" {
		t.Fatalf("a picture: %d %v", code, out)
	}
	if code, _ := postAudio(tw, csrf, "audio/webm", nil); code != http.StatusBadRequest {
		t.Fatalf("no audio: %d", code)
	}
	f.status, f.answer = http.StatusUnauthorized, `{"error":"invalid key"}`
	if code, out := postAudio(tw, csrf, "audio/webm", audio); code != http.StatusBadGateway || !strings.Contains(out["error"], "refuse la clé") {
		t.Fatalf("refused key: %d %v", code, out)
	}
	f.status, f.answer = http.StatusOK, `not json`
	if code, out := postAudio(tw, csrf, "audio/webm", audio); code != http.StatusBadGateway || !strings.Contains(out["error"], "n'a pas pu transcrire") {
		t.Fatalf("bad answer: %d %v", code, out)
	}
	// Not without the session's token.
	if code, _ := postAudio(tw, "", "audio/webm", audio); code != http.StatusForbidden {
		t.Fatalf("without CSRF: %d", code)
	}
}

func TestVoicePrompt(t *testing.T) {
	c := &core{}
	if p := c.voicePrompt(); p != assistantName()+"." {
		t.Fatalf("prompt without nodes: %q", p)
	}
}
