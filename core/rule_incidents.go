package main

import (
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"
)

// Incidents (#244 step 8): an outil rule keeps its incidents, deduplicated
// by code: one opens when its condition becomes true (confirmed on a
// second look), and closes when it recovers; a true condition seen again
// while one is open opens nothing. They are facts owned by "rules", written
// by code from the table, closed values only, so that a condition, a fait
// rule, the brain and the assistants lent them read them:
//
//	rules.r<id>.incident      open or closed (unknown before its first look,
//	                          or while its look fails with none open)
//	rules.r<id>.incidents_7d  how many opened in the last 7 days
//	rules.r<id>.since         when the open one opened, or the last closed
//
// The GetOut case is then: an outil rule on Grafana every 5 minutes wakes
// the brain, which notifies and starts an investigation; another rule may
// follow rules.r<id>.incident.

const (
	ownerRules     = "rules"
	incidentOpen   = "open"
	incidentClosed = "closed"
	incidentWindow = 7 * 24 * time.Hour
)

const ruleIncidentsSchema = `CREATE TABLE IF NOT EXISTS rule_incidents (
  id INTEGER PRIMARY KEY AUTOINCREMENT, rule_id INTEGER NOT NULL, opened INTEGER NOT NULL, closed INTEGER);
CREATE INDEX IF NOT EXISTS rule_incidents_rule ON rule_incidents (rule_id, opened);`

func init() {
	extraSchema = append(extraSchema, ruleIncidentsSchema)
	reservedOwners = append(reservedOwners, ownerRules)
}

// openIncident opens a rule's incident, unless one is open.
func (c *core) openIncident(w *rule, now time.Time) {
	res, err := c.store.db.Exec(`INSERT INTO rule_incidents (rule_id, opened)
		SELECT ?, ? WHERE NOT EXISTS (SELECT 1 FROM rule_incidents WHERE rule_id = ? AND closed IS NULL)`, w.ID, now.Unix(), w.ID)
	if err != nil {
		log.Printf("rule %d: incident: %v", w.ID, err)
		return
	}
	if n, _ := res.RowsAffected(); n > 0 {
		log.Printf("rule %d: incident opened", w.ID)
	}
}

// closeIncident closes its open incident, if any.
func (c *core) closeIncident(w *rule, now time.Time) {
	if res, err := c.store.db.Exec(`UPDATE rule_incidents SET closed = ? WHERE rule_id = ? AND closed IS NULL`, now.Unix(), w.ID); err == nil {
		if n, _ := res.RowsAffected(); n > 0 {
			log.Printf("rule %d: incident closed", w.ID)
		}
	}
}

// incidentFields are a rule's fields among the rules' facts.
var incidentFields = map[string]string{"incident": stateChoice, "incidents_7d": stateNumber, "since": stateDate}

func ruleFactID(id int64) string { return "r" + strconv.FormatInt(id, 10) }

// ruleFacts are the rules' facts now: each active outil rule's incidents,
// once it has looked.
func (c *core) ruleFacts(now time.Time) []fact {
	rs, _ := c.rules()
	var out []fact
	for _, w := range rs {
		if w.Source != sourcePoll || w.State != watchActive {
			continue
		}
		var open, lastClosed, count int64
		c.store.db.QueryRow(`SELECT COALESCE(MAX(CASE WHEN closed IS NULL THEN opened END), 0), COALESCE(MAX(closed), 0),
			COALESCE(SUM(opened >= ?), 0) FROM rule_incidents WHERE rule_id = ?`, now.Add(-incidentWindow).Unix(), w.ID).Scan(&open, &lastClosed, &count)
		if open == 0 && (w.Poll.Checked == 0 || w.Poll.Error != "") {
			continue // never looked, or its last look failed: unknown
		}
		set := time.Unix(w.Poll.Checked, 0)
		if w.Poll.Checked == 0 || set.After(now) {
			set = now
		}
		f := func(field string, v any) fact {
			return fact{Owner: ownerRules, Field: ruleFactID(w.ID) + "." + field, Value: v, Set: set, Expires: now.Add(time.Hour),
				Writer: "rule:" + strconv.FormatInt(w.ID, 10), Closed: true, Detail: map[string]any{"rule": w.Name}}
		}
		state, since := incidentClosed, lastClosed
		if open > 0 {
			state, since = incidentOpen, open
		}
		out = append(out, f("incident", state), f("incidents_7d", float64(count)))
		if since > 0 {
			out = append(out, f("since", time.Unix(since, 0).In(now.Location()).Format("2006-01-02T15:04")))
		}
	}
	return out
}

// checkRuleFactPath checks a path among the rules' facts: r<id> of an outil
// rule, and one of its fields.
func (c *core) checkRuleFactPath(path, field string, values []string) error {
	id, sub, ok := strings.Cut(field, ".")
	n, err := strconv.ParseInt(strings.TrimPrefix(id, "r"), 10, 64)
	if !ok || err != nil || !strings.HasPrefix(id, "r") {
		return fmt.Errorf("%q: the rules' facts are rules.r<id>.incident, .incidents_7d or .since (rules gives the ids)", path)
	}
	w, err := c.rule(n)
	if err != nil || w.Source != sourcePoll {
		return fmt.Errorf("%q: rule %d is not an outil rule: only those keep incidents", path, n)
	}
	if _, ok := incidentFields[sub]; !ok {
		return fmt.Errorf("%q: a rule's facts are incident (open, closed), incidents_7d and since", path)
	}
	if sub == "incident" {
		for _, v := range values {
			if v != incidentOpen && v != incidentClosed {
				return fmt.Errorf("%s: %q is not open or closed", path, v)
			}
		}
	}
	return nil
}
