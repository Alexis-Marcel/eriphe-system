package nodefile

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"go.yaml.in/yaml/v3"
)

// A node's properties are the front matter's fields this package does not
// know (Meta.Extra), as in Obsidian: free names, each a text, a number, a
// date, a [[link]] or a list of them. tags is one of them, a list of tags
// nested with / (cuisine/recette); nothing in code knows what a property
// means. They are read from the file as they are, typed by their YAML: a
// number is a number, a date (2026-10-01, quoted or not) a date.

// TagsKey is the property that holds a node's tags.
const TagsKey = "tags"

// Kinds of a property's value.
const (
	KindText   = "text"
	KindNumber = "number"
	KindDate   = "date"
)

// Limits of what SetProp writes.
const (
	PropKeyMax   = 40  // runes of a property's name
	PropTextMax  = 300 // runes of a text value
	PropListMax  = 50  // values of a list
	PropsMax     = 30  // properties of a node
	tagRunesMax  = 60  // runes of a tag
	propNumLimit = 1e15
)

// Prop is one of a node's properties.
type Prop struct {
	Key    string  // as written
	List   bool    // a list, even of one value
	Values []Value // its values; one when not a list
}

// Value is one value of a property.
type Value struct {
	Kind string  // KindText, KindNumber or KindDate
	Text string  // as written; a date as 2006-01-02 or with its time
	Num  float64 // a number's
}

// Any is the value as JSON gives it: a number, or its text.
func (v Value) Any() any {
	if v.Kind == KindNumber {
		return v.Num
	}
	return v.Text
}

// Any is the property as JSON gives it: its value, or the list of them.
func (p Prop) Any() any {
	if !p.List {
		if len(p.Values) == 0 {
			return nil
		}
		return p.Values[0].Any()
	}
	out := make([]any, 0, len(p.Values))
	for _, v := range p.Values {
		out = append(out, v.Any())
	}
	return out
}

var (
	dateRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}([T ]\d{2}:\d{2}(:\d{2}(\.\d+)?)?(Z|[+-]\d{2}:?\d{2})?)?$`)
	tagRe  = regexp.MustCompile(`^[\p{L}\p{N}_-]+(/[\p{L}\p{N}_-]+)*$`)
)

// Props are the node's properties, in their order, those whose value is
// none of the kinds (a mapping, an empty field) left out.
func (m Meta) Props() []Prop {
	var out []Prop
	for _, f := range m.Extra {
		if p, ok := propOf(f); ok {
			out = append(out, p)
		}
	}
	return out
}

// Tags are the node's tags, as written, without a leading #.
func (m Meta) Tags() []string {
	var out []string
	for _, p := range m.Props() {
		if strings.EqualFold(p.Key, TagsKey) {
			for _, v := range p.Values {
				if t := strings.TrimPrefix(strings.TrimSpace(v.Text), "#"); t != "" {
					out = append(out, t)
				}
			}
		}
	}
	return out
}

func propOf(f Field) (Prop, bool) {
	p := Prop{Key: f.Key}
	n := f.Value
	if n == nil {
		return p, false
	}
	if n.Kind == yaml.SequenceNode && !isLink(n) {
		p.List = true
		for _, e := range n.Content {
			if v, ok := valueOf(e); ok {
				p.Values = append(p.Values, v)
			}
		}
		return p, len(p.Values) > 0
	}
	v, ok := valueOf(n)
	p.Values = []Value{v}
	return p, ok
}

// isLink is [[name]] written without quotes: YAML reads it as a list in a
// list.
func isLink(n *yaml.Node) bool {
	return n.Kind == yaml.SequenceNode && len(n.Content) == 1 && n.Content[0].Kind == yaml.SequenceNode &&
		len(n.Content[0].Content) == 1 && n.Content[0].Content[0].Kind == yaml.ScalarNode
}

func valueOf(n *yaml.Node) (Value, bool) {
	if isLink(n) {
		return Value{Kind: KindText, Text: "[[" + n.Content[0].Content[0].Value + "]]"}, true
	}
	if n.Kind != yaml.ScalarNode {
		return Value{}, false
	}
	s := n.Value
	switch n.ShortTag() {
	case "!!null":
		return Value{}, false
	case "!!int", "!!float":
		f, err := strconv.ParseFloat(strings.ReplaceAll(s, "_", ""), 64)
		if err != nil {
			var i int64
			if n.Decode(&i) != nil {
				return Value{Kind: KindText, Text: s}, true
			}
			f = float64(i)
		}
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return Value{Kind: KindText, Text: s}, true
		}
		return Value{Kind: KindNumber, Num: f, Text: s}, true
	}
	if dateRe.MatchString(strings.TrimSpace(s)) {
		return Value{Kind: KindDate, Text: strings.Replace(strings.TrimSpace(s), " ", "T", 1)}, true
	}
	return Value{Kind: KindText, Text: s}, s != ""
}

// SetProp sets a property, its value as JSON gives it: a text, a number,
// a boolean (written as text), a list of them; nil removes it. A name is
// matched without case. A known field (title, links, …) is not a property,
// nor a name starting with _ (code's fields of an item). tags is a list
// of tags, # left out: cuisine/recette.
func (m *Meta) SetProp(key string, v any) error {
	key = strings.TrimSpace(key)
	if err := propKeyOK(key); err != nil {
		return err
	}
	at := -1
	for i, f := range m.Extra {
		if strings.EqualFold(f.Key, key) {
			at = i
			break
		}
	}
	if v == nil {
		if at >= 0 {
			m.Extra = append(m.Extra[:at], m.Extra[at+1:]...)
		}
		return nil
	}
	node, err := propNode(key, v)
	if err != nil {
		return err
	}
	if at >= 0 {
		m.Extra[at] = Field{Key: m.Extra[at].Key, Value: node}
		return nil
	}
	if len(m.Props()) >= PropsMax {
		return fmt.Errorf("a node has %d properties at most", PropsMax)
	}
	m.Extra = append(m.Extra, Field{Key: key, Value: node})
	return nil
}

func propKeyOK(key string) error {
	switch {
	case key == "" || utf8.RuneCountInString(key) > PropKeyMax || strings.ContainsAny(key, "\r\n\t:#[]{},\"'"):
		return fmt.Errorf("a property's name is one word or a few, %d characters at most, without punctuation: %q", PropKeyMax, key)
	case strings.HasPrefix(key, "_"):
		return fmt.Errorf("%s: a name starting with _ is code's", key)
	}
	for _, k := range known {
		if strings.EqualFold(k, key) {
			return fmt.Errorf("%s is not a property: node_set changes it, or code does", key)
		}
	}
	return nil
}

// propNode is a property's value as YAML, checked.
func propNode(key string, v any) (*yaml.Node, error) {
	tags := strings.EqualFold(key, TagsKey)
	var list []any
	switch x := v.(type) {
	case []any:
		list = x
	case []string:
		for _, s := range x {
			list = append(list, s)
		}
	default:
		if tags {
			list = []any{x}
			break
		}
		s, err := scalar(x)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", key, err)
		}
		return s, nil
	}
	if len(list) > PropListMax {
		return nil, fmt.Errorf("%s: a list holds %d values at most", key, PropListMax)
	}
	seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Style: yaml.FlowStyle}
	for _, e := range list {
		if tags {
			t, _ := e.(string)
			t = strings.TrimPrefix(strings.TrimSpace(t), "#")
			if !tagRe.MatchString(t) || utf8.RuneCountInString(t) > tagRunesMax {
				return nil, fmt.Errorf("tags: %q is not a tag: letters, digits, _ and -, nested with / (cuisine/recette)", e)
			}
			e = t
		}
		s, err := scalar(e)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", key, err)
		}
		seq.Content = append(seq.Content, s)
	}
	return seq, nil
}

func scalar(v any) (*yaml.Node, error) {
	var out any
	switch x := v.(type) {
	case string:
		x = strings.TrimSpace(x)
		if x == "" || strings.ContainsAny(x, "\r\n") || utf8.RuneCountInString(x) > PropTextMax {
			return nil, fmt.Errorf("a text value is one line of %d characters at most", PropTextMax)
		}
		out = x
	case bool:
		out = strconv.FormatBool(x)
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) || math.Abs(x) > propNumLimit {
			return nil, errors.New("a number too large")
		}
		if x == math.Trunc(x) {
			out = int64(x)
		} else {
			out = x
		}
	case int:
		out = int64(x)
	case int64:
		out = x
	default:
		return nil, fmt.Errorf("a value is a text, a number or a list of them, not %T", v)
	}
	var n yaml.Node
	if err := n.Encode(out); err != nil {
		return nil, err
	}
	return &n, nil
}
