package main

// TO DELETE in a later cleanup, with convert_one_thread_test.go and its
// two calls (openStore, useMemory), once the single install has run it.
//
// The one-shot conversion of an install made before « one object, the
// thread » (#578, step 8: #577). It runs once, behind a flag in each
// database's kv, in one transaction per step:
//
//   - core.db, when it opens (convertOneThread): the subjects become the
//     names (names.go), a thread's subject its alias, a merged subject the
//     name of what it went into; the about columns and the topic rules are
//     kept under their target, a mail or an event is no about (dropped);
//     the subjects table, threads.subject and thread_monitors go, with the
//     kv they left; the default skill arrivee goes; a stored skill whose
//     approved version no longer reads, or is written the old way (can,
//     subject), is turned off, the user told why on its page.
//   - memory.db, when the memory attaches (convertOneThreadMemory): a
//     matter that memories are about and no thread follows becomes an
//     ended thread (Q7), tidied away; then the memories' and intentions'
//     abouts, owners and owed_to are kept under their target, the stems
//     and the full-text index follow.
//
// A database made new has nothing to convert: its flag is set at once.

import (
	"database/sql"
	"encoding/json"
	"log"
	"slices"
	"strings"
	"time"
	"unicode"
)

const (
	oneThreadFlag  = "one_thread_converted" // in both kv: when it ran
	oneThreadFresh = "fresh"                // core.db's flag when there was nothing to convert
)

// skillsOffOld is why the conversion turned a skill off (skills.off_why).
const skillsOffOld = "skills.off.old"

// oneThreadSkillsKeys are front matter keys of skills written before #578.
var oneThreadSkillsKeys = []string{"can", "subject"}

// oneThreadDeadKV are the kv keys nothing reads any more.
var oneThreadDeadKV = []string{"subject_keys", "subjects_filled", "notification_subjects", "default_skill", "default_skill_sha"}

// convertOneThread converts core.db, once. A failure is rolled back and
// said: the next start tries again.
func convertOneThread(db *sql.DB) {
	var done string
	if db.QueryRow(`SELECT value FROM kv WHERE key = ?`, oneThreadFlag).Scan(&done) == nil {
		return
	}
	n, err := convertOneThreadCore(db, time.Now())
	if err != nil {
		log.Printf("one-thread conversion of core.db: %v (tried again at the next start)", err)
		return
	}
	if n.fresh {
		return
	}
	log.Printf("one-thread conversion of core.db: %d names (%d of threads, %d merged), %d abouts rekeyed, %d dropped (mail, event), %d rules, %d kv dropped, %d skills off, %d default skill deleted",
		n.names, n.threadNames, n.merged, n.rekeyed, n.dropped, n.rules, n.kv, n.skillsOff, n.arrivee)
}

type oneThreadCounts struct {
	fresh                               bool
	names, threadNames, merged, rekeyed int
	dropped, rules, kv, skillsOff       int
	arrivee                             int
}

// oneThreadAbout is an about written before #578, as kept now: its key
// (aliasKey), "" for a mail or an event (an item, never an about), as
// written for what is no key (a thread, a bare word).
func oneThreadAbout(v string) (key string, dropped bool) {
	v = strings.TrimSpace(v)
	if v == "" || strings.HasPrefix(v, refThread+":") || !isKeyName(v) {
		return v, false
	}
	kind, _, _ := strings.Cut(v, ":")
	if itemKinds[strings.ToLower(strings.TrimSpace(kind))] {
		return "", true
	}
	k, err := aliasKey(v)
	if err != nil || k == "" {
		return v, false
	}
	return k, false
}

// oneThreadEntity tells a key that is an entity: a person, the user, the
// home, a tool, Eriphe's own topics. Never a thread.
func oneThreadEntity(key string) bool {
	kind, _, _ := strings.Cut(key, ":")
	return kind == aliasPerson || kind == aliasTool || key == aliasOwner || key == aliasHome || systemEntity(key)
}

func tableExists(q interface {
	QueryRow(string, ...any) *sql.Row
}, name string) bool {
	var n int
	q.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, name).Scan(&n)
	return n > 0
}

func convertOneThreadCore(db *sql.DB, now time.Time) (n oneThreadCounts, err error) {
	hasCol, err := columns(db, "threads")
	if err != nil {
		return n, err
	}
	tx, err := db.Begin()
	if err != nil {
		return n, err
	}
	defer tx.Rollback()
	hasSubjects := tableExists(tx, "subjects")
	if !hasSubjects && !hasCol["subject"] {
		n.fresh = true
		_, err = tx.Exec(`INSERT INTO kv (key, value) VALUES (?, ?)`, oneThreadFlag, oneThreadFresh)
		if err != nil {
			return n, err
		}
		return n, tx.Commit()
	}

	target := map[string]string{}  // a key → what it names now
	seen := map[string]int64{}     // a key → when it was last written
	extra := map[string][]string{} // a target → names the user gave it
	// A thread's subject is its alias: the latest thread of a key has it.
	if hasCol["subject"] {
		rows, err := tx.Query(`SELECT id, subject, MAX(created, started, ended) FROM threads WHERE subject != '' ORDER BY id`)
		if err != nil {
			return n, err
		}
		for rows.Next() {
			var id, at int64
			var subject string
			if err := rows.Scan(&id, &subject, &at); err != nil {
				rows.Close()
				return n, err
			}
			if k, drop := oneThreadAbout(subject); !drop && k != "" && isKeyName(k) && !oneThreadEntity(k) {
				target[k], seen[k] = threadRef(id), at
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return n, err
		}
		n.threadNames = len(target)
	}
	// The subjects: their keys, titles and aliases; the merged ones last.
	if hasSubjects {
		type subj struct {
			key, title, aliases, merged string
			seen                        int64
		}
		var subs []subj
		rows, err := tx.Query(`SELECT key, title, aliases, merged_into, last_seen FROM subjects ORDER BY merged_into != '', key`)
		if err != nil {
			return n, err
		}
		for rows.Next() {
			var x subj
			if err := rows.Scan(&x.key, &x.title, &x.aliases, &x.merged, &x.seen); err != nil {
				rows.Close()
				return n, err
			}
			subs = append(subs, x)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return n, err
		}
		for _, x := range subs {
			k, drop := oneThreadAbout(x.key)
			if drop || k == "" || !isKeyName(k) {
				continue
			}
			if x.merged != "" {
				into, drop := oneThreadAbout(x.merged)
				if drop || into == "" || into == k {
					continue
				}
				if t, ok := target[into]; ok {
					into = t
				}
				target[k] = into
				n.merged++
			} else if _, ok := target[k]; !ok {
				target[k] = k
			}
			seen[k] = max(seen[k], x.seen)
			var names []string
			json.Unmarshal([]byte(x.aliases), &names)
			extra[target[k]] = append(extra[target[k]], append([]string{x.title}, names...)...)
		}
	}
	// The about columns: each value kept under its target.
	conv := func(v string) (string, bool) {
		k, drop := oneThreadAbout(v)
		if drop {
			return "", true
		}
		if !isKeyName(k) || strings.HasPrefix(k, refThread+":") {
			return k, false
		}
		if t, ok := target[k]; ok {
			return t, false
		}
		target[k] = k // a key no thread has: it names itself
		return k, false
	}
	for _, table := range aboutTables {
		vals, err := distinct(tx, `SELECT DISTINCT about FROM `+table+` WHERE about != '' AND about NOT LIKE 'thread:%'`)
		if err != nil {
			return n, err
		}
		for _, v := range vals {
			to, drop := conv(v)
			if to == v {
				continue
			}
			res, err := tx.Exec(`UPDATE `+table+` SET about = ? WHERE about = ?`, to, v)
			if err != nil {
				return n, err
			}
			c, _ := res.RowsAffected()
			if drop {
				n.dropped += int(c)
			} else {
				n.rekeyed += int(c)
			}
		}
	}
	// The topic rules match their target.
	rows, err := tx.Query(`SELECT id, spec FROM rules WHERE source = ?`, sourceNotification)
	if err != nil {
		return n, err
	}
	specs := map[int64]notifySpec{}
	for rows.Next() {
		var id int64
		var raw string
		var sp notifySpec
		if err := rows.Scan(&id, &raw); err != nil {
			rows.Close()
			return n, err
		}
		if json.Unmarshal([]byte(raw), &sp) == nil && sp.Kind == ruleTopic && sp.Match != "" {
			specs[id] = sp
		}
	}
	rows.Close()
	for id, sp := range specs {
		to, drop := conv(sp.Match)
		if drop || to == sp.Match {
			continue
		}
		sp.Match = to
		b, _ := json.Marshal(sp)
		if _, err := tx.Exec(`UPDATE rules SET spec = ? WHERE id = ?`, string(b), id); err != nil {
			return n, err
		}
		n.rules++
	}
	// The names: every key, then the names the user gave its target.
	keys := make([]string, 0, len(target))
	for k := range target {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		at := seen[k]
		if at == 0 {
			at = now.Unix()
		}
		if _, err := tx.Exec(`INSERT OR REPLACE INTO names (name, target, seen) VALUES (?, ?, ?)`, k, target[k], at); err != nil {
			return n, err
		}
		n.names++
	}
	for t, names := range extra {
		var have []string
		for _, name := range names {
			name = oneLine(name)
			if name == "" || isKeyName(name) || len([]rune(name)) > threadNameMax || len(have) >= threadNamesMax ||
				slices.ContainsFunc(have, func(h string) bool { return strings.EqualFold(foldAccents(h), foldAccents(name)) }) {
				continue
			}
			have = append(have, name)
			res, err := tx.Exec(`INSERT OR IGNORE INTO names (name, target, seen) VALUES (?, ?, ?)`, name, t, now.Unix())
			if err != nil {
				return n, err
			}
			if c, _ := res.RowsAffected(); c > 0 {
				n.names++
			}
		}
	}
	// The brain's turn names what its messages name.
	var turn string
	if tx.QueryRow(`SELECT value FROM kv WHERE key = 'brain_turn'`).Scan(&turn) == nil {
		var m map[string]any
		if json.Unmarshal([]byte(turn), &m) == nil {
			if old, ok := m["subjects"].([]any); ok {
				var named []string
				for _, x := range old {
					if s, ok := x.(string); ok {
						if to, drop := conv(s); !drop && to != "" && !slices.Contains(named, to) {
							named = append(named, to)
						}
					}
				}
				delete(m, "subjects")
				if len(named) > 0 {
					m["named"] = named
				}
				b, _ := json.Marshal(m)
				if _, err := tx.Exec(`UPDATE kv SET value = ? WHERE key = 'brain_turn'`, string(b)); err != nil {
					return n, err
				}
			}
		}
	}
	// What is left of the subjects, the old monitors' threads and the
	// default skill.
	for _, k := range oneThreadDeadKV {
		res, err := tx.Exec(`DELETE FROM kv WHERE key = ?`, k)
		if err != nil {
			return n, err
		}
		c, _ := res.RowsAffected()
		n.kv += int(c)
	}
	res, err := tx.Exec(`DELETE FROM kv WHERE key LIKE 'surface\_missing:%' ESCAPE '\'`)
	if err != nil {
		return n, err
	}
	c, _ := res.RowsAffected()
	n.kv += int(c)
	if hasCol["subject"] {
		for _, q := range []string{`DROP INDEX IF EXISTS threads_live_subject`, `DROP INDEX IF EXISTS threads_subject`, `ALTER TABLE threads DROP COLUMN subject`} {
			if _, err := tx.Exec(q); err != nil {
				return n, err
			}
		}
	}
	for _, q := range []string{`DROP TABLE IF EXISTS subjects`, `DROP TABLE IF EXISTS thread_monitors`} {
		if _, err := tx.Exec(q); err != nil {
			return n, err
		}
	}
	res, err = tx.Exec(`DELETE FROM skills WHERE name = 'arrivee' AND source = 'default'`)
	if err != nil {
		return n, err
	}
	c, _ = res.RowsAffected()
	n.arrivee = int(c)
	if n.skillsOff, err = oneThreadSkillsOff(tx, now); err != nil {
		return n, err
	}
	if err := gcSkillBundles(tx); err != nil {
		return n, err
	}
	b, _ := json.Marshal(map[string]any{"at": now.Unix(), "names": n.names, "rekeyed": n.rekeyed, "dropped": n.dropped, "skills_off": n.skillsOff})
	if _, err := tx.Exec(`INSERT INTO kv (key, value) VALUES (?, ?)`, oneThreadFlag, string(b)); err != nil {
		return n, err
	}
	return n, tx.Commit()
}

// oneThreadSkillsOff turns off the skills whose approved version no longer
// reads, or is written the old way: the user approves a new one.
func oneThreadSkillsOff(tx *sql.Tx, now time.Time) (int, error) {
	rows, err := tx.Query(`SELECT s.name, b.data FROM skills s JOIN skill_bundles b ON b.sha = s.approved WHERE s.approved != ''`)
	if err != nil {
		return 0, err
	}
	var off []string
	for rows.Next() {
		var name string
		var data []byte
		if err := rows.Scan(&name, &data); err != nil {
			rows.Close()
			return 0, err
		}
		if oneThreadSkillOld(data) {
			off = append(off, name)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	for _, name := range off {
		if _, err := tx.Exec(`UPDATE skills SET enabled = 0, off_why = ?, updated = ? WHERE name = ?`, skillsOffOld, now.Unix(), name); err != nil {
			return 0, err
		}
	}
	return len(off), nil
}

// oneThreadSkillOld tells a bundle whose SKILL.md no longer reads, or has
// a key of before (can, subject).
func oneThreadSkillOld(data []byte) bool {
	files, err := unpackSkill(data)
	if err != nil {
		return true
	}
	for _, f := range files {
		if f.Path != "SKILL.md" {
			continue
		}
		if _, err := parseSkillMD(f.Data); err != nil {
			return true
		}
		front, _, _ := strings.Cut(strings.TrimPrefix(strings.ReplaceAll(string(f.Data), "\r\n", "\n"), "---\n"), "\n---")
		for _, line := range strings.Split(front, "\n") {
			k, _, ok := strings.Cut(line, ":")
			if ok && line != "" && line[0] != ' ' && line[0] != '\t' && slices.Contains(oneThreadSkillsKeys, strings.TrimSpace(k)) {
				return true
			}
		}
		return false
	}
	return true
}

func distinct(tx *sql.Tx, q string, args ...any) ([]string, error) {
	rows, err := tx.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// convertOneThreadMemory converts memory.db, once, after core.db: the
// matters with memories and no thread become ended threads (Q7), then
// what the memories and intentions are about is kept under its target.
func (c *core) convertOneThreadMemory() {
	if c.mem == nil {
		return
	}
	var core, done string
	if c.store.db.QueryRow(`SELECT value FROM kv WHERE key = ?`, oneThreadFlag).Scan(&core) != nil {
		log.Printf("one-thread conversion of memory.db: waits for core.db's")
		return
	}
	if c.mem.db.QueryRow(`SELECT value FROM kv WHERE key = ?`, oneThreadFlag).Scan(&done) == nil {
		return
	}
	if core == oneThreadFresh {
		c.mem.db.Exec(`INSERT OR IGNORE INTO kv (key, value) VALUES (?, ?)`, oneThreadFlag, oneThreadFresh)
		return
	}
	n, err := c.convertOneThreadMem(time.Now())
	if err != nil {
		log.Printf("one-thread conversion of memory.db: %v (tried again at the next start)", err)
		return
	}
	log.Printf("one-thread conversion of memory.db: %d ended threads for matters with memories, %d memories and %d intention fields rekeyed, %d dropped (mail, event)",
		n.threads, n.memories, n.intentions, n.dropped)
}

type oneThreadMemCounts struct{ threads, memories, intentions, dropped int }

func (c *core) convertOneThreadMem(now time.Time) (n oneThreadMemCounts, err error) {
	// What the memories are about, and when.
	type span struct{ first, last int64 }
	matters := map[string]span{}
	known := map[string]bool{} // every key they are about: each is in the names
	rows, err := c.mem.db.Query(`SELECT about, MIN(recorded_at), MAX(recorded_at) FROM memories WHERE about != '' GROUP BY about`)
	if err != nil {
		return n, err
	}
	for rows.Next() {
		var about string
		var first, last int64
		if err := rows.Scan(&about, &first, &last); err != nil {
			rows.Close()
			return n, err
		}
		k, drop := oneThreadAbout(about)
		if !drop && isKeyName(k) {
			k = c.store.resolveAbout(k) // a merged subject: the one it went into
		}
		if drop || !isKeyName(k) || strings.HasPrefix(k, refThread+":") {
			continue
		}
		if known[k] = true; oneThreadEntity(k) {
			continue
		}
		s, ok := matters[k]
		if !ok || first < s.first {
			s.first = first
		}
		s.last = max(s.last, last)
		matters[k] = s
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return n, err
	}
	for _, col := range []string{"about", "owner", "owed_to"} {
		vals, err := memDistinct(c.mem.db, `SELECT DISTINCT `+col+` FROM intentions WHERE `+col+` != ''`)
		if err != nil {
			return n, err
		}
		for _, v := range vals {
			if k, drop := oneThreadAbout(v); !drop && isKeyName(k) && !strings.HasPrefix(k, refThread+":") {
				known[k] = true
			}
		}
	}
	watched := c.oneThreadWatched()
	// Q7: a matter no thread follows becomes an ended thread, tidied away:
	// a candidate when the matter comes back. A key a skill or a monitor
	// keeps is left to its watcher, which opens its standing thread.
	tx, err := c.store.db.Begin()
	if err != nil {
		return n, err
	}
	defer tx.Rollback()
	keys := make([]string, 0, len(matters))
	for k := range matters {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		if watched[k] {
			continue
		}
		title := oneThreadTitle(tx, k)
		s := matters[k]
		res, err := tx.Exec(`INSERT INTO threads (title, goal, mode, state, ends, created, started, ended, tidied) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			title, title, modeLight, threadDone, endsGoal, s.first, s.first, s.last, now.Unix())
		if err != nil {
			return n, err
		}
		id, _ := res.LastInsertId()
		to := threadRef(id)
		// The names it had follow; its key names the thread.
		if _, err := tx.Exec(`UPDATE OR IGNORE names SET target = ? WHERE target = ?`, to, k); err != nil {
			return n, err
		}
		if _, err := tx.Exec(`DELETE FROM names WHERE target = ?`, k); err != nil {
			return n, err
		}
		if _, err := tx.Exec(`INSERT OR REPLACE INTO names (name, target, seen) VALUES (?, ?, ?)`, k, to, s.last); err != nil {
			return n, err
		}
		for _, table := range aboutTables {
			if _, err := tx.Exec(`UPDATE `+table+` SET about = ? WHERE about = ?`, to, k); err != nil {
				return n, err
			}
		}
		if err := rematchTopicRules(tx, k, to); err != nil {
			return n, err
		}
		if _, err := tx.Exec(`INSERT INTO thread_log (thread, at, who, text) VALUES (?, ?, ?, ?)`, id, now.Unix(), "engine",
			"Fil terminé créé à la mise à jour : des souvenirs portaient sur "+k+"."); err != nil {
			return n, err
		}
		n.threads++
		delete(known, k)
	}
	for k := range known {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO names (name, target, seen) VALUES (?, ?, ?)`, k, k, now.Unix()); err != nil {
			return n, err
		}
	}
	if err := tx.Commit(); err != nil {
		return n, err
	}
	// The memories and intentions, under their target.
	resolve := func(v string) (string, bool) {
		k, drop := oneThreadAbout(v)
		if drop {
			return "", true
		}
		if !isKeyName(k) {
			return k, false
		}
		return c.store.resolveAbout(k), false
	}
	type mem struct {
		id                    int64
		about, text, keywords string
	}
	var ms []mem
	rows, err = c.mem.db.Query(`SELECT id, about, text, keywords FROM memories WHERE about != '' AND about NOT LIKE 'thread:%'`)
	if err != nil {
		return n, err
	}
	for rows.Next() {
		var x mem
		if err := rows.Scan(&x.id, &x.about, &x.text, &x.keywords); err != nil {
			rows.Close()
			return n, err
		}
		ms = append(ms, x)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return n, err
	}
	type field struct{ col, from, to string }
	var fields []field
	for _, col := range []string{"about", "owner", "owed_to"} {
		vals, err := memDistinct(c.mem.db, `SELECT DISTINCT `+col+` FROM intentions WHERE `+col+` != '' AND `+col+` NOT LIKE 'thread:%'`)
		if err != nil {
			return n, err
		}
		for _, v := range vals {
			to, drop := resolve(v)
			if to != v && !(drop && col == "owner") {
				fields = append(fields, field{col, v, to})
			}
		}
	}
	mtx, err := c.mem.db.Begin()
	if err != nil {
		return n, err
	}
	defer mtx.Rollback()
	for _, x := range ms {
		to, drop := resolve(x.about)
		if to == x.about {
			continue
		}
		if _, err := mtx.Exec(`UPDATE memories SET about = ?, stems = ? WHERE id = ?`, to, memoryStems(x.text, to, x.keywords), x.id); err != nil {
			return n, err
		}
		if drop {
			n.dropped++
		} else {
			n.memories++
		}
	}
	for _, f := range fields {
		res, err := mtx.Exec(`UPDATE intentions SET `+f.col+` = ? WHERE `+f.col+` = ?`, f.to, f.from)
		if err != nil {
			return n, err
		}
		k, _ := res.RowsAffected()
		if f.to == "" {
			n.dropped += int(k)
		} else {
			n.intentions += int(k)
		}
	}
	if _, err := mtx.Exec(`INSERT INTO memories_fts (memories_fts) VALUES ('rebuild')`); err != nil {
		return n, err
	}
	b, _ := json.Marshal(map[string]any{"at": now.Unix(), "threads": n.threads, "memories": n.memories, "intentions": n.intentions, "dropped": n.dropped})
	if _, err := mtx.Exec(`INSERT INTO kv (key, value) VALUES (?, ?)`, oneThreadFlag, string(b)); err != nil {
		return n, err
	}
	return n, mtx.Commit()
}

func memDistinct(db *sql.DB, q string) ([]string, error) {
	rows, err := db.Query(q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// oneThreadWatched are the keys a stored skill's runs or a monitor keep:
// a skill's thread, or its events' key; a monitor's topic.
func (c *core) oneThreadWatched() map[string]bool {
	out := map[string]bool{}
	add := func(s string) {
		if k, err := aliasKey(s); err == nil && k != "" {
			out[k] = true
		}
	}
	if ms, err := c.monitors(); err == nil {
		for _, m := range ms {
			add(m.Name)
		}
	}
	ks, err := c.store.skills()
	if err != nil {
		return out
	}
	for _, k := range ks {
		for _, sha := range []string{k.Approved, k.Pending} {
			if sha == "" {
				continue
			}
			v, err := c.store.skillVersion(sha)
			if err != nil || v.Manifest.Trigger == nil {
				continue
			}
			if v.Manifest.Trigger.Thread != "" {
				add(v.Manifest.Trigger.Thread)
			} else if key := v.Manifest.Trigger.Filters["key"]; key != "" {
				add(key)
			}
		}
	}
	return out
}

// oneThreadTitle is a matter's title: a name the user gave it, else its
// key's words (project:site-web → Site web).
func oneThreadTitle(tx *sql.Tx, key string) string {
	var name string
	if tx.QueryRow(`SELECT name FROM names WHERE target = ? AND instr(name, ':') = 0 AND name NOT IN ('owner', 'home') ORDER BY seen DESC LIMIT 1`, key).Scan(&name) == nil && name != "" {
		return name
	}
	_, slug, _ := strings.Cut(key, ":")
	t := []rune(strings.ReplaceAll(slug, "-", " "))
	if len(t) == 0 {
		return key
	}
	t[0] = unicode.ToUpper(t[0])
	return truncate(string(t), surfaceTitleMax)
}
