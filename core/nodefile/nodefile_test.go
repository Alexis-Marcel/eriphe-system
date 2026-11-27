package nodefile

import (
	"bytes"
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
	return "---\nid: abc123\ntitle: Sam's node\ncreated: 2026-11-17T09:00:00Z\n---\n" + body
}

// authorNew is a new node whose body an author wrote (AuthorBody).
func authorNew(body string) (*Node, error) {
	n, err := Parse([]byte(head("")))
	if err != nil {
		return nil, err
	}
	if err := n.AuthorBody(body); err != nil {
		return nil, err
	}
	return n, n.Validate()
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

// CRLF line ends are kept, in the front matter and the text (a string
// here: git would turn a fixture's into LF).
func TestRoundTripCRLF(t *testing.T) {
	raw := []byte("---\r\nid: abc123\r\ntitle: Windows\r\ncreated: 2026-11-17T09:00:00Z\r\n---\r\nText.\r\n\r\nAfter.\r\n")
	n, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if n.Meta.Title != "Windows" || n.Text() != "Text.\r\n\r\nAfter.\r\n" {
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
	want := `format: 4
id: m4ilx7q2
title: 'Sam: done'
parent: k3v9q2m7
links: [p8d2x4c1, a1b2c3d4]
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
		m := Meta{ID: "abc", Title: title, Created: time.Date(2026, 11, 17, 9, 0, 0, 0, time.UTC), Goal: "line one\nline: two"}
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
	m := Meta{ID: "abc", Title: "Plan", Parent: "k3v9q2m7", Links: []string{"p8d2x4c1"}, Goal: "A plan.",
		Created: time.Date(2026, 11, 17, 9, 0, 0, 0, time.FixedZone("", 3600))}
	n, err := New(m, "# Plan\n")
	if err != nil {
		t.Fatal(err)
	}
	n.Meta.External = true
	out := mustBytes(t, n)
	want := "---\nid: abc\ntitle: Plan\nparent: k3v9q2m7\nlinks: [p8d2x4c1]\ngoal: A plan.\ncreated: 2026-11-17T09:00:00+01:00\nexternal: true\n---\n# Plan\n"
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

func TestErrors(t *testing.T) {
	for _, tc := range []struct {
		name, file, want string
	}{
		{"no front matter", "Text only.\n", "starts with its front matter"},
		{"unclosed front matter", "---\nid: abc\n", "no closing ---"},
		{"bad yaml", "---\nid: [abc\n---\n", "front matter: yaml"},
		{"not a mapping", "---\n- a\n---\n", "a mapping of fields"},
		{"twice", "---\nid: abc\nid: def\n---\n", ""},
		{"no id", strings.Replace(head(""), "id: abc123\n", "", 1), "id \"\""},
		{"bad id", strings.Replace(head(""), "abc123", "Abc-1", 1), "id \"Abc-1\""},
		{"no title", strings.Replace(head(""), "title: Sam's node\n", "", 1), "title is needed"},
		{"no created", strings.Replace(head(""), "created: 2026-11-17T09:00:00Z\n", "", 1), "created is needed"},
		{"bad created", strings.Replace(head(""), "2026-11-17T09:00:00Z", "tomorrow", 1), "front matter: created"},
		{"own parent", strings.Replace(head(""), "created", "parent: abc123\ncreated", 1), "its own parent"},
		{"links twice", strings.Replace(head(""), "created", "links: [abc, abc]\ncreated", 1), "there twice"},
		{"links itself", strings.Replace(head(""), "created", "links: [abc123]\ncreated", 1), "link to itself"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(tc.file))
			if err == nil {
				t.Fatal("parsed")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q, want %q", err, tc.want)
			}
		})
	}
}

// The doc's full example parses, round trips, and is a page the check
// finds nothing in: its references, its do:, card: and ask: links.
func TestDocExamples(t *testing.T) {
	doc, err := os.ReadFile("../../docs/node-format.md")
	if err != nil {
		t.Fatal(err)
	}
	examples := regexp.MustCompile("(?s)````markdown\n(.*?)````\n").FindAllSubmatch(doc, -1)
	if len(examples) != 1 {
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
	p, err := n.Page()
	if err != nil {
		t.Fatal(err)
	}
	if pr := p.Check(func(string) bool { return true }); len(pr) > 0 {
		t.Errorf("problems %+v", pr)
	}
	if len(p.Refs) != 2 || len(p.Links) != 3 {
		t.Errorf("refs %+v, links %+v", p.Refs, p.Links)
	}
}

// A file written when a step's node said the steps it waited for (after)
// still reads: a field this package does not know, written back as it
// was.
func TestOldAfterReads(t *testing.T) {
	raw := strings.Replace(head("Text.\n"), "created", "after: [s1]\ncreated", 1)
	n, err := Parse([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if len(n.Meta.Extra) != 1 || n.Meta.Extra[0].Key != "after" || string(mustBytes(t, n)) != raw {
		t.Errorf("extra %+v, written:\n%s", n.Meta.Extra, mustBytes(t, n))
	}
}

// opened_by is code's: it reads and writes back as it was, an author's
// bytes leave it out, and an author's body keeps it.
func TestOpenedByIsCodes(t *testing.T) {
	meta := Meta{ID: "a1", Title: "Alerte", Created: time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC),
		OpenedBy: &Origin{By: OpenedSkill, Skill: "tri", On: "monitor:mails", Task: "t3"}}
	prev, err := New(meta, "Le disque.\n")
	if err != nil {
		t.Fatal(err)
	}
	raw := string(mustBytes(t, prev))
	if !strings.Contains(raw, "opened_by:\n  by: skill\n  skill: tri\n  trigger: monitor:mails\n  task: t3\n") {
		t.Fatalf("written:\n%s", raw)
	}
	again, err := Parse([]byte(raw))
	if err != nil || again.Meta.OpenedBy == nil || *again.Meta.OpenedBy != *meta.OpenedBy {
		t.Fatalf("read back %+v %v", again.Meta.OpenedBy, err)
	}
	if b, _ := again.AuthorBytes(); strings.Contains(string(b), "opened_by") {
		t.Errorf("an author's bytes say it:\n%s", b)
	}
	if err := again.AuthorBody("Autre texte.\n"); err != nil || again.Meta.OpenedBy == nil || *again.Meta.OpenedBy != *meta.OpenedBy {
		t.Errorf("an author's body: %+v %v", again.Meta.OpenedBy, err)
	}
}

// A heading that repeats the node's title at the top of an author's body
// goes; any other heading stays.
func TestAuthorBodyDropsTitleHeading(t *testing.T) {
	for body, want := range map[string]string{
		"# Sam's node\n\nOù on en est.\n":         "Où on en est.\n",
		"\n## **sam's NODE** ##\nOù on en est.\n": "Où on en est.\n",
		"# Le plan\n\nOù on en est.\n":            "# Le plan\n\nOù on en est.\n",
		"Où on en est.\n# Sam's node\n":           "Où on en est.\n# Sam's node\n",
		"# Sam's node\n":                          "",
	} {
		n, err := authorNew(body)
		if err != nil {
			t.Fatalf("%q: %v", body, err)
		}
		if got := n.Text(); got != want {
			t.Errorf("%q: text %q, want %q", body, got, want)
		}
	}
}
