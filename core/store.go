package main

import (
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"strconv"
	"time"

	_ "modernc.org/sqlite"
)

// Task states, mirrored from the node, plus the ones only eriphe-core knows.
const (
	statePending           = "pending" // not yet accepted by a node
	stateRunning           = "running"
	stateWaitingPermission = "waiting_permission"
	stateIdle              = "idle" // the agent's turn is over: done, or asking something
	stateStopped           = "stopped"
	stateFailed            = "failed"
	stateLost              = "lost" // the node no longer knows the task
)

func finished(state string) bool {
	return state == stateStopped || state == stateFailed || state == stateLost
}

type Store struct {
	db  *sql.DB
	dir string // the data folder, where the database is
}

type NodeRow struct {
	Name     string          `json:"name"`
	Online   bool            `json:"online"`
	Info     json.RawMessage `json:"info,omitempty"`
	LastSeen time.Time       `json:"last_seen"`
	// When its client certificate ends; zero while it has none (machine_tls.go).
	CertExpires time.Time `json:"cert_expires,omitzero"`
	// Its system as it last reported it, kept while it is offline: GOOS,
	// GOARCH and how it was installed (ERIPHE_INSTALL; empty by hand).
	OS      string `json:"os,omitempty"`
	Arch    string `json:"arch,omitempty"`
	Install string `json:"install,omitempty"`
}

type TaskRow struct {
	ID   string `json:"id"`
	Node string `json:"node"` // empty while the task waits for a node
	// Why a pending task has no node yet.
	PendingReason string    `json:"pending_reason,omitempty"`
	Agent         string    `json:"agent,omitempty"`
	NodeTaskID    string    `json:"node_task_id,omitempty"`
	Runtime       string    `json:"runtime"`
	Model         string    `json:"model,omitempty"`
	Identity      string    `json:"identities,omitempty"` // names only, never the secrets
	Kind          string    `json:"kind,omitempty"`       // worker or brain
	Tools         string    `json:"tools,omitempty"`      // its grant from the catalog, comma-separated
	Folders       string    `json:"folders,omitempty"`    // the user's folders lent to it, by name, comma-separated (folders.go)
	Dir           string    `json:"dir"`
	Prompt        string    `json:"prompt"`
	State         string    `json:"state"`
	SessionID     string    `json:"session_id,omitempty"`
	LastSeq       int       `json:"-"`
	Created       time.Time `json:"created"`
	Updated       time.Time `json:"updated"`
}

type EventRow struct {
	Seq  int            `json:"seq"`
	Time time.Time      `json:"time"`
	Type string         `json:"type"`
	Data map[string]any `json:"data"`
}

const schema = `
CREATE TABLE IF NOT EXISTS nodes (
  name TEXT PRIMARY KEY, url TEXT NOT NULL, online INTEGER NOT NULL DEFAULT 0,
  info TEXT, last_seen INTEGER NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS tasks (
  id TEXT PRIMARY KEY, node TEXT NOT NULL, node_task_id TEXT NOT NULL DEFAULT '',
  runtime TEXT NOT NULL, model TEXT NOT NULL DEFAULT '', identity TEXT NOT NULL DEFAULT '',
  dir TEXT NOT NULL, prompt TEXT NOT NULL, state TEXT NOT NULL,
  session_id TEXT NOT NULL DEFAULT '', last_seq INTEGER NOT NULL DEFAULT 0,
  created INTEGER NOT NULL, updated INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS messages (
  seq INTEGER PRIMARY KEY AUTOINCREMENT, time INTEGER NOT NULL,
  role TEXT NOT NULL, text TEXT NOT NULL, delivered INTEGER NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS kv (key TEXT PRIMARY KEY, value TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS inbox (
  seq INTEGER PRIMARY KEY AUTOINCREMENT, task_id TEXT NOT NULL, kind TEXT NOT NULL,
  data TEXT NOT NULL, created INTEGER NOT NULL, acked INTEGER NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS join_tokens (hash TEXT PRIMARY KEY, expires INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS events (
  task_id TEXT NOT NULL, seq INTEGER NOT NULL, time INTEGER NOT NULL,
  type TEXT NOT NULL, data TEXT NOT NULL, PRIMARY KEY (task_id, seq));
`

// extraSchema holds the tables of other modules (the policy's decisions).
var extraSchema []string

func openStore(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	for _, q := range append([]string{schema}, extraSchema...) {
		if _, err := db.Exec(q); err != nil {
			return nil, err
		}
	}
	// Added after v0: which tasks are the brain rather than workers.
	db.Exec(`ALTER TABLE tasks ADD COLUMN kind TEXT NOT NULL DEFAULT 'worker'`)
	db.Exec(`ALTER TABLE tasks ADD COLUMN agent TEXT NOT NULL DEFAULT ''`)
	// Added with the declarative lifecycle: what the task should be, which its
	// node reads, and what it should do (run or stop).
	db.Exec(`ALTER TABLE tasks ADD COLUMN spec TEXT NOT NULL DEFAULT ''`)
	db.Exec(`ALTER TABLE tasks ADD COLUMN desired TEXT NOT NULL DEFAULT 'running'`)
	// Tasks started before it have no spec for their node to read: they are lost.
	db.Exec(`UPDATE tasks SET state = 'lost' WHERE spec = '' AND state NOT IN ('stopped', 'failed', 'lost')`)
	// Added with joining: a node is known by the hash of its secret. Nodes
	// from the configuration file, which had none, are gone.
	if _, err := db.Exec(`ALTER TABLE nodes ADD COLUMN secret_hash TEXT NOT NULL DEFAULT ''`); err == nil {
		db.Exec(`DELETE FROM nodes WHERE secret_hash = ''`)
	}
	// Added with the two-pass scheduler: a task can wait, without a node,
	// for one that is offline now.
	db.Exec(`ALTER TABLE tasks ADD COLUMN pending_reason TEXT NOT NULL DEFAULT ''`)
	db.Exec(`ALTER TABLE messages ADD COLUMN via TEXT NOT NULL DEFAULT ''`)
	// Added with the tool catalog: what a task may use of it.
	db.Exec(`ALTER TABLE tasks ADD COLUMN tools TEXT NOT NULL DEFAULT ''`)
	// Added with folders: the user's folders a task may read (folders.go).
	db.Exec(`ALTER TABLE tasks ADD COLUMN folders TEXT NOT NULL DEFAULT ''`)
	// Added with named join tokens: the node a token is for, whether it may
	// replace that node's secret, and its public id (id.secret). Older
	// tokens have no name: they can join a new name only.
	db.Exec(`ALTER TABLE join_tokens ADD COLUMN name TEXT NOT NULL DEFAULT ''`)
	db.Exec(`ALTER TABLE join_tokens ADD COLUMN rejoin INTEGER NOT NULL DEFAULT 0`)
	db.Exec(`ALTER TABLE join_tokens ADD COLUMN id TEXT NOT NULL DEFAULT ''`)
	// Added with TLS for machines: the serial of the node's client
	// certificate, the one before it (still valid while the node moves to
	// the new one), and when the current one ends.
	db.Exec(`ALTER TABLE nodes ADD COLUMN cert_serial TEXT NOT NULL DEFAULT ''`)
	db.Exec(`ALTER TABLE nodes ADD COLUMN prev_serial TEXT NOT NULL DEFAULT ''`)
	db.Exec(`ALTER TABLE nodes ADD COLUMN cert_expires INTEGER NOT NULL DEFAULT 0`)
	// Added with the machine's system on its card: the last one it
	// reported, kept while it is offline.
	db.Exec(`ALTER TABLE nodes ADD COLUMN os TEXT NOT NULL DEFAULT ''`)
	db.Exec(`ALTER TABLE nodes ADD COLUMN arch TEXT NOT NULL DEFAULT ''`)
	db.Exec(`ALTER TABLE nodes ADD COLUMN install TEXT NOT NULL DEFAULT ''`)
	// Updates had a choice for all machines and for each one (tell me, at
	// night, never); now one switch, at night or not, for all of them
	// (machine_updates.go). "At night" for all stays on; the rest goes.
	db.Exec(`INSERT OR IGNORE INTO kv (key, value) SELECT 'update_night', '1' FROM kv WHERE key = 'update_policy' AND value = 'night'`)
	db.Exec(`DELETE FROM kv WHERE key = 'update_policy' OR key LIKE 'update_policy:%'`)
	return &Store{db: db, dir: filepath.Dir(path)}, nil
}

// joinToken is what a join token allows: joining as Name, and if Rejoin,
// replacing the secret of the node already registered under that name.
type joinToken struct {
	ID      string
	Name    string
	Rejoin  bool
	Expires time.Time
}

func (s *Store) addJoinToken(hash string, t joinToken) error {
	_, err := s.db.Exec(`INSERT INTO join_tokens (hash, expires, name, rejoin, id) VALUES (?, ?, ?, ?, ?)`,
		hash, t.Expires.Unix(), t.Name, t.Rejoin, t.ID)
	return err
}

var (
	errJoinToken  = errors.New("invalid or expired join token")
	errJoinName   = errors.New("this join token is for another node name")
	errNodeExists = errors.New("a node is already registered under this name: rejoining it needs a rejoin token for it")
)

// joinNode consumes a join token and registers the node name with its
// secret. A token only joins under the name it was made for (an older,
// unnamed token under a new name only), and replaces an existing node's
// secret only if it was made to rejoin that node. A refused join leaves the
// token unused.
func (s *Store) joinNode(tokenHash, name, secretHash string) (rejoined bool, err error) {
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM join_tokens WHERE expires < ?`, time.Now().Unix()); err != nil {
		return false, err
	}
	var tokName string
	var rejoin bool
	err = tx.QueryRow(`SELECT name, rejoin FROM join_tokens WHERE hash = ?`, tokenHash).Scan(&tokName, &rejoin)
	if errors.Is(err, sql.ErrNoRows) {
		return false, errJoinToken
	}
	if err != nil {
		return false, err
	}
	if tokName != "" && tokName != name {
		return false, errJoinName
	}
	var exists int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM nodes WHERE name = ?`, name).Scan(&exists); err != nil {
		return false, err
	}
	if exists > 0 && !(rejoin && tokName == name) {
		return false, errNodeExists
	}
	if _, err := tx.Exec(`DELETE FROM join_tokens WHERE hash = ?`, tokenHash); err != nil {
		return false, err
	}
	if _, err := tx.Exec(`INSERT INTO nodes (name, url, secret_hash) VALUES (?, '', ?)
		ON CONFLICT(name) DO UPDATE SET secret_hash = excluded.secret_hash,
		  cert_serial = '', prev_serial = '', cert_expires = 0`, name, secretHash); err != nil {
		return false, err
	}
	return exists > 0, tx.Commit()
}

func (s *Store) checkNode(name, secretHash string) bool {
	var stored string
	err := s.db.QueryRow(`SELECT secret_hash FROM nodes WHERE name = ?`, name).Scan(&stored)
	return err == nil && stored != "" && subtle.ConstantTimeCompare([]byte(stored), []byte(secretHash)) == 1
}

// setNodeCert makes serial the node's certificate, and its current one the
// previous. secretHash binds it to the secret that asked: a node that
// joined again meanwhile does not get it.
func (s *Store) setNodeCert(name, secretHash, serial string, expires time.Time) error {
	res, err := s.db.Exec(`UPDATE nodes SET prev_serial = cert_serial, cert_serial = ?, cert_expires = ?
		WHERE name = ? AND secret_hash = ?`, serial, expires.Unix(), name, secretHash)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return errors.New("the node changed meanwhile")
	}
	return nil
}

// nodeCertValid says whether serial is the node's certificate, or the one
// before it. Forgetting the node or letting it join again clears both.
func (s *Store) nodeCertValid(name, serial string) bool {
	var cur, prev string
	err := s.db.QueryRow(`SELECT cert_serial, prev_serial FROM nodes WHERE name = ?`, name).Scan(&cur, &prev)
	return err == nil && serial != "" && (serial == cur || serial == prev)
}

func (s *Store) hasNode(name string) bool {
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM nodes WHERE name = ?`, name).Scan(&n)
	return n == 1
}

func (s *Store) deleteNode(name string) error {
	_, err := s.db.Exec(`DELETE FROM nodes WHERE name = ?`, name)
	return err
}

func (s *Store) setNodeStatus(name string, online bool, info []byte) error {
	if online {
		_, err := s.db.Exec(`UPDATE nodes SET online = 1, info = ?, last_seen = ? WHERE name = ?`,
			string(info), time.Now().Unix(), name)
		if err != nil {
			return err
		}
		var sys struct {
			OS      string `json:"os"`
			Arch    string `json:"arch"`
			Install string `json:"install"`
		}
		if json.Unmarshal(info, &sys) == nil && sys.OS != "" {
			_, err = s.db.Exec(`UPDATE nodes SET os = ?, arch = ?, install = ? WHERE name = ?`, sys.OS, sys.Arch, sys.Install, name)
		}
		return err
	}
	_, err := s.db.Exec(`UPDATE nodes SET online = 0 WHERE name = ?`, name)
	return err
}

func (s *Store) nodes() ([]NodeRow, error) {
	rows, err := s.db.Query(`SELECT name, online, COALESCE(info, ''), last_seen, cert_expires, os, arch, install FROM nodes ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []NodeRow
	for rows.Next() {
		var n NodeRow
		var info string
		var seen, certExpires int64
		if err := rows.Scan(&n.Name, &n.Online, &info, &seen, &certExpires, &n.OS, &n.Arch, &n.Install); err != nil {
			return nil, err
		}
		if info != "" {
			n.Info = json.RawMessage(info)
		}
		n.LastSeen = time.Unix(seen, 0)
		if certExpires > 0 {
			n.CertExpires = time.Unix(certExpires, 0)
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func (s *Store) insertTask(t *TaskRow) error {
	now := time.Now()
	t.Created, t.Updated = now, now
	if t.Kind == "" {
		t.Kind = "worker"
	}
	_, err := s.db.Exec(`INSERT INTO tasks (id, node, agent, runtime, model, identity, dir, prompt, state, pending_reason, kind, tools, folders, created, updated)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, t.ID, t.Node, t.Agent, t.Runtime, t.Model, t.Identity, t.Dir, t.Prompt,
		t.State, t.PendingReason, t.Kind, t.Tools, t.Folders, now.Unix(), now.Unix())
	return err
}

func (s *Store) updateTask(id string, fields map[string]any) error {
	q, args := "UPDATE tasks SET updated = ?", []any{time.Now().Unix()}
	for k, v := range fields {
		q += ", " + k + " = ?"
		args = append(args, v)
	}
	_, err := s.db.Exec(q+" WHERE id = ?", append(args, id)...)
	return err
}

const taskColumns = `id, node, agent, node_task_id, runtime, model, identity, kind, tools, folders, dir, prompt, state, pending_reason, session_id, last_seq, created, updated`

func scanTask(sc interface{ Scan(...any) error }) (*TaskRow, error) {
	var t TaskRow
	var created, updated int64
	err := sc.Scan(&t.ID, &t.Node, &t.Agent, &t.NodeTaskID, &t.Runtime, &t.Model, &t.Identity, &t.Kind, &t.Tools, &t.Folders, &t.Dir, &t.Prompt,
		&t.State, &t.PendingReason, &t.SessionID, &t.LastSeq, &created, &updated)
	t.Created, t.Updated = time.Unix(created, 0), time.Unix(updated, 0)
	return &t, err
}

var errNoTask = errors.New("no such task")

func (s *Store) task(id string) (*TaskRow, error) {
	t, err := scanTask(s.db.QueryRow(`SELECT `+taskColumns+` FROM tasks WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errNoTask
	}
	return t, err
}

// tasks lists worker tasks, the most recent first; active only if asked.
// The brain is listed while it waits for a permission: only the user can
// answer it.
func (s *Store) tasks(activeOnly bool, limit int) ([]*TaskRow, error) {
	q := `SELECT ` + taskColumns + ` FROM tasks WHERE (kind = 'worker' OR (kind = 'brain' AND state = 'waiting_permission'))`
	if activeOnly {
		q += ` AND state NOT IN ('stopped', 'failed', 'lost')`
	}
	rows, err := s.db.Query(q+` ORDER BY created DESC LIMIT ?`, limit)
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

func (s *Store) addEvents(taskID string, events []EventRow) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	last := 0
	for _, e := range events {
		data, _ := json.Marshal(e.Data)
		if _, err := tx.Exec(`INSERT OR IGNORE INTO events (task_id, seq, time, type, data) VALUES (?, ?, ?, ?, ?)`,
			taskID, e.Seq, e.Time.Unix(), e.Type, string(data)); err != nil {
			return err
		}
		last = max(last, e.Seq)
	}
	if _, err := tx.Exec(`UPDATE tasks SET last_seq = MAX(last_seq, ?), updated = ? WHERE id = ?`,
		last, time.Now().Unix(), taskID); err != nil {
		return err
	}
	return tx.Commit()
}

// events returns the last n events of a task, oldest first.
func (s *Store) events(taskID string, n int) ([]EventRow, error) {
	rows, err := s.db.Query(`SELECT seq, time, type, data FROM
		(SELECT * FROM events WHERE task_id = ? ORDER BY seq DESC LIMIT ?) ORDER BY seq`, taskID, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []EventRow
	for rows.Next() {
		var e EventRow
		var t int64
		var data string
		if err := rows.Scan(&e.Seq, &t, &e.Type, &data); err != nil {
			return nil, err
		}
		e.Time = time.Unix(t, 0)
		json.Unmarshal([]byte(data), &e.Data)
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Store) activeCount(node string) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM tasks WHERE node = ? AND state NOT IN ('stopped', 'failed', 'lost', 'idle')`, node).Scan(&n)
	return n, err
}

// Conversation roles: what I write, what the brain answers, and the events
// eriphe-core reports to the brain.
const (
	roleUser  = "user"
	roleBrain = "brain"
	roleEvent = "event"
)

type Message struct {
	Seq  int       `json:"seq"`
	Time time.Time `json:"time"`
	Role string    `json:"role"`
	Text string    `json:"text"`
	// Via is where I said it, when not in the workspace or the CLI: "voix, Bureau".
	Via string `json:"via,omitempty"`
	// Attachments are the files of my message (attachments.go).
	Attachments []Attachment `json:"attachments,omitempty"`
}

func (s *Store) addMessage(role, text string, delivered bool) (int64, error) {
	return s.addMessageVia(role, text, "", delivered)
}

func (s *Store) addMessageVia(role, text, via string, delivered bool) (int64, error) {
	res, err := s.db.Exec(`INSERT INTO messages (time, role, text, delivered, via) VALUES (?, ?, ?, ?, ?)`,
		time.Now().Unix(), role, text, delivered, via)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// undelivered returns the messages and events waiting for the brain.
func (s *Store) undelivered() ([]Message, error) {
	return s.queryMessages(`SELECT seq, time, role, text, via FROM messages WHERE delivered = 0 AND role != 'brain' ORDER BY seq`)
}

// markDelivered also keeps the last message when the brain was given them:
// what it says after belongs to that turn (voice.go).
func (s *Store) markDelivered(upTo int) error {
	if _, err := s.db.Exec(`UPDATE messages SET delivered = 1 WHERE seq <= ? AND role != 'brain'`, upTo); err != nil {
		return err
	}
	return s.set(deliveredLastKey, strconv.Itoa(s.lastMessageSeq()))
}

func (s *Store) messagesAfter(after, limit int) ([]Message, error) {
	return s.queryMessages(`SELECT seq, time, role, text, via FROM messages WHERE seq > ? ORDER BY seq LIMIT ?`, after, limit)
}

func (s *Store) lastMessageSeq() int {
	var n int
	s.db.QueryRow(`SELECT COALESCE(MAX(seq), 0) FROM messages`).Scan(&n)
	return n
}

func (s *Store) queryMessages(q string, args ...any) ([]Message, error) {
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Message
	for rows.Next() {
		var m Message
		var t int64
		if err := rows.Scan(&m.Seq, &t, &m.Role, &m.Text, &m.Via); err != nil {
			return nil, err
		}
		m.Time = time.Unix(t, 0)
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close() // one connection: the attachments' query needs it
	return out, s.withAttachments(out)
}

func (s *Store) get(key string) string {
	var v string
	s.db.QueryRow(`SELECT value FROM kv WHERE key = ?`, key).Scan(&v)
	return v
}

func (s *Store) set(key, value string) error {
	_, err := s.db.Exec(`INSERT INTO kv (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

// eventsAfter returns up to limit events of a task after seq, oldest first.
func (s *Store) eventsAfter(taskID string, after, limit int) ([]EventRow, error) {
	rows, err := s.db.Query(`SELECT seq, time, type, data FROM events WHERE task_id = ? AND seq > ? ORDER BY seq LIMIT ?`,
		taskID, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []EventRow
	for rows.Next() {
		var e EventRow
		var t int64
		var data string
		if err := rows.Scan(&e.Seq, &t, &e.Type, &data); err != nil {
			return nil, err
		}
		e.Time = time.Unix(t, 0)
		json.Unmarshal([]byte(data), &e.Data)
		out = append(out, e)
	}
	return out, rows.Err()
}
