package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Alexis-Marcel/eriphe-system/core/nodefile"
	"github.com/Alexis-Marcel/eriphe-system/core/nodestore"
)

// The graph workspace (#633), the one surface of the web workspace (/ and
// /espace, once the wizard is over, web_pages.go): the graph
// of nodes, their pages opened inside it, Eriphe's orb and the bar. It is a React
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
//     body (its Markdown page), the items its references name, and the
//     cards it waits on;
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
	mux.Handle("GET /espace/media", w.auth(w.handleMedia)) // web_media.go
	mux.Handle("GET /api/nodes/{id}", w.auth(w.handleNodePage))
	mux.Handle("GET /api/nodes/{id}/trace", w.auth(w.handleNodeTrace))
	mux.Handle("GET /api/nodes/{id}/md", w.auth(w.handleNodeMD))
	mux.Handle("GET /api/proposals/{ref}", w.auth(w.handleProposal)) // web_proposals.go
	mux.Handle("GET /api/report/turns", w.auth(w.handleTurnReport))  // turn_report.go
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
	// configuration; "" once it can. The bar says it, and the way to
	// the wizard.
	Setup string `json:"setup,omitempty"`
	// Attach is what the bar may attach to one message (attachments.go):
	// how many files, and how large each, in bytes.
	Attach espaceAttach `json:"attach"`
	// Voice is what Settings → Voice set (voice.go): Listen, a service
	// hears the user, so the bar has a microphone.
	Voice espaceVoice `json:"voice"`
}

type espaceVoice struct {
	Listen bool `json:"listen"`
}

// espaceAttach is the bar's limits on attachments.
type espaceAttach struct {
	Many int `json:"many"`
	Size int `json:"size"`
}

func (w *web) handleEspaceBoot(rw http.ResponseWriter, r *http.Request, s *session) {
	lang := w.lang(r)
	w.c.noteUserLang(lang) // Eriphe's lines follow it
	b := espaceBoot{Lang: lang, Name: assistantName(), CSRF: s.CSRF, Strings: espaceWords(lang, assistantName()),
		View: w.c.view(), Setup: w.setupState(), Attach: espaceAttach{Many: maxAttachments, Size: maxAttachmentSize},
		Voice: espaceVoice{Listen: w.c.voiceListen().on()}}
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

// graphNode is a node's row as the page gets it: what the graph draws.
type graphNode struct {
	ID    string `json:"id"`
	Title string `json:"title,omitempty"`
	// TitleExternal: its title may be someone else's words, shown marked
	TitleExternal bool         `json:"titleExternal,omitempty"`
	Parent        string       `json:"parent,omitempty"`
	Links         []string     `json:"links,omitempty"`
	Maintained    []string     `json:"maintained,omitempty"` // the skills that maintain it: the graph draws its satellite (node_home.go)
	Status        string       `json:"status"`               // running, waits, done or idle, computed (node_status.go)
	Created       time.Time    `json:"created"`
	Updated       time.Time    `json:"updated"`
	Done          *time.Time   `json:"done,omitempty"`
	External      bool         `json:"external,omitempty"`
	Version       int          `json:"version"`
	Line          string       `json:"line,omitempty"`    // while it runs: its task's last word of where it is
	Unasked       bool         `json:"unasked,omitempty"` // a skill or an event opened it, not the user (node_origin.go)
	Glance        *graphGlance `json:"glance,omitempty"`  // a figure of one of its sources, shown at a glance when it moved (node_glance.go)
	Weight        float64      `json:"weight"`            // how much it weighs, computed once: the graph draws by it (node_weight.go)
}

// graphRow is a node's row.
func (c *core) graphRow(sum nodestore.Summary) graphNode {
	return c.graphRowWith(sum, c.statusFacts(sum.ID))
}

// graphRowWith is a node's row, its status from f.
func (c *core) graphRowWith(sum nodestore.Summary, f statusFacts) graphNode {
	g := graphNode{ID: sum.ID, Title: sum.Title, TitleExternal: sum.TitleExternal(), Parent: sum.Parent, Links: sum.Links, Maintained: f.homes[sum.ID], Status: f.of(sum),
		Created: sum.Created, Updated: sum.Updated, External: sum.External, Version: sum.Version, Unasked: sum.Unasked()}
	if !sum.Done.IsZero() {
		d := sum.Done
		g.Done = &d
	}
	depth, ok := f.depths[sum.ID]
	incoming := f.incoming[sum.ID]
	if !ok { // one row: read in the index
		depth = c.nodeDepth(sum)
		incoming, _ = c.nodes.Incoming(sum.ID)
	}
	g.Weight = nodeWeight(depth, incoming, g.Status, sum.Updated, time.Now())
	g.Glance = c.nodeGlance(sum.ID)
	if p, ok := c.progressOf(sum.ID); ok && g.Status == nodeRunning && sum.Archived.IsZero() {
		g.Line = p.Text
	}
	return g
}

// graphRows is the graph: every node not archived, in a constant number
// of queries (statusFacts, warmNodeCaches).
func (c *core) graphRows() ([]graphNode, error) {
	if c.nodes == nil {
		return []graphNode{}, nil
	}
	xs, err := c.nodes.Graph()
	if err != nil {
		return nil, err
	}
	c.warmNodeCaches(xs)
	f := c.statusFacts("")
	f.depths, f.incoming = nodeDepths(xs), nodeIncoming(xs)
	out := make([]graphNode, 0, len(xs))
	for _, x := range xs {
		out = append(out, c.graphRowWith(x, f))
	}
	return out, nil
}

// nodePage is a node's page: its row, the version shown and the others,
// its body, and its cards: at its foot, those that still ask something
// (pendingAsks) its lines do not place (a card: link, an item's row);
// the others, decided, are its settled ones, the last first, for its ⋯
// menu (« Déjà décidé »).
type nodePage struct {
	Node     graphNode     `json:"node"`
	Version  int           `json:"version"` // the version shown
	Archived *time.Time    `json:"archived,omitempty"`
	Versions []pageVersion `json:"versions"`
	Shift    int           `json:"shift,omitempty"` // 1 when its creation was empty: the user's v1 is its version 2
	// Body is the node's body as its file holds it, the page the
	// workspace reads and draws (web/espace, page/read.ts: the lines it
	// ticks are this body's, as nodefile.ReadPage counts them).
	Body string `json:"body"`
	// Items are the body's references to catalog servers' items, each
	// where it is written, with the card that waits on it (page_items.go);
	// Placed the cards its lines carry (on an item's row, or where a
	// card: link places one): their place, not the foot. Go places
	// them; the page draws them there.
	Items  []pageItem `json:"items"`
	Placed []string   `json:"placed"`
	// Cards: those that still ask, at its foot (but those placed);
	// Settled: those decided, for the « ⋯ » menu.
	Cards   []proposalView `json:"cards"`
	Settled []proposalView `json:"settled"`
	// Refs: the node each [[name]] of its text opens, by the name as
	// written; a name no node has is not there (the page greys it).
	Refs map[string]pageRef `json:"refs"`
	// Alive: what keeps it alive: its skills and monitors (node_alive.go).
	Alive *pageAlive `json:"alive,omitempty"`
	// Origin: who wanted it, as code recorded it (node_origin.go).
	Origin pageOrigin `json:"origin"`
	// Tags and Props: its tags and its other properties, as its version
	// says them (nodefile/props.go), shown read-only.
	Tags  []string   `json:"tags,omitempty"`
	Props []pageProp `json:"props,omitempty"`
}

// pageProp is a property of a node: its name, its value or list of them,
// and its kind (text, number, date; a list's first value's). Media: the
// node's files it names (media:<name>), as the page loads them.
type pageProp struct {
	Key   string      `json:"key"`
	Value any         `json:"value"`
	Kind  string      `json:"kind"`
	Media []pageMedia `json:"media,omitempty"`
}

// pageMedia is one of a node's files a property names: its address on
// eriphe-core (web_media.go), and whether it is an image (else a file to
// download, a PDF).
type pageMedia struct {
	Src   string `json:"src"`
	Image bool   `json:"image"`
}

// pageProps are node id's tags and its other properties.
func (c *core) pageProps(id string, m nodefile.Meta) ([]string, []pageProp) {
	var props []pageProp
	for _, pr := range m.Props() {
		if strings.EqualFold(pr.Key, nodefile.TagsKey) {
			continue
		}
		p := pageProp{Key: pr.Key, Value: pr.Any(), Kind: pr.Values[0].Kind}
		for _, v := range pr.Values {
			if s, ok := v.Any().(string); ok {
				if src := c.nodeFileSrc(id, s); src != "" {
					p.Media = append(p.Media, pageMedia{Src: src, Image: mediaIsImage(s)})
				}
			}
		}
		props = append(props, p)
	}
	return m.Tags(), props
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
	p := nodePage{Node: c.graphRow(sum), Version: d.Version.N, Versions: []pageVersion{}, Cards: []proposalView{}, Refs: map[string]pageRef{}}
	if !sum.Archived.IsZero() {
		a := sum.Archived
		p.Archived = &a
	}
	p.Alive = c.nodeAlive(sum, lang)
	p.Origin = c.nodeOriginLine(d.Node.Meta.OpenedBy, lang)
	p.Tags, p.Props = c.pageProps(id, d.Node.Meta)
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
	// Every way the text writes a name is a key of its own, an embed's
	// (![[Name]]) too.
	var names []string
	p.Items, p.Placed = []pageItem{}, []string{}
	if body, err := d.Node.Body(); err == nil {
		p.Body = body
		for _, l := range nodefile.WikiLinks(body) {
			names = append(names, l.Name)
		}
		pg := nodefile.ReadPage(body)
		names = append(names, embedNames(pg.Blocks)...)
		p.Items = c.pageItems(id, pg)
		p.Placed = c.pagePlaced(id, pg, p.Items)
	}
	if found, err := c.nodes.Resolve(id, names); err == nil {
		for name, x := range found {
			p.Refs[name] = pageRef{ID: x.ID, Title: x.Title, TitleExternal: x.TitleExternal(), External: x.External, Archived: !x.Archived.IsZero()}
		}
	}
	placed := map[string]bool{}
	for _, ref := range p.Placed {
		placed[ref] = true
	}
	p.Cards, p.Settled = []proposalView{}, []proposalView{}
	for _, v := range w.nodeCards(id, lang) {
		switch {
		case !pendingAsks(v):
			if len(p.Settled) < settledMax {
				p.Settled = append(p.Settled, v)
			}
		case !placed[v.Ref]:
			p.Cards = append(p.Cards, v)
		}
	}
	return p, nil
}

// embedNames are the names a page's embeds (![[Name]]) write, in its
// lists and callouts too.
func embedNames(bs []nodefile.PageBlock) []string {
	var out []string
	for _, b := range bs {
		switch b.Kind {
		case nodefile.KindEmbed:
			out = append(out, b.Name)
		case nodefile.KindCallout:
			out = append(out, embedNames(b.Body)...)
		case nodefile.KindList:
			for _, it := range b.Items {
				out = append(out, embedNames(it.Detail)...)
			}
		}
	}
	return out
}

// settledMax is the most decided cards a page lists in its ⋯ menu.
const settledMax = 20

// pendingAsks tells whether a card still asks something of the user on
// its page: it waits, or its yes runs. Done, failed, declined or
// withdrawn, it is decided and leaves the page (card_outcome.go).
func pendingAsks(v proposalView) bool {
	return v.State == cardPending || v.State == cardApproved && v.Exec == callRunning
}

// nodeCards are the cards a node waits on, or did: those linked to it
// (node_turn.go), the last first.
func (w *web) nodeCards(id, lang string) []proposalView {
	out := []proposalView{}
	for _, ref := range w.c.nodeCardRefs(id) {
		xs, err := w.c.cardsWhere(`ref = ?`, ref)
		if err != nil || len(xs) == 0 {
			continue
		}
		x, p := w.c.liveCard(xs[0])
		if p.Kind == "" {
			continue // no proposal of any kind: nothing to draw or answer
		}
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
	// body is what the user edits: the page, without the front matter
	// (web_gestures.go).
	writeJSON(rw, http.StatusOK, map[string]any{"md": string(d.Raw), "body": nodeBody(d.Raw), "version": d.Version.N, "hash": d.Version.Hash, "last": d.Last})
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
