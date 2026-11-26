package main

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// An item opened in place (docs/node-format.md, « Opening an item »): a
// server declares, in the _meta of one of its read tools, that the tool
// reads one of its items by id (openMetaKey: the parameter that takes the
// id, and which field of what it says is the title, the sender, the date,
// the body). A click on a line of a page that references an item of that
// server ([text][m1], with [m1]: mail:<id>) opens it below the line: code
// calls the tool with the item's id as the page writes it, and the page
// shows what it said. Nothing here knows a server or a tool.
//
//   - Declared: the tool is a read (its category), its id parameter in
//     its input schema, and no other parameter required (readsByID). A
//     server with two such tools opens with the first by name.
//   - Clicked: the reference is read again in the page as stored (its last
//     version), by its label, never taken from the click. The tool's
//     policy holds: only a free read that does not reach the open web
//     runs; any other is refused, in a line that says so. The click that
//     opens is no yes: a yes is a card's (web_convo_cards.go), and the
//     opened item shows the card that waits on its line.
//   - Shown: what the tool said, its fields as declared, else a title and
//     a sender found as a row finds them (itemField) and a text said as
//     the body; and the whole as JSON under « Détails », marked external
//     when the server is not trusted; misleading when the page's words for
//     it do not say it (page_items.go). Nothing is kept: no version, no
//     trace, nothing the brain reads. The page keeps it for its session.
//
//	"eriphe/open": {"id": "id", "title": "subject", "from": "from", "date": "date", "body": "body"}

// openMetaKey is the _meta key under which a read tool declares that it
// reads one item by id, and the fields of what it says.
const openMetaKey = "eriphe/open"

// toolOpen is a read tool's declaration: the parameter that takes an
// item's id, and the top-level names of what it says (or of its one
// object).
type toolOpen struct {
	ID    string `json:"id,omitempty"`
	Title string `json:"title,omitempty"`
	From  string `json:"from,omitempty"`
	Date  string `json:"date,omitempty"`
	Body  string `json:"body,omitempty"`
}

// openValueMax is the most bytes of what an open shows.
const openValueMax = 256 << 10

// openTimeout is as long as an open's read may take.
const openTimeout = 30 * time.Second

// itemOpener is a read tool that reads one item by its id: its server,
// the tool, the parameter that takes the id, and its declaration.
type itemOpener struct {
	u    *upstream
	t    *mcp.Tool
	id   string
	decl toolOpen
}

// readsByID checks a catalog tool reads one item by its id alone: a read,
// id one of its parameters, and no other required.
func (c *core) readsByID(u *upstream, t *mcp.Tool, id string) error {
	name := claudeToolName(u.name, t.Name)
	if !c.pool.facts(name, map[string]any{}).read {
		return fmt.Errorf("%s/%s is not a read: opening an item only reads it", u.name, t.Name)
	}
	schema := c.inputSchema(name)
	props, _ := schema["properties"].(map[string]any)
	if _, ok := props[id]; !ok {
		return fmt.Errorf("%s/%s takes no %s (its parameters: %s)", u.name, t.Name, id, strings.Join(slices.Sorted(maps.Keys(props)), ", "))
	}
	if req, _ := schema["required"].([]any); slices.ContainsFunc(req, func(v any) bool { return v != id }) {
		return fmt.Errorf("%s/%s requires more than %s: an item is opened by its id alone", u.name, t.Name, id)
	}
	return nil
}

// serverOpener is the tool with which a catalog server reads one of its
// items by id, as it declares it; nil when it declares none.
func (c *core) serverOpener(server string) *itemOpener {
	if c.pool == nil {
		return nil
	}
	u := c.pool.get(server)
	if u == nil {
		return nil
	}
	_, _, tools := u.snapshot()
	for _, t := range slices.SortedFunc(slices.Values(tools), func(a, b *mcp.Tool) int { return cmp.Compare(a.Name, b.Name) }) {
		var d toolOpen
		if !metaValue(c.toolMeta(u, t), openMetaKey, &d) || d.ID == "" {
			continue
		}
		if c.readsByID(u, t, d.ID) == nil {
			return &itemOpener{u: u, t: t, id: d.ID, decl: d}
		}
	}
	return nil
}

// itemOpenIn is a click that opens an item: a reference of the page, by
// its label.
type itemOpenIn struct {
	Ref string `json:"ref"`
}

// openOut is what an open shows.
type openOut struct {
	Title    string `json:"title,omitempty"`
	From     string `json:"from,omitempty"`
	Date     string `json:"date,omitempty"`
	Body     string `json:"body,omitempty"`
	Raw      string `json:"raw,omitempty"` // the whole, JSON
	External bool   `json:"external,omitempty"`
	// Misleading: the page's words for the reference do not say the item
	// read (page_items.go): shown with a warning.
	Misleading bool `json:"misleading,omitempty"`
}

func (w *web) itemOpenRoutes(mux *webMux) {
	mux.Handle("POST /api/nodes/{id}/open", w.auth(w.handleNodeOpen))
}

// handleNodeOpen is POST /api/nodes/{id}/open: 200 with what the item
// says; 403 for a read that is not free; 404; 409 when the reference
// opens no item; 502 when the tool failed. Every answer but 200 has error, the line to show.
func (w *web) handleNodeOpen(rw http.ResponseWriter, r *http.Request, _ *session) {
	sum, ok := w.gestureNode(rw, r)
	if !ok {
		return
	}
	var in itemOpenIn
	if !gestureBody(rw, r, 16<<10, &in) {
		return
	}
	out, status, why := w.c.openRef(r.Context(), sum.ID, in.Ref, w.lang(r))
	if status != http.StatusOK {
		writeJSON(rw, status, map[string]any{"error": why})
		return
	}
	writeJSON(rw, http.StatusOK, out)
}

// openRef reads in full the item a reference of node id's page names, by
// its label: found in the page as stored, never taken from the click, and
// read by the tool its server declares.
func (c *core) openRef(ctx context.Context, id, label, lang string) (openOut, int, string) {
	d, err := c.nodes.Read(id, 0)
	if err != nil {
		return openOut{}, http.StatusNotFound, errNoNode.Error()
	}
	p, err := d.Node.Page()
	if err != nil {
		return openOut{}, http.StatusNotFound, tr(lang, "open.ref")
	}
	r, ok := c.refItemNamed(p, label)
	if !ok {
		return openOut{}, http.StatusConflict, tr(lang, "open.ref")
	}
	o := c.serverOpener(r.Server)
	if o == nil {
		return openOut{}, http.StatusConflict, tr(lang, "open.ref")
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), openTimeout)
	defer cancel()
	out, status, why := c.readItem(ctx, o, r.ID, lang)
	if status == http.StatusOK {
		out.Misleading = pageMisleads(p, r.Label, itemLabel{Title: out.Title, Detail: out.From})
	}
	return out, status, why
}

// readItem reads one item by its id with the tool that opens it: only a
// free read that does not reach the open web runs, whoever asks (a click,
// a card's words); its fields as the tool declares them, marked external
// when its server is not trusted.
func (c *core) readItem(ctx context.Context, o *itemOpener, id any, lang string) (openOut, int, string) {
	name := claudeToolName(o.u.name, o.t.Name)
	input := map[string]any{o.id: id}
	f := c.pool.facts(name, input)
	switch {
	case f.noAccount:
		return openOut{}, http.StatusForbidden, tr(lang, "act.account")
	case f.value == gridNever:
		return openOut{}, http.StatusForbidden, tr(lang, "act.never")
	case !c.freeAhead(name, input):
		return openOut{}, http.StatusForbidden, tr(lang, "open.notfree")
	}
	raw, _ := json.Marshal(input)
	res, err := o.u.call(ctx, o.t.Name, raw)
	switch {
	case err != nil:
		return openOut{}, http.StatusBadGateway, fmt.Sprintf(tr(lang, "act.failed"), truncate(oneLine(err.Error()), 200))
	case res.IsError:
		return openOut{}, http.StatusBadGateway, fmt.Sprintf(tr(lang, "act.failed"), truncate(oneLine(resultText(res)), 200))
	}
	out := openFields(resultValue(res), o.decl)
	out.External = !c.trustedServers()[o.u.name]
	return out, http.StatusOK, ""
}

// openFields chooses an item's fields from what its tool said, as the tool
// declares them (decl), else its title and sender as a row finds them; and
// keeps the whole as JSON. A text is its body.
func openFields(v any, decl toolOpen) openOut {
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
	out.Title, _ = itemField(m, decl.Title, itemTitleFields[:5], false) // subject, title, name, summary, label
	out.From, _ = itemField(m, decl.From, itemDetailFields, false)      // as written: the page says the name, the address beside
	out.Date = fieldText(m, decl.Date, 80)
	out.Body = fieldText(m, decl.Body, openValueMax)
	return out
}

// What an item says when its tool declares no field for it: the first of
// these fields it has, at its top level, whatever the server (a mail's
// subject and sender, an event's title and organizer, an issue's title
// and author).
var (
	itemTitleFields  = []string{"subject", "title", "name", "summary", "label", "snippet", "text", "description"}
	itemDetailFields = []string{"from", "sender", "author", "organizer", "owner", "creator", "user", "assignee"}
)

const itemTextMax = 160 // runes of an item's title or detail

// itemField is the text at path in an item, else at the first of fields
// it has; and the field it came from ("" for a path given).
func itemField(it any, path string, fields []string, person bool) (string, string) {
	if path != "" {
		v, err := extract(it, path)
		if err != nil {
			return "", ""
		}
		return itemText(v, person), path
	}
	m, ok := it.(map[string]any)
	if !ok {
		return "", ""
	}
	for _, k := range fields {
		if text := itemText(m[k], person); text != "" {
			return text, k
		}
	}
	return "", ""
}

// itemText is a value as one line says it: an object by its name; for a
// person, "Sam <sam@example.org>" by their name too.
func itemText(v any, person bool) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		s := strings.TrimSpace(x)
		if name, _, ok := strings.Cut(s, " <"); ok && person && strings.HasSuffix(s, ">") {
			if name = strings.Trim(strings.TrimSpace(name), `"`); name != "" {
				s = name
			}
		}
		return truncate(oneLine(s), itemTextMax)
	case map[string]any:
		for _, k := range []string{"name", "displayName", "display_name", "login", "email", "title"} {
			if s, ok := x[k].(string); ok && strings.TrimSpace(s) != "" {
				return itemText(s, person)
			}
		}
		return ""
	case []any:
		var parts []string
		for _, e := range x {
			if s := itemText(e, person); s != "" {
				parts = append(parts, s)
			}
		}
		return truncate(strings.Join(parts, ", "), itemTextMax)
	}
	return truncate(oneLine(valueText(v)), itemTextMax)
}

// fieldText is an object's field as a text, n bytes at most; "" for none.
func fieldText(m map[string]any, field string, n int) string {
	if s, ok := m[field].(string); ok && field != "" {
		return truncateBytes(strings.TrimSpace(s), n)
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
