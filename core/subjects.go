package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The known subjects. Every subject key written (a thread's, a memory's,
// an intention's subject and parties, a notification's, a topic rule's)
// is noted in the subjects table of core.db: its key, a title, the other
// names it goes by (aliases) and when it was last seen. From it the code
// tells the subjects a message of the user names (subjectsNamed), and the
// turn is about them (brainTurn.subjects).
//
// Two keys for one matter (project:site and project:site-web) are merged
// when an alias says so (name_subject): the one merged keeps a row that
// points to the other (merged_into), every record under it is rewritten,
// and a key written later is kept under the one it was merged into.

const subjectsSchema = `CREATE TABLE IF NOT EXISTS subjects (
  key TEXT PRIMARY KEY,
  title TEXT NOT NULL DEFAULT '',
  aliases TEXT NOT NULL DEFAULT '[]',     -- other names, as written
  merged_into TEXT NOT NULL DEFAULT '',   -- the key it is kept under now
  first_seen INTEGER NOT NULL, last_seen INTEGER NOT NULL);`

func init() { extraSchema = append(extraSchema, subjectsSchema) }

// knownSubject is a row of the subjects table.
type knownSubject struct {
	Key        string    `json:"key"`
	Title      string    `json:"title,omitempty"`
	Aliases    []string  `json:"aliases,omitempty"`
	MergedInto string    `json:"merged_into,omitempty"`
	LastSeen   time.Time `json:"last_seen"`
}

const (
	subjectTitleMax  = 120
	subjectAliasMax  = 60
	subjectAliasesMx = 12
	subjectsNamedMax = 5 // subjects a message names, at most
)

// subjectBook is what the memory asks of the known subjects: the key one
// is kept under, noted as seen (keep), or only looked up (canon).
type subjectBook interface {
	keepSubject(key, title string) string
	canonSubject(key string) string
}

// canonSubject is the key a subject is kept under: the one it was merged
// into, if it was; "" stays "".
func (s *Store) canonSubject(key string) string {
	for range 5 {
		var into string
		if key == "" || s.db.QueryRow(`SELECT merged_into FROM subjects WHERE key = ?`, key).Scan(&into) != nil || into == "" {
			return key
		}
		key = into
	}
	return key
}

// keepSubject notes a key written now; its canonical key is returned.
func (s *Store) keepSubject(key, title string) string {
	return s.keepSubjectAt(key, title, time.Now())
}

// keepSubjectAt notes a key seen at a time: a new row, or a later last
// seen; a title is kept once given. A record ref is no subject.
func (s *Store) keepSubjectAt(key, title string, at time.Time) string {
	if k, err := subjectKey(key); err != nil || k != key || k == "" {
		return key // none, or not a key: a record's ref, the assistant
	}
	key = s.canonSubject(key)
	title = truncate(oneLine(title), subjectTitleMax)
	if _, err := s.db.Exec(`INSERT INTO subjects (key, title, first_seen, last_seen) VALUES (?, ?, ?, ?)
		ON CONFLICT (key) DO UPDATE SET last_seen = MAX(last_seen, excluded.last_seen),
		  title = CASE WHEN title = '' THEN excluded.title ELSE title END`, key, title, at.Unix(), at.Unix()); err != nil {
		log.Printf("subjects: %s: %v", key, err)
	}
	return key
}

// knownSubject is a subject's row; ok false if it is not known.
func (s *Store) knownSubject(key string) (knownSubject, bool) {
	rows, err := s.subjectsWhere(`key = ?`, key)
	if err != nil || len(rows) == 0 {
		return knownSubject{}, false
	}
	return rows[0], true
}

// subjectsWhere are the rows that match, the latest seen first.
func (s *Store) subjectsWhere(cond string, args ...any) ([]knownSubject, error) {
	rows, err := s.db.Query(`SELECT key, title, aliases, merged_into, last_seen FROM subjects WHERE `+cond+` ORDER BY last_seen DESC, key`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []knownSubject
	for rows.Next() {
		var k knownSubject
		var aliases string
		var seen int64
		if err := rows.Scan(&k.Key, &k.Title, &aliases, &k.MergedInto, &seen); err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(aliases), &k.Aliases)
		k.LastSeen = time.Unix(seen, 0)
		out = append(out, k)
	}
	return out, rows.Err()
}

// subjectIs is the key a subject given by someone is kept under, noted as
// seen: its key (subjectKey), or the one it was merged into.
func (c *core) subjectIs(s, title string) (string, error) {
	k, err := subjectKey(s)
	if err != nil {
		return "", err
	}
	return c.store.keepSubject(k, title), nil
}

// subjectLookup is the key to look a subject up by: its key, or the one it
// was merged into; nothing is noted.
func (c *core) subjectLookup(s string) (string, error) {
	k, err := subjectKey(s)
	if err != nil {
		return "", err
	}
	return c.store.canonSubject(k), nil
}

// useMemory gives eriphe-core its memory, which notes its subjects here.
func (c *core) useMemory(m *memStore) {
	c.mem = m
	if m != nil {
		m.subjects = c.store
	}
}

// keepSubject is the memory's subject as kept, noted as seen.
func (m *memStore) keepSubject(key string) string {
	if m.subjects == nil {
		return key
	}
	return m.subjects.keepSubject(key, "")
}

// canonSubject is the memory's subject to look up by.
func (m *memStore) canonSubject(key string) string {
	if m.subjects == nil {
		return key
	}
	return m.subjects.canonSubject(key)
}

// subjectsFilled is the kv key that says the subjects table holds the
// subjects written before it existed.
const subjectsFilled = "subjects_filled"

// fillSubjects notes, once, the subjects already written in core.db and
// memory.db, each seen when its latest record was.
func (c *core) fillSubjects() error {
	if c.store.get(subjectsFilled) != "" {
		return nil
	}
	type seen struct {
		key, title string
		at         int64
	}
	var all []seen
	collect := func(db *sql.DB, q string) error {
		rows, err := db.Query(q)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var s seen
			if err := rows.Scan(&s.key, &s.title, &s.at); err != nil {
				return err
			}
			all = append(all, s)
		}
		return rows.Err()
	}
	queries := []string{
		`SELECT subject, title, MAX(created) FROM threads WHERE subject != '' GROUP BY subject`,
		`SELECT subject, '', MAX(created) FROM notifications WHERE subject != '' GROUP BY subject`,
		`SELECT subject, '', MAX(updated) FROM tasks WHERE subject != '' GROUP BY subject`,
	}
	for _, q := range queries {
		if err := collect(c.store.db, q); err != nil {
			return err
		}
	}
	if rules, err := c.notifyRules(); err == nil {
		for _, r := range rules {
			if r.Kind == ruleTopic && r.Match != "" {
				all = append(all, seen{key: r.Match, at: time.Now().Unix()})
			}
		}
	}
	if c.mem != nil {
		for _, q := range []string{
			`SELECT subject, '', MAX(updated_at) FROM memories WHERE subject != '' AND status != 'deleted' GROUP BY subject`,
			`SELECT subject, '', MAX(updated_at) FROM intentions WHERE subject != '' GROUP BY subject`,
			`SELECT owed_to, '', MAX(updated_at) FROM intentions WHERE owed_to != '' GROUP BY owed_to`,
		} {
			if err := collect(c.mem.db, q); err != nil {
				return err
			}
		}
	}
	for _, s := range all {
		if k, err := subjectKey(s.key); err == nil && k == s.key {
			c.store.keepSubjectAt(k, s.title, time.Unix(s.at, 0))
		}
	}
	c.store.set(subjectsFilled, "1")
	return nil
}

// subjectNames are the names a subject is told by in a message: its
// slug's words for a named kind, and its aliases. owner, home, the ids
// and the system's own topics have none of their own.
func subjectNames(k knownSubject) []string {
	var names []string
	kind, slug, _ := strings.Cut(k.Key, ":")
	switch kind {
	case subjProject, subjPerson, subjClient, subjTool, subjTopic:
		if !systemSubject(k.Key) {
			names = append(names, slug)
		}
	}
	return append(names, k.Aliases...)
}

// systemSubject tells a subject of Eriphe's own: the workspace, its wakes,
// its journal.
func systemSubject(key string) bool {
	return key == workspaceSubject || key == heartbeatSubject || key == journalSubject
}

// nameStems are the stems of a name's words, in order.
func nameStems(name string) []string {
	var out []string
	for _, w := range memWords(foldAccents(name)) {
		out = append(out, stemWord(w))
	}
	return out
}

// subjectNameMin is the fewest letters of a subject's name told in a
// message: a shorter one would be found everywhere.
const subjectNameMin = 3

// textStems are the stems of a text's words, as a set.
func textStems(text string) map[string]bool {
	set := map[string]bool{}
	for _, w := range memWords(foldAccents(text)) {
		set[stemWord(w)] = true
	}
	return set
}

// namedIn tells whether every word of a name is in the text.
func namedIn(text map[string]bool, name []string) bool {
	if len(name) == 0 {
		return false
	}
	for _, w := range name {
		if !text[w] {
			return false
		}
	}
	return true
}

// subjectsNamed are the known subjects a text names, by their names and
// aliases: the latest seen first, at most subjectsNamedMax. A subject
// whose name is part of another's that is named too (site in site web)
// gives way to it.
func (c *core) subjectsNamed(text string) []string {
	words := textStems(text)
	if len(words) == 0 {
		return nil
	}
	known, err := c.store.subjectsWhere(`merged_into = ''`)
	if err != nil {
		log.Printf("subjects: %v", err)
		return nil
	}
	type hit struct {
		key  string
		name []string
	}
	var hits []hit
	for _, k := range known {
		var best []string
		for _, n := range subjectNames(k) {
			if s := nameStems(n); len(strings.Join(s, "")) >= subjectNameMin && namedIn(words, s) && len(s) > len(best) {
				best = s
			}
		}
		if best != nil {
			hits = append(hits, hit{k.Key, best})
		}
	}
	var out []string
	for i, h := range hits {
		inside := slices.ContainsFunc(hits, func(o hit) bool {
			return o.key != h.key && len(o.name) > len(h.name) && namedIn(setOf(o.name), h.name)
		})
		if !inside && len(out) < subjectsNamedMax {
			out = append(out, hits[i].key)
		}
	}
	return out
}

func setOf(xs []string) map[string]bool {
	m := map[string]bool{}
	for _, x := range xs {
		m[x] = true
	}
	return m
}

// subjectsBlock is what the user's messages name of the known subjects,
// for the brain, and the turn is about them; "" if none. Only keys are
// written: a title or an alias may come from what someone else wrote.
func (c *core) subjectsBlock(msgs []Message) string {
	var named []string
	for _, m := range msgs {
		if m.Role != roleUser {
			continue
		}
		for _, k := range c.subjectsNamed(m.Text) {
			if !slices.Contains(named, k) {
				named = append(named, k)
			}
		}
	}
	c.turn.name(named)
	if len(named) == 0 {
		return ""
	}
	var parts []string
	for _, k := range named {
		line := k
		if th, ok := c.liveThreadOf(k); ok {
			line += fmt.Sprintf(" (thread %d)", th)
		}
		parts = append(parts, line)
	}
	return "[sujets] Ce message nomme des sujets connus : " + strings.Join(parts, ", ") + "."
}

// liveThreadOf is the live thread of a subject, if it has one.
func (c *core) liveThreadOf(key string) (int64, bool) {
	var id int64
	err := c.store.db.QueryRow(`SELECT id FROM threads WHERE subject = ? AND `+activeThreads+` LIMIT 1`, key).Scan(&id)
	return id, err == nil
}

// name records the subjects the turn's messages name; a new delivery
// never keeps those of the turn before.
func (t *brainTurn) name(keys []string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !slices.Equal(t.subjects, keys) {
		t.subjects = slices.Clone(keys)
		t.save()
	}
}

// named are the subjects the turn's messages name.
func (t *brainTurn) named() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return slices.Clone(t.subjects)
}

// nameSubjectIn is name_subject's input.
type nameSubjectIn struct {
	Subject string   `json:"subject" jsonschema:"the subject, kind:slug (project:site)"`
	Title   string   `json:"title,omitempty" jsonschema:"what the user calls it, in a few words: Site web"`
	Aliases []string `json:"aliases,omitempty" jsonschema:"other names the user gives it (Velora, le site); another subject's key (project:site-web), or a name that is another subject's of the same kind, merges that subject into this one"`
}

// nameSubject gives a subject a title and aliases, and merges into it the
// subjects its aliases say are the same. Never in a turn that read
// external content: a merge rewrites records and is not undone.
func (c *core) nameSubject(in nameSubjectIn) (map[string]any, error) {
	if tainted := c.turnTaint(); len(tainted) > 0 {
		return nil, fmt.Errorf("refused: this turn read external content (%s); name a subject in a turn of the user's own", strings.Join(tainted, ", "))
	}
	key, err := c.subjectIs(in.Subject, "")
	if err != nil {
		return nil, fmt.Errorf("subject: %w", err)
	}
	if key == "" {
		return nil, errors.New("subject is needed")
	}
	if len(in.Aliases) > subjectAliasesMx {
		return nil, fmt.Errorf("at most %d aliases", subjectAliasesMx)
	}
	kind, _, _ := strings.Cut(key, ":")
	var merged []string
	var names []string
	for _, a := range in.Aliases {
		a = oneLine(a)
		if a == "" {
			continue
		}
		if strings.Contains(a, ":") { // another subject's key
			other, err := c.subjectLookup(a)
			if err != nil {
				return nil, fmt.Errorf("alias %s: %w", a, err)
			}
			if other == key {
				continue
			}
			if _, ok := c.store.knownSubject(other); !ok {
				return nil, fmt.Errorf("alias %s: no such subject; give a name, not a key", a)
			}
			if err := c.mergeSubject(other, key); err != nil {
				return nil, err
			}
			merged = append(merged, other)
			continue
		}
		if len([]rune(a)) > subjectAliasMax {
			return nil, fmt.Errorf("an alias is %d characters at most", subjectAliasMax)
		}
		names = append(names, a)
		// a name that is another subject's of the same kind: the same matter
		stems := nameStems(a)
		same, _ := c.store.subjectsWhere(`merged_into = '' AND key LIKE ? AND key != ?`, kind+":%", key)
		for _, o := range same {
			_, slug, _ := strings.Cut(o.Key, ":")
			if len(stems) > 0 && slices.Equal(nameStems(slug), stems) {
				if err := c.mergeSubject(o.Key, key); err != nil {
					return nil, err
				}
				merged = append(merged, o.Key)
			}
		}
	}
	if err := c.addSubjectNames(key, oneLine(in.Title), names); err != nil {
		return nil, err
	}
	k, _ := c.store.knownSubject(key)
	out := map[string]any{"subject": k}
	if len(merged) > 0 {
		out["merged"] = merged
		out["note"] = "Merged into " + key + ": their threads, memories, intentions, notifications and rules are under it now, and a key merged is kept under it from now on."
	}
	return out, nil
}

// addSubjectNames sets a subject's title (if given) and adds aliases.
func (c *core) addSubjectNames(key, title string, aliases []string) error {
	k, _ := c.store.knownSubject(key)
	for _, a := range aliases {
		if !slices.ContainsFunc(k.Aliases, func(b string) bool { return strings.EqualFold(foldAccents(a), foldAccents(b)) }) {
			k.Aliases = append(k.Aliases, a)
		}
	}
	if len(k.Aliases) > subjectAliasesMx {
		k.Aliases = k.Aliases[len(k.Aliases)-subjectAliasesMx:]
	}
	if k.Aliases == nil {
		k.Aliases = []string{}
	}
	raw, _ := json.Marshal(k.Aliases)
	if title == "" {
		title = k.Title
	}
	_, err := c.store.db.Exec(`UPDATE subjects SET title = ?, aliases = ? WHERE key = ?`, truncate(title, subjectTitleMax), string(raw), key)
	return err
}

// mergeSubject merges a subject into another: the row of the one merged
// points to the other, its names become the other's aliases, and every
// record under it is rewritten (threads, notifications, attention items,
// cards, tasks, surfaces, topic rules, memories, intentions). Refused when both have a live thread: one is
// closed first. A sub-part's key (project:site:prod) is left as it is.
func (c *core) mergeSubject(from, to string) error {
	if from == to || from == "" || to == "" {
		return nil
	}
	a, okA := c.liveThreadOf(from)
	b, okB := c.liveThreadOf(to)
	if okA && okB {
		return fmt.Errorf("%s and %s both have a live thread (%d and %d): close one first, then merge", from, to, a, b)
	}
	old, _ := c.store.knownSubject(from)
	names := append([]string{}, old.Aliases...)
	if _, slug, ok := strings.Cut(from, ":"); ok && len(subjectNames(knownSubject{Key: from})) > 0 {
		names = append([]string{strings.ReplaceAll(slug, "-", " ")}, names...)
	}
	tx, err := c.store.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range []string{
		`UPDATE subjects SET merged_into = ? WHERE key = ?`,
		`UPDATE subjects SET merged_into = ?1 WHERE merged_into = ?2`,
		`UPDATE threads SET subject = ? WHERE subject = ?`,
	} {
		if _, err := tx.Exec(q, to, from); err != nil {
			return err
		}
	}
	for _, table := range subjectTables {
		if _, err := tx.Exec(`UPDATE `+table+` SET subject = ? WHERE subject = ?`, to, from); err != nil {
			return err
		}
	}
	if err := rematchTopicRules(tx, from, to); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	c.store.keepSubject(to, old.Title)
	if err := c.addSubjectNames(to, "", names); err != nil {
		return err
	}
	if c.mem != nil {
		if err := c.mem.rekeySubject(from, to); err != nil {
			return fmt.Errorf("merge %s into %s, memory: %w", from, to, err)
		}
	}
	log.Printf("subjects: %s merged into %s", from, to)
	return nil
}

// rematchTopicRules makes the topic rules on one key match another.
func rematchTopicRules(tx *sql.Tx, from, to string) error {
	rows, err := tx.Query(`SELECT id, spec FROM rules WHERE source = ?`, sourceNotification)
	if err != nil {
		return err
	}
	specs := map[int64]notifySpec{}
	for rows.Next() {
		var id int64
		var raw string
		var sp notifySpec
		if err := rows.Scan(&id, &raw); err != nil {
			rows.Close()
			return err
		}
		if json.Unmarshal([]byte(raw), &sp) == nil && sp.Kind == ruleTopic && sp.Match == from {
			specs[id] = sp
		}
	}
	rows.Close()
	ids := make([]int64, 0, len(specs))
	for id := range specs {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		sp := specs[id]
		sp.Match = to
		b, _ := json.Marshal(sp)
		if _, err := tx.Exec(`UPDATE rules SET spec = ? WHERE id = ?`, string(b), id); err != nil {
			return err
		}
	}
	return nil
}

// rekeySubject moves the memories and intentions of one subject to
// another; the memories' stems follow.
func (m *memStore) rekeySubject(from, to string) error {
	tx, err := m.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	type mem struct {
		id             int64
		text, keywords string
	}
	rows, err := tx.Query(`SELECT id, text, keywords FROM memories WHERE subject = ?`, from)
	if err != nil {
		return err
	}
	var ms []mem
	for rows.Next() {
		var x mem
		if err := rows.Scan(&x.id, &x.text, &x.keywords); err != nil {
			rows.Close()
			return err
		}
		ms = append(ms, x)
	}
	rows.Close()
	for _, x := range ms {
		if _, err := tx.Exec(`UPDATE memories SET subject = ?, stems = ? WHERE id = ?`, to, memoryStems(x.text, to, x.keywords), x.id); err != nil {
			return err
		}
	}
	for _, col := range []string{"subject", "owner", "owed_to"} {
		if _, err := tx.Exec(`UPDATE intentions SET `+col+` = ? WHERE `+col+` = ?`, to, from); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func registerSubjectTools(s *mcp.Server, c *core) {
	tool(s, "name_subject", "Give a known subject the names the user calls it by: a title, and aliases (Velora, le site). eriphe-core finds a subject in the user's messages by its name and its aliases, and tells you which (« [sujets] »). Two keys for one matter (project:site and project:site-web): give the other key, or its name, as an alias of the one to keep, and the other is merged into it: its threads, memories, intentions, notifications and rules move under it, and its key is kept under it from then on. A merge is not undone, and is refused in a turn that read external content.",
		func(ctx context.Context, in nameSubjectIn) (map[string]any, error) {
			return c.nameSubject(in)
		})
}

// A record's subject. Notifications, attention items, cards, tasks and
// surfaces carry a subject next to their ref (which record), and the code
// sets it by inheritance: what the brain makes in a turn takes the turn's
// subject (turnSubject), a task its thread's or its skill's, a card or a
// surface its maker's (sourceSubject), a task's memories its own (the
// extraction). The model may give one; it no longer has to.

// subjectTables are the tables that carry a subject column of their own.
var subjectTables = []string{"notifications", "attention", "convo_cards", "tasks", "surfaces"}

// notificationSubjects is the kv key that says the notifications' subjects
// moved from their ref to their subject.
const notificationSubjects = "notification_subjects"

// subjectColumns gives the tables made before it their subject column, and
// moves once a notification's subject out of its ref: the ref keeps a
// record's (skill:<name>, intention:<id>) only.
func subjectColumns(db *sql.DB) error {
	for _, table := range subjectTables {
		var n int
		if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info(?) WHERE name = 'subject'`, table).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			if _, err := db.Exec(`ALTER TABLE ` + table + ` ADD COLUMN subject TEXT NOT NULL DEFAULT ''`); err != nil {
				return fmt.Errorf("%s: %w", table, err)
			}
		}
	}
	var done string
	if db.QueryRow(`SELECT value FROM kv WHERE key = ?`, notificationSubjects).Scan(&done) == nil {
		return nil
	}
	rows, err := db.Query(`SELECT DISTINCT ref FROM notifications WHERE ref != ''`)
	if err != nil {
		return err
	}
	var refs []string
	for rows.Next() {
		var s string
		if rows.Scan(&s) == nil {
			refs = append(refs, s)
		}
	}
	rows.Close()
	for _, s := range refs {
		if k := subjectOf(s); k != "" && !isRecordRef(s) {
			if _, err := db.Exec(`UPDATE notifications SET subject = ?, ref = '' WHERE ref = ?`, k, s); err != nil {
				return err
			}
		}
	}
	_, err = db.Exec(`INSERT OR REPLACE INTO kv (key, value) VALUES (?, '1')`, notificationSubjects)
	return err
}

// turnSubject is the subject of the brain's turn: its thread's, or else
// the one known subject my messages of the turn name; "" if neither.
func (c *core) turnSubject() string {
	if id := c.turn.aboutThread(); id > 0 {
		if th, err := c.thread(id); err == nil && th.Subject != "" {
			return th.Subject
		}
	}
	if named := c.turn.named(); len(named) == 1 {
		return named[0]
	}
	return ""
}

// sourceSubject is the subject of who made a record: a thread's (thread:,
// lead:), a task's; "" for anyone else.
func (c *core) sourceSubject(source string) string {
	kind, id, _ := strings.Cut(source, ":")
	switch kind {
	case "thread", "lead":
		n, err := strconv.ParseInt(id, 10, 64)
		if err != nil {
			return ""
		}
		if th, err := c.thread(n); err == nil {
			return th.Subject
		}
	case "task":
		var s string
		c.store.db.QueryRow(`SELECT subject FROM tasks WHERE id = ?`, id).Scan(&s)
		return s
	}
	return ""
}

// cardSubject sets a card's subject once: its proposal's (a memory's, an
// intention's, a thread's, a skill run's), else what its maker inherits
// (nil: none).
func (c *core) cardSubject(kind string, id int64, inherit func() string) {
	s := c.proposalSubject(kind, id)
	if s == "" && inherit != nil {
		s = inherit()
	}
	if s == "" {
		return
	}
	c.store.db.Exec(`UPDATE convo_cards SET subject = ? WHERE ref = ? AND subject = ''`, c.store.keepSubject(s, ""), proposalRef(kind, id))
}

// proposalSubject is the subject of what a card proposes, if it has one.
func (c *core) proposalSubject(kind string, id int64) string {
	switch kind {
	case cardMemory:
		if c.mem != nil {
			if x, err := c.mem.memory(id); err == nil {
				return x.Subject
			}
		}
	case cardIntention:
		if c.mem != nil {
			if x, err := c.mem.intention(id); err == nil {
				return x.Subject
			}
		}
	case cardThread:
		return c.sourceSubject(threadRef(id))
	case cardRunCalls:
		var task string
		c.store.db.QueryRow(`SELECT task FROM skill_runs WHERE id = ?`, id).Scan(&task)
		if task != "" {
			return c.sourceSubject("task:" + task)
		}
	}
	return ""
}

// skillRunSubject is the subject of a skill's run: the one its trigger
// declares, else its events' key where it names a thing: a monitor's
// (monitor.*: topic:<name>), or a key written kind:slug. Another key (a
// task's, a thread's, a surface's id) gives none.
func skillRunSubject(st *skillTrigger, key string, events []journalEvent) string {
	if st != nil && st.Subject != "" {
		return st.Subject
	}
	if strings.Contains(key, ":") {
		return subjectOf(key)
	}
	monitors := len(events) > 0
	for _, e := range events {
		monitors = monitors && strings.HasPrefix(e.Kind, "monitor.")
	}
	if monitors {
		return subjectOf(key)
	}
	return ""
}

// taskSubject is the subject of a task, "" for none or no such task.
func (c *core) taskSubject(id string) string {
	if id == "" {
		return ""
	}
	return c.sourceSubject("task:" + id)
}

// inheritsSubject tells a kind of memory that takes the turn's subject
// when it is given none: what is known and decided of the matter, and
// where it stands. A profile, a preference, a person, a lesson, a
// situation or the day's journal is about someone or something else.
func inheritsSubject(kind string) bool {
	return kind == kindFact || kind == kindDecision || kind == kindProgress
}

// opSubject is the subject of what the extraction adds from a task (its
// first source with a subject): a decision or a progress card of the task
// is the task's subject whatever was guessed, anything else takes it when
// none was given. Otherwise the one given. A replacement keeps its own.
func (c *core) opSubject(op extractionOp, ref string) string {
	var task string
	for _, p := range strings.Split(ref, ",") {
		if id, ok := strings.CutPrefix(p, "task:"); ok {
			if task = c.taskSubject(id); task != "" {
				break
			}
		}
	}
	switch {
	case task == "" || op.Subject == journalSubject:
		return op.Subject
	case op.Kind == kindDecision || op.Kind == kindProgress || strings.TrimSpace(op.Subject) == "":
		return task
	}
	return op.Subject
}
