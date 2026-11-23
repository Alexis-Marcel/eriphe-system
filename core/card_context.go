package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"maps"
	"slices"
	"strings"
	"time"
)

// A call often names what it acts on by a number: to decide, the user needs
// what is behind it. The card reads it, once, when it is asked, with a read
// the tool declares in its _meta (or its preset in its place,
// presets_meta.go), under readsMetaKey. Nothing here knows a server or a
// tool. A read that fails leaves the card with the call's parameters.
//
//	"eriphe/reads": {
//	  "tools": [{"tool": "get_thing", "args": {"method": "get"}}, …],
//	  "with":  ["owner", "repo", "number"],
//	  "show":  [{"field": "title", "name": {"fr": "Titre", "en": "Title"}},
//	            {"field": "body",  "name": {"fr": "Description", "en": "Description"}}]
//	}
//
//   - with are the call's arguments the read takes; a call that lacks one
//     is not read.
//   - tools are the reads, the first one the server has: called with the
//     with arguments, and its own args; only a free read runs (freeCall:
//     a read by its annotations, with an account, free under its policy,
//     never a write nor a Never). What it says goes to the card alone,
//     for the user's eyes, never to a model.
//   - show are the fields of what it said (a JSON object) the card shows,
//     in order, named in the user's language; the first must be there, or
//     nothing is shown.

// readsMetaKey is the _meta key under which a tool declares how a card
// reads what its call names.
const readsMetaKey = "eriphe/reads"

// cardReadTimeout keeps the read from delaying the card.
const cardReadTimeout = 5 * time.Second

// cardDescriptionMax is as much of a description as a card shows.
const cardDescriptionMax = 1000

// toolReads is a tool's declaration of how a card reads what its call names.
type toolReads struct {
	Tools []struct {
		Tool string         `json:"tool"`
		Args map[string]any `json:"args,omitempty"`
	} `json:"tools"`
	With []string `json:"with"`
	Show []struct {
		Field string            `json:"field"`
		Name  map[string]string `json:"name"`
	} `json:"show"`
}

// readsOf reads a tool's declaration from its _meta; nil when it has none
// or none usable.
func readsOf(meta map[string]any) *toolReads {
	var r toolReads
	if !metaValue(meta, readsMetaKey, &r) || len(r.Tools) == 0 || len(r.Show) == 0 {
		return nil
	}
	return &r
}

// readContext says what a call names, as its tool declares: input is the
// call's; has tells whether the server has a tool, and read runs one.
func (r *toolReads) readContext(input map[string]any, lang string, has func(string) bool, read func(tool string, args map[string]any) (string, error)) []cardField {
	args := map[string]any{}
	for _, k := range r.With {
		v, ok := input[k]
		if !ok || v == nil || v == "" {
			return nil
		}
		args[k] = v
	}
	for _, t := range r.Tools {
		if !has(t.Tool) {
			continue
		}
		a := maps.Clone(args)
		maps.Copy(a, t.Args)
		text, err := read(t.Tool, a)
		if err != nil {
			log.Printf("access: reading what a call names for its card: %v", err)
			return nil
		}
		var got map[string]any
		if json.Unmarshal([]byte(text), &got) != nil {
			return nil
		}
		var out []cardField
		for i, f := range r.Show {
			v, _ := got[f.Field].(string)
			if v = strings.TrimSpace(v); v == "" {
				if i == 0 {
					return nil
				}
				continue
			}
			n := cardDescriptionMax
			if i == 0 {
				n = 300
				v = oneLine(v)
			}
			out = append(out, cardField{Name: cmpOr(inLang(f.Name, lang), f.Field), Value: truncate(v, n)})
		}
		return out
	}
	return nil
}

// cardFields are what a card lists: what it read, then the call's parameters
// but a content its diff says.
func cardFields(context []cardField, input map[string]any) []cardField {
	out := context
	diff := slices.ContainsFunc(context, func(f cardField) bool { return f.Name == "diff" })
	for _, f := range callFields(input) {
		if diff && f.Name == "content" {
			continue
		}
		out = append(out, f)
	}
	return out
}

// callContext is what a card adds to a call's parameters.
func (c *core) callContext(ctx context.Context, tool string, input map[string]any) []cardField {
	server, name, ok := splitToolName(tool)
	if !ok {
		return nil
	}
	u := c.pool.get(server)
	if u == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, cardReadTimeout)
	defer cancel()
	if server == foldersServer {
		return c.folderCardContext(ctx, tool, input) // the file and its diff (folder_tools.go)
	}
	t := u.toolByClaudeName(name)
	if t == nil {
		return nil
	}
	r := readsOf(c.toolMeta(u, t))
	if r == nil {
		return nil
	}
	return r.readContext(input, c.userLang(), func(name string) bool { return u.toolByClaudeName(name) != nil },
		func(name string, args map[string]any) (string, error) {
			if !c.freeCall(claudeToolName(u.name, name), args) {
				return "", fmt.Errorf("%s is not a free read: not read for a card", name)
			}
			raw, err := json.Marshal(args)
			if err != nil {
				return "", err
			}
			res, err := u.call(ctx, name, raw)
			if err != nil {
				return "", err
			}
			if res.IsError {
				return "", fmt.Errorf("%s: %s", name, truncate(resultText(res), 200))
			}
			return resultText(res), nil
		})
}
