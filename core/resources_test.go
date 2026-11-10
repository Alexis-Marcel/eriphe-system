package main

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// folderRefs lends folders; Clients, opened for drop in these tests, for drop.
func folderRefs(names ...string) []ResourceRef {
	var out []ResourceRef
	for _, n := range names {
		access := accessRead
		if strings.EqualFold(n, "Clients") {
			access = accessDrop
		}
		out = append(out, ResourceRef{Kind: resFolder, Ref: n, Access: access})
	}
	return out
}

// Resources are checked for their form; an assistant's tools are read into them.
func TestNormalizeResources(t *testing.T) {
	got, err := normalizeResources(runRequest{Tools: []string{"github.get_*", " "}})
	if err != nil {
		t.Fatal(err)
	}
	if want := []ResourceRef{{Kind: resTool, Ref: "github.get_*"}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("tools = %+v", got)
	}
	got, err = normalizeResources(runRequest{Resources: []ResourceRef{
		{Kind: " Repo ", Ref: "owner/repo", Access: "WRITE", Branch: "eriphe/x"},
		{Kind: "folder", Ref: "Clients"},
		{Kind: "tool", Ref: "notes"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Kind != resRepo || got[0].Access != accessWrite || got[1].Access != accessRead || got[2].Access != "" {
		t.Fatalf("explicit = %+v", got)
	}
	for name, r := range map[string]runRequest{
		"is not one of":              {Resources: []ResourceRef{{Kind: "mailbox", Ref: "x"}}},
		"ref is required":            {Resources: []ResourceRef{{Kind: "folder"}}},
		"takes access read or drop":  {Resources: []ResourceRef{{Kind: "place", Ref: "Clients", Access: "write"}}},
		"takes access read or write": {Resources: []ResourceRef{{Kind: "repo", Ref: "o/r", Access: "drop"}}},
		"a repo's only":              {Resources: []ResourceRef{{Kind: "folder", Ref: "Clients", Branch: "x"}}},
		"trust_skills are a repo":    {Resources: []ResourceRef{{Kind: "folder", Ref: "Clients", TrustSkills: true}}},
		"one repository per task":    {Resources: []ResourceRef{{Kind: "repo", Ref: "o/a"}, {Kind: "repo", Ref: "o/b"}}},
		"not a branch name":          {Resources: []ResourceRef{{Kind: "repo", Ref: "o/r", Branch: "--force"}}},
		"inside the repository":      {Resources: []ResourceRef{{Kind: "repo", Ref: "o/r", Dir: "../etc"}}},
	} {
		if _, err := normalizeResources(r); err == nil || !strings.Contains(err.Error(), name) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestAccessScale(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{{"read", "read", true}, {"drop", "read", true}, {"write", "drop", true}, {"read", "drop", false}, {"drop", "write", false}, {"write", "", false}} {
		if atLeast(c.a, c.b) != c.want {
			t.Errorf("%s ≥ %s: %v", c.a, c.b, !c.want)
		}
	}
}

// withDrive gives the core a files server with Drive folders.
func withDrive(t *testing.T, c *core, folders ...Place) {
	t.Helper()
	fc, _ := json.Marshal(map[string]any{"accounts": []any{}, "folders": folders})
	run, _ := json.Marshal(map[string]any{"image": filesImage + ":1", "transport": "stdio", "env": map[string]string{filesConfigEnv: string(fc)}})
	if c.base.Servers == nil {
		c.base.Servers = map[string]Server{}
	}
	c.base.Servers[filesServer] = Server{Run: run, Use: "files"}
	c.snap.Store(mergeCatalog(c.base, c.dyn))
}

// Each entry is checked against what the user opened, and never gets more.
func TestResolveFolders(t *testing.T) {
	c, _ := foldersCore(t)
	withDrive(t, c, Place{Name: "Factures", ID: "d1", Access: folderRead}, Place{Name: "Devis", ID: "d2", Access: folderDrop})

	// The old field lends a folder with its own access; a resource, read unless it says drop.
	l, err := c.resolveRequest(runRequest{Resources: folderRefs("clients", "Photos")})
	if err != nil {
		t.Fatal(err)
	}
	if len(l.List) != 2 || l.List[0] != (ResourceRef{Kind: resFolder, Ref: "Clients", Access: accessDrop}) || l.List[1].Access != accessRead ||
		l.Folders[0].Access != folderDrop || l.Machine != "nas" {
		t.Fatalf("legacy %+v %+v", l.List, l.Folders)
	}
	l, err = c.resolveRequest(runRequest{Resources: []ResourceRef{{Kind: resFolder, Ref: "Clients"}}})
	if err != nil || l.Folders[0].Access != folderRead {
		t.Fatalf("default read: %+v %v", l, err)
	}
	// The same folder twice: once, with the higher access.
	l, _ = c.resolveRequest(runRequest{Resources: []ResourceRef{{Kind: resFolder, Ref: "Clients", Access: accessDrop}, {Kind: resFolder, Ref: "CLIENTS", Access: accessRead}}})
	if len(l.List) != 1 || l.List[0].Access != accessDrop {
		t.Fatalf("merged %+v", l.List)
	}
	// A place lends its tool.
	for _, kind := range []string{resPlace} {
		l, err = c.resolveRequest(runRequest{Resources: []ResourceRef{{Kind: kind, Ref: "devis", Access: accessDrop}}})
		if err != nil || len(l.Grant) != 1 || l.Grant[0] != filesServer || l.List[0].Ref != "Devis" || l.List[0].Kind != resPlace || len(l.Folders) != 0 {
			t.Fatalf("%s: %+v %v", kind, l, err)
		}
	}
	for want, r := range map[string]ResourceRef{
		"for read only: it cannot be lent drop": {Kind: resFolder, Ref: "Photos", Access: accessDrop},
		"is a place of the tool fichiers":       {Kind: resFolder, Ref: "Factures"},
		"is on a machine":                       {Kind: resPlace, Ref: "Clients"},
		"place \"Factures\" is opened":          {Kind: resPlace, Ref: "Factures", Access: accessDrop},
		"unknown":                               {Kind: resFolder, Ref: "Nope"},
	} {
		if _, err := c.resolveRequest(runRequest{Resources: []ResourceRef{r}}); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%+v: %v, want %q", r, err, want)
		}
	}
}

// A tool's access comes from its annotations; read is refused for tools
// that may act.
func TestResolveTools(t *testing.T) {
	c := newCatalogCore(t)
	l, err := c.resolveRequest(runRequest{Tools: []string{"github.get_*", "github"}})
	if err != nil {
		t.Fatal(err)
	}
	if l.List[0].Access != accessRead || l.List[1].Access != accessWrite || strings.Join(l.Grant, ",") != "github,github.get_*" {
		t.Fatalf("derived %+v %v", l.List, l.Grant)
	}
	if _, err := c.resolveRequest(runRequest{Resources: []ResourceRef{{Kind: resTool, Ref: "github.create_issue", Access: accessRead}}}); err == nil ||
		!strings.Contains(err.Error(), "may act") {
		t.Fatalf("acting tool lent read: %v", err)
	}
	if _, err := c.resolveRequest(runRequest{Resources: []ResourceRef{{Kind: resTool, Ref: "mail"}}}); err == nil {
		t.Fatal("an unknown server was lent")
	}
}

// The normalized list is kept with the task, and the old columns and the
// node's spec fields still follow from it.
func TestTaskKeepsResources(t *testing.T) {
	c, store := foldersCore(t)
	ctx := context.Background()
	task, err := c.runTask(ctx, runRequest{Prompt: "fix it", Resources: []ResourceRef{
		{Kind: resRepo, Ref: "owner/site", Access: accessWrite, Branch: "eriphe/fix", Base: "dev", Dir: "web"},
		{Kind: resFolder, Ref: "clients"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	var raw string
	store.db.QueryRow(`SELECT resources FROM tasks WHERE id = ?`, task.ID).Scan(&raw)
	want := `[{"kind":"repo","ref":"owner/site","access":"write","branch":"eriphe/fix","base":"dev","dir":"web"},{"kind":"folder","ref":"Clients","access":"read"}]`
	if raw != want {
		t.Fatalf("stored %s", raw)
	}
	got, _ := store.task(task.ID)
	if len(got.Resources) != 2 || got.Dir != "owner/site@eriphe/fix" || got.Folders != "Clients" {
		t.Fatalf("task %+v", got)
	}
	spec := assignedSpec(t, c, "nas", task.ID)
	if spec.Repo != "owner/site" || spec.Ref != "dev" || spec.Branch != "eriphe/fix" || spec.Dir != "web" {
		t.Fatalf("spec %+v", spec)
	}
	// Lent for reading: mounted read-only, though the folder allows drops.
	if len(spec.Folders) != 1 || spec.Folders[0].Write || strings.Contains(spec.AppendSystemPrompt, "Eriphe/") {
		t.Fatalf("folders %+v, %q", spec.Folders, spec.AppendSystemPrompt)
	}

	// No resources at all is still a list; a task from before has none.
	bare, _ := c.runTask(ctx, runRequest{Prompt: "x"})
	store.db.QueryRow(`SELECT resources FROM tasks WHERE id = ?`, bare.ID).Scan(&raw)
	if raw != "[]" {
		t.Fatalf("empty list stored as %q", raw)
	}
	store.insertTask(&TaskRow{ID: "t-old", Node: "nas", Runtime: "claude-code", Prompt: "x", State: stateRunning, Folders: "Clients"})
	if old, _ := store.task("t-old"); old.Resources != nil {
		t.Fatalf("old task %+v", old.Resources)
	}
	if fs, _ := c.specFolders("Clients", nil); len(fs) != 1 || !fs[0].Write {
		t.Fatalf("old task's folders %+v", fs)
	}
}

// A repository is what a repo resource lends, and no more.
func TestRepoFromResource(t *testing.T) {
	c, _ := credCore(t, "")
	withApp(t, c, newFakeGitHub(t))
	task := &TaskRow{ID: "t-2", Resources: []ResourceRef{{Kind: resRepo, Ref: "https://github.com/owner/repo.git", Access: accessWrite, Branch: "eriphe/fix"}}}
	c.store.set(lentByUserKey(task.ID), "1")
	facts := c.repoFacts(task)
	if d := decideRepo("owner/repo", accessWrite, facts); d.Decision != decideAllow {
		t.Fatalf("push on its branch: %+v", d)
	}
	if d := decideRepo("owner/other", accessRead, facts); d.Decision != decideDeny {
		t.Fatalf("other repo: %+v", d)
	}
}

// On a tainted turn, run_task lends reads only, of every kind.
func TestTaintedResources(t *testing.T) {
	c := newCatalogCore(t)
	withDrive(t, c, Place{Name: "Factures", ID: "d1", Access: folderRead})
	c.conf().User = "Sam"
	refused := []runRequest{
		{Resources: []ResourceRef{{Kind: resPlace, Ref: "Factures"}}},
		{Resources: []ResourceRef{{Kind: resRepo, Ref: "someone/tool", TrustSkills: true}}},
	}
	allowed := []runRequest{
		{},
		{Resources: []ResourceRef{{Kind: resRepo, Ref: "owner/repo"}}},
		// A write to a repository is asked on the start's card, as an access.
		{Resources: []ResourceRef{{Kind: resRepo, Ref: "owner/repo", Access: accessWrite, Branch: "x"}}},
		{Resources: []ResourceRef{{Kind: resTool, Ref: "github.get_*"}}},
		{Tools: []string{"github.get_*"}},
		// What a tool may do is asked on a card at launch (access_card.go).
		{Resources: []ResourceRef{{Kind: resTool, Ref: "github.create_issue", Access: accessWrite}}},
		{Tools: []string{"github"}},
		{Tools: []string{"notes"}},
	}
	for _, r := range append(refused, allowed...) {
		if err := c.taintedResources(r); err != nil {
			t.Fatalf("clean turn %+v: %v", r, err)
		}
	}
	c.turn.taint("gmail.read")
	for _, r := range refused {
		if err := c.taintedResources(r); err == nil || !strings.Contains(err.Error(), "ask Sam first") {
			t.Errorf("tainted %+v: %v", r, err)
		}
	}
	for _, r := range allowed {
		if err := c.taintedResources(r); err != nil {
			t.Errorf("tainted %+v: %v", r, err)
		}
	}
}

// run_task teaches resources first; the old fields say they are legacy.
func TestRunTaskSchema(t *testing.T) {
	c := newCatalogCore(t)
	cs := connectTo(t, newMCPServer(c))
	tools, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tl := range tools.Tools {
		if tl.Name != "run_task" {
			continue
		}
		raw, _ := json.Marshal(tl.InputSchema)
		s := string(raw)
		for _, want := range []string{`"resources"`, `"kind"`, `"access"`, "place"} {
			if !strings.Contains(s, want) {
				t.Errorf("schema lacks %s: %s", want, s)
			}
		}
		return
	}
	t.Fatal("no run_task")
}

// decisionOf is the decision recorded for a task's permission.
func decisionOf(c *core, task, pid string) string {
	var d string
	c.store.db.QueryRow(`SELECT decision FROM decisions WHERE task_id = ? AND permission_id = ?`, task, pid).Scan(&d)
	return d
}
