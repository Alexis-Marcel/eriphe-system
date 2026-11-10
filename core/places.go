package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

// Place is a place given to Eriphe, as its tool keeps it. What a task may
// do there is its server's grid (Server.PlaceGrids, access_grid.go), which
// eriphe-core holds call by call; for a connection's place (a repository,
// connection_places.go), its Access.
type Place struct {
	Tool    string `json:"-"`                // the catalog server that has it
	Name    string `json:"name"`             // how tools name it
	Account string `json:"account"`          // the tool's account it is in
	ID      string `json:"id"`               // its id at the tool
	Access  string `json:"access,omitempty"` // a connection's place: read or write
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

// changePlaces rewrites a tool's places, which restarts its server.
func (c *core) changePlaces(tool string, change func([]Place) ([]Place, error)) error {
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
	for i := range list {
		list[i].Tool, list[i].Access = "", ""
	}
	doc[k.Key], _ = json.Marshal(list)
	raw, _ := json.Marshal(doc)
	env := maps.Clone(spec.Env)
	env[toolConfigEnv] = EnvValue{Value: string(raw)}
	spec.Env = env
	srv.Run = marshalRun(spec)
	srv.PlaceGrids = keepPlaceGrids(srv.PlaceGrids, list)
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

// removePlace takes a place out of its tool.
func (c *core) removePlace(tool, id string) error {
	return c.changePlaces(tool, func(list []Place) ([]Place, error) {
		return slices.DeleteFunc(list, func(p Place) bool { return p.ID == id }), nil
	})
}

// givePlace gives a place of an account of a tool to Eriphe; account may
// be empty with one account. Its name is made unique among the tool's
// places. It follows its account's grid until it has exceptions of its own
// (setPlaceCase).
func (c *core) givePlace(tool, account, id, name string) error {
	id, name = strings.TrimSpace(id), strings.Join(strings.Fields(name), " ")
	if id == "" || name == "" || len(name) > 120 {
		return &catalogError{http.StatusBadRequest, "conn.badplace", "", errors.New("a place needs an id and a name")}
	}
	accounts := c.placeAccounts(tool)
	return c.changePlaces(tool, func(list []Place) ([]Place, error) {
		if slices.ContainsFunc(list, func(p Place) bool { return p.ID == id }) {
			return list, nil
		}
		i := slices.IndexFunc(accounts, func(a placeAccount) bool {
			return strings.EqualFold(a.Name, account) || account == "" && len(accounts) == 1
		})
		if i < 0 {
			return nil, &catalogError{http.StatusBadRequest, "conn.badplace", "", errors.New("no such account")}
		}
		final := name
		for n := 2; slices.ContainsFunc(list, func(p Place) bool { return strings.EqualFold(p.Name, final) }); n++ {
			final = fmt.Sprintf("%s (%d)", name, n)
		}
		return append(list, Place{Name: final, Account: accounts[i].Name, ID: id}), nil
	})
}

// setPlaceCase gives one case of a place its own value, above or below its
// account's; "" has it follow its account again. A case its tool has not
// (its card's Cases) is refused.
func (c *core) setPlaceCase(tool, id, category, value string) error {
	srv, ok := c.conf().Servers[tool]
	k, _, has := keeperOf(srv)
	if !ok || !has || c.source(kindServer, tool) != sourceWorkspace {
		return errReadOnly(kindServer, tool)
	}
	if !slices.Contains(k.Card.cases(), category) || value != "" && !slices.Contains(gridValues, value) {
		return errInvalid(errors.New("a case is one of the tool's, its value free, approval, never or empty"))
	}
	if !slices.ContainsFunc(placesIn(tool, srv), func(p Place) bool { return p.ID == id }) {
		return errNoFolder
	}
	grids := maps.Clone(srv.PlaceGrids)
	if grids == nil {
		grids = map[string]accessGrid{}
	}
	g := grids[id]
	switch category {
	case catRead:
		g.Read = value
	case catChange:
		g.Change = value
	case catDelete:
		g.Delete = value
	case catSend:
		g.Send = value
	}
	grids[id] = g
	srv.PlaceGrids = keepPlaceGrids(grids, placesIn(tool, srv))
	return c.putServer(putUpdate, tool, srv, nil)
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
		case accessDrop:
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
