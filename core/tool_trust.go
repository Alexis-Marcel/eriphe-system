package main

import (
	"log"
	"time"
)

// Trusted tools. One taint rule holds for the brain, its assistants and
// its tasks: a turn is tainted once it reads what comes neither from the
// user nor from Eriphe. A catalog tool taints what reads it unless the
// user marked it trusted, visibly, in Settings → Tools (the tool's access
// page, and a tag on its row): its results are then the user's own, as the
// user's notes are. None is trusted by default.
//
// The mark is per server, kept in the store (tool_trust) rather than in
// the catalog, so a tool of the configuration file is marked the same way.
// A row is a decision: trusted or not, by whom. The configuration's older
// memory.trusted_servers is migrated at start-up, each name once
// (INSERT OR IGNORE): a mark the user removed since is not put back, and a
// name added there later is still taken. A tool removed from the catalog
// keeps a row, untrusted, so a new tool of the same name starts untrusted.

const toolTrustSchema = `CREATE TABLE IF NOT EXISTS tool_trust (
	server  TEXT PRIMARY KEY,
	trusted INTEGER NOT NULL,
	by      TEXT NOT NULL,
	updated INTEGER NOT NULL
);`

func init() { extraSchema = append(extraSchema, toolTrustSchema) }

const (
	trustByUser   = "user"   // Settings → Tools
	trustByConfig = "config" // migrated from memory.trusted_servers
	trustByRemove = "remove" // the tool left the catalog
)

// migrateTrustedServers takes the configuration's memory.trusted_servers
// into the store, once per name.
func (c *core) migrateTrustedServers() {
	var m MemoryConfig
	if c.base.Memory != nil {
		m = *c.base.Memory
	}
	now := time.Now().Unix()
	for _, name := range m.TrustedServers {
		res, err := c.store.db.Exec(`INSERT OR IGNORE INTO tool_trust (server, trusted, by, updated) VALUES (?, 1, ?, ?)`, name, trustByConfig, now)
		if err != nil {
			log.Printf("trust: %s: %v", name, err)
			continue
		}
		if n, _ := res.RowsAffected(); n > 0 {
			log.Printf("trust: %s is trusted, from memory.trusted_servers; it is now changed in Settings → Tools", name)
		}
	}
}

// trustedServers is the set of the servers marked trusted.
func (c *core) trustedServers() map[string]bool {
	out := map[string]bool{}
	rows, err := c.store.db.Query(`SELECT server FROM tool_trust WHERE trusted = 1`)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var s string
		if rows.Scan(&s) == nil {
			out[s] = true
		}
	}
	return out
}

// trustsServer tells whether what server returns is the user's own.
func (c *core) trustsServer(server string) bool {
	var n int
	return server != "" && c.store.db.QueryRow(`SELECT COUNT(*) FROM tool_trust WHERE server = ? AND trusted = 1`, server).Scan(&n) == nil && n > 0
}

// setServerTrust marks a server trusted or not.
func (c *core) setServerTrust(server string, trusted bool, by string) error {
	v := 0
	if trusted {
		v = 1
	}
	_, err := c.store.db.Exec(`INSERT INTO tool_trust (server, trusted, by, updated) VALUES (?, ?, ?, ?)
		ON CONFLICT(server) DO UPDATE SET trusted = excluded.trusted, by = excluded.by, updated = excluded.updated`,
		server, v, by, time.Now().Unix())
	return err
}
