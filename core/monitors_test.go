package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestMonitorPath(t *testing.T) {
	var data any
	json.Unmarshal([]byte(`{"data": {"state": "up", "rules": [
		{"uid": "b", "name": "5xx", "state": "firing"},
		{"uid": "a", "name": "latency", "state": "normal"},
		{"uid": "c", "name": "down", "state": "firing"}]}}`), &data)
	for _, tc := range []struct{ path, want string }{
		{"", ""},
		{"data.state", `up`},
		{"$.data.state", `up`},
		{"data.rules.1.name", `latency`},
		{"data.rules.*.uid", `["a","b","c"]`},
		{"data.rules[state=firing].name", `["5xx","down"]`},
		{"data.rules.*[state=normal].uid", `["a"]`},
		{"data.rules[state=pending].name", `[]`},
	} {
		got, err := extract(data, tc.path)
		if err != nil {
			t.Errorf("%q: %v", tc.path, err)
			continue
		}
		if tc.path == "" {
			if _, ok := got.(map[string]any); !ok {
				t.Errorf("the whole result: %v", got)
			}
			continue
		}
		if s := valueText(got); s != tc.want {
			t.Errorf("%q = %s, want %s", tc.path, s, tc.want)
		}
	}
	for _, bad := range []string{"data.missing", "data.rules.9"} {
		if _, err := extract(data, bad); err == nil {
			t.Errorf("%q found something", bad)
		}
	}
	for _, bad := range []string{"data..state", "data.rules[state", "data.rules[=x]"} {
		if _, err := parsePath(bad); err == nil {
			t.Errorf("%q parsed", bad)
		}
	}
}

// approvedMonitor is a monitor the brain proposed and the user approved.
func approvedMonitor(t *testing.T, c *core, name string, s monitorSpec) *monitor {
	t.Helper()
	if _, err := c.writeMonitor(name, s, nil); err != nil {
		t.Fatal(err)
	}
	m, err := c.monitorNamed(name)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.decideMonitor(m.ID, true); err != nil {
		t.Fatal(err)
	}
	m, _ = c.monitorNamed(name)
	return m
}

func lookAt(c *core, at time.Time) { c.tickMonitors(context.Background(), at) }

// changed: the first look is the baseline; a change is told once a second
// look half a minute later saw it too; a flap tells nothing; only the path
// counts, not the rest of the result.
func TestMonitorChanged(t *testing.T) {
	c, mon, now := pollCore(t)
	mon.rules = []map[string]any{{"uid": "a", "name": "5xx", "state": "normal"}, {"uid": "b", "name": "down", "state": "normal"}}
	approvedMonitor(t, c, "getout-prod", monitorSpec{Server: "monitor", Tool: "alerts", Every: 2, Path: "data.rules[state=firing].name"})
	lookAt(c, *now)
	lookAt(c, now.Add(2*time.Minute)) // the result's time moved, not the path
	if es := eventsOf(t, c, ""); len(es) != 0 {
		t.Fatalf("the baseline told: %+v", es)
	}
	mon.rules[0]["state"] = "firing"
	lookAt(c, now.Add(4*time.Minute))
	if es := eventsOf(t, c, evMonitorChanged); len(es) != 0 {
		t.Fatal("told before its second look")
	}
	lookAt(c, now.Add(4*time.Minute+monitorConfirm))
	es := eventsOf(t, c, evMonitorChanged)
	if len(es) != 1 || es[0].Key != "getout-prod" || !es[0].External {
		t.Fatalf("events %+v", es)
	}
	if v := valueText(es[0].Payload["value"]); v != `["5xx"]` || es[0].Payload["was"] != "[]" {
		t.Fatalf("payload %v", es[0].Payload)
	}
	// A flap: it fires, then is back before the second look.
	mon.rules[1]["state"] = "firing"
	lookAt(c, now.Add(10*time.Minute))
	mon.rules[1]["state"] = "normal"
	lookAt(c, now.Add(10*time.Minute+monitorConfirm))
	lookAt(c, now.Add(20*time.Minute))
	if es := eventsOf(t, c, evMonitorChanged); len(es) != 1 {
		t.Fatalf("a flap told: %+v", es)
	}
	// Not its time yet: no call.
	calls := mon.calls
	lookAt(c, now.Add(20*time.Minute+30*time.Second))
	if mon.calls != calls {
		t.Fatal("looked before its time")
	}
}

// new: one event per item not seen, after the baseline.
func TestMonitorNew(t *testing.T) {
	c, mon, now := pollCore(t)
	mon.rules = []map[string]any{{"uid": "a", "name": "5xx", "state": "firing"}}
	approvedMonitor(t, c, "alertes", monitorSpec{Server: "monitor", Tool: "alerts", Every: 1, Cond: condNew, Path: "data.rules", ID: "uid"})
	lookAt(c, *now)
	if es := eventsOf(t, c, evMonitorNew); len(es) != 0 {
		t.Fatalf("the baseline told: %+v", es)
	}
	mon.rules = append(mon.rules, map[string]any{"uid": "b", "name": "down", "state": "firing"},
		map[string]any{"uid": "c", "name": "certs", "state": "firing"})
	lookAt(c, now.Add(time.Minute))
	lookAt(c, now.Add(2*time.Minute))
	es := eventsOf(t, c, evMonitorNew)
	if len(es) != 2 || es[0].Payload["id"] != "b" || es[1].Payload["id"] != "c" {
		t.Fatalf("events %+v", es)
	}
	if item, _ := es[0].Payload["item"].(map[string]any); item["name"] != "down" {
		t.Fatalf("item %v", es[0].Payload["item"])
	}
}

// Three failed looks in a row tell once; the next good one tells the
// recovery; a failure is no change.
func TestMonitorFailures(t *testing.T) {
	c, mon, now := pollCore(t)
	approvedMonitor(t, c, "etat", monitorSpec{Server: "monitor", Tool: "status", Args: map[string]any{"service": "web"}, Every: 1, Path: "data.state"})
	lookAt(c, *now)
	mon.failing = true
	for i := 1; i <= 5; i++ {
		lookAt(c, now.Add(time.Duration(i)*time.Minute))
	}
	if es := eventsOf(t, c, evMonitorFailed); len(es) != 1 || !strings.Contains(valueText(es[0].Payload["error"]), "unreachable") {
		t.Fatalf("failed %+v", es)
	}
	mon.failing = false
	lookAt(c, now.Add(6*time.Minute))
	lookAt(c, now.Add(7*time.Minute))
	if es := eventsOf(t, c, evMonitorRecovered); len(es) != 1 {
		t.Fatalf("recovered %+v", es)
	}
	if es := eventsOf(t, c, evMonitorChanged); len(es) != 0 {
		t.Fatalf("a failure was a change: %+v", es)
	}
}

// A monitor only calls a free read of the brain's: a tool that acts, or one
// not lent to the brain, is refused when written; one whose access changed
// waits, and nothing is called.
func TestMonitorOnlyFreeReads(t *testing.T) {
	c, mon, now := pollCore(t)
	for _, s := range []monitorSpec{
		{Server: "monitor", Tool: "rollback"},
		{Server: "nowhere", Tool: "status"},
		{Server: "monitor", Tool: "status", Every: 2 * 24 * 60},
		{Server: "monitor", Tool: "status", Cond: condNew},
		{Server: "monitor", Tool: "status", Path: "a..b"},
	} {
		if _, err := c.writeMonitor("x", s, nil); err == nil {
			t.Errorf("%+v was written", s)
		}
	}
	if _, err := c.writeMonitor("Pas Bon", monitorSpec{Server: "monitor", Tool: "status"}, nil); err == nil {
		t.Error("a bad name was written")
	}
	approvedMonitor(t, c, "etat", monitorSpec{Server: "monitor", Tool: "status", Args: map[string]any{"service": "web"}, Every: 1})
	u := c.pool.get("monitor") // the user sets the tool to « Tout sur accord »: no free read
	u.mu.Lock()
	u.srv.Access = accessAsk
	u.mu.Unlock()
	calls := mon.calls
	lookAt(c, *now)
	m, _ := c.monitorNamed("etat")
	if mon.calls != calls || m.Look.Waiting == "" {
		t.Fatalf("calls %d → %d, look %+v", calls, mon.calls, m.Look)
	}
}

// The brain only proposes: a new monitor waits for its card (tainted said),
// no drops it, yes turns it on; a change of an approved one waits too, the
// old one looking meanwhile.
func TestMonitorCard(t *testing.T) {
	c, _, now := pollCore(t)
	spec := monitorSpec{Server: "monitor", Tool: "status", Every: 5, Path: "data.state"}
	if _, err := c.writeMonitor("etat", spec, []string{"mail"}); err != nil {
		t.Fatal(err)
	}
	m, _ := c.monitorNamed("etat")
	p := c.proposalOf(cardMonitor, m.ID)
	if !p.Pending || p.Tainted != "mail" || !strings.Contains(p.Text("fr"), "etat") {
		t.Fatalf("card %+v", p)
	}
	if f := p.fields("fr"); len(f) == 0 || !strings.Contains(f[0].Value, "monitor.status") || !strings.Contains(f[0].Value, "data.state") {
		t.Fatalf("fields %+v", f)
	}
	lookAt(c, *now)
	if m, _ := c.monitorNamed("etat"); m.Look.Checked != 0 {
		t.Fatal("a proposed monitor looked")
	}
	if err := c.decideMonitor(m.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := c.monitorNamed("etat"); err == nil {
		t.Fatal("a refused monitor stayed")
	}

	m = approvedMonitor(t, c, "etat", spec)
	if m.State != monitorOn || c.proposalOf(cardMonitor, m.ID).Pending {
		t.Fatalf("approved %+v", m)
	}
	if out, _ := c.writeMonitor("etat", spec, nil); !strings.Contains(out["result"].(string), "same") {
		t.Fatalf("the same spec: %v", out)
	}
	spec.Every = 15
	c.writeMonitor("etat", spec, nil)
	m, _ = c.monitorNamed("etat")
	if m.Spec.Every != 5 || m.Pending == nil || !c.proposalOf(cardMonitor, m.ID).Pending {
		t.Fatalf("change %+v", m)
	}
	c.decideMonitor(m.ID, true)
	if m, _ = c.monitorNamed("etat"); m.Spec.Every != 15 || m.Pending != nil || m.State != monitorOn {
		t.Fatalf("after yes %+v", m)
	}
	out, isErr := callBrainTool(t, c, "monitors", map[string]any{})
	if isErr || !strings.Contains(valueText(out), "etat") {
		t.Fatalf("monitors %v", out)
	}
}

// A skill reacts to a monitor's event with its when.
func TestMonitorTriggersSkill(t *testing.T) {
	c, now := skillCore(t)
	addTriggered(t, c, "---\nname: prod\ndescription: la prod\nwhen: monitor.changed key=getout-prod\ntools: [monitor]\n---\nEnquête.\n")
	approvedMonitor(t, c, "getout-prod", monitorSpec{Server: "monitor", Tool: "status", Args: map[string]any{"service": "web"}, Every: 1, Path: "data.version"})
	c.emitMonitor(&monitor{Name: "getout-prod", Spec: monitorSpec{Server: "monitor"}}, evMonitorChanged, map[string]any{"value": "2.0.1"})
	c.tickSkills(context.Background(), *now)
	rs := runsOfSkill(t, c, "prod")
	if len(rs) != 1 || !rs[0].External {
		t.Fatalf("runs %+v", rs)
	}
	task, _ := c.store.task(rs[0].Task)
	if !strings.Contains(task.Prompt, "monitor.changed") || !strings.Contains(task.Prompt, "2.0.1") {
		t.Fatalf("prompt %s", task.Prompt)
	}
	// A server cannot write a monitor's kinds.
	if n := c.takeServerEvents("mail", serverEventsDoc{Epoch: "e"}); n != 0 {
		t.Fatal(n)
	}
	if !reservedEventKind(evMonitorChanged) {
		t.Fatal("monitor. is not reserved")
	}
}
