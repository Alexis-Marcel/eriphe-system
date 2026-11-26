package main

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

// A block's snapshot is what core.db kept for it: its sources' last good
// values, the latest read, and the notes of its node's items; nothing for
// a block code kept nothing of, nor in a core.db without the tables.
func TestBlockSnapshots(t *testing.T) {
	st, err := openStore(filepath.Join(t.TempDir(), "core.db"))
	if err != nil {
		t.Fatal(err)
	}
	db := st.db
	for _, q := range []string{
		`INSERT INTO node_reads (node, block, name, spec, value, read) VALUES ('n1', 'mails', 'm', '{}', '{"messages":[]}', 100)`,
		`INSERT INTO node_reads (node, block, name, spec, value, read) VALUES ('n1', 'mails', 'k', '{}', '{"x":1}', 200)`,
		`INSERT INTO node_reads (node, block, name, spec, value, read, error) VALUES ('n1', 'mails', 'never', '{}', '', 0, 'down')`,
		`INSERT INTO node_items (node, source, item, note, state, grp, author, tainted, noted) VALUES ('n1', 'm', 'aWQtMQ', 'à lire', 'act', 'À traiter', 'skill:mails', 'mail', 1)`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	snap := blockSnapshots(db)
	sn := snap("n1", "mails")
	if sn == nil || len(sn.Values) != 2 || string(sn.Values["m"]) != `{"messages":[]}` || !sn.Read.Equal(time.Unix(200, 0)) {
		t.Fatalf("snapshot: %+v", sn)
	}
	if n := sn.Notes["m"]; len(n) != 1 || n[0].Item != "aWQtMQ" || n[0].Group != "À traiter" || n[0].State != "act" || !n[0].Tainted {
		t.Fatalf("notes: %+v", sn.Notes)
	}
	if sn := snap("n2", "mails"); sn != nil {
		t.Fatalf("another node: %+v", sn)
	}
	bare, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "bare.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer bare.Close()
	if sn := blockSnapshots(bare)("n1", "mails"); sn != nil {
		t.Fatalf("without the tables: %+v", sn)
	}
}
