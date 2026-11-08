package main

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// An assistant's views: named filters over its state, run by code, that
// it exposes as read tools (Assistant.Views). A view is either rows of a
// field, filtered (where), sorted and cut (limit, or the caller's n), with
// some of their columns; or a field's history over a window. Reading one
// costs no model, and each answers with its freshness: a late or broken
// assistant says so, and the brain may read the source tool itself. It is
// never blocked.
//
// The brain reads them, free, through two tools of its own: assistants
// (every assistant, its closed values, its views) and assistant_view.
// Another assistant will read them when it is lent them, with the links
// between assistants (#244 step 3).
//
// A view taints the brain's turn only when what it returns is not closed
// (a text, or rows with a text column) and was written by a tainted
// session: the rest is values from closed lists, which carry no words.

const (
	maxViews        = 10
	defaultViewRows = 20
	maxViewWindow   = stateKeep
	minViewWindow   = time.Hour
)

// StateView is a view of an assistant's state.
type StateView struct {
	Name  string `json:"name"`
	Field string `json:"field"` // a field of its state
	// Rows: the rows whose columns have these values, sorted by a column
	// (-column descending), at most Limit (20 by default, the caller's n
	// up to it), with these columns only (all when empty).
	Where   map[string]any `json:"where,omitempty"`
	Sort    string         `json:"sort,omitempty"`
	Limit   int            `json:"limit,omitempty"`
	Columns []string       `json:"columns,omitempty"`
	// A field's history over this window (1h to 7d), newest first; not
	// for rows.
	History string `json:"history,omitempty"`
	// What it gives, in a few words, for whoever reads it.
	About string `json:"about,omitempty"`
}

func (v StateView) limit() int {
	if v.Limit > 0 {
		return v.Limit
	}
	return defaultViewRows
}

// checkViews checks views against their state's schema.
func checkViews(views []StateView, state []StateField) error {
	if len(views) > maxViews {
		return fmt.Errorf("at most %d views", maxViews)
	}
	seen := map[string]bool{}
	for _, v := range views {
		what := "view " + v.Name
		if !stateNameRE.MatchString(v.Name) {
			return fmt.Errorf("%q: a name of lowercase letters, digits and _, starting with a letter, up to 32", v.Name)
		}
		if seen[v.Name] {
			return fmt.Errorf("%s twice", what)
		}
		seen[v.Name] = true
		if len([]rune(v.About)) > 200 {
			return fmt.Errorf("%s: about in 200 characters", what)
		}
		i := slices.IndexFunc(state, func(f StateField) bool { return f.Name == v.Field })
		if i < 0 {
			return fmt.Errorf("%s: field %q is not in its state", what, v.Field)
		}
		f := state[i]
		if v.History != "" {
			d, err := parseTTL(v.History)
			switch {
			case err != nil:
				return fmt.Errorf("%s: history %q: a window, 24h or 7d", what, v.History)
			case d < minViewWindow || d > maxViewWindow:
				return fmt.Errorf("%s: history between 1h and 7d", what)
			case f.Type == stateRows:
				return fmt.Errorf("%s: no history of rows", what)
			case len(v.Where) > 0 || v.Sort != "" || v.Limit != 0 || len(v.Columns) > 0:
				return fmt.Errorf("%s: a history takes no where, sort, limit or columns", what)
			}
			continue
		}
		if f.Type != stateRows {
			if len(v.Where) > 0 || v.Sort != "" || v.Limit != 0 || len(v.Columns) > 0 {
				return fmt.Errorf("%s: where, sort, limit and columns are for rows", what)
			}
			continue
		}
		col := func(name string) (StateField, bool) {
			j := slices.IndexFunc(f.Columns, func(c StateField) bool { return c.Name == name })
			if j < 0 {
				return StateField{}, false
			}
			return f.Columns[j], true
		}
		for k, val := range v.Where {
			c, ok := col(k)
			if !ok {
				return fmt.Errorf("%s: where: no column %q", what, k)
			}
			if _, err := c.checkValue(val, time.UTC); err != nil {
				return fmt.Errorf("%s: where: %w", what, err)
			}
		}
		if _, ok := col(strings.TrimPrefix(v.Sort, "-")); v.Sort != "" && !ok {
			return fmt.Errorf("%s: sort: no column %q", what, strings.TrimPrefix(v.Sort, "-"))
		}
		if v.Limit < 0 || v.Limit > maxStateRows {
			return fmt.Errorf("%s: limit up to %d", what, maxStateRows)
		}
		for _, name := range v.Columns {
			if _, ok := col(name); !ok {
				return fmt.Errorf("%s: columns: no column %q", what, name)
			}
		}
	}
	return nil
}

// closedView tells whether what a view returns is closed values only.
func closedView(v StateView, f StateField) bool {
	if f.Type != stateRows || len(v.Columns) == 0 {
		return f.closed()
	}
	for _, c := range f.Columns {
		if slices.Contains(v.Columns, c.Name) && !c.closed() {
			return false
		}
	}
	return true
}

// viewResult is what a view answers.
type viewResult struct {
	Assistant string     `json:"assistant"`
	View      string     `json:"view"`
	Value     any        `json:"value,omitempty"`   // rows, or the field's value; absent: unknown
	History   []histItem `json:"history,omitempty"` // a history view's, newest first
	Set       *time.Time `json:"set,omitempty"`     // when it was written
	Expires   *time.Time `json:"expires,omitempty"`
	Fresh     bool       `json:"fresh"` // known, not expired, and its assistant running
	Note      string     `json:"note,omitempty"`
	tainted   string     // why what it returns was written tainted
}

type histItem struct {
	At    time.Time `json:"at"`
	Value any       `json:"value"`
}

// runView runs one of a's views; n cuts rows further.
func (c *core) runView(a Assistant, name string, n int, now time.Time) (viewResult, error) {
	i := slices.IndexFunc(a.Views, func(v StateView) bool { return v.Name == name })
	if i < 0 {
		var names []string
		for _, v := range a.Views {
			names = append(names, v.Name)
		}
		return viewResult{}, fmt.Errorf("%s has no view %q; its views: %s", a.Name, name, orStr(strings.Join(names, ", "), "none"))
	}
	v := a.Views[i]
	f := a.State[slices.IndexFunc(a.State, func(f StateField) bool { return f.Name == v.Field })]
	out := viewResult{Assistant: a.Name, View: v.Name}
	if v.History != "" {
		window, _ := parseTTL(v.History)
		rows, err := c.store.db.Query(`SELECT value, set_at, tainted FROM assistant_state_log WHERE assistant = ? AND field = ? AND set_at >= ? ORDER BY id DESC`,
			a.Name, f.Name, now.Add(-window).Unix())
		if err != nil {
			return out, err
		}
		for rows.Next() {
			var raw, t string
			var at int64
			var val any
			if rows.Scan(&raw, &at, &t) != nil || json.Unmarshal([]byte(raw), &val) != nil {
				continue
			}
			if val != nil {
				if _, err := f.checkValue(val, now.Location()); err != nil {
					val = nil // off a schema changed since
				}
			}
			out.History = append(out.History, histItem{At: time.Unix(at, 0), Value: val})
			if t != "" && !f.closed() && out.tainted == "" {
				out.tainted = t
			}
		}
		rows.Close()
	}
	cur := c.assistantState(a, now)[slices.IndexFunc(a.State, func(x StateField) bool { return x.Name == f.Name })]
	if cur.Value != nil {
		set, exp := cur.Set, cur.Expires
		out.Set, out.Expires = &set, &exp
		if v.History == "" {
			out.Value = viewValue(v, f, cur.Value, n)
			if !closedView(v, f) {
				out.tainted = cur.Tainted
			}
		}
	}
	out.Fresh = cur.known(now)
	// Late or broken: it says so; the brain may read the source itself.
	switch st := c.assistantView(a).Status; {
	case a.Disabled:
		out.Fresh, out.Note = false, "Its assistant is turned off: this is what it knew when it stopped. Read the source tool if it matters."
	case st.LastError != "" || st.Failures > 0:
		out.Fresh, out.Note = false, "Its assistant is failing ("+truncate(st.LastError, 120)+"): this may be old. Read the source tool if it matters."
	case !out.Fresh:
		out.Note = "Unknown or expired: its assistant has not checked it lately. Read the source tool if it matters."
	}
	return out, nil
}

// viewValue is a field's value through a view: rows filtered, sorted,
// cut and with its columns; another field's as it is.
func viewValue(v StateView, f StateField, val any, n int) any {
	rows, ok := val.([]any)
	if !ok || f.Type != stateRows {
		return val
	}
	var out []map[string]any
	for _, r := range rows {
		m, _ := r.(map[string]any)
		if !matches(m, v.Where) {
			continue
		}
		out = append(out, m)
	}
	if col, desc := strings.TrimPrefix(v.Sort, "-"), strings.HasPrefix(v.Sort, "-"); col != "" {
		slices.SortStableFunc(out, func(a, b map[string]any) int {
			x := compareAny(a[col], b[col])
			if desc {
				return -x
			}
			return x
		})
	}
	limit := v.limit()
	if n > 0 && n < limit {
		limit = n
	}
	if len(out) > limit {
		out = out[:limit]
	}
	res := make([]any, 0, len(out))
	for _, m := range out {
		if len(v.Columns) > 0 {
			kept := map[string]any{}
			for _, c := range v.Columns {
				if x, ok := m[c]; ok {
					kept[c] = x
				}
			}
			m = kept
		}
		res = append(res, m)
	}
	return res
}

func matches(row map[string]any, where map[string]any) bool {
	for k, want := range where {
		got, ok := row[k]
		if !ok {
			return false
		}
		if s, isStr := want.(string); isStr {
			if g, _ := got.(string); !strings.EqualFold(g, s) {
				return false
			}
			continue
		}
		if fmt.Sprint(got) != fmt.Sprint(want) {
			return false
		}
	}
	return true
}

// compareAny orders numbers as numbers, strings (dates among them) as
// strings, and what is missing last.
func compareAny(a, b any) int {
	switch {
	case a == nil && b == nil:
		return 0
	case a == nil:
		return 1
	case b == nil:
		return -1
	}
	x, xn := a.(float64)
	y, yn := b.(float64)
	if xn && yn {
		switch {
		case x < y:
			return -1
		case x > y:
			return 1
		}
		return 0
	}
	return strings.Compare(fmt.Sprint(a), fmt.Sprint(b))
}

// The brain's tools.

type assistantViewIn struct {
	Assistant string `json:"assistant" jsonschema:"the assistant's id, as assistants lists it"`
	View      string `json:"view" jsonschema:"one of its views"`
	N         int    `json:"n,omitempty" jsonschema:"for rows: at most this many"`
}

// assistantEntry is an assistant as the brain's assistants tool lists it.
type assistantEntry struct {
	Name    string         `json:"name"`
	Display string         `json:"display_name"`
	Status  string         `json:"status"` // available, replying, stopped, noai
	Error   string         `json:"last_error,omitempty"`
	Powers  []string       `json:"powers"`
	Wakes   []string       `json:"wakes,omitempty"`
	State   map[string]any `json:"state,omitempty"` // closed fields: value, or null unknown; rows by count
	Texts   []string       `json:"text_fields,omitempty"`
	Views   []viewEntry    `json:"views,omitempty"`
}

type viewEntry struct {
	Name  string `json:"name"`
	About string `json:"about,omitempty"`
	Rows  bool   `json:"rows,omitempty"` // takes n
}

func registerAssistantTools(s *mcp.Server, c *core) {
	tool(s, "assistants", "Your assistants (Settings → Assistants): each one's status, powers and wake-ups, what it knows now in closed values (texts by their field's name only), and its views, which assistant_view reads. Free, no model, never tainting.",
		func(ctx context.Context, _ struct{}) (map[string]any, error) {
			return map[string]any{"assistants": c.assistantEntries(c.notifyTime())}, nil
		})
	tool(s, "assistant_view", "Read one of an assistant's views: a filter over what it knows, run by eriphe-core, with its freshness. Free, no model. A view that is not fresh says so: read the source tool yourself if it matters. A view with text an assistant wrote after reading external content taints your turn, as that content would.",
		func(ctx context.Context, in assistantViewIn) (viewResult, error) {
			a, ok := c.conf().Assistants[in.Assistant]
			if !ok || a.has(triggerConversation) {
				return viewResult{}, fmt.Errorf("no assistant %q: assistants lists them", in.Assistant)
			}
			res, err := c.runView(a, in.View, in.N, c.notifyTime())
			if err == nil && res.tainted != "" {
				c.turn.taint(taintAssistantPrefix + a.Name)
			}
			return res, err
		})
}

func (c *core) assistantEntries(now time.Time) []assistantEntry {
	m := c.conf()
	out := []assistantEntry{}
	for _, n := range sortedKeys(m.Assistants) {
		a := m.Assistants[n]
		if a.has(triggerConversation) {
			continue // the brain itself
		}
		av := c.assistantView(a)
		e := assistantEntry{Name: a.Name, Display: a.displayName(), Status: plainStatus(av), Error: av.Status.LastError,
			Powers: a.powers(), Wakes: a.Triggers}
		vals := c.assistantState(a, now)
		for i, f := range a.State {
			if e.State == nil {
				e.State = map[string]any{}
			}
			switch {
			case f.Type == stateRows:
				if rows, ok := vals[i].Value.([]any); ok {
					e.State[f.Name] = map[string]int{"rows": len(rows)}
				} else {
					e.State[f.Name] = nil
				}
			case f.closed():
				e.State[f.Name] = vals[i].Value
			default:
				e.Texts = append(e.Texts, f.Name)
			}
		}
		for _, v := range a.Views {
			f := a.State[slices.IndexFunc(a.State, func(x StateField) bool { return x.Name == v.Field })]
			e.Views = append(e.Views, viewEntry{Name: v.Name, About: v.About, Rows: f.Type == stateRows && v.History == ""})
		}
		out = append(out, e)
	}
	return out
}
