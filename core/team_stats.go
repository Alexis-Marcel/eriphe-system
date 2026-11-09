package main

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

// The team's measures (#244 step 6), per assistant and per rule, over the
// last statsDays, from what eriphe-core already keeps (rule_runs, events,
// messages, notifications, cards): no service of their own. They say what
// each costs and gives, and where the user corrects it; many cards during
// a rule's wakes mean a standing approval is missing.

// assistantTasksSchema keeps which tasks an assistant ran: its cost is
// theirs. Tasks started before it are not counted.
const assistantTasksSchema = `CREATE TABLE IF NOT EXISTS assistant_tasks (
  task_id TEXT PRIMARY KEY, name TEXT NOT NULL, started INTEGER NOT NULL);
CREATE INDEX IF NOT EXISTS assistant_tasks_name ON assistant_tasks (name);`

func init() { extraSchema = append(extraSchema, assistantTasksSchema) }

// manyCards is how many cards during a rule's wakes, in the window, make
// its stats say a standing approval may be missing.
const manyCards = 3

func (c *core) noteAssistantTask(name, taskID string) {
	c.store.db.Exec(`INSERT OR IGNORE INTO assistant_tasks (task_id, name, started) VALUES (?, ?, ?)`, taskID, name, time.Now().Unix())
}

// usage is what turns cost.
type usage struct {
	Turns   int     `json:"turns"`
	Tokens  int64   `json:"tokens,omitempty"`
	CostUSD float64 `json:"cost_usd,omitempty"`
	Costed  bool    `json:"-"` // a turn said its cost
}

// turnsUsage sums the turns of tasks (an IN list's ids) since a time, or
// between two when to is not zero.
func (s *Store) turnsUsage(where string, args ...any) usage {
	var u usage
	var costed int
	s.db.QueryRow(`SELECT COUNT(*), COALESCE(SUM(COALESCE(json_extract(data, '$.usage.input_tokens'), 0)
		+ COALESCE(json_extract(data, '$.usage.output_tokens'), 0) + COALESCE(json_extract(data, '$.usage.cache_write_tokens'), 0)), 0),
		COALESCE(SUM(COALESCE(json_extract(data, '$.cost_usd'), 0)), 0), COUNT(json_extract(data, '$.cost_usd'))
		FROM events WHERE type = 'turn_end' AND `+where, args...).Scan(&u.Turns, &u.Tokens, &u.CostUSD, &costed)
	u.Costed = costed > 0
	return u
}

type assistantStats struct {
	Wakes       int    `json:"wakes"`         // by its rules
	Usage       usage  `json:"usage"`         // its turns
	Reports     int    `json:"reports"`       // to the brain
	Notified    int    `json:"notified"`      // the user, itself
	Corrections int    `json:"corrections"`   // its notifications the user found not useful, or not now
	Facts       int    `json:"facts_written"` // values it wrote
	Failures    int    `json:"failures"`      // starts that failed in a row, now
	LastError   string `json:"last_error,omitempty"`
}

func (c *core) assistantStatsOf(a Assistant, since time.Time) assistantStats {
	var s assistantStats
	db := c.store.db
	db.QueryRow(`SELECT COUNT(*) FROM rule_runs r JOIN rules w ON w.id = r.rule_id WHERE w.act = ? AND r.fired >= ? AND r.outcome != ?`,
		actAssistantPrefix+a.Name, since.Unix(), runSkipped).Scan(&s.Wakes)
	ids := c.assistantTaskIDs(a.Name)
	if len(ids) > 0 {
		in := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
		args := []any{since.Unix()}
		for _, id := range ids {
			args = append(args, id)
		}
		s.Usage = c.store.turnsUsage(`time >= ? AND task_id IN (`+in+`)`, args...)
	}
	db.QueryRow(`SELECT COUNT(*) FROM messages WHERE role = ? AND time >= ? AND text LIKE ?`,
		roleEvent, since.Unix(), fmt.Sprintf("Rapport de l'assistant %s : %%", a.displayName())).Scan(&s.Reports)
	db.QueryRow(`SELECT COUNT(*), COALESCE(SUM(feedback IN ('down', 'later')), 0) FROM notifications WHERE source = ? AND created >= ? AND why LIKE ?`,
		notifyFromAssistant, since.Unix(), a.displayName()+" : %").Scan(&s.Notified, &s.Corrections)
	db.QueryRow(`SELECT COUNT(*) FROM facts_log WHERE owner = ? AND writer = ? AND set_at >= ?`, a.Name, a.Name, since.Unix()).Scan(&s.Facts)
	st := c.assistantStatus(a.Name)
	s.Failures, s.LastError = st.Failures, st.LastError
	return s
}

// assistantTaskIDs are the tasks it ran, its current one among them.
func (c *core) assistantTaskIDs(name string) []string {
	var ids []string
	rows, err := c.store.db.Query(`SELECT task_id FROM assistant_tasks WHERE name = ?`, name)
	if err == nil {
		for rows.Next() {
			var id string
			if rows.Scan(&id) == nil {
				ids = append(ids, id)
			}
		}
		rows.Close()
	}
	if t := c.assistantStatus(name).Task; t != "" && !slices.Contains(ids, t) {
		ids = append(ids, t)
	}
	return ids
}

type ruleStats struct {
	Fires       int       `json:"fires"`
	Skipped     int       `json:"skipped,omitempty"` // its condition did not hold
	Missed      int       `json:"missed,omitempty"`  // too late, or dropped
	Notified    int       `json:"notified,omitempty"`
	Acted       int       `json:"acted,omitempty"` // its standing approval used
	Cards       int       `json:"cards,omitempty"` // cards made during its wakes
	Corrections int       `json:"corrections,omitempty"`
	Usage       usage     `json:"usage"` // the brain's turns during its wakes
	Last        time.Time `json:"last,omitzero"`
	Hint        string    `json:"hint,omitempty"`
}

func (c *core) ruleStatsOf(w *rule, since time.Time) ruleStats {
	var s ruleStats
	db := c.store.db
	var last int64
	db.QueryRow(`SELECT COUNT(*), COALESCE(SUM(outcome = ?), 0), COALESCE(SUM(outcome IN (?, ?)), 0), COALESCE(SUM(notification IS NOT NULL), 0),
		COALESCE(SUM(outcome = ?), 0), COALESCE(MAX(fired), 0) FROM rule_runs WHERE rule_id = ? AND fired >= ?`,
		runSkipped, runMissed, runDropped, runActed, w.ID, since.Unix()).Scan(&s.Fires, &s.Skipped, &s.Missed, &s.Notified, &s.Acted, &last)
	if last > 0 {
		s.Last = time.Unix(last, 0)
	}
	db.QueryRow(`SELECT COUNT(*) FROM notifications n JOIN rule_runs r ON r.notification = n.id
		WHERE r.rule_id = ? AND r.fired >= ? AND n.feedback IN ('down', 'later')`, w.ID, since.Unix()).Scan(&s.Corrections)
	// What happened during its wakes of the brain: from the wake to its end
	// (an hour at most, when it did not end).
	const during = `FROM rule_runs r WHERE r.rule_id = ? AND r.fired >= ? AND r.woke IS NOT NULL`
	db.QueryRow(`SELECT COUNT(*) FROM convo_cards x WHERE EXISTS (SELECT 1 `+during+`
		AND x.created >= r.woke AND x.created <= COALESCE(r.ended, r.woke + 3600))`, w.ID, since.Unix()).Scan(&s.Cards)
	if w.Act == "" {
		s.Usage = c.store.turnsUsage(`task_id IN (SELECT id FROM tasks WHERE kind = ?) AND EXISTS (SELECT 1 `+during+`
			AND events.time >= r.woke AND events.time <= COALESCE(r.ended, r.woke + 3600))`, assistantRoleBrain, w.ID, since.Unix())
	}
	if s.Cards >= manyCards {
		s.Hint = "many cards during its wakes: a standing approval may be missing"
	}
	return s
}

// The team: who reads an assistant's facts.

// factReaders are who reads an owner's facts: the brain (its summary
// fields, and always through facts), the rules that name them, and the
// assistants lent them.
type factReaders struct {
	Summary    bool     // in the brain's [assistants] line
	Rules      []string // names of the rules that fire on, or test, its facts
	Assistants []string // display names of those lent them
}

func (c *core) factReadersOf(owner string) factReaders {
	var r factReaders
	m := c.conf()
	if a, ok := m.Assistants[owner]; ok {
		r.Summary = slices.ContainsFunc(a.State, func(f StateField) bool { return f.Summary })
	}
	rs, _ := c.rules()
	for _, w := range rs {
		if slices.ContainsFunc(ruleFactPaths(w), func(p string) bool { return strings.HasPrefix(p, owner+".") }) {
			r.Rules = append(r.Rules, w.Name)
		}
	}
	for _, n := range sortedKeys(m.Assistants) {
		if a := m.Assistants[n]; n != owner && slices.Contains(a.Facts, owner) {
			r.Assistants = append(r.Assistants, a.displayName())
		}
	}
	return r
}

// wakeRules are the rules that wake an assistant.
func (c *core) wakeRules(name string) []*rule {
	rs, _ := c.rules()
	var out []*rule
	for _, w := range rs {
		if w.ThenAssistant() == name {
			out = append(out, w)
		}
	}
	return out
}

func statsSince(now time.Time) time.Time { return now.Add(-statsDays * 24 * time.Hour) }
