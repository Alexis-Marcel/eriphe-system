package main

import (
	"context"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"
)

// Settings → Channels lists the built-in channels with their direction,
// who they guarantee speaks and what they carry, Telegram's panel, then
// the user's channels, in both languages.
func TestWebChannelsPage(t *testing.T) {
	tw := newTestWeb(t)
	tw.setup()
	for lang, words := range map[string][]string{
		"fr": {"<h2>Canaux</h2>", `<a href="#builtin">`, `<a href="#telegram">`, `<a href="#added">`,
			"L&#39;espace et le terminal", "Tu lui parles", "C&#39;est toi : ta session ou ton jeton.",
			"Notifications sur tes appareils", "Il te prévient", "Porte le texte des notifications.",
			"Telegram", "Dans les deux sens", "ton compte, vérifié par un code",
			`id="telegram"`, `id="tg-panel"`, `id="added"`, "Tes canaux", "Ajouter un canal", `hx-post="/settings/channels/add"`},
		"en": {"<h2>Channels</h2>", `<a href="#builtin">`, `<a href="#telegram">`, `<a href="#added">`,
			"The workspace and the CLI", "You talk to it", "It is you: your session or token.",
			"Notifications on your devices", "It tells you", "Carries the notifications&#39; text.",
			"Telegram", "Both ways", "your account, verified with a code",
			`id="telegram"`, `id="tg-panel"`, `id="added"`, "Your channels", "Add a channel", `hx-post="/settings/channels/add"`},
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
}

// A channel is added with its token, shown once; changed (name, trust,
// what it says); its token replaced; removed. Whether its app listens
// shows.
func TestWebChannelsManage(t *testing.T) {
	tw := newTestWeb(t)
	csrf := tw.setup()
	c := tw.c
	tokRe := regexp.MustCompile(`ech_[0-9a-f]{64}`)

	_, body, _ := tw.post("/settings/channels/add", url.Values{"name": {"Home Assistant"}, "carries": {"sentence"}}, csrf)
	tok := tokRe.FindString(body)
	chs := c.addedChannels()
	if tok == "" || len(chs) != 1 || chs[0].Trusted || chs[0].Carries != carrySentence || !strings.Contains(body, "sans confiance") ||
		!strings.Contains(body, "n&#39;écoute pas") {
		t.Fatalf("add: %+v %s", chs, body)
	}
	id := idText(chs[0].ID)
	if _, body, _ := tw.do("GET", "/settings/channels", nil, nil); strings.Contains(body, tok) {
		t.Fatal("the token is shown again")
	}
	go c.out.take(context.Background(), chs[0].ID, nil, time.Second)
	time.Sleep(50 * time.Millisecond)
	if _, body, _ := tw.do("GET", "/settings/channels", nil, nil); !strings.Contains(body, ">écoute<") {
		t.Fatal("listening not shown")
	}

	_, body, _ = tw.post("/settings/channels/"+id, url.Values{"name": {"Maison"}, "trusted": {"1"}, "carries": {""}}, csrf)
	if ch, _ := c.addedChannel(chs[0].ID); ch.Name != "Maison" || !ch.Trusted || ch.Carries != "" || !strings.Contains(body, "de confiance") {
		t.Fatalf("set: %+v", ch)
	}
	_, body, _ = tw.post("/settings/channels/add", url.Values{"name": {"maison"}}, csrf)
	if len(c.addedChannels()) != 1 || !strings.Contains(body, `class="alert"`) {
		t.Fatal("a second channel with the same name")
	}

	_, body, _ = tw.post("/settings/channels/"+id+"/token", url.Values{}, csrf)
	if tok2 := tokRe.FindString(body); tok2 == "" || tok2 == tok {
		t.Fatal("no new token")
	}
	tw.post("/settings/channels/"+id+"/remove", url.Values{}, csrf)
	if len(c.addedChannels()) != 0 {
		t.Fatal("not removed")
	}
}

// Every built-in channel has its words in both languages.
func TestChannelWords(t *testing.T) {
	for _, lang := range []string{"fr", "en"} {
		keys := []string{"settings.channels", "chan.carries.sentence", "chan.carries.none", "chan.speaker.trusted", "chan.speaker.anyone"}
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
