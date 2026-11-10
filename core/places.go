package main

import (
	"encoding/json"
	"maps"
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
// each with its own access. {kind: drive-folder} is read as a place, for
// good: tasks and skills written before places name it so.

const resPlace = "place"

// placeKeeper says where a tool's server keeps its places: a JSON
// variable of its environment, and the key of the list in it.
type placeKeeper struct {
	Image  string // the server's image, without its tag
	Env    string
	Key    string
	Browse string // its setup tool that browses them
}

// placeKeepers are the tools that have places.
var placeKeepers = []placeKeeper{
	{Image: filesImage, Env: filesConfigEnv, Key: "folders", Browse: "browse_folders"},
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

// removePlace takes a place out of its tool's configuration, which
// restarts its server.
func (c *core) removePlace(tool, id string) error {
	srv, ok := c.conf().Servers[tool]
	k, spec, has := keeperOf(srv)
	if !ok || !has {
		return errNoFolder
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal([]byte(spec.Env[k.Env].Value), &doc); err != nil {
		return err
	}
	var list []json.RawMessage
	json.Unmarshal(doc[k.Key], &list)
	list = slices.DeleteFunc(list, func(raw json.RawMessage) bool {
		var p Place
		return json.Unmarshal(raw, &p) == nil && p.ID == id
	})
	doc[k.Key], _ = json.Marshal(list)
	raw, _ := json.Marshal(doc)
	env := maps.Clone(spec.Env)
	env[k.Env] = EnvValue{Value: string(raw)}
	spec.Env = env
	srv.Run = marshalRun(spec)
	return c.putServer(putUpdate, tool, srv, nil)
}
