package nodestore

import (
	"database/sql"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/Alexis-Marcel/eriphe-system/core/nodefile"
)

// Values: the lines of a page that follow the event journal (nodefile's
// listen.go) are rewritten by code at each event. A version is someone
// who wrote; a value is not: it makes none.
//
//   - WriteValues writes the file and indexes it at the version it is
//     at; the node is pending (node_pending: the file's hash as code
//     wrote it). Read, it is the file: the last version and the values
//     written since. A write made on that version (expect) still is on
//     the last one.
//   - The next version of the node, whoever makes it, holds the values:
//     its file is the one on disk, values included. An author's write
//     made on the version read keeps the values code wrote since, unless
//     it changed them itself (nodefile.KeepHeard).
//   - CommitReadings makes one version, author Readings, of the nodes
//     still pending: once a day, before the backup, so the history (and
//     the bundle) holds the day's values when nobody wrote.
//   - At start, a file pending as code wrote it is not one changed
//     outside Eriphe: it stays pending. An index made again from the
//     files knows none: such a file is then committed as changed outside.

// Readings is the author of the version that puts the values code wrote
// into the history (CommitReadings).
const Readings = "readings"

// WriteValues is code's write of the values a node's lines follow: fn
// changes the node's last version as Change's does, and the file is
// written without a version. An archived node is not written.
func (s *Store) WriteValues(id string, fn func(*nodefile.Node) error) (*Doc, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur, err := s.read(id, 0)
	if err != nil {
		return nil, err
	}
	if !cur.Node.Meta.Archived.IsZero() {
		return nil, fmt.Errorf("%s: %w", id, ErrArchived)
	}
	n, err := nodefile.Parse(cur.Raw)
	if err != nil {
		return nil, err
	}
	if err := s.keepPlace(fn)(n); err != nil {
		return nil, err
	}
	if err := n.Validate(); err != nil {
		return nil, err
	}
	settleTitle(n, cur.Node)
	n.Meta.Format = s.format()
	raw, err := n.Bytes()
	if err != nil {
		return nil, err
	}
	if len(raw) > MaxBytes {
		return nil, fmt.Errorf("a node is %d KiB at most, this one would be %d KiB", MaxBytes>>10, len(raw)>>10)
	}
	if string(raw) == string(cur.Raw) {
		return cur, nil
	}
	// The index first: a stop before the file is written leaves a node
	// pending whose file is its version's, committed the next time.
	tx, err := begin(s.db)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err := indexNode(tx, n, cur.File, raw, cur.Last, cur.Version.At); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`INSERT INTO node_pending (id, blob, since) VALUES (?, ?, ?)
		ON CONFLICT (id) DO UPDATE SET blob = excluded.blob`, id, blobHash(raw), s.opts.Now().Unix()); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	if err := writeFile(filepath.Join(s.dir, cur.File), raw); err != nil {
		return nil, err
	}
	s.changed(ChangedDoc, id)
	return &Doc{Node: n, Raw: raw, File: cur.File, Version: cur.Version, Last: cur.Last}, nil
}

// Listening are the nodes not archived whose lines follow the events of
// this kind and key.
func (s *Store) Listening(kind, key string) ([]string, error) {
	rows, err := s.db.Query(`SELECT DISTINCT l.id FROM node_listens l JOIN nodes n ON n.id = l.id
		WHERE n.archived = 0 AND l.kind = ? AND (l.key = '' OR l.key = ?) ORDER BY l.id`, kind, key)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// pendingBlobs are the nodes whose values are in no version yet, and
// their file's hash as code wrote it.
func (s *Store) pendingBlobs() (map[string]string, error) {
	rows, err := s.db.Query(`SELECT id, blob FROM node_pending`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var id, blob string
		if err := rows.Scan(&id, &blob); err != nil {
			return nil, err
		}
		out[id] = blob
	}
	return out, rows.Err()
}

// isPending tells a node whose values are in no version yet.
func (s *Store) isPending(id string) bool {
	var blob string
	return s.db.QueryRow(`SELECT blob FROM node_pending WHERE id = ?`, id).Scan(&blob) == nil
}

// keepHeard keeps in n, a write made on the version whose hash is
// expect, the values code wrote since: in no version yet, or in versions
// of code's since that one (readings) (nodefile.KeepHeard).
func (s *Store) keepHeard(cur *Doc, expect string, n *nodefile.Node) error {
	if expect == cur.Version.Hash && !s.isPending(cur.Node.Meta.ID) {
		return nil
	}
	read, err := s.nodeAt(expect, cur.File)
	if err != nil {
		return err
	}
	was, err := read.Body()
	if err != nil {
		return err
	}
	now, err := cur.Node.Body()
	if err != nil {
		return err
	}
	written, err := n.Body()
	if err != nil {
		return err
	}
	if out := nodefile.KeepHeard(was, now, written); out != written {
		return n.CodeBody(out)
	}
	return nil
}

// nodeAt is a node's file at a commit, read in today's format.
func (s *Store) nodeAt(hash, file string) (*nodefile.Node, error) {
	raw, err := s.fileAt(hash, file)
	if err != nil {
		return nil, err
	}
	n, _, err := s.parse(raw)
	return n, err
}

// CommitReadings makes one version, author Readings, of every node whose
// values are in no version yet; it gives them. A file changed since code
// wrote it is left to the next start (changed outside Eriphe).
func (s *Store) CommitReadings() ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.opts.Now()
	// One a day at most, whoever calls it how often.
	var last sql.NullInt64
	if err := s.db.QueryRow(`SELECT MAX(at) FROM node_versions WHERE author = ?`, Readings).Scan(&last); err != nil {
		return nil, err
	}
	if last.Valid && time.Unix(last.Int64, 0).In(now.Location()).Format(time.DateOnly) == now.Format(time.DateOnly) {
		return nil, nil
	}
	pending, err := s.pendingBlobs()
	if err != nil || len(pending) == 0 {
		return nil, err
	}
	var changes []change
	var stamps []stamp
	var xs []indexed
	var ids []string
	for _, id := range slices.Sorted(maps.Keys(pending)) {
		sum, err := s.summary(id)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		raw, err := os.ReadFile(filepath.Join(s.dir, sum.File))
		if err != nil {
			return nil, err
		}
		if blobHash(raw) != pending[id] {
			continue
		}
		n, raw, err := s.parse(raw)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", sum.File, err)
		}
		changes = append(changes, change{path: sum.File, data: raw, onDisk: true})
		stamps = append(stamps, stamp{id, sum.Version + 1})
		xs = append(xs, indexed{n, sum.File, raw, Version{N: sum.Version + 1, Author: Readings, At: now}})
		ids = append(ids, id)
	}
	if len(changes) == 0 {
		return nil, nil
	}
	hash, err := s.commit("readings: the values the pages follow", Readings, now, changes, stamps)
	if err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	for i := range xs {
		xs[i].v.Hash = hash
	}
	return ids, s.indexVersions(now, xs)
}
