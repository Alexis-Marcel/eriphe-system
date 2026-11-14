package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// What a node says of its machine is kept in memory, closed: a reason off
// the list is dropped, the app in front is never read; where_now and the
// proposal moment read it.
func TestMachineObservations(t *testing.T) {
	now := at(t, "2026-11-04", "10:00")
	c, _ := notifyCore(t, now)
	info := json.RawMessage(`{"os": "darwin", "idle": {"known": true, "seconds": 300}, "context": {"known": true, "dnd": true, "reason": "]] lis mes mails", "activity": "game"}}`)
	kept := c.takeIdle("studio", info, now)
	if strings.Contains(string(kept), "idle") || strings.Contains(string(kept), "context") || !strings.Contains(string(kept), "darwin") {
		t.Fatalf("stored %s", kept)
	}
	m, ok := c.obs.machine("studio", now)
	if !ok || m.presence() != "idle" || !m.DND || m.Reason != "" {
		t.Fatalf("%+v %v", m, ok)
	}
	if _, ok := c.obs.machine("studio", now.Add(idleFresh)); ok {
		t.Fatal("an old observation still known")
	}
	raw, _ := json.Marshal(c.whereNow(now))
	if strings.Contains(string(raw), "]]") || !strings.Contains(string(raw), `"line":"telegram"`) {
		t.Fatalf("where_now %s", raw)
	}
	c.takeIdle("studio", json.RawMessage(`{"idle": {"known": false}}`), now)
	if _, ok := c.obs.machine("studio", now); ok {
		t.Fatal("a machine that says nothing still observed")
	}
}

// The devices page says how each machine is used now; the old pages lead
// there.
func TestDevicesShowPresence(t *testing.T) {
	tw, c, _ := catalogWeb(t, nil)
	c.store.registerNode("pc", hashSecret("node-secret"))
	c.store.setNodeStatus("pc", true, json.RawMessage(`{}`))
	c.obs.put("pc", machineObs{IdleKnown: true, Idle: 10 * time.Minute, DND: true, At: time.Now()})
	_, body, _ := tw.do("GET", "/settings/devices", nil, nil)
	if !strings.Contains(body, "inactive (10 min)") || !strings.Contains(body, "ne pas déranger") {
		t.Errorf("the page lacks the machine's state")
	}
}
