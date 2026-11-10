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
// own access: a drive's folders. A tool that has places browses them with
// a setup tool (eriphe/setup, which tasks never get: setupTool) and keeps
// the ones picked in its own configuration, where its server enforces
// them; where is data (placeKeepers), so a tool is found by having places,
// never by its name.
//
// A place is lent to a task as {kind: place, ref: <its name>}; lending a
// place lends its whole tool, which reaches every place given to Eriphe,
// each with its own access.

const resPlace = "place"

// placeKeeper says where a tool's server keeps its places: a JSON
// variable of its environment, and the key of the list in it.
type placeKeeper struct {
	Image  string // the server's image, without its tag
	Env    string
	Key    string
	Browse string // its setup tool that browses them
	Kind   string // the kind of tool it is on a connection (connection_tools.go)
}

// placeKeepers are the tools that have places.
var placeKeepers = []placeKeeper{
	{Image: filesImage, Env: toolConfigEnv, Key: "places", Browse: "browse_folders", Kind: toolDrive},
}

// Place is a place given to Eriphe, as its tool keeps it.
type Place struct {
	Tool    string `json:"-"`       // the catalog server that has it
	Name    string `json:"name"`    // how tools name it
	Account string `json:"account"` // the tool's account it is in
	ID      string `json:"id"`      // its id at the tool
	Access  string `json:"access"`  // read, or drop: also new files in its results subfolder
}

// keeperOf finds where a server keeps its places, if it has any.
func keeperOf(srv Server) (placeKeeper, RunSpec, bool) {
	if !srv.local() {
		return placeKeeper{}, RunSpec{}, false
	}
	spec, err := decodeRun(srv.Run)
	if err != nil {
		return placeKeeper{}, RunSpec{}, false
	}
	for _, k := range placeKeepers {
		if strings.HasPrefix(spec.Image, k.Image) {
			return k, spec, true
		}
	}
	return placeKeeper{}, RunSpec{}, false
}

// placesIn reads a server's places.
func placesIn(name string, srv Server) []Place {
	k, spec, ok := keeperOf(srv)
	if !ok {
		return nil
	}
	var doc map[string]json.RawMessage
	if json.Unmarshal([]byte(spec.Env[k.Env].Value), &doc) != nil {
		return nil
	}
	var list []Place
	json.Unmarshal(doc[k.Key], &list)
	for i := range list {
		list[i].Tool = name
		list[i].Access = orStr(list[i].Access, accessRead)
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

// placeAccount is an account of a tool with places, and whether places
// may be given to it to write in (its connection granted the tool's
// scopes that write).
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
		t, _ := signInOf(k.Kind, cn.Provider)
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
	if err := json.Unmarshal([]byte(spec.Env[k.Env].Value), &doc); err != nil {
		return err
	}
	list := placesIn(tool, srv)
	list, err := change(list)
	if err != nil {
		return err
	}
	doc[k.Key], _ = json.Marshal(list)
	raw, _ := json.Marshal(doc)
	env := maps.Clone(spec.Env)
	env[k.Env] = EnvValue{Value: string(raw)}
	spec.Env = env
	srv.Run = marshalRun(spec)
	return c.putServer(putUpdate, tool, srv, nil)
}

// removePlace takes a place out of its tool.
func (c *core) removePlace(tool, id string) error {
	return c.changePlaces(tool, func(list []Place) ([]Place, error) {
		return slices.DeleteFunc(list, func(p Place) bool { return p.ID == id }), nil
	})
}

// givePlace gives a place of an account of a tool to Eriphe, or changes
// its access; account may be empty with one account. Its name is made
// unique among the tool's places.
func (c *core) givePlace(tool, account, id, name, access string) error {
	id, name = strings.TrimSpace(id), strings.Join(strings.Fields(name), " ")
	if id == "" || name == "" || len(name) > 120 || (access != accessRead && access != accessDrop) {
		return &catalogError{http.StatusBadRequest, "conn.badplace", "", errors.New("a place needs an id, a name and an access")}
	}
	accounts := c.placeAccounts(tool)
	return c.changePlaces(tool, func(list []Place) ([]Place, error) {
		for _, p := range list {
			if p.ID == id {
				account = p.Account
			}
		}
		i := slices.IndexFunc(accounts, func(a placeAccount) bool {
			return strings.EqualFold(a.Name, account) || account == "" && len(accounts) == 1
		})
		if i < 0 {
			return nil, &catalogError{http.StatusBadRequest, "conn.badplace", "", errors.New("no such account")}
		}
		if access == accessDrop && !accounts[i].CanWrite {
			return nil, &catalogError{http.StatusBadRequest, "conn.readonly", "", errors.New("the account was connected for reading only")}
		}
		for j, p := range list {
			if p.ID == id {
				list[j].Access = access
				return list, nil
			}
		}
		final := name
		for n := 2; slices.ContainsFunc(list, func(p Place) bool { return strings.EqualFold(p.Name, final) }); n++ {
			final = fmt.Sprintf("%s (%d)", name, n)
		}
		return append(list, Place{Name: final, Account: accounts[i].Name, ID: id, Access: access}), nil
	})
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
		Folders []placeItem `json:"folders"`
		Next    string      `json:"next_page_token"`
	}
	raw, _ := json.Marshal(res.StructuredContent)
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, "", err
	}
	return out.Folders, out.Next, nil
}
