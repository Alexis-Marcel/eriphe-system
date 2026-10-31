package main

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

// A big machine that sleeps and a small one that never does.
const (
	laptopInfo = `{"runtimes":["claude-code"],"cpus":16,"memory_bytes":34359738368,"agents_ready":["claude-code"]}`
	nasInfo    = `{"runtimes":["claude-code"],"cpus":4,"always_on":true}`
)

// A resident task goes to an online always-on node that fits, whatever
// the scores; to the best other one only when none can take it; to the
// node asked for by hand always. Other tasks keep the scores.
func TestScheduleResident(t *testing.T) {
	nodes := []NodeRow{
		{Name: "laptop", Online: true, Info: json.RawMessage(laptopInfo)},
		{Name: "nas", Online: true, Info: json.RawMessage(nasInfo)},
	}
	idle := func(string) (int, error) { return 0, nil }
	busy := func(n string) (int, error) {
		if n == "nas" {
			return 3, nil
		}
		return 0, nil
	}
	resident := placement{Runtime: "claude-code", Resident: true}
	normal := placement{Runtime: "claude-code"}
	cases := []struct {
		name   string
		nodes  []NodeRow
		want   string
		p      placement
		active func(string) (int, error)
		got    string
		waits  bool
	}{
		{"scores pick the laptop", nodes, "", normal, idle, "laptop", false},
		{"resident on the nas", nodes, "", resident, idle, "nas", false},
		{"even when the nas is busy", nodes, "", resident, busy, "nas", false},
		{"by hand wins", nodes, "laptop", resident, idle, "laptop", false},
		{"fallback when the nas is off", []NodeRow{nodes[0], {Name: "nas", Info: json.RawMessage(nasInfo)}}, "", resident, idle, "laptop", false},
		{"none has the label", nodes, "", placement{Runtime: "claude-code", Resident: true, Needs: []string{"gpu"}}, idle, "", false},
		{"waits when only the nas could", []NodeRow{{Name: "laptop", Info: json.RawMessage(laptopInfo)}, {Name: "nas", Info: json.RawMessage(nasInfo)}}, "", resident, idle, "", true},
	}
	for _, c := range cases {
		got, err := schedule(c.nodes, c.want, c.p, c.active)
		var waiting *waitingError
		if got != c.got || errors.As(err, &waiting) != c.waits {
			t.Errorf("%s: schedule = %q, %v", c.name, got, err)
		}
	}
	// The nas lacks a label the laptop has: the resident goes there.
	nodes[0].Info = json.RawMessage(`{"runtimes":["claude-code"],"labels":["gpu"]}`)
	if got, err := schedule(nodes, "", placement{Runtime: "claude-code", Resident: true, Needs: []string{"gpu"}}, idle); got != "laptop" || err != nil {
		t.Errorf("label fallback = %q, %v", got, err)
	}
}

// residentCore is a brain with a laptop and a nas, both online, started
// long enough ago for the graces to count.
func residentCore(t *testing.T) *catalogCore {
	t.Helper()
	c := brainCore(t, &BrainConfig{})
	c.store.setNodeStatus("pc", false, nil) // brainCore's, gone for these tests
	c.store.registerNode("laptop", hashSecret("a"))
	c.store.setNodeStatus("laptop", true, []byte(laptopInfo))
	c.store.registerNode("nas", hashSecret("b"))
	c.store.setNodeStatus("nas", true, []byte(nasInfo))
	c.started = time.Now().Add(-time.Hour)
	return c
}

// offlineSince marks a node offline, last seen ago.
func offlineSince(t *testing.T, c *core, name string, ago time.Duration) {
	t.Helper()
	if _, err := c.store.db.Exec(`UPDATE nodes SET online = 0, last_seen = ? WHERE name = ?`, time.Now().Add(-ago).Unix(), name); err != nil {
		t.Fatal(err)
	}
}

// The brain starts on the machine that never sleeps, not the strongest.
func TestBrainLivesOnAlwaysOnNode(t *testing.T) {
	c := residentCore(t)
	c.tendAssistants(context.Background(), "tok")
	if task, err := c.store.task(brainStatus(c.core).Task); err != nil || task.Node != "nas" {
		t.Fatalf("brain on %+v, %v", task, err)
	}
	// An assistant with no trigger and no word for the user is not resident.
	if residentKind(roleTest) || !residentKind(assistantRoleBrain) {
		t.Fatal("resident kinds")
	}
}

// The brain whose node has been gone past the grace moves, with the
// message its node never received, and starts afresh elsewhere; before
// the grace, or pinned by hand, it waits.
func TestResidentMovesOffOfflineNode(t *testing.T) {
	c := residentCore(t)
	ctx := context.Background()
	c.store.setNodeStatus("nas", false, nil)
	c.tendAssistants(ctx, "tok")
	old := brainStatus(c.core).Task
	if task, _ := c.store.task(old); task.Node != "laptop" {
		t.Fatalf("fallback on %q", task.Node)
	}
	setBrainStatus(c.core, func(st *AssistantStatus) { st.Session = "s-1" })
	c.store.updateTask(old, map[string]any{"state": stateIdle})
	c.store.addMessage(roleUser, "Tu es là ?", false)
	c.tendAssistants(ctx, "tok") // delivered to its inbox
	if msgs, _ := c.store.undelivered(); len(msgs) != 0 || !c.hasUndelivered(old) {
		t.Fatalf("not delivered: %v", msgs)
	}
	// The laptop sleeps; the nas is back.
	c.store.setNodeStatus("nas", true, []byte(nasInfo))
	offlineSince(t, c.core, "laptop", 10*time.Second)
	if c.tendAssistants(ctx, "tok"); brainStatus(c.core).Task != old {
		t.Fatal("moved before the grace")
	}
	offlineSince(t, c.core, "laptop", time.Minute)
	c.tendAssistants(ctx, "tok")
	task, _ := c.store.task(old)
	if task.State != stateStopped || c.store.get("brain_stopped:"+old) == "" || brainStatus(c.core).Session != "" {
		t.Fatalf("old task = %+v, status = %+v", task, brainStatus(c.core))
	}
	if msgs, _ := c.store.undelivered(); len(msgs) != 1 {
		t.Fatalf("the message is not handed back: %v", msgs)
	}
	c.tendAssistants(ctx, "tok")
	next, _ := c.store.task(brainStatus(c.core).Task)
	spec, _ := c.store.taskSpec(next.ID)
	if next.ID == old || next.Node != "nas" || spec.Resume != "" {
		t.Fatalf("next = %+v, resume %q", next, spec.Resume)
	}

	// Pinned by hand to the laptop, it waits for it.
	editBrain(c.core, func(a *Assistant) { a.Node = "laptop" })
	c.store.setNodeStatus("laptop", true, []byte(laptopInfo))
	c.store.updateTask(next.ID, map[string]any{"state": stateStopped})
	noWait(c.core, brainAssistant)
	c.tendAssistants(ctx, "tok")
	pinned := brainStatus(c.core).Task
	offlineSince(t, c.core, "laptop", time.Hour)
	if c.tendAssistants(ctx, "tok"); brainStatus(c.core).Task != pinned {
		t.Fatal("a brain pinned by hand moved")
	}
}

// A brain that fell back on a machine that sleeps goes home once the
// always-on one is back, between two turns only.
func TestResidentReturnsToAlwaysOnAtIdle(t *testing.T) {
	c := residentCore(t)
	ctx := context.Background()
	c.store.setNodeStatus("nas", false, nil)
	c.tendAssistants(ctx, "tok")
	old := brainStatus(c.core).Task
	c.store.setNodeStatus("nas", true, []byte(nasInfo))
	c.store.updateTask(old, map[string]any{"state": stateRunning})
	if c.tendAssistants(ctx, "tok"); brainStatus(c.core).Task != old {
		t.Fatal("moved in the middle of a turn")
	}
	c.store.updateTask(old, map[string]any{"state": stateIdle})
	c.tendAssistants(ctx, "tok")
	if task, _ := c.store.task(old); task.State != stateStopped {
		t.Fatalf("still on the laptop: %+v", task)
	}
	c.tendAssistants(ctx, "tok")
	if next, _ := c.store.task(brainStatus(c.core).Task); next.Node != "nas" {
		t.Fatalf("next on %q", next.Node)
	}
}

// A worker not started on a node gone past pendingNodeGrace is placed
// again; one started, or pinned, keeps its node.
func TestStrandedTaskPlacedAgain(t *testing.T) {
	c := residentCore(t)
	add := func(id, state string, pinned bool) {
		c.store.insertTask(&TaskRow{ID: id, Node: "laptop", Kind: "worker", Agent: "sonnet", Runtime: "claude-code", State: state, Pinned: pinned})
		c.store.updateTask(id, map[string]any{"desired": desiredRunning})
	}
	add("t-new", statePending, false)
	add("t-run", stateRunning, false)
	add("t-pin", statePending, true)
	offlineSince(t, c.core, "laptop", time.Minute)
	c.replaceStranded()
	if task, _ := c.store.task("t-new"); task.Node != "laptop" {
		t.Fatal("placed again before the grace")
	}
	offlineSince(t, c.core, "laptop", 6*time.Minute)
	c.replaceStranded()
	for id, want := range map[string]string{"t-new": "nas", "t-run": "laptop", "t-pin": "laptop"} {
		if task, _ := c.store.task(id); task.Node != want {
			t.Errorf("%s on %q, want %q", id, task.Node, want)
		}
	}
}
