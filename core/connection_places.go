package main

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
)

// The places of a connection whose provider mints tokens: what its
// accounts reach that the user gave to Eriphe, each with its own access.
// For the GitHub App's connections, the repositories of its installation
// the user picked: a task is lent only a repository given, at most at its
// access (resolveResources), and a write still asks on the start's card
// unless it is a clean start on a declared branch (decideRepo).
//
// They are browsed through the provider (tokenMinter.browse) and kept by
// eriphe-core, which enforces them: the connection is no server with a
// configuration of its own.

const connectionPlacesSchema = `CREATE TABLE IF NOT EXISTS connection_places (
  connection TEXT NOT NULL, id TEXT NOT NULL, name TEXT NOT NULL, access TEXT NOT NULL,
  PRIMARY KEY (connection, id));`

func init() { extraSchema = append(extraSchema, connectionPlacesSchema) }

// connectionPlaces are a connection's places, by name.
func (c *core) connectionPlaces(id string) []Place {
	rows, err := c.store.db.Query(`SELECT id, name, access FROM connection_places WHERE connection = ? ORDER BY name COLLATE NOCASE`, id)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []Place
	for rows.Next() {
		p := Place{Tool: id}
		if rows.Scan(&p.ID, &p.Name, &p.Access) == nil {
			out = append(out, p)
		}
	}
	return out
}

// giveConnectionPlace gives a place of a connection, or changes its
// access: read, or write.
func (c *core) giveConnectionPlace(id, placeID, access string) error {
	cn, ok := c.connectionByID(id)
	placeID = strings.TrimSpace(placeID)
	if !ok || c.minters[cn.Provider] == nil || placeID == "" || len(placeID) > 200 || (access != accessRead && access != accessWrite) {
		return &catalogError{http.StatusBadRequest, "conn.badplace", "", errors.New("a place needs a connection, an id and an access")}
	}
	_, err := c.store.db.Exec(`INSERT INTO connection_places (connection, id, name, access) VALUES (?, ?, ?, ?)
		ON CONFLICT (connection, id) DO UPDATE SET access = excluded.access`, id, placeID, placeID, access)
	return err
}

// removeConnectionPlace takes a place out of its connection.
func (c *core) removeConnectionPlace(id, placeID string) error {
	_, err := c.store.db.Exec(`DELETE FROM connection_places WHERE connection = ? AND id = ?`, id, placeID)
	return err
}

// dropConnectionPlaces forgets a connection's places, with it.
func (c *core) dropConnectionPlaces(id string) {
	c.store.db.Exec(`DELETE FROM connection_places WHERE connection = ?`, id)
}

// repoPlace is the place a repository (owner/name) is given as, on any
// connection; nil when it is not given.
func (c *core) repoPlace(repo string) *Place {
	var best *Place
	for _, cn := range c.connections() {
		if c.minters[cn.Provider] == nil {
			continue
		}
		for _, p := range c.connectionPlaces(cn.ID) {
			if strings.EqualFold(p.ID, repo) && (best == nil || accessRank[p.Access] > accessRank[best.Access]) {
				best = &p
			}
		}
	}
	return best
}

// repoPlaces are every repository given, for the brain.
func (c *core) repoPlaces() []Place {
	var out []Place
	for _, cn := range c.connections() {
		if c.minters[cn.Provider] != nil {
			out = append(out, c.connectionPlaces(cn.ID)...)
		}
	}
	slices.SortFunc(out, func(a, b Place) int { return strings.Compare(strings.ToLower(a.ID), strings.ToLower(b.ID)) })
	return slices.CompactFunc(out, func(a, b Place) bool { return strings.EqualFold(a.ID, b.ID) })
}

// browseConnection lists what a connection's account reaches, to pick
// places from.
func (c *core) browseConnection(ctx context.Context, id, page string) ([]placeItem, string, error) {
	cn, ok := c.connectionByID(id)
	if !ok || c.minters[cn.Provider] == nil {
		return nil, "", errors.New("no such connection")
	}
	return c.minters[cn.Provider].browse(ctx, cn.Identity, page)
}
