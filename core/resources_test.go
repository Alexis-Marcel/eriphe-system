package main

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

// The old fields of run_task are read into resources; the new ones are
// checked for their form.
func TestNormalizeResources(t *testing.T) {
	got, err := normalizeResources(runRequest{Repo: "owner/repo", Ref: "main", Branch: "eriphe/fix", Dir: "web",
		Tools: []string{"github.get_*", " "}, Folders: []string{"Clients"}})
	if err != nil {
		t.Fatal(err)
	}
	want := []ResourceRef{
		{Kind: resRepo, Ref: "owner/repo", Access: accessRead, Branch: "eriphe/fix", Base: "main", Dir: "web"},
		{Kind: resTool, Ref: "github.get_*"},
		{Kind: resFolder, Ref: "Clients"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("legacy = %+v", got)
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
		"takes access read or drop":  {Resources: []ResourceRef{{Kind: "folder", Ref: "Clients", Access: "write"}}},
		"takes access read or write": {Resources: []ResourceRef{{Kind: "repo", Ref: "o/r", Access: "drop"}}},
		"takes access read, not":     {Resources: []ResourceRef{{Kind: "file", Ref: "att_x", Access: "write"}}},
		"a repo's only":              {Resources: []ResourceRef{{Kind: "folder", Ref: "Clients", Branch: "x"}}},
		"trust_skills are a repo":    {Resources: []ResourceRef{{Kind: "folder", Ref: "Clients", TrustSkills: true}}},
		"one repository per task":    {Repo: "o/a", Resources: []ResourceRef{{Kind: "repo", Ref: "o/b"}}},
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
func withDrive(t *testing.T, c *core, folders ...DriveFolder) {
	t.Helper()
	fc, _ := json.Marshal(filesConfig{Folders: folders})
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
	ctx := context.Background()
	c.addFolder(ctx, Folder{Name: "Clients", Node: "nas", Share: "data", Path: "Clients", Access: folderDrop})
	c.addFolder(ctx, Folder{Name: "Photos", Node: "nas", Share: "media", Path: "Photos"})
	withDrive(t, c, DriveFolder{Name: "Factures", ID: "d1", Access: folderRead}, DriveFolder{Name: "Devis", ID: "d2", Access: folderDrop})

	// The old field lends a folder with its own access; a resource, read unless it says drop.
	l, err := c.resolveRequest(runRequest{Folders: []string{"clients", "Photos"}})
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
	l, _ = c.resolveRequest(runRequest{Folders: []string{"Clients"}, Resources: []ResourceRef{{Kind: resFolder, Ref: "CLIENTS", Access: accessRead}}})
	if len(l.List) != 1 || l.List[0].Access != accessDrop {
		t.Fatalf("merged %+v", l.List)
	}
	// Drive folders lend the files server, for now.
	l, err = c.resolveRequest(runRequest{Resources: []ResourceRef{{Kind: resDriveFolder, Ref: "devis", Access: accessDrop}}})
	if err != nil || len(l.Grant) != 1 || l.Grant[0] != filesServer || l.List[0].Ref != "Devis" || len(l.Folders) != 0 {
		t.Fatalf("drive: %+v %v", l, err)
	}
	for want, r := range map[string]ResourceRef{
		"for read only: it cannot be lent drop": {Kind: resFolder, Ref: "Photos", Access: accessDrop},
		"is in Google Drive":                    {Kind: resFolder, Ref: "Factures"},
		"is on a machine":                       {Kind: resDriveFolder, Ref: "Clients"},
		"Drive folder \"Factures\" is opened":   {Kind: resDriveFolder, Ref: "Factures", Access: accessDrop},
		"unknown":                               {Kind: resFolder, Ref: "Nope"},
		"no such attachment":                    {Kind: resFile, Ref: "att_00000000000000000000000000000000"},
	} {
		if _, err := c.resolveRequest(runRequest{Resources: []ResourceRef{r}}); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%+v: %v, want %q", r, err, want)
		}
	}
	// An attachment of the conversation is known, but not lent yet.
	seq, _ := c.store.addUserMessage("here", nil)
	c.store.db.Exec(`INSERT INTO attachments (id, seq, name, type, size, created) VALUES (?, ?, 'a.txt', 'text/plain', 1, 0)`,
		"att_11111111111111111111111111111111", seq)
	if _, err := c.resolveRequest(runRequest{Resources: []ResourceRef{{Kind: resFile, Ref: "attachment:att_11111111111111111111111111111111"}}}); err == nil ||
		!strings.Contains(err.Error(), "not lent to tasks yet") {
		t.Fatalf("file: %v", err)
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
	c.addFolder(ctx, Folder{Name: "Clients", Node: "nas", Share: "data", Path: "Clients", Access: folderDrop})
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

// Credentials come from the repo resources: a covering entry with enough
// access, a write on a declared branch approved by the user's own turn.
func TestDecideCredentialFromResources(t *testing.T) {
	push := credRequest{Host: "github.com", Path: "owner/site.git", Access: "write", Tool: "git"}
	lentWrite := []repoGrant{{Repo: "owner/site", Access: accessWrite, Branch: "eriphe/fix"}}
	cases := []struct {
		name   string
		req    credRequest
		facts  credFacts
		want   string
		reason string
	}{
		{"covered write on its branch", push, credFacts{Kind: "worker", Repos: lentWrite, Lent: true}, decideAllow, "lent for write"},
		{"covered read", credRequest{Host: "github.com", Path: "Owner/Site", Access: "read"}, credFacts{Kind: "worker", Repos: lentWrite, Lent: true}, decideAllow, ""},
		{"gh write still asks", credRequest{Host: "api.github.com", Path: "owner/site", Access: "write", Tool: "gh"},
			credFacts{Kind: "worker", Repos: lentWrite, Lent: true}, decideAsk, "first push"},
		{"write without a branch asks", push, credFacts{Kind: "worker", Repos: []repoGrant{{Repo: "owner/site", Access: accessWrite}}, Lent: true}, decideAsk, "first push"},
		{"lent read: a write asks", push, credFacts{Kind: "worker", Repos: []repoGrant{{Repo: "owner/site", Access: accessRead, Branch: "x"}}, Lent: true}, decideAsk, "lent for read"},
		{"another repo", credRequest{Host: "github.com", Path: "owner/other.git", Access: "read"}, credFacts{Kind: "worker", Repos: lentWrite, Lent: true}, decideDeny, "not the repository"},
		{"tainted task asks", push, credFacts{Kind: "worker", Repos: lentWrite, Lent: true, Tainted: []string{"WebFetch"}}, decideAsk, "external content"},
		{"fallback asks", credRequest{Host: "github.com", Path: "owner/site.git", Access: "read"},
			credFacts{Kind: "worker", Repos: lentWrite, Lent: true, Fallback: true}, decideAsk, "no GitHub App"},
		{"fallback approved", credRequest{Host: "github.com", Path: "owner/site.git", Access: "read"},
			credFacts{Kind: "worker", Repos: lentWrite, Lent: true, Fallback: true, Granted: func(string, string) bool { return true }}, decideAllow, "approved"},
		{"nothing lent", push, credFacts{Kind: "worker", Lent: false}, decideDeny, "no GitHub repository"},
	}
	for _, c := range cases {
		d := decideCredential(nil, c.req, c.facts)
		if d.Decision != c.want || !strings.Contains(d.Reason, c.reason) {
			t.Errorf("%s: %s (%s), want %s (%q)", c.name, d.Decision, d.Reason, c.want, c.reason)
		}
	}
}

// A task with a repo resource needs no GitHub tool; one from before
// resources still does.
func TestCredentialFromRepoResource(t *testing.T) {
	c, old := credCore(t, "")
	withApp(t, c, newFakeGitHub(t))
	ctx := context.Background()
	task := &TaskRow{ID: "t-2", Node: "pc", Agent: "sonnet", Runtime: "claude-code", Kind: "worker", Prompt: "fix it", State: stateRunning,
		Resources: []ResourceRef{{Kind: resRepo, Ref: "https://github.com/owner/repo.git", Access: accessWrite, Branch: "eriphe/fix"}}}
	c.store.insertTask(task)
	task, _ = c.store.task("t-2")
	if a := c.credential(ctx, task, credRequest{Host: "github.com", Path: "owner/repo.git", Access: "write", Tool: "git"}); a.Decision != decideAllow || a.Password == "" {
		t.Fatalf("push on its branch: %+v", a)
	}
	if a := c.credential(ctx, task, credRequest{Host: "github.com", Path: "owner/other.git", Access: "read"}); a.Decision != decideDeny {
		t.Fatalf("other repo: %+v", a)
	}
	// Started on a tainted turn: its writes ask.
	c.store.set(taskTaintKey(task.ID), "started on a tainted turn: mcp__mail__read")
	if a := c.credential(ctx, task, credRequest{Host: "github.com", Path: "owner/repo.git", Access: "write", Tool: "git"}); a.Decision != decideAsk {
		t.Fatalf("tainted: %+v", a)
	}
	// From before resources, without the GitHub tool: refused, as then.
	if a := c.credential(ctx, old, credRequest{Host: "github.com", Path: "owner/repo.git", Access: "read"}); a.Decision != decideDeny {
		t.Fatalf("old task without the tool: %+v", a)
	}
}

// A push elsewhere than the branch lent for write is the user's.
func TestUndeclaredPush(t *testing.T) {
	for cmd, want := range map[string]string{
		"git push":                                   "",
		"git push -u origin eriphe/fix":              "",
		"git push origin HEAD":                       "",
		"git push origin HEAD:refs/heads/eriphe/fix": "",
		"git -C /work/repo push origin other":        "other",
		"git add . && git push origin HEAD:other":    "other",
		"git push --tags":                            "--tags",
		"git push -o ci.skip origin eriphe/fix":      "",
		`git commit -m "push it" && git status`:      "",
		"git push origin +dev":                       "dev",
	} {
		got := ""
		for _, m := range gitPush.FindAllStringSubmatch(cmd, -1) {
			if got = pushTarget(strings.Fields(m[1]), "eriphe/fix"); got != "" {
				break
			}
		}
		if got != want {
			t.Errorf("%s: %q, want %q", cmd, got, want)
		}
	}

	c, _ := credCore(t, "")
	c.store.insertTask(&TaskRow{ID: "t-2", Node: "pc", Runtime: "claude-code", Kind: "worker", Prompt: "x", State: stateWaitingPermission,
		Resources: []ResourceRef{{Kind: resRepo, Ref: "owner/repo", Access: accessWrite, Branch: "eriphe/fix"}}})
	ev := func(seq int, pid, cmd string) []EventRow {
		return []EventRow{{Seq: seq, Time: time.Now(), Type: "permission", Data: map[string]any{"id": pid, "tool_name": "Bash", "input": map[string]any{"command": cmd}}}}
	}
	c.applyPolicy("t-2", ev(1, "p1", "git push origin eriphe/fix"))
	c.applyPolicy("t-2", ev(2, "p2", "git push origin release"))
	if c.store.userOnly("t-2", "p1") != "" || !strings.Contains(c.store.userOnly("t-2", "p2"), "release") {
		t.Fatalf("p1 %q, p2 %q", c.store.userOnly("t-2", "p1"), c.store.userOnly("t-2", "p2"))
	}
	// A task lent for read, or from before resources: the brain's, as before.
	c.store.insertTask(&TaskRow{ID: "t-3", Node: "pc", Runtime: "claude-code", Kind: "worker", Prompt: "x", State: stateWaitingPermission})
	c.applyPolicy("t-3", ev(1, "p1", "git push origin release"))
	if c.store.userOnly("t-3", "p1") != "" {
		t.Fatal("an old task's push went to the user")
	}
}

// On a tainted turn, run_task lends reads only, of every kind, and no
// legacy identity.
func TestTaintedResources(t *testing.T) {
	c := newCatalogCore(t)
	withDrive(t, c, DriveFolder{Name: "Factures", ID: "d1", Access: folderRead})
	c.conf().User = "Sam"
	c.conf().Identities = map[string]Identity{"github": {Env: "GH_TOKEN", File: "/nope"}}
	refused := []runRequest{
		{Identities: []string{"github"}},
		{Resources: []ResourceRef{{Kind: resRepo, Ref: "owner/repo", Access: accessWrite, Branch: "x"}}},
		{Resources: []ResourceRef{{Kind: resTool, Ref: "github.create_issue", Access: accessWrite}}},
		{Tools: []string{"github"}},
		{Tools: []string{"notes"}}, // read-only by its word, not believed
		{Resources: []ResourceRef{{Kind: resDriveFolder, Ref: "Factures"}}},
		{Resources: []ResourceRef{{Kind: resRepo, Ref: "someone/tool", TrustSkills: true}}},
	}
	allowed := []runRequest{
		{},
		{Repo: "owner/repo", Branch: "eriphe/x"}, // the old field: read, its pushes ask
		{Resources: []ResourceRef{{Kind: resRepo, Ref: "owner/repo"}}},
		{Resources: []ResourceRef{{Kind: resTool, Ref: "github.get_*"}}},
		{Tools: []string{"github.get_*"}},
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
	if err := c.taintedResources(refused[0]); !strings.Contains(err.Error(), "identities github") {
		t.Errorf("identities: %v", err)
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
		for _, want := range []string{`"resources"`, `"kind"`, `"access"`, "drive-folder", "legacy: use resources {kind: repo}", "legacy, do not use"} {
			if !strings.Contains(s, want) {
				t.Errorf("schema lacks %s: %s", want, s)
			}
		}
		return
	}
	t.Fatal("no run_task")
}
