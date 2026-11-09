package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// What a triggered skill did (skill_runs.go), for its page in Settings →
// Skills and for the brain's skill_runs: each run with its events, its
// task, the session it opened, its kept calls and what the user answered,
// whether it told the user, and its cost where the harness gave one; and
// the totals over 30 days.

const (
	skillReportDays = 30
	skillRunsShown  = 30
)

// runEventView is an event of a run, as its page shows it.
type runEventView struct {
	Kind, Key, Summary string
	External           bool
}

// runView is a run, as its page and the brain read it.
type runView struct {
	ID       int64          `json:"id"`
	At       time.Time      `json:"at"`
	Ended    time.Time      `json:"ended,omitzero"`
	State    string         `json:"state"`
	Events   []runEventView `json:"events"`
	Task     string         `json:"task,omitempty"`
	Session  int64          `json:"session,omitempty"`
	Note     string         `json:"note,omitempty"`
	Calls    int            `json:"calls,omitempty"`    // actions it proposed on its card
	Asks     []runAskView   `json:"-"`                  // each, for its page: answered one by one
	Answer   string         `json:"answer,omitempty"`   // approved, rejected or waiting
	Told     bool           `json:"told"`               // its last message became a notification
	CostUSD  float64        `json:"cost_usd,omitempty"` // where the harness gave a cost
	External bool           `json:"external"`
}

// runAskView is an action a run proposed, as its page shows it.
type runAskView struct {
	ID          int64
	Verb, Value string
	State       string
}

// skillTotals are a skill's runs over skillReportDays.
type skillTotals struct {
	Runs, Done, Silent, Failed int
	Accepted, Refused, Waiting int // its cards
	CostUSD                    float64
}

// skillReport is a skill's runs, the latest first, and its totals.
func (c *core) skillReport(name string, limit int, now time.Time) ([]runView, skillTotals) {
	var tot skillTotals
	rows, err := c.store.db.Query(`SELECT `+skillRunCols+` FROM skill_runs WHERE skill = ? AND created >= ? ORDER BY id DESC`,
		name, now.AddDate(0, 0, -skillReportDays).Unix())
	if err != nil {
		return nil, tot
	}
	var runs []*skillRun
	for rows.Next() {
		if r, err := scanSkillRun(rows); err == nil {
			runs = append(runs, r)
		}
	}
	rows.Close()
	var out []runView
	for i, r := range runs {
		v := c.runViewOf(r, i < limit)
		tot.Runs++
		switch r.State {
		case skillRunDone:
			tot.Done++
		case skillRunSilent:
			tot.Silent++
		case skillRunFailed, skillRunRefused:
			tot.Failed++
		}
		switch v.Answer {
		case startApproved:
			tot.Accepted++
		case startRejected:
			tot.Refused++
		case startProposed:
			tot.Waiting++
		}
		tot.CostUSD += v.CostUSD
		if i < limit {
			out = append(out, v)
		}
	}
	return out, tot
}

// runViewOf reads a run; full adds its events and whether it told.
func (c *core) runViewOf(r *skillRun, full bool) runView {
	v := runView{ID: r.ID, At: r.Created, Ended: r.Ended, State: r.State, Task: r.Task, Note: r.Note, External: r.External}
	c.store.db.QueryRow(`SELECT session FROM skill_runs WHERE id = ?`, r.ID).Scan(&v.Session)
	for _, a := range c.runAsks(r.ID) {
		v.Calls++
		if full {
			var lines []string
			for _, f := range a.fields() {
				lines = append(lines, f.Name+" : "+f.Value)
			}
			v.Asks = append(v.Asks, runAskView{ID: a.ID, Verb: callVerb(a.Call, "fr"), Value: truncate(strings.Join(lines, " · "), 300), State: a.State})
		}
		switch {
		case a.State == startProposed:
			v.Answer = startProposed
		case a.State == startApproved && v.Answer != startProposed:
			v.Answer = startApproved
		case v.Answer == "":
			v.Answer = startRejected
		}
	}
	if r.Task != "" {
		v.CostUSD = c.store.usageOf(r.Task).CostUSD
	}
	if !full {
		return v
	}
	for _, id := range r.Events {
		if e, ok := c.journalEvent(id); ok {
			v.Events = append(v.Events, runEventView{Kind: e.Kind, Key: e.Key, Summary: eventSummary(e), External: e.External})
		}
	}
	var n int
	c.store.db.QueryRow(`SELECT COUNT(*) FROM notifications WHERE source = ? AND ref = ? AND created >= ? AND created <= ?`,
		notifyFromSkill, "skill:"+r.Skill, r.Created.Unix(), orTime(r.Ended, time.Now()).Unix()+5).Scan(&n)
	v.Told = n > 0
	return v
}

func orTime(t, or time.Time) time.Time {
	if t.IsZero() {
		return or
	}
	return t
}

// eventSummary is an event in a few words: its subject, title or detail.
func eventSummary(e journalEvent) string {
	for _, k := range []string{"subject", "title", "summary", "detail", "value", "state"} {
		if v, ok := e.Payload[k]; ok {
			return truncate(oneLine(fmt.Sprint(v)), 120)
		}
	}
	return ""
}

// The brain's skill_runs.

type skillRunsIn struct {
	Name  string `json:"name,omitempty" jsonschema:"a skill's name; empty for every triggered skill"`
	Limit int    `json:"limit,omitempty" jsonschema:"at most this many runs per skill, 10 by default"`
}

func registerSkillRunsTool(s *mcp.Server, c *core) {
	tool(s, "skill_runs", "What the user's triggered skills did: per skill, its last runs (when, state: running, done, silent, failed or refused; the events' kinds and keys, not their text; its task, the session it opened, the actions it proposed and the user's answer, whether it told the user, its cost) and its totals over 30 days. Read it before changing a skill, or when the user asks what one did.",
		func(ctx context.Context, in skillRunsIn) (map[string]any, error) {
			return c.brainSkillRuns(in), nil
		})
}

func (c *core) brainSkillRuns(in skillRunsIn) map[string]any {
	limit := in.Limit
	if limit <= 0 || limit > skillRunsShown {
		limit = 10
	}
	now := c.notifyTime()
	out := []map[string]any{}
	for _, sk := range c.triggeredSkills() {
		if in.Name != "" && sk.Name != strings.TrimSpace(in.Name) {
			continue
		}
		runs, tot := c.skillReport(sk.Name, limit, now)
		for i := range runs {
			runs[i].Note = "" // its message may quote what it read: on its page, not here
			for j := range runs[i].Events {
				runs[i].Events[j].Summary = ""
			}
		}
		out = append(out, map[string]any{"skill": sk.Name, "trigger": sk.Trigger.Text("en"), "runs": runs, "last_30_days": tot})
	}
	return map[string]any{"skills": out, "note": "Written by eriphe-core from what it recorded. What a run said is on the skill's page (Settings → Skills), not quoted here."}
}
