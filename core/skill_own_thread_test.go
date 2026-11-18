package main

import (
	"context"
	"testing"
	"time"
)

// skillThreadOf is the thread of an alias, 0 for none.
func skillThreadOf(t *testing.T, c *core, alias string) int64 {
	t.Helper()
	ss, err := c.threadsWhere(threadOfAlias, alias)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) > 1 {
		t.Fatalf("threads of %s: %+v", alias, ss)
	}
	if len(ss) == 0 {
		return 0
	}
	return ss[0].ID
}

// A watcher's thread exists once the user approves it, before any event:
// standing, titled by the skill, its monitor's list read by code at once,
// and no run (no model). Approved again, the same thread, its source once,
// nothing new on it; its first run lands on it.
func TestSkillThreadOnApproval(t *testing.T) {
	c, fm, now := pollCore(t)
	c.store.registerDevice("pc", hashSecret("pc-secret"))
	c.store.setDeviceStatus("pc", true, []byte(`{"runtimes":["claude-code"]}`))
	c.subscribeEvents(c.queueSkillEvent)
	fm.mu.Lock()
	fm.rules = []map[string]any{{"uid": "a", "name": "5xx"}}
	fm.mu.Unlock()
	approvedMonitor(t, c, "alertes", monitorSpec{Server: "monitor", Tool: "alerts", Every: 1, Cond: condNew, Path: "data.rules", ID: "uid"})
	lookAt(c, *now)
	approve := func(desc string) {
		t.Helper()
		k, _, err := c.store.addSkillVersion([]skillFile{{Path: "SKILL.md", Data: []byte("---\nname: veille-alertes\ndescription: " + desc + "\nwhen: monitor.new key=alertes\ntools: [eriphe-threads, monitor]\n---\nNote chaque alerte.\n")}}, "upload")
		if err != nil {
			t.Fatal(err)
		}
		if err := c.approveSkill(k.Name, k.Pending, "owner"); err != nil {
			t.Fatal(err)
		}
		c.surf.refreshing.Wait()
	}
	approve("Trie les alertes.")
	id := skillThreadOf(t, c, "topic:alerte")
	th, err := c.thread(id)
	if id == 0 || err != nil || th.Ends != endsStanding || th.Title != "Veille alertes" || th.led() {
		t.Fatalf("thread %d %+v %v", id, th, err)
	}
	if srcs := c.threadSources(id); len(srcs) != 1 || srcs[0].Name != "alertes" || srcs[0].Items == nil {
		t.Fatalf("sources %+v", srcs)
	}
	if rows := itemRows(t, c, id); rows["a"] == nil {
		t.Fatalf("rows %v", rows)
	}
	if rs := runsOfSkill(t, c, "veille-alertes"); len(rs) != 0 {
		t.Fatalf("a run at approval: %+v", rs)
	}
	s, _ := c.surface(threadSurfaceID(id))
	fresh := s.Look.New

	approve("Trie les alertes de la prod.")
	c.openSkillThreads("", false) // a start
	c.surf.refreshing.Wait()
	if again := skillThreadOf(t, c, "topic:alerte"); again != id || len(c.threadSources(id)) != 1 {
		t.Fatalf("thread %d, sources %+v", again, c.threadSources(id))
	}
	if s, _ = c.surface(threadSurfaceID(id)); s.Look.New != fresh {
		t.Fatalf("new %d, was %d", s.Look.New, fresh)
	}

	fm.mu.Lock()
	fm.rules = append(fm.rules, map[string]any{"uid": "b", "name": "down"})
	fm.mu.Unlock()
	lookAt(c, now.Add(time.Minute))
	lookAt(c, now.Add(2*time.Minute))
	c.tickSkills(context.Background(), now.Add(2*time.Minute))
	if rs := runsOfSkill(t, c, "veille-alertes"); len(rs) != 1 || runThreadOf(c, rs[0]) != id {
		t.Fatalf("runs %+v: not on thread %d", rs, id)
	}
	if len(c.threadSources(id)) != 1 {
		t.Fatalf("sources %+v", c.threadSources(id))
	}
}

// At start, each skill on whose alias has no thread gets it: a monitor's
// watcher with its source, a timed one with thread: without; a skill off,
// one not lent eriphe-threads or one with no alias known before its events
// gets none. Turned on, a skill gets its thread.
func TestSkillThreadsAtStart(t *testing.T) {
	c, _, _ := pollCore(t)
	approvedMonitor(t, c, "alertes", monitorSpec{Server: "monitor", Tool: "alerts", Every: 1, Cond: condNew, Path: "data.rules", ID: "uid"})
	addTriggered(t, c, "---\nname: veille-alertes\ndescription: Les alertes.\nwhen: monitor.new key=alertes\ntools: [eriphe-threads, monitor]\n---\nx\n")
	addTriggered(t, c, "---\nname: matin\ndescription: Le matin.\nwhen: time weekdays 08:00\ntools: [eriphe-threads]\nthread: topic:matin\n---\nx\n")
	addTriggered(t, c, "---\nname: eteint\ndescription: Off.\nwhen: monitor.new key=alertes\ntools: [eriphe-threads]\nthread: topic:eteint\n---\nx\n")
	addTriggered(t, c, "---\nname: sans-fil\ndescription: No threads.\nwhen: time weekdays 09:00\ntools: [monitor]\nthread: topic:sans-fil\n---\nx\n")
	addTriggered(t, c, "---\nname: tout\ndescription: Any key.\nwhen: monitor.new\ntools: [eriphe-threads]\n---\nx\n")
	if err := c.store.enableSkill("eteint", false); err != nil {
		t.Fatal(err)
	}
	if skillThreadOf(t, c, "topic:alerte") != 0 {
		t.Fatal("a thread before the start")
	}
	c.openSkillThreads("", false)
	c.surf.refreshing.Wait()
	id := skillThreadOf(t, c, "topic:alerte")
	if srcs := c.threadSources(id); id == 0 || len(srcs) != 1 || srcs[0].Name != "alertes" {
		t.Fatalf("thread %d, sources %+v", id, srcs)
	}
	morning := skillThreadOf(t, c, "topic:matin")
	if th, _ := c.thread(morning); morning == 0 || !th.standing() || len(c.threadSources(morning)) != 0 {
		t.Fatalf("timed thread %d %+v", morning, th)
	}
	if skillThreadOf(t, c, "topic:eteint") != 0 || skillThreadOf(t, c, "topic:sans-fil") != 0 {
		t.Fatal("a thread for a skill off, or not lent eriphe-threads")
	}
	ss, _ := c.threadsWhere(activeThreads)
	if len(ss) != 2 {
		t.Fatalf("threads %+v", ss)
	}

	// Started again: the same threads.
	c.openSkillThreads("", false)
	if ss, _ = c.threadsWhere(activeThreads); len(ss) != 2 || skillThreadOf(t, c, "topic:alerte") != id {
		t.Fatalf("threads %+v", ss)
	}
	// Turned on: its thread.
	c.store.enableSkill("eteint", true)
	c.openSkillThreads("eteint", true)
	if skillThreadOf(t, c, "topic:eteint") == 0 {
		t.Fatal("no thread once on")
	}
}

// A watcher approved before its monitor gets its thread without a source;
// at the next start, the monitor on, the thread gets it. Just opened, the
// thread is new to the user: a tile until they see it.
func TestSkillThreadSourceAtStart(t *testing.T) {
	c, _, _ := pollCore(t)
	k, _, err := c.store.addSkillVersion([]skillFile{{Path: "SKILL.md", Data: []byte("---\nname: veille-alertes\ndescription: Les alertes.\nwhen: monitor.new key=alertes\ntools: [eriphe-threads, monitor]\n---\nx\n")}}, "upload")
	if err != nil {
		t.Fatal(err)
	}
	if err := c.approveSkill(k.Name, k.Pending, "owner"); err != nil {
		t.Fatal(err)
	}
	id := skillThreadOf(t, c, "topic:alerte")
	if id == 0 || len(c.threadSources(id)) != 0 {
		t.Fatalf("thread %d, sources %+v", id, c.threadSources(id))
	}
	if sf, err := c.surface(threadSurfaceID(id)); err != nil || tileStateOf(sf) != tileNew || foldsQuiet(sf) {
		t.Fatalf("just opened: %+v %v", sf.Look, err)
	}
	approvedMonitor(t, c, "alertes", monitorSpec{Server: "monitor", Tool: "alerts", Every: 1, Cond: condNew, Path: "data.rules", ID: "uid"})
	c.openSkillThreads("", false)
	c.surf.refreshing.Wait()
	if again := skillThreadOf(t, c, "topic:alerte"); again != id || len(c.threadSources(id)) != 1 {
		t.Fatalf("thread %d, sources %+v", again, c.threadSources(id))
	}
}

// The alias of a skill's thread before any event, as its runs make it,
// and the monitor whose new items it handles.
func TestSkillOwnThread(t *testing.T) {
	for when, want := range map[string][2]string{
		"monitor.new key=alertes":                 {"topic:alerte", "alertes"},
		"monitor.changed key=solde":               {"topic:solde", ""},
		"monitor.new, monitor.failed key=alertes": {"topic:alerte", "alertes"},
		"monitor.new":                             {"", ""},
		"mail.received key=alertes":               {"", ""},
		"time weekdays 08:00":                     {"", ""},
	} {
		m, err := parseSkillMD([]byte("---\nname: x\ndescription: y\nwhen: " + when + "\n---\nz\n"))
		if err != nil {
			t.Fatalf("%s: %v", when, err)
		}
		alias, monitor := skillOwnThread(m.Trigger)
		if alias != want[0] || monitor != want[1] {
			t.Errorf("%s: %q %q, want %q %q", when, alias, monitor, want[0], want[1])
		}
	}
	if alias, monitor := skillOwnThread(&skillTrigger{Kind: whenTime, Thread: "topic:matin"}); alias != "topic:matin" || monitor != "" {
		t.Errorf("thread: %q %q", alias, monitor)
	}
}
