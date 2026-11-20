package main

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Alexis-Marcel/eriphe-system/core/nodefile"
)

// An item opened in place (docs/node-format.md, « Reading an item »): a
// source's items descriptor may say how one item is read in full, a read
// tool of the source's own server and its parameter that takes the
// item's id. A click on a row of a List bound to that source opens it
// below the row: code calls the tool with the item's id, as the source
// gave it, and the page shows what it said. Nothing here knows a server
// or a tool.
//
//   - Written: the tool is the catalog's, of the source's own server, a
//     read (its category), its id parameter in its input schema, and no
//     other parameter required (openOK, from nodeGuard).
//   - Clicked: the open is read again in the block as stored, never taken
//     from the page; the id must be one the source lists now. The tool's
//     policy holds: never (or a call that must name its account) is
//     refused; a free read that does not reach the open web runs; any
//     other asks the user first, inline, the call in code's words, and
//     runs on their yes (logged as such). The click that opens is not the
//     yes: the user has not seen the call yet.
//   - Shown: what the tool said, its common fields picked generically
//     (title, from, date, body: openFields) and the whole as JSON under
//     « Détails », marked external when the server is not trusted, with
//     the item's note. Nothing is kept: no version, no trace, nothing the
//     brain reads. The page keeps it for its session.

// openValueMax is the most bytes of what an open shows.
const openValueMax = 256 << 10

// openTimeout is as long as an open's read may take.
const openTimeout = 30 * time.Second

// sourceServer is the server a source reads ("" for a monitor's).
func sourceServer(src nodefile.Source) string {
	if src.Tool != "" {
		server, _, _ := strings.Cut(src.Tool, "/")
		return server
	}
	return src.Resource
}

// openOK checks a source's open against the catalog.
func (c *core) openOK(src nodefile.Source) error {
	if src.Items == nil || src.Items.Open == nil {
		return nil
	}
	o := src.Items.Open
	server, _, _ := strings.Cut(o.Tool, "/")
	if server != sourceServer(src) {
		return fmt.Errorf("items' open: %s is not a tool of the source's own server %s", o.Tool, sourceServer(src))
	}
	u, t := c.actionTool(o.Tool)
	if u == nil || t == nil {
		return fmt.Errorf("items' open: %s is not a tool of the catalog (list_tools)", o.Tool)
	}
	name := claudeToolName(u.name, t.Name)
	if !c.pool.facts(name, map[string]any{}).read {
		return fmt.Errorf("items' open: %s is not a read: opening an item only reads it", o.Tool)
	}
	schema := c.inputSchema(name)
	props, _ := schema["properties"].(map[string]any)
	if _, ok := props[o.ID]; !ok {
		return fmt.Errorf("items' open: %s takes no %s (its parameters: %s)", o.Tool, o.ID, strings.Join(slices.Sorted(maps.Keys(props)), ", "))
	}
	if req, _ := schema["required"].([]any); slices.ContainsFunc(req, func(v any) bool { return v != o.ID }) {
		return fmt.Errorf("items' open: %s requires more than %s: an item is opened by its id alone", o.Tool, o.ID)
	}
	return nil
}

// itemOpenIn is a click that opens an item: the block, its source, the
// item's id, and the user's yes once they saw the call.
type itemOpenIn struct {
	Block  string `json:"block"`
	Source string `json:"source"`
	Item   string `json:"item"`
	Yes    bool   `json:"yes"`
	Tool   string `json:"tool"` // with yes: the tool of the call the user saw
}

// openNote is the item's note, as its row's: its group and note, and
// whether the note may carry someone else's words.
type openNote struct {
	Group    string `json:"group,omitempty"`
	Note     string `json:"note,omitempty"`
	External bool   `json:"external,omitempty"`
}

// openOut is what an open shows, or the call it asks a yes for.
type openOut struct {
	Ask      *openAsk  `json:"ask,omitempty"`
	Title    string    `json:"title,omitempty"`
	From     string    `json:"from,omitempty"`
	Date     string    `json:"date,omitempty"`
	Body     string    `json:"body,omitempty"`
	Raw      string    `json:"raw,omitempty"` // the whole, JSON
	External bool      `json:"external,omitempty"`
	Note     *openNote `json:"note,omitempty"`
}

// openAsk is the call an open asks a yes for, in code's words.
type openAsk struct {
	Tool string   `json:"tool"`
	Args []string `json:"args"`
}

func (w *web) itemOpenRoutes(mux *webMux) {
	mux.Handle("POST /api/nodes/{id}/open", w.auth(w.handleNodeOpen))
}

// handleNodeOpen is POST /api/nodes/{id}/open: 200 with what the item
// says, or the call it asks a yes for (ask); 400 for an item the source
// does not list; 403 for a tool the policy never allows; 404; 409 when
// the source opens no item; 502 when the tool failed. Every answer but
// 200 has error, the line to show.
func (w *web) handleNodeOpen(rw http.ResponseWriter, r *http.Request, _ *session) {
	sum, ok := w.gestureNode(rw, r)
	if !ok {
		return
	}
	var in itemOpenIn
	if !gestureBody(rw, r, 16<<10, &in) {
		return
	}
	out, status, why := w.c.openItem(r.Context(), sum.ID, in, w.lang(r))
	if status != http.StatusOK {
		writeJSON(rw, status, map[string]any{"error": why})
		return
	}
	writeJSON(rw, http.StatusOK, out)
}

// openItem reads an item of a node's source in full.
func (c *core) openItem(ctx context.Context, id string, in itemOpenIn, lang string) (openOut, int, string) {
	d, err := c.nodes.Read(id, 0)
	if err != nil {
		return openOut{}, http.StatusNotFound, errNoNode.Error()
	}
	b := d.Node.Block(in.Block)
	if b == nil || b.Card != "" || b.Validate() != nil {
		return openOut{}, http.StatusNotFound, tr(lang, "open.none")
	}
	src, ok := b.Sources[in.Source]
	if !ok || src.Items == nil || src.Items.Open == nil {
		return openOut{}, http.StatusConflict, tr(lang, "open.none")
	}
	listed, _ := itemIDs(src, c.sourceNow(id, b, in.Source), nil)
	v, ok := listed[in.Item]
	if !ok {
		return openOut{}, http.StatusBadRequest, tr(lang, "open.stale")
	}
	o := src.Items.Open
	if c.openOK(src) != nil {
		return openOut{}, http.StatusGone, tr(lang, "act.gone")
	}
	u, t := c.actionTool(o.Tool)
	name := claudeToolName(u.name, t.Name)
	input := map[string]any{o.ID: v}
	f := c.pool.facts(name, input)
	switch {
	case f.noAccount:
		return openOut{}, http.StatusForbidden, tr(lang, "act.account")
	case f.value == gridNever:
		return openOut{}, http.StatusForbidden, tr(lang, "act.never")
	}
	ahead := c.freeAhead(name, input)
	if !ahead && in.Yes && in.Tool != buttonText(o.Tool, 80) {
		return openOut{}, http.StatusConflict, tr(lang, "act.changed") // the yes was for another call
	}
	if !ahead && !in.Yes {
		return openOut{Ask: &openAsk{Tool: buttonText(o.Tool, 80), Args: argLines(input, lang, 0)}}, http.StatusOK, ""
	}
	if !ahead {
		c.store.logDecision("", "", name, input, decideAllow, byUser, "geste de l'utilisateur : il a ouvert un élément d'une liste et dit oui à sa lecture")
	}
	raw, _ := json.Marshal(input)
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), openTimeout)
	defer cancel()
	res, err := u.call(ctx, t.Name, raw)
	switch {
	case err != nil:
		return openOut{}, http.StatusBadGateway, fmt.Sprintf(tr(lang, "act.failed"), truncate(oneLine(err.Error()), 200))
	case res.IsError:
		return openOut{}, http.StatusBadGateway, fmt.Sprintf(tr(lang, "act.failed"), truncate(oneLine(resultText(res)), 200))
	}
	out := openFields(resultValue(res))
	out.External = !c.trustedServers()[u.name]
	if n, ok := c.itemNotes(id, in.Source)[in.Item]; ok && (n.Group != "" || n.Note != "") {
		out.Note = &openNote{Group: n.Group, Note: n.Note, External: n.Tainted != ""}
	}
	return out, http.StatusOK, ""
}

// What an item's fields are, whatever its source: the first of these it
// has at its top level (or in its one object, {"message": {…}}).
var (
	openDateFields = []string{"date", "received", "receivedAt", "received_at", "sent", "sentAt", "created", "createdAt", "created_at", "updated", "start", "time"}
	openBodyFields = []string{"body", "text", "content", "markdown", "plain", "snippet", "description", "summary"}
)

// openFields picks an item's common fields from what its tool said, and
// keeps the whole as JSON; a text is its body.
func openFields(v any) openOut {
	var out openOut
	if s, ok := v.(string); ok {
		out.Body = truncateBytes(s, openValueMax)
		return out
	}
	raw, _ := json.MarshalIndent(v, "", "  ")
	out.Raw = truncateBytes(string(raw), openValueMax)
	m, ok := v.(map[string]any)
	if !ok {
		return out
	}
	if len(m) == 1 {
		for _, x := range m {
			if inner, ok := x.(map[string]any); ok {
				m = inner
			}
		}
	}
	out.Title, _ = itemField(m, "", itemTitleFields[:5], false) // subject, title, name, summary, label
	out.From, _ = itemField(m, "", itemDetailFields, true)
	out.Date = firstText(m, openDateFields, 80)
	out.Body = firstText(m, openBodyFields, openValueMax)
	return out
}

// firstText is the first of fields an object has as a text, n bytes at
// most.
func firstText(m map[string]any, fields []string, n int) string {
	for _, k := range fields {
		if s, ok := m[k].(string); ok && strings.TrimSpace(s) != "" {
			return truncateBytes(strings.TrimSpace(s), n)
		}
	}
	return ""
}

// truncateBytes is s cut to n bytes at most, on a rune.
func truncateBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	s = s[:n]
	for len(s) > 0 && !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s + "…"
}
