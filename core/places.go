package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"maps"
	"net/http"
	"slices"
	"sort"
	"strings"
)

// Places: what a tool reaches inside it and the user picked, each with its
// own access: the folders of a files tool. A tool that has places browses them with
// a setup tool (eriphe/setup, which tasks never get: setupTool) and keeps
// the ones picked in its own configuration, where its server enforces
// them; where is data (its card's Places, tool_cards.go), so a tool is
// found by having places, never by its name.
//
// A place is lent to a task as {kind: place, ref: <its name>}; lending a
// place lends its whole tool, which reaches every place given to Eriphe,
// each with its own access.

const resPlace = "place"

// placeKeeper says, on a tool's card, where its server keeps its places:
// the key of the list in its configuration (TOOL_CONFIG), its setup tool
// that browses them, and the argument its tools name a place in.
type placeKeeper struct {
	Key    string
	Browse string
	Arg    string
}

// keeping is a server that has places: its card and its spec.
type keeping struct {
	*placeKeeper
	Card toolCard
}

// Place is a place of a tool, as its tool keeps it: a folder with cases of
// its own, or a whole account (Root) when its grid reaches it. What a task
// may do there is the account's grid (the root of its tree, Never until
// set) with each folder's own cases over it, from the top down to the
// place (Server.PlaceGrids, access_grid.go), which eriphe-core holds call
// by call; for a connection's place (a repository, connection_places.go),
// its Access.
type Place struct {
	Tool    string   `json:"-"`                 // the catalog server that has it
	Name    string   `json:"name"`              // how tools name it
	Account string   `json:"account"`           // the tool's account it is in
	ID      string   `json:"id"`                // its id at the tool
	Parents []string `json:"parents,omitempty"` // its folders above, from the top of the account
	Access  string   `json:"access,omitempty"`  // a connection's place: read or write
	Root    bool     `json:"-"`                 // the whole account (its server's "all")
}

// keeperOf finds where a server keeps its places, if it has any: its
// card's.
func keeperOf(srv Server) (keeping, RunSpec, bool) {
	if !srv.local() {
		return keeping{}, RunSpec{}, false
	}
	spec, err := decodeRun(srv.Run)
	if err != nil {
		return keeping{}, RunSpec{}, false
	}
	card, ok := serviceOf(spec)
	if !ok || card.Places == nil {
		return keeping{}, RunSpec{}, false
	}
	return keeping{card.Places, card}, spec, true
}

// placesIn reads a server's places.
func placesIn(name string, srv Server) []Place {
	k, spec, ok := keeperOf(srv)
	if !ok {
		return nil
	}
	var doc map[string]json.RawMessage
	if json.Unmarshal([]byte(spec.Env[toolConfigEnv].Value), &doc) != nil {
		return nil
	}
	var list []Place
	json.Unmarshal(doc[k.Key], &list)
	for i := range list {
		list[i].Tool = name
		list[i].Access = ""
	}
	var accounts []struct {
		Name string `json:"name"`
		All  bool   `json:"all"`
	}
	json.Unmarshal(doc["accounts"], &accounts)
	for _, a := range accounts {
		if a.All {
			list = append(list, Place{Tool: name, Name: a.Name, Account: a.Name, Root: true})
		}
	}
	return list
}

// placeTools are the catalog's servers that have places, by name.
func (c *core) placeTools() []string {
	var out []string
	for name, srv := range c.conf().Servers {
		if _, _, ok := keeperOf(srv); ok {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// places are all the places given to Eriphe.
func (c *core) places() []Place {
	var out []Place
	for _, name := range c.placeTools() {
		out = append(out, placesIn(name, c.conf().Servers[name])...)
	}
	return out
}

// placeMost is the most a place may be lent: write when one of its cases
// above Lire is not never, else read.
func (c *core) placeMost(p Place) string {
	srv := c.conf().Servers[p.Tool]
	k, _, _ := keeperOf(srv)
	a := srv.placeAccess(p)
	for _, cat := range k.Card.cases() {
		if cat != catRead && a.grid.value(cat) != gridNever {
			return accessWrite
		}
	}
	for _, v := range a.exceptions {
		if v != gridNever && !a.ceiling {
			return accessWrite
		}
	}
	return accessRead
}

// placeRights are a place's cases, as the brain is told them.
func (c *core) placeRights(p Place) map[string]string {
	srv := c.conf().Servers[p.Tool]
	k, _, _ := keeperOf(srv)
	a := srv.placeAccess(p)
	out := map[string]string{}
	for _, cat := range k.Card.cases() {
		out[cat] = a.grid.value(cat)
	}
	return out
}

// placeNamed is the place of that name, if any.
func (c *core) placeNamed(name string) *Place {
	name = strings.TrimSpace(name)
	all := c.places()
	i := slices.IndexFunc(all, func(p Place) bool { return strings.EqualFold(p.Name, name) })
	if i < 0 {
		return nil
	}
	return &all[i]
}

// placeAccount is an account of a tool with places, and whether its
// credential can write (its connection granted the tool's scopes that
// write): without, its places only read (readCeilings).
type placeAccount struct {
	Name     string
	CanWrite bool
}

// placeAccounts are the accounts a tool's places are taken from: its
// connections.
func (c *core) placeAccounts(tool string) []placeAccount {
	srv := c.conf().Servers[tool]
	k, _, ok := keeperOf(srv)
	if !ok {
		return nil
	}
	var out []placeAccount
	for _, cn := range c.connectionsOf(srv) {
		t, _ := signInOf(k.Card.ID, cn.Provider)
		_, write := t.granted(cn.Scopes)
		out = append(out, placeAccount{Name: cn.Identity, CanWrite: write})
	}
	return out
}

// changePlaces rewrites a tool's places and their own cases (grids), which
// restarts its server.
func (c *core) changePlaces(tool string, change func([]Place) ([]Place, error), grids map[string]accessGrid) error {
	srv, ok := c.conf().Servers[tool]
	k, spec, has := keeperOf(srv)
	if !ok || !has {
		return errNoFolder
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal([]byte(spec.Env[toolConfigEnv].Value), &doc); err != nil {
		return err
	}
	list := placesIn(tool, srv)
	list, err := change(list)
	if err != nil {
		return err
	}
	list = slices.DeleteFunc(list, func(p Place) bool { return p.Root }) // its account's "all"
	for i := range list {
		list[i].Tool, list[i].Access = "", ""
	}
	doc[k.Key], _ = json.Marshal(list)
	raw, _ := json.Marshal(doc)
	env := maps.Clone(spec.Env)
	env[toolConfigEnv] = EnvValue{Value: string(raw)}
	spec.Env = env
	srv.Run = marshalRun(spec)
	srv.PlaceGrids = keepPlaceGrids(grids, list)
	return c.putServer(putUpdate, tool, srv, nil)
}

// keepPlaceGrids are the exceptions of the places still given.
func keepPlaceGrids(grids map[string]accessGrid, list []Place) map[string]accessGrid {
	out := maps.Clone(grids)
	maps.DeleteFunc(out, func(id string, g accessGrid) bool {
		return g == (accessGrid{}) || !slices.ContainsFunc(list, func(p Place) bool { return p.ID == id })
	})
	if len(out) == 0 {
		return nil
	}
	return out
}

// placeRef is a folder of a tool's account, as the tree names it: its id,
// its name, and its folders above from the top of the account.
type placeRef struct {
	Account, ID, Name string
	Parents           []string
}

// setPlaceCase gives one case of a folder its own value, above or below
// what it follows (its parent's, up to its account's); "" has it follow
// again. A folder with a case of its own is kept as a place of its tool
// (its server then knows it, the deepest place deciding); one whose cases
// all follow is not. A case its tool has not (its card's Cases) is
// refused.
func (c *core) setPlaceCase(tool string, ref placeRef, category, value string) error {
	srv, ok := c.conf().Servers[tool]
	k, _, has := keeperOf(srv)
	if !ok || !has || c.source(kindServer, tool) != sourceWorkspace {
		return errReadOnly(kindServer, tool)
	}
	if !slices.Contains(k.Card.cases(), category) || value != "" && !slices.Contains(gridValues, value) {
		return errInvalid(errors.New("a case is one of the tool's, its value free, approval, never or empty"))
	}
	ref.ID, ref.Name = strings.TrimSpace(ref.ID), strings.Join(strings.Fields(ref.Name), " ")
	accounts := c.placeAccounts(tool)
	i := slices.IndexFunc(accounts, func(a placeAccount) bool {
		return strings.EqualFold(a.Name, ref.Account) || ref.Account == "" && len(accounts) == 1
	})
	if ref.ID == "" || ref.ID == "shared-with-me" || i < 0 && len(accounts) > 0 {
		return errNoFolder
	}
	if i >= 0 { // a tool without connections names its accounts its own way
		ref.Account = accounts[i].Name
	}
	g := srv.PlaceGrids[ref.ID].with(category, value)
	known := slices.ContainsFunc(placesIn(tool, srv), func(p Place) bool { return p.ID == ref.ID && !p.Root })
	if g != (accessGrid{}) && !known && (ref.Name == "" || len(ref.Name) > 120) {
		return &catalogError{http.StatusBadRequest, "conn.badplace", "", errors.New("a folder needs a name")}
	}
	grids := maps.Clone(srv.PlaceGrids)
	if grids == nil {
		grids = map[string]accessGrid{}
	}
	grids[ref.ID] = g
	if known == (g != (accessGrid{})) { // the server's places stay as they are
		srv.PlaceGrids = keepPlaceGrids(grids, placesIn(tool, srv))
		return c.putServer(putUpdate, tool, srv, nil)
	}
	return c.changePlaces(tool, func(list []Place) ([]Place, error) {
		if known {
			return slices.DeleteFunc(list, func(p Place) bool { return p.ID == ref.ID && !p.Root }), nil
		}
		final := ref.Name
		taken := func(name string) bool { // a place's name, or a whole account's (the files server's)
			return slices.ContainsFunc(list, func(p Place) bool { return strings.EqualFold(p.Name, name) }) ||
				slices.ContainsFunc(accounts, func(a placeAccount) bool { return strings.EqualFold(a.Name, name) })
		}
		for n := 2; taken(final); n++ {
			final = fmt.Sprintf("%s (%d)", ref.Name, n)
		}
		return append(list, Place{Name: final, Account: ref.Account, ID: ref.ID, Parents: ref.Parents}), nil
	}, grids)
}

// placeItem is a place as a tool's browsing shows it.
type placeItem struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Link string `json:"link"`
}

// browsePlaces lists an account's places under parent through the tool's
// setup tool, which tasks never get.
func (c *core) browsePlaces(ctx context.Context, tool, account, parent, page string) ([]placeItem, string, error) {
	k, _, ok := keeperOf(c.conf().Servers[tool])
	u := c.pool.get(tool)
	if !ok || u == nil {
		return nil, "", errors.New(tool + " does not run")
	}
	in := map[string]string{"parent": parent}
	if account != "" {
		in["account"] = account
	}
	if page != "" {
		in["page_token"] = page
	}
	args, _ := json.Marshal(in)
	res, err := u.call(ctx, k.Browse, args)
	if err != nil {
		return nil, "", err
	}
	if res.IsError {
		return nil, "", errors.New(resultText(res))
	}
	var out struct {
		Places []placeItem `json:"places"`
		Next   string      `json:"next_page_token"`
	}
	raw, _ := json.Marshal(res.StructuredContent)
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, "", err
	}
	return out.Places, out.Next, nil
}

// legacyPlaces reads the places of a tool kept before their rights were
// the grid's (the NAS, once): a place at read becomes Lire only, one at
// drop Modifier without Supprimer, so nothing is widened; the access goes
// from the configuration, which its server no longer takes. The tool's
// level stays each account's until one gets its own.
func legacyPlaces(s Server) Server {
	k, spec, ok := keeperOf(s)
	if !ok {
		return s
	}
	var doc map[string]json.RawMessage
	if json.Unmarshal([]byte(spec.Env[toolConfigEnv].Value), &doc) != nil {
		return s
	}
	var list []map[string]any
	if json.Unmarshal(doc[k.Key], &list) != nil {
		return s
	}
	changed := false
	grids := maps.Clone(s.PlaceGrids)
	for _, p := range list {
		access, has := p["access"].(string)
		if !has {
			continue
		}
		delete(p, "access")
		changed = true
		id, _ := p["id"].(string)
		if grids == nil {
			grids = map[string]accessGrid{}
		}
		switch access {
		case "drop":
			grids[id] = accessGrid{Delete: gridNever}
		default:
			grids[id] = accessGrid{Change: gridNever, Delete: gridNever}
		}
	}
	if !changed {
		return s
	}
	doc[k.Key], _ = json.Marshal(list)
	raw, _ := json.Marshal(doc)
	env := maps.Clone(spec.Env)
	env[toolConfigEnv] = EnvValue{Value: string(raw)}
	spec.Env = env
	s.Run = marshalRun(spec)
	s.PlaceGrids = grids
	return s
}

// legacyPlaceRoot moves the accounts of the tools with places to their tree
// (the NAS, once): an account's grid was the default of its given folders
// only, and becomes the root of the account, Never until set. Each folder
// given before takes the account's grid as its own cases, under what it
// set itself, so nothing it allowed or refused changes; the account keeps
// its action exceptions.
func legacyPlaceRoot(store *Store) {
	const done = "legacy_place_root"
	if store.get(done) != "" {
		return
	}
	list, err := store.catalogRows()
	if err != nil {
		return
	}
	for _, r := range list {
		var s Server
		if r.Kind != kindServer || json.Unmarshal(r.Spec, &s) != nil {
			continue
		}
		_, cfg, isSvc := accountsOf(s)
		if _, _, ok := keeperOf(s); !ok || !isSvc {
			continue
		}
		grids := maps.Clone(s.PlaceGrids)
		if grids == nil {
			grids = map[string]accessGrid{}
		}
		accounts := map[string]accountGrid{}
		for _, a := range cfg.Accounts {
			old, exceptions := s.baseAccess().grid, map[string]string(nil)
			for name, g := range s.AccountGrids {
				if strings.EqualFold(name, a.Name) {
					old, exceptions = g.Grid, g.Exceptions
				}
			}
			for name, level := range s.AccountAccess {
				if strings.EqualFold(name, a.Name) {
					old = levelGrid(level)
				}
			}
			full := accessGrid{old.value(catRead), old.value(catChange), old.value(catDelete), old.value(catSend)}
			for _, p := range placesIn(r.Name, s) {
				if !p.Root && (strings.EqualFold(p.Account, a.Name) || p.Account == "" && len(cfg.Accounts) == 1) {
					grids[p.ID] = full.over(grids[p.ID])
				}
			}
			accounts[a.Name] = accountGrid{Grid: neverGrid, Exceptions: exceptions}
		}
		s.PlaceGrids, s.AccountGrids, s.AccountAccess = grids, accounts, nil
		if len(s.PlaceGrids) == 0 {
			s.PlaceGrids = nil
		}
		if err := store.putCatalog(kindServer, r.Name, s); err != nil {
			log.Printf("catalog: %s: its tree: %v", r.Name, err)
			return
		}
		log.Printf("catalog: %s: each account is now the root of its folders, Never until set; its folders kept what they had", r.Name)
	}
	store.set(done, "1")
}
