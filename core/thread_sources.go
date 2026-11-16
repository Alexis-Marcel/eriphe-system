package main

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// A thread's sources (#571): the live data it carries, read by code
// without a model, under the same rules as a surface's source (sourceWhy:
// a free read of the brain's, or a resource the server lists).
//
//   - A source is a tool (server/tool with fixed arguments), a resource
//     (a server and its URI), or a monitor the user approved
//     (thread_live.go): its latest number and its events.
//   - A tool's or a resource's last good read is kept on the source and
//     placed in the thread's surface's data at /<name>, where the holder's
//     part binds it. A read that fails or waits keeps the last good one:
//     a place is never blanked by a failure.
//   - items {path, id} names the list in what it reads and the id of each
//     item, as a monitor's new does: code shows its items, one per id, in
//     code's section (/thread/sources), never a model.
//   - Who holds the thread adds one with link_thread (source), or with
//     its part of the thread's surface (surface_write with sources: the
//     part's own, replaced with it).
//   - Read at once when added, every few minutes (every, 5 by default), at
//     a resource's update, and when the user opens the thread's surface;
//     only while the thread is active. A read never holds the engine nor a
//     surface write: the surface is written from what was read last.
//   - What a source of a server not trusted gives is data from outside:
//     the surface says « extérieur » and is tainted for the models
//     (codeTaint source:<name>); its cards still answer in place.

const threadSourcesSchema = `CREATE TABLE IF NOT EXISTS thread_sources (
	thread  INTEGER NOT NULL,
	name    TEXT NOT NULL,              -- its place in the surface's data, /<name>; a monitor's: its name
	kind    TEXT NOT NULL,              -- tool, resource or monitor
	spec    TEXT NOT NULL DEFAULT '{}', -- the source as given (threadSource)
	by      TEXT NOT NULL DEFAULT '',   -- who added it: brain, lead:<id>; part: the holder's part's own
	value   TEXT NOT NULL DEFAULT '',   -- its last good read, JSON
	read    INTEGER NOT NULL DEFAULT 0, -- when
	error   TEXT NOT NULL DEFAULT '',   -- why the last read failed
	waiting TEXT NOT NULL DEFAULT '',   -- why the last read was not made (sourceWhy)
	next_at INTEGER NOT NULL DEFAULT 0,
	created INTEGER NOT NULL,
	PRIMARY KEY (thread, name)
);`

func init() { extraSchema = append(extraSchema, threadSourcesSchema) }

const (
	srcTool     = "tool"
	srcResource = "resource"
	srcMonitor  = "monitor"
	byPart      = "part" // a source the holder's part brought

	threadSourcesMax  = surfaceSourcesMax // sources a thread carries, monitors included
	threadItemsMax    = 200               // items shown of one source
	threadItemTextMax = 160               // runes of an item's title or detail
)

var threadSourceNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,39}$`)

// sourceItems names the items of what a source reads: the list (a path,
// empty for the whole) and each item's id (a path in the item), as a
// monitor's new.
type sourceItems struct {
	Path string `json:"path,omitempty" jsonschema:"the list in what it reads, a path as a monitor's (messages, data.items); empty: the whole result is the list"`
	ID   string `json:"id" jsonschema:"each item's id, a path in the item (id, uid, number)"`
	// What its row shows, read by code: paths in the item; without, its
	// first common field (itemTitleFields, itemDetailFields), its id only
	// when it has no words at all.
	Title  string `json:"title,omitempty" jsonschema:"what its row says to the user, a path in the item (subject, title, summary); without, code takes its subject, title or name"`
	Detail string `json:"detail,omitempty" jsonschema:"beside it, quieter: a path in the item (from, author, start); without, code takes its from, sender, author or organizer"`
}

// threadSource is a source as it is given.
type threadSource struct {
	Name     string         `json:"name,omitempty" jsonschema:"its name in the thread, lowercase letters, digits, dashes: its value's place in the surface's data is /<name>; a monitor's: the monitor's name (the default)"`
	Tool     string         `json:"tool,omitempty" jsonschema:"a free read of yours, server/tool, with args"`
	Args     map[string]any `json:"args,omitempty"`
	Resource string         `json:"resource,omitempty" jsonschema:"or a server's resource: the server, with uri"`
	URI      string         `json:"uri,omitempty"`
	Monitor  string         `json:"monitor,omitempty" jsonschema:"or a monitor (monitors) the user approved: its latest number and its events show on the thread"`
	Items    *sourceItems   `json:"items,omitempty" jsonschema:"a tool or a resource that lists things (mails, events, pull requests): where its list is and each item's id; code shows each item"`
	Every    int            `json:"every,omitempty" jsonschema:"a tool or a resource: minutes between two reads, 1 to 1440, 5 by default"`
}

func (s threadSource) kind() string {
	switch {
	case s.Monitor != "":
		return srcMonitor
	case s.Resource != "":
		return srcResource
	}
	return srcTool
}

// surface is a tool's or a resource's source as a surface's: what
// checkSources, readOne and checkBindings take.
func (s threadSource) surface() surfaceSource {
	return surfaceSource{Tool: s.Tool, Args: s.Args, Resource: s.Resource, URI: s.URI}
}

// storedSource is a thread's source as the store keeps it.
type storedSource struct {
	threadSource
	By      string
	Value   any // its last good read, nil if none
	Read    int64
	Error   string
	Waiting string
	NextAt  int64
}

// check checks a source's shape: one kind, a name, its items' paths.
func (s *threadSource) check() error {
	s.Name, s.Monitor = strings.TrimSpace(s.Name), strings.TrimSpace(s.Monitor)
	if s.Name == "" && s.Monitor != "" {
		s.Name = s.Monitor
	}
	n := 0
	for _, set := range []bool{s.Tool != "", s.Resource != "", s.Monitor != ""} {
		if set {
			n++
		}
	}
	switch {
	case !threadSourceNameRe.MatchString(s.Name) || s.Name == threadDataKey:
		return fmt.Errorf("source %q: a name of lowercase letters, digits and dashes, not %q", s.Name, threadDataKey)
	case n != 1:
		return fmt.Errorf("source %s: one of tool (server/tool, with args), resource (server, with uri) or monitor", s.Name)
	case s.Monitor != "" && (s.Name != s.Monitor || s.Items != nil || len(s.Args) > 0 || s.URI != "" || s.Every != 0):
		return fmt.Errorf("source %s: a monitor is named by its own name, and reads by itself", s.Name)
	case s.Every != 0 && (time.Duration(s.Every)*time.Minute < monitorEveryMin || time.Duration(s.Every)*time.Minute > monitorEveryMax):
		return fmt.Errorf("source %s: every is 1 to 1440 minutes", s.Name)
	}
	if s.Items != nil {
		s.Items.Path, s.Items.ID = strings.TrimSpace(s.Items.Path), strings.TrimSpace(s.Items.ID)
		if s.Items.ID == "" {
			return fmt.Errorf("source %s: items needs id, a path in each item", s.Name)
		}
		s.Items.Title, s.Items.Detail = strings.TrimSpace(s.Items.Title), strings.TrimSpace(s.Items.Detail)
		for _, p := range []string{s.Items.Path, s.Items.ID, s.Items.Title, s.Items.Detail} {
			if len(p) > monitorPathMax {
				return fmt.Errorf("source %s: a path of %d characters at most", s.Name, monitorPathMax)
			}
			if _, err := parsePath(p); err != nil {
				return fmt.Errorf("source %s: %w", s.Name, err)
			}
		}
	}
	return nil
}

// addThreadSource makes a thread carry a source, by who holds it; one of
// the same name is replaced. A tool or a resource is read at once, in the
// background.
func (c *core) addThreadSource(id int64, src threadSource, by string) (string, error) {
	if err := src.check(); err != nil {
		return "", err
	}
	threadMu.Lock()
	s, err := c.thread(id)
	if err != nil {
		threadMu.Unlock()
		return "", err
	}
	if !s.active() {
		threadMu.Unlock()
		return "", fmt.Errorf("the thread is %s: reopen it first", s.Look())
	}
	if src.kind() == srcMonitor {
		m, err := c.monitorNamed(src.Monitor)
		switch {
		case err != nil:
			threadMu.Unlock()
			return "", fmt.Errorf("no monitor %q (see monitors)", src.Monitor)
		case m.State == monitorProposed:
			threadMu.Unlock()
			return "", errors.New("the monitor waits for the user's approval on its card: link it once approved")
		}
		var n int
		c.store.db.QueryRow(`SELECT COUNT(*) FROM thread_sources WHERE thread = ? AND kind = ? AND name != ?`, id, srcMonitor, src.Name).Scan(&n)
		if n >= threadMonitorsMax {
			threadMu.Unlock()
			return "", fmt.Errorf("a thread follows %d monitors at most", threadMonitorsMax)
		}
	} else if err := c.checkSources(map[string]surfaceSource{"/" + src.Name: src.surface()}); err != nil {
		threadMu.Unlock()
		return "", err
	}
	var n int
	c.store.db.QueryRow(`SELECT COUNT(*) FROM thread_sources WHERE thread = ? AND name != ?`, id, src.Name).Scan(&n)
	if n >= threadSourcesMax {
		threadMu.Unlock()
		return "", fmt.Errorf("a thread carries %d sources at most", threadSourcesMax)
	}
	c.putThreadSource(id, src, by)
	threadMu.Unlock()
	if src.kind() == srcMonitor {
		c.logThread(id, by, "Surveillance suivie : "+src.Name+".")
		return "Monitor linked: its latest number and its events show on the thread's surface, read by code; a limit crossed raises the thread for the user. A skill still reacts to its events.", nil
	}
	c.logThread(id, by, "Source suivie : "+src.Name+".")
	c.readThreadSourcesLater(id, []string{src.Name})
	note := fmt.Sprintf("Source %s added: code reads it now and every %d minutes, without a model; its value is at /%s in the thread's surface's data, for your part to bind.", src.Name, cmp.Or(src.Every, surfaceEveryDef), src.Name)
	if src.Items != nil {
		note += " Its items show in code's section, one per id."
	}
	return note, nil
}

// putThreadSource writes a source, its last read kept when it is the same.
func (c *core) putThreadSource(id int64, src threadSource, by string) {
	raw, _ := json.Marshal(src)
	now := time.Now().Unix()
	c.store.db.Exec(`INSERT INTO thread_sources (thread, name, kind, spec, by, created) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (thread, name) DO UPDATE SET kind = excluded.kind, by = excluded.by,
		  value = CASE WHEN spec = excluded.spec THEN value ELSE '' END,
		  read = CASE WHEN spec = excluded.spec THEN read ELSE 0 END,
		  next_at = CASE WHEN spec = excluded.spec THEN next_at ELSE 0 END,
		  spec = excluded.spec`, id, src.Name, src.kind(), string(raw), by, now)
	c.threadMoved(id)
}

// dropThreadSource takes a source away from a thread.
func (c *core) dropThreadSource(id int64, name, by string) (string, error) {
	res, err := c.store.db.Exec(`DELETE FROM thread_sources WHERE thread = ? AND name = ?`, id, name)
	if err != nil {
		return "", err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return "", fmt.Errorf("the thread has no source %q", name)
	}
	c.sourceNotesGone(id, name, time.Now()) // thread_items.go
	c.logThread(id, by, "Source retirée : "+name+".")
	c.kickWorkspace()
	return "Source removed.", nil
}

// threadSources are a thread's sources, oldest first.
func (c *core) threadSources(thread int64) []storedSource {
	rows, err := c.store.db.Query(`SELECT spec, by, value, read, error, waiting, next_at FROM thread_sources WHERE thread = ? ORDER BY created, name`, thread)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []storedSource
	for rows.Next() {
		var spec, value string
		var s storedSource
		if rows.Scan(&spec, &s.By, &value, &s.Read, &s.Error, &s.Waiting, &s.NextAt) != nil || json.Unmarshal([]byte(spec), &s.threadSource) != nil {
			continue
		}
		if value != "" {
			json.Unmarshal([]byte(value), &s.Value)
		}
		out = append(out, s)
	}
	return out
}

// threadReadAgain is a thread whose sources are being read (core's
// threadReads): one read of a thread at a time; one asked meanwhile reads
// all of them after.
type threadReadAgain struct {
	mu   sync.Mutex
	more bool
}

// readThreadSourcesLater reads a thread's sources (those named in only, if
// given) in the background.
func (c *core) readThreadSourcesLater(thread int64, only []string) {
	c.surf.refreshing.Add(1)
	go func() {
		defer c.surf.refreshing.Done()
		c.readThreadSources(context.Background(), thread, only, time.Now())
	}()
}

// readThreadSources reads a thread's tool and resource sources (those in
// only, if given), keeps what each gave, and writes its surface again.
// Never under threadMu: a slow tool holds nothing.
func (c *core) readThreadSources(ctx context.Context, thread int64, only []string, now time.Time) {
	again := &threadReadAgain{}
	if v, busy := c.threadReads.LoadOrStore(thread, again); busy {
		a := v.(*threadReadAgain)
		a.mu.Lock()
		a.more = true
		a.mu.Unlock()
		return
	}
	defer c.threadReads.Delete(thread)
	for {
		c.readThreadSourcesOnce(ctx, thread, only, now)
		again.mu.Lock()
		more := again.more
		again.more = false
		again.mu.Unlock()
		if !more {
			return
		}
		only, now = nil, time.Now()
	}
}

func (c *core) readThreadSourcesOnce(ctx context.Context, thread int64, only []string, now time.Time) {
	s, err := c.thread(thread)
	if err != nil || !s.active() {
		return
	}
	want := map[string]threadSource{}
	srcs := map[string]surfaceSource{}
	for _, src := range c.threadSources(thread) {
		if src.kind() == srcMonitor || (only != nil && !slices.Contains(only, src.Name)) {
			continue
		}
		want[src.Name], srcs[src.Name] = src.threadSource, src.surface()
	}
	if len(srcs) == 0 {
		return
	}
	c.keepSourceReads(thread, want, c.readSources(ctx, srcs, nil), now)
}

// keepSourceReads keeps what a thread's sources gave, by name: a good read
// replaces its value, a failed or refused one keeps it and says why; then
// the thread's surface is written again.
func (c *core) keepSourceReads(thread int64, srcs map[string]threadSource, reads map[string]sourceRead, now time.Time) {
	for name, r := range reads {
		src, ok := srcs[name]
		if !ok {
			continue
		}
		spec, _ := json.Marshal(src)
		next := now.Add(time.Duration(cmp.Or(src.Every, surfaceEveryDef)) * time.Minute).Unix()
		var err error
		if r.err == "" && r.why == "" {
			raw, _ := json.Marshal(r.value)
			// Written again meanwhile as another source: this read is not its.
			var res sql.Result
			res, err = c.store.db.Exec(`UPDATE thread_sources SET value = ?, read = ?, error = '', waiting = '', next_at = ? WHERE thread = ? AND name = ? AND spec = ?`,
				string(raw), now.Unix(), next, thread, name, string(spec))
			if err == nil && src.Items != nil {
				if n, _ := res.RowsAffected(); n > 0 {
					c.itemsListed(thread, storedSource{threadSource: src, Value: r.value}, now) // thread_items.go
				}
			}
		} else {
			_, err = c.store.db.Exec(`UPDATE thread_sources SET error = ?, waiting = ?, next_at = ? WHERE thread = ? AND name = ? AND spec = ?`,
				r.err, r.why, next, thread, name, string(spec))
		}
		if err != nil {
			log.Printf("thread %d: source %s: %v", thread, name, err)
		}
	}
	c.keepThreadSurface(thread)
	c.kickWorkspace()
}

// partSources are the sources of the holder's part of a thread's surface,
// as the thread's: each at /<name>, read every refresh minutes.
func (c *core) partSources(thread int64, in surfaceWriteIn) (map[string]threadSource, error) {
	if len(in.Sources) == 0 {
		if in.Refresh.Every != 0 {
			return nil, errors.New("refresh every is for a part with sources")
		}
		return nil, nil
	}
	out := map[string]threadSource{}
	for ptr, s := range in.Sources {
		name, ok := strings.CutPrefix(ptr, "/")
		if !ok || strings.Contains(name, "/") {
			return nil, fmt.Errorf("source %q: on a thread's surface, a source is at /<name>, a name of lowercase letters, digits and dashes", ptr)
		}
		if slices.ContainsFunc(c.threadSources(thread), func(x storedSource) bool { return x.Name == name && x.By != byPart }) {
			return nil, fmt.Errorf("source %s is the thread's already (link_thread): bind /%s without giving it again", ptr, name)
		}
		src := threadSource{Name: name, Tool: s.Tool, Args: s.Args, Resource: s.Resource, URI: s.URI, Every: in.Refresh.Every}
		if err := src.check(); err != nil {
			return nil, err
		}
		if src.kind() == srcMonitor {
			return nil, fmt.Errorf("source %s: a tool or a resource", ptr)
		}
		out[name] = src
	}
	if err := c.checkSources(in.Sources); err != nil {
		return nil, err
	}
	n := len(out)
	for _, src := range c.threadSources(thread) {
		if _, again := out[src.Name]; !again && src.By != byPart {
			n++
		}
	}
	if n > threadSourcesMax {
		return nil, fmt.Errorf("a thread carries %d sources at most", threadSourcesMax)
	}
	return out, nil
}

// setPartSources makes the part's sources the thread's, in place of the
// part's before, with what their first read gave; those still reading
// are kept once they are read.
func (c *core) setPartSources(thread int64, srcs map[string]threadSource, reads map[string]sourceRead, late *lateReads) {
	rows, err := c.store.db.Query(`SELECT name FROM thread_sources WHERE thread = ? AND by = ?`, thread, byPart)
	if err == nil {
		var gone []string
		for rows.Next() {
			var name string
			if rows.Scan(&name) == nil {
				if _, kept := srcs[name]; !kept {
					gone = append(gone, name)
				}
			}
		}
		rows.Close()
		for _, name := range gone {
			c.store.db.Exec(`DELETE FROM thread_sources WHERE thread = ? AND name = ?`, thread, name)
			c.sourceNotesGone(thread, name, time.Now())
		}
	}
	if len(srcs) == 0 {
		return
	}
	for _, src := range srcs {
		c.putThreadSource(thread, src, byPart)
	}
	got := map[string]sourceRead{}
	for ptr, r := range reads {
		if r.why != surfaceReading {
			got[strings.TrimPrefix(ptr, "/")] = r
		}
	}
	c.keepSourceReads(thread, srcs, got, time.Now())
	if late == nil {
		return
	}
	c.surf.refreshing.Add(1)
	go func() {
		defer c.surf.refreshing.Done()
		all := <-late.done
		got := map[string]sourceRead{}
		for _, ptr := range late.pending {
			got[strings.TrimPrefix(ptr, "/")] = all[ptr]
		}
		c.keepSourceReads(thread, srcs, got, time.Now())
	}()
}

// threadSourcesDue reads the sources whose time came, of the active
// threads: the engine's tick, never waiting for a read.
func (c *core) threadSourcesDue(now time.Time) {
	rows, err := c.store.db.Query(`SELECT DISTINCT s.thread FROM thread_sources s JOIN threads t ON t.id = s.thread
		WHERE s.kind != ? AND s.next_at <= ? AND t.`+activeThreads, srcMonitor, now.Unix())
	if err != nil {
		return
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	for _, id := range ids {
		if _, busy := c.threadReads.Load(id); !busy {
			c.readThreadSourcesLater(id, nil)
		}
	}
}

// threadResourceUpdated is the pool's hook: a server said a resource
// changed; the active threads that read it read it again.
func (c *core) threadResourceUpdated(server, uri string) {
	byThread := map[int64][]string{}
	for _, r := range c.threadResources() {
		if r.server == server && r.uri == uri {
			byThread[r.thread] = append(byThread[r.thread], r.name)
		}
	}
	for id, names := range byThread {
		c.readThreadSourcesLater(id, names)
	}
}

type threadResource struct {
	thread            int64
	name, server, uri string
}

// threadResources are the resources the active threads read.
func (c *core) threadResources() []threadResource {
	rows, err := c.store.db.Query(`SELECT s.thread, s.name, s.spec FROM thread_sources s JOIN threads t ON t.id = s.thread
		WHERE s.kind = ? AND t.`+activeThreads, srcResource)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []threadResource
	for rows.Next() {
		var r threadResource
		var spec string
		var src threadSource
		if rows.Scan(&r.thread, &r.name, &spec) == nil && json.Unmarshal([]byte(spec), &src) == nil {
			r.server, r.uri = src.Resource, src.URI
			out = append(out, r)
		}
	}
	return out
}

// sourceUnread tells whether a thread has a tool or a resource source that
// gave nothing yet: what binds it waits for it.
func (c *core) sourceUnread(thread int64) bool {
	var n int
	c.store.db.QueryRow(`SELECT COUNT(*) FROM thread_sources WHERE thread = ? AND kind != ? AND value = ''`, thread, srcMonitor).Scan(&n)
	return n > 0
}

// threadOpened reads a thread's sources again because the user opened its
// surface, unless they were read moments ago.
func (c *core) threadOpened(ctx context.Context, thread int64) {
	var last int64
	var n int
	c.store.db.QueryRow(`SELECT COUNT(*), COALESCE(MIN(read), 0) FROM thread_sources WHERE thread = ? AND kind != ?`, thread, srcMonitor).Scan(&n, &last)
	if n == 0 || time.Since(time.Unix(last, 0)) < surfaceOpenFresh {
		return
	}
	c.readThreadSources(ctx, thread, nil, time.Now())
}

// threadSourceItems are the items of what a source read: each with its id,
// in the order the source gives them, as many as most (0: all of them).
func threadSourceItems(src storedSource, most int) []map[string]any {
	if src.Items == nil || src.Value == nil {
		return nil
	}
	list, err := extract(src.Value, src.Items.Path)
	items, ok := list.([]any)
	if err != nil || !ok {
		return nil
	}
	var out []map[string]any
	seen := map[string]bool{}
	for i, it := range items {
		v, err := extract(it, src.Items.ID)
		if err != nil || v == nil {
			continue
		}
		id := truncate(valueText(v), 200)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		row := map[string]any{"source": src.Name, "id": id, "n": i}
		title, titleKey := itemField(it, src.Items.Title, itemTitleFields, false)
		detail, detailKey := itemField(it, src.Items.Detail, itemDetailFields, true)
		if title == "" {
			title, titleKey = itemAnyText(it, id, src.Items.ID, detailKey)
		}
		switch {
		case title != "":
			row["title"] = title
		case detail != "":
			row["title"], detail = detail, "" // its only words
		default:
			row["title"] = id // nothing else says what it is
		}
		if detail != "" && detailKey != titleKey {
			row["detail"] = detail
		}
		out = append(out, row)
		if len(out) == most {
			break
		}
	}
	return out
}

// What an item's row says when its source names no path for it: the first
// of these fields it has, at its top level, whatever the source (a mail's
// subject and sender, an event's title and organizer, an issue's title and
// author). Never its id while some words say what it is.
var (
	itemTitleFields  = []string{"subject", "title", "name", "summary", "label", "snippet", "text", "description"}
	itemDetailFields = []string{"from", "sender", "author", "organizer", "owner", "creator", "user", "assignee"}
)

// itemField is the text at path in an item, else at the first of fields
// it has; and the field it came from ("" for a path given).
func itemField(it any, path string, fields []string, person bool) (string, string) {
	if path != "" {
		v, err := extract(it, path)
		if err != nil {
			return "", ""
		}
		return itemText(v, person), path
	}
	m, ok := it.(map[string]any)
	if !ok {
		return "", ""
	}
	for _, k := range fields {
		if text := itemText(m[k], person); text != "" {
			return text, k
		}
	}
	return "", ""
}

// itemAnyText is, for an item none of the common fields names, the first
// short text it has at its top level that is not its id: a row says words
// before an id.
func itemAnyText(it any, id, idPath, skip string) (string, string) {
	m, ok := it.(map[string]any)
	if !ok {
		return "", ""
	}
	for _, k := range slices.Sorted(maps.Keys(m)) {
		lk := strings.ToLower(k)
		if k == idPath || k == skip || lk == "id" || strings.HasSuffix(lk, "_id") || strings.HasSuffix(k, "Id") || strings.HasSuffix(lk, "uid") {
			continue
		}
		s, ok := m[k].(string)
		if !ok || s == id || strings.Contains(s, "://") {
			continue
		}
		if text := itemText(s, false); text != "" && utf8.RuneCountInString(s) <= threadItemTextMax {
			return text, k
		}
	}
	return "", ""
}

// itemText is a value as a row says it, one line: an object by its name;
// for a person (a detail), "Sam <sam@example.org>" by their name too.
func itemText(v any, person bool) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		s := strings.TrimSpace(x)
		if name, _, ok := strings.Cut(s, " <"); ok && person && strings.HasSuffix(s, ">") {
			if name = strings.Trim(strings.TrimSpace(name), `"`); name != "" {
				s = name
			}
		}
		return truncate(oneLine(s), threadItemTextMax)
	case map[string]any:
		for _, k := range []string{"name", "displayName", "display_name", "login", "email", "title"} {
			if s, ok := x[k].(string); ok && strings.TrimSpace(s) != "" {
				return itemText(s, person)
			}
		}
		return ""
	case []any:
		var parts []string
		for _, e := range x {
			if s := itemText(e, person); s != "" {
				parts = append(parts, s)
			}
		}
		return truncate(strings.Join(parts, ", "), threadItemTextMax)
	}
	return truncate(oneLine(valueText(v)), threadItemTextMax)
}

// threadItemsSeen is what a thread's items say, for its state line: how
// many of each state, and when they were last sorted.
type threadItemsSeen struct {
	counts map[string]int // by state, "" for not sorted yet
	sorted int64          // the last note on an item still listed, unix
}

// threadSourcesPart composes what a thread's tool and resource sources
// show in code's section (/thread/sources): the items of those that list
// some, grouped by their notes' state; a source's name only when the
// thread lists the items of several, a word only when it cannot be read.
// Their last reads, by name, are for the surface's data at /<name>. A
// source of a server not trusted taints the surface: its state line says
// « extérieur », once (threadStateText).
func (c *core) threadSourcesPart(in *surfaceIn, out *threadComps, data map[string]any, s Thread, cards []itemCard, seen *threadItemsSeen) map[string]any {
	lang, loc := c.userLang(), c.userZone()
	type comp = map[string]any
	values := map[string]any{}
	notes := map[string]itemNote{}
	for _, n := range c.itemNotes(s.ID) {
		if n.Gone == 0 {
			notes[n.Source+"\x00"+n.Item] = n
		}
	}
	srcs := slices.DeleteFunc(c.threadSources(s.ID), func(x storedSource) bool { return x.kind() == srcMonitor })
	lists := 0
	for _, src := range srcs {
		if src.Items != nil {
			lists++
		}
	}
	var shown []any
	for _, src := range srcs {
		if src.Value != nil {
			values[src.Name] = src.Value // ended too: the holder's part still binds it
		}
		if !s.active() {
			continue // no longer read: code's section shows none
		}
		if !c.trustsServer(src.surface().spec().Server) {
			in.codeTaint = append(in.codeTaint, "source:"+src.Name)
		}
		var parts []string
		if lists > 1 && src.Items != nil {
			parts = append(parts, src.Name)
		}
		problem := ""
		switch {
		case src.Waiting != "":
			problem = tr(lang, "thread.source.waiting")
		case src.Error != "":
			problem = tr(lang, "thread.live.failing")
		}
		if problem != "" {
			if len(parts) == 0 {
				parts = append(parts, src.Name)
			}
			parts = append(parts, problem)
		}
		if len(parts) == 0 && src.Items == nil {
			continue // a value for the holder's part: nothing to say of it
		}
		n := strconv.Itoa(len(shown))
		v := map[string]any{"name": src.Name}
		if len(parts) > 0 {
			v["caption"] = strings.Join(parts, threadExternalSep)
			out.add(comp{"id": "thread_src_" + n + "_by", "component": "Text", "variant": "caption", "text": map[string]any{"@path": "/thread/sources/" + n + "/caption"}})
		}
		if src.Items != nil {
			c.itemsBlock(out, v, s.ID, src, notes, cards, n, lang, loc, &in.codeTaint, seen)
		}
		shown = append(shown, v)
	}
	if len(shown) > 0 {
		data["sources"] = shown
	}
	return values
}
