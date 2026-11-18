package nodestore

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// Item notes: what an author says of one item a block's source lists (a
// mail, an event), kept in the index (node_items) by the node, the
// source and the item's id. A note is not a version of the node.

// ItemNoteMax is the most runes of an item's note.
const ItemNoteMax = 400

// ItemStates are an item's states: what the user should do of it.
var ItemStates = []string{"act", "look", "info", "none", "done"}

// Item is an item's note.
type Item struct {
	Source  string
	Item    string
	Note    string
	State   string
	Author  string
	Tainted string // why it may carry someone else's words; "" when it does not
}

// NoteItem keeps an item's note: the same item noted again replaces it;
// no note nor state takes it away. The node's source must be one of its
// blocks' sources.
func (s *Store) NoteItem(id string, it Item) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if strings.TrimSpace(it.Author) == "" {
		return errors.New("a write has an author")
	}
	it.Note, it.State = strings.TrimSpace(it.Note), strings.TrimSpace(it.State)
	switch {
	case strings.TrimSpace(it.Item) == "":
		return errors.New("an item is named by its id")
	case len([]rune(it.Note)) > ItemNoteMax:
		return fmt.Errorf("a note is %d characters at most", ItemNoteMax)
	case it.State != "" && !slices.Contains(ItemStates, it.State):
		return fmt.Errorf("an item's state is one of %s", strings.Join(ItemStates, ", "))
	}
	if err := s.refuse(it.Note); err != nil {
		return err
	}
	cur, err := s.read(id, 0)
	if err != nil {
		return err
	}
	if !cur.Node.Meta.Archived.IsZero() {
		return fmt.Errorf("%s: %w", id, ErrArchived)
	}
	var names []string
	found := false
	for _, b := range cur.Node.Blocks() {
		for name := range b.Sources {
			names = append(names, name)
			found = found || name == it.Source
		}
	}
	if !found {
		if len(names) == 0 {
			return fmt.Errorf("%s has no source: a block's sources list the items noted", id)
		}
		return fmt.Errorf("%s has no source %q (its sources: %s)", id, it.Source, strings.Join(names, ", "))
	}
	if it.Note == "" && it.State == "" {
		_, err = s.db.Exec(`DELETE FROM node_items WHERE id = ? AND source = ? AND item = ?`, id, it.Source, it.Item)
	} else {
		_, err = s.db.Exec(`INSERT OR REPLACE INTO node_items (id, source, item, note, state, author, tainted, noted) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			id, it.Source, it.Item, it.Note, it.State, it.Author, it.Tainted, s.opts.Now().Unix())
	}
	if err == nil {
		s.changed(ChangedState, id)
	}
	return err
}

// Items are a node's item notes, by source then item.
func (s *Store) Items(id string) ([]Item, error) {
	rows, err := s.db.Query(`SELECT source, item, note, state, author, tainted FROM node_items WHERE id = ? ORDER BY source, item`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Item
	for rows.Next() {
		var it Item
		if err := rows.Scan(&it.Source, &it.Item, &it.Note, &it.State, &it.Author, &it.Tainted); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}
