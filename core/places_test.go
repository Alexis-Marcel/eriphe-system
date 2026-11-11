package main

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
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
	cfg := `{"vault":"main","places":[{"name":"Recettes","account":"sam","id":"r1"},{"name":"Voyages","id":"v2"}]}`
	run, _ := json.Marshal(RunSpec{Image: "registry.example.org/notes:1", Transport: transportStdio,
		Env: map[string]EnvValue{toolConfigEnv: {Value: cfg}, "OTHER": {Value: "kept"}}})
	if err := c.putServer(putCreate, "carnets", Server{Run: run, Use: "notes"}.withLevel(accessReads), nil); err != nil {
		t.Fatal(err)
	}

	if tools := c.placeTools(); len(tools) != 1 || tools[0] != "carnets" {
		t.Fatalf("place tools %v", tools)
	}
	// Nothing is reached until set (rootAccess): each its own cases.
	for _, cs := range []struct{ id, cat, value string }{{"r1", catRead, gridFree}, {"r1", catChange, gridApproval}, {"v2", catRead, gridFree}} {
		if err := c.setPlaceCase("carnets", placeRef{ID: cs.id}, cs.cat, cs.value); err != nil {
			t.Fatal(err)
		}
	}
	p := c.placeNamed("recettes")
	if p == nil || p.Tool != "carnets" || c.placeMost(*p) != accessWrite || p.Account != "sam" {
		t.Fatalf("place %+v", p)
	}
	if v := c.placeNamed("Voyages"); v == nil || c.placeMost(*v) != accessRead {
		t.Fatalf("read only: %+v", v)
	}
	views, _ := c.folderViews()
	if i := slices.IndexFunc(views, func(v folderView) bool { return v.Name == "Recettes" }); i < 0 || views[i].Source != sourcePlace ||
		views[i].Tool != "carnets" || views[i].PlaceID != "r1" {
		t.Fatalf("views %+v", views)
	}

	l, err := c.resolveRequest(runRequest{Resources: []ResourceRef{{Kind: resPlace, Ref: "Recettes", Access: accessWrite}}})
	if err != nil || len(l.Grant) != 1 || l.Grant[0] != "carnets" {
		t.Fatalf("lent: %+v %v", l, err)
	}
	if _, err := c.resolveRequest(runRequest{Resources: []ResourceRef{{Kind: resPlace, Ref: "Voyages", Access: accessWrite}}}); err == nil {
		t.Fatal("a read place lent write")
	}

	for _, cat := range []string{catRead, catChange} { // back to following: no longer kept
		if err := c.setPlaceCase("carnets", placeRef{ID: "r1"}, cat, ""); err != nil {
			t.Fatal(err)
		}
	}
	spec, _ := decodeRun(c.conf().Servers["carnets"].Run)
	var doc map[string]json.RawMessage
	json.Unmarshal([]byte(spec.Env[toolConfigEnv].Value), &doc)
	if c.placeNamed("Recettes") != nil || c.placeNamed("Voyages") == nil || string(doc["vault"]) != `"main"` || spec.Env["OTHER"].Value != "kept" ||
		len(c.conf().Servers["carnets"].PlaceGrids) != 1 {
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
		return []Place{{Name: "Recettes", Account: "sam", ID: "r1"}}, nil
	}, nil); err != nil {
		t.Fatal(err)
	}
	if err := c.dropAccount(notes, "sam"); err != nil {
		t.Fatal(err)
	}
	// bob reads (its level): the whole account is its place, nothing else.
	if st, _ := c.serviceServer(notes); len(st.Config.Accounts) != 1 || len(c.places()) != 1 || !c.places()[0].Root {
		t.Fatalf("after drop: %+v %+v", st.Config, c.places())
	}
}

// The places kept before their rights were the grid's (the NAS, once):
// read is Lire only, drop Modifier without Supprimer, and the access leaves
// the configuration the server reads.
func TestLegacyPlaces(t *testing.T) {
	cfg := `{"accounts":[],"places":[{"name":"A","id":"a","access":"read"},{"name":"B","id":"b","access":"drop"},{"name":"C","id":"c"}]}`
	run, _ := json.Marshal(RunSpec{Image: filesService.Image + ":1", Transport: transportStdio, Env: map[string]EnvValue{toolConfigEnv: {Value: cfg}}})
	s := legacyPlaces(Server{Run: run, Use: "files"})
	want := map[string]accessGrid{"a": {Change: gridNever, Delete: gridNever}, "b": {Delete: gridNever}}
	if len(s.PlaceGrids) != 2 || s.PlaceGrids["a"] != want["a"] || s.PlaceGrids["b"] != want["b"] {
		t.Fatalf("grids: %+v", s.PlaceGrids)
	}
	spec, _ := decodeRun(s.Run)
	if v := spec.Env[toolConfigEnv].Value; strings.Contains(v, "access") || !strings.Contains(v, `"id":"c"`) {
		t.Fatalf("config: %s", v)
	}
	if again := legacyPlaces(s); string(again.Run) != string(s.Run) {
		t.Fatal("not idempotent")
	}
}

// A call is held to the place it names; one naming none takes the
// strictest of the places, and must name one when they differ on a write.
// An account whose token reads only reads only, whatever a place says.
func TestPlaceFacts(t *testing.T) {
	accounts := `[{"name":"sam","provider":"google","auth":{"token_env":"A"},"scopes":["` + scopeDrive + `"]},` +
		`{"name":"bob","provider":"google","auth":{"token_env":"B"},"scopes":["` + scopeDriveReadonly + `"]}]`
	places := `[{"name":"Compta","account":"sam","id":"c"},{"name":"Photos","account":"sam","id":"p"},{"name":"Bob","account":"bob","id":"b"}]`
	run, _ := json.Marshal(RunSpec{Image: filesService.Image + ":1", Transport: transportStdio,
		Env: map[string]EnvValue{toolConfigEnv: {Value: `{"accounts":` + accounts + `,"places":` + places + `}`}}})
	s := Server{Run: run, Use: "files", AccountAccess: map[string]string{"sam": accessReads, "bob": accessReads},
		PlaceGrids: map[string]accessGrid{"p": {Change: gridFree}, "b": {Change: gridFree}}}
	write := &mcp.Tool{Name: "write_file", Annotations: &mcp.ToolAnnotations{DestructiveHint: ptrTo(false), OpenWorldHint: ptrTo(false)}}
	read := &mcp.Tool{Name: "list_files", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}}
	for _, c := range []struct {
		folder string
		want   string
	}{{"Compta", gridApproval}, {"photos", gridFree}, {"Bob", gridNever}} {
		if f := s.toolFacts(write, map[string]any{"folder": c.folder}); f.value != c.want || f.noAccount {
			t.Errorf("%s: %+v, want %s", c.folder, f, c.want)
		}
	}
	if f := s.toolFacts(write, map[string]any{}); !f.noAccount || f.value != gridNever {
		t.Errorf("no folder, a write: %+v", f)
	}
	if f := s.toolFacts(read, map[string]any{}); f.noAccount || f.value != gridFree {
		t.Errorf("no folder, a read: %+v", f)
	}
	if !lends(s, write) {
		t.Error("write_file not lent though a folder allows it")
	}
	if c := s.readCeilings(); !c["bob"] || c["sam"] {
		t.Errorf("ceilings: %v", c)
	}
}
