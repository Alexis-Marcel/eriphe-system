package nodestore

import (
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/Alexis-Marcel/eriphe-system/core/nodefile"
)

// A node's properties and tags, indexed (node_props): one row per value,
// one per element of a list, kept at each write and by reconcile, made
// again from the files with the rest of the index. A number keeps its
// number and a date its ISO text, so that < 20 and before 2026-10-01
// compare right; a tag matches its parents (cuisine finds
// cuisine/recette).

// indexProps writes a node's properties again.
func indexProps(tx execer, m nodefile.Meta) error {
	if _, err := tx.Exec(`DELETE FROM node_props WHERE id = ?`, m.ID); err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, p := range m.Props() {
		key := strings.ToLower(p.Key)
		if seen[key] {
			continue // the same name twice, but for case: the first
		}
		seen[key] = true
		tags := key == nodefile.TagsKey
		for i, v := range p.Values {
			pos := i
			if !p.List {
				pos = -1
			}
			value := v.Text
			var num any
			switch {
			case tags:
				value = strings.TrimPrefix(strings.TrimSpace(value), "#")
			case v.Kind == nodefile.KindNumber:
				num = v.Num
			}
			if _, err := tx.Exec(`INSERT INTO node_props (id, key, pos, kind, value, fold, num) VALUES (?, ?, ?, ?, ?, ?, ?)`,
				m.ID, key, pos, v.Kind, value, strings.ToLower(value), num); err != nil {
				return err
			}
		}
	}
	return nil
}

// Cond is a condition on a property: Op is =, !=, <, <=, >, >=, exists or
// missing. = on a list holds when one of its values is equal, != when
// none is; on tags, a tag equals its parents too. A number compares as a
// number, a date (2026-10-01) as a date, a text without case.
type Cond struct {
	Key   string
	Op    string
	Value any
}

// Query is a find: words and conditions, all of which must hold.
type Query struct {
	Words    string // FTS words, any of them; "" for none
	Where    []Cond
	Sort     string // a property, or title, created, changed; "": the best for the words, else the last changed
	Desc     bool
	Archived bool // the archived nodes too
	Limit    int
}

// Ops a Cond knows.
var condOps = []string{"=", "!=", "<", "<=", ">", ">=", "exists", "missing"}

// Select finds the nodes a query names, sorted, at most its limit.
func (s *Store) Select(q Query) ([]Summary, error) {
	if q.Limit <= 0 {
		q.Limit = 20
	}
	var where []string
	var args []any
	join := ""
	order := "n.updated DESC"
	if w := strings.TrimSpace(q.Words); w != "" {
		match := ftsQuery(w)
		if match == "" {
			return nil, nil
		}
		join = `JOIN node_fts f ON f.id = n.id `
		where = append(where, `node_fts MATCH ?`)
		args = append(args, match)
		order = `bm25(node_fts, 0, 2.0, 1.0)`
	}
	if !q.Archived {
		where = append(where, `n.archived = 0`)
	}
	for _, c := range q.Where {
		sql, a, err := condSQL(c)
		if err != nil {
			return nil, err
		}
		where = append(where, sql)
		args = append(args, a...)
	}
	var orderArgs []any
	if key := strings.ToLower(strings.TrimSpace(q.Sort)); key != "" {
		dir := "ASC"
		if q.Desc {
			dir = "DESC"
		}
		switch key {
		case "title":
			order = `n.title_key ` + dir
		case "created":
			order = `n.created ` + dir
		case "changed", "updated":
			order = `n.updated ` + dir
		default:
			// The first value of the property; nodes without it last.
			sub := `(SELECT %s FROM node_props p WHERE p.id = n.id AND p.key = ? ORDER BY p.pos LIMIT 1)`
			order = fmt.Sprintf(sub+` IS NULL, `+sub+` %s, `+sub+` %s`, "1", "p.num", dir, "p.fold", dir)
			orderArgs = []any{key, key, key}
		}
	}
	tail := ""
	if len(where) > 0 {
		tail = `WHERE ` + strings.Join(where, ` AND `)
	}
	args = append(args, orderArgs...)
	args = append(args, q.Limit)
	return s.summaries(join+tail+` ORDER BY `+order+`, n.id LIMIT ?`, args...)
}

// condSQL is a condition as SQL on the node n.
func condSQL(c Cond) (string, []any, error) {
	key := strings.ToLower(strings.TrimSpace(c.Key))
	op := strings.ToLower(strings.TrimSpace(c.Op))
	if op == "" {
		op = "="
	}
	if key == "" {
		return "", nil, errors.New("a condition names its property (key)")
	}
	has := func(cond string, a ...any) (string, []any) {
		// The nodes whose values hold it, read from the index on (key, …).
		return `n.id IN (SELECT p.id FROM node_props p WHERE p.key = ?` + cond + `)`, append([]any{key}, a...)
	}
	switch op {
	case "exists":
		s, a := has("")
		return s, a, nil
	case "missing":
		s, a := has("")
		return `NOT ` + s, a, nil
	}
	if c.Value == nil {
		return "", nil, fmt.Errorf("%s %s: a value is needed", key, op)
	}
	var match string
	var args []any
	switch {
	case key == nodefile.TagsKey && (op == "=" || op == "!="):
		t := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(fmt.Sprint(c.Value)), "#"))
		// The tag, or one below it: both sort between "cuisine" and
		// "cuisine0" ("/" is just before "0"), a range the index reads.
		match, args = ` AND p.fold >= ? AND p.fold < ? AND (p.fold = ? OR p.fold >= ?)`, []any{t, t + "0", t, t + "/"}
	default:
		num, isNum := number(c.Value)
		text := strings.ToLower(strings.TrimSpace(fmt.Sprint(c.Value)))
		cmp := op
		if op == "!=" {
			cmp = "="
		}
		if !slices.Contains(condOps, op) {
			return "", nil, fmt.Errorf("%s: an op is one of %s", op, strings.Join(condOps, ", "))
		}
		switch {
		case isNum:
			match, args = ` AND p.kind = 'number' AND p.num `+cmp+` ?`, []any{num}
		case op == "=" || op == "!=":
			match, args = ` AND p.fold = ?`, []any{text}
		case isDate(text):
			match, args = ` AND p.kind = 'date' AND p.fold `+cmp+` ?`, []any{text}
		default:
			match, args = ` AND p.kind = 'text' AND p.fold `+cmp+` ?`, []any{text}
		}
	}
	s, a := has(match, args...)
	if op == "!=" {
		s = `NOT ` + s
	}
	return s, a, nil
}

// number is a condition's value as a number, if it is one.
func number(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	}
	return 0, false
}

func isDate(s string) bool {
	return len(s) >= 10 && s[4] == '-' && s[7] == '-' && strings.Trim(s[:4]+s[5:7]+s[8:10], "0123456789") == ""
}

// Props are the values of the properties named of the nodes given, by
// node then name (lowercase): a value alone, or a list.
func (s *Store) Props(ids, keys []string) (map[string]map[string]any, error) {
	out := map[string]map[string]any{}
	if len(ids) == 0 || len(keys) == 0 {
		return out, nil
	}
	var lower []string
	for _, k := range keys {
		lower = append(lower, strings.ToLower(strings.TrimSpace(k)))
	}
	q := `SELECT id, key, pos, kind, value, num FROM node_props WHERE id IN (` + marks(len(ids)) + `) AND key IN (` + marks(len(lower)) + `) ORDER BY id, key, pos`
	args := make([]any, 0, len(ids)+len(lower))
	for _, id := range ids {
		args = append(args, id)
	}
	for _, k := range lower {
		args = append(args, k)
	}
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, key, kind, value string
		var pos int
		var num sql.NullFloat64
		if err := rows.Scan(&id, &key, &pos, &kind, &value, &num); err != nil {
			return nil, err
		}
		var v any = value
		if kind == nodefile.KindNumber && num.Valid {
			v = num.Float64
		}
		if out[id] == nil {
			out[id] = map[string]any{}
		}
		if pos < 0 {
			out[id][key] = v
			continue
		}
		list, _ := out[id][key].([]any)
		out[id][key] = append(list, v)
	}
	return out, rows.Err()
}

func marks(n int) string { return strings.TrimSuffix(strings.Repeat("?, ", n), ", ") }
