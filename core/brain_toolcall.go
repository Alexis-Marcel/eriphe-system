package main

import (
	"encoding/json"
	"log"
	"regexp"
	"strings"
)

// A small model behind an OpenAI-compatible server sometimes writes the
// tool call it means to make as its answer, instead of making it:
//
//	eriphe-core_answer_task {"task_id":"…","allow":true}
//
// Such a reply is never shown to the user: it is logged, and the brain is
// asked once to call the tool instead (brainToolCallText).

// toolCallLineRe is a tool's name followed by its arguments as a JSON
// object: "<server>_<tool> {…}", "mcp__server__tool({…})". The name holds
// an underscore, so a word followed by braces in a sentence is not one.
var toolCallLineRe = regexp.MustCompile(`^(?:mcp__)?[A-Za-z][A-Za-z0-9.-]*_[A-Za-z0-9_]*[A-Za-z0-9]\s*\(?\s*(\{[\s\S]*\})\s*\)?;?$`)

// toolNameRe is what names a tool in a JSON call: one word, no spaces.
var toolNameRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.-]*$`)

// toolCallText tells whether a reply is only one or more tool calls written
// as text: lines like "<server>_<tool> {json}", or a JSON object naming a
// tool and its arguments ({"name": "…", "arguments": {…}}), bare, in a
// <tool_call> tag or alone in a code block.
func toolCallText(text string) bool {
	s := strings.TrimSpace(text)
	if s == "" {
		return false
	}
	s = unwrapToolCall(s)
	if toolCallLine(s) || toolCallJSON(s) {
		return true
	}
	// Several calls, one per line.
	lines := 0
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l == "" {
			continue
		}
		if !toolCallLine(l) && !toolCallJSON(l) {
			return false
		}
		lines++
	}
	return lines > 1
}

// unwrapToolCall takes off what a model wraps its written call in: a
// <tool_call> tag or a single code block.
func unwrapToolCall(s string) string {
	for _, tag := range []string{"tool_call", "function_call"} {
		if strings.HasPrefix(s, "<"+tag+">") && strings.HasSuffix(s, "</"+tag+">") {
			s = strings.TrimSpace(s[len(tag)+2 : len(s)-len(tag)-3])
		}
	}
	if strings.HasPrefix(s, "```") && strings.HasSuffix(s, "```") && strings.Count(s, "```") == 2 {
		inner := strings.TrimSuffix(strings.TrimPrefix(s, "```"), "```")
		if nl := strings.IndexByte(inner, '\n'); nl >= 0 && !strings.ContainsAny(strings.TrimSpace(inner[:nl]), " {") {
			inner = inner[nl+1:] // its language: ```json
		}
		s = strings.TrimSpace(inner)
	}
	return s
}

func toolCallLine(s string) bool {
	m := toolCallLineRe.FindStringSubmatch(s)
	if m == nil {
		return false
	}
	var args map[string]any
	return json.Unmarshal([]byte(m[1]), &args) == nil
}

// toolCallJSON tells whether s is a JSON object that names a tool and
// gives its arguments, and holds nothing else.
func toolCallJSON(s string) bool {
	var obj map[string]json.RawMessage
	if !strings.HasPrefix(s, "{") || json.Unmarshal([]byte(s), &obj) != nil {
		return false
	}
	if fn, ok := obj["function"]; ok { // {"type":"function","function":{…}}
		for k := range obj {
			if k != "function" && k != "type" && k != "id" {
				return false
			}
		}
		return toolCallJSON(string(fn))
	}
	name := ""
	args := false
	for k, v := range obj {
		switch k {
		case "name", "tool", "tool_name":
			if json.Unmarshal(v, &name) != nil || !toolNameRe.MatchString(name) {
				return false
			}
		case "arguments", "parameters", "input", "args":
			var m map[string]any
			var str string
			if json.Unmarshal(v, &m) == nil {
				args = true
			} else if json.Unmarshal(v, &str) == nil && json.Unmarshal([]byte(str), &m) == nil {
				args = true // OpenAI's arguments, a JSON string
			} else {
				return false
			}
		case "type", "id":
		default:
			return false
		}
	}
	return name != "" && args
}

// toolCallStart tells whether a draft starts like a tool call written as
// text, so that it is not shown while it is written.
func toolCallStart(draft string) bool {
	s := strings.TrimSpace(draft)
	for _, p := range []string{"<tool_call>", "<function_call>"} {
		if strings.HasPrefix(s, p) || (len(s) < len(p) && strings.HasPrefix(p, s) && s != "") {
			return true
		}
	}
	first, _, _ := strings.Cut(s, "\n")
	if i := strings.IndexByte(first, '{'); i > 0 {
		name := strings.TrimRight(strings.TrimSpace(first[:i]), "(")
		return strings.Contains(name, "_") && !strings.ContainsAny(name, " \t") && toolNameRe.MatchString(strings.TrimPrefix(name, "mcp__"))
	}
	return strings.HasPrefix(s, `{"name"`) || strings.HasPrefix(s, `{"tool`) || strings.HasPrefix(s, `{"type":"function"`)
}

// toolCallReask is the note that asks the brain, once, to make the call
// it wrote.
const toolCallReask = "Ta dernière réponse était un appel d'outil écrit en texte : je ne l'ai pas montré et rien n'a été fait. Appelle l'outil (ne l'écris pas), puis réponds normalement."

// brainToolCallText handles a reply of the brain that is only a written
// tool call: it is logged, not shown, and the brain is asked once to make
// the call. Once: not when the last message of the conversation is
// already that note, so a model that keeps writing calls is not asked
// again until something new happens.
func (c *core) brainToolCallText(text string) {
	log.Printf("brain: a tool call written as text was not shown: %s", truncate(strings.TrimSpace(text), 300))
	if seq := c.store.lastMessageSeq(); seq > 0 {
		if msgs, err := c.store.messagesAfter(seq-1, 1); err == nil && len(msgs) == 1 &&
			msgs[0].Role == roleEvent && msgs[0].Text == toolCallReask {
			return
		}
	}
	if _, err := c.store.addMessage(roleEvent, toolCallReask, false); err == nil {
		c.ping()
	}
}
