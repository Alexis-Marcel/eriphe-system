package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"time"
)

// The stored subjects are rewritten once to their key (subject.go): the
// threads', the notifications' and the topic rules' in core.db
// (subjectKeysCore), the memories' and the intentions' in memory.db
// (memory schema 6). A value that gives no key is kept and logged: a
// rewrite never stops eriphe-core from starting.

// subjectKeysDone is the kv key that says core.db's subjects are keys.
const subjectKeysDone = "subject_keys"

// subjectKeysCore rewrites core.db's subjects once, then makes sure a
// subject has one live thread (threadsLiveSubject). The tables of removed
// concepts go with it.
func subjectKeysCore(db *sql.DB) error {
	for _, q := range []string{`DROP TABLE IF EXISTS facts`, `DROP TABLE IF EXISTS facts_log`} {
		if _, err := db.Exec(q); err != nil {
			return err
		}
	}
	var done string
	if db.QueryRow(`SELECT value FROM kv WHERE key = ?`, subjectKeysDone).Scan(&done) != nil {
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		defer tx.Rollback()
		for _, step := range []func(*sql.Tx) error{rekeyThreads, rekeyNotifications, rekeyTopicRules} {
			if err := step(tx); err != nil {
				return fmt.Errorf("subject keys: %w", err)
			}
		}
		if _, err := tx.Exec(`INSERT OR REPLACE INTO kv (key, value) VALUES (?, '1')`, subjectKeysDone); err != nil {
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	_, err := db.Exec(threadsLiveSubject)
	return err
}

// rekeyed is a stored subject as a key; ok false when it gives none.
func rekeyed(table string, id any, s string) (string, bool) {
	k, err := subjectKey(s)
	if err != nil {
		log.Printf("subject keys: %s %v keeps %q: %v", table, id, s, err)
		return s, false
	}
	return k, true
}

// rekeyThreads gives each thread its subject's key; of live threads that
// end on one key, the latest keeps it and the others lose it, said in
// their log.
func rekeyThreads(tx *sql.Tx) error {
	type row struct {
		id      int64
		subject string
		live    bool
	}
	rows, err := tx.Query(`SELECT id, subject, ` + activeThreads + ` FROM threads WHERE subject != '' ORDER BY id DESC`)
	if err != nil {
		return err
	}
	var all []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.subject, &r.live); err != nil {
			rows.Close()
			return err
		}
		all = append(all, r)
	}
	rows.Close()
	live := map[string]int64{}
	for _, r := range all {
		k, _ := rekeyed("thread", r.id, r.subject)
		if r.live && live[k] != 0 {
			if _, err := tx.Exec(`UPDATE threads SET subject = '' WHERE id = ?`, r.id); err != nil {
				return err
			}
			if _, err := tx.Exec(`INSERT INTO thread_log (thread, at, who, text) VALUES (?, ?, 'eriphe-core', ?)`, r.id, time.Now().Unix(),
				fmt.Sprintf("Sujet %s retiré : le thread %d le suit déjà.", k, live[k])); err != nil {
				return err
			}
			continue
		}
		if r.live {
			live[k] = r.id
		}
		if k != r.subject {
			if _, err := tx.Exec(`UPDATE threads SET subject = ? WHERE id = ?`, k, r.id); err != nil {
				return err
			}
		}
	}
	return nil
}

// rekeyNotifications gives each notification about a subject its key; a
// record's ref (skill:<name>, intention:<id>) is kept.
func rekeyNotifications(tx *sql.Tx) error {
	rows, err := tx.Query(`SELECT DISTINCT ref FROM notifications WHERE ref != ''`)
	if err != nil {
		return err
	}
	var refs []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			rows.Close()
			return err
		}
		refs = append(refs, s)
	}
	rows.Close()
	for _, s := range refs {
		if isRecordRef(s) {
			continue
		}
		if k, ok := rekeyed("notifications ref", s, s); ok && k != s {
			if _, err := tx.Exec(`UPDATE notifications SET ref = ? WHERE ref = ?`, k, s); err != nil {
				return err
			}
		}
	}
	return nil
}

// rekeyTopicRules gives each topic rule its subject's key.
func rekeyTopicRules(tx *sql.Tx) error {
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
		if json.Unmarshal([]byte(raw), &sp) == nil && sp.Kind == ruleTopic && sp.Match != "" {
			specs[id] = sp
		}
	}
	rows.Close()
	for id, sp := range specs {
		k := ruleMatch(sp.Match)
		if k == "" {
			log.Printf("subject keys: rule %d keeps %q", id, sp.Match)
			continue
		}
		if k == sp.Match {
			continue
		}
		sp.Match = k
		b, _ := json.Marshal(sp)
		if _, err := tx.Exec(`UPDATE rules SET spec = ? WHERE id = ?`, string(b), id); err != nil {
			return err
		}
	}
	return nil
}

// rekeyMemory gives memory.db's memories and intentions their subjects'
// keys (schema 5 to 6); the memories' stems follow their subject.
func rekeyMemory(tx *sql.Tx) error {
	type mem struct {
		id                      int64
		subject, text, keywords string
	}
	rows, err := tx.Query(`SELECT id, subject, text, keywords FROM memories WHERE subject != ''`)
	if err != nil {
		return err
	}
	var ms []mem
	for rows.Next() {
		var m mem
		if err := rows.Scan(&m.id, &m.subject, &m.text, &m.keywords); err != nil {
			rows.Close()
			return err
		}
		ms = append(ms, m)
	}
	rows.Close()
	for _, m := range ms {
		if k, ok := rekeyed("memory", m.id, m.subject); ok && k != m.subject {
			if _, err := tx.Exec(`UPDATE memories SET subject = ?, stems = ? WHERE id = ?`, k, memoryStems(m.text, k, m.keywords), m.id); err != nil {
				return err
			}
		}
	}
	type intent struct {
		id                     int64
		owner, owedTo, subject string
	}
	rows, err = tx.Query(`SELECT id, owner, owed_to, subject FROM intentions`)
	if err != nil {
		return err
	}
	var xs []intent
	for rows.Next() {
		var x intent
		if err := rows.Scan(&x.id, &x.owner, &x.owedTo, &x.subject); err != nil {
			rows.Close()
			return err
		}
		xs = append(xs, x)
	}
	rows.Close()
	for _, x := range xs {
		owner, owedTo, subject := x.owner, x.owedTo, x.subject
		if k, err := intentionParty(owner); err == nil {
			owner = k
		}
		if k, err := intentionParty(owedTo); err == nil {
			owedTo = k
		}
		subject, _ = rekeyed("intention", x.id, subject)
		if owner != x.owner || owedTo != x.owedTo || subject != x.subject {
			if _, err := tx.Exec(`UPDATE intentions SET owner = ?, owed_to = ?, subject = ? WHERE id = ?`, owner, owedTo, subject, x.id); err != nil {
				return err
			}
		}
	}
	return nil
}
