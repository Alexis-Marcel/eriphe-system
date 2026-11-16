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

// The names index (#573). One matter is one thread: what a record is about
// (its about) is a thread, thread:<id>, or an entity of the memory, and the
// names they go by are in one index, names(name, target), in core.db:
//
//   - A key, kind:slug (pr:owner/repo#12, project:site, topic:<monitor>),
//     names one target: the thread it is an alias of (thread:<id>), or
//     itself while no thread has it. An entity names itself: a person
//     (person:lina), the user (owner), the home, a tool (tool:x), Eriphe's
//     own topics; so does a matter no thread follows yet.
//   - A name the user gives a thread (Velora, le site) names it too.
//   - A thread merged into another names that one (thread:7 → thread:9).
//
// A record written about a key is kept about its target: a key that is a
// thread's alias is that thread (noteAbout). When a thread takes a key as
// its alias, every record about the key is about the thread from then on:
// both databases and the rules are rekeyed (giveAlias, rekeyAbout). The
// code finds in the user's messages the threads and entities they name
// (named), by their names and the words of their keys.

const namesSchema = `CREATE TABLE IF NOT EXISTS names (
  name   TEXT NOT NULL,     -- a key (kind:slug), or a name the user gives a thread
  target TEXT NOT NULL,     -- what it names: thread:<id>, or a key (an entity)
  seen   INTEGER NOT NULL,  -- when it was last written
  PRIMARY KEY (name, target));
CREATE UNIQUE INDEX IF NOT EXISTS names_key ON names (name) WHERE instr(name, ':') > 0 OR name IN ('owner', 'home');
CREATE INDEX IF NOT EXISTS names_target ON names (target);`

func init() { extraSchema = append(extraSchema, namesSchema) }

const (
	threadNameMax  = 60 // a name the user gives a thread
	threadNamesMax = 12 // names and aliases of a thread
	namedMax       = 5  // threads and entities a message names, at most
)

// aboutBook is what the memory asks of the names: the target an about is
// kept under, noted as seen (note), or only looked up (resolve).
type aboutBook interface {
	noteAbout(about string) string
	resolveAbout(about string) string
}

// isKeyName tells a name that is a key: one target only.
func isKeyName(name string) bool {
	return strings.Contains(name, ":") || name == aliasOwner || name == aliasHome
}

// resolveAbout is the target an about is kept under: the thread a key is
// an alias of, the thread a merged one went into; "" stays "".
func (s *Store) resolveAbout(about string) string {
	for range 5 {
		var t string
		if about == "" || !isKeyName(about) || s.db.QueryRow(`SELECT target FROM names WHERE name = ?`, about).Scan(&t) != nil || t == about {
			return about
		}
		about = t
	}
	return about
}

// noteAbout is the target an about written now is kept under, its key
// noted as seen. A record ref that is no about is kept as given.
func (s *Store) noteAbout(about string) string {
	if k, err := aboutKey(about); err != nil || k != about || k == "" {
		return about // none, or not a key: a record's ref
	}
	t := s.resolveAbout(about)
	now := time.Now().Unix()
	if t == about && !strings.HasPrefix(about, refThread+":") {
		s.db.Exec(`INSERT OR IGNORE INTO names (name, target, seen) VALUES (?, ?, ?)`, about, about, now)
	}
	s.db.Exec(`UPDATE names SET seen = MAX(seen, ?) WHERE name = ?`, now, about)
	return t
}

// aboutIs is what an about given by someone is kept under, noted as seen:
// a thread, or a key (aboutKey) no thread has.
func (c *core) aboutIs(s string) (string, error) {
	k, err := aboutKey(s)
	if err != nil {
		return "", err
	}
	return c.store.noteAbout(k), nil
}

// aboutLookup is what to look an about up by; nothing is noted.
func (c *core) aboutLookup(s string) (string, error) {
	k, err := aboutKey(s)
	if err != nil {
		return "", err
	}
	return c.store.resolveAbout(k), nil
}

// useMemory gives eriphe-core its memory, which keeps its abouts here.
func (c *core) useMemory(m *memStore) {
	c.mem = m
	if m != nil {
		m.names = c.store
	}
}

// noteAbout is the memory's about as kept, noted as seen.
func (m *memStore) noteAbout(about string) string {
	if m.names == nil {
		return about
	}
	return m.names.noteAbout(about)
}

// resolveAbout is the memory's about to look up by.
func (m *memStore) resolveAbout(about string) string {
	if m.names == nil {
		return about
	}
	return m.names.resolveAbout(about)
}

// threadOfAbout is the thread an about is, if it is one.
func threadOfAbout(about string) (int64, bool) {
	n, err := strconv.ParseInt(strings.TrimPrefix(about, refThread+":"), 10, 64)
	return n, err == nil && n > 0 && strings.HasPrefix(about, refThread+":")
}

// aliasThread is the thread a key is an alias of, whatever its state.
func (c *core) aliasThread(key string) (int64, bool) {
	k, err := aboutKey(key)
	if err != nil || k == "" {
		return 0, false
	}
	return threadOfAbout(c.store.resolveAbout(k))
}

// liveThreadOf is the live thread of a key (an alias, or thread:<id>).
func (c *core) liveThreadOf(key string) (int64, bool) {
	id, ok := c.aliasThread(key)
	if !ok {
		return 0, false
	}
	s, err := c.thread(id)
	return id, err == nil && s.active()
}

// threadNames are the keys and names that name a thread, keys first.
func (c *core) threadNames(id int64) []string {
	rows, err := c.store.db.Query(`SELECT name FROM names WHERE target = ? ORDER BY seen DESC, name`, threadRef(id))
	if err != nil {
		return nil
	}
	defer rows.Close()
	var keys, names []string
	for rows.Next() {
		var n string
		if rows.Scan(&n) != nil {
			continue
		}
		switch {
		case strings.HasPrefix(n, refThread+":"): // a thread merged into it
		case isKeyName(n):
			keys = append(keys, n)
		default:
			names = append(names, n)
		}
	}
	return append(keys, names...)
}

// threadAlias is a thread's first key, "" for none.
func (c *core) threadAlias(id int64) string {
	for _, n := range c.threadNames(id) {
		if isKeyName(n) {
			return n
		}
	}
	return ""
}

// errNotAnAlias refuses an entity as a thread's alias.
func errNotAnAlias(key string) error {
	return fmt.Errorf("%s is a person, the user, the home or Eriphe's own: a thread's alias names a matter (project:, pr:, alert:, topic:…)", key)
}

// freeAlias is a key a new thread may take as its alias: a matter's key
// (not an entity's) no thread has; "" for none.
func (c *core) freeAlias(key string) (string, error) {
	k, err := aliasKey(key)
	if err != nil || k == "" {
		return k, err
	}
	kind, _, _ := strings.Cut(k, ":")
	if kind == aliasPerson || k == aliasOwner || k == aliasHome || systemEntity(k) {
		return "", errNotAnAlias(k)
	}
	if n, ok := threadOfAbout(c.store.resolveAbout(k)); ok {
		if s, err := c.thread(n); err == nil {
			return "", fmt.Errorf("%s is the alias of thread %d « %s » already: one matter, one thread; go on with it (read_thread, reopen_thread)", k, s.ID, s.Title)
		}
	}
	return k, nil
}

// giveAlias makes a key an alias of a thread: what was about the key is
// about the thread from then on (both databases, the rules). A key that
// is another thread's is refused: one matter, one thread.
func (c *core) giveAlias(id int64, key string) error {
	k, err := aliasKey(key)
	if err != nil || k == "" {
		return err
	}
	kind, _, _ := strings.Cut(k, ":")
	if kind == aliasPerson || k == aliasOwner || k == aliasHome || systemEntity(k) {
		return errNotAnAlias(k)
	}
	target := threadRef(id)
	switch t := c.store.resolveAbout(k); {
	case t == target:
		return nil
	case strings.HasPrefix(t, refThread+":"):
		if n, ok := threadOfAbout(t); ok {
			if s, err := c.thread(n); err == nil {
				return fmt.Errorf("%s is the alias of thread %d « %s » already: one matter, one thread", k, s.ID, s.Title)
			}
		}
		return fmt.Errorf("%s is the alias of %s already", k, t)
	}
	tx, err := c.store.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM names WHERE name = ?`, k); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO names (name, target, seen) VALUES (?, ?, ?)`, k, target, time.Now().Unix()); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return c.rekeyAbout(k, target)
}

// addThreadNames adds names the user gives a thread (Velora, le site).
func (c *core) addThreadNames(id int64, names []string) error {
	have := c.threadNames(id)
	now := time.Now().Unix()
	for _, n := range names {
		n = oneLine(n)
		switch {
		case n == "":
			continue
		case len([]rune(n)) > threadNameMax:
			return fmt.Errorf("a name is %d characters at most", threadNameMax)
		case slices.ContainsFunc(have, func(h string) bool { return strings.EqualFold(foldAccents(h), foldAccents(n)) }):
			continue
		case len(have) >= threadNamesMax:
			return fmt.Errorf("a thread has %d names at most", threadNamesMax)
		}
		if _, err := c.store.db.Exec(`INSERT OR IGNORE INTO names (name, target, seen) VALUES (?, ?, ?)`, n, threadRef(id), now); err != nil {
			return err
		}
		have = append(have, n)
	}
	return nil
}

// rekeyAbout moves what is about one target to another: the records of
// both databases, the names, the rules. core.db first, then memory.db
// (they are two files: a failure of the second is said).
func (c *core) rekeyAbout(from, to string) error {
	if from == to || from == "" || to == "" {
		return nil
	}
	tx, err := c.store.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, table := range aboutTables {
		if _, err := tx.Exec(`UPDATE `+table+` SET about = ? WHERE about = ?`, to, from); err != nil {
			return err
		}
	}
	// Names follow, those it has already left aside.
	if _, err := tx.Exec(`UPDATE OR IGNORE names SET target = ? WHERE target = ? AND name != ?`, to, from, from); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM names WHERE target = ? AND name != ?`, from, from); err != nil {
		return err
	}
	if err := rematchTopicRules(tx, from, to); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if c.mem != nil {
		if err := c.mem.rekeyAbout(from, to); err != nil {
			log.Printf("names: %s → %s, memory: %v", from, to, err)
			return fmt.Errorf("%s → %s, memory: %w", from, to, err)
		}
	}
	log.Printf("names: %s → %s", from, to)
	return nil
}

// mergeThreads merges a thread that ended into another, the same matter:
// its names, and what is about it (memories, intentions, notifications,
// cards, rules), are the other's from then on; its own steps and log stay
// with it, and thread:<from> names the other. Not undone.
func (c *core) mergeThreads(from, to int64, by string) error {
	if from == to {
		return errors.New("a thread is not merged into itself")
	}
	a, err := c.thread(from)
	if err != nil {
		return fmt.Errorf("thread %d: %w", from, err)
	}
	b, err := c.thread(to)
	if err != nil {
		return fmt.Errorf("thread %d: %w", to, err)
	}
	if a.active() {
		return fmt.Errorf("thread %d « %s » is %s: only an ended thread is merged into another (close or stop it first)", a.ID, a.Title, a.State)
	}
	if err := c.rekeyAbout(threadRef(from), threadRef(to)); err != nil {
		return err
	}
	if _, err := c.store.db.Exec(`INSERT OR REPLACE INTO names (name, target, seen) VALUES (?, ?, ?)`, threadRef(from), threadRef(to), time.Now().Unix()); err != nil {
		return err
	}
	c.logThread(from, by, fmt.Sprintf("Fusionné dans le thread %d « %s ».", b.ID, b.Title))
	c.logThread(to, by, fmt.Sprintf("Le thread %d « %s » y est fusionné.", a.ID, a.Title))
	return nil
}

// rematchTopicRules makes the topic rules on one about match another.
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

// rekeyAbout moves the memories and intentions about one target to
// another; the memories' stems follow.
func (m *memStore) rekeyAbout(from, to string) error {
	tx, err := m.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	type mem struct {
		id             int64
		text, keywords string
	}
	rows, err := tx.Query(`SELECT id, text, keywords FROM memories WHERE about = ?`, from)
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
		if _, err := tx.Exec(`UPDATE memories SET about = ?, stems = ? WHERE id = ?`, to, memoryStems(x.text, to, x.keywords), x.id); err != nil {
			return err
		}
	}
	for _, col := range []string{"about", "owner", "owed_to"} {
		if _, err := tx.Exec(`UPDATE intentions SET `+col+` = ? WHERE `+col+` = ?`, to, from); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// A name told in a message. A key's words are its slug's, for a kind
// that names (project, person, client, tool, topic); Eriphe's own topics,
// the user, the home and ids have none. A thread's names are told as
// written.

// systemEntity tells an entity of Eriphe's own: the workspace, its wakes,
// its journal. Never a thread's alias, never told in a message.
func systemEntity(key string) bool {
	return key == workspaceEntity || key == heartbeatEntity || key == journalEntity
}

// keyWords is the slug of a key that is told in a message, "" if none.
func keyWords(key string) string {
	kind, slug, _ := strings.Cut(key, ":")
	switch kind {
	case aliasProject, aliasPerson, aliasClient, aliasTool, aliasTopic:
		if !systemEntity(key) {
			return slug
		}
	}
	return ""
}

// nameStems are the stems of a name's words, in order.
func nameStems(name string) []string {
	var out []string
	for _, w := range memWords(foldAccents(name)) {
		out = append(out, stemWord(w))
	}
	return out
}

// nameMin is the fewest letters of a name told in a message: a shorter
// one would be found everywhere.
const nameMin = 3

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

func setOf(xs []string) map[string]bool {
	m := map[string]bool{}
	for _, x := range xs {
		m[x] = true
	}
	return m
}

// named are the threads and entities a text names, by their names and the
// words of their keys: the latest seen first, at most namedMax. One whose
// name is part of another's that is named too (site in site web) gives way
// to it.
func (c *core) named(text string) []string {
	words := textStems(text)
	if len(words) == 0 {
		return nil
	}
	rows, err := c.store.db.Query(`SELECT name, target FROM names ORDER BY seen DESC, name`)
	if err != nil {
		log.Printf("names: %v", err)
		return nil
	}
	type hit struct {
		target string
		name   []string
	}
	var hits []hit
	best := map[string]int{} // target → its hit
	for rows.Next() {
		var name, target string
		if rows.Scan(&name, &target) != nil {
			continue
		}
		told := name
		if isKeyName(name) {
			told = strings.ReplaceAll(keyWords(name), "-", " ")
		}
		s := nameStems(told)
		if len(strings.Join(s, "")) < nameMin || !namedIn(words, s) {
			continue
		}
		if i, ok := best[target]; ok {
			if len(s) > len(hits[i].name) {
				hits[i].name = s
			}
			continue
		}
		best[target] = len(hits)
		hits = append(hits, hit{target, s})
	}
	rows.Close()
	var out []string
	for _, h := range hits {
		inside := slices.ContainsFunc(hits, func(o hit) bool {
			return o.target != h.target && len(o.name) > len(h.name) && namedIn(setOf(o.name), h.name)
		})
		if t := c.store.resolveAbout(h.target); !inside && len(out) < namedMax && !slices.Contains(out, t) {
			out = append(out, t)
		}
	}
	return out
}

// nearThreads are the threads, ended and tidied ones too, that may follow
// the matter of a thread about to be opened: one of their names is in its
// alias's words or its title, or its alias's words are all in their names
// or title. The latest first, at most namedMax; a merged thread gives the
// one it was merged into. The one candidate matcher of every path that
// opens a thread (openThreadOnce).
func (c *core) nearThreads(alias, title string) []Thread {
	aliasStems := nameStems(strings.ReplaceAll(keyWords(alias), "-", " "))
	if len(strings.Join(aliasStems, "")) < nameMin {
		aliasStems = nil
	}
	text := textStems(strings.ReplaceAll(keyWords(alias), "-", " ") + " " + title)
	var hits []string // targets, the latest first; resolved once the rows are read
	// What the thread's words name, by the names known.
	rows, err := c.store.db.Query(`SELECT name, target FROM names WHERE target LIKE 'thread:%' ORDER BY seen DESC, name`)
	if err != nil {
		log.Printf("names: %v", err)
		return nil
	}
	theirs := map[string][]string{} // target → the stems of its names
	for rows.Next() {
		var name, target string
		if rows.Scan(&name, &target) != nil {
			continue
		}
		told := name
		if isKeyName(name) {
			told = strings.ReplaceAll(keyWords(name), "-", " ")
		}
		s := nameStems(told)
		theirs[target] = append(theirs[target], s...)
		if len(strings.Join(s, "")) >= nameMin && namedIn(text, s) {
			hits = append(hits, target)
		}
	}
	rows.Close()
	// Threads whose names or title hold the alias's words.
	if aliasStems != nil {
		rows, err := c.store.db.Query(`SELECT id, title FROM threads ORDER BY id DESC`)
		if err != nil {
			log.Printf("threads: %v", err)
			return nil
		}
		for rows.Next() {
			var id int64
			var t string
			if rows.Scan(&id, &t) != nil {
				continue
			}
			words := setOf(append(nameStems(t), theirs[threadRef(id)]...))
			if namedIn(words, aliasStems) {
				hits = append(hits, threadRef(id))
			}
		}
		rows.Close()
	}
	var out []Thread
	for _, target := range hits {
		id, ok := threadOfAbout(c.store.resolveAbout(target))
		if !ok || len(out) >= namedMax || slices.ContainsFunc(out, func(x Thread) bool { return x.ID == id }) {
			continue
		}
		if s, err := c.thread(id); err == nil {
			out = append(out, s)
		}
	}
	return out
}

// nearMatch is why a thread is not opened: threads that may follow its
// matter already. Taking one (take) or saying why it is another matter
// (new_because) opens again.
type nearMatch struct{ threads []Thread }

func (e *nearMatch) Error() string {
	var b strings.Builder
	b.WriteString("not opened: a matter has one thread, and these may follow it already:")
	for _, s := range e.threads {
		fmt.Fprintf(&b, " thread %d « %s » (%s", s.ID, s.Title, s.State)
		if s.Alias != "" {
			b.WriteString(", " + s.Alias)
		}
		b.WriteString(");")
	}
	b.WriteString(" if one of them is this matter, open_thread again with take: its id (it is given back, reopened if it ended, and takes this alias); if this is another matter, open_thread again with new_because: why, in a few words.")
	return b.String()
}

// namedBlock is what the user's messages name of the threads and entities
// known, for the brain, and the turn is about them; "" if none. Only refs
// and keys are written: a thread's title or a name may come from what
// someone else wrote.
func (c *core) namedBlock(msgs []Message) string {
	var named []string
	for _, m := range msgs {
		if m.Role != roleUser {
			continue
		}
		for _, k := range c.named(m.Text) {
			if !slices.Contains(named, k) {
				named = append(named, k)
			}
		}
	}
	c.turn.name(named)
	if len(named) == 0 {
		return ""
	}
	return "[nommés] Ce message nomme : " + strings.Join(named, ", ") + "."
}

// name records the threads and entities the turn's messages name; a new
// delivery never keeps those of the turn before.
func (t *brainTurn) name(keys []string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !slices.Equal(t.namedRefs, keys) {
		t.namedRefs = slices.Clone(keys)
		t.save()
	}
}

// named are the threads and entities the turn's messages name.
func (t *brainTurn) named() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return slices.Clone(t.namedRefs)
}

// nameThreadIn is name_thread's input.
type nameThreadIn struct {
	Thread int64    `json:"thread_id"`
	Names  []string `json:"names,omitempty" jsonschema:"what names it: names the user gives it (Velora, le site), or keys kind:slug (project:site, pr:owner/repo#12); eriphe-core finds the thread by them in the user's messages and when a thread is opened"`
	Merge  int64    `json:"merge,omitempty" jsonschema:"another thread, ended, that is the same matter: merged into this one, its names, memories, intentions, notifications and rules move here; not undone"`
}

// nameThread gives a thread names and keys, and merges into it a thread
// that is the same matter. Never in a turn that read external content: a
// name taken from someone else's words would bind their matter to it, and
// a merge rewrites records and is not undone.
func (c *core) nameThread(in nameThreadIn) (map[string]any, error) {
	if tainted := c.turnTaint(); len(tainted) > 0 {
		return nil, fmt.Errorf("refused: this turn read external content (%s); name a thread in a turn of the user's own", strings.Join(tainted, ", "))
	}
	s, err := c.thread(in.Thread)
	if err != nil {
		return nil, err
	}
	if len(in.Names) > threadNamesMax {
		return nil, fmt.Errorf("at most %d names", threadNamesMax)
	}
	var names []string
	for _, n := range in.Names {
		n = oneLine(n)
		if n == "" {
			continue
		}
		if strings.Contains(n, ":") {
			if err := c.giveAlias(s.ID, n); err != nil {
				return nil, fmt.Errorf("%s: %w", n, err)
			}
			continue
		}
		names = append(names, n)
	}
	if err := c.addThreadNames(s.ID, names); err != nil {
		return nil, err
	}
	out := map[string]any{"thread": s.ID, "names": c.threadNames(s.ID)}
	if in.Merge > 0 {
		if err := c.mergeThreads(in.Merge, s.ID, byBrain); err != nil {
			return nil, err
		}
		out["merged"] = in.Merge
		out["note"] = fmt.Sprintf("Thread %d is merged into %d: its names, memories, intentions, notifications and rules are this one's now.", in.Merge, s.ID)
	}
	c.turnThread(s.ID)
	return out, nil
}

func registerNameTools(s *mcp.Server, c *core) {
	tool(s, "name_thread", "Give a thread the names the user calls it by (Velora, le site) or keys that name it (project:site, pr:owner/repo#12): eriphe-core finds it by them in the user's messages (« [nommés] ») and when a thread is opened, so the matter keeps one thread. A key another thread has is refused. merge: another thread, ended, that is the same matter, merged into this one (its names, memories, intentions, notifications and rules move here); not undone. Refused in a turn that read external content.",
		func(ctx context.Context, in nameThreadIn) (map[string]any, error) {
			return c.nameThread(in)
		})
}
