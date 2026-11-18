package main

import (
	"database/sql"
	"strconv"
	"strings"
)

// What a record is about. Notifications, attention items, cards, tasks,
// surfaces, memories and intentions carry an about next to their ref
// (which record): a thread, thread:<id>, or an entity's key (person:lina,
// owner, topic:workspace). The code sets it by inheritance: what the brain
// makes in a turn takes the turn's (turnAbout: its thread), a task its
// thread's or its skill's, a card or a surface its maker's (sourceAbout),
// a task's memories its own (the extraction). The model may give one; it
// no longer has to. A key given that is a thread's alias is kept as that
// thread (names.go).

// aboutTables are the core.db tables that carry an about column.
var aboutTables = []string{"notifications", "attention", "convo_cards", "tasks", "surfaces", "card_pushes"}

// A table made before #573 says subject where it now says about: renamed
// as it is, its values converted by step 8 of #578.
func init() {
	schemaFixes = append(schemaFixes, func(db *sql.DB) error {
		for _, table := range aboutTables {
			has, err := columns(db, table)
			if err != nil {
				return err
			}
			if has["subject"] && !has["about"] {
				if _, err := db.Exec(`ALTER TABLE ` + table + ` RENAME COLUMN subject TO about`); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

// columns are a table's columns.
func columns(db *sql.DB, table string) (map[string]bool, error) {
	rows, err := db.Query(`SELECT name FROM pragma_table_info(?)`, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	has := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		has[name] = true
	}
	return has, rows.Err()
}

// turnAbout is what the brain's turn is about: its thread, else the one
// thread or entity my messages of the turn name; "" if neither.
func (c *core) turnAbout() string {
	if id := c.turn.aboutThread(); id > 0 {
		if _, err := c.thread(id); err == nil {
			return threadRef(id)
		}
	}
	if named := c.turn.named(); len(named) == 1 {
		return named[0]
	}
	return ""
}

// sourceAbout is what who made a record is about: a thread (thread:), a
// task's about; "" for anyone else.
func (c *core) sourceAbout(source string) string {
	kind, id, _ := strings.Cut(source, ":")
	switch kind {
	case "thread":
		n, err := strconv.ParseInt(id, 10, 64)
		if err != nil {
			return ""
		}
		if _, err := c.thread(n); err == nil {
			return c.store.resolveAbout(threadRef(n))
		}
	case "task":
		var s string
		c.store.db.QueryRow(`SELECT about FROM tasks WHERE id = ?`, id).Scan(&s)
		return s
	}
	return ""
}

// cardAbout sets a card's about once: its proposal's (a memory's, an
// intention's, a thread's, a skill run's), else what its maker inherits
// (nil: none).
func (c *core) cardAbout(kind string, id int64, inherit func() string) {
	s := c.proposalAbout(kind, id)
	if s == "" && inherit != nil {
		s = inherit()
	}
	if s == "" {
		return
	}
	c.store.db.Exec(`UPDATE convo_cards SET about = ? WHERE ref = ? AND about = ''`, c.store.noteAbout(s), proposalRef(kind, id))
}

// proposalAbout is what a card proposes is about, if anything.
func (c *core) proposalAbout(kind string, id int64) string {
	switch kind {
	case cardMemory:
		if c.mem != nil {
			if x, err := c.mem.memory(id); err == nil {
				return x.About
			}
		}
	case cardIntention:
		if c.mem != nil {
			if x, err := c.mem.intention(id); err == nil {
				return x.About
			}
		}
	case cardCalls:
		if asks := c.batchAsks(id); len(asks) > 0 {
			return c.sourceAbout("task:" + asks[0].TaskID)
		}
	case cardRunCalls:
		var task string
		c.store.db.QueryRow(`SELECT task FROM skill_runs WHERE id = ?`, id).Scan(&task)
		if task != "" {
			return c.sourceAbout("task:" + task)
		}
	}
	return ""
}

// skillRunAlias is what a skill's run is about: its events' key where it
// names a thing: a monitor's (monitor.*: topic:<name>), or a key written
// kind:slug. Another key (a task's, a thread's, a surface's id) gives
// none.
func skillRunAlias(key string, events []journalEvent) string {
	if strings.Contains(key, ":") {
		k, _ := aliasKey(key)
		return k
	}
	monitors := len(events) > 0
	for _, e := range events {
		monitors = monitors && strings.HasPrefix(e.Kind, "monitor.")
	}
	if monitors {
		k, _ := aliasKey(key)
		return k
	}
	return ""
}

// taskAbout is what a task is about, "" for none or no such task.
func (c *core) taskAbout(id string) string {
	if id == "" {
		return ""
	}
	return c.sourceAbout("task:" + id)
}

// inheritsAbout tells a kind of memory that takes the turn's about when it
// is given none: what is known and decided of the matter (where it stands
// is its thread's summary, brainProgress). A profile, a preference, a
// person, a lesson, a situation or the day's journal is about someone or
// something else.
func inheritsAbout(kind string) bool {
	return kind == kindFact || kind == kindDecision
}

// opAbout is what the extraction adds from a task is about (its first
// source with an about): a decision or a progress of the task is the
// task's about whatever was guessed, anything else takes it when none was
// given. Otherwise the one given. A replacement keeps its own.
func (c *core) opAbout(op extractionOp, ref string) string {
	var task string
	for _, p := range strings.Split(ref, ",") {
		if id, ok := strings.CutPrefix(p, "task:"); ok {
			if task = c.taskAbout(id); task != "" {
				break
			}
		}
	}
	switch {
	case task == "" || op.About == journalEntity:
		return op.About
	case op.Kind == kindDecision || op.Kind == kindProgress || strings.TrimSpace(op.About) == "":
		return task
	}
	return op.About
}
