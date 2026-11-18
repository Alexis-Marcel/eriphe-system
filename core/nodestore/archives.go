package nodestore

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// Archives are what the user archived, the latest first: each node
// archived whose parent was not archived with it (a node archived alone,
// or the top of a cascade), and how many nodes were archived with it.
func (s *Store) Archives(limit int) ([]Archived, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if limit <= 0 {
		limit = 100
	}
	xs, err := s.summaries(`LEFT JOIN nodes p ON p.id = n.parent
		WHERE n.archived != 0 AND (p.id IS NULL OR p.archived != n.archived)
		ORDER BY n.archived DESC, n.id LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	out := make([]Archived, 0, len(xs))
	for _, x := range xs {
		with, err := s.archivedWith(x.ID, x.Archived)
		if err != nil {
			return nil, err
		}
		out = append(out, Archived{Summary: x, With: len(with) - 1})
	}
	return out, nil
}

// Archived is a node the user archived, with how many nodes below it were
// archived with it.
type Archived struct {
	Summary
	With int
}

// archivedWith are a node and the nodes below it archived at the same
// time: what one archive did.
func (s *Store) archivedWith(id string, at time.Time) ([]string, error) {
	ids, err := s.below(id)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, nid := range ids {
		x, err := s.summary(nid)
		if err != nil {
			return nil, err
		}
		if x.Archived.Equal(at) {
			out = append(out, nid)
		}
	}
	return out, nil
}

// Reopen brings an archived node back to the graph, with what was archived
// with it and the archived nodes above it (so that it has its place), in
// one commit. A node not archived is left as it is.
func (s *Store) Reopen(id, author string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if strings.TrimSpace(author) == "" {
		return errors.New("a write has an author")
	}
	x, err := s.summary(id)
	if err != nil {
		return err
	}
	if x.Archived.IsZero() {
		return nil
	}
	ids, err := s.archivedWith(id, x.Archived)
	if err != nil {
		return err
	}
	for p := x.Parent; p != ""; {
		px, err := s.summary(p)
		if err != nil || px.Archived.IsZero() || containsID(ids, p) {
			break
		}
		ids = append(ids, p)
		p = px.Parent
	}
	now := s.opts.Now().Truncate(time.Second)
	var changes []change
	var stamps []stamp
	var xs []indexed
	for _, nid := range ids {
		cur, err := s.read(nid, 0)
		if err != nil {
			return err
		}
		cur.Node.Meta.Archived = time.Time{}
		raw, err := cur.Node.Bytes()
		if err != nil {
			return err
		}
		changes = append(changes, change{path: cur.File, data: raw})
		stamps = append(stamps, stamp{nid, cur.Last + 1})
		xs = append(xs, indexed{cur.Node, cur.File, raw, Version{N: cur.Last + 1, Author: author, At: now}})
	}
	hash, err := s.commit(fmt.Sprintf("reopen %s", id), author, now, changes, stamps)
	if err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	for i := range xs {
		xs[i].v.Hash = hash
	}
	if err := s.indexVersions(now, xs); err != nil {
		return err
	}
	for _, x := range xs {
		s.changed(ChangedDoc, x.node.Meta.ID)
	}
	return nil
}

func containsID(ids []string, id string) bool {
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}

// Place is where the user dropped a node on the graph: a root's spot, a
// child's offset from its parent.
type Place struct {
	X, Y float64
}

// Places are the nodes the user placed, by id.
func (s *Store) Places() (map[string]Place, error) {
	rows, err := s.db.Query(`SELECT l.id, l.x, l.y FROM node_layout l JOIN nodes n ON n.id = l.id WHERE l.pinned = 1 AND n.archived = 0`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]Place{}
	for rows.Next() {
		var id string
		var p Place
		if err := rows.Scan(&id, &p.X, &p.Y); err != nil {
			return nil, err
		}
		out[id] = p
	}
	return out, rows.Err()
}

// SetPlace keeps where the user dropped a node. It is no version of the
// node: the graph's layout is the index's alone.
func (s *Store) SetPlace(id string, p Place) error {
	if _, err := s.summary(id); err != nil {
		return err
	}
	_, err := s.db.Exec(`INSERT INTO node_layout (id, x, y, pinned) VALUES (?, ?, ?, 1)
		ON CONFLICT(id) DO UPDATE SET x = excluded.x, y = excluded.y, pinned = 1`, id, p.X, p.Y)
	return err
}

// Unplace forgets where a node was dropped: the layout places it again.
func (s *Store) Unplace(id string) error {
	_, err := s.db.Exec(`DELETE FROM node_layout WHERE id = ?`, id)
	return err
}
