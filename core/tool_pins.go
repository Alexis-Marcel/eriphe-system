package main

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
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
// or a new tool, is held: not lent, not shown, until the user accepts it,
// one change at a time in « Outils à revoir » (tool_review.go), or the
// server's tools at once on its page (approveTools). Each pin keeps the
// definition it was made of, for a change to show what was before.
// Eriphe's own servers are not pinned: those it provides make their tools
// in this process, and those shipped with it (shipped) change with its
// updates.

const toolPinsSchema = `CREATE TABLE IF NOT EXISTS tool_pins (
  server TEXT NOT NULL, tool TEXT NOT NULL, hash TEXT NOT NULL,
  def TEXT NOT NULL DEFAULT '', -- the definition pinned, JSON
  PRIMARY KEY (server, tool));`

func init() {
	extraSchema = append(extraSchema, toolPinsSchema)
	schemaFixes = append(schemaFixes, func(db *sql.DB) error {
		return addColumn(db, "tool_pins", "def", `TEXT NOT NULL DEFAULT ''`)
	})
}

// eripheImages is where Eriphe's own images are published: a server run
// from one of them (eriphe-mail, eriphe-calendar, eriphe-files,
// eriphe-signal) is shipped with Eriphe.
const eripheImages = "ghcr.io/alexis-marcel/eriphe-"

// shipped tells a server shipped with Eriphe: its definitions change with
// Eriphe's updates, which the user already chooses, so they are not pinned.
func (s Server) shipped() bool {
	if !s.local() {
		return false
	}
	spec, err := decodeRun(s.Run)
	return err == nil && strings.HasPrefix(strings.ToLower(spec.Image), eripheImages)
}

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

// toolPin is a pinned definition: its hash, and the tool as it was (nil
// for a pin made before definitions were kept).
type toolPin struct {
	Hash string
	Def  *mcp.Tool
}

// toolPinDefs are a server's pins, by tool name.
func (s *Store) toolPinDefs(server string) (map[string]toolPin, error) {
	rows, err := s.db.Query(`SELECT tool, hash, def FROM tool_pins WHERE server = ?`, server)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]toolPin{}
	for rows.Next() {
		var tool, hash, def string
		if err := rows.Scan(&tool, &hash, &def); err != nil {
			return nil, err
		}
		p := toolPin{Hash: hash}
		if def != "" {
			var t mcp.Tool
			if json.Unmarshal([]byte(def), &t) == nil {
				p.Def = &t
			}
		}
		out[tool] = p
	}
	return out, rows.Err()
}

// toolPins are a server's pinned definitions' hashes, by tool name.
func (s *Store) toolPins(server string) (map[string]string, error) {
	pins, err := s.toolPinDefs(server)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for name, p := range pins {
		out[name] = p.Hash
	}
	return out, nil
}

// pinTools pins the definitions of a server's tools, in place of its pins
// and of the changes refused of it.
func (s *Store) pinTools(server string, tools []*mcp.Tool) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range []string{`DELETE FROM tool_pins WHERE server = ?`, `DELETE FROM tool_refusals WHERE server = ?`} {
		if _, err := tx.Exec(q, server); err != nil {
			return err
		}
	}
	for _, t := range tools {
		if _, err := tx.Exec(`INSERT INTO tool_pins (server, tool, hash, def) VALUES (?, ?, ?, ?)`, server, t.Name, toolHash(t), toolDef(t)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// pinTool pins one tool's definition, in place of its pin.
func (s *Store) pinTool(server string, t *mcp.Tool) error {
	_, err := s.db.Exec(`INSERT INTO tool_pins (server, tool, hash, def) VALUES (?, ?, ?, ?)
		ON CONFLICT (server, tool) DO UPDATE SET hash = excluded.hash, def = excluded.def`, server, t.Name, toolHash(t), toolDef(t))
	return err
}

// unpinTool drops one tool's pin: a tool its server no longer lists.
func (s *Store) unpinTool(server, tool string) error {
	_, err := s.db.Exec(`DELETE FROM tool_pins WHERE server = ? AND tool = ?`, server, tool)
	return err
}

// toolDef is a tool's definition as a pin keeps it.
func toolDef(t *mcp.Tool) string {
	raw, err := json.Marshal(t)
	if err != nil {
		return ""
	}
	return string(raw)
}

// forgetPins drops a server's pins, and the changes refused of it: its
// next listing pins it again.
func (s *Store) forgetPins(server string) error {
	for _, q := range []string{`DELETE FROM tool_pins WHERE server = ?`, `DELETE FROM tool_refusals WHERE server = ?`} {
		if _, err := s.db.Exec(q, server); err != nil {
			return err
		}
	}
	return nil
}

// pinned splits the tools a server lists: those whose definition is the
// one pinned, and the names of those held (changed, or new). A server
// never pinned is pinned now. Should the pins not be read, every tool is
// held: nothing unchecked is lent.
func (p *pool) pinned(u *upstream, tools []*mcp.Tool) (kept, held []*mcp.Tool) {
	if p == nil || p.store == nil || u.provided != nil || u.srv.shipped() {
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
		log.Printf("server %s: tools changed since the user added it, held until accepted: %s", u.name, strings.Join(toolNames(held), ", "))
	}
	return kept, held
}

// errHeldTool refuses a call of a held tool, however it was asked.
var errHeldTool = errors.New("this tool changed since the user added its server: it is not used until they accept it in Eriphe")

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
// shown: those lent and those held, as last listed, refused ones too (the
// user's yes for the server as a whole, on its page). A tool the server
// changed again since stays held.
func (c *core) approveTools(name string) error {
	defer c.toolsMoved(name)
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

// listingSig fingerprints what a server lists, lent and held: when it
// changes, what waits for the user's review may have changed.
func listingSig(tools, held []*mcp.Tool) string {
	var b strings.Builder
	for _, t := range tools {
		b.WriteString("+" + toolHash(t))
	}
	for _, t := range held {
		b.WriteString("?" + toolHash(t))
	}
	return b.String()
}
