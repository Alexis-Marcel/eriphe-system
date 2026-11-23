package nodestore

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Alexis-Marcel/eriphe-system/core/nodefile"
)

// The index, nodes.db: what must be fast, kept from the files. It is
// rebuilt from them (and from the log, for the versions) when it is lost,
// nothing lost: what code keeps beside a node (its items' notes, its
// cards, its sources' reads) is in core.db. A node's status (running,
// waiting, done) is not kept at all: eriphe-core computes it.

// schemaVersion is the schema nodes.db is at; meta.schema_version says
// it. A file at another version is refused, never changed: delete it,
// it is made again from the files.
const schemaVersion = 3

// refsSchema holds the [[links]] of the nodes' text: each name a node's
// text links (its LinkKey) and the node it was resolved to, ” while no
// node has that title. A link keeps the node it was resolved to when that
// node's title changes.
const refsSchema = `
	CREATE TABLE node_refs (id TEXT NOT NULL, key TEXT NOT NULL, target TEXT NOT NULL DEFAULT '', PRIMARY KEY (id, key));
	CREATE INDEX node_refs_key ON node_refs (key, target);
	CREATE INDEX node_refs_target ON node_refs (target);
	CREATE INDEX nodes_title_key ON nodes (title_key);`

const schema = `CREATE TABLE nodes (
	  id TEXT PRIMARY KEY,
	  file TEXT NOT NULL UNIQUE,         -- <slug>-<id>.md, frozen at creation
	  title TEXT NOT NULL,
	  parent TEXT NOT NULL DEFAULT '',
	  lifetime TEXT NOT NULL,
	  created INTEGER NOT NULL,
	  updated INTEGER NOT NULL,
	  done INTEGER NOT NULL DEFAULT 0,
	  archived INTEGER NOT NULL DEFAULT 0,
	  external INTEGER NOT NULL DEFAULT 0,
	  title_trusted INTEGER NOT NULL DEFAULT 0, -- external, its title is not someone else's words
	  opened_by TEXT NOT NULL DEFAULT '', -- who wanted it (its opened_by's by), '' unknown
	  version INTEGER NOT NULL,          -- the last version's n
	  blob TEXT NOT NULL,                -- the file's git hash, to tell a change made outside
	  title_key TEXT NOT NULL DEFAULT ''); -- its title as a [[link]] matches it (nodefile.LinkKey)
	CREATE INDEX nodes_parent ON nodes (parent);
	CREATE TABLE node_links (id TEXT NOT NULL, target TEXT NOT NULL, PRIMARY KEY (id, target));
	CREATE INDEX node_links_target ON node_links (target);` + refsSchema + `
	CREATE TABLE node_versions (
	  id TEXT NOT NULL, n INTEGER NOT NULL,
	  hash TEXT NOT NULL,                -- the commit that made it
	  author TEXT NOT NULL, at INTEGER NOT NULL,
	  PRIMARY KEY (id, n));
	CREATE TABLE node_sources (
	  id TEXT NOT NULL, block TEXT NOT NULL, name TEXT NOT NULL,
	  spec TEXT NOT NULL,                -- the source as the block says it, JSON
	  PRIMARY KEY (id, block, name));
	CREATE VIRTUAL TABLE node_fts USING fts5(id UNINDEXED, title, body,
	  tokenize='unicode61 remove_diacritics 2');`

// openIndex opens nodes.db; fresh tells whether it was made now (and must
// be filled from the files).
func openIndex(path string) (db *sql.DB, fresh bool, err error) {
	db, err = sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=temp_store(memory)")
	if err != nil {
		return nil, false, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS meta (key TEXT PRIMARY KEY, value TEXT NOT NULL)`); err != nil {
		db.Close()
		return nil, false, err
	}
	version := 0
	var v string
	if db.QueryRow(`SELECT value FROM meta WHERE key = 'schema_version'`).Scan(&v) == nil {
		fmt.Sscan(v, &version)
	}
	switch version {
	case schemaVersion:
		return db, false, nil
	case 0:
	default:
		db.Close()
		return nil, false, fmt.Errorf("nodes.db is at schema %d, this eriphe-core reads %d: delete it, it is made again from the files", version, schemaVersion)
	}
	tx, err := db.Begin()
	if err != nil {
		db.Close()
		return nil, false, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(schema); err != nil {
		db.Close()
		return nil, false, err
	}
	if _, err := tx.Exec(`INSERT INTO meta (key, value) VALUES ('schema_version', ?)`, fmt.Sprint(schemaVersion)); err != nil {
		db.Close()
		return nil, false, err
	}
	if err := tx.Commit(); err != nil {
		db.Close()
		return nil, false, err
	}
	return db, true, nil
}

// execer is a transaction or the database.
type execer interface {
	Exec(query string, args ...any) (sql.Result, error)
}

// querier is a transaction or the database, read as well.
type querier interface {
	execer
	Query(query string, args ...any) (*sql.Rows, error)
	QueryRow(query string, args ...any) *sql.Row
}

// indexNode writes what the index keeps of a node's file: its row, links,
// sources and words. The version is kept when the row is
// there; version is the n of its last version. Its text's [[links]] keep
// the nodes they were resolved to; a new one waits for resolveRefs.
func indexNode(tx querier, n *nodefile.Node, file string, raw []byte, version int, updated time.Time) error {
	m := n.Meta
	by := ""
	if m.OpenedBy != nil {
		by = m.OpenedBy.By
	}
	_, err := tx.Exec(`INSERT INTO nodes (id, file, title, parent, lifetime, created, updated, done, archived, external, title_trusted, opened_by, version, blob, title_key)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (id) DO UPDATE SET file = excluded.file, title = excluded.title, parent = excluded.parent,
		  lifetime = excluded.lifetime, updated = excluded.updated, done = excluded.done, archived = excluded.archived,
		  external = excluded.external, title_trusted = excluded.title_trusted, opened_by = excluded.opened_by, version = excluded.version, blob = excluded.blob,
		  title_key = excluded.title_key`,
		m.ID, file, m.Title, m.Parent, string(m.Lifetime), m.Created.Unix(), updated.Unix(),
		unixOr0(m.Done), unixOr0(m.Archived), m.External, m.External && m.TitleTrusted, by, version, blobHash(raw), nodefile.LinkKey(m.Title))
	if err != nil {
		return err
	}
	if err := indexRefs(tx, n); err != nil {
		return err
	}
	for _, q := range []string{`DELETE FROM node_links WHERE id = ?`, `DELETE FROM node_sources WHERE id = ?`, `DELETE FROM node_fts WHERE id = ?`} {
		if _, err := tx.Exec(q, m.ID); err != nil {
			return err
		}
	}
	for _, l := range m.Links {
		if _, err := tx.Exec(`INSERT INTO node_links (id, target) VALUES (?, ?)`, m.ID, l); err != nil {
			return err
		}
	}
	for _, b := range n.Blocks() {
		for name, src := range b.Sources {
			spec, _ := json.Marshal(src)
			if _, err := tx.Exec(`INSERT INTO node_sources (id, block, name, spec) VALUES (?, ?, ?, ?)`, m.ID, b.Name, name, string(spec)); err != nil {
				return err
			}
		}
	}
	_, err = tx.Exec(`INSERT INTO node_fts (id, title, body) VALUES (?, ?, ?)`, m.ID, m.Title, searchText(n))
	return err
}

// forget takes a node out of the index, its versions kept. The links to
// it wait again for a node of their name (resolveRefs).
func forget(tx execer, id string) error {
	for _, q := range []string{`DELETE FROM nodes WHERE id = ?`, `DELETE FROM node_links WHERE id = ?`,
		`DELETE FROM node_sources WHERE id = ?`, `DELETE FROM node_fts WHERE id = ?`,
		`DELETE FROM node_refs WHERE id = ?`, `UPDATE node_refs SET target = '' WHERE target = ?`} {
		if _, err := tx.Exec(q, id); err != nil {
			return err
		}
	}
	return nil
}

func unixOr0(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.Unix()
}

func timeOr0(sec int64) time.Time {
	if sec == 0 {
		return time.Time{}
	}
	return time.Unix(sec, 0)
}

// searchText is what a node is found by beside its title: its text, its
// goal, and the words its blocks show (strings of their components and
// data), not their JSON.
func searchText(n *nodefile.Node) string {
	var b strings.Builder
	b.WriteString(n.Meta.Goal)
	b.WriteString("\n")
	b.WriteString(n.Text())
	for _, bl := range n.Blocks() {
		for _, raw := range []json.RawMessage{bl.Components, bl.Data} {
			var v any
			if json.Unmarshal(raw, &v) == nil {
				words(&b, v)
			}
		}
	}
	return b.String()
}

// words writes the text of a JSON value's strings, but those of keys that
// hold ids, paths and component names.
func words(b *strings.Builder, v any) {
	switch x := v.(type) {
	case string:
		b.WriteString("\n")
		b.WriteString(x)
	case []any:
		for _, e := range x {
			words(b, e)
		}
	case map[string]any:
		for k, e := range x {
			if !notWords[k] {
				words(b, e)
			}
		}
	}
}

var notWords = map[string]bool{"id": true, "component": true, "child": true, "children": true,
	"path": true, "@path": true, "componentId": true, "list": true, "item": true, "kind": true}

// ftsQuery turns free text into an FTS5 query: its words of three letters
// or more, each a prefix (the shorter ones only when there is no other),
// any of them enough: a word the node does not hold (« journée agenda
// aujourd'hui » for « Ma journée ») never hides it, and the ranking puts
// the nodes that hold the most words first. FTS syntax in the input is not
// honoured.
func ftsQuery(q string) string {
	var long, short []string
	for _, w := range strings.FieldsFunc(strings.ToLower(q), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		if utf8.RuneCountInString(w) >= 3 {
			long = append(long, `"`+w+`"*`)
		} else {
			short = append(short, `"`+w+`"`)
		}
	}
	if len(long) == 0 {
		long = short
	}
	return strings.Join(long, " OR ")
}
