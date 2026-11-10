package main

import (
	"net/url"
	"strings"
	"testing"
)

// giveRepos gives repositories to Eriphe for write, as the user does on
// the places of the GitHub App's connection.
func giveRepos(t *testing.T, c *core, repos ...string) {
	t.Helper()
	cn, err := c.keepMinted(githubProvider, "owner")
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range repos {
		if err := c.giveConnectionPlace(cn.ID, r, accessWrite); err != nil {
			t.Fatal(err)
		}
	}
}

// A task is lent only a repository given to Eriphe, at most at its
// access; one taken back is lent no more.
func TestLendOnlyAGivenRepository(t *testing.T) {
	c := newDynamicCore(t, nil).core
	lend := func(access string) error {
		_, err := c.resolveRequest(runRequest{Resources: []ResourceRef{{Kind: resRepo, Ref: "acme/site", Access: access}}})
		return err
	}
	if err := lend(accessRead); err == nil || !strings.Contains(err.Error(), "not given to Eriphe") {
		t.Fatalf("not given: %v", err)
	}
	cn, _ := c.keepMinted(githubProvider, "acme")
	if err := c.giveConnectionPlace(cn.ID, "acme/site", accessRead); err != nil {
		t.Fatal(err)
	}
	if err := lend(accessRead); err != nil {
		t.Fatalf("read: %v", err)
	}
	if err := lend(accessWrite); err == nil || !strings.Contains(err.Error(), "for read only") {
		t.Fatalf("write on a read one: %v", err)
	}
	c.giveConnectionPlace(cn.ID, "acme/site", accessWrite)
	if err := lend(accessWrite); err != nil {
		t.Fatalf("write: %v", err)
	}
	if p := c.repoPlaces(); len(p) != 1 || p[0].ID != "acme/site" || p[0].Access != accessWrite {
		t.Fatalf("list: %+v", p)
	}
	if err := c.giveConnectionPlace(cn.ID, "acme/site", accessDrop); err == nil {
		t.Fatal("a repository given drop")
	}
	c.dropConnection(t.Context(), cn)
	if err := lend(accessRead); err == nil {
		t.Fatal("lent after its connection went")
	}
}

// The repositories of an installation are picked on its connection's
// places page, read or write, and taken back there.
func TestConnectionPlacesPage(t *testing.T) {
	cc := newDynamicCore(t, nil)
	f := newFakeGitHub(t)
	f.org = true
	withApp(t, cc.core, f)
	cn, _ := cc.keepMinted(githubProvider, "acme")
	tw := newTestWebFor(t, cc.core)
	csrf := tw.setup()
	base := "/settings/connections/" + cn.ID + "/places"
	if code, body, _ := tw.do("GET", base, nil, nil); code != 200 || !strings.Contains(body, "GitHub · acme") || !strings.Contains(body, base+"/browse") {
		t.Fatalf("page: %d %s", code, body)
	}
	_, body, _ := tw.do("GET", base+"/browse", nil, nil)
	if !strings.Contains(body, "acme/repo-3") || strings.Contains(body, "Ouvrir") || !strings.Contains(body, `value="write"`) {
		t.Fatalf("browse: %s", body)
	}
	if code, body, h := tw.post(base, url.Values{"id": {"acme/repo-2"}, "access": {"write"}}, csrf); !strings.Contains(body, "acme/repo-2") || !strings.Contains(body, "Lecture &#43; écriture") {
		t.Fatalf("given: %d %v %s", code, h, body)
	}
	if p := cc.repoPlace("ACME/repo-2"); p == nil || p.Access != accessWrite {
		t.Fatalf("place: %+v", p)
	}
	tw.post(base+"/remove", url.Values{"id": {"acme/repo-2"}}, csrf)
	if cc.repoPlace("acme/repo-2") != nil {
		t.Fatal("still given")
	}
	if code, _, _ := tw.do("GET", "/settings/connections/0123456789ab/places", nil, nil); code != 404 {
		t.Fatalf("no connection: %d", code)
	}
}
