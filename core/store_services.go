package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// The services table: what each local MCP server should be (its spec,
// without secrets), where it runs, and what its node last said.

func init() {
	extraSchema = append(extraSchema, `
CREATE TABLE IF NOT EXISTS services (
  name TEXT PRIMARY KEY, spec TEXT NOT NULL, hash TEXT NOT NULL, node TEXT NOT NULL DEFAULT '',
  desired TEXT NOT NULL DEFAULT 'running', state TEXT NOT NULL DEFAULT 'pending',
  restarts INTEGER NOT NULL DEFAULT 0, error TEXT NOT NULL DEFAULT '',
  pending_reason TEXT NOT NULL DEFAULT '', updated INTEGER NOT NULL);`)
}

type ServiceRow struct {
	Name          string    `json:"name"`
	Spec          RunSpec   `json:"spec"`
	Hash          string    `json:"hash"`
	Node          string    `json:"node"` // empty while it waits for one
	Desired       string    `json:"desired"`
	State         string    `json:"state"` // pending, starting, running, failed, stopped
	Restarts      int       `json:"restarts"`
	Error         string    `json:"error,omitempty"`
	PendingReason string    `json:"pending_reason,omitempty"`
	Updated       time.Time `json:"updated"`
}

const serviceColumns = `name, spec, hash, node, desired, state, restarts, error, pending_reason, updated`

func scanService(sc interface{ Scan(...any) error }) (*ServiceRow, error) {
	var r ServiceRow
	var spec string
	var updated int64
	if err := sc.Scan(&r.Name, &spec, &r.Hash, &r.Node, &r.Desired, &r.State, &r.Restarts, &r.Error,
		&r.PendingReason, &updated); err != nil {
		return nil, err
	}
	json.Unmarshal([]byte(spec), &r.Spec)
	r.Updated = time.Unix(updated, 0)
	return &r, nil
}

func (s *Store) services() ([]*ServiceRow, error) {
	rows, err := s.db.Query(`SELECT ` + serviceColumns + ` FROM services ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*ServiceRow
	for rows.Next() {
		r, err := scanService(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

var errNoService = errors.New("no such service")

func (s *Store) service(name string) (*ServiceRow, error) {
	r, err := scanService(s.db.QueryRow(`SELECT `+serviceColumns+` FROM services WHERE name = ?`, name))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errNoService
	}
	return r, err
}

// putService writes a service's spec; a new or changed one starts over
// (pending until its node runs it), on the same node.
func (s *Store) putService(name string, spec RunSpec) error {
	raw, _ := json.Marshal(spec)
	_, err := s.db.Exec(`INSERT INTO services (name, spec, hash, updated) VALUES (?, ?, ?, ?)
		ON CONFLICT(name) DO UPDATE SET spec = excluded.spec, hash = excluded.hash, desired = 'running',
		state = 'pending', restarts = 0, error = '', updated = excluded.updated`,
		name, string(raw), spec.hash(), time.Now().Unix())
	return err
}

func (s *Store) deleteService(name string) error {
	_, err := s.db.Exec(`DELETE FROM services WHERE name = ?`, name)
	return err
}

func (s *Store) updateService(name string, fields map[string]any) error {
	q, args := "UPDATE services SET updated = ?", []any{time.Now().Unix()}
	for k, v := range fields {
		q += ", " + k + " = ?"
		args = append(args, v)
	}
	_, err := s.db.Exec(q+" WHERE name = ?", append(args, name)...)
	return err
}

// assignService gives a waiting service its node: true if it did.
func (s *Store) assignService(name, node string) (bool, error) {
	res, err := s.db.Exec(`UPDATE services SET node = ?, state = 'pending', restarts = 0, error = '',
		pending_reason = '', updated = ? WHERE name = ? AND node = ''`, node, time.Now().Unix(), name)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// unassignService takes a service from its node, to be placed again.
func (s *Store) unassignService(name, reason string) error {
	return s.updateService(name, map[string]any{"node": "", "state": statePending, "restarts": 0,
		"error": "", "pending_reason": reason})
}

func (s *Store) serviceCount(node string) int {
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM services WHERE node = ? AND desired = 'running'`, node).Scan(&n)
	return n
}
