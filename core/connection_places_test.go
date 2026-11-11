package main

import (
	"net/url"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// giveRepos has Eriphe reach repositories, as the GitHub App's connection on
// their owner does, through a GitHub tool whose Modifier is free (a clean
// start pushes to a branch with no card) and the rest on approval.
func giveRepos(t *testing.T, c *core, repos ...string) {
	t.Helper()
	g := accessGrid{Read: gridFree, Change: gridFree, Delete: gridApproval, Send: gridApproval}
	for _, r := range repos {
		owner, _, _ := strings.Cut(r, "/")
		repoTool(t, c, owner, g)
	}
}

// repoTool is a GitHub tool on the app's connection of owner, at grid.
func repoTool(t *testing.T, c *core, owner string, g accessGrid) (connection, string) {
	t.Helper()
	cn, err := c.keepMinted(githubProvider, owner)
	if err != nil {
		t.Fatal(err)
	}
	name := "gh-" + owner
	srv := Server{URL: "https://api.githubcopilot.com/mcp/", Auth: &ServerAuth{Connection: cn.ID}, Use: "github",
		PlaceArgs: repoPlaceArgs, Grid: &g}
	if err := c.putServer(putUpsert, name, srv, nil); err != nil {
		t.Fatal(err)
	}
	return cn, name
}

// A task is lent a repository its owner's connection reaches, at most as its
// cases allow: write once one case above Lire is not never.
func TestLendOnlyAReachedRepository(t *testing.T) {
	c := newDynamicCore(t, nil).core
	lend := func(access string) error {
		_, err := c.resolveRequest(runRequest{Resources: []ResourceRef{{Kind: resRepo, Ref: "acme/site", Access: access}}})
		return err
	}
	if err := lend(accessRead); err == nil || !strings.Contains(err.Error(), "not reached by Eriphe") {
		t.Fatalf("not reached: %v", err)
	}
	cn, _ := repoTool(t, c, "acme", levelGrid(accessReadOnly))
	if err := lend(accessRead); err != nil {
		t.Fatalf("read: %v", err)
	}
	if err := lend(accessWrite); err == nil || !strings.Contains(err.Error(), "for read only") {
		t.Fatalf("write under a read-only tool: %v", err)
	}
	// The tool only reads: a repository cannot go above it.
	c.setRepoCase(cn.ID, "acme/site", catChange, gridFree)
	if err := lend(accessWrite); err == nil {
		t.Fatal("a repository above its read-only tool")
	}
	repoTool(t, c, "acme", levelGrid(accessReads))
	if err := lend(accessWrite); err != nil {
		t.Fatalf("write: %v", err)
	}
	// Below it: Modifier, Supprimer and Envoyer never, the repository reads only.
	for _, cat := range repoWriteCases {
		if err := c.setRepoCase(cn.ID, "ACME/site", cat, gridNever); err != nil {
			t.Fatal(err)
		}
	}
	if err := lend(accessWrite); err == nil || c.repoMost("acme/site") != accessRead || c.repoMost("acme/other") != accessWrite {
		t.Fatalf("a repository below its tool: %v", err)
	}
	if err := c.setRepoCase(cn.ID, "nope", catChange, gridFree); err == nil {
		t.Fatal("a case of no repository")
	}
	c.dropConnection(t.Context(), cn)
	if err := lend(accessRead); err == nil {
		t.Fatal("lent after its connection went")
	}
}

// One rule for the GitHub tool and git: a call naming a repository is held
// to that repository's case; the tool's grid holds for the others.
func TestRepoCaseForTheTool(t *testing.T) {
	c := newDynamicCore(t, nil).core
	cn, name := repoTool(t, c, "acme", levelGrid(accessReads))
	c.setRepoCase(cn.ID, "acme/site", catChange, gridNever)
	c.setRepoCase(cn.ID, "acme/site", catSend, gridFree)
	srv := c.conf().Servers[name]
	pr := &mcp.Tool{Name: "create_pull_request", Annotations: &mcp.ToolAnnotations{DestructiveHint: ptrTo(false), OpenWorldHint: ptrTo(false)}}
	merge := &mcp.Tool{Name: "merge_pull_request", Annotations: &mcp.ToolAnnotations{}}
	for _, k := range []struct {
		tool *mcp.Tool
		repo string
		want string
	}{{pr, "site", gridNever}, {pr, "other", gridApproval}, {merge, "Site", gridFree}, {merge, "other", gridApproval}} {
		if f := srv.toolFacts(k.tool, map[string]any{"owner": "acme", "repo": k.repo}); f.value != k.want {
			t.Errorf("%s on %s: %+v, want %s", k.tool.Name, k.repo, f, k.want)
		}
	}
	if c.repoCase("acme/site", catChange) != gridNever || c.repoCase("acme/site", catSend) != gridFree || c.repoCase("other/x", catRead) != gridNever {
		t.Error("the repository's cases for git")
	}
}

// Every repository of the installation is listed, the ones with cases of
// their own first, each case set there.
func TestConnectionPlacesPage(t *testing.T) {
	cc := newDynamicCore(t, nil)
	f := newFakeGitHub(t)
	f.org = true
	withApp(t, cc.core, f)
	cn, _ := repoTool(t, cc.core, "acme", levelGrid(accessReads))
	tw := newTestWebFor(t, cc.core)
	csrf := tw.setup()
	base := "/settings/connections/" + cn.ID + "/places"
	if code, body, _ := tw.do("GET", base, nil, nil); code != 200 || !strings.Contains(body, "GitHub · acme") || !strings.Contains(body, "acme/repo-3") ||
		!strings.Contains(body, "Comme l&#39;outil") || !strings.Contains(body, base+"/grid?id=acme%2Frepo-3") {
		t.Fatalf("page: %d %s", code, body)
	}
	if code, body, _ := tw.do("GET", base+"/grid?id=acme/repo-3", nil, nil); code != 200 || !strings.Contains(body, `name="case-send" value=""`) ||
		!strings.Contains(body, "Envoyer : Comme l&#39;outil (Sur accord)") {
		t.Fatalf("drawer: %d %s", code, body)
	}
	_, body, _ := tw.post(base+"/case", url.Values{"id": {"acme/repo-3"}, "case-read": {""}, "case-send": {gridNever}}, csrf)
	if strings.Contains(body, `role="alert"`) || !strings.Contains(body, "Envoyer : Jamais") {
		t.Fatalf("case: %s", body)
	}
	if cc.repoCase("acme/repo-3", catSend) != gridNever || cc.repoCase("acme/repo-3", catRead) != gridFree {
		t.Fatal("case not kept")
	}
	if _, body, _ := tw.do("GET", base, nil, nil); strings.Index(body, "acme/repo-3") > strings.Index(body, "acme/repo-1") {
		t.Fatalf("its own rules first: %s", body)
	}
	if code, _, _ := tw.do("GET", "/settings/connections/0123456789ab/places", nil, nil); code != 404 {
		t.Fatalf("no connection: %d", code)
	}
}

// The repositories given before every one was (the NAS, once): one given to
// read keeps to reading, the table goes.
func TestLegacyRepoPlaces(t *testing.T) {
	cc := newDynamicCore(t, nil)
	cn, err := cc.keepMinted(githubProvider, "acme")
	if err != nil {
		t.Fatal(err)
	}
	srv := Server{URL: "https://api.githubcopilot.com/mcp/", Auth: &ServerAuth{Connection: cn.ID}, Use: "github"}
	cc.store.putCatalog(kindServer, "github", srv)
	cc.store.db.Exec(`CREATE TABLE connection_places (connection TEXT, id TEXT, name TEXT, access TEXT)`)
	cc.store.db.Exec(`INSERT INTO connection_places VALUES (?, 'acme/Site', 'acme/Site', 'read'), (?, 'acme/app', 'acme/app', 'write')`, cn.ID, cn.ID)
	legacyRepoPlaces(cc.store)
	d, _ := loadCatalog(&Config{}, cc.store)
	got := d.Servers["github"]
	if len(got.PlaceArgs) != 2 || len(got.PlaceGrids) != 1 || got.PlaceGrids["acme/site"] != (accessGrid{Change: gridNever, Delete: gridNever, Send: gridNever}) {
		t.Fatalf("migrated: %+v", got)
	}
	if _, err := cc.store.db.Exec(`SELECT 1 FROM connection_places`); err == nil {
		t.Fatal("the old table stays")
	}
}
