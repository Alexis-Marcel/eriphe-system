package main

// TO DELETE with convert_one_thread.go.

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
)

// oldInstall makes core.db and memory.db as an install made before #578
// left them, once their columns were renamed: subjects, threads.subject,
// abouts written as keys, skills of before.
func oldInstall(t *testing.T) (core, memory string) {
	t.Helper()
	dir := t.TempDir()
	core, memory = filepath.Join(dir, "core.db"), filepath.Join(dir, "memory.db")
	s, err := openStore(core)
	if err != nil {
		t.Fatal(err)
	}
	if s.get(oneThreadFlag) != oneThreadFresh {
		t.Fatalf("a new core.db: flag %q, want %q", s.get(oneThreadFlag), oneThreadFresh)
	}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := s.db.Exec(q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	exec(`DELETE FROM kv WHERE key = ?`, oneThreadFlag)
	exec(`ALTER TABLE threads ADD COLUMN subject TEXT NOT NULL DEFAULT ''`)
	exec(`CREATE UNIQUE INDEX threads_live_subject ON threads (subject) WHERE subject != '' AND state IN ('open', 'waiting')`)
	exec(`CREATE TABLE subjects (key TEXT PRIMARY KEY, title TEXT NOT NULL DEFAULT '', aliases TEXT NOT NULL DEFAULT '[]',
		merged_into TEXT NOT NULL DEFAULT '', first_seen INTEGER NOT NULL, last_seen INTEGER NOT NULL)`)
	exec(`CREATE TABLE thread_monitors (thread INTEGER, monitor TEXT)`)
	for _, th := range []struct {
		id             int64
		state, subject string
	}{{1, threadDone, "pr:example/app#3"}, {2, threadDone, "PR:example/app#3"}, {3, threadOpen, "projet:Site Web"}} {
		exec(`INSERT INTO threads (id, title, goal, state, created, subject) VALUES (?, ?, ?, ?, ?, ?)`, th.id, "Fil", "But", th.state, 1000+th.id, th.subject)
	}
	for _, sub := range [][]string{
		{"project:site-web", "Site web", `["le site", "Site Web"]`, ""},
		{"project:compta", "Compta", `[]`, ""},
		{"topic:factures", "Factures", `[]`, "project:compta"},
		{"person:sam", "Sam", `[]`, ""},
		{"event:abc", "", `[]`, ""},
	} {
		exec(`INSERT INTO subjects (key, title, aliases, merged_into, first_seen, last_seen) VALUES (?, ?, ?, ?, 1, 2)`, sub[0], sub[1], sub[2], sub[3])
	}
	for _, task := range [][]string{{"t-1", "pr:example/app#3"}, {"t-2", "mail:xyz"}, {"t-3", "topic:factures"}, {"t-4", "topic:mails"}} {
		s.insertTask(&TaskRow{ID: task[0], Kind: "worker", Runtime: "claude-code", State: stateIdle, Prompt: "p"})
		exec(`UPDATE tasks SET about = ? WHERE id = ?`, task[1], task[0])
	}
	b, _ := json.Marshal(notifySpec{Kind: ruleTopic, Match: "topic:factures", Floor: 4})
	exec(`INSERT INTO rules (name, source, spec, prompt, state, by, created, updated) VALUES ('factures', ?, ?, '', 'active', 'user', 1, 1)`, sourceNotification, string(b))
	exec(`INSERT INTO kv (key, value) VALUES ('subject_keys', '1'), ('subjects_filled', '1'), ('surface_missing:x', '1'), ('brain_turn', ?)`,
		`{"from":1,"subjects":["projet:site-web","mail:xyz"]}`)
	// Skills: the default one, one of before, one that no longer reads, one of now.
	addSkill := func(name, source, md string) {
		t.Helper()
		data, sha, err := packSkill([]skillFile{{Path: "SKILL.md", Data: []byte(md)}})
		if err != nil {
			t.Fatal(err)
		}
		exec(`INSERT INTO skill_bundles (sha, data, time) VALUES (?, ?, 1)`, sha, data)
		exec(`INSERT INTO skills (name, approved, source, approved_at, updated) VALUES (?, ?, ?, 1, 1)`, name, sha, source)
	}
	addSkill("arrivee", "default", "---\nname: arrivee\ndescription: Ce que Sam voit en arrivant.\n---\nBonjour.\n")
	addSkill("alertes", skillFromEriphe, "---\nname: alertes\ndescription: Les alertes de la prod.\nwhen: monitor.changed key=prod\ntools: [grafana]\ncan: [open-thread]\n---\nVérifie.\n")
	addSkill("tableau", skillFromEriphe, "---\nname: tableau\ndescription: Un tableau.\nwhen: monitor.new key=prod\ntools: [eriphe-workspace]\n---\nÉcris.\n")
	addSkill("mails", skillFromEriphe, "---\nname: mails\ndescription: Les mails.\nwhen: monitor.new key=mails\ntools: [mail]\nthread: topic:mails\n---\nNote chaque mail.\n")
	s.db.Close()

	m, err := openMemory(memory)
	if err != nil {
		t.Fatal(err)
	}
	for _, x := range [][]string{
		{"project:jardin", "Les tomates sont plantées."},
		{"topic:factures", "La facture d'octobre est payée."},
		{"topic:mails", "Sam lit ses mails le matin."},
		{"person:sam", "Sam préfère le thé."},
		{"event:abc", "La réunion est à 10 h."},
		{"pr:example/app#3", "La PR attend une relecture."},
	} {
		if _, err := m.db.Exec(`INSERT INTO memories (kind, about, text, valid_from, recorded_at, source_kind, trust, status, updated_at, stems)
			VALUES ('fact', ?, ?, 10, 10, 'owner', 'owner', 'active', 10, ?)`, x[0], x[1], memoryStems(x[1], x[0], "")); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := m.db.Exec(`INSERT INTO intentions (kind, text, owner, owed_to, about, state, source_kind, trust, created_at, updated_at)
		VALUES ('promise', 'Envoyer la maquette.', 'owner', 'person:sam', 'project:site-web', 'open', 'owner', 'owner', 1, 1)`); err != nil {
		t.Fatal(err)
	}
	m.db.Close()
	return core, memory
}

// openInstall opens both databases as eriphe-core does.
func openInstall(t *testing.T, core, memory string) *core {
	t.Helper()
	s, err := openStore(core)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.db.Close() })
	c := newCore(&Config{User: "Sam", Listen: "127.0.0.1:8800", DB: core}, s)
	m, err := openMemory(memory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.db.Close() })
	c.useMemory(m)
	return c
}

// An install made before #578 is converted once: the subjects become the
// names, abouts are kept under their target, a matter with memories and
// no thread an ended thread, the old skills off with why.
func TestConvertOneThread(t *testing.T) {
	core, memory := oldInstall(t)
	c := openInstall(t, core, memory)

	one := func(q string, args ...any) string {
		t.Helper()
		var v string
		if err := c.store.db.QueryRow(q, args...).Scan(&v); err != nil {
			t.Fatalf("%s %v: %v", q, args, err)
		}
		return v
	}
	got := names(t, c)
	compta, jardin := got["project:compta"], got["project:jardin"]
	for k, want := range map[string]string{"pr:example/app#3": "thread:2", "project:site-web": "thread:3", "le site": "thread:3",
		"Site web": "thread:3", "topic:facture": compta, "person:sam": "person:sam", "topic:mail": "topic:mail"} {
		if got[k] != want {
			t.Errorf("names: %s → %q, want %q", k, got[k], want)
		}
	}
	if _, ok := got["event:abc"]; ok {
		t.Error("a mail or an event is no name")
	}
	// Q7: compta and jardin have memories and no thread: ended, tidied.
	for key, ref := range map[string]string{"project:compta": compta, "project:jardin": jardin} {
		id, ok := threadOfAbout(ref)
		if !ok {
			t.Fatalf("%s → %q, want a thread", key, ref)
		}
		th, err := c.thread(id)
		if err != nil || th.active() || th.Ends != endsGoal || th.tidied == 0 || th.Alias != key {
			t.Errorf("%s: thread %+v (%v), want an ended goal thread, tidied, its alias", key, th, err)
		}
	}
	if th, _ := c.thread(mustThread(t, compta)); th.Title != "Compta" {
		t.Errorf("a subject's title is its thread's: %q", th.Title)
	}
	if th, _ := c.thread(mustThread(t, jardin)); th.Title != "Jardin" {
		t.Errorf("a key's words are its thread's title: %q", th.Title)
	}
	if n := one(`SELECT COUNT(*) FROM threads`); n != "5" {
		t.Errorf("%s threads, want 3 and 2 ended (topic:mails is a skill's: its watcher opens it)", n)
	}
	// The about columns, the rules, the brain's turn.
	for id, want := range map[string]string{"t-1": "thread:2", "t-2": "", "t-3": compta, "t-4": "topic:mail"} {
		if a := one(`SELECT about FROM tasks WHERE id = ?`, id); a != want {
			t.Errorf("task %s about %q, want %q", id, a, want)
		}
	}
	var sp notifySpec
	json.Unmarshal([]byte(one(`SELECT spec FROM rules`)), &sp)
	if sp.Match != compta || sp.Floor != 4 {
		t.Errorf("rule %+v, want its match %s", sp, compta)
	}
	if turn := c.store.get("brain_turn"); strings.Contains(turn, "subjects") || !strings.Contains(turn, `"named":["thread:3"]`) {
		t.Errorf("brain_turn %s", turn)
	}
	// What went.
	for _, q := range []string{
		`SELECT COUNT(*) FROM sqlite_master WHERE name IN ('subjects', 'thread_monitors', 'threads_live_subject')`,
		`SELECT COUNT(*) FROM pragma_table_info('threads') WHERE name = 'subject'`,
		`SELECT COUNT(*) FROM kv WHERE key IN ('subject_keys', 'subjects_filled') OR key LIKE 'surface_missing:%'`,
		`SELECT COUNT(*) FROM skills WHERE name = 'arrivee'`,
	} {
		if n := one(q); n != "0" {
			t.Errorf("%s: %s", q, n)
		}
	}
	// The skills of before are off, and say why; the one of now runs.
	ks, err := c.store.skills()
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range ks {
		off := k.Name == "alertes" || k.Name == "tableau"
		if k.Enabled == off || (k.OffWhy == skillsOffOld) != off {
			t.Errorf("skill %s: enabled %v, off_why %q", k.Name, k.Enabled, k.OffWhy)
		}
		if (k.Broken != nil) != (k.Name == "tableau") {
			t.Errorf("skill %s: broken %v", k.Name, k.Broken)
		}
	}
	// The memory: under the threads, an event dropped, the index rebuilt.
	m := c.mem
	for text, want := range map[string]string{"Les tomates sont plantées.": jardin, "La facture d'octobre est payée.": compta,
		"Sam lit ses mails le matin.": "topic:mail", "Sam préfère le thé.": "person:sam", "La réunion est à 10 h.": "",
		"La PR attend une relecture.": "thread:2"} {
		var about, stems string
		m.db.QueryRow(`SELECT about, stems FROM memories WHERE text = ?`, text).Scan(&about, &stems)
		if about != want || stems != memoryStems(text, want, "") {
			t.Errorf("memory %q about %q, want %q (stems %q)", text, about, want, stems)
		}
	}
	if _, err := m.db.Exec(`INSERT INTO memories_fts (memories_fts) VALUES ('integrity-check')`); err != nil {
		t.Errorf("full-text index: %v", err)
	}
	var owner, owed, about string
	m.db.QueryRow(`SELECT owner, owed_to, about FROM intentions`).Scan(&owner, &owed, &about)
	if owner != "owner" || owed != "person:sam" || about != "thread:3" {
		t.Errorf("intention owner %q owed_to %q about %q", owner, owed, about)
	}

	// Once: opened again, nothing changes.
	before, threads := names(t, c), one(`SELECT COUNT(*) FROM threads`)
	c.store.db.Close()
	m.db.Close()
	c = openInstall(t, core, memory)
	if after := names(t, c); len(after) != len(before) || one(`SELECT COUNT(*) FROM threads`) != threads {
		t.Errorf("a second start changed it: %d names (was %d), %s threads (was %s)", len(after), len(before), one(`SELECT COUNT(*) FROM threads`), threads)
	}

	// Approving a new version turns an old skill back on.
	if _, _, err := c.store.addSkillVersion([]skillFile{{Path: "SKILL.md", Data: []byte("---\nname: alertes\ndescription: Les alertes de la prod.\nwhen: monitor.changed key=prod\ntools: [grafana]\n---\nNote chaque alerte.\n")}}, skillFromEriphe); err != nil {
		t.Fatal(err)
	}
	k, _ := c.store.skill("alertes")
	if err := c.store.approveSkill("alertes", k.Pending); err != nil {
		t.Fatal(err)
	}
	if k, _ = c.store.skill("alertes"); !k.Enabled || k.OffWhy != "" {
		t.Errorf("approved anew: enabled %v, off_why %q", k.Enabled, k.OffWhy)
	}
}

func mustThread(t *testing.T, ref string) int64 {
	t.Helper()
	id, ok := threadOfAbout(ref)
	if !ok {
		t.Fatalf("%q is no thread", ref)
	}
	return id
}

// A skill whose version no longer reads has its page still, with why.
func TestSkillPageUnreadable(t *testing.T) {
	core, memory := oldInstall(t)
	c := openInstall(t, core, memory)
	tw := newTestWebFor(t, c)
	tw.setup()
	code, body, _ := tw.do("GET", "/settings/skills/tableau", nil, nil)
	if code != http.StatusOK || !strings.Contains(body, "chaque exécution écrit dans son nœud") || !strings.Contains(body, "ancien Eriphe") {
		t.Fatalf("GET a skill that does not read: %d", code)
	}
	if code, body, _ = tw.do("GET", "/settings/skills", nil, nil); code != http.StatusOK || strings.Count(body, "ancien Eriphe") != 2 {
		t.Fatalf("GET the skills: %d, %d said off", code, strings.Count(body, "ancien Eriphe"))
	}
}
