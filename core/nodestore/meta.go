package nodestore

import (
	"fmt"
	"slices"
	"time"

	"github.com/Alexis-Marcel/eriphe-system/core/nodefile"
)

// What code changes of a node's front matter, outside an author's body:
// done. It is a version of its own, made on the last
// one: it never conflicts with an author's write.

// SetDone marks a node done now, or not done any more: its front
// matter's done. It tells whether it changed: a node done already is not
// done again.
func (s *Store) SetDone(id string, done bool, author string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur, err := s.read(id, 0)
	if err != nil {
		return false, err
	}
	if done == !cur.Node.Meta.Done.IsZero() {
		return false, nil
	}
	at := time.Time{}
	if done {
		at = s.opts.Now().Truncate(time.Second)
	}
	if _, err := s.change(id, cur.Version.Hash, author, "done", func(n *nodefile.Node) error {
		n.Meta.Done = at
		return nil
	}); err != nil {
		return false, err
	}
	return true, nil
}

// Recent are the last changed nodes; archived ones only when asked, then
// with the others.
func (s *Store) Recent(archived bool, limit int) ([]Summary, error) {
	if limit <= 0 {
		limit = 20
	}
	cond := `WHERE n.archived = 0`
	if archived {
		cond = ``
	}
	return s.summaries(cond+` ORDER BY n.updated DESC, n.id LIMIT ?`, limit)
}

// Below are a node and every node under it, each once, the node first.
func (s *Store) Below(id string) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.below(id)
}

func (s *Store) below(id string) ([]string, error) {
	if _, err := s.summary(id); err != nil {
		return nil, err
	}
	// A parent's loop made by hand stops there.
	ids, seen := []string{id}, map[string]bool{id: true}
	for i := 0; i < len(ids); i++ {
		kids, err := s.children(ids[i])
		if err != nil {
			return nil, err
		}
		for _, k := range kids {
			if !seen[k.ID] {
				seen[k.ID] = true
				ids = append(ids, k.ID)
			}
		}
	}
	return ids, nil
}

// SetParent moves a node under parent ("" for the top): its front
// matter's parent, a version of its own made on the last one. Neither
// may be archived, and a node never goes under itself nor under a node
// below it. It tells whether it changed.
func (s *Store) SetParent(id, parent, author string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur, err := s.read(id, 0)
	if err != nil {
		return false, err
	}
	if cur.Node.Meta.Parent == parent {
		return false, nil
	}
	if parent != "" {
		p, err := s.summary(parent)
		if err != nil {
			return false, fmt.Errorf("parent %s: %w", parent, err)
		}
		if !p.Archived.IsZero() {
			return false, fmt.Errorf("parent %s: %w", parent, ErrArchived)
		}
		below, err := s.below(id)
		if err != nil {
			return false, err
		}
		if slices.Contains(below, parent) {
			return false, fmt.Errorf("%w: %s is %s or below it", ErrCycle, parent, id)
		}
	}
	if _, err := s.change(id, cur.Version.Hash, author, "parent", func(n *nodefile.Node) error {
		n.Meta.Parent = parent
		return nil
	}); err != nil {
		return false, err
	}
	return true, nil
}

// Backlinks are the nodes not archived that link a node: by a [[link]] of
// their text resolved to it, or a link code wrote; the oldest first.
func (s *Store) Backlinks(id string) ([]Summary, error) {
	return s.summaries(`WHERE n.archived = 0 AND n.id != ? AND n.id IN (SELECT l.id FROM node_links l WHERE l.target = ?
		UNION SELECT r.id FROM node_refs r WHERE r.target = ?) ORDER BY n.created, n.id`, id, id, id)
}

// Graph is the graph: every node not archived, the oldest first.
func (s *Store) Graph() ([]Summary, error) {
	return s.summaries(`WHERE n.archived = 0 ORDER BY n.created, n.id`)
}

// Incoming is how many live nodes link to id (by their text or their
// front matter) or sit under it: what makes a node heavy.
func (s *Store) Incoming(id string) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM nodes n WHERE n.archived = 0 AND n.id != ? AND (n.parent = ?
		OR n.id IN (SELECT l.id FROM node_links l WHERE l.target = ? UNION SELECT r.id FROM node_refs r WHERE r.target = ?))`,
		id, id, id, id).Scan(&n)
	return n, err
}

// neighbours adds to near the nodes id links to and its parent, as the
// index holds them now: their incoming count moves with id's links.
func neighbours(tx querier, id string, near map[string]bool) {
	rows, err := tx.Query(`SELECT parent FROM nodes WHERE id = ? AND parent != ''
		UNION SELECT target FROM node_links WHERE id = ?
		UNION SELECT target FROM node_refs WHERE id = ? AND target != '' AND target != ?`, id, id, id, id)
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var t string
		if rows.Scan(&t) == nil {
			near[t] = true
		}
	}
}
