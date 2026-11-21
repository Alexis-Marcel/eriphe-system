package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/Alexis-Marcel/eriphe-system/core/nodefile"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

// Actions on the items of a list (docs/node-format.md, « Actions »): a
// block declares, by name, what the user may run on the items of one of
// its sources they pick in a list: a catalog tool with fixed args, the
// ids they picked in one of its parameters. Nothing here knows a server
// or a tool: any tool that takes a list of ids is an action with no code.
//
//   - Written: the tool is the catalog's, its parameter takes a list, and
//     the fixed args hold against its input schema (actionOK, from
//     nodeGuard, whoever writes the block).
//   - Shown: a List bound to the source's items gets a box per row; once
//     something is picked, its block's actions, each a button whose words
//     are code's (actionWords: the tool and its fixed args, never the
//     author's label, which is only a hint, and none on an external
//     block). A tool that looks destructive asks once more, showing the
//     exact call (blockLists).
//   - Run: the click is the user's own gesture (web_list_actions.go).

// listItem is a row of a list the user may pick: its place in the
// source's list, its id, what it shows, and the card that waits on it
// (item_cards.go: code's, never the block's data), its Oui on its row.
type listItem struct {
	N      int    `json:"n"`
	ID     string `json:"id"`
	Title  string `json:"title,omitempty"`
	Detail string `json:"detail,omitempty"`
	Card   string `json:"card,omitempty"`
}

// listAction is one of a block's actions as its button shows it: the
// words are code's, One for one item, Many with {n} for how many, All
// for all the rows of the list (« Tout mettre à la corbeille »). The tool
// is not in them: the confirm and the button's tooltip say it.
type listAction struct {
	Name string   `json:"name"`
	One  string   `json:"one"`
	Many string   `json:"many"`
	All  string   `json:"all"`
	Tool string   `json:"tool"`           // server/tool
	Args []string `json:"args,omitempty"` // the args, a line each, for the confirm (one taken from each item says its values)
	Hint string   `json:"hint,omitempty"` // the author's label
	// HintExternal: the hint is on an external block, someone else's words may have written it: shown marked
	HintExternal bool `json:"hintExternal,omitempty"`
	Confirm      bool `json:"confirm,omitempty"`
}

// pageList is a List of a block bound to a source's items: the List's
// component id, its source, its rows, and the actions on them. Filtered:
// the List filters its source's items (list_sections.go), and shows only
// the rows given, never the others.
type pageList struct {
	Component string `json:"component"`
	Source    string `json:"source"`
	Filtered  bool   `json:"filtered,omitempty"`
	Open      bool   `json:"open,omitempty"` // its rows open in place (item_open.go)
	// RowCard: its row template draws each row's card itself (an inline
	// Proposal on _card); else the page adds a row's Oui to the row.
	RowCard bool         `json:"rowCard,omitempty"`
	Items   []listItem   `json:"items"`
	Actions []listAction `json:"actions"`
}

// actionsMaxIDs is the most items one click acts on.
const actionsMaxIDs = 200

// actionTool is an action's tool in the pool, by its server/tool name;
// nil when it is not there.
func (c *core) actionTool(name string) (*upstream, *mcp.Tool) {
	server, tool, ok := strings.Cut(name, "/")
	if !ok || c.pool == nil {
		return nil, nil
	}
	u := c.pool.get(server)
	if u == nil {
		return nil, nil
	}
	return u, u.toolByClaudeName(tool)
}

// actionOK checks an action against the catalog: its tool is there, its
// items parameter takes a list, and its fixed args hold against the
// tool's input schema, that parameter left out.
func (c *core) actionOK(a nodefile.Action) error {
	u, t := c.actionTool(a.Tool)
	if u == nil || t == nil {
		return fmt.Errorf("%s is not a tool of the catalog (list_tools)", a.Tool)
	}
	schema := c.inputSchema(claudeToolName(u.name, t.Name))
	props, _ := schema["properties"].(map[string]any)
	p, _ := props[a.Items].(map[string]any)
	if p == nil || !schemaArray(p) {
		var lists []string
		for _, k := range slices.Sorted(maps.Keys(props)) {
			if q, _ := props[k].(map[string]any); q != nil && schemaArray(q) {
				lists = append(lists, k)
			}
		}
		if len(lists) == 0 {
			return fmt.Errorf("%s takes no list of ids: no action on picked items", a.Tool)
		}
		return fmt.Errorf("items: %s of %s is not a list of ids (its lists: %s)", a.Items, a.Tool, strings.Join(lists, ", "))
	}
	// An arg taken from each item: a parameter of the tool that takes one
	// value (each call gets one), filled at the click.
	byItem := a.ItemArgs()
	for _, k := range slices.Sorted(maps.Keys(byItem)) {
		q, _ := props[k].(map[string]any)
		if q == nil && len(props) > 0 {
			return fmt.Errorf("args.%s: %s has no such parameter", k, a.Tool)
		}
		if q != nil && (schemaArray(q) || q["type"] == "object") {
			return fmt.Errorf("args.%s: taken from each item, it is one value; %s takes a list or an object there", k, a.Tool)
		}
	}
	if err := argsHold(schema, append([]string{a.Items}, slices.Sorted(maps.Keys(byItem))...), a.FixedArgs()); err != nil {
		return fmt.Errorf("args: %v", err)
	}
	return nil
}

// schemaArray tells whether a parameter's schema is a list.
func schemaArray(p map[string]any) bool {
	switch t := p["type"].(type) {
	case string:
		return t == "array"
	case []any:
		return slices.Contains(t, any("array"))
	}
	_, ok := p["items"]
	return ok
}

// argsHold validates fixed args against a tool's input schema, the
// parameters filled at the click (the ids, those taken from each item)
// no longer required.
func argsHold(schema map[string]any, free []string, args map[string]any) error {
	if schema == nil {
		return nil
	}
	s := maps.Clone(schema)
	if req, ok := s["required"].([]any); ok {
		s["required"] = slices.DeleteFunc(slices.Clone(req), func(v any) bool { k, _ := v.(string); return slices.Contains(free, k) })
	}
	doc, err := schemaValue(s)
	if err != nil {
		return err
	}
	comp := jsonschema.NewCompiler()
	if err := comp.AddResource("tool.json", doc); err != nil {
		return err
	}
	sch, err := comp.Compile("tool.json")
	if err != nil {
		return nil // a schema we cannot read: the tool says at the click
	}
	in := args
	if in == nil {
		in = map[string]any{}
	}
	v, err := schemaValue(in)
	if err != nil {
		return err
	}
	var ve *jsonschema.ValidationError
	if err := sch.Validate(v); errors.As(err, &ve) {
		return errors.New(oneLine(strings.TrimPrefix(fmt.Sprintf("%v", ve), "jsonschema validation failed with 'tool.json#'")))
	} else if err != nil {
		return err
	}
	return nil
}

// schemaValue is v as the schema library reads it.
func schemaValue(v any) (any, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return jsonschema.UnmarshalJSON(bytes.NewReader(raw))
}

// destructiveWords are the words of a tool that throws something away.
var destructiveWords = []string{"trash", "delete", "remove", "destroy", "purge", "erase"}

// destructive tells whether a tool looks like it throws something away:
// it says so (destructiveHint), or its name or description does.
func destructive(t *mcp.Tool) bool {
	if t == nil {
		return false
	}
	if t.Annotations != nil && t.Annotations.DestructiveHint != nil && *t.Annotations.DestructiveHint {
		return true
	}
	text := strings.ToLower(t.Name + " " + t.Description)
	return slices.ContainsFunc(destructiveWords, func(w string) bool { return strings.Contains(text, w) })
}

// actionWords are what an action's button says, built by code from its
// tool and fixed args: for one item, and for several ({n} for how many).
// A verb of its own for the usual tools (move, trash, archive, delete,
// mark…), else the tool's title, else its name. The tool itself is not
// in them: what a click runs, the confirm and the card's Détails say.
func actionWords(t *mcp.Tool, toolName string, args map[string]any, lang string) (one, many string) {
	verb, tail := actionVerb(t, toolName, args, lang)
	return fmt.Sprintf(verb, tr(lang, "act.one")) + tail, fmt.Sprintf(verb, tr(lang, "act.many")) + tail
}

// listWords are what a list's buttons say: for one row, for some of its
// rows (« Mettre les {n} à la corbeille »), and for all of them, with no
// count (« Tout mettre à la corbeille »): the line says how many.
func listWords(t *mcp.Tool, toolName string, args map[string]any, lang string) (one, some, all string) {
	verb, tail := actionVerb(t, toolName, args, lang)
	_, short, _ := strings.Cut(toolName, "/")
	all = tr(lang, "act.allverb."+short)
	if all == "act.allverb."+short {
		all = fmt.Sprintf(verb, tr(lang, "act.everything"))
	}
	return fmt.Sprintf(verb, tr(lang, "act.one")) + tail, fmt.Sprintf(verb, tr(lang, "act.all")) + tail, all + tail
}

// actionVerb is an action's verb, with a %s for what it acts on, and
// what follows it: its fixed args.
func actionVerb(t *mcp.Tool, toolName string, args map[string]any, lang string) (verb, tail string) {
	_, short, _ := strings.Cut(toolName, "/")
	verb = tr(lang, "act.verb."+short)
	if verb == "act.verb."+short {
		title := ""
		if t != nil {
			title = cmpOr(t.Title, annotationTitle(t))
		}
		if title = buttonText(title, 50); title != "" {
			verb = title + " · %s"
		} else {
			verb = fmt.Sprintf(tr(lang, "act.verb.other"), buttonText(short, 40), "%s")
		}
	}
	tail = ""
	if a := argsText(args, lang); a != "" {
		tail = " → " + a
	}
	return verb, tail
}

func annotationTitle(t *mcp.Tool) string {
	if t.Annotations != nil {
		return t.Annotations.Title
	}
	return ""
}

// buttonText is a text of a tool's or an author's for a button: one line, no
// placeholder of ours, n runes at most.
func buttonText(s string, n int) string {
	s = strings.NewReplacer("{n}", "", "%", "％").Replace(s)
	return truncate(oneLine(s), n)
}

// argsText says an action's fixed args in a line: a single text by its
// value, else each as « name : value ».
func argsText(args map[string]any, lang string) string {
	if len(args) == 0 {
		return ""
	}
	keys := slices.Sorted(maps.Keys(args))
	if len(keys) == 1 {
		if s, ok := args[keys[0]].(string); ok {
			return buttonText(s, 40)
		}
	}
	return strings.Join(argLines(args, lang, 3), ", ")
}

// argLines are an action's fixed args, « name : value » each, most of
// them (0: all).
func argLines(args map[string]any, lang string, most int) []string {
	var out []string
	for _, k := range slices.Sorted(maps.Keys(args)) {
		if most > 0 && len(out) == most {
			out = append(out, "…")
			break
		}
		out = append(out, buttonText(k, 30)+" : "+buttonText(argValue(args[k], lang), 60))
	}
	return out
}

// argValue is a fixed arg's value in words.
func argValue(v any, lang string) string {
	switch x := v.(type) {
	case bool:
		return tr(lang, map[bool]string{true: "act.yes", false: "act.no"}[x])
	case string:
		return x
	}
	raw, _ := json.Marshal(v)
	return string(raw)
}

// listedItem is an item the user may pick: its id as the source gives it
// (a number stays a number for the tool), and the item itself.
type listedItem struct {
	ID   any
	Item any
}

// itemIDs are the ids of a source's items as the user picks them, those
// the filters keep (none: all), each with its value as the source gives
// it and its item, in the source's order (sourceItems' reading: an id
// given twice is its first item's).
func itemIDs(src nodefile.Source, value any, f itemFilters) (map[string]listedItem, []string) {
	if src.Items == nil || value == nil {
		return nil, nil
	}
	list, err := extract(value, src.Items.Path)
	items, ok := list.([]any)
	if err != nil || !ok {
		return nil, nil
	}
	ids := map[string]listedItem{}
	seen := map[string]bool{}
	var order []string
	for _, it := range items {
		v, err := extract(it, src.Items.ID)
		if err != nil || v == nil {
			continue
		}
		id := truncate(valueText(v), 200)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		if !f.keeps(it) {
			continue
		}
		ids[id] = listedItem{ID: v, Item: it}
		order = append(order, id)
	}
	return ids, order
}

// listPath is where a source's items are in a block's data, as a List's
// template names it: /<source>/<path>; "" when its path is no plain
// fields (a filter, *): no List is bound to it as a whole.
func listPath(name string, src nodefile.Source) string {
	if src.Items == nil {
		return ""
	}
	steps, err := parsePath(src.Items.Path)
	if err != nil {
		return ""
	}
	p := "/" + escapeToken(name)
	for _, st := range steps {
		if st.all || st.field != "" || st.key == "" {
			return ""
		}
		p += "/" + escapeToken(st.key)
	}
	return p
}

func escapeToken(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "~", "~0"), "/", "~1")
}

// sourceRows are a source's rows as a List shows them, those its filters
// keep (none: all), each with its place in the source's list and the card
// that waits on it (_card, as noteItems wrote it), and the items
// themselves, as the source gave them.
func sourceRows(name string, src nodefile.Source, value any, f itemFilters) ([]listItem, []any) {
	out := []listItem{}
	var raw, kept []any
	if src.Items != nil && value != nil {
		list, _ := extract(value, src.Items.Path)
		raw, _ = list.([]any)
	}
	for _, it := range sourceItems(name, src, value, 0) {
		r := listItem{}
		r.N, _ = it["n"].(int)
		if r.N >= len(raw) || !f.keeps(raw[r.N]) {
			continue
		}
		r.ID, _ = it["id"].(string)
		if t, _ := it["title"].(string); t != r.ID {
			r.Title = t
		}
		r.Detail, _ = it["detail"].(string)
		if o, ok := raw[r.N].(map[string]any); ok {
			r.Card, _ = o[itemCardField].(string)
		}
		out = append(out, r)
		kept = append(kept, raw[r.N])
	}
	return out, kept
}

// foldedLists are the Lists a block's Folds name (Fold's list: a batch,
// its line says how many rows it has).
func foldedLists(comps []map[string]any) map[string]bool {
	out := map[string]bool{}
	for _, comp := range comps {
		if l, _ := comp["list"].(string); comp["component"] == "Fold" && l != "" {
			out[l] = true
		}
	}
	return out
}

// blockLists are the Lists of a block bound as a whole to a source's
// items that the page must know: those that filter them (it shows only
// their rows), those some action acts on (the user picks rows), those
// that open their rows, those a Fold names, and those a card waits on
// one of whose rows, each with its rows (from data, the block's data as
// the page gets it, the items' notes and cards on them) and the actions'
// buttons. With only, those of these sources alone.
func (c *core) blockLists(b *nodefile.Block, data map[string]any, lang string, only ...string) []pageList {
	var comps []map[string]any
	json.Unmarshal(b.Components, &comps)
	folded := foldedLists(comps)
	var out []pageList
	for _, comp := range comps {
		if comp["component"] != "List" {
			continue
		}
		id, _ := comp["id"].(string)
		tpl, _ := comp["children"].(map[string]any)
		path, _ := tpl["path"].(string)
		if id == "" || path == "" {
			continue
		}
		name, f, err := listBinding(path, b.Sources)
		if err != nil && strings.Contains(path, "[") && len(only) == 0 {
			// A filter that does not read (a file written by hand): the
			// page shows none of the source's items, never all of them.
			out = append(out, pageList{Component: id, Filtered: true, Items: []listItem{}, Actions: []listAction{}})
			continue
		}
		if err != nil || len(only) > 0 && !slices.Contains(only, name) {
			continue
		}
		src := b.Sources[name]
		rows, items := sourceRows(name, src, data[name], f)
		acts := []listAction{}
		for _, an := range slices.Sorted(maps.Keys(b.Actions)) {
			if a := b.Actions[an]; a.Source == name && (a.List == "" || a.List == id) {
				acts = append(acts, c.listAction(an, a, items, b.External, lang))
			}
		}
		open := src.Items != nil && src.Items.Open != nil
		carded := slices.ContainsFunc(rows, func(r listItem) bool { return r.Card != "" })
		if len(acts) > 0 || f != nil || open || folded[id] || carded {
			out = append(out, pageList{Component: id, Source: name, Filtered: f != nil, Open: open, Items: rows, Actions: acts,
				RowCard: rowCardIn(comps, tpl["componentId"])})
		}
	}
	return out
}

// rowCardIn tells whether a List's row template (the component id) draws
// its row's own card: an inline Proposal bound to the item's _card
// (rowCard), in it or below it. Else the page adds the row's Oui itself.
func rowCardIn(comps []map[string]any, id any) bool {
	byID := map[string]map[string]any{}
	for _, comp := range comps {
		if s, ok := comp["id"].(string); ok {
			byID[s] = comp
		}
	}
	seen := map[string]bool{}
	var walk func(id string) bool
	walk = func(id string) bool {
		comp, ok := byID[id]
		if !ok || seen[id] {
			return false
		}
		seen[id] = true
		if rowCard(comp) {
			return true
		}
		var kids []string
		for _, k := range []string{"child", "aside", "trigger", "content"} {
			if s, ok := comp[k].(string); ok {
				kids = append(kids, s)
			}
		}
		if list, ok := comp["children"].([]any); ok {
			for _, k := range list {
				if s, ok := k.(string); ok {
					kids = append(kids, s)
				}
			}
		}
		return slices.ContainsFunc(kids, walk)
	}
	s, _ := id.(string)
	return walk(s)
}

// listAction is an action's button, its words code's; items are the rows
// of its List, as the source gave them, for the values an arg taken from
// each item has.
func (c *core) listAction(name string, a nodefile.Action, items []any, external bool, lang string) listAction {
	_, t := c.actionTool(a.Tool)
	fixed := a.FixedArgs()
	one, some, all := listWords(t, a.Tool, fixed, lang)
	args := argLines(fixed, lang, 0)
	byItem := a.ItemArgs()
	for _, k := range slices.Sorted(maps.Keys(byItem)) {
		args = append(args, itemArgLine(k, itemArgValues(items, byItem[k]), lang))
	}
	return listAction{Name: name, One: one, Many: some, All: all, Tool: buttonText(a.Tool, 80), Args: args, Confirm: destructive(t),
		Hint: buttonText(a.Label, nodefile.ActionLabelMax), HintExternal: external && a.Label != ""}
}

// itemArgValues are the values items have for a field, in their order,
// each once; an item without it counts as "" (a click refuses it).
func itemArgValues(items []any, field string) []string {
	var out []string
	for _, it := range items {
		if v := itemArgValue(it, field); !slices.Contains(out, v) {
			out = append(out, v)
		}
	}
	return out
}

// itemArgValue is an item's value for an arg taken from it: a scalar of
// its, as text; "" when it has none.
func itemArgValue(it any, field string) string {
	v, err := extract(it, field)
	switch v.(type) {
	case string, float64, int, int64, bool, json.Number:
	default:
		return ""
	}
	if err != nil {
		return ""
	}
	return valueText(v)
}

// itemArgLine says an arg taken from each item for the confirm: its
// values, and that each gets its call (« account : perso, travail (un
// appel par account) »).
func itemArgLine(param string, values []string, lang string) string {
	shown := make([]string, 0, len(values))
	for i, v := range values {
		if i == 5 {
			shown = append(shown, "…")
			break
		}
		shown = append(shown, buttonText(cmpOr(v, "—"), 40))
	}
	each := tr(lang, "act.item.one")
	if len(values) > 1 {
		each = fmt.Sprintf(tr(lang, "act.item.each"), buttonText(param, 30))
	}
	return buttonText(param, 30) + " : " + strings.Join(shown, ", ") + " (" + each + ")"
}
