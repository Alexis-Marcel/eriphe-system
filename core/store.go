package main

import (
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// Task states, mirrored from the device, plus the ones only eriphe-core knows.
const (
	statePending           = "pending" // not yet accepted by a device
	stateRunning           = "running"
	stateWaitingPermission = "waiting_permission"
	stateIdle              = "idle" // the agent's turn is over: done, or asking something
	stateStopped           = "stopped"
	stateFailed            = "failed"
	stateLost              = "lost" // the device no longer knows the task
)

func finished(state string) bool {
	return state == stateStopped || state == stateFailed || state == stateLost
}

type Store struct {
	db  *sql.DB
	dir string // the data folder, where the database is
}

type DeviceRow struct {
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
	// Its access is cut (device_access.go): no work, no folder, no tunnel.
	Suspended bool `json:"suspended,omitempty"`
	// Asleep: away, but it comes back by itself (device_sleep.go). Not
	// stored: set where it is shown.
	Asleep bool `json:"asleep,omitempty"`
}

type TaskRow struct {
	ID     string `json:"id"`
	Device string `json:"device"` // empty while the task waits for a device
	// Why a pending task has no device yet.
	PendingReason string    `json:"pending_reason,omitempty"`
	Agent         string    `json:"agent,omitempty"`
	DeviceTaskID  string    `json:"device_task_id,omitempty"`
	Runtime       string    `json:"runtime"`
	Model         string    `json:"model,omitempty"`
	Kind          string    `json:"kind,omitempty"`    // worker or brain
	Tools         string    `json:"tools,omitempty"`   // its grant from the catalog, comma-separated
	Folders       string    `json:"folders,omitempty"` // the user's folders lent to it, by name, comma-separated (folders.go)
	Dir           string    `json:"dir"`
	Title         string    `json:"title,omitempty"` // what the user reads, if it was given
	Prompt        string    `json:"prompt"`
	State         string    `json:"state"`
	SessionID     string    `json:"session_id,omitempty"`
	LastSeq       int       `json:"-"`
	Created       time.Time `json:"created"`
	Updated       time.Time `json:"updated"`

	// Resources is all it was lent (resources.go); nil for a task from before.
	Resources []ResourceRef `json:"resources,omitempty"`
	// Pinned: its device was chosen by hand, and it never moves.
	Pinned bool `json:"pinned,omitempty"`
	// About is what it is about: its node, its skill's, the brain's
	// turn's, or the one it was given (about.go).
	About string `json:"about,omitempty"`
	// Desired, when inserted: held while its start waits for the user
	// (task_start.go), running otherwise. Not read back.
	Desired string `json:"-"`
}

type EventRow struct {
	Seq  int            `json:"seq"`
	Time time.Time      `json:"time"`
	Type string         `json:"type"`
	Data map[string]any `json:"data"`
}

const schema = `
CREATE TABLE IF NOT EXISTS devices (
  name TEXT PRIMARY KEY, url TEXT NOT NULL, online INTEGER NOT NULL DEFAULT 0,
  info TEXT, last_seen INTEGER NOT NULL DEFAULT 0,
  secret_hash TEXT NOT NULL DEFAULT '',  -- a device is known by the hash of its secret
  cert_serial TEXT NOT NULL DEFAULT '',  -- its client certificate's serial (machine_tls.go)
  prev_serial TEXT NOT NULL DEFAULT '',  -- the one before, valid while it moves to the new one
  cert_expires INTEGER NOT NULL DEFAULT 0,
  os TEXT NOT NULL DEFAULT '', arch TEXT NOT NULL DEFAULT '', install TEXT NOT NULL DEFAULT '', -- its last report
  suspended INTEGER NOT NULL DEFAULT 0); -- "Cut access" (device_access.go)
CREATE TABLE IF NOT EXISTS tasks (
  id TEXT PRIMARY KEY, device TEXT NOT NULL, device_task_id TEXT NOT NULL DEFAULT '',
  runtime TEXT NOT NULL, model TEXT NOT NULL DEFAULT '',
  dir TEXT NOT NULL, prompt TEXT NOT NULL, state TEXT NOT NULL,
  session_id TEXT NOT NULL DEFAULT '', last_seq INTEGER NOT NULL DEFAULT 0,
  created INTEGER NOT NULL, updated INTEGER NOT NULL,
  kind TEXT NOT NULL DEFAULT 'worker', agent TEXT NOT NULL DEFAULT '', title TEXT NOT NULL DEFAULT '',
  spec TEXT NOT NULL DEFAULT '',             -- what its device reads (lifecycle.go)
  desired TEXT NOT NULL DEFAULT 'running',   -- run or stop
  pending_reason TEXT NOT NULL DEFAULT '',   -- why it waits for a device
  tools TEXT NOT NULL DEFAULT '', folders TEXT NOT NULL DEFAULT '',
  resources TEXT NOT NULL DEFAULT '',        -- all it is lent, as JSON (resources.go)
  pinned INTEGER NOT NULL DEFAULT 0,         -- its device given by hand (placement.go)
  about TEXT NOT NULL DEFAULT '');         -- what it is about: node:<id> or an entity's key (about.go)
CREATE TABLE IF NOT EXISTS messages (
  seq INTEGER PRIMARY KEY AUTOINCREMENT, time INTEGER NOT NULL,
  role TEXT NOT NULL, text TEXT NOT NULL, delivered INTEGER NOT NULL DEFAULT 0,
  via TEXT NOT NULL DEFAULT '',
  node TEXT NOT NULL DEFAULT '',   -- the node it belongs to, '' for the home (node_turn.go)
  filed TEXT NOT NULL DEFAULT ''); -- the brain's answer: the node code filed it in (node_net.go)
CREATE TABLE IF NOT EXISTS kv (key TEXT PRIMARY KEY, value TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS inbox (
  seq INTEGER PRIMARY KEY AUTOINCREMENT, task_id TEXT NOT NULL, kind TEXT NOT NULL,
  data TEXT NOT NULL, created INTEGER NOT NULL, acked INTEGER NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS join_tokens (
  hash TEXT PRIMARY KEY, expires INTEGER NOT NULL,
  name TEXT NOT NULL DEFAULT '',      -- the device it is for
  rejoin INTEGER NOT NULL DEFAULT 0,  -- it may replace that device's secret
  id TEXT NOT NULL DEFAULT '');       -- its public id (id.secret)
CREATE TABLE IF NOT EXISTS events (
  task_id TEXT NOT NULL, seq INTEGER NOT NULL, time INTEGER NOT NULL,
  type TEXT NOT NULL, data TEXT NOT NULL, PRIMARY KEY (task_id, seq));
`

// extraSchema holds the tables of other modules (the policy's decisions).
var extraSchema []string

// schemaFixes bring a table made by an older eriphe-core up to date (a
// column added), after the schema: CREATE TABLE IF NOT EXISTS never does.
var schemaFixes []func(*sql.DB) error

// A conversation made before the nodes (#632) gets its messages' node and
// filed; its thread column is left, unread.
func init() {
	schemaFixes = append(schemaFixes, func(db *sql.DB) error {
		for _, col := range []string{"node", "filed"} {
			if err := addColumn(db, "messages", col, "TEXT NOT NULL DEFAULT ''"); err != nil {
				return err
			}
		}
		_, err := db.Exec(`CREATE INDEX IF NOT EXISTS messages_node ON messages (node, seq)`)
		return err
	})
}

// addColumn adds a column to a table that lacks it; a table that has it
// is left as it is.
func addColumn(db *sql.DB, table, column, def string) error {
	rows, err := db.Query(`SELECT name FROM pragma_table_info(?)`, table)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return err
		}
		if name == column {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	rows.Close()
	_, err = db.Exec(`ALTER TABLE ` + table + ` ADD COLUMN ` + column + ` ` + def)
	return err
}

func openStore(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=temp_store(memory)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	for _, q := range append([]string{schema}, extraSchema...) {
		if _, err := db.Exec(q); err != nil {
			return nil, err
		}
	}
	for _, fix := range schemaFixes {
		if err := fix(db); err != nil {
			return nil, err
		}
	}
	return &Store{db: db, dir: filepath.Dir(path)}, nil
}

// joinToken is what a join token allows: joining as Name, and if Rejoin,
// replacing the secret of the device already registered under that name.
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

// joinTokenUnused says whether the join token named id is still there to
// join with: neither used nor expired.
func (s *Store) joinTokenUnused(id string) bool {
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM join_tokens WHERE id = ? AND expires >= ?`, id, time.Now().Unix()).Scan(&n)
	return n > 0
}

var (
	errJoinToken    = errors.New("invalid or expired join token")
	errJoinName     = errors.New("this join token is for another device name")
	errDeviceExists = errors.New("a device is already registered under this name: rejoining it needs a rejoin token for it")
)

// joinDevice consumes a join token and registers the device name with its
// secret. A token only joins under the name it was made for (an older,
// unnamed token under a new name only), and replaces an existing device's
// secret only if it was made to rejoin that device. A refused join leaves the
// token unused.
func (s *Store) joinDevice(tokenHash, name, secretHash string) (rejoined bool, err error) {
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
	if err := tx.QueryRow(`SELECT COUNT(*) FROM devices WHERE name = ?`, name).Scan(&exists); err != nil {
		return false, err
	}
	if exists > 0 && !(rejoin && tokName == name) {
		return false, errDeviceExists
	}
	if _, err := tx.Exec(`DELETE FROM join_tokens WHERE hash = ?`, tokenHash); err != nil {
		return false, err
	}
	if _, err := tx.Exec(`INSERT INTO devices (name, url, secret_hash) VALUES (?, '', ?)
		ON CONFLICT(name) DO UPDATE SET secret_hash = excluded.secret_hash,
		  cert_serial = '', prev_serial = '', cert_expires = 0`, name, secretHash); err != nil {
		return false, err
	}
	return exists > 0, tx.Commit()
}

func (s *Store) checkDevice(name, secretHash string) bool {
	var stored string
	err := s.db.QueryRow(`SELECT secret_hash FROM devices WHERE name = ?`, name).Scan(&stored)
	return err == nil && stored != "" && subtle.ConstantTimeCompare([]byte(stored), []byte(secretHash)) == 1
}

// setDeviceCert makes serial the device's certificate, and its current one the
// previous. secretHash binds it to the secret that asked: a device that
// joined again meanwhile does not get it.
func (s *Store) setDeviceCert(name, secretHash, serial string, expires time.Time) error {
	res, err := s.db.Exec(`UPDATE devices SET prev_serial = cert_serial, cert_serial = ?, cert_expires = ?
		WHERE name = ? AND secret_hash = ?`, serial, expires.Unix(), name, secretHash)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return errors.New("the device changed meanwhile")
	}
	return nil
}

// deviceCertValid says whether serial is the device's certificate, or the one
// before it. Forgetting the device or letting it join again clears both.
func (s *Store) deviceCertValid(name, serial string) bool {
	var cur, prev string
	err := s.db.QueryRow(`SELECT cert_serial, prev_serial FROM devices WHERE name = ?`, name).Scan(&cur, &prev)
	return err == nil && serial != "" && (serial == cur || serial == prev)
}

func (s *Store) hasDevice(name string) bool {
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM devices WHERE name = ?`, name).Scan(&n)
	return n == 1
}

func (s *Store) deleteDevice(name string) error {
	if _, err := s.db.Exec(`DELETE FROM devices WHERE name = ?`, name); err != nil {
		return err
	}
	// Whether it may be woken (wake.go).
	s.forgetWake(name)
	_, err := s.db.Exec(`DELETE FROM device_log WHERE device = ?`, name) // its access log (device_access.go)
	return err
}

func (s *Store) setDeviceStatus(name string, online bool, info []byte) error {
	if online {
		_, err := s.db.Exec(`UPDATE devices SET online = 1, info = ?, last_seen = ? WHERE name = ?`,
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
			_, err = s.db.Exec(`UPDATE devices SET os = ?, arch = ?, install = ? WHERE name = ?`, sys.OS, sys.Arch, sys.Install, name)
		}
		return err
	}
	_, err := s.db.Exec(`UPDATE devices SET online = 0 WHERE name = ?`, name)
	return err
}

func (s *Store) devices() ([]DeviceRow, error) {
	rows, err := s.db.Query(`SELECT name, online, COALESCE(info, ''), last_seen, cert_expires, os, arch, install, suspended FROM devices ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DeviceRow
	for rows.Next() {
		var n DeviceRow
		var info string
		var seen, certExpires int64
		if err := rows.Scan(&n.Name, &n.Online, &info, &seen, &certExpires, &n.OS, &n.Arch, &n.Install, &n.Suspended); err != nil {
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
	_, err := s.db.Exec(`INSERT INTO tasks (id, device, agent, runtime, model, dir, title, prompt, state, pending_reason, kind, tools, folders, resources, pinned, desired, about, created, updated)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, t.ID, t.Device, t.Agent, t.Runtime, t.Model, t.Dir, t.Title, t.Prompt,
		t.State, t.PendingReason, t.Kind, t.Tools, t.Folders, marshalResources(t.Resources), t.Pinned, orStr(t.Desired, desiredRunning), t.About, now.Unix(), now.Unix())
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

// taskTitleMax is as long as a task's title gets.
const taskTitleMax = 110

// label is how the user reads a task: its title, or its prompt's first line.
func (t *TaskRow) label() string {
	if t.Title != "" {
		return t.Title
	}
	line := strings.TrimSpace(t.Prompt)
	if i := strings.IndexByte(line, '\n'); i > 0 {
		line = line[:i]
	}
	return truncate(line, taskTitleMax)
}

const taskColumns = `id, device, agent, device_task_id, runtime, model, kind, tools, folders, resources, dir, title, prompt, state, pending_reason, session_id, last_seq, created, updated, pinned, about`

func scanTask(sc interface{ Scan(...any) error }) (*TaskRow, error) {
	var t TaskRow
	var created, updated int64
	var resources string
	err := sc.Scan(&t.ID, &t.Device, &t.Agent, &t.DeviceTaskID, &t.Runtime, &t.Model, &t.Kind, &t.Tools, &t.Folders, &resources, &t.Dir, &t.Title, &t.Prompt,
		&t.State, &t.PendingReason, &t.SessionID, &t.LastSeq, &created, &updated, &t.Pinned, &t.About)
	t.Resources = unmarshalResources(resources)
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

func (s *Store) activeCount(device string) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM tasks WHERE device = ? AND state NOT IN ('stopped', 'failed', 'lost', 'idle')`, device).Scan(&n)
	return n, err
}

// Conversation roles: what I write, what the brain answers, the events
// eriphe-core reports to the brain, and the brain's own failures (a turn
// that failed, a start that keeps failing: brain_errors.go), which are the
// user's to see and never the brain's to read.
const (
	roleUser  = "user"
	roleBrain = "brain"
	roleEvent = "event"
	roleError = "error"
)

type Message struct {
	Seq  int       `json:"seq"`
	Time time.Time `json:"time"`
	Role string    `json:"role"`
	Text string    `json:"text"`
	// Via is where I said it, when not in the workspace or the CLI: "voix,
	// Bureau". For an error, where the brain failed: "turn" or "start".
	Via string `json:"via,omitempty"`
	// Node is the node the message belongs to, "" for the home: mine
	// written to it, the brain's answer to it, its events (node_turn.go).
	Node string `json:"node,omitempty"`
	// Filed is, for the brain's answer, the node code filed it in, as
	// nothing of the turn had (node_net.go): the answer links to it.
	Filed string `json:"filed,omitempty"`
	// Attachments are the files of my message (attachments.go).
	Attachments []Attachment `json:"attachments,omitempty"`
	// Notice is what the workspace shows of an event: nil for the brain's
	// own notes (web_notices.go).
	Notice *eventNotice `json:"-"`
	// Cards are the cards shown with a message of the brain's, and Why
	// why it was sent when it was a notification (web_convo_cards.go).
	Cards []cardView `json:"-"`
	Why   *msgWhy    `json:"-"`
}

// messageCols are a message's columns, as queryMessages reads them.
const messageCols = "seq, time, role, text, via, node, filed"

func (s *Store) addMessage(role, text string, delivered bool) (int64, error) {
	return s.addMessageVia(role, text, "", delivered)
}

func (s *Store) addMessageVia(role, text, via string, delivered bool) (int64, error) {
	return s.addNodeMessage(role, text, via, "", delivered)
}

// addNodeMessage adds a message that belongs to a node ("": the home).
func (s *Store) addNodeMessage(role, text, via, node string, delivered bool) (int64, error) {
	res, err := s.db.Exec(`INSERT INTO messages (time, role, text, delivered, via, node) VALUES (?, ?, ?, ?, ?, ?)`,
		time.Now().Unix(), role, text, delivered, via, node)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// undelivered returns the messages and events waiting for the brain.
func (s *Store) undelivered() ([]Message, error) {
	return s.queryMessages(`SELECT ` + messageCols + ` FROM messages WHERE delivered = 0 AND role IN ('user', 'event') ORDER BY seq`)
}

// undeliver hands the messages from..to back to the brain: the task they
// went to never received them (placement.go).
func (s *Store) undeliver(from, to int) error {
	_, err := s.db.Exec(`UPDATE messages SET delivered = 0 WHERE seq >= ? AND seq <= ? AND role IN ('user', 'event')`, from, to)
	return err
}

// markDelivered also keeps the last message when the brain was given them:
// what it says after belongs to that turn (channel_in.go).
func (s *Store) markDelivered(upTo int) error {
	if _, err := s.db.Exec(`UPDATE messages SET delivered = 1 WHERE seq <= ? AND role != 'brain'`, upTo); err != nil {
		return err
	}
	return s.set(deliveredLastKey, strconv.Itoa(s.lastMessageSeq()))
}

func (s *Store) messagesAfter(after, limit int) ([]Message, error) {
	return s.queryMessages(`SELECT `+messageCols+` FROM messages WHERE seq > ? ORDER BY seq LIMIT ?`, after, limit)
}

// hasBrainMessage tells whether the brain has ever written in the conversation.
func (s *Store) hasBrainMessage() bool {
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE role = ? LIMIT 1`, roleBrain).Scan(&n)
	return n > 0
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
		if err := rows.Scan(&m.Seq, &t, &m.Role, &m.Text, &m.Via, &m.Node, &m.Filed); err != nil {
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

// swap sets key to value only while it holds old, and tells whether it did.
func (s *Store) swap(key, old, value string) bool {
	res, err := s.db.Exec(`UPDATE kv SET value = ? WHERE key = ? AND value = ?`, value, key, old)
	if err != nil {
		return false
	}
	n, _ := res.RowsAffected()
	return n == 1
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
