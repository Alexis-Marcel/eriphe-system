package main

import (
	"database/sql"
	"encoding/json"
	"time"

	"github.com/Alexis-Marcel/eriphe-system/core/nodefile"
)

// The conversion of the nodes to format 3 (nodefile's topage.go) writes
// in each page what code kept outside its file for a block: its sources'
// last values (node_reads) and its items' notes (node_items), from
// core.db. A table that is not there (a new install) gives nothing.

// blockSnapshots gives a block's snapshot from core.db; nil when code
// kept nothing for it.
func blockSnapshots(db *sql.DB) nodefile.Snapshots {
	return func(node, block string) *nodefile.Snapshot {
		sn := &nodefile.Snapshot{Values: map[string]json.RawMessage{}, Notes: map[string][]nodefile.ItemNote{}}
		if rows, err := db.Query(`SELECT name, value, read FROM node_reads WHERE node = ? AND block = ? AND value != ''`, node, block); err == nil {
			for rows.Next() {
				var name, value string
				var read int64
				if rows.Scan(&name, &value, &read) != nil || !json.Valid([]byte(value)) {
					continue
				}
				sn.Values[name] = json.RawMessage(value)
				if t := time.Unix(read, 0); read > 0 && t.After(sn.Read) {
					sn.Read = t
				}
			}
			rows.Close()
		}
		if rows, err := db.Query(`SELECT source, item, note, state, grp, tainted FROM node_items WHERE node = ? ORDER BY noted`, node); err == nil {
			for rows.Next() {
				var src, tainted string
				var n nodefile.ItemNote
				if rows.Scan(&src, &n.Item, &n.Note, &n.State, &n.Group, &tainted) != nil {
					continue
				}
				n.Tainted = tainted != ""
				sn.Notes[src] = append(sn.Notes[src], n)
			}
			rows.Close()
		}
		if len(sn.Values) == 0 && len(sn.Notes) == 0 {
			return nil
		}
		return sn
	}
}
