package main

import (
	"net/http"
	"net/url"
	"strings"
)

// "Add a tool": a search over the catalog of presets, in a dialog of the
// tools page and on its own page for a browser without JavaScript. While
// nothing is searched it shows the popular ones, then every category; the
// search and the category chips filter the tiles in the browser (app.js;
// without it, all are shown). "Autre service" looks at an address before
// adding it (discover.go).

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
	Popular []presetTile // first, while nothing is searched
	Groups  []presetGroup
	Others  []presetTile // a local program, then a server by its address: always last
	Count   int
}

// popularPresets open the gallery, before its categories.
var popularPresets = []string{"mail", "calendar", "gdrive", "notion", "todoist", "home-assistant", "github", "linear"}

// presetKeywords are more words a preset is found by: brands, and what it
// does in both languages.
var presetKeywords = map[string]string{
	"mail":           "email e-mail courriel gmail outlook icloud proton imap smtp",
	"calendar":       "calendrier calendar rendez-vous google icloud caldav nextcloud",
	"gdrive":         "google docs sheets documents fichiers files",
	"home-assistant": "domotique smart home lumieres lights",
	"github":         "git code repos pull requests issues",
	"linear":         "tickets issues projets",
	"notion":         "notes wiki pages docs",
	"todoist":        "todo to-do taches tasks",
	"ticktick":       "todo to-do taches tasks",
	"remote":         "mcp url adresse address",
	"local":          "docker image programme program",
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
	words := []string{p.ID, tr(lang, "preset."+p.ID+".title"), tr(lang, "preset."+p.ID+".desc")}
	if p.Category != "" {
		words = append(words, tr(lang, "cat."+p.Category))
	}
	if k := presetKeywords[p.ID]; k != "" {
		words = append(words, k)
	}
	return presetTile{toolPreset: p, Added: added[p.ID], Search: strings.ToLower(strings.Join(words, " "))}
}

func (w *web) gallery(lang string) galleryPage {
	added := w.addedPresets(lang)
	var g galleryPage
	for _, id := range popularPresets {
		if p, ok := presetByID(id); ok {
			g.Popular = append(g.Popular, w.tile(lang, p, added))
		}
	}
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
	for _, id := range []string{"local", "remote"} {
		if p, ok := presetByID(id); ok {
			g.Others = append(g.Others, w.tile(lang, p, added))
		}
	}
	return g
}

// handleGallery is "Add a tool" on its own page: what the tools page's
// dialog holds, for a link from elsewhere and a browser without JavaScript.
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
	if r.Method == http.MethodGet && inDialog(r) {
		block = "discover-step" // a step of the tools page's dialog (web_tools_dialog.go)
	}
	w.render(rw, r, s, "settings", block, page{Title: "discover.title", Nav: "settings", Tab: "tools", View: "discover", Data: v})
}
