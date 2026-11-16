package main

import (
	"maps"
	"slices"
	"strings"
	"testing"
	"time"
)

// liveOf is what a thread's surface shows of its first monitor: its
// value, its points and its caption.
func liveOf(t *testing.T, c *core, id int64) (any, int, string) {
	t.Helper()
	_, data, _ := threadSurfaceOf(t, c, id)
	live, _ := data["live"].([]any)
	if len(live) == 0 {
		t.Fatalf("no live part: %v", data)
	}
	m, _ := live[0].(map[string]any)
	n := 0
	if series, ok := m["series"].([]any); ok && len(series) > 0 {
		points, _ := series[0].(map[string]any)["points"].([]any)
		n = len(points)
	}
	caption, _ := m["caption"].(string)
	return m["value"], n, caption
}

// A thread follows a monitor the user approved: code shows its latest
// number on the thread's surface, read again at each look without a
// model; a limit crossed lands in its timeline and its header, raises the
// thread, and crossing back lowers it. What it reads is data: a monitor of
// a server not trusted taints the surface, whose cards still answer in
// place.
func TestThreadFollowsMonitor(t *testing.T) {
	c, mon, now := pollCore(t)
	limit := 90.0
	approvedMonitor(t, c, "cpu", monitorSpec{Server: "monitor", Tool: "status", Args: map[string]any{"service": "web"}, Every: 1,
		Cond: condCross, Path: "data.cpu", Above: &limit})
	s := lightThread(t, c)

	// A proposed monitor is not followed; an unknown one neither; ref and
	// monitor together are refused.
	if _, err := c.writeMonitor("idee", monitorSpec{Server: "monitor", Tool: "status", Every: 1, Path: "data.state"}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := c.addThreadSource(s.ID, threadSource{Monitor: "idee"}, byBrain); err == nil {
		t.Fatal("a proposed monitor followed")
	}
	if _, err := c.addThreadSource(s.ID, threadSource{Monitor: "nope"}, byBrain); err == nil {
		t.Fatal("an unknown monitor followed")
	}
	if _, isErr := callBrainTool(t, c, "link_thread", map[string]any{"thread_id": s.ID, "source": map[string]any{"monitor": "cpu"}, "ref": "intention:1"}); !isErr {
		t.Fatal("ref and monitor together")
	}
	if out, isErr := callBrainTool(t, c, "link_thread", map[string]any{"thread_id": s.ID, "source": map[string]any{"monitor": "cpu"}}); isErr {
		t.Fatalf("link_thread: %v", out)
	}

	at := func(min int, cpu float64) {
		mon.cpu = cpu
		lookAt(c, now.Add(time.Duration(2*min)*time.Minute))
		lookAt(c, now.Add(time.Duration(2*min)*time.Minute+monitorConfirm))
	}
	weight := func() int {
		sf, _, _ := threadSurfaceOf(t, c, s.ID)
		return sf.Weight
	}
	at(0, 47)
	if v, _, caption := liveOf(t, c, s.ID); v != 47.0 || !strings.Contains(caption, "Surveillance cpu") || strings.Contains(caption, "sans modèle") {
		t.Fatalf("first look: %v %q", v, caption)
	}
	at(1, 52)
	v, n, _ := liveOf(t, c, s.ID)
	if v != 52.0 || n < 2 {
		t.Fatalf("read again by code: %v, %d points", v, n)
	}
	if w := weight(); w != threadWeight {
		t.Fatalf("weight %d before any crossing", w)
	}

	// Crossed: in its timeline and its header, and the thread rises;
	// nothing goes to the brain.
	msgs := c.store.lastMessageSeq()
	c.threadVisited(s.ID, now.Add(-2*time.Hour)) // seen before: its header tells what came since
	c.threadVisited(s.ID, now.Add(-time.Hour))
	at(2, 95)
	if task, _ := c.store.task("t-brain"); task.State != stateIdle || c.store.lastMessageSeq() != msgs {
		t.Errorf("the brain was told: %s, %d messages", task.State, c.store.lastMessageSeq()-msgs)
	}
	crossed := "cpu : 95, au-dessus du seuil de 90"
	_, data, meta := threadSurfaceOf(t, c, s.ID)
	states, details := timelineOf(data)
	if states[crossed] != "failed" || !strings.Contains(details[crossed], "surveillance") {
		t.Fatalf("timeline %v %v", states, details)
	}
	h := headerOf(t, c, s.ID)
	if !slices.ContainsFunc(slices.Collect(maps.Values(h)), func(x string) bool { return strings.Contains(x, crossed) }) {
		t.Errorf("not in the header: %v", h)
	}
	if w := weight(); w != threadWeightWatch {
		t.Errorf("weight %d past the limit", w)
	}
	if _, _, caption := liveOf(t, c, s.ID); !strings.Contains(caption, "au-delà du seuil") {
		t.Errorf("caption %q", caption)
	}
	// Data from outside: the surface is tainted, its cards answer.
	if !meta.Tainted || !meta.Answers {
		t.Errorf("meta %+v", meta)
	}

	// Back under: said, and the thread comes down.
	at(3, 60)
	_, data, _ = threadSurfaceOf(t, c, s.ID)
	if states, _ = timelineOf(data); states["cpu : 60, revenu sous le seuil de 90"] != "done" {
		t.Errorf("back: %v", states)
	}
	if w := weight(); w != threadWeight {
		t.Errorf("weight %d back under", w)
	}

	// Paused while past its limit: the thread comes down at once.
	at(4, 97)
	if w := weight(); w != threadWeightWatch {
		t.Fatalf("weight %d past the limit again", w)
	}
	if err := c.pauseMonitor("cpu", true); err != nil {
		t.Fatal(err)
	}
	if w := weight(); w != threadWeight {
		t.Errorf("weight %d once paused", w)
	}
	if err := c.pauseMonitor("cpu", false); err != nil {
		t.Fatal(err)
	}

	// Ended, the thread no longer follows it: no new event lands.
	before := len(c.watchEvents(s.ID, 100))
	if err := c.closeLight(s.ID, "fait", byBrain); err != nil {
		t.Fatal(err)
	}
	at(5, 50)
	at(6, 99)
	if n := len(c.watchEvents(s.ID, 100)); n != before {
		t.Errorf("an ended thread got %d events", n-before)
	}
}

// What a thread keeps of a monitor's events is worded from their kind and
// numbers only, in both languages: never a value, an item or an error.
func TestThreadWatchTexts(t *testing.T) {
	v, l := 0.5, 2.0
	for _, e := range []threadWatchEvent{
		{Monitor: "m", Kind: evMonitorCrossed, Value: &v, Lim: &l, Below: true, Holds: true},
		{Monitor: "m", Kind: evMonitorCrossed, Value: &v, Lim: &l, Below: true},
		{Monitor: "m", Kind: evMonitorChanged}, {Monitor: "m", Kind: evMonitorNew}, {Monitor: "m", Kind: evMonitorFailed},
		{Monitor: "m", Kind: evMonitorRecovered}, {Monitor: "m", Kind: evMonitorReceived}, {Monitor: "m", Kind: "monitor.other"},
	} {
		for _, lang := range []string{"fr", "en"} {
			if text := e.text(lang); strings.Contains(text, "thread.watch") || strings.Contains(text, "%!") || !strings.HasPrefix(text, "m") {
				t.Errorf("%s %+v: %q", lang, e, text)
			}
		}
	}
	small, tiny := 0.004, 0.002
	if got := (threadWatchEvent{Monitor: "m", Kind: evMonitorCrossed, Value: &small, Lim: &tiny, Holds: true}).text("en"); got != "m: 0.004, above the limit of 0.002" {
		t.Errorf("small numbers %q", got)
	}
	if got := fmtNum(1234.5678, "fr"); got != "1234,57" {
		t.Errorf("fmtNum %q", got)
	}
	if got := (threadWatchEvent{Monitor: "m", Kind: evMonitorCrossed, Value: &v, Lim: &l, Below: true, Holds: true}).text("fr"); got != "m : 0,5, en dessous du seuil de 2" {
		t.Errorf("fr %q", got)
	}
}

// A monitor removed is no longer followed, nor a later one of its name.
func TestThreadMonitorRemoved(t *testing.T) {
	c, _, _ := pollCore(t)
	approvedMonitor(t, c, "cpu", monitorSpec{Server: "monitor", Tool: "status", Args: map[string]any{"service": "web"}, Every: 1, Path: "data.state"})
	s := lightThread(t, c)
	if _, err := c.addThreadSource(s.ID, threadSource{Monitor: "cpu"}, byBrain); err != nil {
		t.Fatal(err)
	}
	if err := c.deleteMonitor("cpu"); err != nil {
		t.Fatal(err)
	}
	approvedMonitor(t, c, "cpu", monitorSpec{Server: "monitor", Tool: "status", Args: map[string]any{"service": "web"}, Every: 1, Path: "data.state"})
	if ids := c.threadsFollowing("cpu"); len(ids) != 0 {
		t.Errorf("still followed by %v", ids)
	}
	if _, data, _ := threadSurfaceOf(t, c, s.ID); data["live"] != nil {
		t.Errorf("still live: %v", data["live"])
	}
}
