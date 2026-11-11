package main

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"
)

// A tool's places, on the page of one of its accounts (Settings → Tools →
// <account> → Folders, places.go): one list, the folders given first, each
// with the summary of its cases (Lire, Modifier, Supprimer: its account's,
// or its own, set in its drawer, web_case_grid.go), then the account's own
// folders to give more, browsed through the tool.

// toolFolders is the page's list: the folders of an account, the given
// ones first.
type toolFolders struct {
	Tool, Base string
	Account    string
	Status     string // the tool's: ready, connecting…
	CanWrite   bool   // the account's credential writes: else its cases above Lire are greyed
	Parent     string // the folder browsed, "" for the top
	Name       string // … and its name
	Items      []folderItem
	Next       string
	Error      string
}

// folderItem is one line: a folder given, with its cases, or one to give.
type folderItem struct {
	placeItem
	Given bool // its Name is then the given folder's, as tasks name it
	Grid  caseGrid
}

// placeCount is "no folder", "1 folder", "3 folders".
func placeCount(lang string, n int) string {
	switch n {
	case 0:
		return tr(lang, "places.count0")
	case 1:
		return tr(lang, "places.count1")
	}
	return fmt.Sprintf(tr(lang, "places.count"), n)
}

func (w *web) placeRoutes(mux *http.ServeMux) {
	mux.Handle("GET /settings/tools/{name}/places", w.auth(w.handlePlaces))
	mux.Handle("POST /settings/tools/{name}/places", w.auth(w.handlePlaceGive))
	mux.Handle("GET /settings/tools/{name}/places/grid", w.auth(w.handlePlaceGrid))
	mux.Handle("POST /settings/tools/{name}/places/case", w.auth(w.handlePlaceCase))
	mux.Handle("POST /settings/tools/{name}/places/remove", w.auth(w.handlePlaceRemove))
	mux.Handle("GET /settings/connections/{id}/places", w.auth(w.handleConnPlaces))
	mux.Handle("GET /settings/connections/{id}/places/grid", w.auth(w.handleConnPlaceGrid))
	mux.Handle("POST /settings/connections/{id}/places/case", w.auth(w.handleConnPlaceCase))
}

// placesTool is the tool of the page, if it has places.
func (w *web) placesTool(rw http.ResponseWriter, r *http.Request) (string, bool) {
	name := r.PathValue("name")
	if _, _, ok := keeperOf(w.c.conf().Servers[name]); !ok {
		http.NotFound(rw, r)
		return "", false
	}
	return name, true
}

// toolFolders is the list of one account's folders: the given ones, then,
// browsed through the tool, the account's folders under parent (the given
// ones of them first).
func (w *web) toolFolders(ctx context.Context, tool, account, parent, name, page string) toolFolders {
	srv := w.c.conf().Servers[tool]
	v := toolFolders{Tool: tool, Base: "/settings/tools/" + tool + "/places", Parent: parent, Name: name}
	accounts := w.c.placeAccounts(tool)
	for _, a := range accounts {
		if strings.EqualFold(a.Name, account) || v.Account == "" {
			v.Account, v.CanWrite = a.Name, a.CanWrite
		}
	}
	given := map[string]folderItem{}
	var order []string
	for _, p := range placesOfAccount(tool, srv, v.Account, len(accounts)) {
		it := folderItem{placeItem: placeItem{ID: p.ID, Name: p.Name}, Given: true}
		it.Grid, _ = w.placeGrid(tool, p.ID)
		given[p.ID] = it
		order = append(order, p.ID)
	}
	all := func() []folderItem {
		var out []folderItem
		for _, id := range order {
			out = append(out, given[id])
		}
		return out
	}
	if parent == "" {
		v.Items = all()
	}
	if u := w.c.pool.get(tool); u != nil {
		v.Status, _, _ = u.snapshot()
	}
	if v.Status != statusReady || v.Account == "" {
		v.Items = all() // what is given stays in view, wherever the browsing was
		return v
	}
	items, next, err := w.c.browsePlaces(ctx, tool, v.Account, parent, page)
	if err != nil {
		v.Error, v.Items = err.Error(), all()
		return v
	}
	var mine, others []folderItem
	for _, it := range items {
		g, ok := given[it.ID]
		switch {
		case ok && parent != "":
			g.placeItem.Link = it.Link
			mine = append(mine, g)
		case !ok:
			others = append(others, folderItem{placeItem: it})
		}
	}
	v.Items, v.Next = append(append(v.Items, mine...), others...), next
	return v
}

// raw is a grid's own value of a category, "" when it says nothing.
func (g accessGrid) raw(category string) string {
	switch category {
	case catRead:
		return g.Read
	case catChange:
		return g.Change
	case catDelete:
		return g.Delete
	}
	return g.Send
}

func (w *web) handlePlaces(rw http.ResponseWriter, r *http.Request, s *session) {
	tool, ok := w.placesTool(rw, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	v := w.toolFolders(ctx, tool, q.Get("account"), q.Get("parent"), q.Get("name"), q.Get("page"))
	if v.Error != "" {
		v.Error = w.connError(r, fmt.Errorf("%s", v.Error))
	}
	if r.Header.Get("HX-Request") != "" && r.Header.Get("HX-Target") == "tool-folders" {
		w.render(rw, r, s, "settings", "tool-folders-frag", page{Data: v})
		return
	}
	w.render(rw, r, s, "settings", "", page{Title: "places.title", Nav: "settings", Tab: "tools", View: "tool-folders", Data: v})
}

// foldersAgain answers a change with the list again, where it was.
func (w *web) foldersAgain(rw http.ResponseWriter, r *http.Request, s *session, tool string, err error) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	v := w.toolFolders(ctx, tool, r.PostFormValue("account"), r.PostFormValue("parent"), r.PostFormValue("parent_name"), "")
	if v.Error != "" {
		v.Error = w.connError(r, fmt.Errorf("%s", v.Error))
	}
	if err != nil {
		v.Error = w.connError(r, err)
	} else {
		w.notify()
	}
	w.render(rw, r, s, "settings", "tool-folders-frag", page{Data: v})
}

func (w *web) handlePlaceGive(rw http.ResponseWriter, r *http.Request, s *session) {
	if tool, ok := w.placesTool(rw, r); ok {
		w.foldersAgain(rw, r, s, tool, w.c.givePlace(tool, r.PostFormValue("account"), r.PostFormValue("id"), r.PostFormValue("name")))
	}
}

// placeGrid is a given folder's grid: its account's cases, and its own.
func (w *web) placeGrid(tool, id string) (caseGrid, bool) {
	srv := w.c.conf().Servers[tool]
	k, _, _ := keeperOf(srv)
	i := slices.IndexFunc(placesIn(tool, srv), func(p Place) bool { return p.ID == id })
	if i < 0 {
		return caseGrid{}, false
	}
	p := placesIn(tool, srv)[i]
	write := false
	for _, a := range w.c.placeAccounts(tool) {
		if strings.EqualFold(a.Name, p.Account) {
			write = a.CanWrite
		}
	}
	g := caseGrid{Key: caseKey(p.ID), Title: p.Name, Sub: p.Account, Post: "/settings/tools/" + tool + "/places/case",
		Hidden: map[string]string{"id": p.ID}, Follow: "places.followcol", Hint: "places.cat.%s.hint", Greyed: "places.ceiling"}
	account := srv.placeAccess(Place{Account: p.Account}) // its account's, without its own cases
	own := srv.PlaceGrids[p.ID]
	for _, c := range k.Card.cases() {
		g.Rows = append(g.Rows, caseRow{Category: c, Own: own.raw(c), Follows: account.grid.value(c), Greyed: !write && c != catRead})
	}
	return g.greyed(), true
}

func (w *web) handlePlaceGrid(rw http.ResponseWriter, r *http.Request, s *session) {
	tool, ok := w.placesTool(rw, r)
	if !ok {
		return
	}
	g, ok := w.placeGrid(tool, r.URL.Query().Get("id"))
	if !ok {
		http.NotFound(rw, r)
		return
	}
	w.render(rw, r, s, "settings", "case-drawer", page{Data: g})
}

// handlePlaceCase sets the cases of a given folder its drawer changed, and
// answers with its grid and its summary in the list.
func (w *web) handlePlaceCase(rw http.ResponseWriter, r *http.Request, s *session) {
	tool, ok := w.placesTool(rw, r)
	if !ok {
		return
	}
	id := r.PostFormValue("id")
	srv := w.c.conf().Servers[tool]
	k, _, _ := keeperOf(srv)
	var err error
	for _, c := range postedCases(r, k.Card.cases(), srv.PlaceGrids[id].raw) {
		if err = w.c.setPlaceCase(tool, id, c[0], c[1]); err != nil {
			break
		}
	}
	g, ok := w.placeGrid(tool, id)
	if !ok {
		http.NotFound(rw, r)
		return
	}
	w.caseAnswer(rw, r, s, g, err)
}

// caseAnswer is a drawer's grid again after a change, with the place's
// summary in the list (out of band).
func (w *web) caseAnswer(rw http.ResponseWriter, r *http.Request, s *session, g caseGrid, err error) {
	if err != nil {
		g.Error = w.connError(r, err)
	} else {
		g.Saved = true
		w.notify()
	}
	w.render(rw, r, s, "settings", "case-grid-frag", page{Data: g})
}

func (w *web) handlePlaceRemove(rw http.ResponseWriter, r *http.Request, s *session) {
	if tool, ok := w.placesTool(rw, r); ok {
		w.foldersAgain(rw, r, s, tool, w.c.removePlace(tool, r.PostFormValue("id")))
	}
}

// The repositories of a connection whose provider mints tokens
// (connection_places.go): every one its account reaches, the ones with
// rules of their own first, each case following the connection's tool or
// set apart.

type repoList struct {
	Tool, Base string // the connection's label, and the page's address
	Ceiling    bool   // its tool only reads: no case goes above
	Items      []repoItem
	Next, Page string
	Error      string
}

type repoItem struct {
	Repo, Link string
	Own        bool
	Grid       caseGrid
}

// placesConn is the connection of the page, if it has places.
func (w *web) placesConn(rw http.ResponseWriter, r *http.Request) (connection, bool) {
	cn, ok := w.c.connectionByID(r.PathValue("id"))
	if !ok || w.c.minters[cn.Provider] == nil {
		http.NotFound(rw, r)
		return connection{}, false
	}
	return cn, true
}

func (w *web) repoList(ctx context.Context, cn connection, page string) repoList {
	v := repoList{Tool: providerLabel(cn.Provider) + " · " + cn.Identity, Base: "/settings/connections/" + cn.ID + "/places", Page: page}
	names := w.c.connServers(cn.ID)
	if len(names) == 0 {
		v.Error = "no tool on this connection"
		return v
	}
	srv := w.c.conf().Servers[names[0]]
	v.Ceiling = srv.baseAccess().level() == accessReadOnly
	item := func(repo, link string) repoItem {
		_, has := srv.PlaceGrids[strings.ToLower(repo)]
		g, _ := w.repoGrid(cn, repo)
		return repoItem{Repo: repo, Link: link, Own: has, Grid: g}
	}
	var own []string
	for id := range srv.PlaceGrids {
		own = append(own, id)
	}
	slices.Sort(own)
	if page == "" {
		for _, id := range own {
			v.Items = append(v.Items, item(id, ""))
		}
	}
	items, next, err := w.c.browseConnection(ctx, cn.ID, page)
	if err != nil {
		v.Error = err.Error()
		return v
	}
	for _, it := range items {
		if _, has := srv.PlaceGrids[strings.ToLower(it.ID)]; has {
			for i := range v.Items {
				if strings.EqualFold(v.Items[i].Repo, it.ID) {
					v.Items[i].Repo, v.Items[i].Link, v.Items[i].Grid.Title = it.ID, it.Link, it.ID
				}
			}
			continue
		}
		v.Items = append(v.Items, item(it.ID, it.Link))
	}
	v.Next = next
	return v
}

func (w *web) handleConnPlaces(rw http.ResponseWriter, r *http.Request, s *session) {
	cn, ok := w.placesConn(rw, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	v := w.repoList(ctx, cn, r.URL.Query().Get("page"))
	if v.Error != "" {
		v.Error = w.connError(r, fmt.Errorf("%s", v.Error))
	}
	if r.Header.Get("HX-Request") != "" && r.Header.Get("HX-Target") == "repo-list" {
		w.render(rw, r, s, "settings", "repo-list-frag", page{Data: v})
		return
	}
	w.render(rw, r, s, "settings", "", page{Title: "repos.title", Nav: "settings", Tab: "tools", View: "places", Data: v})
}

// repoGrid is a repository's grid: its tool's cases, and its own.
func (w *web) repoGrid(cn connection, repo string) (caseGrid, bool) {
	names := w.c.connServers(cn.ID)
	if len(names) == 0 || repoOf(repo) == "" {
		return caseGrid{}, false
	}
	srv := w.c.conf().Servers[names[0]]
	base := srv.baseAccess()
	ceiling := base.level() == accessReadOnly
	g := caseGrid{Key: caseKey(strings.ToLower(repo)), Title: repo, Sub: providerLabel(cn.Provider) + " · " + cn.Identity,
		Post: "/settings/connections/" + cn.ID + "/places/case", Hidden: map[string]string{"id": repo},
		Follow: "repos.followcol", Hint: "repos.cat.%s.hint", Greyed: "repos.ceiling"}
	own := srv.PlaceGrids[strings.ToLower(repo)]
	for _, cat := range categories {
		g.Rows = append(g.Rows, caseRow{Category: cat, Own: own.raw(cat), Follows: base.grid.value(cat), Greyed: ceiling && cat != catRead})
	}
	return g.greyed(), true
}

func (w *web) handleConnPlaceGrid(rw http.ResponseWriter, r *http.Request, s *session) {
	cn, ok := w.placesConn(rw, r)
	if !ok {
		return
	}
	g, ok := w.repoGrid(cn, r.URL.Query().Get("id"))
	if !ok {
		http.NotFound(rw, r)
		return
	}
	w.render(rw, r, s, "settings", "case-drawer", page{Data: g})
}

// handleConnPlaceCase sets the cases of a repository its drawer changed.
func (w *web) handleConnPlaceCase(rw http.ResponseWriter, r *http.Request, s *session) {
	cn, ok := w.placesConn(rw, r)
	if !ok {
		return
	}
	repo := r.PostFormValue("id")
	g, ok := w.repoGrid(cn, repo)
	if !ok {
		http.NotFound(rw, r)
		return
	}
	var err error
	for _, c := range postedCases(r, categories, func(cat string) string {
		return g.Rows[slices.Index(categories, cat)].Own
	}) {
		if err = w.c.setRepoCase(cn.ID, repo, c[0], c[1]); err != nil {
			break
		}
	}
	g, _ = w.repoGrid(cn, repo)
	w.caseAnswer(rw, r, s, g, err)
}
