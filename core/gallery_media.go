package main

import (
	"slices"

	"github.com/Alexis-Marcel/eriphe-system/core/a2ui"
	"github.com/Alexis-Marcel/eriphe-system/core/nodefile"
)

// galleryMedia serves a Gallery's images as an Image's: each item's image
// field (a2ui.ImageFields) becomes eriphe-core's own address
// (mediaOrDrop), for the items written in the block (allowed when its
// author may name an address: authorOK) and for those of the data it
// binds (a source's: core fetches and checks them); anything else is
// dropped, and the page loads no other address (viaCore). comps are the
// block's components as the page gets them, data its data: both changed
// in place.
func (c *core) galleryMedia(id string, b *nodefile.Block, comps []map[string]any, data map[string]any, authorOK bool) {
	for _, comp := range comps {
		if comp["component"] != "Gallery" {
			continue
		}
		switch items := comp["items"].(type) {
		case []any:
			for _, it := range items {
				o, _ := it.(map[string]any)
				for _, k := range a2ui.ImageFields {
					if s, ok := o[k].(string); ok {
						o[k] = c.mediaOrDrop(id, s, authorOK)
					}
				}
			}
		case map[string]any:
			path, _ := items["@path"].(string)
			at := tokens(path)
			if len(at) == 0 {
				continue
			}
			_, fromSource := b.Sources[at[0]]
			ok := fromSource || authorOK
			for _, k := range a2ui.ImageFields {
				rewriteAt(data, append(slices.Clone(at), "*", k), func(u string) string { return c.mediaOrDrop(id, u, ok) })
			}
		}
	}
}
