package nodestore

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/Alexis-Marcel/eriphe-system/core/nodefile"
)

// What code changes of a node's front matter, outside an author's body:
// done, and its links. Each is a version of its own, made on the last
// one: it never conflicts with an author's write.

// SetDone marks a node done now, or not done any more: its front
// matter's done, and its state (done, else idle). It tells whether it
// changed: a node done already is not done again.
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
	state := Idle
	if done {
		state = Done
	}
	_, err = s.db.Exec(`UPDATE nodes SET state = ? WHERE id = ?`, state, id)
	return true, err
}

// Link links a node to another: to joins its links. It tells whether it
// changed: a link there already is not added again.
func (s *Store) Link(id, to, author string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	to = strings.TrimSpace(to)
	if to == id {
		return false, errors.New("a node is not linked to itself")
	}
	if _, err := s.summary(to); err != nil {
		return false, fmt.Errorf("link %s: %w", to, err)
	}
	cur, err := s.read(id, 0)
	if err != nil {
		return false, err
	}
	if slices.Contains(cur.Node.Meta.Links, to) {
		return false, nil
	}
	_, err = s.change(id, cur.Version.Hash, author, "link", func(n *nodefile.Node) error {
		n.Meta.Links = append(n.Meta.Links, to)
		return nil
	})
	return err == nil, err
}

// InStates are the nodes in one of states, archived ones left out, the
// last changed first.
func (s *Store) InStates(states []string, limit int) ([]Summary, error) {
	if len(states) == 0 {
		return nil, nil
	}
	if limit <= 0 {
		limit = 20
	}
	args := make([]any, 0, len(states)+1)
	for _, st := range states {
		args = append(args, st)
	}
	marks := strings.TrimSuffix(strings.Repeat("?, ", len(states)), ", ")
	return s.summaries(`WHERE n.archived = 0 AND n.state IN (`+marks+`) ORDER BY n.updated DESC, n.id LIMIT ?`, append(args, limit)...)
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
