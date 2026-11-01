package main

import (
	"encoding/json"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"
)

// routeCore is notifyCore with two devices: the Android phone of
// notifyCore (/push/1) and a Mac (/push/mac) whose node says it is in use.
func routeCore(t *testing.T, now time.Time) (*core, *browser, string, string) {
	t.Helper()
	c, b := notifyCore(t, now)
	mac := b.sub("/push/mac")
	mac.Label = "Mac · Chrome"
	macID, err := c.push.subscribe(mac)
	if err != nil {
		t.Fatal(err)
	}
	c.setDevicePref(macID, func(p *devicePref) { p.Node = "studio" })
	c.putSignals([]ctxSignal{presenceSignal(srcMachine+"studio", nodeIdle{Known: true, Seconds: 5}, now)})
	return c, b, macID, pushID(b.sub("/push/1").Endpoint)
}

func (b *browser) sentTo() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return slices.Clone(b.paths)
}

// In route mode, a "now" goes to the Mac in use only; not seen in 10
// minutes, to the phone; then nowhere more, since nothing else is set up.
func TestRouteOneDeviceThenNext(t *testing.T) {
	now := at(t, "2026-11-04", "10:00")
	c, b, _, _ := routeCore(t, now)
	c.store.set(routeModeKey, routeOn)
	ownerSays(c, "ok")
	if _, err := c.brainNotify(notifyIn{Text: "La PR 42 est fusionnée.", Level: levelNow, Why: "Demandé.", Ref: "project:site"}); err != nil {
		t.Fatal(err)
	}
	c.push.pending.Wait()
	if got := b.sentTo(); !slices.Equal(got, []string{"/push/mac"}) {
		t.Fatalf("sent to %v, want the Mac only", got)
	}
	// Five minutes later: too early.
	clock := now.Add(5 * time.Minute)
	c.notifyClock = func() time.Time { return clock }
	c.escalateDue()
	c.push.pending.Wait()
	if n := len(b.sentTo()); n != 1 {
		t.Fatalf("moved on after 5 minutes: %d", n)
	}
	// Eleven minutes: the Mac has gone idle, the phone.
	clock = now.Add(11 * time.Minute)
	c.putSignals([]ctxSignal{presenceSignal(srcMachine+"studio", nodeIdle{Known: true, Seconds: 600}, clock)})
	c.escalateDue()
	c.push.pending.Wait()
	if got := b.sentTo(); !slices.Equal(got, []string{"/push/mac", "/push/1"}) {
		t.Fatalf("sent to %v, want the phone next", got)
	}
	clock = now.Add(30 * time.Minute)
	c.escalateDue()
	c.push.pending.Wait()
	if n := len(b.sentTo()); n != 2 {
		t.Fatalf("%d pushes: nothing else is set up", n)
	}
	e := c.recentRoutes(1)[0]
	if e.Mode != routeOn || len(e.Sent) != 2 || !e.NextAt.IsZero() || e.Step != 1 {
		t.Fatalf("log %+v", e)
	}
}

// An answer stops the escalation; a brief or a task done is never moved on.
func TestRouteSeenStops(t *testing.T) {
	now := at(t, "2026-11-04", "10:00")
	c, b, _, _ := routeCore(t, now)
	c.store.set(routeModeKey, routeOn)
	ownerSays(c, "ok")
	out, _ := c.brainNotify(notifyIn{Text: "Facture reçue.", Level: levelNow, Why: "Nouvelle facture.", Ref: "source:invoices"})
	c.push.pending.Wait()
	if _, err := c.notificationFeedback(out["id"].(int64), "up"); err != nil {
		t.Fatal(err)
	}
	c.notifyClock = func() time.Time { return now.Add(20 * time.Minute) }
	c.escalateDue()
	c.push.pending.Wait()
	if n := len(b.sentTo()); n != 1 {
		t.Fatalf("%d pushes after it was answered", n)
	}
	if e := c.recentRoutes(1)[0]; e.AckedHow != ackFeedback {
		t.Fatalf("log %+v", e)
	}
	// A task done: one device, nothing next.
	c.tell(pushNote{Body: map[string]string{"fr": "Tâche finie"}, URL: "/"})
	c.push.pending.Wait()
	if e := c.recentRoutes(1)[0]; !e.NextAt.IsZero() || len(e.Sent) != 1 {
		t.Fatalf("a task done: %+v", e)
	}
}

// Observe mode, the default: everything goes as before, and the plan is written down.
func TestRouteObserve(t *testing.T) {
	now := at(t, "2026-11-04", "10:00")
	c, b, _, _ := routeCore(t, now)
	ownerSays(c, "ok")
	c.brainNotify(notifyIn{Text: "Le build est vert.", Level: levelNow, Why: "Le build.", Ref: "project:site"})
	c.push.pending.Wait()
	if n := len(b.sentTo()); n != 2 {
		t.Fatalf("observe mode sent %d pushes, want every device", n)
	}
	e := c.recentRoutes(1)[0]
	if e.Mode != routeObserve || len(e.Planned.First) != 1 || e.Planned.First[0].Step != stepComputer || len(e.Sent) != 2 || !e.NextAt.IsZero() {
		t.Fatalf("log %+v", e)
	}
	if got := describePlan("fr", e.Planned); !strings.Contains(got, "ordinateur · Mac · Chrome") || !strings.Contains(got, "(10 min) téléphone") {
		t.Fatalf("described %q", got)
	}
}

// Eriphe in front on the Mac: in that page, no push anywhere; shown is seen.
func TestRouteInApp(t *testing.T) {
	now := at(t, "2026-11-04", "10:00")
	c, b, macID, _ := routeCore(t, now)
	c.store.set(routeModeKey, routeOn)
	c.push.seenOn("tab-1", true, tabSeen{sub: macID, kind: kindComputer})
	ownerSays(c, "ok")
	c.brainNotify(notifyIn{Text: "Le déploiement est fini.", Level: levelNow, Why: "Le déploiement.", Ref: "project:site"})
	c.push.pending.Wait()
	if n := len(b.sentTo()); n != 0 {
		t.Fatalf("%d pushes with Eriphe in front", n)
	}
	notes := c.showInApp("tab-1")
	if len(notes) != 1 || notes[0].Body["fr"] != "Le déploiement est fini." {
		t.Fatalf("in the page: %+v", notes)
	}
	if again := c.showInApp("tab-1"); len(again) != 0 {
		t.Fatal("shown twice")
	}
	c.notifyClock = func() time.Time { return now.Add(20 * time.Minute) }
	c.escalateDue()
	c.push.pending.Wait()
	if n := len(b.sentTo()); n != 0 {
		t.Fatalf("moved on after it was shown: %d", n)
	}
}

// A rule's line added goes with the first; a forbidden one never.
func TestRouteRuleLines(t *testing.T) {
	now := at(t, "2026-11-04", "10:00")
	c, b, _, _ := routeCore(t, now)
	c.store.set(routeModeKey, routeOn)
	if _, err := c.addNotifyRule(notifyRule{Kind: ruleTopic, Match: "project:getout", Outcome: levelNow, Also: "phone", State: ruleActive, By: "owner"}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.addNotifyRule(notifyRule{Kind: ruleTopic, Match: "project:site", Outcome: levelNow, Never: "computer", State: ruleActive, By: "owner"}); err != nil {
		t.Fatal(err)
	}
	ownerSays(c, "ok")
	c.brainNotify(notifyIn{Text: "Getout est en panne.", Level: levelNow, Why: "Alerte.", Ref: "project:getout"})
	c.push.pending.Wait()
	if got := b.sentTo(); len(got) != 2 {
		t.Fatalf("also the phone: %v", got)
	}
	c.brainNotify(notifyIn{Text: "Le site est en ligne.", Level: levelNow, Why: "Déploiement.", Ref: "project:site"})
	c.push.pending.Wait()
	if got := b.sentTo(); len(got) != 3 || got[2] != "/push/1" {
		t.Fatalf("never the computer: %v", got)
	}
	if _, err := c.addNotifyRule(notifyRule{Kind: ruleTopic, Match: "x", Outcome: levelNow, Also: "mac", State: ruleActive, By: "owner"}); err == nil {
		t.Fatal("an unknown line was accepted")
	}
}

// A node's idle time is kept in memory, never in its stored info.
func TestTakeIdle(t *testing.T) {
	c, _ := pushCore(t)
	now := time.Now()
	info := c.takeIdle("studio", json.RawMessage(`{"cpus":8,"idle":{"known":true,"seconds":42,"locked":true}}`), now)
	if strings.Contains(string(info), "idle") || !strings.Contains(string(info), `"cpus":8`) {
		t.Fatalf("stored %s", info)
	}
	if idle, ok := c.idleOf("studio", now.Add(time.Minute)); !ok || idle.Seconds != 42 || !idle.Locked {
		t.Fatalf("%+v %v", idle, ok)
	}
	if _, ok := c.idleOf("studio", now.Add(idleFresh+time.Second)); ok {
		t.Fatal("an old idle time is still known")
	}
	c.takeIdle("studio", json.RawMessage(`{"idle":{"known":false}}`), now)
	if _, ok := c.idleOf("studio", now); ok {
		t.Fatal("unknown read as known")
	}
}

func TestDeviceKind(t *testing.T) {
	for _, x := range []struct{ label, ua, want string }{
		{"iPhone · Safari · app", "", kindPhone}, {"iPad · Safari", "", kindPhone}, {"Android · Chrome", "", kindPhone},
		{"Mac · Chrome", "", kindComputer}, {"Windows · Edge", kindPhone, kindComputer},
		{"Firefox", kindPhone, kindPhone}, {"Firefox", kindComputer, kindComputer}, {"Appareil", "", kindComputer},
	} {
		if got := deviceKind(x.label, x.ua); got != x.want {
			t.Errorf("%s/%s: %s, want %s", x.label, x.ua, got, x.want)
		}
	}
	if uaKind("Mozilla/5.0 (Linux; Android 14) Mobile Safari") != kindPhone || uaKind("Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7)") != kindComputer {
		t.Fatal("uaKind")
	}
	if got := suggestNode("Mac · Chrome", []NodeRow{{Name: "studio", OS: "darwin"}, {Name: "nas", OS: "linux"}}); got != "studio" {
		t.Fatalf("suggested %q", got)
	}
	if got := suggestNode("Mac · Chrome", []NodeRow{{Name: "a", OS: "darwin"}, {Name: "b", OS: "darwin"}}); got != "" {
		t.Fatalf("two Macs, suggested %q", got)
	}
}

// The page: shown, switched to route, a line moved and turned off; a
// presence answers with what waits for the tab.
func TestWherePage(t *testing.T) {
	tw := newTestWeb(t)
	csrf := tw.setup()
	if code, body, _ := tw.do("GET", "/settings/notifications", nil, nil); code != 200 || !strings.Contains(body, "Où te prévenir") ||
		!strings.Contains(body, "Eriphe ouvert devant toi") || !strings.Contains(body, "relie Home Assistant") {
		t.Fatalf("page: %d", code)
	}
	if code, body, _ := tw.post("/where/mode", url.Values{"mode": {"route"}}, csrf); code != 200 || tw.c.routeSettings().Mode != routeOn ||
		!strings.Contains(body, "un seul appareil") {
		t.Fatalf("mode: %d", code)
	}
	if code, _, _ := tw.post("/where/line", url.Values{"step": {"telegram"}, "on": {""}}, csrf); code != 200 ||
		!slices.Contains(tw.c.routeSettings().Off, stepTelegram) {
		t.Fatalf("off: %d %v", code, tw.c.routeSettings().Off)
	}
	if code, _, _ := tw.post("/where/line", url.Values{"step": {"voice"}, "action": {"up"}}, csrf); code != 200 ||
		!slices.Equal(tw.c.routeSettings().Order[:2], []string{stepVoice, stepInApp}) {
		t.Fatalf("up: %d %v", code, tw.c.routeSettings().Order)
	}
	if code, _, _ := tw.post("/where/delay", url.Values{"delay": {"5"}}, csrf); code != 200 || tw.c.routeSettings().Delay != 5*time.Minute {
		t.Fatalf("delay: %d", code)
	}
	if code, _, h := tw.post("/where/delay", url.Values{"delay": {"500"}}, csrf); code != 200 || !strings.Contains(h.Get("HX-Trigger"), "error") {
		t.Fatalf("a delay of 500: %d", code)
	}
	if code, _, _ := tw.post("/where/line", url.Values{"step": {"mac"}, "action": {"on"}}, csrf); code != 400 {
		t.Fatalf("an unknown line: %d", code)
	}
	// A tab in front gets what was routed to it.
	tw.post("/presence", url.Values{"tab": {"t1"}, "front": {"1"}}, csrf)
	tw.c.tell(pushNote{Body: map[string]string{"fr": "Bonjour", "en": "Hello"}, URL: "/"})
	code, body, _ := tw.post("/presence", url.Values{"tab": {"t1"}, "front": {"1"}, "sub": {"not-an-id"}}, csrf)
	if code != 200 || !strings.Contains(body, `"Bonjour"`) {
		t.Fatalf("presence: %d %s", code, body)
	}
	if code, body, _ := tw.do("GET", "/settings/notifications", nil, nil); code != 200 || !strings.Contains(body, "dans la page") || !strings.Contains(body, "vue dans la page") {
		t.Fatalf("log: %d", code)
	}
}
