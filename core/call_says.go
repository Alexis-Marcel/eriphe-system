package main

import (
	"cmp"
	"encoding/json"
	"fmt"
	"maps"
	"path"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// How a tool says its own call, in the user's language: a server declares
// it in the tool's _meta, under saysMetaKey, and code fills it. Nothing
// here knows a server or a tool: it reads what the tool declares.
//
//	"eriphe/says": {
//	  "text":   {"fr": "{response} {item}[ : « {comment} »]", "en": "…"},
//	  "item":   {"fr": "l'invitation", "en": "the invitation"},
//	  "items":  {"fr": ["1 invitation", "{n} invitations"], "en": […]},
//	  "done":   {"fr": ["1 répondue", "{n} répondues"], "en": […]},
//	  "values": {"response": {"accept": {"fr": "Accepter", "en": "Accept"}, …}}
//	}
//
//   - text is the sentence, by language (en, else any, when the user's is
//     missing). {item} is what the call acts on: the item a node's source
//     names (its title quoted, and its detail), several counted by items
//     (one, then many with {n}), else the declared item words, else
//     « 1 élément ». {name} is the argument name's value: its words from
//     values when declared (the key "" for an argument left out), a date or
//     time said as one, a list joined, a short text as written; a bool or
//     an id with no words is not said. {count} is how many items it acts
//     on, counted by items even when one is known by its label.
//   - [ … ] is a part said only when every placeholder in it has words.
//   - done says what calls did once they ran (one, then many with {n}),
//     else « Fait pour N éléments ».
//
// The tool's title stays its name in lists; this is only how a card says
// one call of it. A tool that declares nothing is said by its title (its
// annotations', else its own), else its name: never by a word of core's
// for its name.
//
// A server Eriphe does not write cannot declare: its preset may, in its
// place (toolPreset.Meta, presets_meta.go), what its tools' _meta would
// say. A key the tool declares itself wins.

// saysMetaKey is the _meta key under which a tool declares how its call reads.
const saysMetaKey = "eriphe/says"

// toolSays is a tool's declaration of how its call reads.
type toolSays struct {
	Text   map[string]string                       `json:"text"`
	Item   map[string]string                       `json:"item,omitempty"`
	Items  map[string][]string                     `json:"items,omitempty"`
	Done   map[string][]string                     `json:"done,omitempty"`
	Values map[string]map[string]map[string]string `json:"values,omitempty"`
}

// saysOf reads a tool's declaration from its _meta; nil when it has none
// or none usable.
func saysOf(meta map[string]any) *toolSays {
	var s toolSays
	if !metaValue(meta, saysMetaKey, &s) || len(s.Text) == 0 {
		return nil
	}
	return &s
}

// metaValue reads a declaration of a tool's _meta into v; false when it
// has none or none that reads.
func metaValue(meta map[string]any, key string, v any) bool {
	x, ok := meta[key]
	if !ok {
		return false
	}
	raw, err := json.Marshal(x)
	return err == nil && json.Unmarshal(raw, v) == nil
}

// toolSaysOf is a catalog tool's declaration (saysOf), by its call name;
// nil when it declares none or is not there.
func (c *core) toolSaysOf(name string) *toolSays {
	if u, t := c.catalogTool(callTool(name)); t != nil {
		return saysOf(c.toolMeta(u, t))
	}
	return nil
}

// toolMeta is a catalog tool's _meta: what its preset declares for it
// (presets_meta.go), under what it declares itself.
func (c *core) toolMeta(u *upstream, t *mcp.Tool) map[string]any {
	var p toolPreset
	if s, ok := c.conf().Servers[u.name]; ok && s.URL != "" {
		p, _ = presetByURL(s.URL)
	}
	if len(p.Meta) == 0 {
		return t.Meta
	}
	out := map[string]any{}
	// Patterns first, the longest last, then the tool's exact name, then
	// the tool itself.
	var pats []string
	for k := range p.Meta {
		if ok, _ := path.Match(k, t.Name); ok && k != t.Name {
			pats = append(pats, k)
		}
	}
	slices.SortFunc(pats, func(a, b string) int { return cmp.Compare(len(a), len(b)) })
	for _, k := range append(pats, t.Name) {
		maps.Copy(out, p.Meta[k])
	}
	maps.Copy(out, t.Meta)
	return out
}

// counted says n of a declared counted form (one, then many with {n}) in
// lang; "" when none is declared.
func counted(m map[string][]string, n int, lang string) string {
	forms, ok := m[lang]
	if !ok {
		forms, ok = m["en"]
	}
	if !ok {
		for _, k := range slices.Sorted(maps.Keys(m)) {
			forms = m[k]
			break
		}
	}
	if len(forms) == 0 {
		return ""
	}
	f := forms[len(forms)-1]
	if n == 1 {
		f = forms[0]
	}
	return strings.ReplaceAll(f, "{n}", fmt.Sprint(n))
}

// inLang is a declared text in lang: else in English, else in any.
func inLang(m map[string]string, lang string) string {
	if s, ok := m[lang]; ok {
		return s
	}
	if s, ok := m["en"]; ok {
		return s
	}
	for _, k := range slices.Sorted(maps.Keys(m)) {
		return m[k]
	}
	return ""
}

// itemWords say what a call acts on, for {item}: one item by its label,
// several counted (their lines below the sentence), else the declared
// item words, else counted. n is how many it acts on (0: one). A nil
// declaration counts them as « éléments ».
func (s *toolSays) itemWords(labels []itemLabel, n int, lang string) string {
	if len(labels) == 1 && n <= 1 {
		if l := labelLine(labels[0], lang); l != "" {
			return l
		}
	}
	if s == nil {
		return countItems(max(n, 1), lang)
	}
	if n <= 1 {
		if w := inLang(s.Item, lang); w != "" {
			return w
		}
	}
	return s.countWords(n, lang)
}

// countWords say n items as the tool counts them (items), else as
// « éléments ».
func (s *toolSays) countWords(n int, lang string) string {
	if w := counted(s.Items, max(n, 1), lang); w != "" {
		return w
	}
	return countItems(max(n, 1), lang)
}

// phrase is the sentence the tool declares, filled for this call: item,
// the words of {item}; count, those of {count}; loc, the zone a time with
// an offset is said in.
func (s *toolSays) phrase(input map[string]any, item, count, lang string, loc *time.Location) string {
	fill := func(name string) string {
		switch name {
		case "item":
			return item
		case "count":
			return count
		}
		return s.argWords(name, input[name], lang, loc)
	}
	var b strings.Builder
	text := inLang(s.Text, lang)
	for text != "" {
		i := strings.IndexByte(text, '[')
		if i < 0 {
			b.WriteString(fillText(text, fill, false))
			break
		}
		b.WriteString(fillText(text[:i], fill, false))
		j := strings.IndexByte(text[i:], ']')
		if j < 0 {
			b.WriteString(fillText(text[i+1:], fill, true))
			break
		}
		b.WriteString(fillText(text[i+1:i+j], fill, true))
		text = text[i+j+1:]
	}
	return upperFirst(strings.Join(strings.Fields(b.String()), " "))
}

// fillText fills a template's {name}s; optional, it is "" when one of
// them has no words.
func fillText(t string, fill func(string) string, optional bool) string {
	var b strings.Builder
	for {
		i := strings.IndexByte(t, '{')
		if i < 0 {
			b.WriteString(t)
			return b.String()
		}
		j := strings.IndexByte(t[i:], '}')
		if j < 0 {
			b.WriteString(t)
			return b.String()
		}
		b.WriteString(t[:i])
		w := fill(t[i+1 : i+j])
		if w == "" && optional {
			return ""
		}
		b.WriteString(w)
		t = t[i+j+1:]
	}
}

// argWords say an argument's value: its declared words, a date or a
// time in words, a list joined, a short text as written; "" for none.
func (s *toolSays) argWords(name string, v any, lang string, loc *time.Location) string {
	key := ""
	if v != nil {
		key = valueText(v)
	}
	if words, ok := s.Values[name][key]; ok {
		return inLang(words, lang)
	}
	if words, ok := s.Values[name][strings.ToLower(strings.TrimSpace(key))]; ok {
		return inLang(words, lang) // « Archive » as archive
	}
	switch x := v.(type) {
	case string:
		x = strings.TrimSpace(x)
		if w := dateWords(x, lang, loc); w != "" {
			return w
		}
		return buttonText(x, 60)
	case []any:
		var parts []string
		for _, e := range x {
			if str, ok := e.(string); ok && strings.TrimSpace(str) != "" {
				parts = append(parts, strings.TrimSpace(str))
			}
		}
		return buttonText(strings.Join(parts, ", "), 60)
	case float64:
		return fmt.Sprint(x)
	}
	return ""
}

// dateFormats are the ways a call writes a date or a time.
var dateFormats = []string{time.RFC3339, "2006-01-02T15:04:05", "2006-01-02T15:04", "2006-01-02 15:04", time.DateOnly}

// dateWords say a date or a time as people read it: « lundi 24 novembre,
// 14 h 10 », “Monday, November 24, 14:10”; "" when s is not one. A time
// with an offset is said in loc; one without, as written.
func dateWords(s, lang string, loc *time.Location) string {
	if utf8.RuneCountInString(s) < len(time.DateOnly) || s[0] < '0' || s[0] > '9' {
		return ""
	}
	for _, f := range dateFormats {
		t, err := time.Parse(f, s)
		if err != nil {
			continue
		}
		if f == time.RFC3339 && loc != nil {
			t = t.In(loc)
		}
		var day string
		if lang == "fr" {
			day = fmt.Sprintf("%s %d %s", frDays[t.Weekday()], t.Day(), frMonths[t.Month()-1])
		} else {
			day = t.Format("Monday, January 2")
		}
		if f == time.DateOnly {
			return day
		}
		if lang == "fr" {
			if t.Minute() == 0 {
				return fmt.Sprintf("%s, %d h", day, t.Hour())
			}
			return fmt.Sprintf("%s, %d h %02d", day, t.Hour(), t.Minute())
		}
		return day + ", " + t.Format("15:04")
	}
	return ""
}
