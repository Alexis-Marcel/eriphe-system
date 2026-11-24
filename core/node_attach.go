package main

import (
	"fmt"
	"log"
	"maps"
	"os"
	"slices"
	"strings"

	"github.com/Alexis-Marcel/eriphe-system/core/nodefile"
	"github.com/Alexis-Marcel/eriphe-system/core/nodestore"
)

// A file the user attached to a message can be kept with a node (#748):
// the photo of a shirt in the node of that shirt, a recipe's PDF in the
// recipe's. The brain gives node_set a property whose value is the
// attachment's id (photo: att_…): code copies the file into the node's
// media (one of its files, a commit in the graph's repository, as a
// task's image is: web_media.go) and the property says media:<name>. The
// page shows that property as the node's picture, and a Table of a
// node_find source shows it as a thumbnail of each node found
// (nodeFindMedia). The bytes never go through a model.
//
// What code keeps:
//   - only an attachment of one of the user's messages, found by its id,
//     never a path the brain names;
//   - an image (PNG, JPEG, GIF, WebP) or a PDF, sniffed from its bytes, of
//     nodestore.MaxFile at most, and nodestore.MaxFiles files a node; a
//     text is written in the node's body instead;
//   - all is checked before anything is kept: refused, nothing is;
//   - an attachment from a channel the user did not trust taints the
//     turn, as reading it does (readAttachment): the node is external.

// nodeAttach keeps the attachments props name (a value att_…, or a list
// of them) in node id's files, and gives props with media:<name> in their
// place, and what it kept, property by property. Props without an
// attachment are given as they are.
func (c *core) nodeAttach(id string, meta nodefile.Meta, props map[string]any) (map[string]any, map[string]any, error) {
	type kept struct {
		att  *Attachment
		data []byte
		ext  string
	}
	files := map[string]kept{}
	var order, keys []string
	look := func(k string, v any) error {
		s, ok := v.(string)
		if !ok || !attachmentID.MatchString(strings.TrimSpace(s)) {
			return nil
		}
		s = strings.TrimSpace(s)
		if !slices.Contains(keys, k) {
			keys = append(keys, k)
		}
		if _, ok := files[s]; ok {
			return nil
		}
		a, err := c.store.attachment(s)
		if err != nil {
			return fmt.Errorf("%s: no such attachment (its id is in a [pièce jointe …] line of the user's message)", s)
		}
		if a.Size > nodestore.MaxFile {
			return fmt.Errorf("%s (%q, %s): a node keeps a file of %s at most; ask the user for a smaller one", s, a.Name, humanSize(a.Size), humanSize(nodestore.MaxFile))
		}
		data, err := os.ReadFile(c.store.attachmentPath(a.ID))
		if err != nil {
			return fmt.Errorf("%s: the attachment's file is gone", s)
		}
		_, ext, ok := nodeFileType(data)
		if !ok {
			return fmt.Errorf("%s (%q, %s): a node keeps an image or a PDF only; write a text in its body", s, a.Name, a.Type)
		}
		files[s] = kept{att: a, data: data, ext: ext}
		order = append(order, s)
		return nil
	}
	for _, k := range slices.Sorted(maps.Keys(props)) {
		vals, isList := props[k].([]any)
		if !isList {
			vals = []any{props[k]}
		}
		for _, v := range vals {
			if err := look(k, v); err != nil {
				return nil, nil, err
			}
		}
	}
	if len(files) == 0 {
		return props, nil, nil
	}
	have, err := c.nodes.Files(id)
	if err != nil {
		return nil, nil, err
	}
	if len(have)+len(files) > nodestore.MaxFiles {
		return nil, nil, fmt.Errorf("a node keeps %d files at most; %s has %d", nodestore.MaxFiles, id, len(have))
	}
	// The properties checked as they will be written, before any file is
	// kept: a name refused keeps nothing.
	probe := meta
	probe.Extra = slices.Clone(meta.Extra)
	for _, k := range slices.Sorted(maps.Keys(props)) {
		if err := probe.SetProp(k, swapAttachments(props[k], func(string) string { return "media:med_00000000000000000000000000000000.png" })); err != nil {
			return nil, nil, err
		}
	}
	for _, s := range order {
		if c.fromUntrusted(files[s].att.Seq) {
			c.turn.taint(c.store.msgTaint(files[s].att.Seq)) // as reading it would
		}
	}
	names := map[string]string{}
	for _, s := range order {
		f := files[s]
		name, err := c.nodes.PutFile(id, byBrain, f.ext, f.data)
		if err != nil {
			return nil, nil, err
		}
		names[s] = mediaScheme + name
		log.Printf("node %s: file %s (%s) from attachment %s", id, name, humanSize(int64(len(f.data))), s)
	}
	out, media := maps.Clone(props), map[string]any{}
	for _, k := range keys {
		out[k] = swapAttachments(props[k], func(s string) string { return names[s] })
		media[k] = out[k]
	}
	return out, media, nil
}

// swapAttachments is a property's value with each attachment's id
// replaced by to's.
func swapAttachments(v any, to func(string) string) any {
	one := func(x any) any {
		if s, ok := x.(string); ok && attachmentID.MatchString(strings.TrimSpace(s)) {
			return to(strings.TrimSpace(s))
		}
		return x
	}
	if xs, ok := v.([]any); ok {
		out := make([]any, len(xs))
		for i, x := range xs {
			out[i] = one(x)
		}
		return out
	}
	return one(v)
}
