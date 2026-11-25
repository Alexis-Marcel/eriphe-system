package main

import (
	"encoding/json"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// The turn report: how long the brain's turns take, what it does in them
// and how tasks are launched, over the last days, read from the tasks'
// events (no service of its own, nothing written). It measures the brain
// before and after a change to it (README: Usage and measures).

const (
	reportDays    = 7  // the window by default
	reportMaxDays = 90 // and at most
	// slowTurn: a turn longer than this keeps the user waiting.
	slowTurn = 30 * time.Second
)

// spread is a measure over a set of values: how many were measured, and
// their percentiles by nearest rank (the smallest value with at least p %
// of the values at or below it).
type spread struct {
	N      int   `json:"n"`
	Median int64 `json:"median"`
	P75    int64 `json:"p75"`
	P90    int64 `json:"p90"`
	Max    int64 `json:"max"`
}

func spreadOf(xs []int64) spread {
	if len(xs) == 0 {
		return spread{}
	}
	s := append([]int64(nil), xs...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	at := func(p float64) int64 { return s[max(int(math.Ceil(p*float64(len(s))))-1, 0)] }
	return spread{N: len(s), Median: at(0.5), P75: at(0.75), P90: at(0.9), Max: s[len(s)-1]}
}

// reportTurn is one of the brain's turns: its tool calls by bare name.
type reportTurn struct {
	Task          string         `json:"task"`
	Seq           int            `json:"seq"` // its turn_end's
	At            time.Time      `json:"at"`
	DurationMS    *int64         `json:"duration_ms,omitempty"`
	OutputTokens  *int64         `json:"output_tokens,omitempty"`
	ContextTokens *int64         `json:"context_tokens,omitempty"`
	Tools         map[string]int `json:"tools"`
	Launches      int            `json:"launches"` // its run_task calls
}

type brainReport struct {
	Turns         int            `json:"turns"`
	DurationMS    spread         `json:"duration_ms"`
	SlowTurns     int            `json:"slow_turns"` // over 30 s
	SlowShare     float64        `json:"slow_share"` // of the brain's time, in those
	OutputTokens  spread         `json:"output_tokens"`
	ContextTokens spread         `json:"context_tokens"`
	Tools         map[string]int `json:"tools"`
}

type launchReport struct {
	Turns            int    `json:"turns"` // brain turns that called run_task
	DurationMS       spread `json:"duration_ms"`
	OutputTokens     spread `json:"output_tokens"`
	RunTaskCalls     int    `json:"run_task_calls"`
	BrainPromptChars spread `json:"brain_prompt_chars"`
	SkillRuns        int    `json:"skill_runs"`
	SkillPromptChars spread `json:"skill_prompt_chars"`
	OtherTasks       int    `json:"other_tasks"` // worker tasks not run by a skill
}

type readReport struct {
	Tasks        int    `json:"tasks"`         // worker tasks started in the window
	TasksReading int    `json:"tasks_reading"` // of which called node_read or node_find
	Calls        spread `json:"calls"`         // those calls per task
	ByOthers     spread `json:"by_others"`     // node_read calls per task on a node other than its own
}

type turnReport struct {
	Days     int          `json:"days"`
	Since    time.Time    `json:"since"`
	Brain    brainReport  `json:"brain"`
	Launches launchReport `json:"launches"`
	Reads    readReport   `json:"reads"`
	// TurnList: every turn of the window, the longest first.
	TurnList []reportTurn `json:"turn_list"`
}

// bareTool is a tool's name without its server: mcp__eriphe-core__node_write
// is node_write; Bash stays Bash.
func bareTool(name string) string {
	if p := strings.SplitN(name, "__", 3); len(p) == 3 && p[0] == "mcp" {
		return p[2]
	}
	return name
}

// numAt reads a number at a path of a turn_end's data.
func numAt(d map[string]any, path ...string) (int64, bool) {
	var v any = d
	for _, k := range path {
		m, ok := v.(map[string]any)
		if !ok {
			return 0, false
		}
		v = m[k]
	}
	f, ok := v.(float64)
	return int64(f), ok
}

// turnContext is a turn's context: Claude Code's context_tokens, else its
// input, cache reads and writes (as lastSessionTurn).
func turnContext(d map[string]any) (int64, bool) {
	if n, ok := numAt(d, "usage", "context_tokens"); ok {
		return n, true
	}
	var sum int64
	var found bool
	for _, k := range []string{"input_tokens", "cache_read_tokens", "cache_write_tokens"} {
		if n, ok := numAt(d, "usage", k); ok {
			sum, found = sum+n, true
		}
	}
	return sum, found
}

// turnReport computes the report of the turns that ended since.
func (s *Store) turnReport(since time.Time, days int) (turnReport, error) {
	rep := turnReport{Days: days, Since: since, TurnList: []reportTurn{}}
	rep.Brain.Tools = map[string]int{}
	turns, prompts, err := s.brainTurns(since)
	if err != nil {
		return rep, err
	}
	var dur, out, ctx, ldur, lout []int64
	var total, slow int64
	for _, t := range turns {
		rep.Brain.Turns++
		for k, n := range t.Tools {
			rep.Brain.Tools[k] += n
		}
		if t.DurationMS != nil {
			d := *t.DurationMS
			dur, total = append(dur, d), total+d
			if d > slowTurn.Milliseconds() {
				rep.Brain.SlowTurns++
				slow += d
			}
		}
		if t.OutputTokens != nil {
			out = append(out, *t.OutputTokens)
		}
		if t.ContextTokens != nil {
			ctx = append(ctx, *t.ContextTokens)
		}
		if t.Launches > 0 {
			rep.Launches.Turns++
			rep.Launches.RunTaskCalls += t.Launches
			if t.DurationMS != nil {
				ldur = append(ldur, *t.DurationMS)
			}
			if t.OutputTokens != nil {
				lout = append(lout, *t.OutputTokens)
			}
		}
	}
	if total > 0 {
		rep.Brain.SlowShare = math.Round(float64(slow)/float64(total)*1000) / 1000
	}
	rep.Brain.DurationMS, rep.Brain.OutputTokens, rep.Brain.ContextTokens = spreadOf(dur), spreadOf(out), spreadOf(ctx)
	rep.Launches.DurationMS, rep.Launches.OutputTokens, rep.Launches.BrainPromptChars = spreadOf(ldur), spreadOf(lout), spreadOf(prompts)
	sort.SliceStable(turns, func(i, j int) bool { return durOf(turns[i]) > durOf(turns[j]) })
	rep.TurnList = append(rep.TurnList, turns...)
	if err := s.workerReport(since, &rep); err != nil {
		return rep, err
	}
	return rep, nil
}

func durOf(t reportTurn) int64 {
	if t.DurationMS == nil {
		return -1
	}
	return *t.DurationMS
}

// brainTurns are the brain's turns that ended since, oldest first, and the
// length of the prompts its run_task calls wrote. A turn is a brain task's
// events after its previous turn_end, up to its own: its tool calls may be
// older than since.
func (s *Store) brainTurns(since time.Time) ([]reportTurn, []int64, error) {
	rows, err := s.db.Query(`SELECT t.id, COALESCE((SELECT MAX(seq) FROM events
		WHERE task_id = t.id AND type = 'turn_end' AND time < ?), 0)
		FROM tasks t WHERE t.kind = 'brain' ORDER BY t.created, t.id`, since.Unix())
	if err != nil {
		return nil, nil, err
	}
	type cut struct {
		id  string
		seq int
	}
	var cuts []cut
	for rows.Next() {
		var c cut
		if err := rows.Scan(&c.id, &c.seq); err != nil {
			rows.Close()
			return nil, nil, err
		}
		cuts = append(cuts, c)
	}
	rows.Close()
	var turns []reportTurn
	var prompts []int64
	for _, c := range cuts {
		ev, err := s.db.Query(`SELECT seq, time, type, data FROM events WHERE task_id = ? AND seq > ?
			AND type IN ('tool_use', 'turn_end') ORDER BY seq`, c.id, c.seq)
		if err != nil {
			return nil, nil, err
		}
		cur := reportTurn{Task: c.id, Tools: map[string]int{}}
		var curPrompts []int64
		for ev.Next() {
			var seq int
			var at int64
			var typ, raw string
			if err := ev.Scan(&seq, &at, &typ, &raw); err != nil {
				ev.Close()
				return nil, nil, err
			}
			var d map[string]any
			json.Unmarshal([]byte(raw), &d)
			if typ == "tool_use" {
				name, _ := d["name"].(string)
				name = bareTool(name)
				cur.Tools[name]++
				if name == "run_task" {
					cur.Launches++
					in, _ := d["input"].(map[string]any)
					p, _ := in["prompt"].(string)
					curPrompts = append(curPrompts, int64(utf8.RuneCountInString(p)))
				}
				continue
			}
			cur.Seq, cur.At = seq, time.Unix(at, 0).UTC()
			if n, ok := numAt(d, "duration_ms"); ok {
				cur.DurationMS = &n
			}
			if n, ok := numAt(d, "usage", "output_tokens"); ok {
				cur.OutputTokens = &n
			}
			if n, ok := turnContext(d); ok {
				cur.ContextTokens = &n
			}
			turns, prompts = append(turns, cur), append(prompts, curPrompts...)
			cur, curPrompts = reportTurn{Task: c.id, Tools: map[string]int{}}, nil
		}
		err = ev.Err()
		ev.Close()
		if err != nil {
			return nil, nil, err
		}
	}
	return turns, prompts, nil
}

// workerReport fills the launches by skills and the tasks' graph reads,
// from the worker tasks started since: a skill's run is in skill_runs. The
// reads are their node_read and node_find calls; by others, their
// node_read calls that name a node other than their own (task_nodes).
func (s *Store) workerReport(since time.Time, rep *turnReport) error {
	rows, err := s.db.Query(`SELECT t.id, t.prompt, EXISTS (SELECT 1 FROM skill_runs r WHERE r.task = t.id)
		FROM tasks t WHERE t.kind = 'worker' AND t.created >= ?`, since.Unix())
	if err != nil {
		return err
	}
	reads, others := map[string]int64{}, map[string]int64{}
	var skillPrompts []int64
	for rows.Next() {
		var id, prompt string
		var bySkill bool
		if err := rows.Scan(&id, &prompt, &bySkill); err != nil {
			rows.Close()
			return err
		}
		reads[id], others[id] = 0, 0
		if bySkill {
			rep.Launches.SkillRuns++
			skillPrompts = append(skillPrompts, int64(utf8.RuneCountInString(prompt)))
		} else {
			rep.Launches.OtherTasks++
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	calls, err := s.db.Query(`SELECT e.task_id, COALESCE(json_extract(e.data, '$.name'), ''),
		COALESCE(json_extract(e.data, '$.input.id'), ''), COALESCE(n.node, '')
		FROM events e JOIN tasks t ON t.id = e.task_id LEFT JOIN task_nodes n ON n.task = t.id
		WHERE t.kind = 'worker' AND t.created >= ? AND e.type = 'tool_use'`, since.Unix())
	if err != nil {
		return err
	}
	defer calls.Close()
	for calls.Next() {
		var id, name, read, own string
		if err := calls.Scan(&id, &name, &read, &own); err != nil {
			return err
		}
		switch b := bareTool(name); {
		case b == "node_read":
			reads[id]++
			if read = strings.TrimSpace(read); read != "" && read != own {
				others[id]++
			}
		case b == "node_find":
			reads[id]++
		}
	}
	if err := calls.Err(); err != nil {
		return err
	}
	ns, ob := make([]int64, 0, len(reads)), make([]int64, 0, len(others))
	for id, n := range reads {
		ns, ob = append(ns, n), append(ob, others[id])
		if n > 0 {
			rep.Reads.TasksReading++
		}
	}
	rep.Launches.SkillPromptChars = spreadOf(skillPrompts)
	rep.Reads.Tasks, rep.Reads.Calls, rep.Reads.ByOthers = len(reads), spreadOf(ns), spreadOf(ob)
	return nil
}

// handleTurnReport is GET /api/report/turns[?days=n]: the owner's alone
// (w.auth), read-only.
func (w *web) handleTurnReport(rw http.ResponseWriter, r *http.Request, _ *session) {
	days := reportDays
	if v := r.URL.Query().Get("days"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > reportMaxDays {
			writeJSON(rw, http.StatusBadRequest, map[string]string{"error": "days: a whole number from 1 to 90"})
			return
		}
		days = n
	}
	rep, err := w.c.store.turnReport(w.now().Add(-time.Duration(days)*24*time.Hour), days)
	if err != nil {
		writeJSON(rw, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	rw.Header().Set("Cache-Control", "no-store")
	writeJSON(rw, http.StatusOK, rep)
}
