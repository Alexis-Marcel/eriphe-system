package nodefile

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"testing"
)

const formatFile = "---\nid: a1\ntitle: Paint\nlifetime: work\ncreated: 2026-11-17T09:00:00Z\n---\nThe wall is grey.\n"

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

// Today's files say no format, and keep their bytes: the format is
// written only past 1. A file of format 2 keeps its field.
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
		if n.Meta.Format != 1 {
			t.Errorf("%s: format %d", name, n.Meta.Format)
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
