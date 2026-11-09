package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

// An assistant's state: what it knows now, typed: its kept facts
// (facts.go), whose owner is the assistant. Its schema is part of
// its spec (Assistant.State): fields, each a choice from a list, a number,
// a date, a text, or a list of rows whose columns are such fields. Its one
// writer is the assistant itself, through set_state, checked by code: a
// value off its list, a number that is not one, an unknown field are
// refused. Each value has its time and its validity; expired, it reads
// unknown. A few days of history are kept, and the whole is capped.
// Stored in eriphe-core's database: a read costs milliseconds and no
// model.
//
// Taint is per field: a closed field (a choice, a number, a date, a count
// of rows) never taints whatever its writer read; a text, or rows with a
// text column, keeps why its writer's session was tainted when it wrote
// it, and taints what reads it.
//
// What reads it: the brain, in each turn, through one line per assistant
// of its summary fields (closed only: assistantsLine); the views come with
// #244 step 2c. The state orients, never authorises: the grid, the cards
// and the approvals never read it.

// Its kept facts (facts.go): owner is the assistant's name, writer who
// wrote it. Before #244 step 3 the tables were assistant_state and
// assistant_state_log, keyed by assistant (renamed at start: store.go).
const assistantStateSchema = `CREATE TABLE IF NOT EXISTS facts (
  owner TEXT NOT NULL, field TEXT NOT NULL, value TEXT NOT NULL, set_at INTEGER NOT NULL,
  expires INTEGER NOT NULL, tainted TEXT NOT NULL DEFAULT '', writer TEXT NOT NULL DEFAULT '', PRIMARY KEY (owner, field));
CREATE TABLE IF NOT EXISTS facts_log (
  id INTEGER PRIMARY KEY AUTOINCREMENT, owner TEXT NOT NULL, field TEXT NOT NULL, value TEXT NOT NULL,
  set_at INTEGER NOT NULL, tainted TEXT NOT NULL DEFAULT '', writer TEXT NOT NULL DEFAULT '');
CREATE INDEX IF NOT EXISTS facts_log_at ON facts_log (owner, set_at);
UPDATE facts SET writer = owner WHERE writer = '';
UPDATE facts_log SET writer = owner WHERE writer = '';`

func init() { extraSchema = append(extraSchema, assistantStateSchema) }

// The types of a field.
const (
	stateChoice = "choice"
	stateNumber = "number"
	stateDate   = "date"
	stateText   = "text"
	stateRows   = "rows"
)

const (
	maxStateFields  = 20
	maxStateColumns = 12
	maxStateChoices = 30
	stateChoiceMax  = 40  // runes
	stateTextMax    = 500 // runes
	defaultMaxRows  = 50
	maxStateRows    = 200
	maxStateBytes   = 64 << 10 // all its values, as JSON
	stateKeep       = 7 * 24 * time.Hour
	maxStateLog     = 2000 // per assistant
	defaultStateTTL = 24 * time.Hour
	minStateTTL     = time.Minute
	maxStateTTL     = 30 * 24 * time.Hour
)

// StateField is a field of an assistant's state, or a column of rows.
type StateField struct {
	Name   string   `json:"name"`
	Type   string   `json:"type"`             // choice, number, date, text or rows
	Values []string `json:"values,omitempty"` // a choice's list
	// A field's validity, a duration (15m, 4h, 2d); 24h by default.
	TTL string `json:"ttl,omitempty"`
	// In the brain's summary line: a closed field (choice, number, date),
	// or rows by their count.
	Summary bool `json:"summary,omitempty"`
	// Rows: their columns (no rows in rows), the column that keys them
	// (set_state then updates and removes rows by key), at most MaxRows
	// (50 by default, up to 200).
	Columns []StateField `json:"columns,omitempty"`
	Key     string       `json:"key,omitempty"`
	MaxRows int          `json:"max_rows,omitempty"`
}

var stateNameRE = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)

// closed tells whether a field's value is from a closed set: it never
// carries words of what its writer read.
func (f StateField) closed() bool {
	switch f.Type {
	case stateChoice, stateNumber, stateDate:
		return true
	case stateRows:
		return !slices.ContainsFunc(f.Columns, func(c StateField) bool { return !c.closed() })
	}
	return false
}

func (f StateField) ttl() time.Duration {
	if d, err := parseTTL(f.TTL); err == nil && d > 0 {
		return d
	}
	return defaultStateTTL
}

func (f StateField) maxRows() int {
	if f.MaxRows > 0 {
		return f.MaxRows
	}
	return defaultMaxRows
}

// parseTTL reads a validity: a Go duration, or a number of days (2d).
func parseTTL(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	if n, ok := strings.CutSuffix(s, "d"); ok {
		var days int
		if _, err := fmt.Sscan(n, &days); err != nil || fmt.Sprint(days) != n {
			return 0, fmt.Errorf("ttl %q: a duration, 15m, 4h or 2d", s)
		}
		return time.Duration(days) * 24 * time.Hour, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("ttl %q: a duration, 15m, 4h or 2d", s)
	}
	return d, nil
}

// checkStateSchema checks a schema: names, types, lists, validity.
func checkStateSchema(fields []StateField) error {
	if len(fields) > maxStateFields {
		return fmt.Errorf("at most %d fields", maxStateFields)
	}
	return checkStateFields(fields, false)
}

func checkStateFields(fields []StateField, columns bool) error {
	seen := map[string]bool{}
	for _, f := range fields {
		what := "field " + f.Name
		if columns {
			what = "column " + f.Name
		}
		if !stateNameRE.MatchString(f.Name) {
			return fmt.Errorf("%q: a name of lowercase letters, digits and _, starting with a letter, up to 32", f.Name)
		}
		if seen[f.Name] {
			return fmt.Errorf("%s twice", what)
		}
		seen[f.Name] = true
		switch f.Type {
		case stateChoice:
			if len(f.Values) == 0 || len(f.Values) > maxStateChoices {
				return fmt.Errorf("%s: a choice lists 1 to %d values", what, maxStateChoices)
			}
			vals := map[string]bool{}
			for _, v := range f.Values {
				if n := utf8.RuneCountInString(v); n == 0 || n > stateChoiceMax || strings.TrimSpace(v) != v || vals[strings.ToLower(v)] {
					return fmt.Errorf("%s: value %q: 1 to %d characters, each once", what, v, stateChoiceMax)
				}
				vals[strings.ToLower(v)] = true
			}
		case stateNumber, stateDate, stateText:
		case stateRows:
			if columns {
				return fmt.Errorf("%s: no rows in rows", what)
			}
			if len(f.Columns) == 0 || len(f.Columns) > maxStateColumns {
				return fmt.Errorf("%s: rows have 1 to %d columns", what, maxStateColumns)
			}
			if err := checkStateFields(f.Columns, true); err != nil {
				return fmt.Errorf("%s: %w", what, err)
			}
			if f.Key != "" && !slices.ContainsFunc(f.Columns, func(c StateField) bool { return c.Name == f.Key }) {
				return fmt.Errorf("%s: key %q is not one of its columns", what, f.Key)
			}
			if f.MaxRows < 0 || f.MaxRows > maxStateRows {
				return fmt.Errorf("%s: max_rows up to %d", what, maxStateRows)
			}
		default:
			return fmt.Errorf("%s: type %q: choice, number, date, text or rows", what, f.Type)
		}
		if f.Type != stateChoice && len(f.Values) > 0 {
			return fmt.Errorf("%s: values are a choice's", what)
		}
		if f.Type != stateRows && (len(f.Columns) > 0 || f.Key != "" || f.MaxRows != 0) {
			return fmt.Errorf("%s: columns, key and max_rows are rows'", what)
		}
		if columns && (f.TTL != "" || f.Summary) {
			return fmt.Errorf("%s: a column has its rows' validity, and no summary", what)
		}
		if d, err := parseTTL(f.TTL); err != nil {
			return fmt.Errorf("%s: %w", what, err)
		} else if f.TTL != "" && (d < minStateTTL || d > maxStateTTL) {
			return fmt.Errorf("%s: ttl between %s and 30 days", what, minStateTTL)
		}
		if f.Summary && f.Type == stateText {
			return fmt.Errorf("%s: a text is never in the summary, which is closed values only", what)
		}
	}
	return nil
}

// checkValue checks a value against its field and returns it as kept:
// a choice as listed, a number, a date as YYYY-MM-DD or YYYY-MM-DDTHH:MM
// in the user's zone, a trimmed text, rows of such values.
func (f StateField) checkValue(v any, loc *time.Location) (any, error) {
	switch f.Type {
	case stateChoice:
		s, _ := v.(string)
		for _, x := range f.Values {
			if strings.EqualFold(x, strings.TrimSpace(s)) {
				return x, nil
			}
		}
		return nil, fmt.Errorf("%s: %v is not one of %s", f.Name, v, strings.Join(f.Values, ", "))
	case stateNumber:
		n, ok := v.(float64)
		if !ok || math.IsNaN(n) || math.IsInf(n, 0) {
			return nil, fmt.Errorf("%s: %v is not a number", f.Name, v)
		}
		return n, nil
	case stateDate:
		s, _ := v.(string)
		s = strings.TrimSpace(s)
		if _, err := time.ParseInLocation("2006-01-02", s, loc); err == nil {
			return s, nil
		}
		for _, layout := range []string{"2006-01-02T15:04", "2006-01-02 15:04", "2006-01-02T15:04:05"} {
			if t, err := time.ParseInLocation(layout, s, loc); err == nil {
				return t.Format("2006-01-02T15:04"), nil
			}
		}
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			return t.In(loc).Format("2006-01-02T15:04"), nil
		}
		return nil, fmt.Errorf("%s: %v is not a date, YYYY-MM-DD or YYYY-MM-DDTHH:MM", f.Name, v)
	case stateText:
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("%s: a text", f.Name)
		}
		if s = strings.TrimSpace(s); utf8.RuneCountInString(s) > stateTextMax {
			return nil, fmt.Errorf("%s: at most %d characters", f.Name, stateTextMax)
		}
		return s, nil
	case stateRows:
		list, ok := v.([]any)
		if !ok {
			return nil, fmt.Errorf("%s: a list of rows", f.Name)
		}
		if len(list) > f.maxRows() {
			return nil, fmt.Errorf("%s: at most %d rows", f.Name, f.maxRows())
		}
		out := make([]any, 0, len(list))
		keys := map[string]bool{}
		for i, x := range list {
			row, err := f.checkRow(x, loc)
			if err != nil {
				return nil, fmt.Errorf("%s, row %d: %w", f.Name, i+1, err)
			}
			if f.Key != "" {
				k := fmt.Sprint(row[f.Key])
				if row[f.Key] == nil || keys[k] {
					return nil, fmt.Errorf("%s, row %d: its key %s is missing or twice", f.Name, i+1, f.Key)
				}
				keys[k] = true
			}
			out = append(out, row)
		}
		return out, nil
	}
	return nil, fmt.Errorf("%s: unknown type", f.Name)
}

func (f StateField) checkRow(x any, loc *time.Location) (map[string]any, error) {
	in, ok := x.(map[string]any)
	if !ok {
		return nil, errors.New("a row is an object of its columns")
	}
	row := map[string]any{}
	for k, v := range in {
		i := slices.IndexFunc(f.Columns, func(c StateField) bool { return c.Name == k })
		if i < 0 {
			return nil, fmt.Errorf("no column %q", k)
		}
		if v == nil {
			continue
		}
		val, err := f.Columns[i].checkValue(v, loc)
		if err != nil {
			return nil, err
		}
		row[k] = val
	}
	return row, nil
}

// stateValue is a field's value as kept.
type stateValue struct {
	Field   string    `json:"field"`
	Value   any       `json:"value"` // nil: unknown (never set, cleared or expired)
	Set     time.Time `json:"set"`
	Expires time.Time `json:"expires"`
	Tainted string    `json:"tainted,omitempty"`
}

func (v stateValue) known(now time.Time) bool { return v.Value != nil && now.Before(v.Expires) }

// assistantState is an assistant's state as its schema reads it now: one
// value per field, in its order; an expired value, or one its schema no
// longer takes, reads unknown.
func (c *core) assistantState(a Assistant, now time.Time) []stateValue {
	kept := map[string]stateValue{}
	rows, err := c.store.db.Query(`SELECT field, value, set_at, expires, tainted FROM facts WHERE owner = ?`, a.Name)
	if err == nil {
		for rows.Next() {
			var v stateValue
			var raw string
			var set, exp int64
			if rows.Scan(&v.Field, &raw, &set, &exp, &v.Tainted) == nil && json.Unmarshal([]byte(raw), &v.Value) == nil {
				v.Set, v.Expires = time.Unix(set, 0), time.Unix(exp, 0)
				kept[v.Field] = v
			}
		}
		rows.Close()
	}
	out := make([]stateValue, 0, len(a.State))
	for _, f := range a.State {
		v, ok := kept[f.Name]
		if !ok {
			out = append(out, stateValue{Field: f.Name})
			continue
		}
		if _, err := f.checkValue(v.Value, now.Location()); err != nil || !v.known(now) {
			v.Value = nil // expired, or off a schema changed since
		}
		out = append(out, v)
	}
	return out
}

// setStateIn is set_state's input.
type setStateIn struct {
	Values map[string]any `json:"values" jsonschema:"the fields to set, by name, each a value of its type (see your instructions); null clears a field. For rows with a key, either the whole list, or {\"upsert\": [rows], \"remove\": [keys]}"`
}

// setState writes what an assistant knows now, checked against its
// schema: all the fields or none. tainted is why its session is.
func (c *core) setState(a Assistant, in setStateIn, tainted []string, now time.Time) (map[string]any, error) {
	if len(a.State) == 0 {
		return nil, errors.New("you have no state")
	}
	if len(in.Values) == 0 {
		return nil, errors.New("values: the fields to set")
	}
	current := map[string]stateValue{}
	for _, v := range c.assistantState(a, now) {
		current[v.Field] = v
	}
	type write struct {
		f   StateField
		val any
	}
	var writes []write
	var errs []string
	for _, name := range sortedKeys(in.Values) {
		i := slices.IndexFunc(a.State, func(f StateField) bool { return f.Name == name })
		if i < 0 {
			errs = append(errs, fmt.Sprintf("no field %q", name))
			continue
		}
		f, v := a.State[i], in.Values[name]
		if v == nil {
			writes = append(writes, write{f, nil})
			continue
		}
		if m, ok := v.(map[string]any); ok && f.Type == stateRows {
			merged, err := f.merge(current[name].Value, m)
			if err != nil {
				errs = append(errs, err.Error())
				continue
			}
			v = merged
		}
		val, err := f.checkValue(v, now.Location())
		if err != nil {
			errs = append(errs, err.Error())
			continue
		}
		writes = append(writes, write{f, val})
	}
	if len(errs) > 0 {
		return nil, fmt.Errorf("refused, nothing written: %s", strings.Join(errs, "; "))
	}
	// The cap: all its values, as they would be.
	size := 0
	for _, v := range current {
		if !slices.ContainsFunc(writes, func(w write) bool { return w.f.Name == v.Field }) && v.Value != nil {
			raw, _ := json.Marshal(v.Value)
			size += len(raw)
		}
	}
	for _, w := range writes {
		raw, _ := json.Marshal(w.val)
		size += len(raw)
	}
	if size > maxStateBytes {
		return nil, fmt.Errorf("refused, nothing written: the state would hold %d bytes, at most %d: keep fewer rows or shorter texts", size, maxStateBytes)
	}
	why := strings.Join(tainted, ", ")
	tx, err := c.store.db.BeginTx(context.Background(), nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	set := []string{}
	for _, w := range writes {
		if w.val == nil {
			if _, err := tx.Exec(`DELETE FROM facts WHERE owner = ? AND field = ?`, a.Name, w.f.Name); err != nil {
				return nil, err
			}
			tx.Exec(`INSERT INTO facts_log (owner, field, value, set_at, writer) VALUES (?, ?, 'null', ?, ?)`, a.Name, w.f.Name, now.Unix(), a.Name)
			set = append(set, w.f.Name)
			continue
		}
		raw, _ := json.Marshal(w.val)
		t := ""
		if !w.f.closed() {
			t = why // a closed value never taints
		}
		if _, err := tx.Exec(`INSERT INTO facts (owner, field, value, set_at, expires, tainted, writer) VALUES (?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(owner, field) DO UPDATE SET value = excluded.value, set_at = excluded.set_at, expires = excluded.expires, tainted = excluded.tainted, writer = excluded.writer`,
			a.Name, w.f.Name, string(raw), now.Unix(), now.Add(w.f.ttl()).Unix(), t, a.Name); err != nil {
			return nil, err
		}
		if _, err := tx.Exec(`INSERT INTO facts_log (owner, field, value, set_at, tainted, writer) VALUES (?, ?, ?, ?, ?, ?)`,
			a.Name, w.f.Name, string(raw), now.Unix(), t, a.Name); err != nil {
			return nil, err
		}
		set = append(set, w.f.Name)
	}
	// A few days of history, and no more than its cap.
	tx.Exec(`DELETE FROM facts_log WHERE owner = ? AND set_at < ?`, a.Name, now.Add(-stateKeep).Unix())
	tx.Exec(`DELETE FROM facts_log WHERE owner = ? AND id NOT IN
		(SELECT id FROM facts_log WHERE owner = ? ORDER BY id DESC LIMIT ?)`, a.Name, a.Name, maxStateLog)
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return map[string]any{"set": set, "note": "Written. What expires reads unknown: write it again when you check it."}, nil
}

// merge applies {upsert, remove} to rows with a key.
func (f StateField) merge(current any, m map[string]any) (any, error) {
	if f.Key == "" {
		return nil, fmt.Errorf("%s: rows without a key take the whole list", f.Name)
	}
	for k := range m {
		if k != "upsert" && k != "remove" {
			return nil, fmt.Errorf("%s: upsert and remove only", f.Name)
		}
	}
	rows, _ := current.([]any)
	rows = slices.Clone(rows)
	key := func(r any) string {
		m, _ := r.(map[string]any)
		return fmt.Sprint(m[f.Key])
	}
	if rm, ok := m["remove"]; ok {
		list, ok := rm.([]any)
		if !ok {
			return nil, fmt.Errorf("%s: remove is a list of keys", f.Name)
		}
		for _, k := range list {
			rows = slices.DeleteFunc(rows, func(r any) bool { return key(r) == fmt.Sprint(k) })
		}
	}
	if up, ok := m["upsert"]; ok {
		list, ok := up.([]any)
		if !ok {
			return nil, fmt.Errorf("%s: upsert is a list of rows", f.Name)
		}
		for _, r := range list {
			if i := slices.IndexFunc(rows, func(x any) bool { return key(x) == key(r) }); i >= 0 {
				rows[i] = r
			} else {
				rows = append(rows, r)
			}
		}
	}
	return rows, nil
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// The brain's summary.

// assistantsLine is one line for the brain's turn: per enabled assistant
// with summary fields, their values, closed only, and how old the oldest
// is. Nothing here taints: no words of what an assistant read.
func (c *core) assistantsLine(now time.Time) string {
	m := c.conf()
	var parts []string
	for _, n := range sortedKeys(m.Assistants) {
		a := m.Assistants[n]
		if a.Disabled || len(a.State) == 0 {
			continue
		}
		vals := c.assistantState(a, now)
		var fields []string
		var oldest time.Time
		for i, f := range a.State {
			if !f.Summary || !f.closed() && f.Type != stateRows {
				continue
			}
			v := vals[i]
			if !v.known(now) {
				fields = append(fields, f.Name+" inconnu")
				continue
			}
			if oldest.IsZero() || v.Set.Before(oldest) {
				oldest = v.Set
			}
			fields = append(fields, f.Name+" "+summaryValue(f, v.Value))
		}
		if len(fields) == 0 {
			continue
		}
		part := a.displayName() + " : " + strings.Join(fields, ", ")
		if !oldest.IsZero() {
			part += " (" + shortAge(now.Sub(oldest)) + ")"
		}
		parts = append(parts, part)
	}
	if len(parts) == 0 {
		return ""
	}
	return "[assistants : " + strings.Join(parts, " · ") + "]"
}

// summaryValue is a closed value in the summary; rows by their count.
func summaryValue(f StateField, v any) string {
	switch x := v.(type) {
	case []any:
		return fmt.Sprint(len(x))
	case float64:
		return fmt.Sprint(x) // whole numbers without a decimal point
	case string:
		if f.Type == stateDate {
			return strings.Replace(x, "T", " ", 1)
		}
		return x
	}
	return fmt.Sprint(v)
}

// shortAge is a duration as the brain reads it: 4 min, 2 h, 3 j.
func shortAge(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "à l'instant"
	case d < time.Hour:
		return fmt.Sprintf("il y a %d min", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("il y a %d h", int(d.Hours()))
	}
	return fmt.Sprintf("il y a %d j", int(d.Hours()/24))
}

// durationWords is a validity in French: 15 min, 4 h, 2 j.
func durationWords(d time.Duration) string {
	switch {
	case d%(24*time.Hour) == 0:
		return fmt.Sprintf("%d j", int(d.Hours()/24))
	case d%time.Hour == 0:
		return fmt.Sprintf("%d h", int(d.Hours()))
	case d%time.Minute == 0:
		return fmt.Sprintf("%d min", int(d.Minutes()))
	}
	return d.String()
}

// stateInstructions describe its state to an assistant, in French, after
// its mission.
func stateInstructions(fields []StateField) string {
	if len(fields) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n## Ton état\n\nCe que tu sais maintenant, que tu écris avec `set_state` (tous les champs d'un appel sont refusés si l'un ne va pas) et relis avec `state`. Une valeur expire après sa durée et devient « inconnu » : réécris-la quand tu la vérifies. Ses champs :\n")
	for _, f := range fields {
		fmt.Fprintf(&b, "- `%s` : %s, valable %s", f.Name, fieldWords(f), durationWords(f.ttl()))
		if f.Summary {
			b.WriteString(", lu par Eriphe à chaque tour")
		}
		b.WriteString("\n")
	}
	return b.String()
}

func fieldWords(f StateField) string {
	switch f.Type {
	case stateChoice:
		return "un choix parmi " + strings.Join(f.Values, ", ")
	case stateNumber:
		return "un nombre"
	case stateDate:
		return "une date, AAAA-MM-JJ ou AAAA-MM-JJTHH:MM"
	case stateText:
		return fmt.Sprintf("un texte d'au plus %d caractères", stateTextMax)
	case stateRows:
		var cols []string
		for _, c := range f.Columns {
			cols = append(cols, "`"+c.Name+"` ("+fieldWords(c)+")")
		}
		s := fmt.Sprintf("une liste d'au plus %d lignes, colonnes %s", f.maxRows(), strings.Join(cols, ", "))
		if f.Key != "" {
			s += fmt.Sprintf(" ; clé `%s` : envoie la liste entière, ou {\"upsert\": [lignes], \"remove\": [clés]}", f.Key)
		}
		return s
	}
	return f.Type
}
