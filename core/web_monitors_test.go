package main

import (
	"net/url"
	"strings"
	"testing"
)

// Settings → Surveillance: the user tries a tool, adds a monitor (it looks
// at once, no card), sees it with the skill that reacts, pauses, resumes,
// answers a proposal of the brain's, and deletes it.
func TestMonitorsPage(t *testing.T) {
	c, mon, now := pollCore(t)
	mon.rules = []map[string]any{{"uid": "a", "name": "5xx", "state": "firing"}}
	tw := newTestWebFor(t, c)
	csrf := tw.setup()

	code, body, _ := tw.do("GET", "/settings/monitors", nil, nil)
	if code != 200 || !strings.Contains(body, "monitor.alerts") || strings.Contains(body, "monitor.rollback") {
		t.Fatalf("list %d: the tool picker lacks the read or offers the write:\n%s", code, body)
	}
	form := url.Values{"name": {"getout-prod"}, "tool": {"monitor.alerts"}, "every": {"2m"}, "cond": {"changed"},
		"path": {"data.rules[state=firing].name"}}
	if _, body, _ := tw.post("/settings/monitors/try", form, csrf); !strings.Contains(body, `[&#34;5xx&#34;]`) {
		t.Fatalf("try: %s", body)
	}
	bad := url.Values{"tool": {"monitor.rollback"}}
	if _, body, _ := tw.post("/settings/monitors/try", bad, csrf); !strings.Contains(body, "read-only") {
		t.Fatalf("try a write: %s", body)
	}
	if _, _, h := tw.post("/settings/monitors", form, csrf); h.Get("HX-Redirect") != "/settings/monitors/getout-prod" {
		t.Fatalf("save: %v", h)
	}
	m, err := c.monitorNamed("getout-prod")
	if err != nil || m.State != monitorOn || m.Spec.Every != 2 {
		t.Fatalf("saved %+v, %v", m, err)
	}
	addTriggered(t, c, "---\nname: prod\ndescription: la prod\nwhen: monitor.changed key=getout-prod\n---\nEnquête.\n")
	lookAt(c, *now)
	code, body, _ = tw.do("GET", "/settings/monitors/getout-prod", nil, nil)
	for _, want := range []string{"getout-prod", "toutes les 2 min", "data.rules[state=firing].name", "prod", "Mettre en pause"} {
		if code != 200 || !strings.Contains(body, want) {
			t.Errorf("page %d lacks %q", code, want)
		}
	}
	tw.post("/settings/monitors/getout-prod/pause", nil, csrf)
	if m, _ := c.monitorNamed("getout-prod"); m.State != monitorPaused {
		t.Fatalf("pause: %s", m.State)
	}
	calls := mon.calls
	lookAt(c, now.Add(10*60e9))
	if mon.calls != calls {
		t.Fatal("a paused monitor looked")
	}
	tw.post("/settings/monitors/getout-prod/resume", nil, csrf)
	if m, _ := c.monitorNamed("getout-prod"); m.State != monitorOn || m.Look.Known {
		t.Fatalf("resume: %+v", m)
	}

	// A proposal of the brain's, answered here.
	c.writeMonitor("etat", monitorSpec{Server: "monitor", Tool: "status", Args: map[string]any{"service": "web"}}, nil)
	if _, body, _ := tw.do("GET", "/settings/monitors/etat", nil, nil); !strings.Contains(body, "Approuver") {
		t.Fatal("no approval on a proposed monitor's page")
	}
	tw.post("/settings/monitors/etat/approve", nil, csrf)
	if m, _ := c.monitorNamed("etat"); m.State != monitorOn {
		t.Fatalf("approve: %s", m.State)
	}

	// An address, made here: its URL and secret shown once, a listening monitor.
	code, body, _ = tw.do("POST", "/settings/monitors/address", url.Values{"csrf": {csrf}, "name": {"grafana"}, "signed": {"on"},
		"header": {"X-Grafana-Alerting-Signature"}}, nil)
	if code != 200 || !strings.Contains(body, "/hooks/in/") || !strings.Contains(body, "HMAC-SHA256") {
		t.Fatalf("address %d:\n%s", code, body)
	}
	if m, err := c.monitorNamed("grafana"); err != nil || m.Spec.Way != wayListen {
		t.Fatalf("address monitor %+v, %v", m, err)
	}
	if _, body, _ := tw.do("GET", "/settings/monitors/grafana", nil, nil); !strings.Contains(body, "monitor.received key=grafana") || strings.Contains(body, `hx-post="/settings/monitors/try"`) {
		t.Fatal("an address's page: its when, and no poll form")
	}
	if _, body, _ := tw.do("GET", "/settings/skills", nil, nil); strings.Contains(body, "/settings/alerts") {
		t.Fatal("the skills page still makes addresses")
	}
	if _, _, h := tw.post("/settings/monitors/getout-prod/delete", nil, csrf); h.Get("HX-Redirect") != "/settings/monitors" {
		t.Fatalf("delete: %v", h)
	}
	if _, err := c.monitorNamed("getout-prod"); err == nil {
		t.Fatal("still there")
	}
}

// A server that publishes events is offered to follow, once: followed, it
// is no longer offered, and a second monitor cannot follow it.
func TestMonitorFollowPage(t *testing.T) {
	c, _, _ := pollCore(t)
	c.events.cursorMu.Lock()
	c.events.servers = map[string]bool{"monitor": true}
	c.events.cursorMu.Unlock()
	tw := newTestWebFor(t, c)
	csrf := tw.setup()
	if _, body, _ := tw.do("GET", "/settings/monitors", nil, nil); !strings.Contains(body, `hx-post="/settings/monitors/follow"`) {
		t.Fatal("no stream to follow offered")
	}
	if _, _, h := tw.post("/settings/monitors/follow", url.Values{"name": {"suivi"}, "server": {"monitor"}}, csrf); h.Get("HX-Redirect") != "/settings/monitors/suivi" {
		t.Fatalf("follow: %v", h)
	}
	if m, err := c.monitorNamed("suivi"); err != nil || m.Spec.Way != wayFollow || m.State != monitorOn {
		t.Fatalf("followed %+v, %v", m, err)
	}
	if _, body, _ := tw.do("GET", "/settings/monitors", nil, nil); strings.Contains(body, `hx-post="/settings/monitors/follow"`) {
		t.Fatal("a followed stream is still offered")
	}
	if err := c.saveMonitor("autre", monitorSpec{Way: wayFollow, Server: "monitor"}); err == nil {
		t.Fatal("a second monitor followed the same server")
	}
	if _, err := c.writeMonitor("mails", monitorSpec{Way: wayFollow, Server: "monitor"}, nil); err == nil {
		t.Fatal("the brain proposed a second follower")
	}
	if _, body, _ := tw.do("GET", "/settings/monitors/suivi", nil, nil); !strings.Contains(body, "suit les événements") || strings.Contains(body, `hx-post="/settings/monitors/try"`) {
		t.Fatalf("a follower's page: its words, and no poll form:\n%s", body)
	}
}

// The page's text fields are typed, so the stylesheet dresses them as the
// others.
func TestMonitorsFieldsTyped(t *testing.T) {
	raw, err := webFS.ReadFile("web/templates/monitors.html")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "<input name=") {
		t.Fatal("an input without a type")
	}
}
