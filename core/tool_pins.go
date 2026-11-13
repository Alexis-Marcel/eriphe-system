package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"slices"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Pinned tool definitions. What a catalog server says of a tool (its
// description, its schemas, its annotations) is what the agents read and
// what the grid sorts it by: a server that changes it after the user added
// it could hide instructions in a description, or turn a send into a read.
// The first listing of a server pins each tool's definition, adding the
// server being the user's yes; a tool listed later with another definition,
// or a new tool, is held: not lent, not shown, until the user accepts the
// server's tools again on its page (approveTools). Eriphe's own provided
// servers are not pinned: their tools are made in this process.

const toolPinsSchema = `CREATE TABLE IF NOT EXISTS tool_pins (
  server TEXT NOT NULL, tool TEXT NOT NULL, hash TEXT NOT NULL,
  PRIMARY KEY (server, tool));`

func init() { extraSchema = append(extraSchema, toolPinsSchema) }

// toolHash fingerprints what a server says of a tool.
func toolHash(t *mcp.Tool) string {
	raw, _ := json.Marshal(struct {
		Name, Title, Description string
		Annotations              *mcp.ToolAnnotations
		InputSchema              any
		OutputSchema             any
	}{t.Name, t.Title, t.Description, t.Annotations, t.InputSchema, t.OutputSchema})
	h := sha256.Sum256(raw)
	return hex.EncodeToString(h[:])
}

// toolPins are a server's pinned definitions, by tool name.
func (s *Store) toolPins(server string) (map[string]string, error) {
	rows, err := s.db.Query(`SELECT tool, hash FROM tool_pins WHERE server = ?`, server)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var tool, hash string
		if err := rows.Scan(&tool, &hash); err != nil {
			return nil, err
		}
		out[tool] = hash
	}
	return out, rows.Err()
}

// pinTools pins the definitions of a server's tools, in place of its pins.
func (s *Store) pinTools(server string, tools []*mcp.Tool) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM tool_pins WHERE server = ?`, server); err != nil {
		return err
	}
	for _, t := range tools {
		if _, err := tx.Exec(`INSERT INTO tool_pins (server, tool, hash) VALUES (?, ?, ?)`, server, t.Name, toolHash(t)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// forgetPins drops a server's pins: its next listing pins it again.
func (s *Store) forgetPins(server string) error {
	_, err := s.db.Exec(`DELETE FROM tool_pins WHERE server = ?`, server)
	return err
}

// pinned splits the tools a server lists: those whose definition is the
// one pinned, and the names of those held (changed, or new). A server
// never pinned is pinned now. Should the pins not be read, every tool is
// held: nothing unchecked is lent.
func (p *pool) pinned(u *upstream, tools []*mcp.Tool) (kept, held []*mcp.Tool) {
	if p == nil || p.store == nil || u.provided != nil {
		return tools, nil
	}
	pins, err := p.store.toolPins(u.name)
	if err == nil && len(pins) == 0 {
		err = p.store.pinTools(u.name, tools)
		if err == nil {
			return tools, nil
		}
	}
	if err != nil {
		log.Printf("server %s: tool pins: %v", u.name, err)
	}
	for _, t := range tools {
		if h, ok := pins[t.Name]; ok && err == nil && h == toolHash(t) {
			kept = append(kept, t)
		} else {
			held = append(held, t)
		}
	}
	if len(held) > 0 {
		names := toolNames(held)
		if !slices.Equal(names, u.heldTools()) && p.toolsHeld != nil {
			go p.toolsHeld(u.name, len(held))
		}
		log.Printf("server %s: tools changed since the user added it, held until accepted: %s", u.name, strings.Join(names, ", "))
	}
	return kept, held
}

// errHeldTool refuses a call of a held tool, however it was asked.
var errHeldTool = errors.New("this tool changed since the user added its server: it is not used until they accept it on the tool's page in Eriphe")

// heldTools are the tools of a server held for a definition the user has
// not accepted.
func (u *upstream) heldTools() []string {
	u.mu.Lock()
	defer u.mu.Unlock()
	return toolNames(u.unaccepted)
}

// heldDefs are the definitions of the held tools, as last listed.
func (u *upstream) heldDefs() []*mcp.Tool {
	u.mu.Lock()
	defer u.mu.Unlock()
	return slices.Clone(u.unaccepted)
}

func toolNames(tools []*mcp.Tool) []string {
	var out []string
	for _, t := range tools {
		out = append(out, t.Name)
	}
	return out
}

// approveTools accepts the definitions of a server's tools the user was
// shown: those lent and those held, as last listed. A tool the server
// changed again since stays held.
func (c *core) approveTools(name string) error {
	u := c.pool.get(name)
	if u == nil {
		return c.store.forgetPins(name)
	}
	_, _, tools := u.snapshot()
	if err := c.store.pinTools(name, append(slices.Clone(tools), u.heldDefs()...)); err != nil {
		return err
	}
	u.refreshTools()
	return nil
}

// toolsHeld tells the brain that a server's tools are held, for it to tell
// the user: their names are the server's, so only their count is said.
func (c *core) toolsHeld(server string, n int) {
	if c.conf().brain() == nil {
		return
	}
	c.store.addMessage(roleEvent, fmt.Sprintf("Catalogue : %d outil(s) de « %s » ont changé ou sont apparus depuis que l'utilisateur l'a ajouté. Eriphe les retient : ils ne sont pas utilisés tant qu'il ne les accepte pas sur la page de l'outil (Réglages → Outils → %s). Dis-le-lui.", n, server, server), false)
	c.ping()
}
