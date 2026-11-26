package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/Alexis-Marcel/eriphe-system/core/nodefile"
	"github.com/Alexis-Marcel/eriphe-system/core/nodestore"
)

// An action on a group of items (docs/node-format.md, « Actions »): a
// line [Jeter ces mails](do:mail/trash), alone, the first under a
// heading, acts on the items the page references from that heading to
// the next of its level or higher (nodefile's Scope). The page says where
// the action is; code composes it. Nothing here knows a server or a tool.
//
//   - Its items: the references under the heading to the tool's server's
//     items (page_items.go), each id once, on a row the reader sees (not
//     a paragraph's, nor a folded callout's), but those whose item is
//     ticked (done or dropped), and those a call of the same tool already
//     names in the node (waiting on its own card, or said yes to lately,
//     ran or running): the same yes is not asked twice.
//   - Its words are code's: what the tool does to how many (the card's
//     sentence, call_words.go), each item as its server reads it (a free
//     read, as a card on an item), never the link's words; a reference
//     whose words do not say the item read is marked misleading.
//   - Its calls are composed from the tool's input schema: the argument
//     that takes the items' ids (actArg): one call with them all when it
//     takes a list, one call each when it takes one id. A tool that needs
//     anything else has no do: (nothing is made up).
//   - The author of its line must have the tool (the page check says it
//     at the write, do-tool): who wrote the line, by the node's versions
//     (lineAuthor), as the write's check knew them.
//   - It is not kept while it waits: the page is its state. Its card is
//     drawn from the page as it is, the user unticks what stays, and at
//     the yes code reads the page again: only the items ticked that are
//     still under the heading run, never one added since the card was
//     drawn. Then the calls are kept as one calls card on the node and
//     that card is said yes to at once, by this yes: the run, its outcome
//     (the orb's « C'est fait »), its record (« Déjà décidé ») and the
//     skill woken to write the page again are the card's, as for any
//     other (item_cards.go). Every call goes through the access grid
//     (catalogCall): one the grid never allows is refused before, and
//     fails if it changed since.

// Why a do: acts on nothing, as the page says it (its words in actWhy).
const (
	actPlace   = "place"   // not alone on its line, the first under a heading
	actTool    = "tool"    // no such tool in the catalog
	actAuthor  = "author"  // the author of its line does not have the tool
	actCompose = "compose" // the tool's schema takes no ids alone
	actGrid    = "grid"    // the access grid never allows it
)

// errActRefused is a yes on a do: that acts on nothing; errActNone, one
// on items none of which is still under its heading.
var (
	errActRefused = errors.New("this action cannot run")
	errActNone    = errors.New("none of the items chosen is still under its heading")
	errNotAct     = errors.New("the line is not an action on its heading's items")
)

// pageAct is a do: of a node's page as code composes it: what the page
// draws on its line, and what its yes runs.
type pageAct struct {
	Line    int    `json:"line"`
	Text    string `json:"text"` // the line as stored: the yes names it
	Tool    string `json:"tool"` // server/tool, as the link writes it
	Refused string `json:"refused,omitempty"`
	Why     string `json:"why,omitempty"` // refused, in words
	// Items are what it acts on now, in the page's order.
	Items []actItem `json:"items"`
	// Acts: what Oui does for 1, 2… of its items ticked; Verbs: what its
	// button says.
	Acts  []string `json:"acts,omitempty"`
	Verbs []string `json:"verbs,omitempty"`
	// Tainted: the page is someone else's words (the node is external).
	Tainted bool `json:"tainted,omitempty"`

	name  string // its call's name (mcp__server__tool)
	arg   string // the argument of its ids
	list  bool   // a list of them: one call
	taint string
}

// actItem is an item a do: acts on: its reference (label, line), the row
// that carries its box, its id, and the item as its server read it.
type actItem struct {
	ID         string `json:"id"`
	Label      string `json:"label"`
	Line       int    `json:"line"`
	Row        int    `json:"row"`
	Title      string `json:"title,omitempty"`
	Detail     string `json:"detail,omitempty"`
	External   bool   `json:"external,omitempty"`
	Misleading bool   `json:"misleading,omitempty"`
	Unread     bool   `json:"unread,omitempty"`
}

// composeAct is the do: on the line ref names of node id's last version:
// its items (read by their server when read is set, for its words) and
// its calls' shape. An error when the line is gone, or is no do:.
func (c *core) composeAct(ctx context.Context, id string, ref nodefile.LineRef, read bool, lang string) (pageAct, error) {
	d, err := c.nodes.Read(id, 0)
	if err != nil {
		return pageAct{}, errNoNode
	}
	body, err := d.Node.Body()
	if err != nil {
		return pageAct{}, errNotAct
	}
	ln, err := nodefile.FindLine(body, ref)
	if err != nil {
		return pageAct{}, err
	}
	p := nodefile.ReadPage(body)
	i := slices.IndexFunc(p.Links, func(l nodefile.PageLink) bool { return l.Kind == nodefile.LinkDo && l.Line == ln })
	if i < 0 {
		return pageAct{}, errNotAct
	}
	l := p.Links[i]
	lines := strings.Split(body, "\n")
	a := pageAct{Line: ln, Text: strings.TrimRight(lines[ln-1], "\r"), Tool: l.Target, Items: []actItem{}, Tainted: d.Node.Meta.External}
	if a.Tainted {
		a.taint = "page " + nodeRef(id)
	}
	refuse := func(why string) (pageAct, error) {
		a.Refused, a.Why = why, tr(lang, "act.do."+why)
		return a, nil
	}
	sec, ok := p.Scope(l)
	if !ok {
		return refuse(actPlace)
	}
	server, tool, _ := strings.Cut(l.Target, "/")
	a.name = claudeToolName(server, tool)
	schema := c.inputSchema(a.name)
	if server == "" || tool == "" || schema == nil {
		return refuse(actTool)
	}
	if !c.authorHasTool(c.lineAuthor(id, a.Text), l.Target) {
		return refuse(actAuthor)
	}
	if a.arg, a.list, ok = actArg(schema); !ok {
		return refuse(actCompose)
	}
	if f := c.pool.facts(a.name, a.input(nil)); f.noAccount || f.value == gridNever && !f.gesture {
		return refuse(actGrid)
	}
	a.Items = c.actItems(id, p, sec, server, a.name)
	if read && len(a.Items) > 0 {
		ls := make([]itemLabel, len(a.Items))
		for i, it := range a.Items {
			ls[i] = itemLabel{ID: it.ID, Server: server}
		}
		c.itemWords(ctx, server, ls)
		for i := range a.Items {
			it := &a.Items[i]
			it.Title, it.Detail, it.External = ls[i].Title, ls[i].Detail, !c.trustsServer(server)
			it.Unread = it.Title == "" && it.Detail == ""
			it.Misleading = pageMisleads(p, it.Label, ls[i])
		}
	}
	n := len(a.Items)
	if n > 0 {
		q := a.question(a.Items)
		for k := 1; k <= n; k++ {
			a.Acts = append(a.Acts, c.kindSentence(q, k, lang))
			a.Verbs = append(a.Verbs, c.callVerb(q, k, lang))
		}
	}
	return a, nil
}

// actItems are the items a do: acts on now (see the top of this file).
func (c *core) actItems(node string, p *nodefile.Page, sec nodefile.Section, server, name string) []actItem {
	refs := map[string]refItem{}
	for _, r := range c.refItems(p) {
		if r.Server == server {
			refs[r.Label] = r
		}
	}
	done := c.actedOn(node, name)
	out := []actItem{}
	for _, u := range sec.Uses {
		r, ok := refs[u.Label]
		if !ok || u.Task == nodefile.TaskDone || u.Task == nodefile.TaskDropped || done[r.ID] {
			continue
		}
		row := p.Row(u.Item)
		if row == 0 || p.Unseen(u.Line) || slices.ContainsFunc(out, func(x actItem) bool { return x.ID == r.ID }) {
			continue
		}
		out = append(out, actItem{ID: r.ID, Label: r.Label, Line: u.Line, Row: row})
	}
	return out
}

// actedOn are the items of a node a call of tool name already names: one
// that waits on its card, or one said yes to within itemCardsKept that
// did not fail.
func (c *core) actedOn(node, name string) map[string]bool {
	out := map[string]bool{}
	asks := c.asksWhere(`node = ? AND batch != 0 AND call LIKE ? AND (state = ? OR state = ? AND decided >= ?)`,
		node, name+" %", startProposed, startApproved, time.Now().Add(-itemCardsKept).Unix())
	for _, x := range asks {
		if n, _, _ := strings.Cut(x.Call, " "); n != name {
			continue
		}
		if x.State == startProposed && !c.askWaits(x) {
			continue
		}
		if s, _ := c.callState(x.ID); x.State == startApproved && s == callFailed {
			continue
		}
		for _, id := range choiceItems(x) {
			out[id] = true
		}
	}
	return out
}

// actArg is the argument of a tool's input that takes the items' ids, as
// its schema says it: the one it requires, else (none required) the one
// named for ids (idArg); list when it takes a list of strings, else one
// string. ok is false when there is no such one argument, or the tool
// requires another: nothing else is filled in.
func actArg(schema map[string]any) (arg string, list, ok bool) {
	props, _ := schema["properties"].(map[string]any)
	var cands []string
	if req, _ := schema["required"].([]any); len(req) > 0 {
		for _, r := range req {
			if s, ok := r.(string); ok {
				cands = append(cands, s)
			}
		}
	} else {
		for k := range props {
			if idArg(k, nil, nil) {
				cands = append(cands, k)
			}
		}
	}
	if len(cands) != 1 {
		return "", false, false
	}
	p, _ := props[cands[0]].(map[string]any)
	switch {
	case schemaIs(p, "array"):
		items, _ := p["items"].(map[string]any)
		if !schemaIs(items, "string") {
			return "", false, false
		}
		return cands[0], true, true
	case schemaIs(p, "string"):
		return cands[0], false, true
	}
	return "", false, false
}

// schemaIs tells whether a schema's type is t (or a list holding t, its
// other types null).
func schemaIs(s map[string]any, t string) bool {
	switch x := s["type"].(type) {
	case string:
		return x == t
	case []any:
		has := false
		for _, e := range x {
			switch e {
			case t:
				has = true
			case "null":
			default:
				return false
			}
		}
		return has
	}
	return false
}

// input is the input of one call on items (all of them for a list; the
// first, else).
func (a pageAct) input(items []actItem) map[string]any {
	if !a.list {
		id := ""
		if len(items) > 0 {
			id = items[0].ID
		}
		return map[string]any{a.arg: id}
	}
	ids := []any{}
	for _, it := range items {
		ids = append(ids, it.ID)
	}
	return map[string]any{a.arg: ids}
}

// calls are a do:'s calls on items: one for them all, or one each.
func (a pageAct) calls(items []actItem) [][]actItem {
	if a.list {
		return [][]actItem{items}
	}
	var out [][]actItem
	for _, it := range items {
		out = append(out, []actItem{it})
	}
	return out
}

// question is a call of the do: on items, as a card says it (by count:
// labels with their ids only).
func (a pageAct) question(items []actItem) accessQuestion {
	raw, _ := json.Marshal(a.input(items))
	q := accessQuestion{Call: a.name + " []", Input: string(raw)}
	for _, it := range items {
		q.Labels = append(q.Labels, itemLabel{ID: it.ID})
	}
	return q
}

// lineAuthor is who wrote a line of node id's page: the author of the
// oldest of the versions, back from the last, that all hold it whole; ""
// when none does, or it goes back further than lineAuthorDepth.
func (c *core) lineAuthor(id, text string) string {
	vs, err := c.nodes.Versions(id)
	if err != nil {
		return ""
	}
	author := ""
	for i := len(vs) - 1; i >= 0; i-- {
		if len(vs)-i > lineAuthorDepth {
			return ""
		}
		d, err := c.nodes.Read(id, vs[i].N)
		if err != nil {
			return ""
		}
		body, err := d.Node.Body()
		if err != nil || !nodefile.HasLine(body, text) {
			break
		}
		author = vs[i].Author
	}
	return author
}

// lineAuthorDepth is how many versions back lineAuthor looks.
const lineAuthorDepth = 200

// authorHasTool tells whether a node's author has a tool (server/tool):
// the user, in the workspace or in the files (outside), has every tool;
// the brain, those of the catalog; a task, those its grant lends; a
// skill, those its last run was lent. Code (a value, a conversion) has
// none: it writes no action.
func (c *core) authorHasTool(author, tool string) bool {
	switch {
	case author == byUser || author == nodestore.Outside:
		server, name, _ := strings.Cut(tool, "/")
		_, ok := c.conf().Servers[server]
		return ok && name != ""
	case author == byBrain:
		return c.brainHasTool(tool)
	}
	task := ""
	if t, ok := strings.CutPrefix(author, "task:"); ok {
		task = t
	} else if s, ok := strings.CutPrefix(author, "skill:"); ok {
		if rs, err := c.skillRunsOf(s, 1); err == nil && len(rs) > 0 {
			task = rs[0].Task
		}
	}
	if task == "" {
		return false
	}
	t, err := c.store.task(task)
	if err != nil {
		return false
	}
	return c.lentTool(c.taskGrant(t))(tool)
}

// actYes is the user's yes on a do: of node id's page, on the items
// chosen (their ids, as its card drew them ticked): the do: composed
// again from the page as it is now, it runs on those of them it still
// acts on, kept as one calls card said yes to at once. It gives the
// card's ref.
func (c *core) actYes(ctx context.Context, id string, ref nodefile.LineRef, chosen []string, by string) (string, error) {
	lang := c.userLang()
	a, err := c.composeAct(ctx, id, ref, true, lang)
	if err != nil {
		return "", err
	}
	if a.Refused != "" {
		return "", fmt.Errorf("%w: %s", errActRefused, a.Why)
	}
	var items []actItem
	for _, it := range a.Items {
		if slices.Contains(chosen, it.ID) {
			items = append(items, it)
		}
	}
	if len(items) == 0 {
		return "", errActNone
	}
	server, _, _ := splitToolName(a.name)
	var batch int64
	var ids []int64
	for _, group := range a.calls(items) {
		input := a.input(group)
		raw, _ := json.Marshal(input)
		cctx, cancel := context.WithTimeout(ctx, cardReadTimeout)
		fields, _ := json.Marshal(cardFields(c.callContext(cctx, a.name, input), input))
		cancel()
		n := c.callNeed(a.name, input, c.pool.facts(a.name, input))
		var ls []itemLabel
		for _, it := range group {
			ls = append(ls, itemLabel{ID: it.ID, Server: server, Title: it.Title, Detail: it.Detail, External: it.External})
		}
		labels, _ := json.Marshal(ls)
		item := ""
		if len(group) == 1 {
			item = group[0].ID
		}
		res, err := c.store.db.Exec(`INSERT INTO access_asks (task_id, permission_id, server, category, account, call, input, tainted, created, node, item, labels, batch)
			VALUES ('', '', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, n.Server, n.Category, n.Account, a.name+" "+string(fields), string(raw),
			a.taint, time.Now().Unix(), id, item, string(labels), batch)
		aid, err := rowsID(res, err)
		if err != nil {
			return "", err
		}
		if batch == 0 {
			batch = aid
			c.store.db.Exec(`UPDATE access_asks SET batch = ? WHERE id = ?`, batch, aid)
		}
		ids = append(ids, aid)
	}
	cref := proposalRef(cardCalls, batch)
	c.syncCard(c.proposalOf(cardCalls, batch)) // its card, linked to the node
	xs, err := c.cardsWhere(`ref = ? AND state = ?`, cref, cardPending)
	if err != nil || len(xs) == 0 {
		return "", errNoCard
	}
	if _, err := c.decideCardChosen(ctx, xs[0].ID, ids, nil, by); err != nil {
		return "", err
	}
	log.Printf("page %s: do: %s on %d item(s), card %s", id, a.Tool, len(items), cref)
	return cref, nil
}

// actIn names a do: of a page: its line (from 1, in the body as the page
// read it) and the line's text as the page read it; for a yes, the items
// ticked, by id.
type actIn struct {
	Line  int      `json:"line"`
	Text  string   `json:"text"`
	Items []string `json:"items,omitempty"`
}

func (w *web) actRoutes(mux *webMux) {
	mux.Handle("POST /api/nodes/{id}/do", w.auth(w.handleNodeAct))
	mux.Handle("POST /api/nodes/{id}/do/yes", w.auth(w.handleNodeActYes))
}

// handleNodeAct is POST /api/nodes/{id}/do: a do: of the page as code
// composes it now (pageAct), its items read by their server: what its
// card draws. 404; 409 when the line is gone, there twice, or no do:.
func (w *web) handleNodeAct(rw http.ResponseWriter, r *http.Request, _ *session) {
	sum, ok := w.gestureNode(rw, r)
	if !ok {
		return
	}
	var in actIn
	if !gestureBody(rw, r, 16<<10, &in) {
		return
	}
	a, err := w.c.composeAct(r.Context(), sum.ID, nodefile.LineRef{Line: in.Line, Text: in.Text}, true, w.lang(r))
	if err != nil {
		writeActErr(rw, err, w.lang(r))
		return
	}
	writeJSON(rw, http.StatusOK, a)
}

// handleNodeActYes is POST /api/nodes/{id}/do/yes: the user's yes on a
// do:, on the items ticked (actYes). 200 with the card it made (card);
// 403 when the do: acts on nothing (refused, why in error); 409 when its
// line is gone, or none of the items is still under its heading; 410
// archived.
func (w *web) handleNodeActYes(rw http.ResponseWriter, r *http.Request, _ *session) {
	sum, ok := w.gestureNode(rw, r)
	if !ok {
		return
	}
	if !sum.Archived.IsZero() {
		writeJSON(rw, http.StatusGone, map[string]string{"error": "archived"})
		return
	}
	var in actIn
	if !gestureBody(rw, r, 64<<10, &in) {
		return
	}
	ref, err := w.c.actYes(r.Context(), sum.ID, nodefile.LineRef{Line: in.Line, Text: in.Text}, in.Items, "web")
	if err != nil {
		writeActErr(rw, err, w.lang(r))
		return
	}
	w.notify()
	writeJSON(rw, http.StatusOK, map[string]string{"card": ref})
}

// writeActErr answers a do: that could not be composed or run.
func writeActErr(rw http.ResponseWriter, err error, lang string) {
	switch {
	case errors.Is(err, errNoNode):
		writeJSON(rw, http.StatusNotFound, map[string]string{"error": err.Error()})
	case errors.Is(err, errActRefused):
		writeJSON(rw, http.StatusForbidden, map[string]string{"error": err.Error()})
	case errors.Is(err, errActNone):
		writeJSON(rw, http.StatusConflict, map[string]string{"error": tr(lang, "act.do.none")})
	case errors.Is(err, nodefile.ErrLineGone), errors.Is(err, nodefile.ErrLineTwice), errors.Is(err, errNotAct),
		errors.Is(err, errCardDecided), errors.Is(err, errNoCard):
		writeJSON(rw, http.StatusConflict, map[string]string{"error": err.Error()})
	default:
		writeJSON(rw, http.StatusBadRequest, map[string]string{"error": err.Error()})
	}
}
