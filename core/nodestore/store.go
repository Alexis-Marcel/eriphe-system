// Package nodestore keeps the graph's nodes: markdown files in a git
// repository (go-git, no git binary), and an index for what must be fast
// (nodes.db, SQLite). The files are the truth; the index is rebuilt from
// them when it is lost. The file format is package nodefile's
// (docs/node-format.md).
//
//   - A node is <slug>-<id>.md, flat at the repository's root, its trace
//     <slug>-<id>.trace.md, its media in files/<id>/. The slug is frozen
//     at creation: a new title changes the file's front matter, not its
//     name.
//   - Every write is one commit, with its author (brain, task:<id>,
//     user…). A node's write makes a new version; the index keeps each
//     version's commit hash, so history is never asked of git by path. A
//     write carries the hash of the version it was made on (expect): a
//     stale one is refused (ErrConflict), never written over.
//   - An author's write goes through nodefile's author API: code's fields
//     (marks, snapshots, external, title_trusted) are kept, and a write that changes
//     them is refused. Secrets are refused by the filter the store is
//     given (Options.Refuse).
//   - The title of an external node may be someone else's words, unless
//     it is trusted (title_trusted): written while the node was not
//     external, or by a writer that vouched for it (nodefile's
//     VouchTitle: the user, an author not tainted). Each write settles
//     it (settleTitle): fail-closed, a title written on an external node
//     by a writer that did not vouch is not trusted. Not trusted, it is
//     shown as it is and marked so (TitleExternal), never replaced.
//   - At start, the files are compared to the index: a file changed
//     outside Eriphe is committed as a version of its own (author
//     "outside"), reindexed and reported.
package nodestore

import (
	"bytes"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"

	git "github.com/go-git/go-git/v5"
	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
	_ "modernc.org/sqlite"

	"github.com/Alexis-Marcel/eriphe-system/core/nodefile"
)

// Limits of a node: its file's bytes and its blocks.
const (
	MaxBytes  = 512 << 10
	MaxBlocks = 32
	// MaxTrace is the most bytes one append to a trace adds.
	MaxTrace = 256 << 10
)

// States of a node, code's, in the index only.
const (
	Running = "running"
	Waiting = "waiting"
	Idle    = "idle"
	Done    = "done"
)

// Outside is the author of a version made from a file changed outside
// Eriphe, found at start.
const Outside = "outside"

var (
	// ErrConflict: the write was made on a version that is no longer the
	// last one.
	ErrConflict = errors.New("the node changed since the version this write was made on")
	// ErrNotFound: no node has this id.
	ErrNotFound = errors.New("no such node")
	// ErrArchived: an archived node is not written any more.
	ErrArchived = errors.New("the node is archived")
)

// Options are a store's.
type Options struct {
	// Refuse tells why a text an author writes is refused (a secret, say);
	// nil when it is not. nil: nothing is refused.
	Refuse func(text string) error
	// Now is the clock; time.Now by default.
	Now func() time.Time
	// NewID makes a node's id; random by default.
	NewID func() string
}

// What changed of a node, as Watch tells it.
const (
	ChangedDoc   = "doc"   // a new version of its file
	ChangedTrace = "trace" // its trace grew
	ChangedState = "state" // its state, in the index only
	ChangedItems = "items" // the notes on the items of one of its sources (Source), in the index only
)

// Change is a node that changed, and what of it.
type Change struct {
	ID     string
	What   string // ChangedDoc, ChangedTrace, ChangedState or ChangedItems
	Source string // ChangedItems: the source whose items' notes changed
}

// Store is the graph's nodes.
type Store struct {
	dir  string // the repository
	db   *sql.DB
	repo *git.Repository
	opts Options

	// One write at a time: the repository's worktree is not shared. A
	// read of the repository waits for a write.
	mu sync.RWMutex

	watch func(Change) // Watch's
}

// Watch calls fn after each change of a node, the store's lock held: fn
// must not call the store. One watcher; nil stops it.
func (s *Store) Watch(fn func(Change)) {
	s.mu.Lock()
	s.watch = fn
	s.mu.Unlock()
}

// changed tells the watcher, the lock held.
func (s *Store) changed(what string, ids ...string) {
	if s.watch == nil {
		return
	}
	for _, id := range ids {
		s.watch(Change{ID: id, What: what})
	}
}

// Report is what a start found.
type Report struct {
	Rebuilt bool     // the index was made again from the files
	Behind  bool     // the index was behind the repository: its versions were read again from the log
	Changed []string // nodes whose file was changed outside Eriphe
	Gone    []string // nodes whose file was removed outside Eriphe
	Broken  []string // files that do not read as a node: "<file>: <why>"
}

// Version is one of a node's versions.
type Version struct {
	N      int
	Hash   string // the commit that made it
	Author string
	At     time.Time
}

// Doc is a node as read: its file at a version.
type Doc struct {
	Node    *nodefile.Node
	Raw     []byte
	File    string
	Version Version // the version read
	Last    int     // the last version's n
	State   string
}

// Summary is what the index holds of a node.
type Summary struct {
	ID       string
	File     string
	Title    string
	Parent   string
	Lifetime nodefile.Lifetime
	State    string
	Created  time.Time
	Updated  time.Time
	Done     time.Time
	Archived time.Time
	External bool
	// TitleTrusted: external, its title is not someone else's words
	// (nodefile's Meta.TitleTrusted).
	TitleTrusted bool
	// OpenedBy is who wanted it (nodefile's Meta.OpenedBy.By): "" when
	// not known.
	OpenedBy string
	Version  int
	Links    []string // the nodes it links to: its text's [[links]], resolved, and its front matter's
}

// Unasked tells a node the user did not ask for: opened by a skill or
// after an event (nodefile.Origin.Unasked).
func (x Summary) Unasked() bool { return nodefile.UnaskedBy(x.OpenedBy) }

// TitleExternal tells whether its title may be someone else's words: an
// external node's, not trusted.
func (x Summary) TitleExternal() bool { return x.External && !x.TitleTrusted }

// Open opens the store: the repository in dir, the index at index (kept
// outside dir). The index is made from the files when it is missing; the
// files changed outside Eriphe are committed and reported.
func Open(dir, index string, opts Options) (*Store, Report, error) {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.NewID == nil {
		opts.NewID = randomID
	}
	if rel, err := filepath.Rel(dir, index); err == nil && !strings.HasPrefix(rel, "..") {
		return nil, Report{}, errors.New("the index is kept outside the repository")
	}
	repo, err := openRepo(dir)
	if err != nil {
		return nil, Report{}, fmt.Errorf("repository: %w", err)
	}
	if err := checkRepo(repo); err != nil {
		return nil, Report{}, fmt.Errorf("repository %s: %w; put it back from a backup (nodes-<day>.bundle: git clone it) and keep the broken one aside", dir, err)
	}
	if err := os.MkdirAll(filepath.Dir(index), 0o700); err != nil {
		return nil, Report{}, err
	}
	db, fresh, err := openIndex(index)
	if err != nil {
		return nil, Report{}, fmt.Errorf("index: %w", err)
	}
	s := &Store{dir: dir, db: db, repo: repo, opts: opts}
	rep, err := s.reconcile(fresh)
	if err != nil {
		db.Close()
		return nil, rep, err
	}
	return s, rep, nil
}

// Close closes the index.
func (s *Store) Close() error { return s.db.Close() }

// randomID is a new node's id: 10 letters and digits.
func randomID() string {
	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, 10)
	rand.Read(b)
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return string(b)
}

// NewID is an id no node has yet, for a node to create later with it
// (New.ID): a card shows what its yes creates.
func (s *Store) NewID() string {
	id := s.opts.NewID()
	for range 8 {
		if _, err := s.summary(id); errors.Is(err, ErrNotFound) {
			break
		}
		id = s.opts.NewID()
	}
	return id // Create says so if it is taken
}

// slug is a file name's part from a title: lowercase ASCII letters and
// digits, words joined by a dash, 40 characters at most.
func slug(title string) string {
	t := transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)
	folded, _, _ := transform.String(t, strings.ToLower(title))
	var b strings.Builder
	dash := false
	for _, r := range folded {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			if dash && b.Len() > 0 {
				b.WriteByte('-')
			}
			dash = false
			b.WriteRune(r)
			if b.Len() >= 40 {
				break
			}
			continue
		}
		dash = true
	}
	if b.Len() == 0 {
		return "node"
	}
	return strings.TrimRight(b.String(), "-")
}

// traceFile is a node's trace's file.
func traceFile(file string) string { return strings.TrimSuffix(file, ".md") + ".trace.md" }

// New is a node to create.
type New struct {
	ID       string // its id when it was given beforehand (a card that shows it); a new one when empty
	Title    string
	Parent   string
	Links    []string
	After    []string          // a step's: its sibling steps it waits for
	Lifetime nodefile.Lifetime // work by default
	Goal     string
	Body     string // the author's: text and blocks, short forms accepted
	External bool   // code's: it holds someone else's words, and so do its blocks
	// TitleTrusted: its maker stands for its title even when External
	// (the user approved it on a card that showed it).
	TitleTrusted bool
	// OpenedBy: who opened it, as code knows it (nodefile.Origin).
	OpenedBy *nodefile.Origin
	Author   string
}

// Create makes a node: its file, its first version.
func (s *Store) Create(nw New) (*Doc, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n, err := s.prepare(nw)
	if err != nil {
		return nil, err
	}
	file := slug(n.Meta.Title) + "-" + n.Meta.ID + ".md"
	return s.save(n, file, nil, nw.Author, "create")
}

// Check tells whether Create would make the node, and makes nothing: a
// card that shows a node before it exists is never one that cannot be
// answered yes.
func (s *Store) Check(nw New) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	n, err := s.prepare(nw)
	if err != nil {
		return err
	}
	if len(n.Blocks()) > MaxBlocks {
		return fmt.Errorf("a node holds %d blocks at most, this one would hold %d", MaxBlocks, len(n.Blocks()))
	}
	raw, err := n.Bytes()
	if err != nil {
		return err
	}
	if len(raw) > MaxBytes {
		return fmt.Errorf("a node is %d KiB at most, this one would be %d KiB", MaxBytes>>10, len(raw)>>10)
	}
	return nil
}

// prepare checks a node to create and builds it.
func (s *Store) prepare(nw New) (*nodefile.Node, error) {
	if nw.Lifetime == "" {
		nw.Lifetime = nodefile.Work
	}
	if err := s.refuse(nw.Title, nw.Goal, nw.Body); err != nil {
		return nil, err
	}
	if nw.Parent != "" {
		if _, err := s.summary(nw.Parent); err != nil {
			return nil, fmt.Errorf("parent %s: %w", nw.Parent, err)
		}
	}
	for _, l := range nw.Links {
		if _, err := s.summary(l); err != nil {
			return nil, fmt.Errorf("link %s: %w", l, err)
		}
	}
	for _, a := range nw.After {
		sum, err := s.summary(a)
		if err != nil {
			return nil, fmt.Errorf("after %s: %w", a, err)
		}
		if sum.Parent != nw.Parent || nw.Parent == "" {
			return nil, fmt.Errorf("after %s: a step waits only for steps of its own parent", a)
		}
	}
	id := nw.ID
	if id == "" {
		id = s.opts.NewID()
	} else if !nodefile.IDOK(id) {
		return nil, fmt.Errorf("id %q: lowercase letters and digits, 32 at most", id)
	}
	if _, err := s.summary(id); err == nil {
		return nil, fmt.Errorf("id %s is taken", id)
	}
	now := s.opts.Now().Truncate(time.Second)
	n, err := nodefile.New(nodefile.Meta{
		ID: id, Title: strings.TrimSpace(nw.Title), Parent: nw.Parent, Links: nw.Links, After: nw.After,
		Lifetime: nw.Lifetime, Goal: nw.Goal, Created: now, External: nw.External, OpenedBy: nw.OpenedBy,
	}, "")
	if err != nil {
		return nil, err
	}
	if err := n.AuthorBody(nw.Body); err != nil {
		return nil, err
	}
	if nw.External {
		for _, b := range n.Blocks() {
			n.SetBlockExternal(b.Name, true)
		}
	}
	if nw.TitleTrusted {
		n.VouchTitle()
	}
	return n, nil
}

// Write replaces a node's body (its text and blocks) with an author's,
// made on the version whose hash is expect. Code's fields of its blocks
// are kept. A body alike gives the version it was made on.
func (s *Store) Write(id, body, expect, author string) (*Doc, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.refuse(body); err != nil {
		return nil, err
	}
	return s.change(id, expect, author, "write", func(n *nodefile.Node) error { return n.AuthorBody(body) })
}

// change applies fn to a node's last version and writes it, when expect
// names that version (an author's write always names one).
func (s *Store) change(id, expect, author, op string, fn func(*nodefile.Node) error) (*Doc, error) {
	cur, err := s.read(id, 0)
	if err != nil {
		return nil, err
	}
	if !cur.Node.Meta.Archived.IsZero() {
		return nil, fmt.Errorf("%s: %w", id, ErrArchived)
	}
	if expect == "" {
		return nil, errors.New("expect is needed: the hash of the version this write was made on")
	}
	if expect != cur.Version.Hash && !s.onlyCodeSince(cur, expect) {
		return nil, fmt.Errorf("%w: written on %s, the last version is v%d (%s)", ErrConflict, short(expect), cur.Version.N, short(cur.Version.Hash))
	}
	n, err := nodefile.Parse(cur.Raw)
	if err != nil {
		return nil, err
	}
	if err := fn(n); err != nil {
		return nil, err
	}
	if err := n.Validate(); err != nil {
		return nil, err
	}
	return s.save(n, cur.File, cur, author, op)
}

// Change is code's write of a node: fn changes its last version, which
// expect names, as code does what no author may (a block's or the
// node's external, its title or goal). The node's id, parent, the steps
// it waits for, creation, done and archived are never changed this way. The store's filter runs
// over the node written.
func (s *Store) Change(id, expect, author string, fn func(*nodefile.Node) error) (*Doc, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.change(id, expect, author, "change", func(n *nodefile.Node) error {
		was := n.Meta
		if err := fn(n); err != nil {
			return err
		}
		m := n.Meta
		if m.ID != was.ID || m.Parent != was.Parent || !slices.Equal(m.After, was.After) || !m.Created.Equal(was.Created) || !m.Done.Equal(was.Done) || !m.Archived.Equal(was.Archived) {
			return errors.New("a node's id, parent, after, creation, done and archived are not changed by a write")
		}
		raw, err := n.Bytes()
		if err != nil {
			return err
		}
		return s.refuse(string(raw))
	})
}

// onlyCodeSince tells whether a node's versions since the one whose hash
// is expect changed only code's fields (a source's snapshot, the user's
// marks, external): a write made on that version then applies on the
// last one, code's fields kept. A live block's snapshot never makes an
// author's write stale.
func (s *Store) onlyCodeSince(cur *Doc, expect string) bool {
	var n int
	if s.db.QueryRow(`SELECT n FROM node_versions WHERE id = ? AND hash = ?`, cur.Node.Meta.ID, expect).Scan(&n) != nil || n >= cur.Version.N {
		return false
	}
	raw, err := s.fileAt(expect, cur.File)
	if err != nil {
		return false
	}
	was, err := nodefile.Parse(raw)
	if err != nil {
		return false
	}
	a, err1 := was.AuthorBytes()
	b, err2 := cur.Node.AuthorBytes()
	return err1 == nil && err2 == nil && bytes.Equal(a, b)
}

func short(hash string) string {
	if len(hash) > 10 {
		return hash[:10]
	}
	return hash
}

// save writes a node as its next version, prev its last (nil for a new
// node), and indexes it. A node alike prev is not written again.
func (s *Store) save(n *nodefile.Node, file string, prev *Doc, author, op string) (*Doc, error) {
	if strings.TrimSpace(author) == "" {
		return nil, errors.New("a write has an author")
	}
	if len(n.Blocks()) > MaxBlocks {
		return nil, fmt.Errorf("a node holds %d blocks at most, this one would hold %d", MaxBlocks, len(n.Blocks()))
	}
	var was *nodefile.Node
	if prev != nil {
		was = prev.Node
	}
	settleTitle(n, was)
	raw, err := n.Bytes()
	if err != nil {
		return nil, err
	}
	if len(raw) > MaxBytes {
		return nil, fmt.Errorf("a node is %d KiB at most, this one would be %d KiB", MaxBytes>>10, len(raw)>>10)
	}
	if prev != nil && string(raw) == string(prev.Raw) {
		return prev, nil
	}
	num := 1
	if prev != nil {
		num = prev.Last + 1
	}
	now := s.opts.Now()
	hash, err := s.commit(fmt.Sprintf("%s %s: %s", op, n.Meta.ID, n.Meta.Title), author, now,
		[]change{{path: file, data: raw}}, []stamp{{n.Meta.ID, num}})
	if err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	v := Version{N: num, Hash: hash, Author: author, At: now}
	if err := s.indexVersions(now, []indexed{{n, file, raw, v}}); err != nil {
		return nil, err
	}
	s.changed(ChangedDoc, n.Meta.ID)
	state := Idle
	if prev != nil {
		state = prev.State
	}
	return &Doc{Node: n, Raw: raw, File: file, Version: v, Last: num, State: state}, nil
}

// settleTitle sets whether a node's title is trusted, as it is written
// over prev (nil for a new node): never said of a node not external; a
// title its writer vouched for is trusted; one unchanged keeps what it
// was (trusted when written before the node was external); any other,
// written on an external node, may be someone else's words.
func settleTitle(n, prev *nodefile.Node) {
	m := &n.Meta
	switch {
	case !m.External:
		m.TitleTrusted = false
	case n.TitleVouched():
		m.TitleTrusted = true
	case prev != nil && prev.Meta.Title == m.Title:
		m.TitleTrusted = !prev.Meta.External || prev.Meta.TitleTrusted
	default:
		m.TitleTrusted = false
	}
}

// indexed is a node version to index.
type indexed struct {
	node *nodefile.Node
	file string
	raw  []byte
	v    Version
}

// indexVersions indexes node versions just committed, in one transaction.
func (s *Store) indexVersions(now time.Time, xs []indexed) error {
	if len(xs) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, x := range xs {
		if err := indexNode(tx, x.node, x.file, x.raw, x.v.N, now); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT OR REPLACE INTO node_versions (id, n, hash, author, at) VALUES (?, ?, ?, ?, ?)`,
			x.node.Meta.ID, x.v.N, x.v.Hash, x.v.Author, x.v.At.Unix()); err != nil {
			return err
		}
	}
	if err := setIndexed(tx, xs[len(xs)-1].v.Hash); err != nil {
		return err
	}
	// A node that got a title others' text links: their links resolve.
	linked, err := resolveRefs(tx)
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.changed(ChangedDoc, linked...)
	return nil
}

// setIndexed keeps the last commit the index holds (meta.head): a start
// that finds the repository further replays the log (reconcile).
func setIndexed(tx execer, hash string) error {
	_, err := tx.Exec(`INSERT OR REPLACE INTO meta (key, value) VALUES ('head', ?)`, hash)
	return err
}

// refuse runs the store's filter over what an author writes.
func (s *Store) refuse(parts ...string) error {
	if s.opts.Refuse == nil {
		return nil
	}
	if err := s.opts.Refuse(strings.Join(parts, "\n")); err != nil {
		return fmt.Errorf("refused: %w", err)
	}
	return nil
}

// Read reads a node at a version: n, 0 for the last one.
func (s *Store) Read(id string, version int) (*Doc, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.read(id, version)
}

func (s *Store) read(id string, version int) (*Doc, error) {
	sum, err := s.summary(id)
	if err != nil {
		return nil, err
	}
	n := version
	if n == 0 {
		n = sum.Version
	}
	if n < 0 || n > sum.Version {
		return nil, fmt.Errorf("%s has versions 1 to %d, not %d", id, sum.Version, version)
	}
	var v Version
	var at int64
	err = s.db.QueryRow(`SELECT n, hash, author, at FROM node_versions WHERE id = ? AND n = ?`, id, n).Scan(&v.N, &v.Hash, &v.Author, &at)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%s: version %d is not in the index", id, n)
	}
	if err != nil {
		return nil, err
	}
	v.At = time.Unix(at, 0)
	var raw []byte
	if version == 0 {
		raw, err = os.ReadFile(filepath.Join(s.dir, sum.File))
	} else {
		raw, err = s.fileAt(v.Hash, sum.File)
	}
	if err != nil {
		return nil, err
	}
	node, err := nodefile.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", sum.File, err)
	}
	return &Doc{Node: node, Raw: raw, File: sum.File, Version: v, Last: sum.Version, State: sum.State}, nil
}

// Versions are a node's versions, the first first.
func (s *Store) Versions(id string) ([]Version, error) {
	rows, err := s.db.Query(`SELECT n, hash, author, at FROM node_versions WHERE id = ? ORDER BY n`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Version
	for rows.Next() {
		var v Version
		var at int64
		if err := rows.Scan(&v.N, &v.Hash, &v.Author, &at); err != nil {
			return nil, err
		}
		v.At = time.Unix(at, 0)
		out = append(out, v)
	}
	return out, rows.Err()
}

// AppendTrace adds an entry at the end of a node's trace: when, who, and
// its text, never cut. The trace is not a version of the node; the node
// is changed now all the same (Summary.Updated).
func (s *Store) AppendTrace(id, author, text string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if strings.TrimSpace(author) == "" {
		return errors.New("a write has an author")
	}
	if len(text) > MaxTrace {
		return fmt.Errorf("a trace entry is %d KiB at most", MaxTrace>>10)
	}
	if err := s.refuse(text); err != nil {
		return err
	}
	sum, err := s.summary(id)
	if err != nil {
		return err
	}
	file := traceFile(sum.File)
	old, err := os.ReadFile(filepath.Join(s.dir, file))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	now := s.opts.Now()
	entry := fmt.Sprintf("## %s · %s\n\n%s\n\n", now.Format(time.RFC3339), author, strings.TrimRight(text, "\n"))
	if len(old) == 0 {
		old = []byte("# " + sum.Title + "\n\n")
	}
	hash, err := s.commit(fmt.Sprintf("trace %s", id), author, now, []change{{path: file, data: append(old, entry...)}}, nil)
	if err != nil {
		return err
	}
	// The node moved: it is among the last changed (Recent).
	if _, err := s.db.Exec(`UPDATE nodes SET updated = ? WHERE id = ?`, now.Unix(), id); err != nil {
		return err
	}
	if err := setIndexed(s.db, hash); err != nil {
		return err
	}
	s.changed(ChangedTrace, id)
	return nil
}

// Trace is a node's trace; empty while it has none.
func (s *Store) Trace(id string) ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sum, err := s.summary(id)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(filepath.Join(s.dir, traceFile(sum.File)))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	return b, err
}

// SetState sets a node's state: running, waiting, idle or done.
func (s *Store) SetState(id, state string) error {
	if !slices.Contains([]string{Running, Waiting, Idle, Done}, state) {
		return fmt.Errorf("a node's state is %s, %s, %s or %s, not %q", Running, Waiting, Idle, Done, state)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var was string
	if err := s.db.QueryRow(`SELECT state FROM nodes WHERE id = ?`, id).Scan(&was); err != nil {
		return fmt.Errorf("%s: %w", id, ErrNotFound)
	}
	if was == state {
		return nil
	}
	if _, err := s.db.Exec(`UPDATE nodes SET state = ? WHERE id = ?`, state, id); err != nil {
		return err
	}
	s.changed(ChangedState, id)
	return nil
}

// Archive archives a node and, in the same commit, every node below it
// not archived yet. Archived nodes leave the graph and stay findable.
func (s *Store) Archive(id, author string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if strings.TrimSpace(author) == "" {
		return errors.New("a write has an author")
	}
	ids, err := s.below(id)
	if err != nil {
		return err
	}
	now := s.opts.Now().Truncate(time.Second)
	var changes []change
	var stamps []stamp
	var xs []indexed
	for _, nid := range ids {
		cur, err := s.read(nid, 0)
		if err != nil {
			return err
		}
		if !cur.Node.Meta.Archived.IsZero() {
			continue
		}
		cur.Node.Meta.Archived = now
		raw, err := cur.Node.Bytes()
		if err != nil {
			return err
		}
		changes = append(changes, change{path: cur.File, data: raw})
		stamps = append(stamps, stamp{nid, cur.Last + 1})
		xs = append(xs, indexed{cur.Node, cur.File, raw, Version{N: cur.Last + 1, Author: author, At: now}})
	}
	if len(changes) == 0 {
		return nil
	}
	hash, err := s.commit(fmt.Sprintf("archive %s", id), author, now, changes, stamps)
	if err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	for i := range xs {
		xs[i].v.Hash = hash
	}
	if err := s.indexVersions(now, xs); err != nil {
		return err
	}
	for _, x := range xs {
		s.changed(ChangedDoc, x.node.Meta.ID)
	}
	return nil
}

// Children are a node's children, archived ones included, the oldest
// first.
func (s *Store) Children(id string) ([]Summary, error) { return s.children(id) }

func (s *Store) children(id string) ([]Summary, error) {
	return s.summaries(`WHERE parent = ? ORDER BY created, id`, id)
}

// Find finds nodes by words of their title, goal, text or blocks, the
// best first; archived ones only when asked.
func (s *Store) Find(query string, archived bool, limit int) ([]Summary, error) {
	match := ftsQuery(query)
	if match == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 20
	}
	cond := `AND n.archived = 0`
	if archived {
		cond = ``
	}
	return s.summaries(`JOIN node_fts f ON f.id = n.id WHERE node_fts MATCH ? `+cond+` ORDER BY bm25(node_fts, 0, 2.0, 1.0) LIMIT ?`, match, limit)
}

// Get is what the index holds of a node.
func (s *Store) Get(id string) (Summary, error) { return s.summary(id) }

func (s *Store) summary(id string) (Summary, error) {
	xs, err := s.summaries(`WHERE n.id = ?`, id)
	if err != nil {
		return Summary{}, err
	}
	if len(xs) == 0 {
		return Summary{}, fmt.Errorf("%s: %w", id, ErrNotFound)
	}
	return xs[0], nil
}

func (s *Store) summaries(where string, args ...any) ([]Summary, error) {
	rows, err := s.db.Query(`SELECT n.id, n.file, n.title, n.parent, n.lifetime, n.state, n.created, n.updated,
		n.done, n.archived, n.external, n.title_trusted, n.opened_by, n.version,
		(SELECT group_concat(t, ' ') FROM (SELECT l.target AS t FROM node_links l WHERE l.id = n.id
		  UNION SELECT r.target FROM node_refs r WHERE r.id = n.id AND r.target != '' AND r.target != n.id)) FROM nodes n `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Summary
	for rows.Next() {
		var x Summary
		var created, updated, done, archived int64
		var links sql.NullString
		if err := rows.Scan(&x.ID, &x.File, &x.Title, &x.Parent, &x.Lifetime, &x.State, &created, &updated,
			&done, &archived, &x.External, &x.TitleTrusted, &x.OpenedBy, &x.Version, &links); err != nil {
			return nil, err
		}
		x.Links = strings.Fields(links.String)
		x.Created, x.Updated, x.Done, x.Archived = time.Unix(created, 0), time.Unix(updated, 0), timeOr0(done), timeOr0(archived)
		out = append(out, x)
	}
	return out, rows.Err()
}
