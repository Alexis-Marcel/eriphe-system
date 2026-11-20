package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Alexis-Marcel/eriphe-system/core/a2ui"
	"github.com/Alexis-Marcel/eriphe-system/core/nodefile"
	"github.com/Alexis-Marcel/eriphe-system/core/nodestore"
)

// The graph workspace (#633), the one surface of the web workspace (/ and
// /espace, once the wizard is over, web_pages.go): the graph
// of nodes, their pages opened inside it, and one console. It is a React
// app (web/espace), built into web/static/espace and embedded with the
// other static files; eriphe-core serves it from a small HTML shell
// (templates/espace.html). The app gets its words, the CSRF token and the
// assistant's name from /espace/boot, never from inline code: the CSP
// stays strict (web.go).
//
// Go reads the nodes; the page gets them ready to draw:
//
//   - the graph, its rows, by the stream (web_sse.go, graph_feed.go);
//   - a node's page (GET /api/nodes/{id}): its row, its versions, its
//     body as parts (markdown text, or a block ready for the A2UI
//     renderer: its components and its data, the sources' last values in
//     it), and the cards it waits on;
//   - its trace (GET /api/nodes/{id}/trace), as entries;
//   - its file (GET /api/nodes/{id}/md), as it is, and its body as the
//     user edits it.
//
// What the user does there is in web_gestures.go.
//
// A title that may be someone else's words (written on an external node,
// not trusted) is sent as it is with title_external, and the page marks it
// so: it never looks like a trusted one, and nothing acts on it. A node's
// goal is the brain's, never shown.

func (w *web) graphRoutes(mux *webMux) {
	mux.Handle("GET /espace", w.auth(w.handleHome)) // web_pages.go: after the wizard
	mux.Handle("GET /espace/boot", w.auth(w.handleEspaceBoot))
	mux.Handle("GET /espace/catalog/v1.json", w.auth(w.handleEspaceCatalog))
	mux.Handle("GET /espace/media", w.auth(w.handleMedia)) // web_media.go
	mux.Handle("GET /api/nodes/{id}", w.auth(w.handleNodePage))
	mux.Handle("GET /api/nodes/{id}/trace", w.auth(w.handleNodeTrace))
	mux.Handle("GET /api/nodes/{id}/md", w.auth(w.handleNodeMD))
	mux.Handle("GET /api/proposals/{ref}", w.auth(w.handleProposal)) // web_proposals.go
	mux.Handle("GET /api/proposals", w.auth(w.handleDayProposals))   // web_proposals.go
}

// handleEspace is the app's page.
func (w *web) handleEspace(rw http.ResponseWriter, r *http.Request, s *session) {
	w.render(rw, r, s, "espace", "espace", page{Title: "nav.espace", Data: w.c.view().Theme}) // the theme chosen, before the app draws
}

// espaceBoot is what the app needs before its first paint.
type espaceBoot struct {
	Lang    string            `json:"lang"`
	Name    string            `json:"name"`
	CSRF    string            `json:"csrf"`
	Strings map[string]string `json:"strings"`
	View    espaceView        `json:"view"` // the pages open, the display settings (web_gestures.go)
	// Setup says Eriphe cannot think yet: "noai" while its wizard was left
	// for later (web_welcome.go), "nobrain" with no brain in the
	// configuration; "" once it can. The console says it, and the way to
	// the wizard.
	Setup string `json:"setup,omitempty"`
}

func (w *web) handleEspaceBoot(rw http.ResponseWriter, r *http.Request, s *session) {
	lang := w.lang(r)
	w.c.noteUserLang(lang) // Eriphe's lines follow it
	b := espaceBoot{Lang: lang, Name: assistantName(), CSRF: s.CSRF, Strings: espaceWords(lang, assistantName()),
		View: w.c.view(), Setup: w.setupState()}
	rw.Header().Set("Content-Type", "application/json")
	rw.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(rw).Encode(b)
}

// setupState is what Eriphe lacks to think, for the boot: "" when nothing.
func (w *web) setupState() string {
	switch w.brainState() {
	case "none":
		return "nobrain"
	case "noai":
		return "noai"
	}
	return ""
}

// handleEspaceCatalog is GET /espace/catalog/v1.json: the catalog of the
// components a block is made of (core/a2ui), which the app's renderer
// reads. The path carries the catalog's version: a v2 gets a path of its
// own.
func (w *web) handleEspaceCatalog(rw http.ResponseWriter, r *http.Request, _ *session) {
	rw.Header().Set("Content-Type", "application/json")
	rw.Header().Set("Cache-Control", "no-cache")
	rw.Write(a2ui.CatalogJSON())
}

// graphNode is a node's row as the page gets it: what the graph draws.
type graphNode struct {
	ID    string `json:"id"`
	Title string `json:"title,omitempty"`
	// TitleExternal: its title may be someone else's words, shown marked
	TitleExternal bool       `json:"titleExternal,omitempty"`
	Parent        string     `json:"parent,omitempty"`
	Links         []string   `json:"links,omitempty"`
	Lifetime      string     `json:"lifetime"`
	State         string     `json:"state"`
	Created       time.Time  `json:"created"`
	Updated       time.Time  `json:"updated"`
	Done          *time.Time `json:"done,omitempty"`
	External      bool       `json:"external,omitempty"`
	Version       int        `json:"version"`
	Waits         bool       `json:"waits,omitempty"`   // one of its cards waits for the user, or a source marked attention lists items (node_attention.go)
	Line          string     `json:"line,omitempty"`    // while it runs: its task's last word of where it is
	Unasked       bool       `json:"unasked,omitempty"` // a skill or an event opened it, not the user (node_origin.go)
}

// graphRow is a node's row.
func (c *core) graphRow(sum nodestore.Summary) graphNode {
	g := graphNode{ID: sum.ID, Title: sum.Title, TitleExternal: sum.TitleExternal(), Parent: sum.Parent, Links: sum.Links, Lifetime: string(sum.Lifetime), State: sum.State,
		Created: sum.Created, Updated: sum.Updated, External: sum.External, Version: sum.Version, Unasked: sum.Unasked()}
	if !sum.Done.IsZero() {
		d := sum.Done
		g.Done = &d
	}
	g.Waits = c.nodeCardWaits(sum.ID) || c.nodeAttention(sum.ID)
	if p, ok := c.progressOf(sum.ID); ok && sum.State == nodestore.Running {
		g.Line = p.Text
	}
	return g
}

// graphRows is the graph: every node not archived.
func (c *core) graphRows() ([]graphNode, error) {
	if c.nodes == nil {
		return []graphNode{}, nil
	}
	xs, err := c.nodes.Graph()
	if err != nil {
		return nil, err
	}
	out := make([]graphNode, 0, len(xs))
	for _, x := range xs {
		out = append(out, c.graphRow(x))
	}
	return out, nil
}

// nodePage is a node's page: its row, the version shown and the others,
// its body in parts, and its cards: those its document does not place
// (a card block places one where its author wrote it).
type nodePage struct {
	Node     graphNode      `json:"node"`
	Version  int            `json:"version"` // the version shown
	Archived *time.Time     `json:"archived,omitempty"`
	Versions []pageVersion  `json:"versions"`
	Shift    int            `json:"shift,omitempty"` // 1 when its creation was empty: the user's v1 is its version 2
	Parts    []pagePart     `json:"parts"`
	Cards    []proposalView `json:"cards"`
	// Refs: the node each [[name]] of its text opens, by the name as
	// written; a name no node has is not there (the page greys it).
	Refs map[string]pageRef `json:"refs"`
	// Alive: a standing node's sources, monitors and skills (node_alive.go).
	Alive *pageAlive `json:"alive,omitempty"`
	// Origin: who wanted it, as code recorded it (node_origin.go).
	Origin pageOrigin `json:"origin"`
}

// pageRef is the node a [[link]] of a page opens: its id, its title as
// the graph gives it (titleExternal when it may be someone else's words:
// the page marks it so, never passing it for a trusted one), and whether
// it is archived (it shows so, and opens nothing).
type pageRef struct {
	ID            string `json:"id"`
	Title         string `json:"title,omitempty"`
	TitleExternal bool   `json:"titleExternal,omitempty"` // its title may be someone else's words
	External      bool   `json:"external,omitempty"`
	Archived      bool   `json:"archived,omitempty"`
}

// pageVersion is a version of a node: its number, who made it, when.
// Code's own versions (a source's new values) are not listed, but the
// last one; nor a creation with nothing in it (a step opened for its task
// to write): the user numbers the versions from the first with something
// in it (shift).
type pageVersion struct {
	N      int       `json:"n"`
	Author string    `json:"author"`
	At     time.Time `json:"at"`
}

// pagePart is a piece of a node's body: markdown text, or a block.
type pagePart struct {
	Text  string     `json:"text,omitempty"`
	Block *pageBlock `json:"block,omitempty"`
}

// pageBlock is a block ready to draw: its components and its data, with
// the sources' last values at /<name> and the user's marks at /marks; or,
// when it does not read as a block, what is wrong (its raw JSON is the
// file's, in the .md tab). A card block gives its card's ref, and the
// card as code reads it from its records (Proposal: what it says, what
// its yes does, where it stands), only when it is one of the node's own
// cards and the block is not external: else none, and the page says the
// card is not found, with no Oui or Non.
type pageBlock struct {
	Name       string           `json:"name"`
	External   bool             `json:"external,omitempty"`
	Components []a2ui.Component `json:"components"`
	Data       map[string]any   `json:"data"`
	Error      string           `json:"error,omitempty"`
	Card       string           `json:"card,omitempty"`
	Proposal   *proposalView    `json:"proposal,omitempty"`
	// Lists: the Lists the user may pick items in, and the actions on
	// them (list_actions.go).
	Lists []pageList `json:"lists,omitempty"`
}

var errNoNode = errors.New("no such node")

// nodePageOf is a node's page, at a version (0: the last).
func (w *web) nodePageOf(id string, version int, lang string) (nodePage, error) {
	c := w.c
	if c.nodes == nil || !nodefile.IDOK(id) {
		return nodePage{}, errNoNode
	}
	sum, err := c.nodes.Get(id)
	if err != nil {
		return nodePage{}, errNoNode
	}
	d, err := c.nodes.Read(id, version)
	if err != nil {
		return nodePage{}, errNoNode
	}
	p := nodePage{Node: c.graphRow(sum), Version: d.Version.N, Versions: []pageVersion{}, Parts: []pagePart{}, Cards: []proposalView{}, Refs: map[string]pageRef{}}
	if !sum.Archived.IsZero() {
		a := sum.Archived
		p.Archived = &a
	}
	p.Alive = c.nodeAlive(sum, lang)
	p.Origin = c.nodeOriginLine(d.Node.Meta.OpenedBy, lang)
	if vs, err := c.nodes.Versions(id); err == nil {
		if len(vs) > 1 && vs[0].N == 1 && d.Version.N != 1 {
			if first, err := c.nodes.Read(id, 1); err == nil && strings.TrimSpace(nodeBody(first.Raw)) == "" {
				p.Shift = 1
			}
		}
		for i, v := range vs {
			if v.Author == nodeByCode && i < len(vs)-1 && v.N != d.Version.N {
				continue
			}
			if v.N <= p.Shift {
				continue
			}
			p.Versions = append(p.Versions, pageVersion{N: v.N, Author: v.Author, At: v.At})
		}
	}
	// The sources' last values are the reads' at the last version; an
	// older one shows its own snapshot.
	var reads []nodeRead
	if d.Version.N == d.Last {
		reads = c.nodeReads(id)
	}
	for _, part := range d.Node.Parts() {
		if part.Block == nil {
			p.Parts = append(p.Parts, pagePart{Text: part.Text})
			continue
		}
		b := c.blockView(id, part.Block, reads)
		if b.Error == "" && b.Card == "" {
			b.Lists = c.blockLists(part.Block, b.Data, lang)
		}
		if b.Card != "" && b.Error == "" {
			b.Proposal = w.placedCard(id, part.Block, lang)
		}
		p.Parts = append(p.Parts, pagePart{Block: &b})
	}
	// Every way the text writes a name is a key of its own.
	var names []string
	for _, part := range d.Node.Parts() {
		for _, l := range nodefile.WikiLinks(part.Text) {
			names = append(names, l.Name)
		}
	}
	if found, err := c.nodes.Resolve(id, names); err == nil {
		for name, x := range found {
			p.Refs[name] = pageRef{ID: x.ID, Title: x.Title, TitleExternal: x.TitleExternal(), External: x.External, Archived: !x.Archived.IsZero()}
		}
	}
	placed := map[string]bool{}
	for _, part := range p.Parts {
		if part.Block != nil && part.Block.Proposal != nil {
			placed[part.Block.Proposal.Ref] = true
		}
	}
	for _, v := range w.nodeCards(id, lang) {
		if !placed[v.Ref] {
			p.Cards = append(p.Cards, v)
		}
	}
	return p, nil
}

// placedCard is the card a card block of node id places, as its records
// say it now: pending, decided, or gone (withdrawn with its node, #662).
// None for a ref that is not one of the node's cards, nor from a block
// someone else's words may have written: such a block forges nothing.
func (w *web) placedCard(id string, b *nodefile.Block, lang string) *proposalView {
	if b.External || !w.c.nodeHasCard(id, b.Card) {
		return nil
	}
	xs, err := w.c.cardsWhere(`ref = ?`, b.Card)
	if err != nil || len(xs) == 0 {
		return nil
	}
	x, p := w.c.liveCard(xs[0])
	v := w.proposalView(x, p, lang)
	return &v
}

// blockView is a block of node id ready to draw. Its media addresses go
// through eriphe-core's endpoint (web_media.go), never loaded from
// outside: one a source gave, or one the author wrote when the block is
// not external (then a model that read something from outside may have
// written it, to carry what it read out in its address: A2UI #4045), else
// it is dropped. A media:<name> address is one of node id's own files,
// whoever wrote it: it carries nothing out.
// A Link's address goes as it is, but one that is not an address a Link
// may open goes as "".
func (c *core) blockView(id string, b *nodefile.Block, reads []nodeRead) pageBlock {
	v := pageBlock{Name: b.Name, External: b.External, Components: []a2ui.Component{}, Data: map[string]any{}}
	if err := b.Validate(); err != nil {
		v.Error = err.Error()
		return v
	}
	if b.Card != "" {
		v.Card = b.Card // the card itself: placedCard, by the page
		return v
	}
	if b.Data != nil {
		json.Unmarshal(b.Data, &v.Data)
		if v.Data == nil {
			v.Data = map[string]any{}
		}
	}
	var snap map[string]any
	json.Unmarshal(b.Snapshot, &snap)
	for name := range b.Sources {
		if val, ok := snap[name]; ok {
			v.Data[name] = val
		}
		for _, r := range reads {
			if r.Block == b.Name && r.Name == name && r.Value != nil {
				v.Data[name] = r.Value
			}
		}
	}
	// The items' notes on them (list_sections.go): the values are this
	// view's own, read for it.
	c.noteSources(id, b, v.Data)
	marks := map[string]any{}
	for list, items := range b.Marks {
		m := map[string]any{}
		for item, mark := range items {
			m[item] = mark
		}
		marks[list] = m
	}
	v.Data[nodefile.MarksKey] = marks

	var list []map[string]any
	json.Unmarshal(b.Components, &list)
	authorOK := !b.External
	for _, comp := range list {
		kind, _ := comp["component"].(string)
		for prop, val := range comp {
			if s, ok := val.(string); ok && a2ui.IsMedia(kind, prop) {
				comp[prop] = c.mediaOrDrop(id, s, authorOK)
			}
			if s, ok := val.(string); ok && a2ui.IsLink(kind, prop) {
				comp[prop] = linkOrDrop(s)
			}
		}
	}
	unfilterLists(list) // code filters, the page shows the rows it gets (list_sections.go)
	raw, _ := json.Marshal(list)
	json.Unmarshal(raw, &v.Components)
	if v.Components == nil {
		v.Components = []a2ui.Component{}
	}
	for _, bound := range a2ui.Eriphe().BoundPaths(b.Components) {
		at := tokens(bound.Path)
		if _, ok := b.Sources[at0(at)]; ok {
			at = unfiltered(at) // under a List that filters: each item of the source's
		}
		if bound.Link {
			rewriteAt(v.Data, at, linkOrDrop)
		}
		if !bound.Media || len(at) == 0 {
			continue
		}
		_, fromSource := b.Sources[at[0]]
		ok := fromSource || authorOK
		rewriteAt(v.Data, at, func(u string) string { return c.mediaOrDrop(id, u, ok) })
	}
	return v
}

// sourceEvents are what "source" events send of a block's source: its
// node, block and name, its value at /<name> as the page draws it (its
// items' notes on them), and the rows of the Lists the page must know
// that show it (blockLists: those that filter it, those the user picks
// in). block "": each block of the node that reads a source so named (its
// items' notes changed). None when the node or the source is no more.
func (c *core) sourceEvents(id, block, name string) []map[string]any {
	if c.nodes == nil {
		return nil
	}
	d, err := c.nodes.Read(id, 0)
	if err != nil {
		return nil
	}
	var out []map[string]any
	var reads []nodeRead
	for _, b := range d.Node.Blocks() {
		if block != "" && b.Name != block {
			continue
		}
		if _, ok := b.Sources[name]; !ok {
			continue
		}
		if reads == nil {
			reads = c.nodeReads(id)
		}
		v := c.blockView(id, b, reads)
		val, ok := v.Data[name]
		if !ok || v.Error != "" {
			continue
		}
		ev := map[string]any{"node": id, "block": b.Name, "name": name, "value": val}
		if lists := c.blockLists(b, v.Data, "fr", name); len(lists) > 0 {
			rows := make([]map[string]any, 0, len(lists))
			for _, l := range lists {
				rows = append(rows, map[string]any{"component": l.Component, "items": l.Items})
			}
			ev["lists"] = rows
		}
		out = append(out, ev)
	}
	return out
}

// nodeCards are the cards a node waits on, or did: those linked to it
// (node_turn.go), the last first.
func (w *web) nodeCards(id, lang string) []proposalView {
	out := []proposalView{}
	rows, err := w.c.store.db.Query(`SELECT ref FROM node_cards WHERE node = ? ORDER BY created DESC`, id)
	if err != nil {
		return out
	}
	var refs []string
	for rows.Next() {
		var ref string
		if rows.Scan(&ref) == nil {
			refs = append(refs, ref)
		}
	}
	rows.Close()
	for _, ref := range refs {
		xs, err := w.c.cardsWhere(`ref = ?`, ref)
		if err != nil || len(xs) == 0 {
			continue
		}
		x, p := w.c.liveCard(xs[0])
		out = append(out, w.proposalView(x, p, lang))
	}
	return out
}

// handleNodePage is GET /api/nodes/{id}: a node's page, at a version
// (?version=, the last by default); 404 for a node there is not.
func (w *web) handleNodePage(rw http.ResponseWriter, r *http.Request, _ *session) {
	version, _ := strconv.Atoi(r.URL.Query().Get("version"))
	p, err := w.nodePageOf(r.PathValue("id"), max(0, version), w.lang(r))
	if err != nil {
		writeJSON(rw, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	rw.Header().Set("Cache-Control", "no-store")
	writeJSON(rw, http.StatusOK, p)
}

// traceEntry is an entry of a node's trace: when, who, what.
type traceEntry struct {
	At     time.Time `json:"at"`
	Author string    `json:"author"` // brain, user, task:<id>, eriphe (code), outside
	Text   string    `json:"text"`
}

// traceEntries reads a trace's entries (nodestore's AppendTrace: a
// heading "## <time> · <author>", then the text), oldest first.
func traceEntries(raw []byte) []traceEntry {
	out := []traceEntry{}
	var cur *traceEntry
	var body []string
	flush := func() {
		if cur != nil {
			cur.Text = strings.TrimSpace(strings.Join(body, "\n"))
			out = append(out, *cur)
		}
		cur, body = nil, nil
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if head, ok := strings.CutPrefix(line, "## "); ok {
			at, author, ok := strings.Cut(head, " · ")
			if t, err := time.Parse(time.RFC3339, at); ok && err == nil {
				flush()
				cur = &traceEntry{At: t, Author: author}
				continue
			}
		}
		if cur != nil {
			body = append(body, line)
		}
	}
	flush()
	return out
}

// handleNodeTrace is GET /api/nodes/{id}/trace: a node's trace, as
// entries; 404 for a node there is not.
func (w *web) handleNodeTrace(rw http.ResponseWriter, r *http.Request, _ *session) {
	id := r.PathValue("id")
	if w.c.nodes == nil || !nodefile.IDOK(id) {
		writeJSON(rw, http.StatusNotFound, map[string]string{"error": errNoNode.Error()})
		return
	}
	raw, err := w.c.nodes.Trace(id)
	if err != nil {
		writeJSON(rw, http.StatusNotFound, map[string]string{"error": errNoNode.Error()})
		return
	}
	rw.Header().Set("Cache-Control", "no-store")
	writeJSON(rw, http.StatusOK, map[string]any{"entries": traceEntries(raw)})
}

// handleNodeMD is GET /api/nodes/{id}/md: a node's file as it is, at a
// version (?version=), with the hash of that version (what a write
// expects); 404 for a node there is not.
func (w *web) handleNodeMD(rw http.ResponseWriter, r *http.Request, _ *session) {
	id := r.PathValue("id")
	version, _ := strconv.Atoi(r.URL.Query().Get("version"))
	if w.c.nodes == nil || !nodefile.IDOK(id) {
		writeJSON(rw, http.StatusNotFound, map[string]string{"error": errNoNode.Error()})
		return
	}
	d, err := w.c.nodes.Read(id, max(0, version))
	if err != nil {
		writeJSON(rw, http.StatusNotFound, map[string]string{"error": errNoNode.Error()})
		return
	}
	rw.Header().Set("Cache-Control", "no-store")
	// body is what the user edits: the author's view of the body, without
	// the front matter and code's fields (web_gestures.go).
	writeJSON(rw, http.StatusOK, map[string]any{"md": string(d.Raw), "body": authorBody(d), "version": d.Version.N, "hash": d.Version.Hash, "last": d.Last})
}

// espaceWords are the app's words in a language, with the assistant's name.
func espaceWords(lang, name string) map[string]string {
	out := map[string]string{}
	for k := range espaceStrings["fr"] {
		in := lang
		s, ok := espaceStrings[in][k]
		if !ok {
			s, in = espaceStrings["fr"][k], "fr"
		}
		out[k] = withName(in, s, name)
	}
	return out
}

// espaceStringsFile is the words the app uses on its dev server, with no
// eriphe-core behind (web/espace/src/strings.json): a test keeps it the
// same as the tables above.
func espaceStringsFile() []byte {
	langs := map[string]map[string]string{}
	for _, lang := range []string{"fr", "en"} {
		langs[lang] = espaceWords(lang, defaultDisplayName)
	}
	// Sorted keys, two-space indent: json.Marshal sorts map keys.
	raw, _ := json.MarshalIndent(langs, "", "  ")
	return append(raw, '\n')
}

// espaceKeys lists the keys of a language, for the tests.
func espaceKeys(lang string) []string {
	var keys []string
	for k := range espaceStrings[lang] {
		if strings.HasPrefix(k, "espace.") || k == "nav.espace" {
			keys = append(keys, k)
		}
	}
	slices.Sort(keys)
	return keys
}
