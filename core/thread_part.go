package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/Alexis-Marcel/eriphe-system/core/a2ui"
)

// The holder's part of a thread's surface (#610): named blocks. Who holds
// a thread (the brain, or its lead: mayAddPart) composes its part, shown
// under code's section, block by block:
//
//   - surface_write with block adds that block, or replaces it where it
//     stands; surface_remove with block takes it away. Without a block
//     nothing is written or removed: there is no whole part.
//   - Blocks keep the order they were first written in; before (another
//     block's name) places the block written before that one, new or not.
//   - Each block is checked alone: its components (one has id root,
//     renamed thread_part_<block>), its data and its sources, its bindings
//     against its own data and sources, the thread's own sources
//     (link_thread) and code's data (/thread). Its component ids and its
//     data keys are its own: one another block holds is refused, naming
//     that block.
//   - Its sources become the thread's (thread_sources.go): read by code
//     at /<name>. Two blocks may give the same source, alike; a source no
//     block gives any more goes.
//   - Taint, per block: a block written after reading external content,
//     or that binds a source of a server not trusted, says « extérieur »
//     beside it (partBlock.external), and the surface is tainted for the
//     models while a tainted block is there.
//   - While it has no block, the thread shows its default composition
//     instead (thread_default.go): a block written takes its place.
//
// The blocks are stored in order as one value (threadPartKey + the
// thread's id). What the user marks on what a block shows is code's, in
// the thread's data, never in a block (thread_marks.go).

const threadBlocksMax = 16 // blocks of a part

var partBlockRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_]{0,31}$`)

func init() {
	// A part written whole, before blocks, is dropped with the sources it
	// gave: its holder writes it again, by blocks.
	schemaFixes = append(schemaFixes, func(db *sql.DB) error {
		n := strconv.Itoa(len(threadPartKey))
		whole := `substr(key, 1, ` + n + `) = ? AND (CASE WHEN json_valid(value) THEN json_type(value, '$.blocks') END) IS NULL`
		if _, err := db.Exec(`DELETE FROM thread_sources WHERE by = ? AND thread IN
			(SELECT CAST(substr(key, `+n+` + 1) AS INTEGER) FROM kv WHERE `+whole+`)`, byPart, threadPartKey); err != nil {
			return err
		}
		_, err := db.Exec(`DELETE FROM kv WHERE `+whole, threadPartKey)
		return err
	})
}

// threadPart is the part the thread's holder added to its surface: its
// blocks, in order.
type threadPart struct {
	Blocks []partBlock `json:"blocks"`
}

// partBlock is one named block of a part.
type partBlock struct {
	Name       string                  `json:"name"`
	Author     string                  `json:"author"`
	Components json.RawMessage         `json:"components"` // its root renamed partRoot(name)
	Data       json.RawMessage         `json:"data,omitempty"`
	Sources    map[string]threadSource `json:"sources,omitempty"` // the thread's sources it gives, by name
	Tainted    []string                `json:"tainted,omitempty"`
}

// partRoot is the id a block's root takes on the thread's surface: code's
// prefix, so that no author's id meets it.
func partRoot(block string) string { return threadPartRoot + "_" + block }

// threadPartMu makes the changes of the parts one at a time: the brain
// and a lead may write blocks of one part at the same moment.
var threadPartMu sync.Mutex

// threadPartOf is a thread's part; nil when it has no block.
func (c *core) threadPartOf(thread int64) *threadPart {
	raw := c.store.get(threadPartKey + strconv.FormatInt(thread, 10))
	if raw == "" {
		return nil
	}
	var p threadPart
	if json.Unmarshal([]byte(raw), &p) != nil || len(p.Blocks) == 0 {
		return nil
	}
	return &p
}

// savePart keeps a thread's part; one with no block is gone.
func (c *core) savePart(thread int64, p *threadPart) {
	key := threadPartKey + strconv.FormatInt(thread, 10)
	if p == nil || len(p.Blocks) == 0 {
		c.store.db.Exec(`DELETE FROM kv WHERE key = ?`, key)
		return
	}
	raw, _ := json.Marshal(p)
	c.store.set(key, string(raw))
}

// names are the part's blocks' names, in order.
func (p *threadPart) names() []string {
	if p == nil {
		return nil
	}
	out := make([]string, 0, len(p.Blocks))
	for _, b := range p.Blocks {
		out = append(out, b.Name)
	}
	return out
}

// index is where a block is in the part; -1 when it has none so named.
func (p *threadPart) index(name string) int {
	if p == nil {
		return -1
	}
	return slices.IndexFunc(p.Blocks, func(b partBlock) bool { return b.Name == name })
}

// sourceNames are the names of the sources its blocks give, but one's.
func (p *threadPart) sourceNames(except string) map[string]bool {
	out := map[string]bool{}
	if p == nil {
		return out
	}
	for _, b := range p.Blocks {
		if b.Name != except {
			for name := range b.Sources {
				out[name] = true
			}
		}
	}
	return out
}

// external tells whether a block may show someone else's words: written
// after reading external content, or binding a source of a server not
// trusted (untrusted, by name), at /<name> or /thread/items/<name>. The
// surface then says « extérieur » beside it, and only there.
func (b partBlock) external(untrusted map[string]bool) bool {
	if len(b.Tainted) > 0 {
		return true
	}
	for name := range b.Sources {
		if untrusted[name] {
			return true
		}
	}
	for _, bound := range a2ui.Eriphe().BoundPaths(b.Components) {
		at := tokens(bound.Path)
		if len(at) > 2 && at[0] == threadDataKey && at[1] == "items" {
			at = at[2:]
		}
		if len(at) > 0 && untrusted[at[0]] {
			return true
		}
	}
	return false
}

// tainted is why its blocks may carry someone else's words.
func (p *threadPart) tainted() []string {
	var out []string
	for _, b := range p.Blocks {
		out = append(out, b.Tainted...)
	}
	return slices.Compact(slices.Sorted(slices.Values(out)))
}

// clash is why a block may not stand beside the part's others: a
// component id or a data key another block holds, a data key that is a
// source's place, or a source another block gives otherwise. ids and keys
// are the block's own.
func (p *threadPart) clash(b partBlock, ids, keys []string) error {
	if p == nil {
		return nil
	}
	for _, o := range p.Blocks {
		if o.Name == b.Name {
			continue
		}
		var list []map[string]any
		json.Unmarshal(o.Components, &list)
		for _, comp := range list {
			if id, _ := comp["id"].(string); slices.Contains(ids, id) {
				return fmt.Errorf("the component id %s is block %s's already: name it otherwise", id, o.Name)
			}
		}
		var data map[string]any
		json.Unmarshal(o.Data, &data)
		for _, k := range keys {
			if _, ok := data[k]; ok {
				return fmt.Errorf("data /%s is block %s's already: write your values under another key, or bind it from that block", k, o.Name)
			}
			if _, ok := o.Sources[k]; ok {
				return fmt.Errorf("data /%s is the place of block %s's source: write your values under another key", k, o.Name)
			}
		}
		for name, src := range b.Sources {
			if _, ok := data[name]; ok {
				return fmt.Errorf("source /%s: /%s is block %s's data already: name the source otherwise", name, name, o.Name)
			}
			if other, ok := o.Sources[name]; ok && !sameSource(src, other) {
				return fmt.Errorf("source /%s is block %s's, read otherwise: give it alike (same tool, arguments and refresh), or name it otherwise", name, o.Name)
			}
		}
	}
	return nil
}

// sameSource tells whether two sources read the same thing alike.
func sameSource(a, b threadSource) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}

// place puts a block in the part: where it stands when it has one so
// named, else last; before another block when before names one.
func (p *threadPart) place(b partBlock, before string) error {
	i := p.index(b.Name)
	if before == "" {
		if i >= 0 {
			p.Blocks[i] = b
		} else {
			p.Blocks = append(p.Blocks, b)
		}
		return nil
	}
	if p.index(before) < 0 {
		return fmt.Errorf("before: no block %s in the part (its blocks: %s)", before, blockList(p))
	}
	if i >= 0 {
		p.Blocks = slices.Delete(p.Blocks, i, i+1)
	}
	p.Blocks = slices.Insert(p.Blocks, p.index(before), b)
	return nil
}

// blocksAt are the blocks errors of the thread's whole surface point at
// (at: each component's block by its place, threadSurfaceIn); all of them
// when one error points at none.
func (p *threadPart) blocksAt(at map[int]string, errs []a2ui.ValidationError) []string {
	var out []string
	for _, e := range errs {
		rest, ok := strings.CutPrefix(e.Path, "/components/")
		n, err := strconv.Atoi(strings.SplitN(rest, "/", 2)[0])
		name := at[n]
		if !ok || err != nil || name == "" {
			return p.names()
		}
		if !slices.Contains(out, name) {
			out = append(out, name)
		}
	}
	if len(out) == 0 {
		return p.names()
	}
	return out
}

// without is the part but some blocks; nil when none is left.
func (p *threadPart) without(names []string) *threadPart {
	kept := &threadPart{}
	for _, b := range p.Blocks {
		if !slices.Contains(names, b.Name) {
			kept.Blocks = append(kept.Blocks, b)
		}
	}
	if len(kept.Blocks) == 0 {
		return nil
	}
	return kept
}

// blockList names a part's blocks in an error.
func blockList(p *threadPart) string {
	if names := p.names(); len(names) > 0 {
		return strings.Join(names, ", ")
	}
	return "none yet"
}

// mayAddPart tells whether an author holds a thread, and so may compose
// its part of the thread's surface: the brain, or its own lead.
func mayAddPart(author string, thread int64) bool {
	return author == surfaceAuthorBrain || author == "lead:"+strconv.FormatInt(thread, 10)
}

// writeThreadPart is surface_write on a thread's surface: one block of
// the holder's part, checked alone, put in its place, then the surface
// written again with it.
func (c *core) writeThreadPart(who surfaceCaller, thread int64, in surfaceWriteIn) (map[string]any, error) {
	sid := threadSurfaceID(thread)
	if !mayAddPart(who.author, thread) {
		return nil, fmt.Errorf("the surface %s is the thread's, kept by code: only who holds the thread composes a part of it", sid)
	}
	s, err := c.thread(thread)
	if err != nil {
		return nil, err
	}
	if !s.active() {
		return nil, fmt.Errorf("the thread is %s: its surface takes no new block", s.State)
	}
	part := c.threadPartOf(thread)
	name, before := strings.TrimSpace(in.Block), strings.TrimSpace(in.Before)
	switch {
	case !partBlockRe.MatchString(name):
		return nil, fmt.Errorf("block is needed: the name of the block of your part you write (lowercase letters, digits and _, 32 at most); a new name adds a block, the name of one there replaces it alone (its blocks: %s)", blockList(part))
	case before == name:
		return nil, errors.New("before names another block: the one to place this one before")
	case in.Components == nil:
		return nil, errors.New("components are needed: a flat list, one with id root")
	case part.index(name) < 0 && len(part.names()) >= threadBlocksMax:
		return nil, fmt.Errorf("a part has %d blocks at most: replace one, or remove one (its blocks: %s)", threadBlocksMax, blockList(part))
	}
	srcs, err := c.partSources(thread, in, part.sourceNames(name))
	if err != nil {
		return nil, err
	}
	tainted := who.taint()
	var errs []a2ui.ValidationError
	var ids []string
	fail := func(i int, msg string) {
		errs = append(errs, a2ui.ValidationError{Code: a2ui.CodeValidationFailed, SurfaceID: sid, Path: "/components/" + strconv.Itoa(i), Message: msg})
	}
	for i, comp := range in.Components {
		id, _ := comp["id"].(string)
		ids = append(ids, id)
		if strings.HasPrefix(id, threadCompPrefix) {
			fail(i, "ids starting with "+threadCompPrefix+" are code's on a thread's surface: name it otherwise")
		}
		if comp["component"] == "Proposal" && len(tainted) > 0 {
			fail(i, "a Proposal placed after reading external content could be answered as something else: the thread's cards are shown by code already")
		}
	}
	if _, ok := in.Data[threadDataKey]; ok {
		return nil, fmt.Errorf("data /%s is code's on a thread's surface: write your values under another key", threadDataKey)
	}
	keys := slices.Sorted(maps.Keys(in.Data))
	for _, k := range keys {
		if slices.ContainsFunc(c.threadSources(thread), func(x storedSource) bool { return x.Name == k && x.By != byPart }) {
			return nil, fmt.Errorf("data /%s is the place of the thread's source %s: write your values under another key", k, k)
		}
	}
	block := partBlock{Name: name, Author: who.author, Sources: srcs, Tainted: slices.Compact(slices.Sorted(slices.Values(tainted)))}
	if err := part.clash(block, ids, keys); err != nil {
		return nil, err
	}
	comps, _ := json.Marshal(in.Components)
	data, _ := json.Marshal(orEmptyMap(in.Data))
	total, totalData := len(comps), len(data)
	if part != nil {
		for _, b := range part.Blocks {
			if b.Name != name {
				total, totalData = total+len(b.Components), totalData+len(b.Data)
			}
		}
	}
	if total > threadPartMax || totalData > threadPartMax {
		return nil, fmt.Errorf("a part's blocks hold %d KiB of components and of data at most, all together", threadPartMax>>10)
	}
	if len(errs) == 0 {
		errs = a2ui.ValidateSurface(a2ui.Surface{ID: sid, Components: comps, DataModel: data})
	}
	if len(errs) == 0 {
		errs = actionGuard(sid, comps, len(tainted) > 0)
	}
	if len(errs) == 0 {
		errs = a2ui.CheckLinks(sid, comps)
	}
	if len(errs) == 0 {
		errs = offersGuard(sid, comps)
	}
	// Its sources, read once to check what it binds (held surfaceReadCap at
	// most: those still reading are checked against their schema).
	reads, late := c.readSourcesWithin(in.Sources, surfaceReadCap)
	if len(errs) == 0 {
		merged, err := mergeSources(orEmptyMap(in.Data), in.Sources, reads)
		if err != nil {
			return nil, err
		}
		// The thread's own sources (link_thread) and code's data are there
		// to bind too (/thread/children, /thread/steps, /thread/marks):
		// checked against what they hold now, never kept in the block.
		for _, src := range c.threadSources(thread) {
			if _, given := merged[src.Name]; !given && src.By != byPart && src.Value != nil {
				merged[src.Name] = src.Value
			}
		}
		merged[threadDataKey] = c.threadCodeData(sid)
		errs = c.checkBindings(sid, comps, merged, in.Sources, reads)
	}
	if len(errs) > 0 {
		return nil, invalidSurface(errs)
	}
	for _, comp := range in.Components {
		if comp["id"] == "root" {
			comp["id"] = partRoot(name)
		}
	}
	block.Components, _ = json.Marshal(in.Components)
	block.Data = data

	threadPartMu.Lock()
	part = c.threadPartOf(thread) // again: another holder may have written one meanwhile
	if part == nil {
		part = &threadPart{}
	}
	err = part.clash(block, ids, keys)
	if err == nil {
		err = part.place(block, before)
	}
	if err == nil {
		// Its sources first, with what their first read gave: the block is
		// never written bound to a value not there yet.
		c.setPartSources(thread, srcs, part.sourceNames(name), reads, late)
		c.savePart(thread, part)
	}
	threadPartMu.Unlock()
	if err != nil {
		return nil, err
	}
	c.keepThreadSurface(thread)
	note := "Your block is on the thread's surface, under what code keeps there (its head, what waits for the user, its children), among your part's blocks in that order, in place of code's default from its data: compose what the user should see of its steps and items too (/thread/steps, /thread/items/<source>, an Offers for the actions offered on its rows). Writing a block again replaces it alone, where it stands (before moves it); surface_remove with its name takes it away. Its title, weight, why and lifetime are code's."
	if len(srcs) > 0 {
		note += " Its sources are the thread's: code reads them again, without a model, while the thread is active."
	}
	return map[string]any{"id": sid, "author": threadAuthor(thread), "block": name, "blocks": part.names(), "note": note}, nil
}

// threadCodeData is code's data on a thread's surface as it shows now,
// /thread: an empty object when it has none yet.
func (c *core) threadCodeData(sid string) map[string]any {
	var data map[string]any
	if sf, err := c.surface(sid); err == nil {
		var all map[string]json.RawMessage
		json.Unmarshal(sf.Data, &all)
		json.Unmarshal(all[threadDataKey], &data)
	}
	return orEmptyMap(data)
}

// removeThreadPart is surface_remove on a thread's surface: one block of
// the holder's part goes, with the sources only it gave; the surface
// stays.
func (c *core) removeThreadPart(who surfaceCaller, thread int64, block string) (map[string]any, error) {
	if !mayAddPart(who.author, thread) {
		return nil, fmt.Errorf("the surface %s is the thread's, kept by code while it lives", threadSurfaceID(thread))
	}
	name := strings.TrimSpace(block)
	threadPartMu.Lock()
	part := c.threadPartOf(thread)
	i := part.index(name)
	if i < 0 {
		threadPartMu.Unlock()
		if name == "" {
			return nil, fmt.Errorf("block is needed: the name of the block of your part to take away (its blocks: %s)", blockList(part))
		}
		return nil, fmt.Errorf("no block %s in the part (its blocks: %s)", name, blockList(part))
	}
	part.Blocks = slices.Delete(part.Blocks, i, i+1)
	c.setPartSources(thread, nil, part.sourceNames(""), nil, nil)
	c.savePart(thread, part)
	threadPartMu.Unlock()
	c.keepThreadSurface(thread)
	return map[string]any{"removed": name, "blocks": part.names(), "note": "That block is gone; your other blocks stay, and the thread's surface stays while the thread does."}, nil
}

// dropThreadPart takes a thread's whole part away, with the sources its
// blocks gave: code's, when what it showed is over (a day's thread).
func (c *core) dropThreadPart(thread int64) {
	threadPartMu.Lock()
	defer threadPartMu.Unlock()
	c.savePart(thread, nil)
	c.setPartSources(thread, nil, nil, nil, nil)
}

func orEmptyMap(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}
