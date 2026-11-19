package nodestore

import (
	"database/sql"
	"errors"

	"github.com/Alexis-Marcel/eriphe-system/core/nodefile"
)

// A node's text links other nodes by their title, [[Title]] (nodefile's
// WikiLinks). The index resolves each name to a node once and keeps it:
// the link holds when that node's title changes, as its id is kept, not
// its title. A name no node has waits ('') until a node gets it. The
// index is the only place that keeps what a name was resolved to: one
// rebuilt from the files resolves the names again by the titles of the
// day, so a link written with a title since changed then waits.
//
// A node's links (Summary.Links, the graph's edges) are those of its
// text, resolved, and those code wrote in its front matter.

// indexRefs writes a node's text links: each keeps the node it was
// resolved to while that node is there; a new one waits.
func indexRefs(tx querier, n *nodefile.Node) error {
	id := n.Meta.ID
	prev := map[string]string{}
	rows, err := tx.Query(`SELECT r.key, r.target FROM node_refs r JOIN nodes n ON n.id = r.target WHERE r.id = ?`, id)
	if err != nil {
		return err
	}
	for rows.Next() {
		var k, t string
		if err := rows.Scan(&k, &t); err != nil {
			rows.Close()
			return err
		}
		prev[k] = t
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM node_refs WHERE id = ?`, id); err != nil {
		return err
	}
	for _, l := range n.WikiLinks() {
		k := nodefile.LinkKey(l.Name)
		t := prev[k]
		if t == id {
			t = ""
		}
		if _, err := tx.Exec(`INSERT INTO node_refs (id, key, target) VALUES (?, ?, ?)`, id, k, t); err != nil {
			return err
		}
	}
	return nil
}

// resolveRefs resolves the links that wait to the node of their name, if
// one has it now. It gives the nodes whose links it resolved.
func resolveRefs(tx querier) ([]string, error) {
	type ref struct{ id, key string }
	var waiting []ref
	rows, err := tx.Query(`SELECT r.id, r.key FROM node_refs r
		WHERE r.target = '' AND EXISTS (SELECT 1 FROM nodes n WHERE n.title_key = r.key AND n.id != r.id) ORDER BY r.id, r.key`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var r ref
		if err := rows.Scan(&r.id, &r.key); err != nil {
			rows.Close()
			return nil, err
		}
		waiting = append(waiting, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var changed []string
	for _, r := range waiting {
		t, err := byTitleKey(tx, r.key, r.id)
		if err != nil {
			return changed, err
		}
		if t == "" {
			continue
		}
		if _, err := tx.Exec(`UPDATE node_refs SET target = ? WHERE id = ? AND key = ?`, t, r.id, r.key); err != nil {
			return changed, err
		}
		if len(changed) == 0 || changed[len(changed)-1] != r.id {
			changed = append(changed, r.id)
		}
	}
	return changed, nil
}

// byTitleKey is the node whose title has key, but not: one not archived
// first, then the oldest. "" when none.
func byTitleKey(tx querier, key, not string) (string, error) {
	var id string
	err := tx.QueryRow(`SELECT id FROM nodes WHERE title_key = ? AND id != ? ORDER BY archived != 0, created, id LIMIT 1`, key, not).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return id, err
}

// Resolve tells which node each of names, links of node id's text, opens:
// the one the index resolved it to, else the node of that title now (a
// link of an older version that the last one no longer has). A name no
// node has is left out.
func (s *Store) Resolve(id string, names []string) (map[string]Summary, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := map[string]Summary{}
	for _, name := range names {
		k := nodefile.LinkKey(name)
		var t string
		err := s.db.QueryRow(`SELECT r.target FROM node_refs r JOIN nodes n ON n.id = r.target WHERE r.id = ? AND r.key = ?`, id, k).Scan(&t)
		if errors.Is(err, sql.ErrNoRows) {
			t, err = byTitleKey(s.db, k, id)
		}
		if err != nil {
			return nil, err
		}
		if t == "" {
			continue
		}
		sum, err := s.summary(t)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out[name] = sum
	}
	return out, nil
}
