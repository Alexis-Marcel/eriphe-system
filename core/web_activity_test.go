package main

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// The pages that moved lead to their new place, with what the address
// carried: links in sent notifications and in the brain's messages keep
// working.
func TestActivityRedirects(t *testing.T) {
	tw := newTestWeb(t)
	tw.setup()
	for from, to := range map[string]string{
		"/settings/happened":                      "/activity",
		"/settings/happened?range=week&kind=task": "/activity?range=week&kind=task",
		"/settings/journal":                       "/activity?kind=access",
		"/settings/channels":                      "/settings/notifications#channels",
		"/settings/where":                         "/settings/notifications#where",
		"/memory?view=inbox":                      "/activity/approve",
	} {
		code, _, h := tw.do("GET", from, nil, nil)
		if code != http.StatusSeeOther || h.Get("Location") != to {
			t.Errorf("GET %s: %d to %q, want %q", from, code, h.Get("Location"), to)
		}
	}
	// The addresses that stayed still answer.
	for _, path := range []string{"/settings/notifications?n=1", "/settings/watches", "/settings/devices"} {
		if code, _, _ := tw.do("GET", path, nil, nil); code != 200 {
			t.Errorf("GET %s: %d", path, code)
		}
	}
}

// Activité is in the main navigation, beside Tâches, not in the settings:
// its feed filters by kind and period, and the tool accesses are the
// decisions' journal, paged.
func TestActivityFeed(t *testing.T) {
	tw := newTestWeb(t)
	tw.setup()
	c := tw.c
	c.notify(&notification{Text: "Le serveur répond lentement.", Why: "Une alerte.", Source: notifyFromBrain}, notifyItem{Level: levelBrief})
	c.store.insertTask(&TaskRow{ID: "t-9", Kind: "worker", Runtime: "claude-code", State: stateRunning, Prompt: "Range les factures"})
	c.store.recordDecision("t-9", "p-0", "Bash", map[string]any{"command": "git push origin main"}, decideDeny, byPolicy, "no pushing to main")

	code, body, _ := tw.do("GET", "/activity?range=day", nil, nil)
	if code != 200 {
		t.Fatalf("GET /activity: %d", code)
	}
	for _, want := range []string{`<html lang="fr" class="shell">`, `id="activity-btn"`, `aria-label="Activité" title="Activité" aria-current="page"`,
		`<a href="/activity" aria-current="page">`, "Le serveur répond lentement.", "Range les factures", `href="/activity?range=day&amp;kind=access"`} {
		if !strings.Contains(body, want) {
			t.Errorf("the feed lacks %q", want)
		}
	}
	for _, gone := range []string{`class="settings-nav"`, "git push origin main"} {
		if strings.Contains(body, gone) {
			t.Errorf("the feed has %q", gone)
		}
	}
	_, body, _ = tw.do("GET", "/activity?range=day&kind=notification", nil, nil)
	if strings.Contains(body, "Range les factures") || !strings.Contains(body, "Le serveur répond lentement.") {
		t.Error("the kind filter let a task through")
	}
	_, body, _ = tw.do("GET", "/activity?kind=access", nil, nil)
	if !strings.Contains(body, "git push origin main") || !strings.Contains(body, "Refusé") || strings.Contains(body, "Range les factures") {
		t.Error("the tool accesses are not the journal")
	}
	for i := 0; i < journalPage; i++ {
		c.store.recordDecision("t-9", "p-x", "Read", map[string]any{"file_path": "/tmp/x"}, decideAllow, byPolicy, "")
	}
	_, body, _ = tw.do("GET", "/activity?kind=access", nil, nil)
	if !strings.Contains(body, `hx-get="/activity?kind=access&amp;offset=100"`) {
		t.Fatal("no way to the next page of the journal")
	}
	_, body, _ = tw.do("GET", "/activity?kind=access&offset=100", nil, nil)
	if strings.Contains(body, "<html") || !strings.Contains(body, "git push origin main") {
		t.Errorf("the next page is not the rows alone: %.200s", body)
	}
	// The conversation's top bar has it too; the settings no longer list it.
	if _, body, _ := tw.do("GET", "/", nil, nil); !strings.Contains(body, `id="activity-btn" href="/activity"`) {
		t.Error("the conversation has no way to Activité")
	}
	_, body, _ = tw.do("GET", "/settings", nil, nil)
	for _, gone := range []string{"/settings/happened", "/settings/journal", "/settings/channels", "/settings/where"} {
		if strings.Contains(body, `href="`+gone+`"`) {
			t.Errorf("the settings still list %s", gone)
		}
	}
}

// « À valider » lists every kind of proposal, each with its own approval;
// the Activité button counts them and leads there.
func TestApprovals(t *testing.T) {
	tw, csrf := newMemoryWeb(t)
	c, m := tw.c, tw.c.mem

	_, body, _ := tw.do("GET", "/activity/approve", nil, nil)
	if !strings.Contains(body, "Rien à valider") || strings.Contains(body, `<b class="badge">`) {
		t.Fatal("an empty inbox is not said so")
	}

	mustAdd(t, m, &Memory{Kind: kindFact, Text: "Le colis arrive jeudi", Status: memProposed, SourceKind: srcExtraction, Trust: trustExternal})
	mustIntend(t, m, &Intention{Text: "Régler la facture du fournisseur", DueAt: m.now().Add(48 * time.Hour), State: intentProposed,
		SourceKind: srcExtraction, Trust: trustExternal})
	c.addNotifyRule(notifyRule{Kind: ruleTopic, Match: "source:mail", Outcome: levelBrief, State: ruleProposed, By: "brain", Why: "Trop de mails."})
	w, _ := c.addWatch(&watch{Name: "Revue du dimanche", Source: sourceSchedule, Spec: watchSpec{Schedule: "sunday 18:00"},
		Prompt: "Fais la revue", State: watchProposed, By: "brain"})
	c.store.insertTask(&TaskRow{ID: "t-perm", Agent: "small", Runtime: "claude-code", Prompt: "Publish", State: stateWaitingPermission})
	c.store.addEvents("t-perm", []EventRow{{Seq: 1, Time: time.Now(), Type: "permission", Data: map[string]any{"id": "p-1", "tool_name": "Bash",
		"input": map[string]any{"command": "npm publish"}}}})

	_, body, _ = tw.do("GET", "/activity/approve", nil, nil)
	for _, want := range []string{
		`id="permissions"`, "npm publish", `hx-post="/tasks/t-perm/answer"`,
		`id="memory"`, "Le colis arrive jeudi", `hx-post="/memory/1/approve"`,
		"Régler la facture du fournisseur", `hx-post="/memory/intention/1/approve"`,
		`id="rules"`, "Trop de mails.", `hx-post="/notify/rules/1/approve"`,
		`id="watches"`, "Revue du dimanche", `hx-post="/watches/` + idText(w.ID) + `/approve"`,
		`hx-trigger="memory-changed from:body"`, `<b class="tab-count">5</b>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("À valider lacks %q", want)
		}
	}
	// The button counts the proposals (the permissions are on Tâches) and leads to them.
	if _, body, _ := tw.do("GET", "/settings/agents", nil, nil); !strings.Contains(body, `id="activity-btn" href="/activity/approve"`) ||
		!strings.Contains(body, `<b class="badge">4</b>`) {
		t.Error("the Activité button does not count the proposals")
	}
	// Approving one empties its place, and the page says it changed.
	h := map[string]string{"X-CSRF-Token": csrf}
	if _, _, hd := tw.do("POST", "/notify/rules/1/approve", url.Values{}, h); !strings.Contains(hd.Get("HX-Trigger"), "memory-changed") {
		t.Error("approving a rule does not reload À valider")
	}
	if _, _, hd := tw.do("POST", "/tasks/t-perm/answer", url.Values{"permission": {"p-1"}, "allow": {"no"}}, h); !strings.Contains(hd.Get("HX-Trigger"), "memory-changed") {
		t.Error("answering a permission does not reload À valider")
	}
	_, body, _ = tw.do("GET", "/activity/approve", nil, nil)
	if strings.Contains(body, "Trop de mails.") {
		t.Error("an approved rule still waits")
	}
	// English.
	tw.hc.Jar.SetCookies(mustURL(tw.srv.URL), []*http.Cookie{{Name: "lang", Value: "en"}})
	if _, body, _ := tw.do("GET", "/activity/approve", nil, nil); !strings.Contains(body, "To approve") || !strings.Contains(body, "Routines and watches") {
		t.Error("no English")
	}
}

// Notifications is one page with its four sections: where to reach the
// user (this device, the devices, the router's order), the channels, the
// rules and quiet hours, and what was sent. A rule is asked of the brain:
// the page has no form to write one, only to delete.
func TestNotificationsOnePage(t *testing.T) {
	tw := newTestWeb(t)
	tw.setup()
	c := tw.c
	c.addNotifyRule(notifyRule{Kind: ruleTopic, Match: "project:site", Outcome: levelNow, State: ruleActive, By: "owner"})
	code, body, _ := tw.do("GET", "/settings/notifications", nil, nil)
	if code != 200 {
		t.Fatalf("GET: %d", code)
	}
	order := []string{`<a href="#where">`, `<a href="#channels">`, `<a href="#rules">`, `<a href="#sent">`,
		`id="where"`, `id="push"`, `id="push-devices"`, `id="where-body"`, "Eriphe ouvert devant toi",
		`id="channels"`, `id="telegram"`, `id="rules"`, `id="notify-rules"`, "project:site", `hx-post="/notify/rules/1/delete"`, `class="ask"`,
		`id="sent"`, "Dernières décisions"}
	at := 0
	for _, want := range order {
		i := strings.Index(body[at:], want)
		if i < 0 {
			t.Fatalf("the page lacks %q after its place", want)
		}
		at += i + len(want)
	}
	for _, gone := range []string{`class="rule-form"`, `name="match"`} {
		if strings.Contains(body, gone) {
			t.Errorf("the page still has %q", gone)
		}
	}
	if n := strings.Count(body, `<a href="/settings/notifications" aria-current="page">`); n != 1 {
		t.Errorf("Notifications is not the current entry: %d", n)
	}
}
