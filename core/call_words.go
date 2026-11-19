package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

// A kept call read plainly (calls and runcalls cards): what its yes does,
// in a sentence of code's, and the items it acts on as the user sees them
// in their node; its exact call (tool and parameters) stays under the
// card's Détails, and the yes runs that, as it is shown.
//
//   - The sentence is the call's action (callSentence): for the usual
//     actions on mails (move, trash, mark), by the tool and its
//     parameters, with how many; for any other, the tool's name (callVerb).
//   - The items are those of its node's sources its input names by their
//     id, with their title and detail (a mail's subject and sender) as
//     the source lists them, kept with the call when it is kept (the item
//     may leave the list after); an id no source lists is counted, never
//     guessed. Never the words a model wrote of them (an item's note).
//   - A call kept after reading external content stays « extérieur »:
//     the item's words are someone else's, shown quoted, the badge says it.

// itemLabel is an item a call acts on, as its node's source lists it.
type itemLabel struct {
	ID     string `json:"id"`
	Title  string `json:"title,omitempty"`
	Detail string `json:"detail,omitempty"`
}

// callLabelsMax is the most items one call names.
const callLabelsMax = 20

// callItemLabels are the items a call's input names by their id, among
// those the node's sources of the call's own server list.
func (c *core) callItemLabels(node, tool string, input map[string]any) []itemLabel {
	server, _, ok := splitToolName(tool)
	if node == "" || c.nodes == nil || !ok {
		return nil
	}
	values := map[string]bool{}
	inputValues(input, values)
	var out []itemLabel
	seen := map[string]bool{}
	for _, r := range c.nodeReads(node) {
		if r.Source.Items == nil || r.Source.Monitor != "" || c.sourceServer(r.Source) != server {
			continue
		}
		for _, it := range sourceItems(r.Name, r.Source, r.Value, 0) {
			id, _ := it["id"].(string)
			if !values[id] || seen[id] {
				continue
			}
			seen[id] = true
			l := itemLabel{ID: id}
			if t, _ := it["title"].(string); t != id {
				l.Title = t
			}
			l.Detail, _ = it["detail"].(string)
			out = append(out, l)
			if len(out) == callLabelsMax {
				return out
			}
		}
	}
	return out
}

// labelsOf are the items a kept call acts on: those kept with it, else
// those its node lists now.
func (c *core) labelsOf(a accessQuestion) []itemLabel {
	if len(a.Labels) > 0 || a.Node == "" {
		return a.Labels
	}
	name, _, _ := strings.Cut(a.Call, " ")
	var input map[string]any
	json.Unmarshal([]byte(a.Input), &input)
	return c.callItemLabels(a.Node, name, input)
}

// labelLine says an item in a line: its title quoted, and its detail.
func labelLine(l itemLabel, lang string) string {
	title := truncate(oneLine(l.Title), 80)
	detail := truncate(oneLine(l.Detail), 60)
	switch {
	case title != "" && detail != "":
		return fmt.Sprintf(tr(lang, "card.call.item"), title, detail)
	case title != "":
		return fmt.Sprintf(tr(lang, "card.call.itemt"), title)
	}
	return ""
}

// callSentence says what a kept call does, in a sentence: the action, and
// how many items for an action on several.
func callSentence(call, input, lang string) string {
	name, _, _ := strings.Cut(call, " ")
	tool := name
	if _, t, ok := splitToolName(name); ok {
		tool = t
	}
	var in map[string]any
	json.Unmarshal([]byte(input), &in)
	ids, isList := in["ids"].([]any)
	if !isList {
		return upperFirst(callVerb(call, lang))
	}
	n := max(len(ids), 1)
	mails := fmt.Sprintf(tr(lang, "card.call.mails"), n)
	if n == 1 {
		mails = tr(lang, "card.call.mail")
	}
	switch tool {
	case "move":
		to, _ := in["to"].(string)
		switch strings.ToLower(strings.TrimSpace(to)) {
		case "archive":
			return fmt.Sprintf(tr(lang, "card.call.archive"), mails)
		case "inbox":
			return fmt.Sprintf(tr(lang, "card.call.inbox"), mails)
		case "":
		default:
			return fmt.Sprintf(tr(lang, "card.call.move"), mails, truncate(oneLine(to), 60))
		}
	case "trash":
		if junk, _ := in["junk"].(bool); junk {
			return fmt.Sprintf(tr(lang, "card.call.junk"), mails)
		}
		return fmt.Sprintf(tr(lang, "card.call.trash"), mails)
	case "mark":
		var parts []string
		if read, ok := in["read"].(bool); ok {
			parts = append(parts, tr(lang, map[bool]string{true: "card.call.read", false: "card.call.unread"}[read]))
		}
		if flagged, ok := in["flagged"].(bool); ok {
			parts = append(parts, tr(lang, map[bool]string{true: "card.call.flagged", false: "card.call.unflagged"}[flagged]))
		}
		if len(parts) > 0 {
			return fmt.Sprintf(tr(lang, "card.call.mark"), mails, strings.Join(parts, tr(lang, "card.call.and")))
		}
	}
	return upperFirst(callVerb(call, lang))
}

// upperFirst is s with its first letter a capital.
func upperFirst(s string) string {
	for i, r := range s {
		return strings.ToUpper(string(r)) + s[i+len(string(r)):]
	}
	return s
}

// callTool is a call's tool as its card's Détails name it: server/tool.
func callTool(call string) string {
	name, _, _ := strings.Cut(call, " ")
	if server, tool, ok := splitToolName(name); ok {
		return server + "/" + tool
	}
	return name
}

// callDetail is a kept call's exact parameters, under the card's
// Détails.
func callDetail(a accessQuestion) cardField {
	var lines []string
	for _, f := range a.fields() {
		lines = append(lines, f.Name+" : "+f.Value)
	}
	return cardField{Name: callTool(a.Call), Value: truncate(strings.Join(lines, "\n"), cardValueMax), Detail: true}
}

// callsWords are a card's words for the calls it keeps: one sentence for
// one call, its items below; for several, text is said by the card, and
// each call is a line, with its items.
func (c *core) callsWords(asks []accessQuestion, lang string) (sentence string, lines []string) {
	if len(asks) == 1 {
		a := asks[0]
		for _, l := range c.labelsOf(a) {
			if s := labelLine(l, lang); s != "" {
				lines = append(lines, s)
			}
		}
		return callSentence(a.Call, a.Input, lang), lines
	}
	var said []string
	for _, a := range asks {
		s := callSentence(a.Call, a.Input, lang)
		said = append(said, s)
		var items []string
		for _, l := range c.labelsOf(a) {
			if x := labelLine(l, lang); x != "" {
				items = append(items, x)
			}
		}
		if len(items) > 0 {
			s = fmt.Sprintf(tr(lang, "card.call.with"), s, strings.Join(items, ", "))
		}
		lines = append(lines, s)
	}
	return strings.Join(said, " · "), lines
}
