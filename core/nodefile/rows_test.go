package nodefile

import (
	"encoding/json"
	"strings"
	"testing"
)

// A source's rows are where its items' path says, in the block's data;
// node_find's are its nodes; a path code cannot follow plainly is none.
func TestRowsPath(t *testing.T) {
	for _, c := range []struct {
		name string
		src  Source
		want string
	}{
		{"mails", Source{Tool: "mail/search", Items: &Items{Path: "messages", ID: "id"}}, "/mails/messages"},
		{"mails", Source{Tool: "mail/search", Items: &Items{Path: "$.result.messages", ID: "id"}}, "/mails/result/messages"},
		{"list", Source{Tool: "x/list", Items: &Items{ID: "id"}}, "/list"},
		{"a/b", Source{Tool: "x/list", Items: &Items{Path: "x~y", ID: "id"}}, "/a~1b/x~0y"},
		{"notes", Source{Tool: NodeFindTool}, "/notes/nodes"},
		{"mails", Source{Tool: "mail/search", Items: &Items{Path: "messages[unread=true]", ID: "id"}}, ""},
		{"mails", Source{Tool: "mail/search", Items: &Items{Path: "groups.*.items", ID: "id"}}, ""},
		{"weather", Source{Tool: "weather/now"}, ""},
	} {
		if got := RowsPath(c.name, c.src); got != c.want {
			t.Errorf("RowsPath(%s, %+v) = %q, want %q", c.name, c.src, got, c.want)
		}
	}
}

// A component that names a source reads its rows: the file keeps the
// name, Bound turns it into the binding; a name that is not a source of
// the block, a Sections over a source with no items, or a source whose
// rows code cannot find, is refused with what to write.
func TestNamedSources(t *testing.T) {
	block := func(comps string) string {
		return "```eriphe:block mail\n{\"components\": " + comps + ", \"sources\": {" +
			`"mails": {"tool": "mail/search", "items": {"path": "messages", "id": "id", "title": "subject"}},` +
			`"notes": {"tool": "eriphe-core/node_find", "args": {"tag": "recette"}},` +
			`"meteo": {"tool": "weather/now"}` +
			"}}\n```\n"
	}
	n, err := authorNew(block(`{"component": "Sections", "items": "mails"}`))
	if err != nil {
		t.Fatal(err)
	}
	b := n.Block("mail")
	if !strings.Contains(string(b.Components), `"items":"mails"`) {
		t.Errorf("the file lost what its author wrote: %s", b.Components)
	}
	if got := string(b.Bound()); !strings.Contains(got, `"items":{"@path":"/mails/messages"}`) {
		t.Errorf("Bound: %s", got)
	}
	n, err = authorNew(block(`[{"id": "root", "component": "Column", "children": ["t", "g"]}, {"id": "t", "component": "Table", "rows": "notes"}, {"id": "g", "component": "Gallery", "items": "notes"}]`))
	if err != nil {
		t.Fatal(err)
	}
	var comps []map[string]any
	json.Unmarshal(n.Block("mail").Bound(), &comps)
	if comps[1]["rows"].(map[string]any)["@path"] != "/notes/nodes" || comps[2]["items"].(map[string]any)["@path"] != "/notes/nodes" {
		t.Errorf("node_find's rows: %v", comps)
	}
	for comps, want := range map[string]string{
		`{"component": "Sections", "items": "courrier"}`: `no source "courrier"`,
		`{"component": "Sections", "items": "notes"}`:    "lists no items",
		`{"component": "Table", "rows": "meteo"}`:        "lists no rows",
	} {
		if _, err := authorNew(block(comps)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v, want %q", comps, err, want)
		}
	}
}
