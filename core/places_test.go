package main

import (
	"encoding/json"
	"slices"
	"testing"
)

// A tool has places when its card says where it keeps them: any tool, not
// only the drive. Its places are listed with the folders, lent as places
// (lending its whole tool), and taken out of its configuration.
func TestPlacesOfAnyTool(t *testing.T) {
	old := toolCards
	toolCards = append(slices.Clone(toolCards), toolCard{ID: "notes", Base: "carnets", Image: "registry.example.org/notes",
		Places: &placeKeeper{Key: "places", Browse: "browse_places"}})
	t.Cleanup(func() { toolCards = old })
	c := newDynamicCore(t, nil).core
	cfg := `{"vault":"main","places":[{"name":"Recettes","account":"sam","id":"r1","access":"drop"},{"name":"Voyages","id":"v2"}]}`
	run, _ := json.Marshal(RunSpec{Image: "registry.example.org/notes:1", Transport: transportStdio,
		Env: map[string]EnvValue{toolConfigEnv: {Value: cfg}, "OTHER": {Value: "kept"}}})
	if err := c.putServer(putCreate, "carnets", Server{Run: run, Use: "notes"}.withLevel(accessReads), nil); err != nil {
		t.Fatal(err)
	}

	if tools := c.placeTools(); len(tools) != 1 || tools[0] != "carnets" {
		t.Fatalf("place tools %v", tools)
	}
	p := c.placeNamed("recettes")
	if p == nil || p.Tool != "carnets" || p.Access != accessDrop || p.Account != "sam" {
		t.Fatalf("place %+v", p)
	}
	if v := c.placeNamed("Voyages"); v == nil || v.Access != accessRead {
		t.Fatalf("default access: %+v", v)
	}
	views, _ := c.folderViews()
	if i := slices.IndexFunc(views, func(v folderView) bool { return v.Name == "Recettes" }); i < 0 || views[i].Source != sourcePlace ||
		views[i].Tool != "carnets" || views[i].PlaceID != "r1" {
		t.Fatalf("views %+v", views)
	}

	l, err := c.resolveRequest(runRequest{Resources: []ResourceRef{{Kind: resPlace, Ref: "Recettes", Access: accessDrop}}})
	if err != nil || len(l.Grant) != 1 || l.Grant[0] != "carnets" {
		t.Fatalf("lent: %+v %v", l, err)
	}
	if _, err := c.resolveRequest(runRequest{Resources: []ResourceRef{{Kind: resPlace, Ref: "Voyages", Access: accessDrop}}}); err == nil {
		t.Fatal("a read place lent drop")
	}

	if err := c.removePlace("carnets", "r1"); err != nil {
		t.Fatal(err)
	}
	spec, _ := decodeRun(c.conf().Servers["carnets"].Run)
	var doc map[string]json.RawMessage
	json.Unmarshal([]byte(spec.Env[toolConfigEnv].Value), &doc)
	if c.placeNamed("Recettes") != nil || c.placeNamed("Voyages") == nil || string(doc["vault"]) != `"main"` || spec.Env["OTHER"].Value != "kept" {
		t.Fatalf("after remove: %+v", spec.Env)
	}
}

// A card is all a tool needs: one no code knows takes several accounts,
// each with its own level, and its places.
func TestAnyToolCard(t *testing.T) {
	notes := toolCard{ID: "notes", Base: "carnets", Image: "registry.example.org/notes", Preset: "local", AccountLevels: true,
		Places: &placeKeeper{Key: "places", Browse: "browse_places"}}
	old := toolCards
	toolCards = append(slices.Clone(toolCards), notes)
	t.Cleanup(func() { toolCards = old })
	c := newDynamicCore(t, nil).core
	for _, name := range []string{"sam", "bob"} {
		if err := c.putAccount(notes, accountPut{Lang: "fr", Account: svcAccount{Name: name}, Password: "pw-" + name}); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.setAccountLevel("carnets", "bob", accessReadOnly); err != nil {
		t.Fatal(err)
	}
	st, ok := c.serviceServer(notes)
	if !ok || st.Name != "carnets" || len(st.Config.Accounts) != 2 || !st.Config.Accounts[1].ReadOnly {
		t.Fatalf("server: %+v", st)
	}
	if err := c.changePlaces("carnets", func([]Place) ([]Place, error) {
		return []Place{{Name: "Recettes", Account: "sam", ID: "r1", Access: accessRead}}, nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := c.dropAccount(notes, "sam"); err != nil {
		t.Fatal(err)
	}
	if st, _ := c.serviceServer(notes); len(st.Config.Accounts) != 1 || len(c.places()) != 0 {
		t.Fatalf("after drop: %+v %+v", st.Config, c.places())
	}
}
