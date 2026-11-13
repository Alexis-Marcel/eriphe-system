package main

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"
)

// A Drive account's page (Settings → Tools → <account>, places.go): its
// tree. The root, "the whole Drive", is the account's grid, Never until
// set; a folder follows its parent (a top folder, the account), or has
// cases of its own, above or below, set in its drawer (web_case_grid.go).
// Folders open as an accordion, their children browsed through the tool
// when opened; a folder with cases of its own is kept in the tool's
// configuration, one without is not.

// placeTree is the tree of one account, on its access page.
type placeTree struct {
	Base, Account string
	ReadOnly      bool // its credential only reads: above Lire is greyed
	Root          caseGrid
}

// treeLevel is the folders under one folder of the tree ("" for the top).
type treeLevel struct {
	Base, Account string
	Parents       string // the ids from the top to this level's folder, comma separated
	PName         string // that folder's name
	Items         []treeItem
	Next, Error   string
	NotReady      bool
}

// treeItem is one folder of a level: its children's Parents, and its grid.
type treeItem struct {
	placeItem
	Path string
	Grid caseGrid
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

func (w *web) placeRoutes(mux *webMux) {
	mux.Handle("GET /settings/tools/{name}/tree", w.auth(w.handleTree))
	mux.Handle("GET /settings/tools/{name}/tree/grid", w.auth(w.handleTreeGrid))
	mux.Handle("POST /settings/tools/{name}/tree/case", w.auth(w.handleTreeCase))
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

// treeBase is the address of a tool's tree.
func treeBase(tool string) string { return "/settings/tools/" + tool + "/tree" }

// splitParents reads a Parents list.
func splitParents(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, ",")
}

// placeTreeOf is an account's tree, for its page; ok is false for a tool
// without places.
func (w *web) placeTreeOf(lang, tool, account string) (placeTree, bool) {
	if _, _, ok := keeperOf(w.c.conf().Servers[tool]); !ok || account == "" {
		return placeTree{}, false
	}
	return placeTree{Base: treeBase(tool), Account: account, ReadOnly: !w.canWrite(tool, account), Root: w.treeGrid(lang, tool, account, placeRef{}, "")}, true
}

// canWrite tells whether an account's credential writes.
func (w *web) canWrite(tool, account string) bool {
	for _, a := range w.c.placeAccounts(tool) {
		if strings.EqualFold(a.Name, account) {
			return a.CanWrite
		}
	}
	return false
}

// treeGrid is the grid of the root (ref.ID "") or of a folder: its own
// cases, and what it follows, its parent's (pname) or the account's.
func (w *web) treeGrid(lang, tool, account string, ref placeRef, pname string) caseGrid {
	srv := w.c.conf().Servers[tool]
	k, _, _ := keeperOf(srv)
	write := w.canWrite(tool, account)
	g := caseGrid{Key: caseKey(account + "/" + ref.ID), Title: ref.Name, Sub: account, Post: treeBase(tool) + "/case",
		Hidden: map[string]string{"account": account, "id": ref.ID, "name": ref.Name, "parents": strings.Join(ref.Parents, ","), "pname": pname},
		Hint:   "places.cat.%s.hint", Greyed: "places.ceiling"}
	if ref.ID == "" { // the root: the account's grid, nothing above it
		g.Title = tr(lang, "tree.root")
		root := srv.rootAccess(account)
		for _, c := range k.Card.cases() {
			v := root.grid.value(c)
			g.Rows = append(g.Rows, caseRow{Category: c, Own: v, Follows: v, Greyed: !write && c != catRead})
		}
		return g.greyed()
	}
	g.Follow = "places.followcol"
	if len(ref.Parents) > 0 {
		g.FollowName = fmt.Sprintf(tr(lang, "tree.followas"), pname)
	}
	for _, p := range placesIn(tool, srv) {
		if p.ID == ref.ID && !p.Root {
			g.Title = p.Name
		}
	}
	above := srv.placeAccess(Place{Account: account, Parents: ref.Parents})
	own := srv.PlaceGrids[ref.ID]
	for _, c := range k.Card.cases() {
		g.Rows = append(g.Rows, caseRow{Category: c, Own: own.raw(c), Follows: above.grid.value(c), Greyed: !write && c != catRead})
	}
	return g.greyed()
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

// treeLevelOf is the folders under parents' last, browsed through the tool;
// while it starts, the top shows the folders kept, with cases of their own.
func (w *web) treeLevelOf(ctx context.Context, lang, tool, account, parents, pname, page string) treeLevel {
	v := treeLevel{Base: treeBase(tool), Account: account, Parents: parents, PName: pname}
	chain := splitParents(parents)
	item := func(it placeItem) treeItem {
		path := append(slices.Clone(chain), it.ID)
		return treeItem{placeItem: it, Path: strings.Join(path, ","),
			Grid: w.treeGrid(lang, tool, account, placeRef{Account: account, ID: it.ID, Name: it.Name, Parents: chain}, pname)}
	}
	status := ""
	if u := w.c.pool.get(tool); u != nil {
		status, _, _ = u.snapshot()
	}
	if status != statusReady {
		v.NotReady = true
		if len(chain) == 0 {
			srv := w.c.conf().Servers[tool]
			for _, p := range placesOfAccount(tool, srv, account, len(w.c.placeAccounts(tool))) {
				if !p.Root && len(p.Parents) == 0 {
					v.Items = append(v.Items, item(placeItem{ID: p.ID, Name: p.Name}))
				}
			}
		}
		return v
	}
	parent := ""
	if len(chain) > 0 {
		parent = chain[len(chain)-1]
	}
	items, next, err := w.c.browsePlaces(ctx, tool, account, parent, page)
	if err != nil {
		v.Error = err.Error()
		return v
	}
	for _, it := range items {
		if it.ID != "shared-with-me" || len(chain) == 0 {
			v.Items = append(v.Items, item(it))
		}
	}
	v.Next = next
	return v
}

// handleTree answers an opened folder with its children (an accordion's
// level), or a level's next page.
func (w *web) handleTree(rw http.ResponseWriter, r *http.Request, s *session) {
	tool, ok := w.placesTool(rw, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	v := w.treeLevelOf(ctx, w.lang(r), tool, q.Get("account"), q.Get("parents"), q.Get("pname"), q.Get("page"))
	w.render(rw, r, s, "settings", "tree-level-frag", page{Data: v})
}

// treeRef is the folder a request names; ID "" is the root.
func treeRef(get func(string) string) (placeRef, string) {
	return placeRef{Account: get("account"), ID: get("id"), Name: get("name"), Parents: splitParents(get("parents"))}, get("pname")
}

func (w *web) handleTreeGrid(rw http.ResponseWriter, r *http.Request, s *session) {
	tool, ok := w.placesTool(rw, r)
	if !ok {
		return
	}
	ref, pname := treeRef(r.URL.Query().Get)
	w.render(rw, r, s, "settings", "case-drawer", page{Data: w.treeGrid(w.lang(r), tool, ref.Account, ref, pname)})
}

// handleTreeCase sets the cases its drawer changed: the root's go to the
// account's grid (its exceptions kept), a folder's to its own.
func (w *web) handleTreeCase(rw http.ResponseWriter, r *http.Request, s *session) {
	tool, ok := w.placesTool(rw, r)
	if !ok {
		return
	}
	ref, pname := treeRef(r.PostFormValue)
	srv := w.c.conf().Servers[tool]
	k, _, _ := keeperOf(srv)
	var err error
	if ref.ID == "" {
		root := srv.rootAccess(ref.Account)
		g := root.grid
		changed := false
		for _, c := range postedCases(r, k.Card.cases(), g.value) {
			if c[1] != "" {
				g, changed = g.with(c[0], c[1]), true
			}
		}
		if changed {
			err = w.c.setAccountGrid(tool, ref.Account, accountGrid{Grid: g, Exceptions: w.accountExceptions(srv, ref.Account)})
		}
	} else {
		for _, c := range postedCases(r, k.Card.cases(), srv.PlaceGrids[ref.ID].raw) {
			if err = w.c.setPlaceCase(tool, ref, c[0], c[1]); err != nil {
				break
			}
		}
	}
	w.caseAnswer(rw, r, s, w.treeGrid(w.lang(r), tool, ref.Account, ref, pname), err)
}

// accountExceptions are an account's own exceptions.
func (w *web) accountExceptions(srv Server, account string) map[string]string {
	for name, g := range srv.AccountGrids {
		if strings.EqualFold(name, account) {
			return g.Exceptions
		}
	}
	return nil
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
