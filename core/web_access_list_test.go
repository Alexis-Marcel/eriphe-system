package main

import (
	"context"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"
)

// Settings → Access lists the web and the mobile app, then Telegram once
// its bot is set, then the API accesses, each with who speaks, what it
// carries and its state; "Ajouter un accès" opens the catalog. In both
// languages.
func TestWebAccessList(t *testing.T) {
	tw := newTestWeb(t)
	tw.setup()
	if _, _, err := tw.c.addChannel("Home Assistant", false, carrySentence); err != nil {
		t.Fatal(err)
	}
	if _, _, err := tw.c.addChannel("Raccourci", true, ""); err != nil {
		t.Fatal(err)
	}
	for lang, words := range map[string][]string{
		"fr": {"<h2>Accès</h2>", "Ajouter un accès", "Espace web", "C&#39;est toi : ta session ou ton jeton.", "App mobile", "Aucun appareil",
			"Porte le texte des notifications.", "Home Assistant", "n&#39;écoute pas", "Sans confiance", "Raccourci", "De confiance", "Ne fait que recevoir.",
			`id="access-dlg"`, "Telegram", "Application ou script"},
		"en": {"<h2>Access</h2>", "Add an access", "Web workspace", "It is you: your session or token.", "Mobile app", "No device",
			"Carries the notifications&#39; text.", "Home Assistant", "not listening", "Not trusted", "Raccourci", "Trusted", "Only receives.",
			`id="access-dlg"`, "Telegram", "App or script"},
	} {
		code, body, _ := tw.do("GET", "/settings/access", nil, map[string]string{"Cookie": "lang=" + lang})
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
		if n := strings.Count(body, `<a href="/settings/access" aria-current="page">`); n != 1 {
			t.Errorf("%s: Access is not the current entry: %d", lang, n)
		}
		for _, want := range []string{`href="/settings/access/web"`, `href="/settings/access/app"`, `href="/settings/access/api/1"`, `href="/settings/access/api/2"`,
			`href="/settings/access/new/telegram"`, `href="/settings/access/new/api"`, `data-dialog="access-dlg"`} {
			if !strings.Contains(body, want) {
				t.Errorf("%s: no %s", lang, want)
			}
		}
		if strings.Contains(body, `href="/settings/access/telegram"`) {
			t.Errorf("%s: Telegram listed before its bot is set", lang)
		}
	}
	// Without JavaScript, the catalog is a page.
	if code, body, _ := tw.do("GET", "/settings/access/new", nil, nil); code != 200 || !strings.Contains(body, `href="/settings/access/new/api"`) ||
		strings.Contains(body, "/settings/access/new/home-assistant") {
		t.Fatalf("catalog page: %d", code)
	}
	// Telegram's entry leads to its page; an unknown kind is not found.
	if code, _, h := tw.do("GET", "/settings/access/new/telegram", nil, nil); code != 303 || h.Get("Location") != "/settings/access/telegram" {
		t.Fatalf("telegram: %d %s", code, h.Get("Location"))
	}
	if code, _, _ := tw.do("GET", "/settings/access/new/home-assistant", nil, nil); code != 404 {
		t.Fatalf("Home Assistant is no kind of its own: %d", code)
	}
	if code, _, _ := tw.do("GET", "/settings/access/new/discord", nil, nil); code != 404 {
		t.Fatalf("an unknown kind: %d", code)
	}
}

// An API access is added from its kind's form, filled as the kind says;
// its token shows once, and the address becomes its page's. It is changed,
// its token replaced, and it is removed. Whether its app listens shows.
func TestWebAccessAPI(t *testing.T) {
	tw := newTestWeb(t)
	csrf := tw.setup()
	c := tw.c
	tokRe := regexp.MustCompile(`ech_[0-9a-f]{64}`)

	_, body, _ := tw.do("GET", "/settings/access/new/api", nil, nil)
	for _, want := range []string{`name="kind" value="api"`, "Appareils et services", `hx-post="/settings/access/api"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("the form lacks %q", want)
		}
	}
	if strings.Contains(body, `name="trusted" value="1" checked`) {
		t.Fatal("trusted by default")
	}
	code, body, h := tw.post("/settings/access/api", url.Values{"kind": {"api"}, "name": {"Home Assistant"}, "carries": {"sentence"}}, csrf)
	tok := tokRe.FindString(body)
	chs := c.addedChannels()
	if code != 200 || tok == "" || len(chs) != 1 || chs[0].Kind != apiGeneric || chs[0].Trusted || chs[0].Carries != carrySentence ||
		h.Get("HX-Push-Url") != "/settings/access/api/"+idText(chs[0].ID) || !strings.Contains(body, `id="access-api"`) {
		t.Fatalf("add: %d %+v %v", code, chs, h)
	}
	id := idText(chs[0].ID)
	if _, body, _ := tw.do("GET", "/settings/access/api/"+id, nil, nil); strings.Contains(body, tok) || !strings.Contains(body, "sans confiance") {
		t.Fatal("the token is shown again, or the trust is not")
	}
	go c.out.take(context.Background(), chs[0].ID, nil, nil, time.Second)
	time.Sleep(50 * time.Millisecond)
	if _, body, _ := tw.do("GET", "/settings/access", nil, nil); !strings.Contains(body, ">écoute<") {
		t.Fatal("listening not shown")
	}

	_, body, _ = tw.post("/settings/access/api/"+id, url.Values{"name": {"Maison"}, "trusted": {"1"}, "carries": {""}}, csrf)
	if ch, _ := c.addedChannel(chs[0].ID); ch.Name != "Maison" || !ch.Trusted || ch.Carries != "" || ch.Kind != apiGeneric || !strings.Contains(body, "de confiance") {
		t.Fatalf("set: %+v", ch)
	}
	// A second with the same name: refused, the form kept.
	_, body, h = tw.post("/settings/access/api", url.Values{"kind": {"api"}, "name": {"maison"}}, csrf)
	if len(c.addedChannels()) != 1 || !strings.Contains(body, `class="alert"`) || !strings.Contains(body, `name="kind" value="api"`) || h.Get("HX-Push-Url") != "" {
		t.Fatal("a second access with the same name")
	}
	// A kind not known is a generic one.
	tw.post("/settings/access/api", url.Values{"kind": {"discord"}, "name": {"Script"}}, csrf)
	if chs := c.addedChannels(); len(chs) != 2 || chs[1].Kind != apiGeneric {
		t.Fatalf("generic: %+v", chs)
	}

	_, body, _ = tw.post("/settings/access/api/"+id+"/token", url.Values{}, csrf)
	if tok2 := tokRe.FindString(body); tok2 == "" || tok2 == tok {
		t.Fatal("no new token")
	}
	code, _, h = tw.post("/settings/access/api/"+id+"/remove", url.Values{}, csrf)
	if len(c.addedChannels()) != 1 || code != 204 || h.Get("HX-Redirect") != "/settings/access" {
		t.Fatalf("remove: %d %v", code, h)
	}
	if code, _, _ := tw.do("GET", "/settings/access/api/"+id, nil, nil); code != 404 {
		t.Fatalf("a removed access's page: %d", code)
	}
}

// The former pages lead to Access, and their sections to each access's
// page (early.js).
func TestWebAccessMoved(t *testing.T) {
	tw := newTestWeb(t)
	tw.setup()
	for from, to := range map[string]string{"/settings/remote": "/settings/access", "/settings/channels": "/settings/access",
		"/settings/home-assistant": "/settings/tools"} {
		if code, _, h := tw.do("GET", from, nil, nil); code != 301 || h.Get("Location") != to {
			t.Errorf("GET %s: %d %s", from, code, h.Get("Location"))
		}
	}
	early, err := webFS.ReadFile("web/static/early.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"#telegram": "telegram"`, `"#push": "app"`, `"#app": "app"`, `location.replace("/settings/access/" + page)`} {
		if !strings.Contains(string(early), want) {
			t.Errorf("early.js lacks %s", want)
		}
	}
	// The app's page has the install, the push switch and the devices; the
	// notifications page only links to it.
	_, body, _ := tw.do("GET", "/settings/access/app", nil, nil)
	for _, want := range []string{`data-install`, `id="push"`, `id="push-toggle"`, `id="push-devices"`, `href="#install"`} {
		if !strings.Contains(body, want) {
			t.Errorf("the app's page lacks %s", want)
		}
	}
	if _, body, _ := tw.do("GET", "/settings/access/telegram", nil, nil); !strings.Contains(body, `id="tg-panel"`) {
		t.Error("Telegram's page has no panel")
	}
}

// Every kind of access has its words in both languages, and those of what
// rows say.
func TestAccessWords(t *testing.T) {
	for _, lang := range []string{"fr", "en"} {
		keys := []string{"settings.access", "acc.lead", "acc.add", "acc.app.none", "acc.app.device", "acc.app.devices", "acc.tg.linked", "acc.tg.notlinked"}
		for _, k := range []string{entryWeb, entryApp, tgChannel, apiGeneric} {
			keys = append(keys, "acc.kind."+k, "acc.kind."+k+".desc")
		}
		keys = append(keys, "acc.api.how."+apiGeneric)
		for _, s := range []string{speakerUser, speakerAccount, "trusted", "anyone"} {
			keys = append(keys, "chan.speaker."+s)
		}
		for _, cr := range []string{carryText, carrySentence, "none"} {
			keys = append(keys, "chan.carries."+cr)
		}
		for _, key := range keys {
			if _, ok := webStrings[lang][key]; !ok {
				t.Errorf("%s: no %s", lang, key)
			}
		}
	}
}

// Every API access is generic: from before kinds, or of Home Assistant's
// former kind.
func TestChannelKindMigration(t *testing.T) {
	c, _ := testMemoryCore(t)
	c.addChannel("Home Assistant", false, carrySentence)
	c.addChannel("Script", false, "")
	c.store.db.Exec(`UPDATE channels SET kind = 'home-assistant' WHERE name = 'Home Assistant'`)
	c.store.db.Exec(`UPDATE channels SET kind = '' WHERE name = 'Script'`)
	for _, q := range extraColumns {
		if strings.Contains(q, "channels") {
			c.store.db.Exec(q)
		}
	}
	chs := c.addedChannels()
	if len(chs) != 2 || chs[0].Kind != apiGeneric || chs[1].Kind != apiGeneric {
		t.Fatalf("%+v", chs)
	}
}
