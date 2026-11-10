package main

import (
	"encoding/json"
	"slices"
	"testing"
)

// A tool has places when its data says where it keeps them: any tool, not
// only the drive. Its places are listed with the folders, lent as places
// (lending its whole tool), and taken out of its configuration.
func TestPlacesOfAnyTool(t *testing.T) {
	old := placeKeepers
	placeKeepers = append(slices.Clone(placeKeepers), placeKeeper{Image: "registry.example.org/notes", Env: "NOTES_CONFIG", Key: "places"})
	t.Cleanup(func() { placeKeepers = old })
	c := newDynamicCore(t, nil).core
	cfg := `{"vault":"main","places":[{"name":"Recettes","account":"sam","id":"r1","access":"drop"},{"name":"Voyages","id":"v2"}]}`
	run, _ := json.Marshal(RunSpec{Image: "registry.example.org/notes:1", Transport: transportStdio,
		Env: map[string]EnvValue{"NOTES_CONFIG": {Value: cfg}, "OTHER": {Value: "kept"}}})
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
	json.Unmarshal([]byte(spec.Env["NOTES_CONFIG"].Value), &doc)
	if c.placeNamed("Recettes") != nil || c.placeNamed("Voyages") == nil || string(doc["vault"]) != `"main"` || spec.Env["OTHER"].Value != "kept" {
		t.Fatalf("after remove: %+v", spec.Env)
	}
}
