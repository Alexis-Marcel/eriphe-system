package main

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

// Notes on a thread's items (#571): what the brain or a thread's lead
// writes about one item a source of the thread lists (a mail, an event, a
// pull request), by the source's name and the item's id: a short note, a
// state, a due date (note_item).
//
//   - Code joins them to the items when it composes the thread's surface:
//     each item's row carries its note (thread_sources.go).
//   - Its author and its taint are code's, never given: the brain's, with
//     its turn's taint; a lead's, with its session's. A note written after
//     reading external content is tainted: shown, marked « extérieur »,
//     never quoted into a summary, and its row offers no ask. Reading it
//     (read_thread, the lead's thread) taints the reader's turn, so a
//     summary written then is refused (remember kind progress).
//   - A note survives the source's reads while its item is listed. Once a
//     good read no longer lists it, it is kept threadNoteKeep (the item may
//     come back), then it goes; a source taken away takes its notes the
//     same way.

const threadItemsSchema = `CREATE TABLE IF NOT EXISTS thread_items (
	thread  INTEGER NOT NULL,
	source  TEXT NOT NULL,              -- the thread's source it is an item of
	item    TEXT NOT NULL,              -- its id in that source
	note    TEXT NOT NULL DEFAULT '',
	state   TEXT NOT NULL DEFAULT '',   -- act, look, info, none, done
	due     INTEGER NOT NULL DEFAULT 0,
	author  TEXT NOT NULL,              -- brain, lead:<thread>
	tainted TEXT NOT NULL DEFAULT '',   -- why its words may be someone else's
	noted   INTEGER NOT NULL,
	gone    INTEGER NOT NULL DEFAULT 0, -- the first good read that no longer listed its item; 0 while listed
	PRIMARY KEY (thread, source, item)
);`

func init() { extraSchema = append(extraSchema, threadItemsSchema) }

const (
	threadNoteMax    = 300                // runes of a note
	threadItemIDMax  = 200                // runes of an item's id
	threadNotesMax   = 500                // notes a thread keeps
	threadNoteKeep   = 7 * 24 * time.Hour // a note whose item left its source
	itemStateAct     = "act"              // the user has something to do: answer, decide
	itemStateLook    = "look"             // to look at
	itemStateInfo    = "info"             // to know, nothing to do
	itemStateNone    = "none"             // nothing to do
	itemStateDone    = "done"             // handled
	threadNoteTaint  = "note:"            // + source/item: a tainted note on a thread's surface
	threadNoteAuthor = "lead:"            // + the thread's id: its lead's notes
)

var itemStates = []string{itemStateAct, itemStateLook, itemStateInfo, itemStateNone, itemStateDone}

// itemNote is a note on an item, as read back.
type itemNote struct {
	Source  string `json:"source"`
	Item    string `json:"item"`
	Note    string `json:"note,omitempty"`
	State   string `json:"state,omitempty"`
	Due     int64  `json:"due,omitempty"`
	Author  string `json:"author"`
	Tainted string `json:"tainted,omitempty"`
	Noted   int64  `json:"noted"`
	Gone    int64  `json:"gone,omitempty"` // its item left its source then
}

// noteItemIn is note_item's input; the brain names the thread, a lead's is
// its own.
type noteItemIn struct {
	Thread int64  `json:"thread_id,omitempty" jsonschema:"the thread (a lead: leave it out, it is yours)"`
	Source string `json:"source" jsonschema:"the name of the thread's source that lists the item (one with items)"`
	Item   string `json:"item" jsonschema:"the item's id, as the source gives it"`
	Note   string `json:"note,omitempty" jsonschema:"what the user should know of it, in their language, one short sentence; empty with no state: the note is taken away"`
	State  string `json:"state,omitempty" jsonschema:"act (the user has something to do: answer, decide), look (to look at), info (to know), none (nothing to do) or done (handled)"`
	Due    string `json:"due,omitempty" jsonschema:"by when, YYYY-MM-DD or YYYY-MM-DDTHH:MM (local time)"`
}

// noteItem writes, replaces or takes away the note on an item of one of a
// thread's sources. author and taint are who writes it and why its words
// may be someone else's: code's, never given.
func (c *core) noteItem(id int64, in noteItemIn, author string, taint []string) (string, error) {
	in.Source, in.Item = strings.TrimSpace(in.Source), strings.TrimSpace(in.Item)
	in.Note, in.State = oneLine(in.Note), strings.ToLower(strings.TrimSpace(in.State))
	switch {
	case in.Item == "" || utf8.RuneCountInString(in.Item) > threadItemIDMax:
		return "", fmt.Errorf("item is its id in the source, %d characters at most", threadItemIDMax)
	case utf8.RuneCountInString(in.Note) > threadNoteMax:
		return "", fmt.Errorf("a note of %d characters at most", threadNoteMax)
	case in.State != "" && !slices.Contains(itemStates, in.State):
		return "", errors.New("state is act, look, info, none or done")
	}
	due, err := parseWhen(in.Due)
	if err != nil {
		return "", err
	}
	s, err := c.thread(id)
	if err != nil {
		return "", err
	}
	if !s.active() {
		return "", fmt.Errorf("the thread is %s: reopen it first", s.Look())
	}
	var src *storedSource
	for _, x := range c.threadSources(id) {
		if x.Name == in.Source {
			src = &x
			break
		}
	}
	if src == nil || src.Items == nil {
		return "", fmt.Errorf("the thread has no source %q that lists items (link_thread source with items)", in.Source)
	}
	if in.Note == "" && in.State == "" && due.IsZero() {
		c.store.db.Exec(`DELETE FROM thread_items WHERE thread = ? AND source = ? AND item = ?`, id, in.Source, in.Item)
		c.threadMoved(id)
		return "Note taken away.", nil
	}
	var n int
	c.store.db.QueryRow(`SELECT COUNT(*) FROM thread_items WHERE thread = ? AND NOT (source = ? AND item = ?)`, id, in.Source, in.Item).Scan(&n)
	if n >= threadNotesMax {
		return "", fmt.Errorf("a thread keeps %d notes at most", threadNotesMax)
	}
	// An item its source no longer lists, or not yet: kept as gone, and
	// back if a read lists it.
	listed := slices.ContainsFunc(threadSourceItems(*src, 0), func(it map[string]any) bool { return it["id"] == in.Item })
	now := time.Now().Unix()
	gone := int64(0)
	if !listed {
		gone = now
	}
	var dueAt int64
	if !due.IsZero() {
		dueAt = due.Unix()
	}
	tainted := strings.Join(slices.Compact(slices.Sorted(slices.Values(taint))), ", ")
	if _, err := c.store.db.Exec(`INSERT INTO thread_items (thread, source, item, note, state, due, author, tainted, noted, gone) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (thread, source, item) DO UPDATE SET note = excluded.note, state = excluded.state, due = excluded.due,
		  author = excluded.author, tainted = excluded.tainted, noted = excluded.noted,
		  gone = CASE WHEN excluded.gone = 0 THEN 0 WHEN thread_items.gone > 0 THEN thread_items.gone ELSE excluded.gone END`,
		id, in.Source, in.Item, in.Note, in.State, dueAt, author, tainted, now, gone); err != nil {
		return "", err
	}
	c.threadMoved(id)
	note := "Noted: it shows on the item's row, read by code."
	if !listed {
		note += fmt.Sprintf(" %s does not list %q in its last read: the note waits for it, %d days at most.", in.Source, in.Item, int(threadNoteKeep/(24*time.Hour)))
	}
	if tainted != "" {
		note += " Written after reading external content: shown as « extérieur », never quoted into a summary."
	}
	return note, nil
}

// itemNotes are a thread's notes, by source then item.
func (c *core) itemNotes(thread int64) []itemNote {
	rows, err := c.store.db.Query(`SELECT source, item, note, state, due, author, tainted, noted, gone FROM thread_items WHERE thread = ? ORDER BY source, item`, thread)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []itemNote
	for rows.Next() {
		var n itemNote
		if rows.Scan(&n.Source, &n.Item, &n.Note, &n.State, &n.Due, &n.Author, &n.Tainted, &n.Noted, &n.Gone) == nil {
			out = append(out, n)
		}
	}
	return out
}

// itemsListed keeps, after a good read of a source with items, which of
// its notes' items it lists: one listed again is back, one no longer
// listed is gone from now on.
func (c *core) itemsListed(thread int64, src storedSource, now time.Time) {
	listed := map[string]bool{}
	for _, it := range threadSourceItems(src, 0) { // all it lists, beyond those shown
		listed[it["id"].(string)] = true
	}
	rows, err := c.store.db.Query(`SELECT item, gone FROM thread_items WHERE thread = ? AND source = ?`, thread, src.Name)
	if err != nil {
		return
	}
	type change struct {
		item string
		gone int64
	}
	var changes []change
	for rows.Next() {
		var item string
		var gone int64
		if rows.Scan(&item, &gone) != nil {
			continue
		}
		switch {
		case listed[item] && gone > 0:
			changes = append(changes, change{item, 0})
		case !listed[item] && gone == 0:
			changes = append(changes, change{item, now.Unix()})
		}
	}
	rows.Close() // one connection: written after
	for _, ch := range changes {
		c.store.db.Exec(`UPDATE thread_items SET gone = ? WHERE thread = ? AND source = ? AND item = ?`, ch.gone, thread, src.Name, ch.item)
	}
}

// sourceNotesGone starts the clock of a source's notes: the source was
// taken away.
func (c *core) sourceNotesGone(thread int64, source string, now time.Time) {
	c.store.db.Exec(`UPDATE thread_items SET gone = ? WHERE thread = ? AND source = ? AND gone = 0`, now.Unix(), thread, source)
}

// pruneItemNotes takes away the notes whose item left its source more
// than threadNoteKeep ago: the engine's tick.
func (c *core) pruneItemNotes(now time.Time) {
	c.store.db.Exec(`DELETE FROM thread_items WHERE gone > 0 AND gone < ?`, now.Add(-threadNoteKeep).Unix())
}

// noteAuthor is who wrote a note, as its row says it.
func noteAuthor(author, lang string) string {
	if strings.HasPrefix(author, threadNoteAuthor) {
		return tr(lang, "thread.surface.part.lead")
	}
	return tr(lang, "thread.surface.part.brain")
}

// noteRow puts a note on its item's row: its words, its state, its due
// date, who wrote it and whether it may carry someone else's words.
func noteRow(row map[string]any, n itemNote, lang string, loc *time.Location, now time.Time) {
	row["note"], row["state"], row["by"] = n.Note, n.State, noteAuthor(n.Author, lang)
	parts := []string{}
	if n.Tainted != "" {
		row["external"] = true
		parts = append(parts, tr(lang, "thread.surface.external"))
	}
	if n.State != "" {
		parts = append(parts, tr(lang, "thread.item.state."+n.State))
	}
	if n.Due > 0 {
		due := time.Unix(n.Due, 0)
		row["due"] = due.In(loc).Format(time.RFC3339)
		parts = append(parts, fmt.Sprintf(tr(lang, "thread.item.due"), threadClock(due, now, loc, lang)))
	}
	if n.Note != "" {
		parts = append(parts, n.Note)
	}
	row["detail"] = strings.Join(parts, threadExternalSep)
}

// readNotesTaint is why reading a thread's notes taints the reader: each
// tainted note's.
func readNotesTaint(notes []itemNote) []string {
	var out []string
	for _, n := range notes {
		if n.Tainted != "" {
			out = append(out, threadNoteTaint+n.Source+"/"+n.Item)
		}
	}
	return out
}
