package main

import (
	"net/http"
	"net/url"
	"slices"
	"strings"
)

// "Add a tool": the catalog of presets by category, with a search that
// filters the tiles in the browser (app.js; without it, all are shown),
// and "Autre service", which looks at an address before adding it
// (discover.go).

// presetTile is a preset in the gallery.
type presetTile struct {
	toolPreset
	Added  bool   // a tool of the workspace comes from it
	Search string // the words the search matches
}

type presetGroup struct {
	Cat   string
	Tiles []presetTile
}

type galleryPage struct {
	Groups []presetGroup
	Others []presetTile // a server by its address, a local program
	Count  int
}

// addedPresets are the presets the workspace's tools come from.
func (w *web) addedPresets(lang string) map[string]bool {
	added := map[string]bool{}
	for _, s := range w.c.conf().Servers {
		if p, _ := presetOf(lang, s); p.ID != "" {
			added[p.ID] = true
		}
	}
	return added
}

func (w *web) tile(lang string, p toolPreset, added map[string]bool) presetTile {
	words := []string{p.ID, tr(lang, "preset."+p.ID+".title"), tr(lang, "preset."+p.ID+".desc"), tr(lang, "cat."+p.Category)}
	return presetTile{toolPreset: p, Added: added[p.ID], Search: strings.ToLower(strings.Join(words, " "))}
}

func (w *web) gallery(lang string) galleryPage {
	added := w.addedPresets(lang)
	var g galleryPage
	for _, cat := range toolCategories {
		group := presetGroup{Cat: cat}
		for _, p := range toolPresets {
			if p.Category == cat {
				group.Tiles = append(group.Tiles, w.tile(lang, p, added))
			}
		}
		if len(group.Tiles) > 0 {
			g.Groups = append(g.Groups, group)
			g.Count += len(group.Tiles)
		}
	}
	for _, p := range toolPresets {
		if p.Category == "" {
			t := w.tile(lang, p, added)
			t.Search = "" // always shown: what the search did not find
			g.Others = append(g.Others, t)
		}
	}
	return g
}

// featuredPresets open the tools page's short list; the gallery has the rest.
var featuredPresets = []string{"mail", "calendar", "gdrive", "home-assistant", "notion", "todoist"}

func (w *web) featured(lang string) ([]presetTile, int) {
	added := w.addedPresets(lang)
	var out []presetTile
	for _, id := range featuredPresets {
		if p, ok := presetByID(id); ok {
			out = append(out, w.tile(lang, p, added))
		}
	}
	return out, len(slices.DeleteFunc(slices.Clone(toolPresets), func(p toolPreset) bool { return p.Category == "" }))
}

// handleGallery is "Add a tool": the tiles of the presets, by category.
func (w *web) handleGallery(rw http.ResponseWriter, r *http.Request, s *session) {
	w.render(rw, r, s, "settings", "", page{Title: "tools.add", Nav: "settings", Tab: "tools", View: "gallery", Data: w.gallery(w.lang(r))})
}

// discoverPage is "Autre service": the address, and what Eriphe found there.
type discoverPage struct {
	URL   string
	Found *discovery
	Name  string // a name for the tool, from the address
	Error string
}

// FormURL is the tool form, filled with what was found, for an auth.
func (d discoverPage) FormURL(auth string) string {
	q := url.Values{"preset": {"remote"}}
	for k, v := range map[string]string{"url": d.URL, "auth": auth, "name": d.Name} {
		if v != "" {
			q.Set(k, v)
		}
	}
	return "/settings/tools/new?" + q.Encode()
}

// handleDiscover shows "Autre service" (GET) and looks at the address
// posted (POST): the result alone for htmx, the whole page otherwise.
func (w *web) handleDiscover(rw http.ResponseWriter, r *http.Request, s *session) {
	v := discoverPage{URL: strings.TrimSpace(r.FormValue("url"))}
	block := ""
	if r.Method == http.MethodPost {
		if r.Header.Get("HX-Request") != "" {
			block = "discover-result"
		}
		d, err := discover(r.Context(), &http.Client{Transport: transportOf(w.c.pool.http)}, v.URL)
		if err != nil {
			v.Error = w.formError(r, err)
			if v.URL == "" {
				v.Error = w.formError(r, errNoURL)
			}
		} else {
			v.URL, v.Found, v.Name = d.URL, &d, guessName(d.Host())
		}
	}
	w.render(rw, r, s, "settings", block, page{Title: "discover.title", Nav: "settings", Tab: "tools", View: "discover", Data: v})
}
