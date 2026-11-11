package main

import (
	"fmt"
	"hash/fnv"
	"net/http"
)

// A place's own rules, in a drawer (Settings → a tool's folders, a
// connection's repositories, Settings → Folders): the tool's grid (Lire ·
// Modifier · Supprimer · Envoyer by Libre · Sur accord · Jamais), with one
// more column, "as the tool" (its account, its device), which a case
// follows until it is given a value of its own. The lists show only a
// summary of each place: what it sets of its own, or that it follows.

// caseGrid is one place's grid, in its drawer, and its summary in a list.
type caseGrid struct {
	Key    string            // the place's, for its summary's id in the list
	Title  string            // the place's name
	Sub    string            // what it belongs to: an account, a tool, a device
	Post   string            // where a change goes
	Hidden map[string]string // sent with it
	Follow string            // the text key of the "as …" column, "" for none
	Hint   string            // the text key pattern of a case's hint
	Greyed string            // the text key of why a case is greyed
	Rows   []caseRow
	Error  string
	Saved  bool
}

// caseRow is one case of a place: its own value ("" follows) and the
// value it follows.
type caseRow struct {
	Category, Own, Follows string
	Greyed                 bool // above what its tool or folder allows
}

// Own are the rows with a value of their own, for the summary.
func (g caseGrid) Own() []caseRow {
	var out []caseRow
	for _, r := range g.Rows {
		if r.Own != "" && !r.Greyed {
			out = append(out, r)
		}
	}
	return out
}

// greyed are the rows above what the place allows: never, whatever they
// say of their own or follow.
func (g caseGrid) greyed() caseGrid {
	for i, r := range g.Rows {
		if r.Greyed {
			g.Rows[i].Own, g.Rows[i].Follows = "", gridNever
		}
	}
	return g
}

// Value is what the row's radio says: its own, else "" (follows), or the
// value it follows when the grid has no "as …" column.
func (r caseRow) Value(follow string) string {
	if r.Own == "" && follow == "" {
		return r.Follows
	}
	return r.Own
}

// caseKey is a short id for a place, for the id of its summary.
func caseKey(id string) string {
	h := fnv.New64a()
	h.Write([]byte(id))
	return fmt.Sprintf("c%x", h.Sum64())
}

// postedCases are the cases a grid's form changed: its "case-<category>"
// fields whose value is not the one set (now), in the grid's order.
func postedCases(r *http.Request, cats []string, now func(string) string) [][2]string {
	r.ParseForm()
	var out [][2]string
	for _, cat := range cats {
		v, ok := r.PostForm["case-"+cat]
		if ok && len(v) > 0 && v[0] != now(cat) {
			out = append(out, [2]string{cat, v[0]})
		}
	}
	return out
}
