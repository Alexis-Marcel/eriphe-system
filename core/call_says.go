package main

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

// How a tool says its own call, in the user's language: a server declares
// it in the tool's _meta, under saysMetaKey, and code fills it. Nothing
// here knows a server or a tool: it reads what the tool declares.
//
//	"eriphe/says": {
//	  "text":   {"fr": "{response} {item}[ : « {comment} »]", "en": "…"},
//	  "item":   {"fr": "l'invitation", "en": "the invitation"},
//	  "values": {"response": {"accept": {"fr": "Accepter", "en": "Accept"}, …}}
//	}
//
//   - text is the sentence, by language (en, else any, when the user's is
//     missing). {item} is what the call acts on: the item a node's source
//     names (its title quoted, and its detail), several counted, else the
//     declared item words, else « 1 élément ». {name} is the argument
//     name's value: its words from values when declared (the key "" for an
//     argument left out), a date or time said as one, a list joined, a
//     short text as written; a bool or an id with no words is not said.
//   - [ … ] is a part said only when every placeholder in it has words.
//
// The tool's title stays its name in lists; this is only how a card says
// one call of it.

// saysMetaKey is the _meta key under which a tool declares how its call reads.
const saysMetaKey = "eriphe/says"

// toolSays is a tool's declaration of how its call reads.
type toolSays struct {
	Text   map[string]string                       `json:"text"`
	Item   map[string]string                       `json:"item,omitempty"`
	Values map[string]map[string]map[string]string `json:"values,omitempty"`
}

// saysOf reads a tool's declaration from its _meta; nil when it has none
// or none usable.
func saysOf(meta map[string]any) *toolSays {
	v, ok := meta[saysMetaKey]
	if !ok {
		return nil
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	var s toolSays
	if json.Unmarshal(raw, &s) != nil || len(s.Text) == 0 {
		return nil
	}
	return &s
}

// toolSaysOf is a catalog tool's declaration (saysOf), by its call name;
// nil when it declares none or is not there.
func (c *core) toolSaysOf(name string) *toolSays {
	if _, t := c.catalogTool(callTool(name)); t != nil {
		return saysOf(t.Meta)
	}
	return nil
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
// item words, else counted. n is how many it acts on (0: one).
func (s *toolSays) itemWords(labels []itemLabel, n int, noun, lang string) string {
	if len(labels) == 1 && n <= 1 {
		if l := labelLine(labels[0], lang); l != "" {
			return l
		}
	}
	if n <= 1 {
		if w := inLang(s.Item, lang); w != "" {
			return w
		}
	}
	return countNoun(noun, max(n, 1), lang)
}

// phrase is the sentence the tool declares, filled for this call: item,
// the words of {item}; loc, the zone a time with an offset is said in.
func (s *toolSays) phrase(input map[string]any, item, lang string, loc *time.Location) string {
	fill := func(name string) string {
		if name == "item" {
			return item
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
