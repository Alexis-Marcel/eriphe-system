package main

import (
	"strings"
	"testing"

	"github.com/Alexis-Marcel/eriphe-system/core/nodefile"
)

// A Gallery's images reach the page only as eriphe-core's own addresses:
// those its author wrote when it may name one (a block of the user's, not
// one written after an outside read), the node's own files always, those
// a source gave (core fetches and checks them); any other is dropped.
func TestGalleryMedia(t *testing.T) {
	c := &core{}
	b := &nodefile.Block{Name: "pieces", Sources: map[string]nodefile.Source{"shop": {Tool: "shop/list", Items: &nodefile.Items{Path: "items", ID: "id"}}}}
	viaCore := func(s any) bool { u, _ := s.(string); return strings.HasPrefix(u, mediaPath+"?") }
	for _, authorOK := range []bool{true, false} {
		items := []any{
			map[string]any{"title": "Veste", "photo": "https://example.org/veste.jpg"},
			map[string]any{"title": "Chemise", "image": "media:med_0123456789abcdef0123456789abcdef.png"},
			map[string]any{"title": "Piège", "photo": "javascript:alert(1)"},
		}
		comps := []map[string]any{
			{"id": "a", "component": "Gallery", "items": items},
			{"id": "b", "component": "Gallery", "items": map[string]any{"@path": "/shop/items"}},
			{"id": "c", "component": "Gallery", "items": map[string]any{"@path": "/mine"}},
		}
		data := map[string]any{
			"shop": map[string]any{"items": []any{map[string]any{"id": "1", "photo": "https://example.org/1.jpg"}, map[string]any{"id": "2", "photo": "file:///etc/passwd"}}},
			"mine": []any{map[string]any{"photo": "https://example.org/mine.jpg"}},
		}
		c.galleryMedia("n1", b, comps, data, authorOK)
		photo := func(v any, k string) any { return v.(map[string]any)[k] }
		if got := photo(items[0], "photo"); viaCore(got) != authorOK {
			t.Errorf("authorOK %v: an address its author wrote: %v", authorOK, got)
		}
		if got := photo(items[1], "image"); !viaCore(got) || !strings.Contains(got.(string), "med_0123456789abcdef0123456789abcdef.png") {
			t.Errorf("authorOK %v: the node's own file: %v", authorOK, got)
		}
		if got := photo(items[2], "photo"); got != "" {
			t.Errorf("authorOK %v: not an address: %v", authorOK, got)
		}
		shop := data["shop"].(map[string]any)["items"].([]any)
		if got := photo(shop[0], "photo"); !viaCore(got) {
			t.Errorf("authorOK %v: a source's image: %v", authorOK, got)
		}
		if got := photo(shop[1], "photo"); got != "" {
			t.Errorf("authorOK %v: a source's file address: %v", authorOK, got)
		}
		if got := photo(data["mine"].([]any)[0], "photo"); viaCore(got) != authorOK {
			t.Errorf("authorOK %v: the block's own data: %v", authorOK, got)
		}
	}
}
