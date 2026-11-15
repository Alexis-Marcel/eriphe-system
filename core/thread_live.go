package main

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// A live thread (#555): a thread may follow monitors (monitors.go), which
// are sources the user approved and that code reads on a clock, without a
// model. Who holds the thread links one by name (link_thread, monitor);
// then, on the thread's surface, composed by code:
//
//   - its latest number (a Metric) and the last ones (a Chart), each look
//     writing the surface again; with when it was read and « aucun
//     modèle »: the number is the monitor's, not Eriphe's;
//   - each of its events (crossed, changed, new, failed, recovered,
//     received) as a line of the timeline and a fact of the header, worded
//     by code from the event's kind and numbers only (thread_watch): never
//     a changed value, a new item or an error's text;
//   - while a limit it watches is crossed, or its looks fail, the thread's
//     weight rises: it comes to the user without a model speaking.
//
// Nothing wakes the brain here: a skill reacts to a monitor's events as
// before (skill_triggers.go). What a monitor reads is data: a monitor of a
// server not trusted makes the surface « extérieur » for the models
// (codeTaint monitor:<name>); its Proposals still answer in place.
//
// A thread's holder's part stays composed: no sources (thread_surface.go).

const threadLiveSchema = `CREATE TABLE IF NOT EXISTS thread_monitors (
	thread  INTEGER NOT NULL,
	monitor TEXT NOT NULL,
	created INTEGER NOT NULL,
	PRIMARY KEY (thread, monitor)
);
CREATE TABLE IF NOT EXISTS thread_watch (
	id      INTEGER PRIMARY KEY AUTOINCREMENT,
	thread  INTEGER NOT NULL,
	monitor TEXT NOT NULL,
	at      INTEGER NOT NULL,
	kind    TEXT NOT NULL,             -- the event: monitor.crossed, monitor.changed, …
	value   REAL,                      -- crossed: the number
	lim     REAL,                      -- crossed: the limit
	below   INTEGER NOT NULL DEFAULT 0, -- crossed: the limit is a floor
	holds   INTEGER NOT NULL DEFAULT 0, -- crossed: past the limit now
	tainted TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS thread_watch_thread ON thread_watch (thread, id);`

func init() { extraSchema = append(extraSchema, threadLiveSchema) }

const (
	threadMonitorsMax = 3  // monitors a thread follows
	threadWatchShown  = 5  // monitor events its timeline shows, the last ones
	threadWatchKept   = 50 // kept a thread
	monitorPointsMax  = 24 // numbers a monitor keeps for a chart
	threadWeightWatch = 65 // a thread whose monitor is past its limit, or failing
)

// monitorPoint is a number a monitor read, and when.
type monitorPoint struct {
	At int64   `json:"at"`
	V  float64 `json:"v"`
}

// notePoint keeps the number a good look read, if it is one; else the
// last number is no longer true.
func notePoint(st *monitorLook, got any, now time.Time) {
	n, ok := number(got)
	if !ok {
		st.Points = nil
		return
	}
	st.Points = append(st.Points, monitorPoint{now.Unix(), n})
	if k := len(st.Points); k > monitorPointsMax {
		st.Points = st.Points[k-monitorPointsMax:]
	}
}

// linkMonitor makes a thread follow a monitor, by its name: one the user
// approved.
func (c *core) linkMonitor(id int64, name, by string) (string, error) {
	threadMu.Lock()
	defer threadMu.Unlock()
	s, err := c.thread(id)
	if err != nil {
		return "", err
	}
	if !s.active() {
		return "", fmt.Errorf("the thread is %s: reopen it first", s.Look())
	}
	name = strings.TrimSpace(name)
	m, err := c.monitorNamed(name)
	if err != nil {
		return "", fmt.Errorf("no monitor %q (see monitors)", name)
	}
	if m.State == monitorProposed {
		return "", errors.New("the monitor waits for the user's approval on its card: link it once approved")
	}
	var n int
	c.store.db.QueryRow(`SELECT COUNT(*) FROM thread_monitors WHERE thread = ? AND monitor != ?`, id, name).Scan(&n)
	if n >= threadMonitorsMax {
		return "", fmt.Errorf("a thread follows %d monitors at most", threadMonitorsMax)
	}
	c.store.db.Exec(`INSERT OR IGNORE INTO thread_monitors (thread, monitor, created) VALUES (?, ?, ?)`, id, name, time.Now().Unix())
	c.logThread(id, by, "Surveillance suivie : "+name+".")
	return "Monitor linked: its latest number and its events show on the thread's surface, read by code; a limit crossed raises the thread for the user. A skill still reacts to its events.", nil
}

// threadMonitors are the monitors a thread follows, that still exist.
func (c *core) threadMonitors(thread int64) []*monitor {
	rows, err := c.store.db.Query(`SELECT monitor FROM thread_monitors WHERE thread = ? ORDER BY created, monitor`, thread)
	if err != nil {
		return nil
	}
	var names []string
	for rows.Next() {
		var n string
		if rows.Scan(&n) == nil {
			names = append(names, n)
		}
	}
	rows.Close() // one connection: the monitors are read after
	var out []*monitor
	for _, n := range names {
		if m, err := c.monitorNamed(n); err == nil {
			out = append(out, m)
		}
	}
	return out
}

// threadsFollowing are the active threads that follow a monitor.
func (c *core) threadsFollowing(name string) []int64 {
	rows, err := c.store.db.Query(`SELECT m.thread FROM thread_monitors m JOIN threads t ON t.id = m.thread
		WHERE m.monitor = ? AND t.`+activeThreads+` ORDER BY m.thread`, name)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			out = append(out, id)
		}
	}
	return out
}

// monitorToThreads keeps a monitor's event on each thread that follows it,
// from its kind and numbers only. A poll's look writes the surfaces once
// its state is saved (monitorLooked); an incoming call's, at once.
func (c *core) monitorToThreads(m *monitor, kind string, p map[string]any) {
	ids := c.threadsFollowing(m.Name)
	if len(ids) == 0 {
		return
	}
	var value, lim any
	below, holds := false, false
	if kind == evMonitorCrossed {
		value = p["value"]
		if v, ok := p["below"]; ok {
			lim, below = v, true
		} else {
			lim = p["above"]
		}
		holds, _ = p["holds"].(bool)
	}
	tainted := ""
	if !c.trustsServer(m.Spec.Server) {
		tainted = "monitor:" + m.Name
	}
	now := time.Now().Unix()
	for _, id := range ids {
		c.store.db.Exec(`INSERT INTO thread_watch (thread, monitor, at, kind, value, lim, below, holds, tainted) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			id, m.Name, now, kind, value, lim, below, holds, tainted)
		c.store.db.Exec(`DELETE FROM thread_watch WHERE thread = ? AND id NOT IN (SELECT id FROM thread_watch WHERE thread = ? ORDER BY id DESC LIMIT ?)`,
			id, id, threadWatchKept)
		if m.Spec.Way != wayPoll {
			c.threadMoved(id)
		}
	}
}

// monitorLooked writes again the surfaces of the threads that follow a
// monitor, once its look is saved: its new number, its events, its state.
func (c *core) monitorLooked(name string) {
	ids := c.threadsFollowing(name)
	for _, id := range ids {
		c.keepThreadSurface(id) // written only when what it shows changed
	}
	if len(ids) > 0 {
		c.kickWorkspace() // its weight may have changed
	}
}

// threadWatchEvent is a monitor's event as a thread keeps it.
type threadWatchEvent struct {
	ID      int64
	Monitor string
	At      time.Time
	Kind    string
	Value   *float64
	Lim     *float64
	Below   bool
	Holds   bool
	Tainted string
}

// watchEvents are a thread's monitor events, the last n, oldest first.
func (c *core) watchEvents(thread int64, n int) []threadWatchEvent {
	rows, err := c.store.db.Query(`SELECT id, monitor, at, kind, value, lim, below, holds, tainted FROM
		(SELECT * FROM thread_watch WHERE thread = ? ORDER BY id DESC LIMIT ?) ORDER BY id`, thread, n)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []threadWatchEvent
	for rows.Next() {
		var e threadWatchEvent
		var at int64
		if rows.Scan(&e.ID, &e.Monitor, &at, &e.Kind, &e.Value, &e.Lim, &e.Below, &e.Holds, &e.Tainted) == nil {
			e.At = time.Unix(at, 0)
			out = append(out, e)
		}
	}
	return out
}

// fmtNum words a number as the user reads it.
func fmtNum(v float64, lang string) string {
	s := strconv.FormatFloat(v, 'f', -1, 64)
	if f := strconv.FormatFloat(v, 'f', 2, 64); len(f) < len(s) {
		s = strings.TrimRight(strings.TrimRight(f, "0"), ".")
	}
	if lang != "en" {
		s = strings.Replace(s, ".", ",", 1)
	}
	return s
}

// text words a monitor's event from its kind and numbers only.
func (e threadWatchEvent) text(lang string) string {
	if e.Kind == evMonitorCrossed && e.Value != nil && e.Lim != nil {
		key := "thread.watch.above"
		switch {
		case e.Below && e.Holds:
			key = "thread.watch.below"
		case e.Below:
			key = "thread.watch.backabove"
		case !e.Holds:
			key = "thread.watch.backbelow"
		}
		return fmt.Sprintf(tr(lang, key), e.Monitor, fmtNum(*e.Value, lang), fmtNum(*e.Lim, lang))
	}
	kind := strings.TrimPrefix(e.Kind, "monitor.")
	if k := "thread.watch." + kind; tr(lang, k) != k {
		return fmt.Sprintf(tr(lang, k), e.Monitor)
	}
	return fmt.Sprintf(tr(lang, "thread.watch.other"), e.Monitor)
}

// state is its Timeline item's: past a limit or failing is failed.
func (e threadWatchEvent) state() string {
	if e.Kind == evMonitorFailed || e.Kind == evMonitorCrossed && e.Holds {
		return "failed"
	}
	return "done"
}

// threadLive composes the live part of an active thread's surface: each
// monitor it follows, its latest number and the last ones. True when one
// is past its limit or failing: the thread's weight rises.
func (c *core) threadLive(in *surfaceIn, out *threadComps, data map[string]any, s Thread, now time.Time) bool {
	if !s.active() {
		return false
	}
	ms := c.threadMonitors(s.ID)
	if len(ms) == 0 {
		return false
	}
	lang, loc := c.userLang(), c.userZone()
	type comp = map[string]any
	hot := false
	var live []any
	out.add(comp{"id": "thread_live", "component": "Text", "variant": "subtitle", "text": tr(lang, "thread.live.title")})
	for i, m := range ms {
		n := strconv.Itoa(i)
		alert := m.Spec.Cond == condCross && m.Look.Hit || m.Look.Failed
		hot = hot || alert
		v := map[string]any{"name": m.Name}
		parts := []string{fmt.Sprintf(tr(lang, "thread.live.by"), m.Name)}
		if k := len(m.Look.Points); k > 0 {
			last := m.Look.Points[k-1]
			v["value"] = last.V
			parts = append(parts, fmt.Sprintf(tr(lang, "thread.live.read"), threadClock(time.Unix(last.At, 0), now, loc, lang)))
			out.add(comp{"id": "thread_live_" + n, "component": "Metric", "label": m.Name, "value": map[string]any{"@path": "/thread/live/" + n + "/value"}})
			if k > 1 {
				points := make([]any, 0, k)
				for _, p := range m.Look.Points {
					points = append(points, map[string]any{"x": threadClock(time.Unix(p.At, 0), now, loc, lang), "y": p.V})
				}
				v["series"] = []any{map[string]any{"name": m.Name, "points": points}}
				out.add(comp{"id": "thread_live_" + n + "_chart", "component": "Chart", "kind": "line", "series": map[string]any{"@path": "/thread/live/" + n + "/series"}})
			}
		} else if m.Look.Checked > 0 {
			parts = append(parts, fmt.Sprintf(tr(lang, "thread.live.read"), threadClock(time.Unix(m.Look.Checked, 0), now, loc, lang)))
		}
		switch {
		case m.Look.Failed:
			parts = append(parts, tr(lang, "thread.live.failing"))
		case alert:
			parts = append(parts, tr(lang, "thread.live.past"))
		case m.State == monitorPaused:
			parts = append(parts, tr(lang, "thread.live.paused"))
		}
		parts = append(parts, tr(lang, "thread.live.nomodel"))
		if !c.trustsServer(m.Spec.Server) {
			parts = append(parts, tr(lang, "thread.surface.external"))
			in.codeTaint = append(in.codeTaint, "monitor:"+m.Name)
		}
		v["caption"] = strings.Join(parts, threadExternalSep)
		live = append(live, v)
		out.add(comp{"id": "thread_live_" + n + "_by", "component": "Text", "variant": "caption", "text": map[string]any{"@path": "/thread/live/" + n + "/caption"}})
	}
	data["live"] = live
	return hot
}

// watchItems are a thread's last monitor events as Timeline items, and
// as facts for its header.
func (c *core) watchItems(in *surfaceIn, s Thread, lang string, loc *time.Location) ([]any, []threadFact) {
	var items []any
	var facts []threadFact
	for _, e := range c.watchEvents(s.ID, threadWatchShown) {
		text := e.text(lang)
		detail := tr(lang, "thread.watch.by")
		if e.Tainted != "" {
			detail = tr(lang, "thread.surface.external") + threadExternalSep + detail
			in.codeTaint = append(in.codeTaint, e.Tainted)
		}
		items = append(items, map[string]any{"label": text, "state": e.state(), "date": e.At.In(loc).Format(time.RFC3339), "detail": detail})
		facts = append(facts, threadFact{e.At, text + threadExternalSep + detail})
	}
	return items, facts
}
