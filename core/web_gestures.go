package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"math"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/Alexis-Marcel/eriphe-system/core/a2ui"
	"github.com/Alexis-Marcel/eriphe-system/core/nodefile"
	"github.com/Alexis-Marcel/eriphe-system/core/nodestore"
)

// What the user does on the graph workspace (#633), kept by eriphe-core:
//
//   - a block's button that asks Eriphe, or a page's ask: link: the ask its
//     author wrote, read again from the stored block or page, sent as the
//     user's message to its node; it acts on nothing (a yes is a card's,
//     web_convo_cards.go);
//   - a task of a node's page ticked (- [x]): its line written, on the
//     last version, by the user;
//   - the archive of a node and what is under it, and its reopening, the
//     user's alone (archiveNode, reopenNode);
//   - an edit of a node's text in its .md tab, made on the version it was
//     read at (a newer one is a conflict, never overwritten);
//   - the pages open and the display settings (kv espace_view): the
//     workspace as the user left it, on any of their screens. Where the
//     bubbles are is not kept: the graph lays itself out afresh.
//
// Every route is the workspace's session, its CSRF token in X-CSRF-Token.

func (w *web) gestureRoutes(mux *webMux) {
	mux.Handle("PUT /api/nodes/{id}/md", w.auth(w.handleNodeSave))
	mux.Handle("POST /api/nodes/{id}/ask", w.auth(w.handleNodeAsk))
	mux.Handle("POST /api/nodes/{id}/task", w.auth(w.handleNodeTask))
	mux.Handle("POST /api/nodes/{id}/archive", w.auth(w.handleNodeArchive))
	mux.Handle("POST /api/nodes/{id}/reopen", w.auth(w.handleNodeReopen))
	mux.Handle("GET /api/archives", w.auth(w.handleArchives))
	mux.Handle("PUT /api/espace/view", w.auth(w.handleEspaceView))
}

// gestureBody reads a gesture's JSON body, 64 KiB at most (a node's text
// 256 KiB).
func gestureBody(rw http.ResponseWriter, r *http.Request, max int64, v any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(rw, r.Body, max)).Decode(v); err != nil {
		writeJSON(rw, http.StatusBadRequest, map[string]string{"error": "bad request: " + err.Error()})
		return false
	}
	return true
}

// gestureNode is the node a gesture names, on the graph; false, answered,
// when there is none.
func (w *web) gestureNode(rw http.ResponseWriter, r *http.Request) (nodestore.Summary, bool) {
	id := r.PathValue("id")
	if w.c.nodes == nil || !nodefile.IDOK(id) {
		writeJSON(rw, http.StatusNotFound, map[string]string{"error": errNoNode.Error()})
		return nodestore.Summary{}, false
	}
	sum, err := w.c.nodes.Get(id)
	if err != nil {
		writeJSON(rw, http.StatusNotFound, map[string]string{"error": errNoNode.Error()})
		return nodestore.Summary{}, false
	}
	return sum, true
}

// A node's text, as the user edits it.

// nodeSaveIn is a save of the .md tab: the node's body as the user wrote
// it (the author's view: its text and blocks, without code's fields), and
// the hash of the version it was made on.
type nodeSaveIn struct {
	Body   string `json:"body"`
	Expect string `json:"expect"`
}

// handleNodeSave is PUT /api/nodes/{id}/md: the user's write of a node's
// body. 200 with its version and hash (the same when nothing changed);
// 409 when a newer version came meanwhile, with that version's body and
// hash, nothing written; 410 for an archived node; 400 for a body that
// does not read (a block, a secret), with why.
func (w *web) handleNodeSave(rw http.ResponseWriter, r *http.Request, _ *session) {
	sum, ok := w.gestureNode(rw, r)
	if !ok {
		return
	}
	var in nodeSaveIn
	if !gestureBody(rw, r, 256<<10, &in) {
		return
	}
	c := w.c
	d, err := c.authorWrite(sum.ID, in.Expect, byUser, false, c.nodeSourceOK(), func(n *nodefile.Node) error { return n.AuthorBody(in.Body) })
	switch {
	case errors.Is(err, nodestore.ErrConflict):
		out := map[string]any{"error": "conflict"}
		if last, err := c.nodes.Read(sum.ID, 0); err == nil {
			out["version"], out["hash"], out["body"] = last.Version.N, last.Version.Hash, authorBody(last)
		}
		writeJSON(rw, http.StatusConflict, out)
		return
	case errors.Is(err, nodestore.ErrArchived):
		writeJSON(rw, http.StatusGone, map[string]string{"error": "archived"})
		return
	case err != nil:
		writeJSON(rw, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if len(d.Node.Blocks()) > 0 {
		c.readNodeLater(context.Background(), sum.ID, nil) // a source new or changed, read at once (node_live.go)
	}
	writeJSON(rw, http.StatusOK, map[string]any{"version": d.Version.N, "hash": d.Version.Hash})
}

// authorBody is a version's body as its author writes it: its text and
// blocks, without the front matter and code's fields of the blocks.
func authorBody(d *nodestore.Doc) string {
	text, err := d.Node.AuthorText()
	if err != nil {
		return nodeBody(d.Raw)
	}
	return text
}

// A block's gestures, and a page's.

// nodeAskIn names the button clicked: its block and its component, or
// for an ask of an Asks, the component and the ask's place ("root#1");
// or, on the page, the line of an ask: link and which of that line's
// ask: links it is (N, from 0).
type nodeAskIn struct {
	Block     string `json:"block"`
	Component string `json:"component"`
	Line      int    `json:"line"`
	N         int    `json:"n"`
}

// handleNodeAsk is POST /api/nodes/{id}/ask: a block's button, or a
// page's ask: link, that asks Eriphe. Its words are read again from the
// block or the page as stored, never taken from the request; they go as
// the user's message to the node (it never decides a card). A block
// marked external (written after an outside read, or reading a server
// not trusted), or a page of a node marked so, asks too, but its words
// may echo what was read: the message carries the taint (addAsk), so the
// turn it starts is tainted as the one that wrote it, and they never pass
// for the user's own. 404 for no such button; 410 archived.
func (w *web) handleNodeAsk(rw http.ResponseWriter, r *http.Request, _ *session) {
	sum, ok := w.gestureNode(rw, r)
	if !ok {
		return
	}
	if !sum.Archived.IsZero() {
		writeJSON(rw, http.StatusGone, map[string]string{"error": "archived"})
		return
	}
	var in nodeAskIn
	if !gestureBody(rw, r, 4<<10, &in) {
		return
	}
	c := w.c
	last, err := c.nodes.Read(sum.ID, 0)
	if err != nil {
		writeJSON(rw, http.StatusNotFound, map[string]string{"error": errNoNode.Error()})
		return
	}
	var text, block string
	var external bool
	ok = false
	if in.Block == "" {
		text, ok = pageAsk(last.Node, in.Line, in.N)
		external = last.Node.Meta.External
	} else if b := last.Node.Block(in.Block); b != nil {
		text, ok = a2ui.AskText(b.Components, in.Component)
		block, external = b.Name, b.External
	}
	if !ok || strings.TrimSpace(text) == "" {
		writeJSON(rw, http.StatusNotFound, map[string]string{"error": "no such button"})
		return
	}
	if err := c.addAsk(text, sum.ID, block, external); err != nil {
		writeJSON(rw, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	c.ping()
	w.notify()
	rw.WriteHeader(http.StatusNoContent)
}

// pageAsk is the words of the nth ask: link (from 0) of a page's line,
// as the page holds them.
func pageAsk(n *nodefile.Node, line, nth int) (string, bool) {
	p, err := n.Page()
	if err != nil || line < 1 || nth < 0 {
		return "", false
	}
	i := 0
	for _, l := range p.Links {
		if l.Kind != nodefile.LinkAsk || l.Line != line {
			continue
		}
		if i == nth {
			return l.Label, true
		}
		i++
	}
	return "", false
}

// askTaintPrefix is the taint of the words of a button of a block marked
// external: its node and block follow.
const askTaintPrefix = "ask:"

// addAsk adds a button's words as the user's message to node: a block's
// (block, its name) or its page's (block ""). From a block or a page
// marked external, the message is tainted as it is stored, in the
// same statement: delivered, it taints the turn (brain.go), and it is
// never taken for the user's own words (fromUntrusted).
func (c *core) addAsk(text, node, block string, external bool) error {
	why := ""
	if external {
		why = askTaintPrefix + node
		if block != "" {
			why += "/" + block
		}
	}
	_, err := c.store.db.Exec(`INSERT INTO messages (time, role, text, delivered, node, taint) VALUES (?, ?, ?, 0, ?, ?)`,
		time.Now().Unix(), roleUser, text, node, why)
	return err
}

// A page's tasks.

// nodeTaskIn is a task the user ticked on a node's page: its line (from
// 1, in the body as the page read it), the line's text as the page read
// it, and its new state (" ", "x", "/", "-").
type nodeTaskIn struct {
	Line  int    `json:"line"`
	Text  string `json:"text"`
	State string `json:"state"`
}

// handleNodeTask is POST /api/nodes/{id}/task: the user ticks (or
// unticks) a task of a node's page. Its line is written on the node's
// last version, whichever it is (nodefile.SetTask through ChangeLast): a
// version by the user, the box its only change; the line is found by its
// text, its number a hint, so another author's write since does not stop
// it, and nothing the request says but the state reaches the file. The
// skills that work in the node hear of it (node.gesture). 200 with the
// version and its hash; 409 when the line is gone or there twice (the
// page reads itself again); 410 archived; 400 for a state that is not
// one.
func (w *web) handleNodeTask(rw http.ResponseWriter, r *http.Request, _ *session) {
	sum, ok := w.gestureNode(rw, r)
	if !ok {
		return
	}
	var in nodeTaskIn
	if !gestureBody(rw, r, 16<<10, &in) {
		return
	}
	c := w.c
	d, err := c.authorWriteOn(sum.ID, onLast, byUser, false, c.nodeSourceOK(), func(n *nodefile.Node) error {
		body, err := n.Body()
		if err != nil {
			return err
		}
		out, err := nodefile.SetTask(body, nodefile.LineRef{Line: in.Line, Text: in.Text}, in.State)
		if err != nil {
			return err
		}
		return n.AuthorBody(out)
	})
	switch {
	case errors.Is(err, nodestore.ErrArchived):
		writeJSON(rw, http.StatusGone, map[string]string{"error": "archived"})
		return
	case errors.Is(err, nodefile.ErrLineGone), errors.Is(err, nodefile.ErrLineTwice):
		writeJSON(rw, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	case err != nil:
		writeJSON(rw, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	c.wakeNodeSkills(sum.ID, "task")
	w.notify()
	writeJSON(rw, http.StatusOK, map[string]any{"version": d.Version.N, "hash": d.Version.Hash})
}

// Archives.

// handleNodeArchive is POST /api/nodes/{id}/archive: the user archives a
// node and every node under it (archiveNode): they leave the graph and
// stay findable. 204; 404.
func (w *web) handleNodeArchive(rw http.ResponseWriter, r *http.Request, _ *session) {
	sum, ok := w.gestureNode(rw, r)
	if !ok {
		return
	}
	if err := w.c.archiveNode(r.Context(), sum.ID, byUser); err != nil {
		writeJSON(rw, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	w.c.dropFromView(sum.ID)
	w.notify() // its cards are withdrawn
	rw.WriteHeader(http.StatusNoContent)
}

// handleNodeReopen is POST /api/nodes/{id}/reopen: an archived node comes
// back to the graph (reopenNode). 204; 404.
func (w *web) handleNodeReopen(rw http.ResponseWriter, r *http.Request, _ *session) {
	sum, ok := w.gestureNode(rw, r)
	if !ok {
		return
	}
	if err := w.c.reopenNode(sum.ID, byUser); err != nil {
		writeJSON(rw, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	rw.WriteHeader(http.StatusNoContent)
}

// reopenNode brings an archived node back to the graph, with what was
// archived with it and its archived parents: the user's alone. What the
// archive stopped stays stopped (its tasks, its steps' work), and its
// cards stay withdrawn: it comes back as a page to read.
func (c *core) reopenNode(id, by string) error {
	if c.nodes == nil {
		return errNoGraph
	}
	if by != byUser {
		return errors.New("only the user reopens a node")
	}
	return c.nodes.Reopen(id, by)
}

// archivedNode is a line of the archives: the top of what one archive
// took.
type archivedNode struct {
	graphNode
	Archived string `json:"archived"`
	With     int    `json:"with"` // how many nodes were archived with it
}

// handleArchives is GET /api/archives: what the user archived, the latest
// first. A title that may be someone else's words is given marked (titleExternal).
func (w *web) handleArchives(rw http.ResponseWriter, r *http.Request, _ *session) {
	out := []archivedNode{}
	if w.c.nodes != nil {
		xs, err := w.c.nodes.Archives(100)
		if err != nil {
			writeJSON(rw, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		for _, x := range xs {
			row := w.c.graphRow(x.Summary) // archived: waits for no one, says no line
			out = append(out, archivedNode{graphNode: row, Archived: x.Archived.Format(time.RFC3339), With: x.With})
		}
	}
	rw.Header().Set("Cache-Control", "no-store")
	writeJSON(rw, http.StatusOK, map[string]any{"archives": out})
}

// espaceView is the workspace as the user left it: the pages open, the one
// the bar talked to, the last of Eriphe's shows it took (so that a page
// the user closed does not open again with the next visit) and the display
// settings (the theme, the graph's forces and whether the open node is its
// centre); and what the user closed of the orb's, that no screen shows it
// again.
type espaceView struct {
	Open  []string        `json:"open"`
	Talk  string          `json:"talk,omitempty"`
	Shown int             `json:"shown,omitempty"`
	Theme string          `json:"theme,omitempty"` // "" (the system's), light or dark
	Orb   *espaceOrbClose `json:"orb,omitempty"`
	// Forces are the graph's forces the user changed in the settings, by
	// name (web/espace, graph/layout.ts: FORCE_RANGES); the others are the
	// defaults. Centring false: the open node is not the graph's centre
	// (absent, it is).
	Forces   map[string]float64 `json:"forces,omitempty"`
	Centring *bool              `json:"centring,omitempty"`
}

// espaceForceRanges are the bounds of each of the graph's forces, as the
// workspace has them (graph/layout.ts, FORCE_RANGES): a value kept is
// brought within them, a name that is not a force is dropped.
var espaceForceRanges = map[string][2]float64{
	"centre":    {0, 0.4},
	"repulsion": {0, 6000},
	"link":      {0, 1.5},
	"distance":  {0, 300},
}

// forcesKept is what of fs the workspace knows as forces, each within its
// range; nil when nothing is left.
func forcesKept(fs map[string]float64) map[string]float64 {
	var out map[string]float64
	for k, v := range fs {
		r, ok := espaceForceRanges[k]
		if !ok || math.IsNaN(v) || math.IsInf(v, 0) {
			continue
		}
		if out == nil {
			out = map[string]float64{}
		}
		out[k] = min(r[1], max(r[0], v))
	}
	return out
}

// espaceOrbClose is what the user closed of the orb's (web/espace,
// orb/model.ts): the home's answer, the words seen and the failures by
// their message's seq; each only grows.
type espaceOrbClose struct {
	Answer int64 `json:"answer"`
	Seen   int64 `json:"seen"`
	Failed int64 `json:"failed"`
}

const (
	espaceViewKey = "espace_view"
	espaceOpenMax = 12
)

// view is the workspace as the user left it, with only the nodes still on
// the graph.
func (c *core) view() espaceView {
	var v espaceView
	json.Unmarshal([]byte(c.store.get(espaceViewKey)), &v)
	v.Open = slices.DeleteFunc(v.Open, func(id string) bool { return !c.onGraph(id) })
	if v.Open == nil {
		v.Open = []string{}
	}
	if !slices.Contains(v.Open, v.Talk) {
		v.Talk = ""
	}
	return v
}

// onGraph tells whether a node is on the graph: there, not archived.
func (c *core) onGraph(id string) bool {
	if c.nodes == nil || !nodefile.IDOK(id) {
		return false
	}
	sum, err := c.nodes.Get(id)
	return err == nil && sum.Archived.IsZero()
}

// dropFromView closes the pages of a node that left the graph.
func (c *core) dropFromView(id string) {
	v := c.view()
	raw, _ := json.Marshal(v)
	c.store.set(espaceViewKey, string(raw))
}

// handleEspaceView is PUT /api/espace/view: the pages open and the display
// settings, as the workspace has them now. 204; 400 for a theme there is
// not, or a node that is not one.
func (w *web) handleEspaceView(rw http.ResponseWriter, r *http.Request, _ *session) {
	var in espaceView
	if !gestureBody(rw, r, 4<<10, &in) {
		return
	}
	if in.Theme != "" && in.Theme != "light" && in.Theme != "dark" {
		writeJSON(rw, http.StatusBadRequest, map[string]string{"error": "theme is light, dark or empty"})
		return
	}
	if in.Shown < 0 {
		in.Shown = 0
	}
	if o := in.Orb; o != nil {
		o.Answer, o.Seen, o.Failed = max(0, o.Answer), max(0, o.Seen), max(0, o.Failed)
	}
	if len(in.Open) > espaceOpenMax {
		in.Open = in.Open[:espaceOpenMax]
	}
	in.Forces = forcesKept(in.Forces)
	if in.Centring != nil && *in.Centring {
		in.Centring = nil // on is the default: kept as nothing
	}
	for _, id := range append(slices.Clone(in.Open), in.Talk) {
		if id != "" && !nodefile.IDOK(id) {
			writeJSON(rw, http.StatusBadRequest, map[string]string{"error": "not a node's id"})
			return
		}
	}
	was := w.c.view().Open
	raw, _ := json.Marshal(in)
	if err := w.c.store.set(espaceViewKey, string(raw)); err != nil {
		log.Printf("espace: view: %v", err)
	}
	// A node just opened: its glance past its threshold is seen (node_glance.go).
	w.c.glanceOpened(slices.DeleteFunc(slices.Clone(in.Open), func(id string) bool { return slices.Contains(was, id) }))
	rw.WriteHeader(http.StatusNoContent)
}
