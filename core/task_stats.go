package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// A few measures of how reliable the tasks are, from what eriphe-core
// already keeps (receipts, failures, the turns' usage): no service of
// their own. They say where the harness needs work, and later how much
// room an autonomous task can be given.

// statsDays is the window of the measures.
const statsDays = 30

// taskUsage is what a task's turns used, as their harness reported it.
type taskUsage struct {
	Tokens  int64   // input, output and cache writes (tokensUsed)
	CostUSD float64 // where the harness gives a cost
	Known   bool    // a turn reported its usage
}

// usageOf sums a task's usage.
func (s *Store) usageOf(id string) taskUsage {
	var u taskUsage
	var known int
	s.db.QueryRow(`SELECT COALESCE(SUM(COALESCE(json_extract(data, '$.usage.input_tokens'), 0)
		+ COALESCE(json_extract(data, '$.usage.output_tokens'), 0)
		+ COALESCE(json_extract(data, '$.usage.cache_write_tokens'), 0)), 0),
		COALESCE(SUM(COALESCE(json_extract(data, '$.cost_usd'), 0)), 0),
		COUNT(json_extract(data, '$.usage'))
		FROM events WHERE task_id = ? AND type = 'turn_end'`, id).Scan(&u.Tokens, &u.CostUSD, &known)
	u.Known = known > 0
	return u
}

// taskStats are the measures over the last statsDays.
type taskStats struct {
	Finished  int // worker tasks that ended
	Receipts  int // of which ended with a receipt
	Accepted  int // done, no failed check, no mismatch
	FirstPass int // accepted without a retry, a check sent back, a receipt asked again or a second turn
	// Unsupported: receipts that said more than eriphe-core saw (done with
	// a failed check, a change not in the diff); Unchecked: done receipts
	// that claim verifications no check covered.
	Unsupported int
	Unchecked   int
	// What the accepted tasks cost, where their harness said.
	Tokens      int64
	CostUSD     float64
	Costed      int // accepted tasks with a known usage
	Minutes     float64
	Failures    []classCount // by class, the most first
	FailedTasks int          // tasks with at least one failure
}

type classCount struct {
	Class string
	N     int
}

// Rate is a share in percent, for the template.
func (s taskStats) Rate(n, of int) int {
	if of == 0 {
		return 0
	}
	return (n*100 + of/2) / of
}

// TokensPerAccepted and CostPerAccepted are averages over the accepted
// tasks whose usage is known.
func (s taskStats) TokensPerAccepted() int64 {
	if s.Costed == 0 {
		return 0
	}
	return s.Tokens / int64(s.Costed)
}

func (s taskStats) CostPerAccepted() float64 {
	if s.Costed == 0 {
		return 0
	}
	return s.CostUSD / float64(s.Costed)
}

func (s taskStats) MinutesPerAccepted() int {
	if s.Accepted == 0 {
		return 0
	}
	return int(s.Minutes/float64(s.Accepted) + .5)
}

// money is an amount in US dollars, as lang writes it.
func money(lang string, usd float64) string {
	s := fmt.Sprintf("%.2f", usd)
	if usd > 0 && usd < 0.01 {
		s = fmt.Sprintf("%.3f", usd)
	}
	if lang == "fr" {
		return strings.Replace(s, ".", ",", 1) + " $"
	}
	return "$" + s
}

// tokenCount is a number of tokens, short: 950, 12 k, 1.2 M.
func tokenCount(lang string, n int64) string {
	var s string
	switch {
	case n >= 1_000_000:
		s = fmt.Sprintf("%.1f M", float64(n)/1e6)
	case n >= 1000:
		s = fmt.Sprintf("%d k", (n+500)/1000)
	default:
		s = fmt.Sprint(n)
	}
	if lang == "fr" {
		s = strings.Replace(s, ".", ",", 1)
	}
	return s
}

// stats reads the measures of the worker tasks that ended since since.
func (s *Store) stats(since time.Time) taskStats {
	var st taskStats
	rows, err := s.db.Query(`SELECT t.id, t.created, t.updated, COALESCE(h.receipt, ''), COALESCE(h.retries, 0),
		COALESCE(h.repairs, 0), COALESCE(h.failures, ''),
		(SELECT COUNT(*) FROM events e WHERE e.task_id = t.id AND e.type = 'turn_end'),
		(SELECT COUNT(*) FROM events e WHERE e.task_id = t.id AND e.type = 'retry')
		FROM tasks t LEFT JOIN task_harness h ON h.task_id = t.id
		WHERE t.kind = 'worker' AND t.state IN ('stopped', 'failed', 'lost') AND t.updated >= ?`, since.Unix())
	if err != nil {
		return st
	}
	type row struct {
		id                                string
		created, updated                  int64
		receipt, failures                 string
		retries, repairs, turns, sentBack int
	}
	var list []row
	for rows.Next() {
		var r row
		if rows.Scan(&r.id, &r.created, &r.updated, &r.receipt, &r.retries, &r.repairs, &r.failures, &r.turns, &r.sentBack) == nil {
			list = append(list, r)
		}
	}
	rows.Close()
	classes := map[string]int{}
	for _, r := range list {
		st.Finished++
		var failures []string
		if r.failures != "" {
			json.Unmarshal([]byte(r.failures), &failures)
		}
		if len(failures) > 0 {
			st.FailedTasks++
		}
		for _, f := range failures {
			classes[f]++
		}
		var cr *checkedReceipt
		if r.receipt != "" {
			json.Unmarshal([]byte(r.receipt), &cr)
		}
		if !cr.Given() {
			continue
		}
		st.Receipts++
		if cr.Unsupported {
			st.Unsupported++
		}
		if cr.Status == "done" && len(cr.Unchecked) > 0 {
			st.Unchecked++
		}
		if cr.Status != "done" || len(cr.failedChecks()) > 0 || cr.Unsupported {
			continue
		}
		st.Accepted++
		if r.retries == 0 && r.repairs == 0 && r.sentBack == 0 && r.turns <= 1 {
			st.FirstPass++
		}
		st.Minutes += float64(r.updated-r.created) / 60
		if u := s.usageOf(r.id); u.Known {
			st.Costed++
			st.Tokens += u.Tokens
			st.CostUSD += u.CostUSD
		}
	}
	for c, n := range classes {
		st.Failures = append(st.Failures, classCount{c, n})
	}
	sort.Slice(st.Failures, func(i, j int) bool {
		if st.Failures[i].N != st.Failures[j].N {
			return st.Failures[i].N > st.Failures[j].N
		}
		return st.Failures[i].Class < st.Failures[j].Class
	})
	return st
}
