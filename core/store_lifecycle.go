package main

import (
	"encoding/json"
	"time"
)

// The declarative side of a task: its spec (without secrets) and what it
// should do, written by eriphe-core; and its inbox, the messages and
// permission answers waiting for its node.

const (
	desiredRunning = "running"
	desiredStopped = "stopped"
)

// assignment is a task as its node must see it.
type assignment struct {
	ID      string
	Kind    string // worker or brain
	Agent   string
	Idents  string // identity names, comma-separated
	Tools   string // catalog grant, comma-separated
	Folders string // the user's folders, by name, comma-separated
	// Resources is all it was lent; nil for a task from before (resources.go).
	Resources []ResourceRef
	Desired   string
	State     string
	Spec      nodeTaskSpec
}

func (s *Store) setSpec(id string, spec nodeTaskSpec) error {
	raw, _ := json.Marshal(spec)
	return s.updateTask(id, map[string]any{"spec": string(raw)})
}

// assignments returns a node's tasks that are not over yet.
func (s *Store) assignments(node string) ([]assignment, error) {
	rows, err := s.db.Query(`SELECT id, kind, agent, identity, tools, folders, resources, desired, state, spec FROM tasks
		WHERE node = ? AND node != '' AND state NOT IN ('stopped', 'failed', 'lost') ORDER BY created`, node)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []assignment
	for rows.Next() {
		var a assignment
		var spec, resources string
		if err := rows.Scan(&a.ID, &a.Kind, &a.Agent, &a.Idents, &a.Tools, &a.Folders, &resources, &a.Desired, &a.State, &spec); err != nil {
			return nil, err
		}
		a.Resources = unmarshalResources(resources)
		json.Unmarshal([]byte(spec), &a.Spec)
		out = append(out, a)
	}
	return out, rows.Err()
}

// unassigned returns the tasks waiting for a node, the oldest first.
func (s *Store) unassigned() ([]*TaskRow, error) {
	rows, err := s.db.Query(`SELECT ` + taskColumns + ` FROM tasks
		WHERE node = '' AND state = 'pending' AND desired = 'running' ORDER BY created`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*TaskRow
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// assign gives a waiting task its node, unless it moved meanwhile (stopped,
// or assigned by another pass): true if it did.
func (s *Store) assign(id, node string) (bool, error) {
	res, err := s.db.Exec(`UPDATE tasks SET node = ?, pending_reason = '', updated = ?
		WHERE id = ? AND node = '' AND state = 'pending' AND desired = 'running'`, node, time.Now().Unix(), id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// stranded returns the tasks given a node that has not started them, and
// not pinned to it: they may be placed again (placement.go).
func (s *Store) stranded() ([]*TaskRow, error) {
	rows, err := s.db.Query(`SELECT ` + taskColumns + ` FROM tasks
		WHERE node != '' AND state = 'pending' AND desired = 'running' AND pinned = 0 ORDER BY created`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*TaskRow
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// unassign takes a task not started yet from its node, to be placed
// again, unless it moved meanwhile: true if it did.
func (s *Store) unassign(id, node, reason string) (bool, error) {
	res, err := s.db.Exec(`UPDATE tasks SET node = '', pending_reason = ?, updated = ?
		WHERE id = ? AND node = ? AND state = 'pending' AND desired = 'running'`, reason, time.Now().Unix(), id, node)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// InboxItem is something a task's node must deliver to its agent.
type InboxItem struct {
	Seq  int            `json:"seq"`
	Kind string         `json:"kind"` // message or permission
	Data map[string]any `json:"data"`
}

func (s *Store) addInbox(taskID, kind string, data map[string]any) error {
	raw, _ := json.Marshal(data)
	_, err := s.db.Exec(`INSERT INTO inbox (task_id, kind, data, created) VALUES (?, ?, ?, ?)`,
		taskID, kind, string(raw), time.Now().Unix())
	return err
}

func (s *Store) inbox(taskID string) ([]InboxItem, error) {
	rows, err := s.db.Query(`SELECT seq, kind, data FROM inbox WHERE task_id = ? AND acked = 0 ORDER BY seq`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []InboxItem{}
	for rows.Next() {
		var it InboxItem
		var data string
		if err := rows.Scan(&it.Seq, &it.Kind, &data); err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(data), &it.Data)
		out = append(out, it)
	}
	return out, rows.Err()
}

func (s *Store) ackInbox(taskID string, upTo int) error {
	_, err := s.db.Exec(`UPDATE inbox SET acked = 1 WHERE task_id = ? AND seq <= ?`, taskID, upTo)
	return err
}

// answered tells whether a permission request already has an answer, sent
// or delivered: it must not be answered twice.
func (s *Store) answered(taskID, permissionID string) bool {
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM inbox WHERE task_id = ? AND kind = 'permission'
		AND json_extract(data, '$.id') = ?`, taskID, permissionID).Scan(&n)
	return n > 0
}
