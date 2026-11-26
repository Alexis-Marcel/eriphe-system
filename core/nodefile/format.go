package nodefile

import (
	"bytes"
	"errors"
	"fmt"

	"go.yaml.in/yaml/v3"
)

// A node is the user's note: it outlives every change of Eriphe. Each
// file says the format it is written in, its front matter's format (1
// when absent: the files written before the field). A change of the
// format comes with its conversion, from the format before it, and a test
// that reads the old format; the store converts an older file at start,
// in a commit of its own (the old one stays in the history), and refuses
// a newer one (docs/node-format.md, "Format version").

// Conversion turns the bytes of a file in one format into the next
// format's. It need not write the front matter's format: Upgrade does.
type Conversion func(raw []byte) ([]byte, error)

// conversions are the format's changes, the oldest first: conversions[i]
// turns format i+1 into format i+2 (ConversionsWith, topage.go). Without
// snapshots: a version of the history is converted without the values
// code read when it was the last.
var conversions = ConversionsWith(nil)

// dropLifetime turns format 1 into format 2: the front matter's lifetime
// (work or standing) goes, every other byte kept. A node a skill
// maintains is told by the skill, never by its file.
func dropLifetime(raw []byte) ([]byte, error) {
	front, _, body, err := splitFront(raw)
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	for rest := front; len(rest) > 0; {
		line, next, _ := cutLine(rest)
		rest = next
		if bytes.HasPrefix(line, []byte("lifetime:")) {
			continue // a scalar on its own line: work or standing
		}
		out.Write(line)
	}
	out.Write(body)
	return out.Bytes(), nil
}

// Conversions are the format's changes, the oldest first; the format
// written today is the one after the last (1 + their number).
func Conversions() []Conversion { return conversions }

// ErrNewerFormat: a file is in a format newer than the ones this Eriphe
// knows. It is never read nor written: Eriphe is updated first.
var ErrNewerFormat = errors.New("a format newer than this Eriphe's")

// FormatOf is the format a file says it is in: its front matter's
// format, 1 when absent. Only that field is read, whatever the rest of
// the file holds: the front matter stays YAML whatever the format.
func FormatOf(raw []byte) (int, error) {
	_, yml, _, err := splitFront(raw)
	if err != nil {
		return 0, err
	}
	var f struct {
		Format int `yaml:"format"`
	}
	if err := yaml.Unmarshal(yml, &f); err != nil {
		return 0, fmt.Errorf("front matter: %w", err)
	}
	switch {
	case f.Format == 0:
		return 1, nil
	case f.Format < 0:
		return 0, errors.New("front matter: format: a whole number, 1 or more")
	}
	return f.Format, nil
}

// Upgrade brings a file to the last format convs make (1 + len(convs)),
// through each conversion from its own, and writes that format in its
// front matter. A file in the last format is given back as it is; a file
// in a newer one is refused (ErrNewerFormat). What it gives reads as a
// node, or it is an error.
func Upgrade(raw []byte, convs []Conversion) ([]byte, error) {
	from, err := FormatOf(raw)
	if err != nil {
		return nil, err
	}
	last := len(convs) + 1
	switch {
	case from > last:
		return nil, fmt.Errorf("format %d, %w (%d)", from, ErrNewerFormat, last)
	case from == last:
		return raw, nil
	}
	out := raw
	for i := from - 1; i < len(convs); i++ {
		if out, err = convs[i](out); err != nil {
			return nil, fmt.Errorf("conversion from format %d: %w", i+1, err)
		}
	}
	if out, err = sayFormat(out, last); err != nil {
		return nil, fmt.Errorf("converted from format %d: %w", from, err)
	}
	if n, err := Parse(out); err != nil {
		return nil, fmt.Errorf("converted from format %d: %w", from, err)
	} else if n.Meta.Format != last {
		return nil, fmt.Errorf("converted from format %d: it says format %d", from, n.Meta.Format)
	}
	return out, nil
}

// sayFormat writes format in a file's front matter, every other byte
// kept: its format line replaced, or one put first.
func sayFormat(raw []byte, format int) ([]byte, error) {
	front, _, body, err := splitFront(raw)
	if err != nil {
		return nil, err
	}
	first, rest, _ := cutLine(front)
	eol := "\n"
	if bytes.HasSuffix(first, []byte("\r\n")) {
		eol = "\r\n" // the file's own line ends
	}
	said := fmt.Appendf(nil, "format: %d%s", format, eol)
	out := append([]byte{}, first...)
	if bytes.Contains(front, []byte("\nformat:")) {
		for len(rest) > 0 {
			var line []byte
			line, rest, _ = cutLine(rest)
			if bytes.HasPrefix(line, []byte("format:")) {
				line = said
			}
			out = append(out, line...)
		}
	} else {
		out = append(append(out, said...), rest...)
	}
	return append(out, body...), nil
}
