package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/Alexis-Marcel/eriphe-system/core/a2ui"
)

// Surfaces (#417): the tiles of the workspace, in A2UI v1.0 (core/a2ui).
// A surface is written by an author (the brain, a skill, a thread's lead,
// a task; or code itself: the attention queue's, a thread's,
// thread_surface.go) and kept by code: a title, components from the catalog, data,
// sources, a refresh, a lifetime, a weight and a why.
//
// A source is a read the author declares, at a JSON pointer of the data
// model: a call of any catalog tool with fixed arguments
// ({"tool": "server/tool", "args": {…}}) or one of a server's resources
// ({"resource": "server", "uri": "…"}). Code runs it as a monitor runs its
// look (monitors.go: sourceWhy, readSource): a free read of the brain's,
// read-only by the tool's own annotation, or a resource the server lists;
// within a timeout; its result put at the pointer as it came, never
// understood. A server not trusted taints the surface. Nothing here knows
// a server or a tool: any tool linked tomorrow is a source with no code.
//
// The paths the components bind must exist: in the composed data, or under
// a source in what it gave (its first result) or in its tool's
// outputSchema. Otherwise the author gets an error where it bound.
//
// Code refreshes the sources, never a model: on a resource's update (the
// server tells, pool_follow.go), every N minutes (1 to 1440, on the
// surfaces' own loop, runSurfaces), and when the user opens the surface (surfaceOpened).
// Only a path whose value changed goes out (updateDataModel). Code removes
// a surface when its lifetime ends: a date (until), a ref no longer
// pending or running (while), or its author gone (kept).
//
// Every change goes out as A2UI messages, numbered in order, to whoever
// subscribed (surfaceSubscribe): the workspace's event stream
// (surfaces_stream.go).

const surfacesSchema = `CREATE TABLE IF NOT EXISTS surfaces (
  id TEXT PRIMARY KEY, title TEXT NOT NULL, author TEXT NOT NULL,
  components TEXT NOT NULL, -- A2UI, a flat list
  sources TEXT NOT NULL DEFAULT '{}', -- JSON pointer → {tool, args} or {resource, uri}
  data TEXT NOT NULL DEFAULT '{}', -- the data model: the composed values and the sources' results
  data_at INTEGER NOT NULL DEFAULT 0, -- when the author wrote its values (composed), 0 for none
  refresh TEXT NOT NULL DEFAULT '{}', lifetime TEXT NOT NULL DEFAULT '{}',
  weight INTEGER NOT NULL DEFAULT 50, why TEXT NOT NULL DEFAULT '',
  tainted TEXT NOT NULL DEFAULT '', -- why its content is external, comma-separated; '' when not
  look TEXT NOT NULL DEFAULT '{}', -- its sources' last reads
  next_at INTEGER NOT NULL DEFAULT 0, created INTEGER NOT NULL, updated INTEGER NOT NULL,
  subject TEXT NOT NULL DEFAULT ''); -- what it is about, kind:slug (subjects.go)`

func init() { extraSchema = append(extraSchema, surfacesSchema) }

const (
	surfacesMax        = 100
	surfaceSourcesMax  = 8
	surfaceTitleMax    = 120
	surfaceWhyMax      = 500
	surfaceJSONMax     = 64 << 10  // components, composed data
	surfaceValueMax    = 256 << 10 // a source's result
	surfaceTimeout     = 20 * time.Second
	surfacesTickEvery  = 15 * time.Second
	surfaceReaders     = 4               // refreshes reading at once
	surfaceRefreshCap  = 8 * time.Second // a scheduled refresh's reads, well under the tick
	surfaceEveryDef    = 5               // minutes, when a surface has sources
	surfaceUntilDef    = 24 * time.Hour
	surfaceUntilMax    = 90 * 24 * time.Hour
	surfaceOpenFresh   = 30 * time.Second // opened again sooner: no new read
	surfaceWeightDef   = 50
	surfaceSubBuffer   = 256
	surfaceExtension   = "eriphe" // the key of Eriphe's metadata in createSurface
	surfaceByUser      = "user"
	surfaceByLifetime  = "lifetime"
	surfaceAuthorBrain = "brain"
	surfaceAuthorCore  = "core" // code itself: the attention queue's surfaces (workspace_queue.go), never an assistant
)

var (
	errNoSurface    = errors.New("no such surface")
	surfaceIDRe     = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)
	surfaceAuthorRe = regexp.MustCompile(`^(brain|skill:[a-z0-9][a-z0-9-]{0,63}|lead:[0-9]{1,18}|task:[A-Za-z0-9_-]{1,64})$`)
)

// surfaceSource is one read of a surface: a tool ("server/tool") with
// fixed arguments, or a server's resource by its URI.
type surfaceSource struct {
	Tool     string         `json:"tool,omitempty"`
	Args     map[string]any `json:"args,omitempty"`
	Resource string         `json:"resource,omitempty"` // the server
	URI      string         `json:"uri,omitempty"`
}

// spec is the source as a monitor's look: what sourceWhy and readSource take.
func (s surfaceSource) spec() monitorSpec {
	if s.Resource != "" {
		return monitorSpec{Way: wayPoll, Server: s.Resource, Resource: s.URI}
	}
	server, tool, _ := strings.Cut(s.Tool, "/")
	return monitorSpec{Way: wayPoll, Server: server, Tool: tool, Args: s.Args}
}

// String names the source in a message: server/tool, or server and URI.
func (s surfaceSource) String() string {
	if s.Resource != "" {
		return s.Resource + " " + s.URI
	}
	return s.Tool
}

// surfaceRefresh is how often code reads its sources again, besides a
// resource's update and the surface opened.
type surfaceRefresh struct {
	Every int `json:"every,omitempty"` // minutes; 0: never on a clock
}

// surfaceLifetime is when code removes a surface: one of a date, a ref
// that must stay pending or running (task:<id>, thread:<id>, card:<id>),
// or as long as its author is there.
type surfaceLifetime struct {
	Until time.Time `json:"until,omitzero"`
	While string    `json:"while,omitempty"`
	Kept  bool      `json:"kept,omitempty"`
}

// surfaceIn is what an author writes of a surface. ID empty: a new one.
type surfaceIn struct {
	ID         string                   `json:"id,omitempty"`
	Title      string                   `json:"title"`
	Components json.RawMessage          `json:"components"`
	Data       json.RawMessage          `json:"data,omitempty"` // composed: the values the author wrote, an object
	Sources    map[string]surfaceSource `json:"sources,omitempty"`
	Refresh    surfaceRefresh           `json:"refresh,omitzero"`
	Lifetime   surfaceLifetime          `json:"lifetime,omitzero"`
	Weight     int                      `json:"weight,omitempty"` // 1 to 100; 50 if 0
	Why        string                   `json:"why,omitempty"`
	// Subject is what it is about, given; a new surface without one takes
	// inherit (its writer's turn or task), else its author's (a thread's,
	// a lead's, a task's: sourceSubject). A surface written again keeps
	// the one it had unless one is given.
	Subject string `json:"-"`
	inherit string
	// Tainted is why what the author wrote may come from outside: the
	// turn's taint (turnTaint) of the assistant that writes it.
	Tainted []string `json:"-"`
	// wait caps the sources' first read (0: each its whole timeout). A
	// source still reading then is accepted waiting, read on in the
	// background, and its bindings checked when it comes
	// (workspace_tools.go: an author's turn is never held long).
	wait time.Duration
	// What code writes of a surface it keeps (a thread's,
	// thread_surface.go): composed by code from parts each checked alone
	// (its actions are not checked again as an author's); why its own
	// parts may come from outside; whether its Proposals answer in place
	// despite them; its one-line summary.
	composed  bool
	codeTaint []string
	answers   bool
	summary   string
}

// surface is one stored surface.
type surface struct {
	ID         string                   `json:"id"`
	Title      string                   `json:"title"`
	Author     string                   `json:"author"`
	Components json.RawMessage          `json:"components"`
	Sources    map[string]surfaceSource `json:"sources,omitempty"`
	Data       json.RawMessage          `json:"data"`
	DataAt     time.Time                `json:"data_at,omitzero"`
	Refresh    surfaceRefresh           `json:"refresh,omitzero"`
	Lifetime   surfaceLifetime          `json:"lifetime"`
	Weight     int                      `json:"weight"`
	Why        string                   `json:"why,omitempty"`
	Tainted    string                   `json:"tainted,omitempty"`
	Subject    string                   `json:"subject,omitempty"`
	Look       surfaceLook              `json:"look,omitzero"`
	NextAt     time.Time                `json:"-"`
	Created    time.Time                `json:"created"`
	Updated    time.Time                `json:"updated"`
}

// surfaceLook is what code knows of its sources' reads.
type surfaceLook struct {
	AuthorTaint []string               `json:"author_taint,omitempty"`
	Read        int64                  `json:"read,omitempty"` // the last read of all its sources, unix
	Sources     map[string]sourceState `json:"sources,omitempty"`
	// What came back about it (surfaces_stream.go): the user's last
	// action on it, the last error for its author to fix.
	Action    *surfaceActed     `json:"action,omitempty"`
	LastError *surfaceLastError `json:"last_error,omitempty"`
	// What code keeps of a surface it writes (thread_surface.go): why its
	// own parts may come from outside, whether its Proposals answer in
	// place all the same, and its one-line summary for a small tile.
	CodeTaint []string `json:"code_taint,omitempty"`
	Answers   bool     `json:"answers,omitempty"`
	Summary   string   `json:"summary,omitempty"`
}

// sourceState is a source's last read: when, and why it failed or waits.
type sourceState struct {
	Read    int64  `json:"read,omitempty"`
	Error   string `json:"error,omitempty"`
	Waiting string `json:"waiting,omitempty"` // sourceWhy refused it: nothing was called
}

// surfaceChange is one A2UI message about a surface, numbered: Seq grows
// by one with each message, in the order they are sent. It starts again
// with eriphe-core; a subscriber that reconnects starts from a snapshot.
type surfaceChange struct {
	Seq     int64        `json:"seq"`
	Message a2ui.Message `json:"message"`
}

// surfaceHub orders the changes and hands them to the subscribers.
type surfaceHub struct {
	mu   sync.Mutex // one change at a time: the row written, then its messages numbered and sent
	seq  int64
	subs map[chan surfaceChange]struct{}

	reading sync.Map // surface id → a refresh in progress

	// The refresh worker (runSurfaces): due holds the surfaces whose
	// scheduled refresh is queued or running, slots bounds the refreshes
	// reading at once, refreshing counts those not over (tests wait on it).
	due        sync.Map
	slotsOnce  sync.Once
	slots      chan struct{}
	refreshing sync.WaitGroup

	keyOnce sync.Once
	key     []byte // signs the media addresses (surfaces_media.go)
}

const surfaceCols = `id, title, author, components, sources, data, data_at, refresh, lifetime, weight, why, tainted, look, next_at, created, updated, subject`

func scanSurface(row interface{ Scan(...any) error }) (surface, error) {
	var s surface
	var comps, sources, data, refresh, lifetime, look string
	var dataAt, next, created, updated int64
	if err := row.Scan(&s.ID, &s.Title, &s.Author, &comps, &sources, &data, &dataAt, &refresh, &lifetime, &s.Weight, &s.Why,
		&s.Tainted, &look, &next, &created, &updated, &s.Subject); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return s, errNoSurface
		}
		return s, err
	}
	s.Components, s.Data = json.RawMessage(comps), json.RawMessage(data)
	json.Unmarshal([]byte(sources), &s.Sources)
	json.Unmarshal([]byte(refresh), &s.Refresh)
	json.Unmarshal([]byte(lifetime), &s.Lifetime)
	json.Unmarshal([]byte(look), &s.Look)
	if dataAt > 0 {
		s.DataAt = time.Unix(dataAt, 0)
	}
	s.NextAt, s.Created, s.Updated = time.Unix(next, 0), time.Unix(created, 0), time.Unix(updated, 0)
	return s, nil
}

// surfaces are all the surfaces, the heaviest first, then the latest.
func (c *core) surfaces() ([]surface, error) {
	rows, err := c.store.db.Query(`SELECT ` + surfaceCols + ` FROM surfaces ORDER BY weight DESC, updated DESC, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []surface
	for rows.Next() {
		s, err := scanSurface(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// surface is one surface by its id; errNoSurface if there is none.
func (c *core) surface(id string) (surface, error) {
	return scanSurface(c.store.db.QueryRow(`SELECT `+surfaceCols+` FROM surfaces WHERE id = ?`, id))
}

// sourceRead is what one read of a source gave.
type sourceRead struct {
	value any
	err   string // it failed
	why   string // sourceWhy refused it: nothing was called
}

// surfaceWrite writes a surface for its author: a new one (in.ID empty, or
// an id not taken), or one of the author's own again. Its sources are read
// once, so the paths its components bind are checked against what they
// give. What is wrong with the A2UI or the bindings comes back as
// validation errors, for the author to fix; a refused source or a wrong
// field as an error.
func (c *core) surfaceWrite(author string, in surfaceIn) (surface, []a2ui.ValidationError, error) {
	if !surfaceAuthorRe.MatchString(author) {
		return surface{}, nil, fmt.Errorf("author %q: brain, skill:<name>, lead:<thread>, task:<id>", author)
	}
	return c.writeSurface(author, in)
}

// writeSurface is surfaceWrite for any author, code's own (surfaceAuthorCore,
// thread:<id>) too: its surfaces do not count in surfacesMax, so a full pool
// never refuses what the queue or a thread needs to show.
func (c *core) writeSurface(author string, in surfaceIn) (surface, []a2ui.ValidationError, error) {
	in.ID = strings.TrimSpace(in.ID)
	if in.ID == "" {
		in.ID = newSurfaceID()
	}
	if !surfaceIDRe.MatchString(in.ID) {
		return surface{}, nil, errors.New("a surface's id is lowercase letters, digits and dashes")
	}
	in.Title, in.Why = strings.TrimSpace(in.Title), strings.TrimSpace(in.Why)
	switch {
	case in.Title == "":
		return surface{}, nil, errors.New("a surface needs a title")
	case utf8.RuneCountInString(in.Title) > surfaceTitleMax:
		return surface{}, nil, fmt.Errorf("a title of %d characters at most", surfaceTitleMax)
	case utf8.RuneCountInString(in.Why) > surfaceWhyMax:
		return surface{}, nil, fmt.Errorf("a why of %d characters at most", surfaceWhyMax)
	case len(in.Components) > surfaceJSONMax || len(in.Data) > surfaceJSONMax:
		return surface{}, nil, fmt.Errorf("components and data of %d KiB at most each", surfaceJSONMax>>10)
	case len(in.Sources) > surfaceSourcesMax:
		return surface{}, nil, fmt.Errorf("%d sources at most", surfaceSourcesMax)
	case in.Weight < 0 || in.Weight > 100:
		return surface{}, nil, errors.New("weight is 1 to 100")
	}
	if in.Weight == 0 {
		in.Weight = surfaceWeightDef
	}
	if !keptByCode(author) {
		in.Weight = c.authorWeight(in.ID, in.Weight)
	}
	now := time.Now()
	life, err := c.checkLifetime(in.Lifetime, now)
	if err != nil {
		return surface{}, nil, err
	}
	in.Lifetime = life
	if err := c.checkSources(in.Sources); err != nil {
		return surface{}, nil, err
	}
	if len(in.Sources) > 0 && in.Refresh.Every == 0 {
		in.Refresh.Every = surfaceEveryDef
	}
	if d := time.Duration(in.Refresh.Every) * time.Minute; in.Refresh.Every != 0 &&
		(len(in.Sources) == 0 || d < monitorEveryMin || d > monitorEveryMax) {
		return surface{}, nil, errors.New("refresh every is 1 to 1440 minutes, for a surface with sources")
	}
	composed := map[string]any{}
	if t := strings.TrimSpace(string(in.Data)); t != "" && t != "null" {
		if err := json.Unmarshal(in.Data, &composed); err != nil || composed == nil {
			return surface{}, nil, errors.New("data is an object: the values the components bind")
		}
	}
	rawComposed, _ := json.Marshal(composed)
	if errs := a2ui.ValidateSurface(a2ui.Surface{ID: in.ID, Components: in.Components, DataModel: rawComposed}); len(errs) > 0 {
		return surface{}, errs, nil
	}
	if errs := actionGuard(in.ID, in.Components, len(in.Tainted) > 0); len(errs) > 0 && !in.composed {
		return surface{}, errs, nil
	}
	if errs := a2ui.CheckLinks(in.ID, in.Components); len(errs) > 0 {
		return surface{}, errs, nil
	}
	// A thread's surface id is its thread's (thread_surface.go).
	if n, ok := threadOfSurface(in.ID); ok && author != threadAuthor(n) {
		return surface{}, nil, fmt.Errorf("the surface %s is its thread's, kept by code: write another id", in.ID)
	}
	// The author's own surface only (the brain writes any, which stays its
	// author's); the id of another's is taken.
	if old, err := c.surface(in.ID); err == nil && !mayChange(author, old.Author) {
		return surface{}, nil, fmt.Errorf("the surface %s is %s's: write another id", in.ID, old.Author)
	}

	// Its sources, read once (no lock held: a tool may be slow); those
	// still reading after in.wait go on in the background.
	reads, late := c.readSourcesWithin(in.Sources, in.wait)
	data, err := mergeSources(composed, in.Sources, reads)
	if err != nil {
		return surface{}, nil, err
	}
	if errs := c.checkBindings(in.ID, in.Components, data, in.Sources, reads); len(errs) > 0 {
		return surface{}, errs, nil
	}
	look := surfaceLook{AuthorTaint: slices.Compact(slices.Sorted(slices.Values(in.Tainted))), Read: now.Unix(),
		CodeTaint: slices.Compact(slices.Sorted(slices.Values(in.codeTaint))), Answers: in.answers, Summary: in.summary}
	noteReads(&look, reads, now)
	rawData, _ := json.Marshal(data)
	sources, _ := json.Marshal(orEmpty(in.Sources))
	refresh, _ := json.Marshal(in.Refresh)
	lifetime, _ := json.Marshal(in.Lifetime)
	rawLook, _ := json.Marshal(look)
	var dataAt int64
	if len(composed) > 0 {
		dataAt = now.Unix()
	}
	next := int64(0)
	if in.Refresh.Every > 0 {
		next = now.Add(time.Duration(in.Refresh.Every) * time.Minute).Unix()
	}
	tainted := c.surfaceTaint(look, in.Sources)
	in.Subject = c.store.keepSubject(in.Subject, "")
	subject := in.Subject // a new surface's
	if subject == "" {
		subject = c.store.keepSubject(orStr(in.inherit, c.sourceSubject(author)), "")
	}

	c.surf.mu.Lock()
	old, err := c.surface(in.ID)
	switch {
	case err == nil && !mayChange(author, old.Author):
		c.surf.mu.Unlock()
		return surface{}, nil, fmt.Errorf("the surface %s is %s's: write another id", in.ID, old.Author)
	case errors.Is(err, errNoSurface):
		var n int
		c.store.db.QueryRow(`SELECT COUNT(*) FROM surfaces WHERE author != ? AND author NOT LIKE 'thread:%'`, surfaceAuthorCore).Scan(&n)
		if n >= surfacesMax && !keptByCode(author) {
			c.surf.mu.Unlock()
			return surface{}, nil, fmt.Errorf("%d surfaces at most: remove one first", surfacesMax)
		}
		_, err = c.store.db.Exec(`INSERT INTO surfaces (`+surfaceCols+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			in.ID, in.Title, author, string(in.Components), string(sources), string(rawData), dataAt, string(refresh), string(lifetime),
			in.Weight, in.Why, tainted, string(rawLook), next, now.Unix(), now.Unix(), subject)
	case err == nil:
		_, err = c.store.db.Exec(`UPDATE surfaces SET title = ?, components = ?, sources = ?, data = ?, data_at = ?, refresh = ?,
			lifetime = ?, weight = ?, why = ?, tainted = ?, look = ?, next_at = ?, updated = ?,
			subject = CASE WHEN ? != '' THEN ? ELSE subject END WHERE id = ?`,
			in.Title, string(in.Components), string(sources), string(rawData), dataAt, string(refresh), string(lifetime),
			in.Weight, in.Why, tainted, string(rawLook), next, now.Unix(), in.Subject, in.Subject, in.ID)
	}
	if err != nil {
		c.surf.mu.Unlock()
		return surface{}, nil, err
	}
	s, err := c.surface(in.ID)
	if err == nil {
		if old.ID == "" {
			c.sendSurface(c.createMessage(s))
		} else {
			c.sendSurface(c.rewriteMessages(old, s)...)
		}
	}
	c.surf.mu.Unlock()
	c.followSurfaceResources()
	if err == nil && late != nil {
		go c.surfaceLateReads(s, late)
	}
	c.relayout()
	return s, nil, err
}

// mayChange tells whether an author may write or remove a surface of
// owner's: its own; the brain any but code's (the attention queue's, kept
// by code while its item is open: workspace_queue.go; a thread's, kept
// while it lives: thread_surface.go).
func mayChange(author, owner string) bool {
	return author == owner || (author == surfaceAuthorBrain && !keptByCode(owner))
}

// surfaceRemove removes a surface: by its author, the user (surfaceByUser)
// or code when its lifetime ended (surfaceByLifetime).
func (c *core) surfaceRemove(id, by string) error {
	c.surf.mu.Lock()
	s, err := c.surface(id)
	if err != nil {
		c.surf.mu.Unlock()
		return err
	}
	if !mayChange(by, s.Author) && by != surfaceByUser && by != surfaceByLifetime {
		c.surf.mu.Unlock()
		return fmt.Errorf("the surface %s is %s's", id, s.Author)
	}
	if _, err := c.store.db.Exec(`DELETE FROM surfaces WHERE id = ?`, id); err != nil {
		c.surf.mu.Unlock()
		return err
	}
	c.sendSurface(a2ui.Message{Version: a2ui.Version, DeleteSurface: &a2ui.DeleteSurface{SurfaceID: id}})
	c.surf.mu.Unlock()
	c.dropSurfaceMedia(id) // surfaces_capture.go
	log.Printf("surface %s removed by %s", id, by)
	if s.Author == surfaceAuthorCore && by == surfaceByUser {
		c.queueSurfaceDismissed(id) // its item leaves the queue too (workspace_queue.go)
	}
	c.followSurfaceResources()
	c.relayout()
	return nil
}

func newSurfaceID() string {
	b := make([]byte, 6)
	rand.Read(b)
	return "s-" + hex.EncodeToString(b)
}

func orEmpty(m map[string]surfaceSource) map[string]surfaceSource {
	if m == nil {
		return map[string]surfaceSource{}
	}
	return m
}

// checkLifetime checks one lifetime, and gives the default one: a day.
func (c *core) checkLifetime(l surfaceLifetime, now time.Time) (surfaceLifetime, error) {
	l.While = strings.TrimSpace(l.While)
	n := 0
	for _, set := range []bool{!l.Until.IsZero(), l.While != "", l.Kept} {
		if set {
			n++
		}
	}
	switch {
	case n == 0:
		return surfaceLifetime{Until: now.Add(surfaceUntilDef).Truncate(time.Second)}, nil
	case n > 1:
		return l, errors.New("one lifetime: until a date, while a ref, or kept")
	case !l.Until.IsZero() && (!l.Until.After(now) || l.Until.Sub(now) > surfaceUntilMax):
		return l, errors.New("until is a date to come, within 90 days")
	case l.While != "":
		kind, _, _ := strings.Cut(l.While, ":")
		if kind != "task" && kind != "thread" && kind != "card" {
			return l, errors.New("while is a ref: task:<id>, thread:<id> or card:<id>")
		}
		if !c.refLive(l.While) {
			return l, fmt.Errorf("%s is not pending or running", l.While)
		}
	}
	return l, nil
}

// checkSources checks each source's shape and that code may read it.
func (c *core) checkSources(srcs map[string]surfaceSource) error {
	var ptrs []string
	for ptr, s := range srcs {
		if ptr == "" || ptr == "/" || !strings.HasPrefix(ptr, "/") || strings.HasSuffix(ptr, "/") {
			return fmt.Errorf("source %q: a JSON pointer in the data, under its root (/mails)", ptr)
		}
		if strings.Contains(ptr, "*") || pointerBad(ptr) {
			return fmt.Errorf("source %q: a JSON pointer, ~0 for ~ and ~1 for /", ptr)
		}
		switch {
		case (s.Tool == "") == (s.Resource == ""):
			return fmt.Errorf("source %s: a tool (server/tool, with args) or a resource (server, with uri)", ptr)
		case s.Tool != "" && (!strings.Contains(s.Tool, "/") || s.URI != ""):
			return fmt.Errorf("source %s: tool is server/tool", ptr)
		case s.Resource != "" && (s.URI == "" || len(s.Args) > 0):
			return fmt.Errorf("source %s: a resource is read by its uri, without arguments", ptr)
		}
		if raw, _ := json.Marshal(s.Args); len(raw) > monitorArgsMax {
			return fmt.Errorf("source %s: args of %d bytes at most", ptr, monitorArgsMax)
		}
		if why := c.sourceWhy(s.spec()); why != "" {
			return fmt.Errorf("source %s: %s", ptr, why)
		}
		ptrs = append(ptrs, ptr)
	}
	sort.Strings(ptrs)
	for i := 1; i < len(ptrs); i++ {
		if strings.HasPrefix(ptrs[i], ptrs[i-1]+"/") {
			return fmt.Errorf("sources %s and %s: one inside the other", ptrs[i-1], ptrs[i])
		}
	}
	return nil
}

func pointerBad(p string) bool {
	for i := 0; i < len(p); i++ {
		if p[i] == '~' && (i+1 == len(p) || (p[i+1] != '0' && p[i+1] != '1')) {
			return true
		}
	}
	return false
}

// readSources reads the sources (only those at the pointers in only, if
// given), each under the guards, at once.
func (c *core) readSources(ctx context.Context, srcs map[string]surfaceSource, only []string) map[string]sourceRead {
	out := map[string]sourceRead{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for ptr, s := range srcs {
		if only != nil && !slices.Contains(only, ptr) {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := c.readOne(ctx, s)
			mu.Lock()
			out[ptr] = r
			mu.Unlock()
		}()
	}
	wg.Wait()
	return out
}

func (c *core) readOne(ctx context.Context, s surfaceSource) sourceRead {
	spec := s.spec()
	if why := c.sourceWhy(spec); why != "" {
		return sourceRead{why: why}
	}
	v, err := c.readSource(ctx, spec, surfaceTimeout)
	if err != nil {
		return sourceRead{err: truncate(err.Error(), 300)}
	}
	if raw, _ := json.Marshal(v); len(raw) > surfaceValueMax {
		return sourceRead{err: fmt.Sprintf("%s gave %d KiB, more than %d: narrow its arguments", s, len(raw)>>10, surfaceValueMax>>10)}
	}
	return sourceRead{value: v}
}

func noteReads(look *surfaceLook, reads map[string]sourceRead, now time.Time) {
	if look.Sources == nil {
		look.Sources = map[string]sourceState{}
	}
	for ptr, r := range reads {
		st := look.Sources[ptr]
		st.Error, st.Waiting = r.err, r.why
		if r.err == "" && r.why == "" {
			st.Read = now.Unix()
		}
		look.Sources[ptr] = st
	}
}

// surfaceTaint is why a surface's content comes from outside: its author's
// turn, what code wrote from outside (a thread's step), and each source on
// a server not trusted.
func (c *core) surfaceTaint(look surfaceLook, srcs map[string]surfaceSource) string {
	out := append(slices.Clone(look.AuthorTaint), look.CodeTaint...)
	for _, s := range srcs {
		if !c.trustsServer(s.spec().Server) {
			out = append(out, s.String())
		}
	}
	slices.Sort(out)
	return strings.Join(slices.Compact(out), ", ")
}

// mergeSources puts each source's result at its pointer in a copy of the
// composed data; a source not read leaves its place empty.
func mergeSources(composed map[string]any, srcs map[string]surfaceSource, reads map[string]sourceRead) (map[string]any, error) {
	data := deepCopy(composed).(map[string]any)
	for ptr := range srcs {
		r, ok := reads[ptr]
		if !ok || r.err != "" || r.why != "" {
			continue
		}
		if err := setPointer(data, ptr, r.value); err != nil {
			return nil, fmt.Errorf("source %s: %w", ptr, err)
		}
	}
	return data, nil
}

func deepCopy(v any) any {
	raw, _ := json.Marshal(v)
	var out any
	json.Unmarshal(raw, &out)
	if out == nil {
		if _, ok := v.(map[string]any); ok {
			return map[string]any{}
		}
	}
	return out
}

// tokens splits a JSON pointer, each token unescaped.
func tokens(ptr string) []string {
	if ptr == "" || ptr == "/" {
		return nil
	}
	parts := strings.Split(strings.TrimPrefix(ptr, "/"), "/")
	for i, p := range parts {
		parts[i] = strings.ReplaceAll(strings.ReplaceAll(p, "~1", "/"), "~0", "~")
	}
	return parts
}

// setPointer sets the value at a pointer, making the objects on its way.
func setPointer(data map[string]any, ptr string, v any) error {
	toks := tokens(ptr)
	cur := data
	for i, t := range toks[:len(toks)-1] {
		next, ok := cur[t]
		if !ok || next == nil {
			m := map[string]any{}
			cur[t] = m
			cur = m
			continue
		}
		m, ok := next.(map[string]any)
		if !ok {
			return fmt.Errorf("/%s is not an object in the data", strings.Join(toks[:i+1], "/"))
		}
		cur = m
	}
	cur[toks[len(toks)-1]] = v
	return nil
}

// getPointer reads the value at a pointer.
func getPointer(data any, ptr string) (any, bool) {
	cur := data
	for _, t := range tokens(ptr) {
		switch x := cur.(type) {
		case map[string]any:
			v, ok := x[t]
			if !ok {
				return nil, false
			}
			cur = v
		case []any:
			i, err := strconv.Atoi(t)
			if err != nil || i < 0 || i >= len(x) {
				return nil, false
			}
			cur = x[i]
		default:
			return nil, false
		}
	}
	return cur, true
}

// sourceCovering is the source whose pointer holds path, and the rest of path
// under it; "" if none does.
func sourceCovering(path string, srcs map[string]surfaceSource) (string, []string) {
	best := ""
	for ptr := range srcs {
		if (path == ptr || strings.HasPrefix(path, ptr+"/")) && len(ptr) > len(best) {
			best = ptr
		}
	}
	if best == "" {
		return "", nil
	}
	return best, tokens(strings.TrimPrefix(path, best))
}

// checkBindings checks that what the components read exists: under a
// source, in what it gave or in its tool's outputSchema (a source that
// could not be read and has no schema is not checked: it says why on the
// surface); elsewhere, in the data. An empty list cannot tell what its
// items hold: what is under it is not checked.
func (c *core) checkBindings(id string, comps json.RawMessage, data map[string]any, srcs map[string]surfaceSource,
	reads map[string]sourceRead) []a2ui.ValidationError {
	var errs []a2ui.ValidationError
	fail := func(b a2ui.Bound, msg string) {
		errs = append(errs, a2ui.ValidationError{Code: a2ui.CodeValidationFailed, SurfaceID: id, Path: "/components" + b.At, Message: msg})
	}
	for _, b := range a2ui.Eriphe().BoundPaths(comps) {
		if b.Link && !linksOK(valuesAt(data, tokens(b.Path))) {
			fail(b, fmt.Sprintf("a Link opens a field that is already an http(s) address as a whole, and %s holds something else: bind the address a tool gives, or write it as it is", b.Path))
			continue
		}
		ptr, rest := sourceCovering(b.Path, srcs)
		if ptr == "" {
			if ok, hint := lookup(data, tokens(b.Path), b.List); !ok {
				fail(b, fmt.Sprintf("nothing at %s in the surface's data%s: write it in data, or read it from a source", b.Path, hint))
			}
			continue
		}
		src, r := srcs[ptr], reads[ptr]
		schema := c.outputSchema(src)
		if r.err == "" && r.why == "" {
			ok, hint := lookup(r.value, rest, b.List)
			if ok {
				continue
			}
			if _, has := schemaHas(schema, rest, b.List); has {
				continue // the tool says it may give it: this result did not
			}
			fail(b, fmt.Sprintf("nothing at %s in what %s gives%s", b.Path, src, hint))
			continue
		}
		if schema == nil {
			continue // not read, nothing to check against: the surface says why
		}
		if known, has := schemaHas(schema, rest, b.List); known && !has {
			fail(b, fmt.Sprintf("%s's outputSchema has nothing at %s", src, b.Path))
		}
	}
	return errs
}

// linksOK tells whether the values bound to a Link's url are addresses it
// may open (a2ui.LinkURLOK), or empty: no link.
func linksOK(vs []any) bool {
	for _, v := range vs {
		if u, ok := v.(string); (!ok && v != nil) || (ok && u != "" && !a2ui.LinkURLOK(u)) {
			return false
		}
	}
	return true
}

// lookup tells whether a path leads somewhere in v ("*": in an item of a
// list, an empty list telling nothing); if not, a hint of what is there.
func lookup(v any, toks []string, list bool) (bool, string) {
	if len(toks) == 0 {
		if _, ok := v.([]any); list && !ok {
			return false, " (not a list)"
		}
		return true, ""
	}
	t, rest := toks[0], toks[1:]
	switch x := v.(type) {
	case map[string]any:
		if t == "*" {
			return false, " (an object, not a list)"
		}
		next, ok := x[t]
		if !ok {
			return false, fieldsHint(x)
		}
		return lookup(next, rest, list)
	case []any:
		if t == "*" {
			if len(x) == 0 {
				return true, ""
			}
			hint := ""
			for _, it := range x {
				ok, h := lookup(it, rest, list)
				if ok {
					return true, ""
				}
				if hint == "" {
					hint = h
				}
			}
			return false, hint
		}
		i, err := strconv.Atoi(t)
		if err != nil || i < 0 || i >= len(x) {
			return false, " (a list: bind its items through a template)"
		}
		return lookup(x[i], rest, list)
	}
	return false, ""
}

func fieldsHint(m map[string]any) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if len(keys) == 0 {
		return " (an empty object)"
	}
	if len(keys) > 12 {
		keys = append(keys[:12], "…")
	}
	return " (there: " + strings.Join(keys, ", ") + ")"
}

// outputSchema is a source's tool's outputSchema, nil for a resource or a
// tool without one.
func (c *core) outputSchema(s surfaceSource) map[string]any {
	if s.Tool == "" {
		return nil
	}
	spec := s.spec()
	u := c.pool.get(spec.Server)
	if u == nil {
		return nil
	}
	t := u.tool(spec.Tool)
	if t == nil || t.OutputSchema == nil {
		return nil
	}
	raw, _ := json.Marshal(t.OutputSchema)
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return nil
	}
	return m
}

// schemaHas tells whether a schema says a path exists (has), and whether it
// can tell at all (known): a schema that leaves a place open
// ($ref, no properties, additional properties) cannot.
func schemaHas(s map[string]any, toks []string, list bool) (known, has bool) {
	if s == nil {
		return false, false
	}
	if _, ok := s["$ref"]; ok {
		return false, false
	}
	for _, k := range []string{"anyOf", "oneOf", "allOf"} {
		if subs, ok := s[k].([]any); ok {
			known = true
			for _, sub := range subs {
				m, _ := sub.(map[string]any)
				k, h := schemaHas(m, toks, list)
				if h {
					return true, true
				}
				if !k {
					known = false
				}
			}
			return known, false
		}
	}
	if len(toks) == 0 {
		if !list {
			return true, true
		}
		switch t := s["type"].(type) {
		case string:
			return true, t == "array"
		case []any:
			return true, slices.Contains(t, any("array"))
		}
		return false, false
	}
	t, rest := toks[0], toks[1:]
	if t == "*" || isIndex(t) {
		items, ok := s["items"].(map[string]any)
		if !ok {
			return false, false
		}
		return schemaHas(items, rest, list)
	}
	props, _ := s["properties"].(map[string]any)
	if p, ok := props[t].(map[string]any); ok {
		return schemaHas(p, rest, list)
	}
	switch add := s["additionalProperties"].(type) {
	case map[string]any:
		return schemaHas(add, rest, list)
	case bool:
		if add {
			return false, false
		}
		return true, false
	}
	if props == nil {
		return false, false
	}
	return true, false // the tool said its fields
}

func isIndex(t string) bool {
	_, err := strconv.Atoi(t)
	return err == nil
}

// A surface's life: refresh and removal.

// runSurfaces is the surfaces' loop, apart from the monitors': a slow
// source never delays a monitor's look.
func (c *core) runSurfaces(ctx context.Context) {
	tick := time.NewTicker(surfacesTickEvery)
	defer tick.Stop()
	for {
		c.tickSurfaces(ctx, c.notifyTime())
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// tickSurfaces removes the surfaces whose lifetime ended, hands the
// sources whose interval came to the refresh worker, and keeps the
// resources followed. It never waits for a read.
func (c *core) tickSurfaces(ctx context.Context, now time.Time) {
	all, err := c.surfaces()
	if err != nil {
		log.Printf("surfaces: %v", err)
		return
	}
	for _, s := range all {
		if c.surfaceEnded(s, now) {
			c.surfaceRemove(s.ID, surfaceByLifetime)
			continue
		}
		if s.Refresh.Every > 0 && len(s.Sources) > 0 && !s.NextAt.After(now) {
			if _, queued := c.surf.due.LoadOrStore(s.ID, true); queued {
				continue // still waiting for a slot, or reading
			}
			c.refreshLater(ctx, s.ID, now, func() { c.surf.due.Delete(s.ID) })
		}
	}
	c.sweepSurfaceMedia(all, now) // surfaces_capture.go
	c.followSurfaceResources()
}

// refreshLater runs a scheduled refreshSurface on the worker: in its own
// goroutine, once one of surfaceReaders slots is free, its reads cut at
// surfaceRefreshCap. done runs after it.
func (c *core) refreshLater(ctx context.Context, id string, now time.Time, done func()) {
	c.surf.slotsOnce.Do(func() { c.surf.slots = make(chan struct{}, surfaceReaders) })
	c.surf.refreshing.Add(1)
	go func() {
		defer c.surf.refreshing.Done()
		defer done()
		select {
		case c.surf.slots <- struct{}{}:
		case <-ctx.Done():
			return
		}
		defer func() { <-c.surf.slots }()
		c.refreshWithin(ctx, id, nil, now, surfaceRefreshCap)
	}()
}

// surfaceEnded tells whether a surface's lifetime is over.
func (c *core) surfaceEnded(s surface, now time.Time) bool {
	switch l := s.Lifetime; {
	case !l.Until.IsZero():
		return !now.Before(l.Until)
	case l.While != "":
		return !c.refLive(l.While)
	case l.Kept:
		return !c.authorLive(s.Author)
	}
	return false
}

// refLive tells whether a ref is still pending or running: a task not over
// (its turn ended counts as over), a thread not ended, a card waiting.
func (c *core) refLive(ref string) bool {
	kind, id, _ := strings.Cut(ref, ":")
	switch kind {
	case "task":
		t, err := c.store.task(id)
		return err == nil && !finished(t.State) && t.State != stateIdle
	case "thread", "lead":
		n, err := strconv.ParseInt(id, 10, 64)
		if err != nil {
			return false
		}
		s, err := c.thread(n)
		return err == nil && s.Ended.IsZero()
	case "card":
		n, err := strconv.ParseInt(id, 10, 64)
		if err != nil {
			return false
		}
		x, err := c.card(n)
		if err != nil {
			return false
		}
		x, _ = c.liveCard(x)
		return x.State == cardPending
	}
	return false
}

// authorLive tells whether a surface's author is still there to keep it.
func (c *core) authorLive(author string) bool {
	kind, id, _ := strings.Cut(author, ":")
	switch kind {
	case surfaceAuthorBrain, surfaceAuthorCore:
		return true
	case "skill":
		_, err := c.store.skill(id)
		return err == nil
	}
	return c.refLive(author)
}

// surfaceOpened reads a surface's sources again because the user opened
// it, unless they were read moments ago. The page's action "open" calls it
// (surfaceAction).
func (c *core) surfaceOpened(ctx context.Context, id string) error {
	s, err := c.surface(id)
	if err != nil {
		return err
	}
	if len(s.Sources) == 0 || time.Since(time.Unix(s.Look.Read, 0)) < surfaceOpenFresh {
		return nil
	}
	c.refreshSurface(ctx, id, nil, time.Now())
	return nil
}

// surfaceResourceUpdated is the pool's hook: a server said a resource
// changed; the surfaces that read it read it again.
func (c *core) surfaceResourceUpdated(server, uri string) {
	all, err := c.surfaces()
	if err != nil {
		return
	}
	for _, s := range all {
		var only []string
		for ptr, src := range s.Sources {
			if src.Resource == server && src.URI == uri {
				only = append(only, ptr)
			}
		}
		if len(only) > 0 { // one at a time a surface: one asked meanwhile returns at once
			go c.refreshSurface(context.Background(), s.ID, only, time.Now())
		}
	}
}

// followSurfaceResources asks each server for the updates of the resources
// the surfaces read, and of those only.
func (c *core) followSurfaceResources() {
	if c.pool == nil {
		return
	}
	want := map[string][]string{}
	for name := range c.conf().Servers {
		want[name] = nil
	}
	all, _ := c.surfaces()
	for _, s := range all {
		for _, src := range s.Sources {
			if src.Resource != "" {
				want[src.Resource] = append(want[src.Resource], src.URI)
			}
		}
	}
	for name, uris := range want {
		if u := c.pool.get(name); u != nil {
			u.follow(uris)
		}
	}
}

// refreshSurface reads a surface's sources again (those at only, if given)
// and sends the paths whose value changed. One refresh of a surface at a
// time: one asked meanwhile runs once the current one is over (all its
// sources), so an update told during a read is not lost.
func (c *core) refreshSurface(ctx context.Context, id string, only []string, now time.Time) {
	c.refreshWithin(ctx, id, only, now, 0)
}

// refreshWithin is refreshSurface with each pass's reads cut at limit (0:
// each read's own surfaceTimeout only).
func (c *core) refreshWithin(ctx context.Context, id string, only []string, now time.Time, limit time.Duration) {
	again := new(atomic.Bool)
	if v, busy := c.surf.reading.LoadOrStore(id, again); busy {
		v.(*atomic.Bool).Store(true)
		return
	}
	defer c.surf.reading.Delete(id)
	pass := func(only []string, now time.Time) {
		ctx := ctx
		if limit > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, limit)
			defer cancel()
		}
		c.refreshOnce(ctx, id, only, now)
	}
	pass(only, now)
	for again.Swap(false) {
		pass(nil, time.Now())
	}
}

func (c *core) refreshOnce(ctx context.Context, id string, only []string, now time.Time) {
	s, err := c.surface(id)
	if err != nil {
		return
	}
	c.applyReads(id, s.Sources, c.readSources(ctx, s.Sources, only), only, now)
}

// applyReads puts what the sources readFrom gave (those at only, if given)
// in the surface as it is now, and sends the paths whose value changed.
func (c *core) applyReads(id string, readFrom map[string]surfaceSource, reads map[string]sourceRead, only []string, now time.Time) {
	c.surf.mu.Lock()
	defer c.surf.mu.Unlock()
	s, err := c.surface(id) // it may have changed meanwhile
	if err != nil {
		return
	}
	var data map[string]any
	if json.Unmarshal(s.Data, &data) != nil || data == nil {
		data = map[string]any{}
	}
	var changed []string
	for ptr, r := range reads {
		// Written again meanwhile with another source there: this read
		// is not its.
		if src, ok := s.Sources[ptr]; !ok || valueText(src) != valueText(readFrom[ptr]) {
			delete(reads, ptr)
			continue
		}
		if r.err != "" || r.why != "" {
			continue
		}
		old, _ := getPointer(data, ptr)
		if valueText(old) == valueText(r.value) {
			continue
		}
		if setPointer(data, ptr, r.value) == nil {
			changed = append(changed, ptr)
		}
	}
	sort.Strings(changed)
	noteReads(&s.Look, reads, now)
	if only == nil {
		s.Look.Read = now.Unix()
	}
	rawData, _ := json.Marshal(data)
	rawLook, _ := json.Marshal(s.Look)
	next := s.NextAt.Unix()
	if only == nil && s.Refresh.Every > 0 {
		next = now.Add(time.Duration(s.Refresh.Every) * time.Minute).Unix()
	}
	tainted := c.surfaceTaint(s.Look, s.Sources)
	updated := s.Updated.Unix()
	if len(changed) > 0 {
		updated = now.Unix()
	}
	if _, err := c.store.db.Exec(`UPDATE surfaces SET data = ?, look = ?, next_at = ?, tainted = ?, updated = ? WHERE id = ?`,
		string(rawData), string(rawLook), next, tainted, updated, id); err != nil {
		log.Printf("surface %s: %v", id, err)
		return
	}
	flipped := (s.Tainted == "") != (tainted == "")
	s.Data, s.Tainted = rawData, tainted
	if flipped { // what is shown around it changed: created again
		s.Updated = time.Unix(updated, 0)
		c.sendSurface(a2ui.Message{Version: a2ui.Version, DeleteSurface: &a2ui.DeleteSurface{SurfaceID: id}}, c.createMessage(s))
		return
	}
	if len(changed) == 0 {
		return
	}
	view := c.surfaceView(s)
	var msgs []a2ui.Message
	for _, ptr := range changed {
		v, _ := getPointer(view.data, ptr)
		raw, _ := json.Marshal(v)
		msgs = append(msgs, a2ui.Message{Version: a2ui.Version, UpdateDataModel: &a2ui.UpdateDataModel{SurfaceID: id, Path: ptr, Value: raw}})
	}
	c.sendSurface(msgs...)
}

// The changes, as A2UI messages.

// surfaceSubscribe gives the surfaces as they are now (one createSurface
// each, numbered with the last change sent) and every change after it, in
// order, on changes. Nothing is missed between the two. A subscriber that
// falls behind (surfaceSubBuffer changes not read) has its channel closed:
// it subscribes again and starts from a new snapshot. cancel ends it.
func (c *core) surfaceSubscribe() (snapshot []surfaceChange, changes <-chan surfaceChange, cancel func()) {
	ch := make(chan surfaceChange, surfaceSubBuffer)
	c.surf.mu.Lock()
	all, _ := c.surfaces()
	for _, s := range all {
		snapshot = append(snapshot, surfaceChange{Seq: c.surf.seq, Message: c.createMessage(s)})
	}
	if c.surf.subs == nil {
		c.surf.subs = map[chan surfaceChange]struct{}{}
	}
	c.surf.subs[ch] = struct{}{}
	c.surf.mu.Unlock()
	var once sync.Once
	return snapshot, ch, func() {
		once.Do(func() {
			c.surf.mu.Lock()
			if _, ok := c.surf.subs[ch]; ok {
				delete(c.surf.subs, ch)
				close(ch)
			}
			c.surf.mu.Unlock()
		})
	}
}

// sendSurface numbers messages and hands them out; c.surf.mu is held.
// It never blocks: a subscriber that is full is dropped.
func (c *core) sendSurface(msgs ...a2ui.Message) {
	for _, m := range msgs {
		c.surf.seq++
		ch := surfaceChange{Seq: c.surf.seq, Message: m}
		for sub := range c.surf.subs {
			select {
			case sub <- ch:
			default:
				delete(c.surf.subs, sub)
				close(sub)
			}
		}
	}
}

// surfaceMeta is Eriphe's metadata of a surface, under the extension
// "eriphe" of createSurface's metadata: what the workspace shows around
// the surface.
type surfaceMeta struct {
	Title   string    `json:"title"`
	Author  string    `json:"author"`
	Weight  int       `json:"weight"`
	Why     string    `json:"why,omitempty"`
	Tainted bool      `json:"tainted,omitempty"`
	Answers bool      `json:"answers,omitempty"` // tainted, its Proposals answer in place all the same: code placed them (thread_surface.go)
	Summary string    `json:"summary,omitempty"` // one line, for a small tile or a chip
	Updated time.Time `json:"updated"`
}

// createMessage is the createSurface of a surface as it is now.
func (c *core) createMessage(s surface) a2ui.Message {
	view := c.surfaceView(s)
	meta, _ := json.Marshal(map[string]any{"extensions": map[string]any{surfaceExtension: surfaceMeta{Title: s.Title,
		Author: s.Author, Weight: s.Weight, Why: s.Why, Tainted: s.Tainted != "", Answers: s.Look.Answers, Summary: s.Look.Summary,
		Updated: s.Updated.UTC()}}})
	data, _ := json.Marshal(view.data)
	return a2ui.Message{Version: a2ui.Version, CreateSurface: &a2ui.CreateSurface{SurfaceID: s.ID, CatalogID: a2ui.Eriphe().ID(),
		Components: view.components, DataModel: data, Metadata: meta}}
}

// rewriteMessages are the messages of a surface written again: its new
// components and data; delete and create again when a component went away
// or what is shown around it changed (A2UI removes no component).
func (c *core) rewriteMessages(old, s surface) []a2ui.Message {
	oldView, view := c.surfaceView(old), c.surfaceView(s)
	ids := func(list []a2ui.Component) []string {
		var out []string
		for _, x := range list {
			out = append(out, x.ID)
		}
		return out
	}
	gone := slices.ContainsFunc(ids(oldView.components), func(id string) bool { return !slices.Contains(ids(view.components), id) })
	if gone || old.Title != s.Title || old.Weight != s.Weight || old.Why != s.Why || (old.Tainted == "") != (s.Tainted == "") ||
		old.Look.Answers != s.Look.Answers || old.Look.Summary != s.Look.Summary {
		return []a2ui.Message{{Version: a2ui.Version, DeleteSurface: &a2ui.DeleteSurface{SurfaceID: s.ID}}, c.createMessage(s)}
	}
	data, _ := json.Marshal(view.data)
	return []a2ui.Message{
		{Version: a2ui.Version, UpdateComponents: &a2ui.UpdateComponents{SurfaceID: s.ID, Components: view.components}},
		{Version: a2ui.Version, UpdateDataModel: &a2ui.UpdateDataModel{SurfaceID: s.ID, Path: "/", Value: data}},
	}
}

// surfaceViewed is a surface as the page gets it: its media addresses
// served by eriphe-core (surfaces_media.go), never loaded from outside.
type surfaceViewed struct {
	components []a2ui.Component
	data       map[string]any
}

// surfaceView rewrites a surface's media addresses. One a source gave goes
// through eriphe-core's media endpoint; one the author wrote too, unless
// the surface is tainted: then a model that read something from outside
// may have written it, to carry what it read out in its address (A2UI
// #4045), and it is dropped. Anything but http(s) is dropped, but for an
// image a task gave for this surface (viewMedia, surfaces_capture.go).
//
// A Link's address goes as it is, never through core: it opens only on
// the user's click, shown in clear. One that is not an address a Link may
// open (a2ui.LinkURLOK; a source may give anything) goes as "": no link.
func (c *core) surfaceView(s surface) surfaceViewed {
	var v surfaceViewed
	var list []map[string]any
	json.Unmarshal(s.Components, &list)
	authorOK := len(s.Look.AuthorTaint) == 0
	for _, comp := range list {
		kind, _ := comp["component"].(string)
		for prop, val := range comp {
			if str, ok := val.(string); ok && a2ui.IsMedia(kind, prop) {
				comp[prop] = c.viewMedia(s.ID, str, authorOK)
			}
			if str, ok := val.(string); ok && a2ui.IsLink(kind, prop) {
				comp[prop] = linkOrDrop(str)
			}
		}
	}
	raw, _ := json.Marshal(list)
	json.Unmarshal(raw, &v.components)
	if v.components == nil {
		v.components = []a2ui.Component{}
	}
	json.Unmarshal(s.Data, &v.data)
	if v.data == nil {
		v.data = map[string]any{}
	}
	for _, b := range a2ui.Eriphe().BoundPaths(s.Components) {
		if b.Link {
			rewriteAt(v.data, tokens(b.Path), linkOrDrop)
		}
		if !b.Media {
			continue
		}
		fromSource, _ := sourceCovering(b.Path, s.Sources)
		ok := fromSource != "" || authorOK
		rewriteAt(v.data, tokens(b.Path), func(u string) string { return c.viewMedia(s.ID, u, ok) })
	}
	return v
}

// linkOrDrop is a Link's address as the page gets it: as it is, or "".
func linkOrDrop(u string) string {
	if a2ui.LinkURLOK(u) {
		return u
	}
	return ""
}

// rewriteAt applies fn to the strings at a path, "*" for each item.
func rewriteAt(v any, toks []string, fn func(string) string) any {
	if len(toks) == 0 {
		if s, ok := v.(string); ok {
			return fn(s)
		}
		return v
	}
	t, rest := toks[0], toks[1:]
	switch x := v.(type) {
	case map[string]any:
		if next, ok := x[t]; ok {
			x[t] = rewriteAt(next, rest, fn)
		}
	case []any:
		if t == "*" {
			for i := range x {
				x[i] = rewriteAt(x[i], rest, fn)
			}
		} else if i, err := strconv.Atoi(t); err == nil && i >= 0 && i < len(x) {
			x[i] = rewriteAt(x[i], rest, fn)
		}
	}
	return v
}

// An author's surface (the brain's, a skill's, a lead's, a task's) weighs
// at most surfaceAuthorMax: being placed first is the attention queue's,
// from the weight of an item about it (surface:<id>, queueSurfaceWeight).

const surfaceAuthorMax = weightUrgent - 1

// authorWeight is the weight an author's surface is kept at: its own,
// capped, raised to its open item's.
func (c *core) authorWeight(id string, w int) int {
	w = min(w, surfaceAuthorMax)
	var item int
	c.store.db.QueryRow(`SELECT COALESCE(MAX(weight), 0) FROM attention WHERE ref = ?`, refSurface+":"+id).Scan(&item)
	if item > 0 {
		w = max(w, queueSurfaceWeight(item))
	}
	return w
}

// surfaceLent raises a surface to the weight of its open item.
func (c *core) surfaceLent(id string) {
	var item int
	c.store.db.QueryRow(`SELECT COALESCE(MAX(weight), 0) FROM attention WHERE ref = ?`, refSurface+":"+id).Scan(&item)
	if item > 0 {
		c.store.db.Exec(`UPDATE surfaces SET weight = MAX(weight, ?) WHERE id = ?`, queueSurfaceWeight(item), id)
	}
}
