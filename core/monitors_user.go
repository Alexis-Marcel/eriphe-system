package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
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
	if why := c.toolWhy(s); why != "" {
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
	_, err := c.store.db.Exec(`DELETE FROM monitors WHERE name = ?`, name)
	return err
}

// tryMonitor calls a spec's tool once and says what its path extracts,
// with the same checks as a look; nothing is kept.
func (c *core) tryMonitor(ctx context.Context, s monitorSpec) (string, error) {
	if err := s.check(); err != nil {
		return "", err
	}
	if why := c.toolWhy(s); why != "" {
		return "", errors.New(why)
	}
	got, err := c.callMonitor(ctx, s)
	if err != nil {
		return "", err
	}
	switch s.Cond {
	case condNew:
		items, ok := got.([]any)
		if !ok {
			return "", fmt.Errorf("%q is not a list", cmpOr(s.Path, "the result"))
		}
		var ids []string
		for _, it := range items {
			if v, err := extract(it, s.ID); err == nil && v != nil {
				ids = append(ids, valueText(v))
			}
		}
		return fmt.Sprintf("%d item(s), ids: %s", len(items), truncate(strings.Join(ids, ", "), 400)), nil
	case condCross:
		n, ok := number(got)
		if !ok {
			return "", fmt.Errorf("%q is not a number: %s", cmpOr(s.Path, "the result"), truncate(valueText(got), 80))
		}
		return fmt.Sprint(n), nil
	}
	return truncate(valueText(got), 1000), nil
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
		if strings.HasPrefix(sk.Trigger.Kind, "monitor.") && strings.EqualFold(sk.Trigger.Filters["key"], name) {
			out = append(out, sk.Name)
		}
	}
	return out
}

// monitorTools are the tools a monitor may call now: the brain's free
// reads, as server.tool.
func (c *core) monitorTools() []string {
	var out []string
	for _, t := range c.lentTools() {
		if c.toolWhy(monitorSpec{Server: t.server, Tool: t.tool}) == "" {
			out = append(out, t.server+"."+t.tool)
		}
	}
	return out
}
