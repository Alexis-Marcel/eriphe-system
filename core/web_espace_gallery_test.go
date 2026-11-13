package main

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/Alexis-Marcel/eriphe-system/core/a2ui"
)

// The app's gallery (web/espace/src/a2ui/gallery.json), surfaces that use
// every component of the catalog, which its tests render: core accepts it,
// so the page is tested on what a model may write.
func TestEspaceGalleryIsValid(t *testing.T) {
	raw, err := os.ReadFile("web/espace/src/a2ui/gallery.json")
	if err != nil {
		t.Fatal(err)
	}
	var langs map[string]json.RawMessage
	if err := json.Unmarshal(raw, &langs); err != nil {
		t.Fatal(err)
	}
	var cat struct {
		Components map[string]json.RawMessage `json:"components"`
	}
	if err := json.Unmarshal(a2ui.CatalogJSON(), &cat); err != nil {
		t.Fatal(err)
	}
	for _, lang := range []string{"fr", "en"} {
		msgs, ok := langs[lang]
		if !ok {
			t.Fatalf("no %s gallery", lang)
		}
		for _, e := range a2ui.Validate(msgs) {
			t.Errorf("%s: %+v", lang, e)
		}
		// Its actions keep to the grammar an author writes (#425), its links
		// to the addresses a Link may open.
		var created []struct {
			CreateSurface a2ui.Surface `json:"createSurface"`
		}
		json.Unmarshal(msgs, &created)
		for _, s := range created {
			for _, e := range a2ui.CheckActions(s.CreateSurface.ID, s.CreateSurface.Components) {
				t.Errorf("%s: %+v", lang, e)
			}
			for _, e := range a2ui.CheckLinks(s.CreateSurface.ID, s.CreateSurface.Components) {
				t.Errorf("%s: %+v", lang, e)
			}
		}
		// Every component of the catalog is in it.
		var surfaces []struct {
			CreateSurface struct {
				Components []struct {
					Component string `json:"component"`
				} `json:"components"`
			} `json:"createSurface"`
		}
		if err := json.Unmarshal(msgs, &surfaces); err != nil {
			t.Fatal(err)
		}
		used := map[string]bool{}
		for _, s := range surfaces {
			for _, c := range s.CreateSurface.Components {
				used[c.Component] = true
			}
		}
		for name := range cat.Components {
			if !used[name] {
				t.Errorf("%s gallery lacks %s", lang, name)
			}
		}
	}
}
