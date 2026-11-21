package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"maps"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/Alexis-Marcel/eriphe-system/core/nodefile"
)

// The click on one of a block's actions (list_actions.go): the user's own
// gesture, on the items they picked in a list.
//
//   - The action and the List it was made in are read again in the block
//     as stored, never taken from the page: each must be the same at the
//     version the page showed and at the last one (code's own versions, a
//     source's snapshot, come in between and change nothing of them); the
//     action shows on that List.
//   - The ids must be items the source lists now (its last read, else its
//     snapshot) that the List's filter keeps now, its items' notes as
//     they are: none is guessed, the set is never made larger (a mail come
//     since the page showed the list is not in it), and the tool gets each
//     id as the source gave it. One id that is not is enough for nothing
//     to run.
//   - The tool's access policy holds: never is refused (but a tool only the
//     user's gesture runs, catalogFacts.gesture, which the click runs); on approval, the
//     click is the user's yes, given on the exact call the page showed;
//     free runs. Each is kept in the decisions' journal, by the user, and
//     in the node's trace, in code's words (never the items' titles,
//     someone else's words the brain would read as the user's). No turn
//     starts.
//   - Then the source is read again, the page says in a line what was
//     done, or why not, and the skills that work in the node are woken
//     (wakeNodeSkills): its view changed by the user's hand.
//
// One call with all the ids: what the user confirmed is that call. An
// action with an arg taken from each item ({"@item": "account"}) makes
// one call per value the picked items have, read from the source (the
// mails of each account, by their account), each under its own access
// policy, all checked before any runs; the confirm said those values. A
// call that fails says so for its items; a tool's words in its error are
// shown as plain text.

func (w *web) listActionRoutes(mux *webMux) {
	mux.Handle("POST /api/nodes/{id}/act", w.auth(w.handleNodeAct))
}

// nodeActIn is a click on an action: the block, the List it was made in
// (its component id), the action's name, the node's version the page
// showed, and the ids picked (or all the List showed: the page sends
// them, the very ones it showed).
type nodeActIn struct {
	Block   string   `json:"block"`
	List    string   `json:"list"`
	Action  string   `json:"action"`
	Version int      `json:"version"`
	IDs     []string `json:"ids"`
}

// actTimeout is as long as an action's call may take.
const actTimeout = 2 * time.Minute

// handleNodeAct is POST /api/nodes/{id}/act. 200 with the line that says
// what was done; 400 for ids the source does not list (nothing done);
// 403 for a tool the policy never allows; 404; 409 when the action
// changed since the page showed it; 410 archived, or the tool gone; 502
// when the tool failed. Every answer but 200 has error, the line to show.
func (w *web) handleNodeAct(rw http.ResponseWriter, r *http.Request, _ *session) {
	sum, ok := w.gestureNode(rw, r)
	if !ok {
		return
	}
	var in nodeActIn
	if !gestureBody(rw, r, 64<<10, &in) {
		return
	}
	lang := w.lang(r)
	res := w.c.runAction(r.Context(), sum.ID, in, lang)
	if res.Status != http.StatusOK {
		writeJSON(rw, res.Status, map[string]any{"error": res.Text})
		return
	}
	writeJSON(rw, http.StatusOK, map[string]any{"text": res.Text, "done": res.Done})
}

// actResult is what a click did: an HTTP status, the line to show, and
// how many items the call took.
type actResult struct {
	Status int
	Text   string
	Done   int
}

func actFail(status int, text string) actResult { return actResult{Status: status, Text: text} }

// runAction runs a click on an action of node id.
func (c *core) runAction(ctx context.Context, id string, in nodeActIn, lang string) actResult {
	if c.nodes == nil {
		return actFail(http.StatusNotFound, errNoNode.Error())
	}
	sum, err := c.nodes.Get(id)
	if err != nil {
		return actFail(http.StatusNotFound, errNoNode.Error())
	}
	if !sum.Archived.IsZero() {
		return actFail(http.StatusGone, tr(lang, "act.archived"))
	}
	last, err := c.nodes.Read(id, 0)
	if err != nil {
		return actFail(http.StatusNotFound, errNoNode.Error())
	}
	b := last.Node.Block(in.Block)
	if b == nil || b.Card != "" {
		return actFail(http.StatusNotFound, tr(lang, "act.noaction"))
	}
	a, ok := b.Actions[in.Action]
	if !ok {
		return actFail(http.StatusConflict, tr(lang, "act.changed"))
	}
	path := listTemplate(b, in.List)
	if in.Version > 0 && in.Version != last.Version.N {
		shown, err := c.nodes.Read(id, in.Version)
		var was *nodefile.Block
		if err == nil {
			was = shown.Node.Block(in.Block)
		}
		if was == nil || !reflect.DeepEqual(was.Actions[in.Action], a) || listTemplate(was, in.List) != path {
			return actFail(http.StatusConflict, tr(lang, "act.changed"))
		}
	}
	if b.Validate() != nil {
		return actFail(http.StatusConflict, tr(lang, "act.changed"))
	}
	// The List the click was made in: one of the action's source's items,
	// the action shown on it; its filter as stored.
	source, filter, err := listBinding(path, b.Sources)
	if err != nil || source != a.Source || a.List != "" && a.List != in.List {
		return actFail(http.StatusConflict, tr(lang, "act.changed"))
	}
	if len(in.IDs) == 0 {
		return actFail(http.StatusBadRequest, tr(lang, "act.none"))
	}
	if len(in.IDs) > actionsMaxIDs {
		return actFail(http.StatusBadRequest, fmt.Sprintf(tr(lang, "act.toomany"), actionsMaxIDs))
	}
	src := b.Sources[a.Source]
	value := c.sourceNow(id, b, a.Source)
	noteItems(src, value, c.itemNotes(id, a.Source), c.itemCards(id, a.Source)) // its notes now, for the filters
	listed, _ := itemIDs(src, value, filter)
	var picked []listedItem
	seen := map[string]bool{}
	missing := 0
	for _, x := range in.IDs {
		v, ok := listed[x]
		if !ok {
			missing++
			continue
		}
		if !seen[x] {
			seen[x] = true
			picked = append(picked, v)
		}
	}
	if missing > 0 {
		return actFail(http.StatusBadRequest, fmt.Sprintf(tr(lang, "act.stale"), missing))
	}

	u, t := c.actionTool(a.Tool)
	if u == nil || t == nil {
		return actFail(http.StatusGone, tr(lang, "act.gone"))
	}
	name := claudeToolName(u.name, t.Name)
	calls, missingArg := actionCalls(a, picked)
	if missingArg != "" {
		return actFail(http.StatusBadRequest, fmt.Sprintf(tr(lang, "act.noitemarg"), missingArg))
	}
	// Each call under its tool's policy, all of them before any runs: one
	// that may not run, and nothing does.
	for _, cl := range calls {
		cl.facts = c.pool.facts(name, cl.input)
		switch f := cl.facts; {
		case f.noAccount:
			return actFail(http.StatusForbidden, tr(lang, "act.account"))
		case f.value == gridNever && !f.gesture:
			return actFail(http.StatusForbidden, tr(lang, "act.never"))
		case f.gesture && !strings.HasPrefix(src.Tool, u.name+"/"):
			// A tool only the user's gesture runs acts on what its own server
			// listed and the user saw, never on another source's ids.
			return actFail(http.StatusForbidden, tr(lang, "act.never"))
		}
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), actTimeout)
	defer cancel()
	one, many := actionWords(t, a.Tool, a.FixedArgs(), "fr")
	done, failed := 0, 0
	code, why := "", ""
	for _, cl := range calls {
		f := cl.facts
		reason := "geste de l'utilisateur sur une liste"
		if f.gesture {
			reason = "geste de l'utilisateur sur une liste : un outil que seul son geste lance"
		}
		if f.value == gridApproval {
			reason = "geste de l'utilisateur sur une liste : son clic vaut son oui"
		}
		raw, _ := json.Marshal(cl.input)
		out, _, c1, w1 := c.catalogCall(ctx, name, string(raw), true, func(input map[string]any) {
			c.store.logDecision("", "", name, input, decideAllow, byUser, reason)
		})
		_ = out // the tool's result: someone else's words, never shown nor kept
		said := one
		if len(cl.ids) > 1 {
			said = strings.ReplaceAll(many, "{n}", fmt.Sprint(len(cl.ids)))
		}
		note := "Sur la liste du bloc « " + in.Block + " », " + c.conf().User + " a lancé : " + said + " (" + a.Tool + ")"
		if f.value == gridApproval {
			note += " (son clic vaut son oui)"
		}
		if c1 != "" {
			note += ". Échec."
			failed += len(cl.ids)
			if code == "" {
				code, why = c1, w1
			}
		} else {
			note += "."
			done += len(cl.ids)
		}
		if err := c.nodes.AppendTrace(id, byUser, note); err != nil {
			log.Printf("nodes: %s: trace: %v", id, err)
		}
	}
	c.readNodeLater(context.Background(), id, []string{a.Source})
	if done > 0 {
		c.wakeNodeSkills(id, "action")
	}
	switch {
	case code == "":
		return actResult{Status: http.StatusOK, Text: doneText(a.Tool, done, lang), Done: done}
	case done > 0:
		// Some calls ran, some did not: both said, the page reads the
		// source again.
		return actResult{Status: http.StatusOK, Text: fmt.Sprintf(tr(lang, "act.partial"), doneText(a.Tool, done, lang), failed), Done: done}
	case code == callWhyNotAllowed:
		return actFail(http.StatusForbidden, tr(lang, "act.never"))
	case code == callWhyGone, code == callWhyNotKept:
		return actFail(http.StatusGone, tr(lang, "act.gone"))
	}
	return actFail(http.StatusBadGateway, fmt.Sprintf(tr(lang, "act.failed"), truncate(oneLine(why), 200)))
}

// actionCall is one call a click makes: its input, the ids it takes, and
// its tool's policy for that input.
type actionCall struct {
	input map[string]any
	ids   []any
	facts catalogFacts
}

// actionCalls are the calls a click on picked items makes: one with all
// their ids, or, for an action whose args are taken from each item, one
// per value they have, each with the ids of its items, in the order
// first met. The values are read from the source's items, never from the
// page. missing names the arg an item has no value for: nothing runs.
func actionCalls(a nodefile.Action, picked []listedItem) (calls []*actionCall, missing string) {
	byItem := a.ItemArgs()
	params := slices.Sorted(maps.Keys(byItem))
	keyed := map[string]*actionCall{}
	for _, it := range picked {
		input := a.FixedArgs()
		key := ""
		for _, k := range params {
			text := itemArgValue(it.Item, byItem[k])
			if text == "" {
				return nil, k
			}
			v, _ := extract(it.Item, byItem[k])
			input[k] = v
			key += k + "\x00" + text + "\x00"
		}
		cl := keyed[key]
		if cl == nil {
			cl = &actionCall{input: input}
			keyed[key] = cl
			calls = append(calls, cl)
		}
		cl.ids = append(cl.ids, it.ID)
	}
	for _, cl := range calls {
		cl.input[a.Items] = cl.ids
	}
	return calls, ""
}

// sourceNow is a block's source's value now: its last read, else its
// snapshot.
func (c *core) sourceNow(id string, b *nodefile.Block, name string) any {
	for _, r := range c.nodeReads(id) {
		if r.Block == b.Name && r.Name == name && r.Value != nil {
			return r.Value
		}
	}
	var snap map[string]any
	json.Unmarshal(b.Snapshot, &snap)
	return snap[name]
}

// doneText says in a line what a click did: by the tool's verb when it
// has one of its own, else how many items it took.
func doneText(tool string, n int, lang string) string {
	_, short, _ := strings.Cut(tool, "/")
	key := "act.done." + short
	if tr(lang, key+".one") == key+".one" {
		key = "act.done"
	}
	if n == 1 {
		return tr(lang, key+".one")
	}
	return fmt.Sprintf(tr(lang, key+".many"), n)
}
