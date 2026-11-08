package main

import (
	"context"
	"strings"
	"testing"
	"time"
)

// Settings → Channels lists every channel with its direction, who it
// guarantees speaks and what it carries, in both languages; the voice says
// whether Home Assistant listens.
func TestWebChannelsPage(t *testing.T) {
	tw := newTestWeb(t)
	tw.setup()
	for lang, words := range map[string][]string{
		"fr": {"<h2>Canaux</h2>", "L&#39;espace et le terminal", "Tu lui parles", "C&#39;est toi : ta session ou ton jeton.",
			"Notifications sur tes appareils", "Il te prévient", "Porte le texte des notifications.",
			"Telegram", "Dans les deux sens", "ton compte, vérifié par un code",
			"La voix de la maison", "Quelqu&#39;un dans la pièce, peut-être pas toi", "Ne dit qu&#39;une phrase écrite par Eriphe",
			`id="telegram"`, `id="tg-panel"`, `id="voice"`, `hx-post="/settings/voice-token"`},
		"en": {"<h2>Channels</h2>", "The workspace and the CLI", "You talk to it", "It is you: your session or token.",
			"Notifications on your devices", "It tells you", "Carries the notifications&#39; text.",
			"Telegram", "Both ways", "your account, verified with a code",
			"The house&#39;s voice", "Someone in the room, maybe not you", "Says only a sentence written by Eriphe",
			`id="telegram"`, `id="tg-panel"`, `id="voice"`, `hx-post="/settings/voice-token"`},
	} {
		code, body, _ := tw.do("GET", "/settings/channels", nil, map[string]string{"Cookie": "lang=" + lang})
		if code != 200 {
			t.Fatalf("%s: GET %d", lang, code)
		}
		at := 0
		for _, want := range words {
			i := strings.Index(body[at:], want)
			if i < 0 {
				t.Fatalf("%s: the page lacks %q after its place", lang, want)
			}
			at += i + len(want)
		}
		if n := strings.Count(body, `<a href="/settings/channels" aria-current="page">`); n != 1 {
			t.Errorf("%s: Channels is not the current entry: %d", lang, n)
		}
		if !strings.Contains(body, `href="/settings/notifications#push"`) {
			t.Errorf("%s: push does not lead to its devices", lang)
		}
	}
	// The integration listening shows.
	go tw.c.voice.take(context.Background(), time.Second)
	time.Sleep(50 * time.Millisecond)
	tw.c.newVoiceToken()
	if _, body, _ := tw.do("GET", "/settings/channels", nil, nil); !strings.Contains(body, "Home Assistant écoute : les enceintes peuvent parler.") {
		t.Error("listening not shown")
	}
}

// Every channel has its words in both languages.
func TestChannelWords(t *testing.T) {
	for _, lang := range []string{"fr", "en"} {
		keys := []string{"settings.channels"}
		for _, k := range channelKinds {
			keys = append(keys, "chan.name."+k.ID)
			if k.Speaker != "" {
				keys = append(keys, "chan.speaker."+k.Speaker)
			}
			if k.Carries != "" {
				keys = append(keys, "chan.carries."+k.Carries)
			}
		}
		for _, d := range []string{"in", "out", "both"} {
			keys = append(keys, "chan.dir."+d)
		}
		for _, key := range keys {
			if _, ok := webStrings[lang][key]; !ok {
				t.Errorf("%s: no %s", lang, key)
			}
		}
	}
}
