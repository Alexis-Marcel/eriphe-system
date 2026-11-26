package nodefile

import (
	"bytes"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
)

const formatFile = "---\nid: a1\ntitle: Paint\ncreated: 2026-11-17T09:00:00Z\n---\nThe wall is grey.\n"

func TestFormatOf(t *testing.T) {
	for raw, want := range map[string]int{
		formatFile: 1,
		strings.Replace(formatFile, "---\n", "---\nformat: 2\n", 1): 2,
		// Only the field is read: the rest may be a format this code no
		// longer parses.
		"---\nformat: 7\nwhat: [a future shape\n---\n": 0,
		"---\nformat: 7\nwhatever: {a: 1}\n---\n":      7,
	} {
		got, err := FormatOf([]byte(raw))
		if want == 0 {
			if err == nil {
				t.Errorf("%q: format %d, want an error", raw, got)
			}
			continue
		}
		if err != nil || got != want {
			t.Errorf("%q: %d %v, want %d", raw, got, err, want)
		}
	}
	if _, err := FormatOf([]byte(strings.Replace(formatFile, "---\n", "---\nformat: -1\n", 1))); err == nil {
		t.Error("format -1 read")
	}
	if _, err := Parse([]byte(strings.Replace(formatFile, "---\n", "---\nformat: 0\n", 1))); err == nil {
		t.Error("format 0 parsed")
	}
}

// Today's files say format 2 and keep their bytes; the format is written
// only past 1, so a file of format 1 written again says none.
func TestFormatRoundTrip(t *testing.T) {
	for _, name := range []string{"testdata/full.md", "testdata/fenced.md"} {
		raw, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		n, err := Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		if out, _ := n.Bytes(); n.Meta.Format != 2 || !bytes.Equal(out, raw) {
			t.Errorf("%s: format %d, bytes kept %v", name, n.Meta.Format, bytes.Equal(out, raw))
		}
	}
	two := strings.Replace(formatFile, "---\n", "---\nformat: 2\n", 1)
	n, err := Parse([]byte(two))
	if err != nil || n.Meta.Format != 2 {
		t.Fatalf("format 2: %v %+v", err, n)
	}
	n.Meta.Title = "Paint again"
	out, _ := n.Bytes()
	if !strings.HasPrefix(string(out), "---\nformat: 2\nid: a1\n") {
		t.Fatalf("written again:\n%s", out)
	}
	n, _ = Parse([]byte(formatFile))
	n.Meta.Title = "Paint again"
	if out, _ := n.Bytes(); strings.Contains(string(out), "format") {
		t.Fatalf("format 1 written:\n%s", out)
	}
}

func TestUpgrade(t *testing.T) {
	grey := func(raw []byte) ([]byte, error) {
		return bytes.ReplaceAll(raw, []byte("grey"), []byte("gray")), nil
	}
	same, err := Upgrade([]byte(formatFile), nil)
	if err != nil || string(same) != formatFile {
		t.Fatalf("today's format changed: %v\n%s", err, same)
	}
	out, err := Upgrade([]byte(formatFile), []Conversion{grey})
	if err != nil {
		t.Fatal(err)
	}
	n, err := Parse(out)
	if err != nil || n.Meta.Format != 2 || !strings.Contains(n.Text(), "gray") {
		t.Fatalf("upgraded: %v\n%s", err, out)
	}
	if again, _ := Upgrade(out, []Conversion{grey}); !bytes.Equal(again, out) {
		t.Fatal("a file in the last format was converted again")
	}
	if _, err := Upgrade(out, nil); !errors.Is(err, ErrNewerFormat) {
		t.Fatalf("a newer format: %v", err)
	}
	bad := func([]byte) ([]byte, error) { return []byte("not a node"), nil }
	if _, err := Upgrade([]byte(formatFile), []Conversion{bad}); err == nil {
		t.Fatal("a conversion that breaks the file")
	}
}

// Format 1 said each node's lifetime (work or standing); format 2 says
// none: a node a skill maintains is told by the skill. The conversion
// drops the field and keeps every other byte, the format said once.
func TestFormat1DropsLifetime(t *testing.T) {
	old, err := os.ReadFile("testdata/format1.md")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(old, []byte("\nlifetime: standing\n")) {
		t.Fatal("testdata/format1.md says no lifetime")
	}
	out, err := Upgrade(old, Conversions()[:1])
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(strings.Replace(string(old), "lifetime: standing\n", "", 1), "---\n", "---\nformat: 2\n", 1)
	if string(out) != want {
		t.Fatalf("converted:\n%s\nwant:\n%s", out, want)
	}
	n, err := Parse(out)
	if err != nil || n.Meta.Format != 2 || slices.ContainsFunc(n.Meta.Extra, func(f Field) bool { return f.Key == "lifetime" }) {
		t.Fatalf("parsed: %v %+v", err, n.Meta)
	}
	if again, err := Upgrade(out, Conversions()[:1]); err != nil || !bytes.Equal(again, out) {
		t.Fatalf("converted again: %v", err)
	}
	if full, _ := os.ReadFile("testdata/full.md"); string(out) != string(full) {
		t.Fatal("format 1's full.md converted is not today's full.md")
	}
}

// Only the front matter's own lifetime line goes: the same words in the
// body, or indented under another key, stay; a format line already there
// is said again in place; a CRLF file keeps its line endings.
func TestFormat1LifetimeLineOnly(t *testing.T) {
	for _, c := range []struct{ name, in, want string }{
		{"format said",
			"---\nformat: 1\nid: a1b2c3d4e5\ntitle: Plan\nlifetime: work\ncreated: 2026-11-01T09:00:00Z\nupdated: 2026-11-01T09:00:00Z\n---\nLe plan.\n",
			"---\nformat: 2\nid: a1b2c3d4e5\ntitle: Plan\ncreated: 2026-11-01T09:00:00Z\nupdated: 2026-11-01T09:00:00Z\n---\nLe plan.\n"},
		{"body and nested kept",
			"---\nid: a1b2c3d4e5\ntitle: Plan\nlifetime: standing\nprops:\n  lifetime: longue\ncreated: 2026-11-01T09:00:00Z\nupdated: 2026-11-01T09:00:00Z\n---\nlifetime: standing\n",
			"---\nformat: 2\nid: a1b2c3d4e5\ntitle: Plan\nprops:\n  lifetime: longue\ncreated: 2026-11-01T09:00:00Z\nupdated: 2026-11-01T09:00:00Z\n---\nlifetime: standing\n"},
		{"crlf",
			"---\r\nid: a1b2c3d4e5\r\ntitle: Plan\r\nlifetime: standing\r\ncreated: 2026-11-01T09:00:00Z\r\nupdated: 2026-11-01T09:00:00Z\r\n---\r\nLe plan.\r\n",
			"---\r\nformat: 2\r\nid: a1b2c3d4e5\r\ntitle: Plan\r\ncreated: 2026-11-01T09:00:00Z\r\nupdated: 2026-11-01T09:00:00Z\r\n---\r\nLe plan.\r\n"},
	} {
		out, err := Upgrade([]byte(c.in), Conversions()[:1])
		if err != nil || string(out) != c.want {
			t.Errorf("%s: %v\n%q\nwant\n%q", c.name, err, out, c.want)
		}
	}
}
