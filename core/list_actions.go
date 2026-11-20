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
// source's list, its id, and what it shows.
type listItem struct {
	N      int    `json:"n"`
	ID     string `json:"id"`
	Title  string `json:"title,omitempty"`
	Detail string `json:"detail,omitempty"`
}

// listAction is one of a block's actions as its button shows it: the
// words are code's, One for one item, Many with {n} for how many.
type listAction struct {
	Name string   `json:"name"`
	One  string   `json:"one"`
	Many string   `json:"many"`
	Tool string   `json:"tool"`           // server/tool
	Args []string `json:"args,omitempty"` // the fixed args, a line each, for the confirm
	Hint string   `json:"hint,omitempty"` // the author's label
	// HintExternal: the hint is on an external block, someone else's words may have written it: shown marked
	HintExternal bool `json:"hintExternal,omitempty"`
	Confirm      bool `json:"confirm,omitempty"`
}

// pageList is a List of a block the user may pick items in: the List's
// component id, its source, its rows, and the actions on them.
type pageList struct {
	Component string       `json:"component"`
	Source    string       `json:"source"`
	Items     []listItem   `json:"items"`
	Actions   []listAction `json:"actions"`
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
	if err := argsHold(schema, a.Items, a.Args); err != nil {
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

// argsHold validates fixed args against a tool's input schema, the items
// parameter no longer required (the ids go there at the click).
func argsHold(schema map[string]any, items string, args map[string]any) error {
	if schema == nil {
		return nil
	}
	s := maps.Clone(schema)
	if req, ok := s["required"].([]any); ok {
		s["required"] = slices.DeleteFunc(slices.Clone(req), func(v any) bool { return v == items })
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
// mark…), else the tool's title, else its name.
func actionWords(t *mcp.Tool, toolName string, args map[string]any, lang string) (one, many string) {
	_, short, _ := strings.Cut(toolName, "/")
	verb := tr(lang, "act.verb."+short)
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
	tail := ""
	if a := argsText(args, lang); a != "" {
		tail = " → " + a
	}
	tail += " (" + buttonText(toolName, 80) + ")"
	return fmt.Sprintf(verb, tr(lang, "act.one")) + tail, fmt.Sprintf(verb, tr(lang, "act.many")) + tail
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

// itemIDs are the ids of a source's items as the user picks them, each
// with its value as the source gives it (a number stays a number for
// the tool), in the source's order (sourceItems' reading).
func itemIDs(src nodefile.Source, value any) (map[string]any, []string) {
	if src.Items == nil || value == nil {
		return nil, nil
	}
	list, err := extract(value, src.Items.Path)
	items, ok := list.([]any)
	if err != nil || !ok {
		return nil, nil
	}
	ids := map[string]any{}
	var order []string
	for _, it := range items {
		v, err := extract(it, src.Items.ID)
		if err != nil || v == nil {
			continue
		}
		id := truncate(valueText(v), 200)
		if _, seen := ids[id]; id == "" || seen {
			continue
		}
		ids[id] = v
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

// sourceRows are a source's rows as a List the user picks in shows them.
func sourceRows(name string, src nodefile.Source, value any) []listItem {
	out := []listItem{}
	for _, it := range sourceItems(name, src, value, 0) {
		r := listItem{}
		r.N, _ = it["n"].(int)
		r.ID, _ = it["id"].(string)
		if t, _ := it["title"].(string); t != r.ID {
			r.Title = t
		}
		r.Detail, _ = it["detail"].(string)
		out = append(out, r)
	}
	return out
}

// blockLists are the Lists of a block the user may pick items in: each
// bound as a whole to the items of a source some action acts on, with its
// rows (from data, the block's data as the page gets it) and the actions'
// buttons.
func (c *core) blockLists(b *nodefile.Block, data map[string]any, lang string) []pageList {
	if len(b.Actions) == 0 {
		return nil
	}
	var comps []map[string]any
	json.Unmarshal(b.Components, &comps)
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
		for _, name := range slices.Sorted(maps.Keys(b.Sources)) {
			src := b.Sources[name]
			if listPath(name, src) != path {
				continue
			}
			var acts []listAction
			for _, an := range slices.Sorted(maps.Keys(b.Actions)) {
				if a := b.Actions[an]; a.Source == name {
					acts = append(acts, c.listAction(an, a, b.External, lang))
				}
			}
			if len(acts) > 0 {
				out = append(out, pageList{Component: id, Source: name, Items: sourceRows(name, src, data[name]), Actions: acts})
			}
		}
	}
	return out
}

// listAction is an action's button, its words code's.
func (c *core) listAction(name string, a nodefile.Action, external bool, lang string) listAction {
	_, t := c.actionTool(a.Tool)
	one, many := actionWords(t, a.Tool, a.Args, lang)
	return listAction{Name: name, One: one, Many: many, Tool: buttonText(a.Tool, 80), Args: argLines(a.Args, lang, 0), Confirm: destructive(t),
		Hint: buttonText(a.Label, nodefile.ActionLabelMax), HintExternal: external && a.Label != ""}
}

// blockSourceRows are the rows of a source of a node's block, as the
// "source" event sends them with its value: nil when no action acts on
// it.
func (c *core) blockSourceRows(id, block, name string, value any) []listItem {
	if c.nodes == nil {
		return nil
	}
	d, err := c.nodes.Read(id, 0)
	if err != nil {
		return nil
	}
	b := d.Node.Block(block)
	if b == nil {
		return nil
	}
	src, ok := b.Sources[name]
	if !ok || !slices.ContainsFunc(slices.Collect(maps.Values(b.Actions)), func(a nodefile.Action) bool { return a.Source == name }) {
		return nil
	}
	return sourceRows(name, src, value)
}
