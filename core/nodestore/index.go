package nodestore

import (
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Alexis-Marcel/eriphe-system/core/nodefile"
)

// The index, nodes.db: what must be fast, kept from the files. It is
// rebuilt from them (and from the log, for the versions) when it is lost,
// nothing lost: what code keeps beside a node (its
// cards, its glances) is in core.db. A node's status (running,
// waiting, done) is not kept at all: eriphe-core computes it.

// schemaVersion is the schema nodes.db is at; meta.schema_version says
// it. A file at an older version is made again from the files and the
// log (openIndex); one at a newer version is refused, never changed.
const schemaVersion = 9

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
	  goal TEXT NOT NULL DEFAULT '',       -- what it is for, one line (its front matter's goal)
	  parent TEXT NOT NULL DEFAULT '',
	  created INTEGER NOT NULL,
	  updated INTEGER NOT NULL,
	  done INTEGER NOT NULL DEFAULT 0,
	  archived INTEGER NOT NULL DEFAULT 0,
	  external INTEGER NOT NULL DEFAULT 0,
	  title_trusted INTEGER NOT NULL DEFAULT 0, -- external, its title is not someone else's words
	  opened_by TEXT NOT NULL DEFAULT '', -- who wanted it (its opened_by's by), '' unknown
	  version INTEGER NOT NULL,          -- the last version's n
	  blob TEXT NOT NULL,                -- the file's git hash, to tell a change made outside
	  title_key TEXT NOT NULL DEFAULT '', -- its title as a [[link]] matches it (nodefile.LinkKey)
	  fts INTEGER NOT NULL DEFAULT 0);   -- its words' row in node_fts (rowid), so they are replaced without a scan
	CREATE INDEX nodes_parent ON nodes (parent);
	CREATE TABLE node_links (id TEXT NOT NULL, target TEXT NOT NULL, PRIMARY KEY (id, target));
	CREATE INDEX node_links_target ON node_links (target);` + refsSchema + `
	CREATE TABLE node_versions (
	  id TEXT NOT NULL, n INTEGER NOT NULL,
	  hash TEXT NOT NULL,                -- the commit that made it
	  author TEXT NOT NULL, at INTEGER NOT NULL,
	  PRIMARY KEY (id, n));
	CREATE TABLE node_listens (
	  id TEXT NOT NULL, kind TEXT NOT NULL,
	  key TEXT NOT NULL,                 -- '' for the events of any key
	  PRIMARY KEY (id, kind, key));
	CREATE INDEX node_listens_kind ON node_listens (kind, key);
	CREATE TABLE node_pending (
	  id TEXT PRIMARY KEY,
	  blob TEXT NOT NULL,                -- the file's git hash as code wrote it, no version made
	  since INTEGER NOT NULL);           -- when the first of its values not in a version was written
	CREATE TABLE node_props (
	  id TEXT NOT NULL, key TEXT NOT NULL, -- its name, lowercase
	  pos INTEGER NOT NULL,              -- its place in a list; -1 for a value alone
	  kind TEXT NOT NULL,                -- text, number or date (nodefile.Kind*)
	  value TEXT NOT NULL,               -- as written (a tag without its #)
	  fold TEXT NOT NULL,                -- lowercase, what an equality and a sort read
	  num REAL,                          -- a number's
	  PRIMARY KEY (id, key, pos));
	CREATE INDEX node_props_fold ON node_props (key, fold);
	CREATE INDEX node_props_num ON node_props (key, num);
	CREATE VIRTUAL TABLE node_fts USING fts5(id UNINDEXED, title, body,
	  tokenize='unicode61 remove_diacritics 2');`

// openIndex opens nodes.db; fresh tells whether it was made now (and must
// be filled from the files). An index at an older schema holds nothing
// the files and the log do not: it is deleted (its file, its -wal and
// -shm, nothing else) and made again; old says the schema it was at. One
// at a newer schema is refused, never changed.
func openIndex(path string) (db *sql.DB, fresh bool, old int, err error) {
	db, fresh, version, err := openIndexAt(path)
	if err != nil || version == 0 || version == schemaVersion {
		return db, fresh, 0, err
	}
	db.Close()
	if version > schemaVersion {
		return nil, false, 0, fmt.Errorf("nodes.db is at schema %d, newer than the %d this eriphe-core reads: update Eriphe", version, schemaVersion)
	}
	for _, f := range []string{path, path + "-wal", path + "-shm"} {
		if err := os.Remove(f); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, false, 0, fmt.Errorf("nodes.db at schema %d, not made again: %w", version, err)
		}
	}
	db, fresh, _, err = openIndexAt(path)
	return db, fresh, version, err
}

// openIndexAt opens nodes.db, made at today's schema when it is new; at
// another schema, it gives the schema and the database as it is.
func openIndexAt(path string) (db *sql.DB, fresh bool, version int, err error) {
	db, err = sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=temp_store(memory)")
	if err != nil {
		return nil, false, 0, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS meta (key TEXT PRIMARY KEY, value TEXT NOT NULL)`); err != nil {
		db.Close()
		return nil, false, 0, err
	}
	var v string
	if db.QueryRow(`SELECT value FROM meta WHERE key = 'schema_version'`).Scan(&v) == nil {
		fmt.Sscan(v, &version)
	}
	if version != 0 {
		return db, false, version, nil
	}
	tx, err := db.Begin()
	if err != nil {
		db.Close()
		return nil, false, 0, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(schema); err != nil {
		db.Close()
		return nil, false, 0, err
	}
	if _, err := tx.Exec(`INSERT INTO meta (key, value) VALUES ('schema_version', ?)`, fmt.Sprint(schemaVersion)); err != nil {
		db.Close()
		return nil, false, 0, err
	}
	if err := tx.Commit(); err != nil {
		db.Close()
		return nil, false, 0, err
	}
	return db, true, 0, nil
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

// cachedTx is a transaction that prepares each statement once, however
// many nodes it indexes; its statements close with it.
type cachedTx struct {
	*sql.Tx
	stmts map[string]*sql.Stmt
}

// begin starts a transaction that prepares each statement once.
func begin(db *sql.DB) (*cachedTx, error) {
	tx, err := db.Begin()
	if err != nil {
		return nil, err
	}
	return &cachedTx{Tx: tx, stmts: map[string]*sql.Stmt{}}, nil
}

func (t *cachedTx) stmt(query string) (*sql.Stmt, error) {
	if st, ok := t.stmts[query]; ok {
		return st, nil
	}
	st, err := t.Tx.Prepare(query)
	if err != nil {
		return nil, err
	}
	t.stmts[query] = st
	return st, nil
}

func (t *cachedTx) Exec(query string, args ...any) (sql.Result, error) {
	st, err := t.stmt(query)
	if err != nil {
		return nil, err
	}
	return st.Exec(args...)
}

func (t *cachedTx) Query(query string, args ...any) (*sql.Rows, error) {
	st, err := t.stmt(query)
	if err != nil {
		return nil, err
	}
	return st.Query(args...)
}

func (t *cachedTx) QueryRow(query string, args ...any) *sql.Row {
	st, err := t.stmt(query)
	if err != nil {
		return t.Tx.QueryRow(query, args...) // it fails the same way, its Scan says so
	}
	return st.QueryRow(args...)
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
	// Its words replace those it had, found by their row, not by a scan
	// of node_fts (its id is not indexed there).
	var fts int64
	if err := tx.QueryRow(`SELECT fts FROM nodes WHERE id = ?`, m.ID).Scan(&fts); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if fts != 0 {
		if _, err := tx.Exec(`DELETE FROM node_fts WHERE rowid = ?`, fts); err != nil {
			return err
		}
	}
	res, err := tx.Exec(`INSERT INTO node_fts (rowid, id, title, body) VALUES (NULLIF(?, 0), ?, ?, ?)`, fts, m.ID, m.Title, searchText(n))
	if err != nil {
		return err
	}
	if fts, err = res.LastInsertId(); err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO nodes (id, file, title, goal, parent, created, updated, done, archived, external, title_trusted, opened_by, version, blob, title_key, fts)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (id) DO UPDATE SET file = excluded.file, title = excluded.title, goal = excluded.goal, parent = excluded.parent,
		  updated = excluded.updated, done = excluded.done, archived = excluded.archived,
		  external = excluded.external, title_trusted = excluded.title_trusted, opened_by = excluded.opened_by, version = excluded.version, blob = excluded.blob,
		  title_key = excluded.title_key, fts = excluded.fts`,
		m.ID, file, m.Title, m.Goal, m.Parent, m.Created.Unix(), updated.Unix(),
		unixOr0(m.Done), unixOr0(m.Archived), m.External, m.External && m.TitleTrusted, by, version, blobHash(raw), nodefile.LinkKey(m.Title), fts)
	if err != nil {
		return err
	}
	if err := indexRefs(tx, n); err != nil {
		return err
	}
	if err := indexProps(tx, m); err != nil {
		return err
	}
	for _, q := range []string{`DELETE FROM node_links WHERE id = ?`, `DELETE FROM node_listens WHERE id = ?`} {
		if _, err := tx.Exec(q, m.ID); err != nil {
			return err
		}
	}
	for _, o := range n.Ons() {
		if _, err := tx.Exec(`INSERT INTO node_listens (id, kind, key) VALUES (?, ?, ?)`, m.ID, o.Kind, o.Key); err != nil {
			return err
		}
	}
	for _, l := range m.Links {
		if _, err := tx.Exec(`INSERT INTO node_links (id, target) VALUES (?, ?)`, m.ID, l); err != nil {
			return err
		}
	}
	return nil
}

// forget takes a node out of the index, its versions kept. The links to
// it wait again for a node of their name (resolveRefs).
func forget(tx execer, id string) error {
	// Its words first, by their row, while its row says which.
	for _, q := range []string{`DELETE FROM node_fts WHERE rowid = (SELECT fts FROM nodes WHERE id = ?)`,
		`DELETE FROM nodes WHERE id = ?`, `DELETE FROM node_links WHERE id = ?`,
		`DELETE FROM node_listens WHERE id = ?`, `DELETE FROM node_pending WHERE id = ?`,
		`DELETE FROM node_refs WHERE id = ?`, `DELETE FROM node_props WHERE id = ?`, `UPDATE node_refs SET target = '' WHERE target = ?`} {
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

// searchText is what a node is found by beside its title: its goal and
// its page.
func searchText(n *nodefile.Node) string { return n.Meta.Goal + "\n" + n.Text() }

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
