package main

import (
	"context"
	"errors"
	"maps"
	"net/http"
	"slices"
	"sort"
	"strings"
)

// The places of a connection whose provider mints tokens: for the GitHub
// App's, the repositories of its installation. Every one it reaches is
// given, one added on GitHub at once; what Eriphe may do there is one rule
// for the GitHub tool and for git: the tool's grid is every repository's
// default (Lire · Modifier · Supprimer · Envoyer), and a repository may set
// a case of its own, above or below it (the tool's place_grids, by
// owner/name, argPlaceAccess). A tool that only reads is its repositories'
// ceiling. In git: Modifier pushes to a branch, Supprimer deletes one,
// Envoyer pushes to the default branch or a tag (git_access.go).

// repoServers are the catalog's tools on a minting connection whose account
// is the repository's owner, by name: what decides for the repository.
func (c *core) repoServers(repo string) []string {
	owner, _, _ := strings.Cut(repo, "/")
	conf := c.conf()
	var out []string
	for _, name := range slices.Sorted(maps.Keys(conf.Servers)) {
		s := conf.Servers[name]
		if len(s.PlaceArgs) == 0 {
			continue
		}
		cn, ok := c.connectionByID(s.authConnection())
		if ok && c.minters[cn.Provider] != nil && strings.EqualFold(cn.Identity, owner) {
			out = append(out, name)
		}
	}
	return out
}

// connServers are the tools on a connection that name places by their
// arguments.
func (c *core) connServers(id string) []string {
	conf := c.conf()
	var out []string
	for _, name := range slices.Sorted(maps.Keys(conf.Servers)) {
		if s := conf.Servers[name]; len(s.PlaceArgs) > 0 && s.authConnection() == id {
			out = append(out, name)
		}
	}
	return out
}

// repoCase is a repository's value of a case: never when no tool reaches
// it.
func (c *core) repoCase(repo, category string) string {
	names := c.repoServers(repo)
	if len(names) == 0 {
		return gridNever
	}
	return c.conf().Servers[names[0]].argPlaceAccess(repo).grid.value(category)
}

// repoWriteCases are the cases a git or gh write may be about.
var repoWriteCases = []string{catChange, catDelete, catSend}

// repoMost is the most a repository may be lent: write when one of its
// cases above Lire is not never, read when none is, "" when it is not
// reached.
func (c *core) repoMost(repo string) string {
	if len(c.repoServers(repo)) == 0 {
		return ""
	}
	for _, cat := range repoWriteCases {
		if c.repoCase(repo, cat) != gridNever {
			return accessWrite
		}
	}
	return accessRead
}

// setRepoCase gives one case of a repository its own value on every tool of
// its connection; "" has it follow the tool again.
func (c *core) setRepoCase(id, repo, category, value string) error {
	repo = strings.ToLower(repoOf(repo))
	if repo == "" || !slices.Contains(categories, category) || value != "" && !slices.Contains(gridValues, value) {
		return errInvalid(errors.New("a case of a repository: owner/name, a category, free, approval, never or empty"))
	}
	names := c.connServers(id)
	if len(names) == 0 {
		return &catalogError{http.StatusBadRequest, "conn.badplace", "", errors.New("no tool on this connection")}
	}
	for _, name := range names {
		if c.source(kindServer, name) != sourceWorkspace {
			return errReadOnly(kindServer, name)
		}
		srv := c.conf().Servers[name]
		grids := maps.Clone(srv.PlaceGrids)
		if grids == nil {
			grids = map[string]accessGrid{}
		}
		grids[repo] = grids[repo].with(category, value)
		maps.DeleteFunc(grids, func(_ string, g accessGrid) bool { return g == (accessGrid{}) })
		if len(grids) == 0 {
			grids = nil
		}
		srv.PlaceGrids = grids
		if err := c.putServer(putUpdate, name, srv, nil); err != nil {
			return err
		}
	}
	return nil
}

// repoView is a repository for the brain: its name and its cases.
type repoView struct {
	Repo  string            `json:"repo"`
	Cases map[string]string `json:"cases"`
	Own   bool              `json:"own_rules,omitempty"` // it has cases of its own
}

// repoViews are the repositories the connections reach, the ones with
// cases of their own first, for the brain: a page of each installation.
func (c *core) repoViews(ctx context.Context) ([]repoView, []string) {
	var out []repoView
	var problems []string
	seen := map[string]bool{}
	for _, cn := range c.connections() {
		names := c.connServers(cn.ID)
		if c.minters[cn.Provider] == nil || len(names) == 0 {
			continue
		}
		srv := c.conf().Servers[names[0]]
		items, _, err := c.minters[cn.Provider].browse(ctx, cn.Identity, "")
		if err != nil {
			problems = append(problems, cn.Identity+": "+err.Error())
		}
		var repos []string
		for _, it := range items {
			repos = append(repos, it.ID)
		}
		for id := range srv.PlaceGrids {
			repos = append(repos, id)
		}
		for _, r := range repos {
			if seen[strings.ToLower(r)] {
				continue
			}
			seen[strings.ToLower(r)] = true
			a := srv.argPlaceAccess(r)
			v := repoView{Repo: r, Cases: map[string]string{}}
			_, v.Own = srv.PlaceGrids[strings.ToLower(r)]
			for _, cat := range categories {
				v.Cases[cat] = a.grid.value(cat)
			}
			out = append(out, v)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Own != out[j].Own {
			return out[i].Own
		}
		return strings.ToLower(out[i].Repo) < strings.ToLower(out[j].Repo)
	})
	return out, problems
}

// browseConnection lists what a connection's account reaches.
func (c *core) browseConnection(ctx context.Context, id, page string) ([]placeItem, string, error) {
	cn, ok := c.connectionByID(id)
	if !ok || c.minters[cn.Provider] == nil {
		return nil, "", errors.New("no such connection")
	}
	return c.minters[cn.Provider].browse(ctx, cn.Identity, page)
}
