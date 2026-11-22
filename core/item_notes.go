package main

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/Alexis-Marcel/eriphe-system/core/nodestore"
)

// Item notes: what an author says of one item a block's source lists (a
// mail, an event), by the node, the source and the item's id. They are
// code's and the workspace's, kept in core.db beside the node's cards and
// its sources' reads: never a version of the node, and nothing a rebuilt
// nodes.db loses.

const nodeItemsSchema = `CREATE TABLE IF NOT EXISTS node_items (
	node    TEXT NOT NULL,
	source  TEXT NOT NULL,
	item    TEXT NOT NULL,
	note    TEXT NOT NULL DEFAULT '',
	state   TEXT NOT NULL DEFAULT '',   -- one of itemStates
	grp     TEXT NOT NULL DEFAULT '',   -- its group: the section a view shows it in
	author  TEXT NOT NULL,
	tainted TEXT NOT NULL DEFAULT '',   -- why it may carry someone else's words
	noted   INTEGER NOT NULL,
	PRIMARY KEY (node, source, item)
);`

func init() { extraSchema = append(extraSchema, nodeItemsSchema) }

// itemNoteMax is the most runes of an item's note.
const itemNoteMax = 400

// itemGroupMax is the most runes of an item's group.
const itemGroupMax = 40

// itemStates are an item's states: what the user should do of it.
var itemStates = []string{"act", "look", "info", "none", "done"}

// itemNote is an item's note. Its state is one of itemStates, what the
// user should do of it (code reads it: attention, the runs); its group is
// a short label the noter chooses (« À supprimer »): the section a view
// shows it in, a List filtering on it ([_group=À supprimer]).
type itemNote struct {
	Source  string
	Item    string
	Note    string
	State   string
	Group   string
	Author  string
	Tainted string // why it may carry someone else's words; "" when it does not
}

// noteItem keeps an item's note: the same item noted again replaces it;
// no note, state nor group takes it away. The node's source must be one
// of its blocks' sources, the node not archived. A group is one line
// without [ ] / = ! (a List's filter names it), itemGroupMax runes at
// most. The pages that show the source are told.
func (c *core) noteItem(id string, it itemNote) error {
	if c.nodes == nil {
		return errors.New("the graph is not open on this eriphe-core")
	}
	if strings.TrimSpace(it.Author) == "" {
		return errors.New("a write has an author")
	}
	it.Note, it.State, it.Group = strings.TrimSpace(it.Note), strings.TrimSpace(it.State), strings.TrimSpace(it.Group)
	switch {
	case strings.TrimSpace(it.Item) == "":
		return errors.New("an item is named by its id")
	case len([]rune(it.Note)) > itemNoteMax:
		return fmt.Errorf("a note is %d characters at most", itemNoteMax)
	case it.State != "" && !slices.Contains(itemStates, it.State):
		return fmt.Errorf("an item's state is one of %s", strings.Join(itemStates, ", "))
	case len([]rune(it.Group)) > itemGroupMax || strings.ContainsAny(it.Group, "\n\r\t[]/=!"):
		return fmt.Errorf("a group is one line of %d characters at most, without [ ] / = ! (a list's filter names it)", itemGroupMax)
	}
	if err := refuseSecret(it.Note + "\n" + it.Group); err != nil {
		return fmt.Errorf("refused: %w", err)
	}
	cur, err := c.nodes.Read(id, 0)
	if err != nil {
		return err
	}
	if !cur.Node.Meta.Archived.IsZero() {
		return fmt.Errorf("%s: %w", id, nodestore.ErrArchived)
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
		slices.Sort(names)
		return fmt.Errorf("%s has no source %q (its sources: %s)", id, it.Source, strings.Join(names, ", "))
	}
	if it.Note == "" && it.State == "" && it.Group == "" {
		_, err = c.store.db.Exec(`DELETE FROM node_items WHERE node = ? AND source = ? AND item = ?`, id, it.Source, it.Item)
	} else {
		_, err = c.store.db.Exec(`INSERT OR REPLACE INTO node_items (node, source, item, note, state, grp, author, tainted, noted) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			id, it.Source, it.Item, it.Note, it.State, it.Group, it.Author, it.Tainted, time.Now().Unix())
	}
	if err != nil {
		return err
	}
	// Its row, and the source as the page draws it, in each block that
	// reads it.
	c.graphSaid(graphEvent{Kind: graphEvNode, Node: id})
	c.graphSaid(graphEvent{Kind: graphEvSource, Node: id, Name: it.Source})
	return nil
}

// nodeItems are a node's item notes, by source then item.
func (c *core) nodeItems(id string) ([]itemNote, error) {
	rows, err := c.store.db.Query(`SELECT source, item, note, state, grp, author, tainted FROM node_items WHERE node = ? ORDER BY source, item`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []itemNote
	for rows.Next() {
		var it itemNote
		if err := rows.Scan(&it.Source, &it.Item, &it.Note, &it.State, &it.Group, &it.Author, &it.Tainted); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}
