package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"
)

// What the user does with the monitors (monitors.go), on their page
// (web_monitors.go): what they write is theirs, so it looks at once, with
// no card; they pause, resume and delete one, and try a tool before
// saving, to see what a path extracts.

// saveMonitor writes a monitor of the user's: new, or changed (a new
// baseline). A change the brain proposed on it is dropped: the user's
// version is the one that holds.
func (c *core) saveMonitor(name string, s monitorSpec) error {
	name = strings.ToLower(strings.TrimSpace(name))
	if !monitorNameRe.MatchString(name) {
		return errors.New("a name of lowercase letters, digits and dashes: getout-prod")
	}
	if err := s.check(); err != nil {
		return err
	}
	if why := c.monitorWhy(s); why != "" {
		return errors.New(why)
	}
	raw, _ := json.Marshal(s)
	now := time.Now().Unix()
	m, err := c.monitorNamed(name)
	switch {
	case err == sql.ErrNoRows:
		var n int
		c.store.db.QueryRow(`SELECT COUNT(*) FROM monitors`).Scan(&n)
		if n >= monitorsMax {
			return fmt.Errorf("%d monitors at most", monitorsMax)
		}
		_, err = c.store.db.Exec(`INSERT INTO monitors (name, state, spec, created, updated) VALUES (?, ?, ?, ?, ?)`,
			name, monitorOn, string(raw), now, now)
		return err
	case err != nil:
		return err
	case m.Spec.Way == wayListen:
		return fmt.Errorf("%q is an address: pick another name", name)
	}
	state := m.State
	if state == monitorProposed {
		state = monitorOn
	}
	_, err = c.store.db.Exec(`UPDATE monitors SET state = ?, spec = ?, pending = '', look = '{}', next_at = 0, tainted = '', updated = ? WHERE id = ?`,
		state, string(raw), now, m.ID)
	return err
}

// pauseMonitor stops or restarts a monitor's looks; restarted, it takes a
// new baseline, so what changed meanwhile is not told late.
func (c *core) pauseMonitor(name string, pause bool) error {
	m, err := c.monitorNamed(name)
	if err != nil {
		return err
	}
	if m.State == monitorProposed {
		return errors.New("a proposed monitor is approved or refused, not paused")
	}
	state := monitorOn
	if pause {
		state = monitorPaused
	}
	_, err = c.store.db.Exec(`UPDATE monitors SET state = ?, look = '{}', next_at = 0, updated = ? WHERE id = ?`, state, time.Now().Unix(), m.ID)
	return err
}

// deleteMonitor removes a monitor; its events stay in the journal.
func (c *core) deleteMonitor(name string) error {
	if m, err := c.monitorNamed(name); err == nil && m.Spec.Way == wayListen {
		if v := currentVault(); v != nil {
			v.remove(hookSecretName(m.ID))
		}
	}
	_, err := c.store.db.Exec(`DELETE FROM monitors WHERE name = ?`, name)
	return err
}

// tryMonitor calls a spec's tool once and says what its path extracts,
// with the same checks as a look; nothing is kept.
func (c *core) tryMonitor(ctx context.Context, s monitorSpec) (string, error) {
	t, err := c.tryLook(ctx, s)
	return t.Said, err
}

// tried is what one try of a spec found: in words, and what a look would
// compare (the extracted value, the items' ids, the number).
type tried struct {
	Said string
	Got  any
	IDs  []string
	N    float64
}

func (c *core) tryLook(ctx context.Context, s monitorSpec) (tried, error) {
	var t tried
	if err := s.check(); err != nil {
		return t, err
	}
	if s.Way != wayPoll {
		return t, errors.New("only a monitor that polls is tried")
	}
	if why := c.sourceWhy(s); why != "" {
		return t, errors.New(why)
	}
	got, err := c.callMonitor(ctx, s)
	if err != nil {
		return t, err
	}
	t.Got = got
	switch s.Cond {
	case condNew:
		items, ok := got.([]any)
		if !ok {
			return t, fmt.Errorf("%q is not a list", cmpOr(s.Path, "the result"))
		}
		for _, it := range items {
			if v, err := extract(it, s.ID); err == nil && v != nil {
				t.IDs = append(t.IDs, valueText(v))
			}
		}
		t.Said = fmt.Sprintf("%d item(s), ids: %s", len(items), truncate(strings.Join(t.IDs, ", "), 400))
	case condCross:
		n, ok := number(got)
		if !ok {
			return t, fmt.Errorf("%q is not a number: %s", cmpOr(s.Path, "the result"), truncate(valueText(got), 80))
		}
		t.N, t.Said = n, fmt.Sprint(n)
	default:
		t.Said = truncate(valueText(got), 1000)
	}
	return t, nil
}

// tryMonitorSaid is the brain's try: a monitor of the user's (by name), or
// a draft spec, called once with nothing kept nor written. For a monitor
// that has looked, it also says whether a look now would find a change. A
// server not trusted taints the turn, as a call of its tool would.
func (c *core) tryMonitorSaid(ctx context.Context, name string, s monitorSpec) (map[string]any, error) {
	var m *monitor
	if name != "" && s.Server == "" {
		var err error
		if m, err = c.monitorNamed(name); err != nil {
			return nil, fmt.Errorf("no monitor %q (monitors)", name)
		}
		s = m.Spec
	}
	if s.Way == wayPoll && c.sourceWhy(s) == "" && !c.trustsServer(s.Server) {
		c.turn.taint(s.source())
	}
	t, err := c.tryLook(ctx, s)
	if err != nil {
		return map[string]any{"works": false, "error": err.Error()}, nil
	}
	out := map[string]any{"works": true, "extracts": t.Said}
	if m == nil || !m.Look.Known {
		return out, nil
	}
	switch s.Cond {
	case condNew:
		n := 0
		for _, id := range t.IDs {
			if !slices.Contains(m.Look.Seen, id) {
				n++
			}
		}
		out["new_since_last_look"] = n
	case condCross:
		hit := s.Above != nil && t.N > *s.Above || s.Below != nil && t.N < *s.Below
		out["holds"], out["held_at_last_look"] = hit, m.Look.Hit
	default:
		h := sha256.Sum256([]byte(valueText(t.Got)))
		out["changed_since_last_look"] = hex.EncodeToString(h[:]) != m.Look.Value
		out["last_value"] = m.Look.Shown
	}
	return out, nil
}

// monitorEvents are a monitor's last events, newest first.
func (c *core) monitorEvents(name string, limit int) []journalEvent {
	rows, err := c.store.db.Query(`SELECT id, kind, key, source, at, payload, external FROM event_journal
		WHERE source = ? ORDER BY id DESC LIMIT ?`, "monitor:"+name, limit)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []journalEvent
	for rows.Next() {
		if e, err := scanEvent(rows); err == nil {
			out = append(out, e)
		}
	}
	return out
}

// monitorEventCount is how many events a monitor wrote since.
func (c *core) monitorEventCount(name string, since time.Time) int {
	var n int
	c.store.db.QueryRow(`SELECT COUNT(*) FROM event_journal WHERE source = ? AND at >= ?`, "monitor:"+name, since.Unix()).Scan(&n)
	return n
}

// skillsFollowing are the active skills whose when names the monitor.
func (c *core) skillsFollowing(name string) []string {
	var out []string
	for _, sk := range c.triggeredSkills() {
		monitorKind := slices.ContainsFunc(sk.Trigger.Kinds, func(k string) bool { return strings.HasPrefix(k, "monitor.") })
		if monitorKind && strings.EqualFold(sk.Trigger.Filters["key"], name) {
			out = append(out, sk.Name)
		}
	}
	return out
}

// monitorSources are what a monitor may look at now: the brain's free
// reads, as server.tool, then the catalog servers' resources, as server
// and URI (monitorSpec.source).
func (c *core) monitorSources() []string {
	var out []string
	for _, t := range c.lentTools() {
		if c.sourceWhy(monitorSpec{Server: t.server, Tool: t.tool}) == "" {
			out = append(out, t.server+"."+t.tool)
		}
	}
	var res []string
	for name := range c.conf().Servers {
		if u := c.pool.get(name); u != nil {
			for _, uri := range u.resourceURIs() {
				res = append(res, monitorSpec{Server: name, Resource: uri}.source())
			}
		}
	}
	sort.Strings(res)
	return append(out, res...)
}

// sourceOf reads a source as monitorSources gives it into s.
func sourceOf(src string, s *monitorSpec) bool {
	src = strings.TrimSpace(src)
	if server, uri, ok := strings.Cut(src, " "); ok {
		s.Server, s.Resource = server, strings.TrimSpace(uri)
		return server != "" && s.Resource != ""
	}
	server, tool, ok := strings.Cut(src, ".")
	s.Server, s.Tool = server, tool
	return ok
}
