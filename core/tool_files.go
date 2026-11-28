package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Alexis-Marcel/eriphe-system/core/nodestore"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// A tool that takes one of a node's files (#820): to print a node's PDF,
// to put its photo somewhere. The tool declares which of its arguments
// take a file, in its _meta (or its preset in its place, presets_meta.go):
//
//	"eriphe/files": ["document"]
//
// The caller (the brain, a task) gives such an argument as a node's file,
// media:<node id>/<name>, as the node shows it (media:<name>) with the
// node's id before the name. The permission card, the ticket and the
// decision log keep that short reference: proxyCall swaps it for the
// file's bytes only once the call is allowed, right before it goes
// upstream, as a data URI (data:application/pdf;base64,…). Nothing here
// knows a tool: it reads what the tool declares.
//
// What goes is a node's file only, read with the media's own rules
// (nodeMedia): an image or a PDF, sniffed from its bytes. A caller cannot
// hand bytes of its own (a data: value is refused), nor a path; an
// argument the tool did not declare is never read.

// filesMetaKey is the _meta key under which a tool names the arguments
// that take a node's file.
const filesMetaKey = "eriphe/files"

// fileArgs are the arguments of tool t (its _meta, its preset's) that
// take a node's file; none for a tool that declares none.
func (c *core) fileArgs(u *upstream, t *mcp.Tool) []string {
	if t == nil {
		return nil
	}
	var names []string
	if !metaValue(c.toolMeta(u, t), filesMetaKey, &names) {
		return nil
	}
	return names
}

// withNodeFiles is args with each declared file argument's reference
// swapped for the file's bytes; an error, in words for the caller, when
// one is not a node's file. args unchanged when the tool declares none.
func (c *core) withNodeFiles(names []string, args json.RawMessage) (json.RawMessage, error) {
	if len(names) == 0 || len(args) == 0 {
		return args, nil
	}
	var in map[string]any
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("arguments must be a JSON object")
	}
	changed := false
	for _, name := range names {
		v, ok := in[name]
		if !ok || v == nil {
			continue
		}
		ref, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("%s takes one of a node's files: media:<node id>/<name>", name)
		}
		uri, err := c.nodeFileURI(ref)
		if err != nil {
			return nil, fmt.Errorf("%s: %v", name, err)
		}
		in[name] = uri
		changed = true
	}
	if !changed {
		return args, nil
	}
	return json.Marshal(in)
}

// nodeFileURI is the data URI of the node's file a reference names,
// media:<node id>/<name>.
func (c *core) nodeFileURI(ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	at, ok := strings.CutPrefix(ref, mediaScheme)
	id, name, slash := strings.Cut(at, "/")
	if !ok || !slash || id == "" || strings.Contains(name, "/") || !nodestore.ValidFileName(name) {
		return "", fmt.Errorf("give one of a node's files as media:<node id>/<name> (the node's media:<name> with its id), not %q", shortRef(ref))
	}
	body, kind, err := c.nodeMedia(at)
	if err != nil {
		return "", fmt.Errorf("node %s has no file %s", id, name)
	}
	return "data:" + kind + ";base64," + base64.StdEncoding.EncodeToString(body), nil
}

// shortRef is a value cut for an error's words: a caller's long data: value
// is not echoed whole.
func shortRef(s string) string {
	if len(s) > 60 {
		return s[:60] + "…"
	}
	return s
}

// fileArgsNote is what a declared argument's description says to the
// caller, in the tool's own schema.
const fileArgsNote = "One of a node's files: media:<node id>/<name> (the node's media:<name> with its id); Eriphe sends the file itself."

// withFileArgsNote is a tool's input schema with each declared file
// argument's description saying how to give a node's file; schema as it is
// when the tool declares none or it does not read.
func withFileArgsNote(schema any, names []string) any {
	if len(names) == 0 {
		return schema
	}
	raw, err := json.Marshal(schema)
	if err != nil {
		return schema
	}
	var s map[string]any
	if json.Unmarshal(raw, &s) != nil {
		return schema
	}
	props, _ := s["properties"].(map[string]any)
	for _, name := range names {
		p, ok := props[name].(map[string]any)
		if !ok {
			continue
		}
		d, _ := p["description"].(string)
		if d = strings.TrimRight(strings.TrimSpace(d), "."); d != "" {
			d += ". "
		}
		p["description"] = d + fileArgsNote
	}
	return s
}
