package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The source outil: eriphe-core itself calls a catalog tool, read-only
// only, with fixed arguments, at an interval, outside any task and without
// a model, and tests a deterministic condition on its result:
//
//   - changed: the value at path (or the whole result) is not the last one;
//   - compare: the value at path compared to a constant (eq, ne, gt, ge, lt,
//     le, contains);
//   - status: the call fails or the tool answers an error.
//
// It wakes the brain only on a transition: the condition becomes true, or
// (compare, status) false again, a recovery. A transition is confirmed by a
// second call half a minute later before it fires, so a flap wakes nobody.
// What the tool answers is external content: the excerpt kept with the run
// taints the turn it wakes.
//
// The tool must be read-only by its own annotation on a server believed for
// it, lent to the brain, and named by no rule of the user's policy (a free
// read, freeRead): checked when the watch is written and before every call.

const (
	condChanged = "changed"
	condCompare = "compare"
	condStatus  = "status"

	pollEveryDefault = 5
	pollEveryMax     = 24 * 60
	pollConfirm      = 30 * time.Second
	pollTimeout      = 30 * time.Second
	pollArgsMax      = 4 << 10
	pollExcerptMax   = 1000

	causePoll      = "poll"      // the condition became true
	causeRecovered = "recovered" // it became false again
	causeChanged   = "changed"
)

var condOps = []string{"eq", "ne", "gt", "ge", "lt", "le", "contains"}

// watchCond is the deterministic test of an outil watch.
type watchCond struct {
	Kind  string `json:"kind"`
	Path  string `json:"path,omitempty"` // data.items.0.state; the whole result if empty
	Op    string `json:"op,omitempty"`
	Value string `json:"value,omitempty"`
}

// pollState is what an outil watch's calls saw, kept between ticks.
type pollState struct {
	Known     bool   `json:"known,omitempty"` // a first result was seen
	Hit       bool   `json:"hit,omitempty"`   // the condition held at the last confirmed look
	Value     string `json:"value,omitempty"` // changed: the hash of the last confirmed value
	Pending   bool   `json:"pending,omitempty"`
	PendHit   bool   `json:"pend_hit,omitempty"`
	PendValue string `json:"pend_value,omitempty"`
	Error     string `json:"error,omitempty"`
	Checked   int64  `json:"checked,omitempty"`
}

// checkPoll validates an outil watch's shape (the catalog is checked by
// checkPollTool, which needs the core).
func (w *watch) checkPoll() error {
	s := w.Spec
	w.Spec = watchSpec{Server: strings.TrimSpace(s.Server), Tool: strings.TrimSpace(s.Tool), Args: s.Args, Every: s.Every,
		Cond: s.Cond, Judge: strings.TrimSpace(s.Judge)}
	if w.Spec.Server == "" || w.Spec.Tool == "" {
		return errors.New("an outil watch names a catalog server and one of its read-only tools")
	}
	if w.Spec.Every == 0 {
		w.Spec.Every = pollEveryDefault
	}
	if w.Spec.Every < 1 || w.Spec.Every > pollEveryMax {
		return fmt.Errorf("every is 1 to %d minutes", pollEveryMax)
	}
	if raw, _ := json.Marshal(w.Spec.Args); len(raw) > pollArgsMax {
		return fmt.Errorf("args are at most %d bytes", pollArgsMax)
	}
	c := w.Spec.Cond
	if c == nil {
		c = &watchCond{Kind: condChanged}
		w.Spec.Cond = c
	}
	switch c.Kind {
	case condChanged, condStatus:
		c.Op, c.Value = "", ""
	case condCompare:
		if !containsStr(condOps, c.Op) {
			return fmt.Errorf("op is %s", strings.Join(condOps, ", "))
		}
	default:
		return errors.New("cond.kind is changed, compare or status")
	}
	if len(c.Path) > 200 || len(c.Value) > 200 || len(w.Spec.Judge) > 300 {
		return errors.New("cond.path and cond.value are at most 200 characters, judge 300")
	}
	return nil
}

// checkPollTool tells whether eriphe-core may call an outil watch's tool
// on its own: a free read of the brain's.
func (c *core) checkPollTool(w *watch) error {
	server, tool := w.Spec.Server, w.Spec.Tool
	if _, ok := c.conf().Servers[server]; !ok || c.pool.get(server) == nil {
		return fmt.Errorf("%s is not a server of the catalog", server)
	}
	if !grantAllows(c.brainGrant(), server, tool) {
		return fmt.Errorf("%s.%s is not one of the brain's tools", server, tool)
	}
	name := claudeToolName(server, tool)
	if !c.pool.readOnly(name) {
		return fmt.Errorf("%s.%s is not read-only (by its own annotation, on a server believed for it): a watch only reads", server, tool)
	}
	if !c.freeRead(name) {
		return fmt.Errorf("a rule of the policy names %s.%s: a watch calls free reads only", server, tool)
	}
	return nil
}

// tickPoll calls an outil watch's tool when its time has come.
func (c *core) tickPoll(ctx context.Context, w *watch, now time.Time) {
	if w.NextAt.After(now) {
		return
	}
	st := w.Poll
	next := now.Add(time.Duration(w.Spec.Every) * time.Minute)
	defer func() {
		st.Checked = now.Unix()
		raw, _ := json.Marshal(st)
		c.store.db.Exec(`UPDATE watches SET poll = ?, next_at = ? WHERE id = ?`, string(raw), next.Unix(), w.ID)
	}()
	if err := c.checkPollTool(w); err != nil {
		st.Error = err.Error() // its access changed: it waits, and Settings says why
		return
	}
	hit, value, excerpt, err := c.pollOnce(ctx, w)
	if err != nil {
		st.Error = truncate(err.Error(), 300)
		return
	}
	st.Error = ""
	cause := ""
	switch {
	case w.Spec.Cond.Kind == condChanged && !st.Known:
		st.Known, st.Value = true, value // the baseline: nothing changed yet
		return
	case !st.Pending && !transition(w, st, hit, value):
		return
	case !st.Pending:
		// A transition: confirm it before waking anyone.
		st.Pending, st.PendHit, st.PendValue = true, hit, value
		next = now.Add(pollConfirm)
		return
	case hit != st.PendHit || value != st.PendValue:
		st.Pending = false // it flapped: nothing happened
		log.Printf("watch %d: a transition did not hold on its second look", w.ID)
		return
	}
	st.Pending = false
	switch {
	case w.Spec.Cond.Kind == condChanged:
		cause = causeChanged
	case hit:
		cause = causePoll
	default:
		cause = causeRecovered
	}
	st.Known, st.Hit, st.Value = true, hit, value
	if hit || cause == causeChanged {
		if ok, note := judgeAllows(ctx, w.Spec.Judge, excerpt); !ok {
			log.Printf("watch %d: the judge held the wake back", w.ID)
			return
		} else if note != "" {
			log.Printf("watch %d: %s", w.ID, note)
		}
	}
	c.fireWatch(w, &watchRun{Cause: cause, Detail: excerpt, External: true})
}

// transition tells whether a look differs from the last confirmed state.
// Before any, the condition is taken as false: true at the first look wakes.
func transition(w *watch, st pollState, hit bool, value string) bool {
	if w.Spec.Cond.Kind == condChanged {
		return value != st.Value
	}
	return hit != st.Hit
}

// pollOnce calls the tool and tests the condition: whether it holds, the
// value it saw (hashed, for changed) and an excerpt of the result.
func (c *core) pollOnce(ctx context.Context, w *watch) (hit bool, value, excerpt string, err error) {
	u := c.pool.get(w.Spec.Server)
	if u == nil {
		return false, "", "", errors.New("server gone")
	}
	args, _ := json.Marshal(w.Spec.Args)
	if w.Spec.Args == nil {
		args = nil
	}
	ctx, cancel := context.WithTimeout(ctx, pollTimeout)
	defer cancel()
	res, callErr := u.call(ctx, w.Spec.Tool, args)
	failed := callErr != nil || res == nil || res.IsError
	if w.Spec.Cond.Kind == condStatus {
		if failed {
			excerpt = "error"
			if callErr != nil {
				excerpt = callErr.Error()
			} else if res != nil {
				excerpt = resultText(res)
			}
			return true, "", truncate(excerpt, pollExcerptMax), nil
		}
		return false, "", truncate(resultText(res), pollExcerptMax), nil
	}
	if callErr != nil {
		return false, "", "", callErr
	}
	if failed {
		return false, "", "", errors.New("the tool answered an error")
	}
	got, ok := atPath(resultValue(res), w.Spec.Cond.Path)
	if !ok {
		return false, "", "", fmt.Errorf("nothing at %q in the result", w.Spec.Cond.Path)
	}
	text := valueText(got)
	excerpt = truncate(text, pollExcerptMax)
	if w.Spec.Cond.Path != "" {
		excerpt = w.Spec.Cond.Path + " = " + excerpt
	}
	if w.Spec.Cond.Kind == condChanged {
		h := sha256.Sum256([]byte(text))
		return false, hex.EncodeToString(h[:]), excerpt, nil
	}
	return compareValue(text, w.Spec.Cond.Op, w.Spec.Cond.Value), "", excerpt, nil
}

// resultValue is a tool's result as data: its structured content, else its
// text read as JSON, else the text itself.
func resultValue(res *mcp.CallToolResult) any {
	if res.StructuredContent != nil {
		raw, _ := json.Marshal(res.StructuredContent)
		var v any
		if json.Unmarshal(raw, &v) == nil {
			return v
		}
	}
	text := resultText(res)
	var v any
	if json.Unmarshal([]byte(text), &v) == nil {
		return v
	}
	return text
}

// atPath walks a dotted path (a.b.0.c, $ or empty for the whole) in JSON data.
func atPath(v any, path string) (any, bool) {
	path = strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(path), "$"), ".")
	if path == "" {
		return v, true
	}
	for _, key := range strings.Split(strings.ReplaceAll(strings.ReplaceAll(path, "[", "."), "]", ""), ".") {
		switch x := v.(type) {
		case map[string]any:
			got, ok := x[key]
			if !ok {
				return nil, false
			}
			v = got
		case []any:
			i, err := strconv.Atoi(key)
			if err != nil || i < 0 || i >= len(x) {
				return nil, false
			}
			v = x[i]
		default:
			return nil, false
		}
	}
	return v, true
}

func valueText(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	raw, _ := json.Marshal(v)
	return string(raw)
}

// compareValue compares as numbers when both are, as text otherwise.
func compareValue(got, op, want string) bool {
	if op == "contains" {
		return strings.Contains(got, want)
	}
	a, errA := strconv.ParseFloat(strings.TrimSpace(got), 64)
	b, errB := strconv.ParseFloat(strings.TrimSpace(want), 64)
	cmp := strings.Compare(got, want)
	if errA == nil && errB == nil {
		switch {
		case a < b:
			cmp = -1
		case a > b:
			cmp = 1
		default:
			cmp = 0
		}
	}
	switch op {
	case "eq":
		return cmp == 0
	case "ne":
		return cmp != 0
	case "gt":
		return cmp > 0
	case "ge":
		return cmp >= 0
	case "lt":
		return cmp < 0
	case "le":
		return cmp <= 0
	}
	return false
}
