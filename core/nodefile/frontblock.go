package nodefile

import (
	"errors"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"
)

// A page written with a front matter of its own at its top, as Obsidian
// writes a note (a --- line, a YAML mapping, a --- line), holds the
// node's properties: they are taken as node_set would set them, and the
// block leaves the page. All of the block or nothing: a block one of
// whose fields is not a property (title, id, parent and the other fields
// code or node_set writes, a name starting with _, a mapping as a value)
// stays in the page as text, nothing lost, and Check tells its author.

// frontBlock cuts a front-matter block at the top of a body, after blank
// lines: its fields, the block as written, and the body after it, the
// blank lines after it dropped. ok is false when the body does not start
// with one: a --- line, a YAML mapping of named fields, a --- line.
func frontBlock(body string) (fields []Field, block, rest string, ok bool) {
	at := 0
	for at < len(body) {
		line, _, _ := strings.Cut(body[at:], "\n")
		if strings.TrimSpace(line) != "" {
			break
		}
		at += len(line) + 1
	}
	if at >= len(body) {
		return nil, "", "", false
	}
	front, yml, after, err := splitFront([]byte(body[at:]))
	if err != nil {
		return nil, "", "", false
	}
	var doc yaml.Node
	if yaml.Unmarshal(yml, &doc) != nil || len(doc.Content) == 0 {
		return nil, "", "", false
	}
	top := doc.Content[0]
	if top.Kind != yaml.MappingNode || len(top.Content) == 0 {
		return nil, "", "", false
	}
	for i := 0; i+1 < len(top.Content); i += 2 {
		k := top.Content[i]
		if k.Kind != yaml.ScalarNode || strings.TrimSpace(k.Value) == "" {
			return nil, "", "", false
		}
		fields = append(fields, Field{Key: strings.TrimSpace(k.Value), Value: top.Content[i+1]})
	}
	rest = string(after)
	for {
		line, next, cut := strings.Cut(rest, "\n")
		if !cut || strings.TrimSpace(line) != "" {
			break
		}
		rest = next
	}
	if strings.TrimSpace(rest) == "" {
		rest = ""
	}
	return fields, string(front), rest, true
}

// foldProps sets fields as the node's properties, all or none: an error
// leaves m as it was. A field with no value is skipped. With keep, a
// property m has already keeps its value; else the field's wins.
func (m *Meta) foldProps(fields []Field, keep bool) error {
	out := *m
	out.Extra = slices.Clone(m.Extra)
	for _, f := range fields {
		if noValue(f.Value) {
			continue
		}
		if err := propKeyOK(f.Key); err != nil {
			return err
		}
		p, ok := propOf(f)
		if !ok || (p.List && len(p.Values) != len(f.Value.Content)) {
			return errNotProp
		}
		if keep && slices.ContainsFunc(out.Extra, func(e Field) bool { return strings.EqualFold(e.Key, f.Key) }) {
			continue
		}
		if err := out.SetProp(f.Key, p.Any()); err != nil {
			return err
		}
	}
	m.Extra = out.Extra
	return nil
}

var errNotProp = errors.New("a value is a text, a number, a date, a [[link]] or a list of them")

// noValue tells a field written without a value: empty, null or [].
func noValue(n *yaml.Node) bool {
	switch {
	case n == nil:
		return true
	case n.Kind == yaml.ScalarNode:
		return n.ShortTag() == "!!null" || n.Value == ""
	case n.Kind == yaml.SequenceNode:
		return len(n.Content) == 0
	}
	return false
}

// foldFront takes the front-matter block at the top of a body an author
// writes as the node's properties, the block's fields winning, and gives
// the body without it. A block the node's page holds already is not
// written now: it is left as it is, as is one that does not fold.
func (n *Node) foldFront(body string) string {
	fields, block, rest, ok := frontBlock(body)
	if !ok {
		return body
	}
	if _, had, _, ok := frontBlock(n.body); ok && had == block {
		return body
	}
	if n.Meta.foldProps(fields, false) != nil {
		return body
	}
	return rest
}

// foldFrontBlock turns format 3 into format 4: a front-matter block an
// author wrote at the top of a page, kept there as text until format 4,
// becomes the node's properties (one its front matter has already keeps
// its value: set later, by node_set) and leaves the page. A file without
// one, or with one that does not fold, keeps its bytes.
func foldFrontBlock(raw []byte) ([]byte, error) {
	n, err := Parse(raw)
	if err != nil {
		return nil, err
	}
	fields, _, rest, ok := frontBlock(n.body)
	if !ok || n.Meta.foldProps(fields, true) != nil {
		return raw, nil
	}
	n.body = rest
	return n.Bytes()
}
