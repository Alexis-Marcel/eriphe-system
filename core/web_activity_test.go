package main

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// The Journal is a discreet entry of the settings, no longer a tab of the
// main navigation: its feed filters by kind and period, and the tool
// accesses are the decisions' journal, paged.
func TestJournalInSettings(t *testing.T) {
	tw := newTestWeb(t)
	tw.setup()
	c := tw.c
	c.notify(&notification{Text: "Le serveur répond lentement.", Why: "Une alerte.", Source: notifyFromBrain}, notifyItem{Level: levelBrief})
	c.store.insertTask(&TaskRow{ID: "t-9", Kind: "worker", Runtime: "claude-code", State: stateRunning, Prompt: "Range les factures"})
	c.store.recordDecision("t-9", "p-0", "Bash", map[string]any{"command": "git push origin main"}, decideDeny, byPolicy, "no pushing to main")

	code, body, _ := tw.do("GET", "/settings/journal?range=day", nil, nil)
	if code != 200 {
		t.Fatalf("GET /settings/journal: %d", code)
	}
	for _, want := range []string{`class="settings-nav"`, `<a href="/settings/journal" aria-current="page">`, "<h2>Journal</h2>",
		"Le serveur répond lentement.", "Range les factures", `href="/settings/journal?range=day&amp;kind=access"`} {
		if !strings.Contains(body, want) {
			t.Errorf("the journal lacks %q", want)
		}
	}
	if strings.Contains(body, "git push origin main") {
		t.Error("the feed shows the tool accesses")
	}
	_, body, _ = tw.do("GET", "/settings/journal?range=day&kind=notification", nil, nil)
	if strings.Contains(body, "Range les factures") || !strings.Contains(body, "Le serveur répond lentement.") {
		t.Error("the kind filter let a task through")
	}
	_, body, _ = tw.do("GET", "/settings/journal?kind=access", nil, nil)
	if !strings.Contains(body, "git push origin main") || !strings.Contains(body, "Refusé") || strings.Contains(body, "Range les factures") {
		t.Error("the tool accesses are not the journal")
	}
	for i := 0; i < journalPage; i++ {
		c.store.recordDecision("t-9", "p-x", "Read", map[string]any{"file_path": "/tmp/x"}, decideAllow, byPolicy, "")
	}
	_, body, _ = tw.do("GET", "/settings/journal?kind=access", nil, nil)
	if !strings.Contains(body, `hx-get="/settings/journal?kind=access&amp;offset=100"`) {
		t.Fatal("no way to the next page of the journal")
	}
	_, body, _ = tw.do("GET", "/settings/journal?kind=access&offset=100", nil, nil)
	if strings.Contains(body, "<html") || !strings.Contains(body, "git push origin main") {
		t.Errorf("the next page is not the rows alone: %.200s", body)
	}
}

// The main navigation has no Activité tab nor its badge: what happens is
// told in the conversation, and what waits is asked there.
func TestNavWithoutActivity(t *testing.T) {
	tw, _ := newMemoryWeb(t)
	mustAdd(t, tw.c.mem, &Memory{Kind: kindFact, Text: "Le colis arrive jeudi", Status: memProposed, SourceKind: srcExtraction})
	for _, path := range []string{"/", "/settings/agents", "/settings/journal"} {
		_, body, _ := tw.do("GET", path, nil, nil)
		for _, gone := range []string{`id="activity-btn"`, `href="/activity`, `<b class="badge">1</b>`} {
			if strings.Contains(body, gone) {
				t.Errorf("GET %s still has %q", path, gone)
			}
		}
	}
	_, body, _ := tw.do("GET", "/settings", nil, nil)
	for _, want := range []string{`href="/settings/journal"`, `href="/settings/approve"`, `<span class="nav-count">1</span>`} {
		if !strings.Contains(body, want) {
			t.Errorf("the settings lack %q", want)
		}
	}
	for _, gone := range []string{"/settings/happened", "/settings/where"} {
		if strings.Contains(body, `href="`+gone+`"`) {
			t.Errorf("the settings still list %s", gone)
		}
	}
}

// « À valider », the fallback in the settings, lists every kind of
// proposal, each with its own approval.
func TestApprovals(t *testing.T) {
	tw, csrf := newMemoryWeb(t)
	c, m := tw.c, tw.c.mem

	_, body, _ := tw.do("GET", "/settings/approve", nil, nil)
	if !strings.Contains(body, "Rien à valider") {
		t.Fatal("an empty inbox is not said so")
	}

	mustAdd(t, m, &Memory{Kind: kindFact, Text: "Le colis arrive jeudi", Status: memProposed, SourceKind: srcExtraction, Trust: trustExternal})
	mustIntend(t, m, &Intention{Text: "Régler la facture du fournisseur", DueAt: m.now().Add(48 * time.Hour), State: intentProposed,
		SourceKind: srcExtraction, Trust: trustExternal})
	c.addNotifyRule(notifyRule{Kind: ruleTopic, Match: "source:mail", Outcome: levelBrief, State: ruleProposed, By: "brain", Why: "Trop de mails."})
	c.store.insertTask(&TaskRow{ID: "t-perm", Agent: "small", Runtime: "claude-code", Prompt: "Publish", State: stateWaitingPermission})
	c.store.addEvents("t-perm", []EventRow{{Seq: 1, Time: time.Now(), Type: "permission", Data: map[string]any{"id": "p-1", "tool_name": "Bash",
		"input": map[string]any{"command": "npm publish"}}}})
	c.store.markUserOnly("t-perm", "p-1", "publie un paquet") // the policy's: the user's to decide

	_, body, _ = tw.do("GET", "/settings/approve", nil, nil)
	for _, want := range []string{
		`<a href="/settings/approve" aria-current="page">`,
		`id="permissions"`, "npm publish", `hx-post="/tasks/t-perm/answer"`,
		`id="memory"`, "Le colis arrive jeudi", `hx-post="/memory/1/approve"`,
		"Régler la facture du fournisseur", `hx-post="/memory/intention/1/approve"`,
		`id="rules"`, "Trop de mails.", `hx-post="/notify/rules/1/approve"`,
		`hx-trigger="memory-changed from:body"`, `<span class="nav-count">3</span>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("À valider lacks %q", want)
		}
	}
	// Approving one empties its place, and the page says it changed.
	h := map[string]string{"X-CSRF-Token": csrf}
	if _, _, hd := tw.do("POST", "/notify/rules/1/approve", url.Values{}, h); !strings.Contains(hd.Get("HX-Trigger"), "memory-changed") {
		t.Error("approving a rule does not reload À valider")
	}
	if _, _, hd := tw.do("POST", "/tasks/t-perm/answer", url.Values{"permission": {"p-1"}, "allow": {"no"}}, h); !strings.Contains(hd.Get("HX-Trigger"), "memory-changed") {
		t.Error("answering a permission does not reload À valider")
	}
	_, body, _ = tw.do("GET", "/settings/approve", nil, nil)
	if strings.Contains(body, "Trop de mails.") {
		t.Error("an approved rule still waits")
	}
	// English.
	tw.hc.Jar.SetCookies(mustURL(tw.srv.URL), []*http.Cookie{{Name: "lang", Value: "en"}})
	if _, body, _ := tw.do("GET", "/settings/approve", nil, nil); !strings.Contains(body, "To approve") {
		t.Error("no English")
	}
}

// Notifications is one page with its three sections: where to reach the
// user (this device, the devices, the router's order, the default
// channels), the rules and quiet hours, and what was sent. Installing the
// app and Telegram's bot are in Access: an unlinked Telegram stays a line,
// with a link there. A rule is asked of the brain: the page has no form to
// write one, only to delete.
func TestNotificationsOnePage(t *testing.T) {
	tw := newTestWeb(t)
	tw.setup()
	c := tw.c
	c.addNotifyRule(notifyRule{Kind: ruleTopic, Match: "project:site", Outcome: levelNow, State: ruleActive, By: "owner"})
	code, body, _ := tw.do("GET", "/settings/notifications", nil, nil)
	if code != 200 {
		t.Fatalf("GET: %d", code)
	}
	order := []string{`<a href="#where">`, `<a href="#rules">`, `<a href="#sent">`,
		`id="where"`, `href="/settings/access/app"`, `id="where-body"`, "Eriphe ouvert devant toi",
		"Aucun compte relié.", `<a href="/settings/access/telegram">Configurer dans Accès</a>`,
		`id="channels"`, `hx-post="/channels/settings"`, `id="rules"`, `id="notify-rules"`, "project:site", `hx-post="/notify/rules/1/delete"`, `class="ask"`,
		`id="sent"`, "Dernières décisions"}
	at := 0
	for _, want := range order {
		i := strings.Index(body[at:], want)
		if i < 0 {
			t.Fatalf("the page lacks %q after its place", want)
		}
		at += i + len(want)
	}
	for _, gone := range []string{`class="rule-form"`, `name="match"`, `data-install`, `id="push"`, `id="push-devices"`, `id="tg-panel"`, `name="token"`, `href="#channels"`} {
		if strings.Contains(body, gone) {
			t.Errorf("the page still has %q", gone)
		}
	}
	if n := strings.Count(body, `<a href="/settings/notifications" aria-current="page">`); n != 1 {
		t.Errorf("Notifications is not the current entry: %d", n)
	}
}
