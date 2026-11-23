package nodefile

import (
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
// turns format i+1 into format i+2. None yet: format 1 is the only one.
var conversions []Conversion

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
	n, err := Parse(out)
	if err != nil {
		return nil, fmt.Errorf("converted from format %d: %w", from, err)
	}
	n.Meta.Format = last
	return n.Bytes()
}
