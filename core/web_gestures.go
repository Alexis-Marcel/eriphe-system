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
//   - a mark on an item a block shows (keep, drop, done), written by code
//     in the block (nodefile's SetMark), a version of code's: no turn;
//   - a block's button that asks Eriphe: the ask its author wrote, read
//     again from the stored block, sent as the user's message to its node;
//   - the archive of a node and what is under it, and its reopening, the
//     user's alone (archiveNode, reopenNode);
//   - an edit of a node's text in its .md tab, made on the version it was
//     read at (a newer one is a conflict, never overwritten);
//   - where a bubble was dropped (node_layout: no version), the pages open
//     and the display settings (kv espace_view): the workspace as the user
//     left it, on any of their screens.
//
// Every route is the workspace's session, its CSRF token in X-CSRF-Token.

func (w *web) gestureRoutes(mux *webMux) {
	mux.Handle("PUT /api/nodes/{id}/md", w.auth(w.handleNodeSave))
	mux.Handle("POST /api/nodes/{id}/mark", w.auth(w.handleNodeMark))
	mux.Handle("POST /api/nodes/{id}/ask", w.auth(w.handleNodeAsk))
	mux.Handle("POST /api/nodes/{id}/archive", w.auth(w.handleNodeArchive))
	mux.Handle("POST /api/nodes/{id}/reopen", w.auth(w.handleNodeReopen))
	mux.Handle("PUT /api/nodes/{id}/place", w.auth(w.handleNodePlace))
	mux.Handle("DELETE /api/nodes/{id}/place", w.auth(w.handleNodePlace))
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
	d, err := c.authorWrite(sum.ID, in.Expect, byUser, false, c.viewSourceOK(""), func(n *nodefile.Node) error { return n.AuthorBody(in.Body) })
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

// A block's gestures.

// nodeMarkIn is a mark on an item a block shows: ids only, never text.
type nodeMarkIn struct {
	Block string `json:"block"`
	List  string `json:"list"`
	Item  string `json:"item"`
	Mark  string `json:"mark"` // keep, drop, done, or "" to take it away
}

// handleNodeMark is POST /api/nodes/{id}/mark: code writes the user's mark
// in the block, on its last version, as a version of its own (not listed
// on the page), and starts no turn. 204; 400 for a mark the block cannot
// carry (no such block, a list or item that is not an id, another mark);
// 404; 410 archived.
func (w *web) handleNodeMark(rw http.ResponseWriter, r *http.Request, _ *session) {
	sum, ok := w.gestureNode(rw, r)
	if !ok {
		return
	}
	var in nodeMarkIn
	if !gestureBody(rw, r, 4<<10, &in) {
		return
	}
	c := w.c
	last, err := c.nodes.Read(sum.ID, 0)
	if err != nil {
		writeJSON(rw, http.StatusNotFound, map[string]string{"error": errNoNode.Error()})
		return
	}
	if !blockHas(last.Node.Block(in.Block), "Mark") {
		writeJSON(rw, http.StatusBadRequest, map[string]string{"error": "no Mark in this block"})
		return
	}
	_, err = c.nodes.Change(sum.ID, last.Version.Hash, nodeByCode, func(n *nodefile.Node) error { return n.SetMark(in.Block, in.List, in.Item, in.Mark) })
	switch {
	case errors.Is(err, nodestore.ErrArchived):
		writeJSON(rw, http.StatusGone, map[string]string{"error": "archived"})
	case err != nil:
		writeJSON(rw, http.StatusBadRequest, map[string]string{"error": err.Error()})
	default:
		rw.WriteHeader(http.StatusNoContent)
	}
}

// blockHas tells whether a block has a component of that type.
func blockHas(b *nodefile.Block, component string) bool {
	if b == nil {
		return false
	}
	var list []map[string]any
	json.Unmarshal(b.Components, &list)
	return slices.ContainsFunc(list, func(c map[string]any) bool { return c["component"] == component })
}

// nodeAskIn names the button clicked: its block and its component.
type nodeAskIn struct {
	Block     string `json:"block"`
	Component string `json:"component"`
}

// handleNodeAsk is POST /api/nodes/{id}/ask: a block's button that asks
// Eriphe. Its words are read again from the block as stored, never taken
// from the page; they go as the user's message to the block's node (it
// never decides a card). Refused for a block marked external (a button
// someone else's words may have written, which the brain would read as
// the user's), 403; 404 for no such button; 410 archived.
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
	b := last.Node.Block(in.Block)
	if b == nil {
		writeJSON(rw, http.StatusNotFound, map[string]string{"error": "no such block"})
		return
	}
	if b.External {
		writeJSON(rw, http.StatusForbidden, map[string]string{"error": "a block drawn from external content asks nothing"})
		return
	}
	text, ok := a2ui.AskText(b.Components, in.Component)
	if !ok || strings.TrimSpace(text) == "" {
		writeJSON(rw, http.StatusNotFound, map[string]string{"error": "no such button"})
		return
	}
	if _, err := c.store.addUserMessage(text, nil, sum.ID); err != nil {
		writeJSON(rw, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	c.ping()
	w.notify()
	rw.WriteHeader(http.StatusNoContent)
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
// cards are no longer its own: it comes back as a page to read.
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
// first. A title that may be someone else's words is not given.
func (w *web) handleArchives(rw http.ResponseWriter, r *http.Request, _ *session) {
	out := []archivedNode{}
	if w.c.nodes != nil {
		xs, err := w.c.nodes.Archives(100)
		if err != nil {
			writeJSON(rw, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		for _, x := range xs {
			row := w.c.graphRow(x.Summary)
			row.Waits, row.Line = false, ""
			out = append(out, archivedNode{graphNode: row, Archived: x.Archived.Format(time.RFC3339), With: x.With})
		}
	}
	rw.Header().Set("Cache-Control", "no-store")
	writeJSON(rw, http.StatusOK, map[string]any{"archives": out})
}

// Where things are.

// placeIn is where a bubble was dropped: a root's spot, a child's offset
// from its parent, in the graph's units.
type placeIn struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// placeMax bounds a place: far beyond any screen, never a number JSON
// cannot carry back.
const placeMax = 1e6

// handleNodePlace is PUT /api/nodes/{id}/place (where the user dropped a
// bubble) and DELETE (the layout places it again). No version, no turn.
// 204; 400 for a place out of bounds; 404.
func (w *web) handleNodePlace(rw http.ResponseWriter, r *http.Request, _ *session) {
	sum, ok := w.gestureNode(rw, r)
	if !ok {
		return
	}
	if r.Method == http.MethodDelete {
		w.c.nodes.Unplace(sum.ID)
		rw.WriteHeader(http.StatusNoContent)
		return
	}
	var in placeIn
	if !gestureBody(rw, r, 1<<10, &in) {
		return
	}
	if math.IsNaN(in.X) || math.IsNaN(in.Y) || math.Abs(in.X) > placeMax || math.Abs(in.Y) > placeMax {
		writeJSON(rw, http.StatusBadRequest, map[string]string{"error": "a place out of bounds"})
		return
	}
	if err := w.c.nodes.SetPlace(sum.ID, nodestore.Place{X: in.X, Y: in.Y}); err != nil {
		writeJSON(rw, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	rw.WriteHeader(http.StatusNoContent)
}

// espaceView is the workspace as the user left it: the pages open, the one
// the console talked to, the last of Eriphe's shows it took (so that a page
// the user closed does not open again with the next visit) and the display
// settings.
type espaceView struct {
	Open  []string `json:"open"`
	Talk  string   `json:"talk,omitempty"`
	Shown int      `json:"shown,omitempty"`
	Theme string   `json:"theme,omitempty"` // "" (the system's), light or dark
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
	if len(in.Open) > espaceOpenMax {
		in.Open = in.Open[:espaceOpenMax]
	}
	for _, id := range append(slices.Clone(in.Open), in.Talk) {
		if id != "" && !nodefile.IDOK(id) {
			writeJSON(rw, http.StatusBadRequest, map[string]string{"error": "not a node's id"})
			return
		}
	}
	raw, _ := json.Marshal(in)
	if err := w.c.store.set(espaceViewKey, string(raw)); err != nil {
		log.Printf("espace: view: %v", err)
	}
	rw.WriteHeader(http.StatusNoContent)
}

// dropped are where the user dropped the bubbles still on the graph.
func (c *core) dropped() map[string]placeIn {
	out := map[string]placeIn{}
	if c.nodes == nil {
		return out
	}
	ps, err := c.nodes.Places()
	if err != nil {
		log.Printf("espace: places: %v", err)
	}
	for id, p := range ps {
		out[id] = placeIn{X: p.X, Y: p.Y}
	}
	return out
}
