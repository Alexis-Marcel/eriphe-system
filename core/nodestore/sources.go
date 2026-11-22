package nodestore

import (
	"encoding/json"

	"github.com/Alexis-Marcel/eriphe-system/core/nodefile"
)

// LiveSource is a source a block of a node that is not archived reads:
// what code reads on its clock (node_sources).
type LiveSource struct {
	ID     string // the node's
	Block  string
	Name   string
	Source nodefile.Source
}

// Sources are the sources of the nodes not archived, by node, block and
// name; those of one node only when id is given.
func (s *Store) Sources(id string) ([]LiveSource, error) {
	return s.sources(id, false)
}

// KeptSources are the sources of every node, archived ones too: those an
// archived node's page still shows the last values of.
func (s *Store) KeptSources(id string) ([]LiveSource, error) {
	return s.sources(id, true)
}

func (s *Store) sources(id string, archived bool) ([]LiveSource, error) {
	q := `SELECT s.id, s.block, s.name, s.spec FROM node_sources s JOIN nodes n ON n.id = s.id WHERE (n.archived = 0 OR ?)`
	args := []any{archived}
	if id != "" {
		q += ` AND s.id = ?`
		args = append(args, id)
	}
	rows, err := s.db.Query(q+` ORDER BY s.id, s.block, s.name`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LiveSource
	for rows.Next() {
		var x LiveSource
		var spec string
		if err := rows.Scan(&x.ID, &x.Block, &x.Name, &spec); err != nil {
			return nil, err
		}
		if json.Unmarshal([]byte(spec), &x.Source) == nil {
			out = append(out, x)
		}
	}
	return out, rows.Err()
}
