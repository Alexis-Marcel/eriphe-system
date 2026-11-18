package nodestore

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Alexis-Marcel/eriphe-system/core/nodefile"
)

// reconcile compares the files to the index at start. fresh: the index
// was just made, so it is filled from the files and the log.
//
//   - An index behind the repository (a copy put back, a stop between a
//     commit and its indexing) reads the versions again from the log.
//   - A file that is not the last commit's (changed, added or removed by
//     hand) is committed, author Outside: each node in it gets a version,
//     and is reported.
//   - A committed file the index does not hold as it is, is indexed.
//   - A file that does not read as a node is reported and left alone.
func (s *Store) reconcile(fresh bool) (Report, error) {
	rep := Report{Rebuilt: fresh}
	head, err := s.headFiles()
	if err != nil {
		return rep, fmt.Errorf("repository: %w", err)
	}
	var indexedHead string
	s.db.QueryRow(`SELECT value FROM meta WHERE key = 'head'`).Scan(&indexedHead)
	repoHead, err := s.headHash()
	if err != nil {
		return rep, fmt.Errorf("repository: %w", err)
	}
	if !fresh && indexedHead != repoHead {
		rep.Behind = true
	}
	if fresh || rep.Behind {
		logged, err := s.logVersions()
		if err != nil {
			return rep, fmt.Errorf("log: %w", err)
		}
		tx, err := s.db.Begin()
		if err != nil {
			return rep, err
		}
		for _, v := range logged {
			if _, err := tx.Exec(`INSERT OR REPLACE INTO node_versions (id, n, hash, author, at) VALUES (?, ?, ?, ?, ?)`,
				v.id, v.n, v.hash, v.author, v.at.Unix()); err != nil {
				tx.Rollback()
				return rep, err
			}
		}
		if err := tx.Commit(); err != nil {
			return rep, err
		}
	}
	type row struct {
		file, blob string
		version    int
	}
	inIndex := map[string]row{}
	rows, err := s.db.Query(`SELECT id, file, blob, version FROM nodes`)
	if err != nil {
		return rep, err
	}
	for rows.Next() {
		var id string
		var r row
		if err := rows.Scan(&id, &r.file, &r.blob, &r.version); err != nil {
			rows.Close()
			return rep, err
		}
		inIndex[id] = r
	}
	rows.Close()

	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return rep, err
	}
	now := s.opts.Now()
	var changes []change
	var stamps []stamp
	var outside, known []indexed
	onDisk := map[string]bool{}
	broken := map[string]bool{} // files on disk that do not read as a node
	seen := map[string]string{} // id: its file
	for _, e := range entries {
		name := e.Name()
		if !e.Type().IsRegular() || !strings.HasSuffix(name, ".md") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(s.dir, name))
		if err != nil {
			return rep, err
		}
		onDisk[name] = true
		committed := head[name].String() == blobHash(raw)
		if strings.HasSuffix(name, ".trace.md") {
			if !committed {
				changes = append(changes, change{path: name, data: raw})
			}
			continue
		}
		n, err := nodefile.Parse(raw)
		if err != nil {
			rep.Broken = append(rep.Broken, name+": "+err.Error())
			broken[name] = true
			continue
		}
		id := n.Meta.ID
		if !strings.HasSuffix(name, "-"+id+".md") {
			rep.Broken = append(rep.Broken, fmt.Sprintf("%s: its id is %s, its name does not end with -%s.md", name, id, id))
			continue
		}
		if other, ok := seen[id]; ok {
			rep.Broken = append(rep.Broken, fmt.Sprintf("%s: id %s is %s's already", name, id, other))
			continue
		}
		seen[id] = name
		last, lastAt, err := s.lastVersion(id)
		if err != nil {
			return rep, err
		}
		if !committed || last == 0 {
			changes = append(changes, change{path: name, data: raw})
			stamps = append(stamps, stamp{id, last + 1})
			outside = append(outside, indexed{n, name, raw, Version{N: last + 1, Author: Outside, At: now}})
			if !committed {
				rep.Changed = append(rep.Changed, id)
			}
			continue
		}
		if r, ok := inIndex[id]; !ok || r.file != name || r.blob != blobHash(raw) || r.version != last {
			known = append(known, indexed{n, name, raw, Version{N: last, At: lastAt}})
		}
	}
	// Removed by hand: out of the index, and out of the repository.
	for id, r := range inIndex {
		if _, ok := seen[id]; ok || broken[r.file] {
			continue // a node whose file broke keeps its row: reported, to mend
		}
		if !onDisk[r.file] {
			rep.Gone = append(rep.Gone, id)
		}
		if err := forget(s.db, id); err != nil {
			return rep, err
		}
	}
	for name := range head {
		if strings.HasSuffix(name, ".md") && !onDisk[name] {
			changes = append(changes, change{path: name})
		}
	}
	sort.Strings(rep.Changed)
	sort.Strings(rep.Gone)
	if len(changes) > 0 {
		hash, err := s.commit("outside: files changed outside Eriphe", Outside, now, changes, stamps)
		if err != nil {
			return rep, fmt.Errorf("commit: %w", err)
		}
		for i := range outside {
			outside[i].v.Hash = hash
		}
		if err := s.indexVersions(now, outside); err != nil {
			return rep, err
		}
	}
	if len(known) > 0 {
		tx, err := s.db.Begin()
		if err != nil {
			return rep, err
		}
		defer tx.Rollback()
		for _, x := range known {
			if err := indexNode(tx, x.node, x.file, x.raw, x.v.N, x.v.At); err != nil {
				return rep, err
			}
		}
		if err := tx.Commit(); err != nil {
			return rep, err
		}
	}
	if repoHead, err = s.headHash(); err != nil || repoHead == "" {
		return rep, err
	}
	return rep, setIndexed(s.db, repoHead)
}

// lastVersion is a node's last version in the index: its n (0 if none)
// and when it was made.
func (s *Store) lastVersion(id string) (int, time.Time, error) {
	var n int
	var at int64
	err := s.db.QueryRow(`SELECT COALESCE(MAX(n), 0), COALESCE(MAX(at), 0) FROM node_versions WHERE id = ?`, id).Scan(&n, &at)
	return n, time.Unix(at, 0), err
}
