package nodefile

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
)

func read(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func mustBytes(t *testing.T, n *Node) []byte {
	t.Helper()
	out, err := n.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// head is a front matter, then body.
func head(body string) string {
	return "---\nid: abc123\ntitle: Sam's node\nlifetime: work\ncreated: 2026-11-17T09:00:00Z\n---\n" + body
}

func TestRoundTrip(t *testing.T) {
	files, _ := filepath.Glob("testdata/*.md")
	if len(files) < 3 {
		t.Fatalf("fixtures: %v", files)
	}
	for _, f := range files {
		t.Run(filepath.Base(f), func(t *testing.T) {
			raw := read(t, filepath.Base(f))
			n, err := Parse(raw)
			if err != nil {
				t.Fatal(err)
			}
			if out := mustBytes(t, n); !bytes.Equal(out, raw) {
				t.Errorf("round trip changed the file:\n%s", out)
			}
		})
	}
}

// CRLF line ends are kept, in the front matter, the text and a block (a
// string here: git would turn a fixture's into LF).
func TestRoundTripCRLF(t *testing.T) {
	raw := []byte("---\r\nid: abc123\r\ntitle: Windows\r\nlifetime: work\r\ncreated: 2026-11-17T09:00:00Z\r\n---\r\nText.\r\n\r\n" +
		"```eriphe:block note\r\n{\"components\": [{\"id\": \"root\", \"component\": \"Text\", \"text\": \"Hi\"}]}\r\n```\r\nAfter.\r\n")
	n, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if n.Meta.Title != "Windows" || n.Block("note") == nil || n.Text() != "Text.\r\n\r\nAfter.\r\n" {
		t.Errorf("read: %+v %q", n.Meta, n.Text())
	}
	if out := mustBytes(t, n); !bytes.Equal(out, raw) {
		t.Errorf("round trip changed the file:\n%q", out)
	}
}

func TestFrontMatterFields(t *testing.T) {
	n, err := Parse(read(t, "full.md"))
	if err != nil {
		t.Fatal(err)
	}
	m := n.Meta
	at := func(s string) time.Time { v, _ := time.Parse(time.RFC3339, s); return v }
	switch {
	case m.ID != "m4ilx7q2":
		t.Errorf("id %q", m.ID)
	case m.Title != "Reply to Sam: the production app":
		t.Errorf("title %q", m.Title)
	case m.Parent != "k3v9q2m7":
		t.Errorf("parent %q", m.Parent)
	case !slices.Equal(m.Links, []string{"p8d2x4c1", "a1b2c3d4"}):
		t.Errorf("links %v", m.Links)
	case m.Lifetime != Work:
		t.Errorf("lifetime %q", m.Lifetime)
	case m.Goal != "Answer Sam within the hour.\nFind why the app fails.":
		t.Errorf("goal %q", m.Goal)
	case !m.Created.Equal(at("2026-11-17T10:02:00+01:00")):
		t.Errorf("created %v", m.Created)
	case !m.Done.Equal(at("2026-11-17T11:30:00+01:00")):
		t.Errorf("done %v", m.Done)
	case !m.Archived.Equal(at("2026-11-18T08:00:00+01:00")):
		t.Errorf("archived %v", m.Archived)
	case !m.External:
		t.Error("external not read")
	}
	var keys []string
	for _, f := range m.Extra {
		keys = append(keys, f.Key)
	}
	if !slices.Equal(keys, []string{"color", "tags"}) {
		t.Errorf("unknown fields: %v", keys)
	}
	if got := n.Blocks(); len(got) != 2 || got[0].Name != "errors" || got[1].Name != "draft" {
		t.Errorf("blocks: %v", got)
	}
	if !strings.Contains(n.Text(), "A rule above") || strings.Contains(n.Text(), "eriphe:block") {
		t.Errorf("text: %q", n.Text())
	}
}

func TestUnknownFieldsKeptOnChange(t *testing.T) {
	n, err := Parse(read(t, "full.md"))
	if err != nil {
		t.Fatal(err)
	}
	n.Meta.Title = "Sam: done"
	out := string(mustBytes(t, n))
	front, _, _ := strings.Cut(strings.TrimPrefix(out, "---\n"), "\n---\n")
	want := `id: m4ilx7q2
title: 'Sam: done'
parent: k3v9q2m7
links: [p8d2x4c1, a1b2c3d4]
lifetime: work
goal: |-
  Answer Sam within the hour.
  Find why the app fails.
created: 2026-11-17T10:02:00+01:00
done: 2026-11-17T11:30:00+01:00
archived: 2026-11-18T08:00:00+01:00
external: true
color: amber
tags:
  - mail
  - prod`
	if front != want {
		t.Errorf("front matter:\n%s\nwant:\n%s", front, want)
	}
	again, err := Parse([]byte(out))
	if err != nil || again.Meta.Title != "Sam: done" || len(again.Meta.Extra) != 2 {
		t.Fatalf("written again: %v %+v", err, again)
	}
	if _, body, _ := strings.Cut(out, "\n---\n# The"); !strings.HasSuffix(string(read(t, "full.md")), body) {
		t.Error("the body changed with the front matter")
	}
}

func TestAwkwardScalars(t *testing.T) {
	for _, title := range []string{"a: b", "# not a comment", `"quoted"`, "yes", "123", "- dash", "it's", "« Ma journée »"} {
		m := Meta{ID: "abc", Title: title, Lifetime: Work, Created: time.Date(2026, 11, 17, 9, 0, 0, 0, time.UTC), Goal: "line one\nline: two"}
		n, err := New(m, "Text.\n")
		if err != nil {
			t.Fatal(err)
		}
		out := mustBytes(t, n)
		again, err := Parse(out)
		if err != nil {
			t.Fatalf("%q: %v\n%s", title, err, out)
		}
		if again.Meta.Title != title || again.Meta.Goal != m.Goal {
			t.Errorf("%q came back as %q / %q", title, again.Meta.Title, again.Meta.Goal)
		}
		if out2 := mustBytes(t, again); !bytes.Equal(out, out2) {
			t.Errorf("%q: not stable:\n%s\n%s", title, out, out2)
		}
	}
}

func TestWriterOutputParsesToSameBytes(t *testing.T) {
	m := Meta{ID: "abc", Title: "Plan", Parent: "k3v9q2m7", Links: []string{"p8d2x4c1"}, Lifetime: Work, Goal: "A plan.",
		Created: time.Date(2026, 11, 17, 9, 0, 0, 0, time.FixedZone("", 3600))}
	n, err := New(m, "# Plan\n")
	if err != nil {
		t.Fatal(err)
	}
	if err := n.PutAuthorBlock("steps", []byte(`{"components": {"component": "Text", "text": {"@path": "/t"}}, "data": {"t": "One"}}`), ""); err != nil {
		t.Fatal(err)
	}
	if err := n.SetMark("steps", "lines", "one", MarkDone); err != nil {
		t.Fatal(err)
	}
	n.Meta.External = true
	out := mustBytes(t, n)
	want := "---\nid: abc\ntitle: Plan\nparent: k3v9q2m7\nlinks: [p8d2x4c1]\nlifetime: work\ngoal: A plan.\ncreated: 2026-11-17T09:00:00+01:00\nexternal: true\n---\n# Plan\n\n" +
		"```eriphe:block steps\n{\"components\": [\n  {\"component\":\"Text\",\"id\":\"root\",\"text\":{\"@path\":\"/t\"}}\n],\n \"data\": {\"t\":\"One\"},\n \"marks\": {\"lines\":{\"one\":\"done\"}}}\n```\n"
	if string(out) != want {
		t.Fatalf("written:\n%s\nwant:\n%s", out, want)
	}
	again, err := Parse(out)
	if err != nil {
		t.Fatal(err)
	}
	if out2 := mustBytes(t, again); !bytes.Equal(out, out2) {
		t.Errorf("not stable:\n%s", out2)
	}
}

func TestEditOneBlockLeavesTheRest(t *testing.T) {
	raw := read(t, "full.md")
	n, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if err := n.SetMark("errors", "series", "s1", MarkKeep); err != nil {
		t.Fatal(err)
	}
	out := mustBytes(t, n)
	start := bytes.Index(raw, []byte("```eriphe:block errors"))
	end := bytes.Index(raw, []byte("\nBetween the blocks."))
	if !bytes.Equal(out[:start], raw[:start]) {
		t.Error("the bytes before the block changed")
	}
	if !bytes.HasSuffix(out, raw[end:]) {
		t.Error("the bytes after the block changed")
	}
	if !bytes.Contains(out, []byte(`"marks": {"series":{"s1":"keep"}}`)) {
		t.Errorf("the mark is not written:\n%s", out)
	}
}

func TestErrors(t *testing.T) {
	block := func(name, js string) string { return head("Text.\n\n```eriphe:block " + name + "\n" + js + "\n```\n") }
	text := `{"components": [{"id": "root", "component": "Text", "text": "Hi"}]}`
	for _, tc := range []struct {
		name, file, want, block string
	}{
		{"no front matter", "Text only.\n", "starts with its front matter", ""},
		{"unclosed front matter", "---\nid: abc\n", "no closing ---", ""},
		{"bad yaml", "---\nid: [abc\n---\n", "front matter: yaml", ""},
		{"not a mapping", "---\n- a\n---\n", "a mapping of fields", ""},
		{"twice", "---\nid: abc\nid: def\n---\n", "", ""},
		{"no id", strings.Replace(head(""), "id: abc123\n", "", 1), "id \"\"", ""},
		{"bad id", strings.Replace(head(""), "abc123", "Abc-1", 1), "id \"Abc-1\"", ""},
		{"no title", strings.Replace(head(""), "title: Sam's node\n", "", 1), "title is needed", ""},
		{"bad lifetime", strings.Replace(head(""), "lifetime: work", "lifetime: forever", 1), "lifetime is work or standing", ""},
		{"no created", strings.Replace(head(""), "created: 2026-11-17T09:00:00Z\n", "", 1), "created is needed", ""},
		{"bad created", strings.Replace(head(""), "2026-11-17T09:00:00Z", "tomorrow", 1), "front matter: created", ""},
		{"own parent", strings.Replace(head(""), "lifetime", "parent: abc123\nlifetime", 1), "its own parent", ""},
		{"links twice", strings.Replace(head(""), "lifetime", "links: [abc, abc]\nlifetime", 1), "there twice", ""},
		{"links itself", strings.Replace(head(""), "lifetime", "links: [abc123]\nlifetime", 1), "link to itself", ""},
		{"bad block json", block("plan", `{"components": [`), "not a block's JSON", "plan"},
		{"two objects", block("plan", text+text), "one JSON object only", "plan"},
		{"unknown block key", block("plan", `{"components": [], "layout": 1}`), "unknown field", "plan"},
		{"unknown component", block("plan", `{"components": [{"id": "root", "component": "Carousel"}]}`), "", "plan"},
		{"no root", block("plan", `{"components": [{"id": "x", "component": "Text", "text": "Hi"}]}`), "", "plan"},
		{"missing source", block("chart", `{"components": [{"id": "root", "component": "Chart", "kind": "line", "series": {"@path": "/errors/series"}}]}`), "nothing at /errors/series", "chart"},
		{"missing data", block("chart", `{"components": [{"id": "root", "component": "Text", "text": {"@path": "/t"}}], "data": {"u": "x"}}`), "(there: u)", "chart"},
		{"bad bound link", block("link", `{"components": [{"id": "root", "component": "Link", "label": "Go", "url": {"@path": "/u"}}], "data": {"u": "javascript:alert(1)"}}`), "an http(s) address as a whole", "link"},
		{"bad link", block("link", `{"components": [{"id": "root", "component": "Link", "label": "Go", "url": "javascript:alert(1)"}]}`), "http(s) address", "link"},
		{"data marks", block("plan", `{"components": [{"id": "root", "component": "Text", "text": "Hi"}], "data": {"marks": 1}}`), "data /marks is code's", "plan"},
		{"data is source", block("plan", `{"components": [{"id": "root", "component": "Text", "text": "Hi"}], "data": {"s": 1}, "sources": {"s": {"tool": "a/b"}}}`), "place of the source s", "plan"},
		{"two kinds", block("plan", `{"components": [{"id": "root", "component": "Text", "text": "Hi"}], "sources": {"s": {"tool": "a/b", "monitor": "m"}}}`), "one of tool, resource or monitor", "plan"},
		{"source name", block("plan", `{"components": [{"id": "root", "component": "Text", "text": "Hi"}], "sources": {"Bad Name": {"tool": "a/b"}}}`), "a name is lowercase", "plan"},
		{"every", block("plan", `{"components": [{"id": "root", "component": "Text", "text": "Hi"}], "sources": {"s": {"tool": "a/b", "every": 2000}}}`), "every is 1 to 1440", "plan"},
		{"bad mark", block("plan", `{"components": [{"id": "root", "component": "Text", "text": "Hi"}], "marks": {"l": {"i": "maybe"}}}`), "keep, drop or done", "plan"},
		{"bad snapshot", block("plan", `{"components": [{"id": "root", "component": "Text", "text": "Hi"}], "snapshot": [1]}`), "snapshot: an object", "plan"},
		{"short form in a file", block("plan", `{"components": {"component": "Text", "text": "Hi"}}`), "", "plan"},
		{"unclosed block", head("```eriphe:block plan\n" + text + "\n"), "no closing ``` line", "plan"},
		{"same name twice", head("```eriphe:block plan\n" + text + "\n```\n```eriphe:block plan\n" + text + "\n```\n"), "one block so named", "plan"},
		{"bad name", head("```eriphe:block Plan\n" + text + "\n```\n"), "a block's name", "Plan"},
		{"no name", head("```eriphe:block\n" + text + "\n```\n"), "a block's name", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(tc.file))
			if err == nil {
				t.Fatal("parsed")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q, want %q", err, tc.want)
			}
			var be *BlockError
			if errors.As(err, &be) != (tc.block != "" || strings.HasPrefix(tc.name, "no name")) {
				t.Errorf("block error: %v", err)
			} else if be != nil && be.Name != tc.block {
				t.Errorf("block %q, want %q", be.Name, tc.block)
			}
		})
	}
}

func TestBlockNamePattern(t *testing.T) {
	partBlockRe := regexp.MustCompile(`^[a-z0-9][a-z0-9_]{0,31}$`) // core/thread_part.go
	for _, name := range []string{"a", "errors", "errors_5xx", "0day", "_x", "Errors", "a-b", "a b", "é", strings.Repeat("a", 32), strings.Repeat("a", 33)} {
		if BlockRe.MatchString(name) != partBlockRe.MatchString(name) {
			t.Errorf("%q: %v", name, BlockRe.MatchString(name))
		}
		_, err := Parse([]byte(head("```eriphe:block " + name + "\n{\"components\": [{\"id\": \"root\", \"component\": \"Text\", \"text\": \"Hi\"}]}\n```\n")))
		if (err == nil) != partBlockRe.MatchString(name) {
			t.Errorf("%q: %v", name, err)
		}
	}
}

func TestShortForm(t *testing.T) {
	for _, comps := range []string{
		`{"component": "Text", "text": "Hi"}`,
		`[{"component": "Text", "text": "Hi"}]`,
		`[{"id": "hello", "component": "Text", "text": "Hi"}]`,
	} {
		raw := head("```eriphe:block note\n{\"components\": " + comps + "}\n```\n")
		n, err := ParseAuthor([]byte(raw), nil)
		if err != nil {
			t.Fatalf("%s: %v", comps, err)
		}
		out := mustBytes(t, n)
		if !bytes.Contains(out, []byte(`{"component":"Text","id":"root","text":"Hi"}`)) {
			t.Errorf("%s: not completed:\n%s", comps, out)
		}
		if _, err := Parse(out); err != nil {
			t.Errorf("%s: the full form does not parse: %v", comps, err)
		}
	}
	// Two components without root are not a short form.
	raw := head("```eriphe:block note\n{\"components\": [{\"id\": \"a\", \"component\": \"Text\", \"text\": \"Hi\"}, {\"id\": \"b\", \"component\": \"Text\", \"text\": \"Ho\"}]}\n```\n")
	if _, err := ParseAuthor([]byte(raw), nil); err == nil {
		t.Error("two components without root parsed")
	}
}

func TestAuthorCodeFields(t *testing.T) {
	prev, err := Parse(read(t, "full.md"))
	if err != nil {
		t.Fatal(err)
	}
	draft := func(extra string) string {
		return `{"components": [{"id": "root", "component": "Markdown", "text": {"@path": "/text"}}], "data": {"text": "Shorter."}` + extra + `}`
	}
	refused := []struct{ name, extra string }{
		{"marks changed", `, "marks": {"lines": {"l1": "keep"}}`},
		{"marks emptied", `, "marks": {}`},
		{"external changed", `, "external": false`},
		{"snapshot written", `, "snapshot": {"at": "2026-11-17T10:00"}`},
	}
	for _, tc := range refused {
		t.Run(tc.name, func(t *testing.T) {
			n, _ := Parse(read(t, "full.md"))
			err := n.PutAuthorBlock("draft", []byte(draft(tc.extra)), "")
			if !errors.Is(err, ErrCodeField) {
				t.Fatalf("not refused: %v", err)
			}
			var be *BlockError
			if !errors.As(err, &be) || be.Name != "draft" {
				t.Errorf("error %v", err)
			}
		})
	}
	// Left out, or written alike: kept.
	for _, extra := range []string{"", `, "marks": {"lines": {"l1": "done"}}, "external": true`} {
		n, _ := Parse(read(t, "full.md"))
		if err := n.PutAuthorBlock("draft", []byte(draft(extra)), ""); err != nil {
			t.Fatalf("%q: %v", extra, err)
		}
		b := n.Block("draft")
		if b.Marks["lines"]["l1"] != MarkDone || !b.External {
			t.Errorf("%q: code's fields lost: %+v", extra, b)
		}
		if !bytes.Contains(mustBytes(t, n), []byte("Shorter.")) {
			t.Error("the block is not written")
		}
	}
	// A whole node: the front matter's external, and a block's.
	whole := string(read(t, "full.md"))
	cases := map[string]string{
		"node external off": strings.Replace(whole, "external: true\n", "external: false\n", 1),
		"block external on": strings.Replace(whole, `"marks": {},`+"\n"+` "external": false`, `"external": true`, 1),
		"new block marked":  whole + "\n```eriphe:block more\n{\"components\": [{\"id\": \"root\", \"component\": \"Text\", \"text\": \"Hi\"}], \"marks\": {\"l\": {\"i\": \"keep\"}}}\n```\n",
	}
	for name, raw := range cases {
		if _, err := ParseAuthor([]byte(raw), prev); !errors.Is(err, ErrCodeField) {
			t.Errorf("%s: not refused: %v", name, err)
		}
	}
	if _, err := ParseAuthor([]byte(whole), nil); !errors.Is(err, ErrCodeField) {
		t.Errorf("a new node said external: %v", err)
	}
	// Left out of a whole node: kept from prev.
	bare := strings.Replace(whole, "external: true\n", "", 1)
	bare = strings.Replace(bare, `"marks": {"lines": {"l1": "done"}},`+"\n"+` "external": true}`, `"data": {"text": "x"}}`, 1)
	bare = strings.Replace(bare, `"data": {"text": "I am looking at it within the hour."},`+"\n", "", 1)
	n, err := ParseAuthor([]byte(bare), prev)
	if err != nil {
		t.Fatal(err)
	}
	if !n.Meta.External || n.Block("draft").Marks["lines"]["l1"] != MarkDone || !n.Block("draft").External {
		t.Errorf("code's fields not kept: %+v", n.Block("draft"))
	}
	again, err := Parse(mustBytes(t, n))
	if err != nil || !again.Meta.External || again.Block("draft").Marks["lines"]["l1"] != MarkDone {
		t.Errorf("not written: %v", err)
	}
	// AuthorBody keeps them too.
	body := "New text.\n\n```eriphe:block draft\n" + draft("") + "\n```\n"
	if err := prev.AuthorBody(body); err != nil {
		t.Fatal(err)
	}
	if prev.Block("draft").Marks["lines"]["l1"] != MarkDone || prev.Block("errors") != nil {
		t.Errorf("body: %+v", prev.Blocks())
	}
	if err := prev.AuthorBody("```eriphe:block draft\n" + draft(`, "external": false`) + "\n```\n"); !errors.Is(err, ErrCodeField) {
		t.Errorf("body: not refused: %v", err)
	}
}

// Broken author input is an error, never a panic.
func TestAuthorBadComponents(t *testing.T) {
	for _, comps := range []string{`null`, `[null]`, `{}`, `[]`, `"x"`, `[1]`} {
		raw := `{"components": ` + comps + `}`
		n, _ := Parse([]byte(head("Text.\n")))
		if err := n.PutAuthorBlock("b", []byte(raw), ""); err == nil {
			t.Errorf("%s: put", comps)
		}
		if err := n.AuthorBody("```eriphe:block b\n" + raw + "\n```\n"); err == nil {
			t.Errorf("%s: body", comps)
		}
		if _, err := ParseAuthor([]byte(head("```eriphe:block b\n"+raw+"\n```\n")), nil); err == nil {
			t.Errorf("%s: parse", comps)
		}
	}
}

func TestPutAuthorBlockAfterOpenFence(t *testing.T) {
	n, err := Parse([]byte(head("Text.\n\n```go\nfunc main() {}\n")))
	if err != nil {
		t.Fatal(err)
	}
	if err := n.PutAuthorBlock("b", []byte(`{"components": {"component": "Text", "text": "Hi"}}`), ""); err == nil {
		t.Errorf("written after an open fence:\n%s", mustBytes(t, n))
	}
}

func TestPutAuthorBlockPlaces(t *testing.T) {
	n, err := Parse([]byte(head("Text.")))
	if err != nil {
		t.Fatal(err)
	}
	put := func(name, before string) error {
		return n.PutAuthorBlock(name, []byte(`{"components": {"component": "Text", "text": "`+name+`"}}`), before)
	}
	for _, name := range []string{"a", "b"} {
		if err := put(name, ""); err != nil {
			t.Fatal(err)
		}
	}
	if err := put("c", "a"); err != nil {
		t.Fatal(err)
	}
	if err := put("b", "c"); err != nil {
		t.Fatal(err)
	}
	if err := put("x", "nothere"); err == nil {
		t.Error("before a block not there")
	}
	var names []string
	for _, b := range n.Blocks() {
		names = append(names, b.Name)
	}
	if !slices.Equal(names, []string{"b", "c", "a"}) {
		t.Errorf("order %v", names)
	}
	again, err := Parse(mustBytes(t, n))
	if err != nil || len(again.Blocks()) != 3 || !strings.HasPrefix(again.Text(), "Text.\n\n") {
		t.Errorf("%v\n%s", err, mustBytes(t, n))
	}
}

func TestCodeAPI(t *testing.T) {
	n, err := Parse(read(t, "full.md"))
	if err != nil {
		t.Fatal(err)
	}
	if err := n.SetMark("draft", "lines", "l1", "maybe"); err == nil {
		t.Error("a mark that is none")
	}
	if err := n.SetMark("nothere", "l", "i", MarkKeep); err == nil {
		t.Error("a mark on no block")
	}
	if err := n.SetMark("draft", "lines", "l1", ""); err != nil {
		t.Fatal(err)
	}
	if err := n.SetSnapshot("errors", json.RawMessage(`{"at": "2026-11-17T10:20", "errors_5xx": {"series": [{"name": "5xx", "points": [{"x": "10:20", "y": 3}]}]}}`)); err != nil {
		t.Fatal(err)
	}
	if err := n.SetSnapshot("errors", json.RawMessage(`[1]`)); err == nil {
		t.Error("a snapshot that is no object")
	}
	if err := n.SetBlockExternal("errors", true); err != nil {
		t.Fatal(err)
	}
	again, err := Parse(mustBytes(t, n))
	if err != nil {
		t.Fatal(err)
	}
	if len(again.Block("draft").Marks) != 0 || !again.Block("errors").External || !strings.Contains(string(again.Block("errors").Snapshot), "10:20") {
		t.Errorf("code's fields: %+v %+v", again.Block("draft"), again.Block("errors"))
	}
}

// The doc's examples: the full one parses, round trips and shows its
// marks; the canonical one is what code writes.
func TestDocExamples(t *testing.T) {
	doc, err := os.ReadFile("../../docs/node-format.md")
	if err != nil {
		t.Fatal(err)
	}
	examples := regexp.MustCompile("(?s)````markdown\n(.*?)````\n").FindAllSubmatch(doc, -1)
	if len(examples) != 2 {
		t.Fatalf("%d examples", len(examples))
	}
	full := examples[0][1]
	n, err := Parse(full)
	if err != nil {
		t.Fatal(err)
	}
	if out := mustBytes(t, n); !bytes.Equal(out, full) {
		t.Errorf("round trip:\n%s", out)
	}
	if n.Block("titles").Marks["titles"]["t2"] != MarkKeep || len(n.Block("readers").Sources) != 1 {
		t.Errorf("blocks: %+v", n.Blocks())
	}
	canon := examples[1][1]
	c, err := Parse([]byte(head(string(canon))))
	if err != nil {
		t.Fatal(err)
	}
	got, err := c.Block("status").canonical()
	if err != nil || !bytes.Equal(got, canon) {
		t.Errorf("canonical form:\n%s\nwant:\n%s", got, canon)
	}
}
