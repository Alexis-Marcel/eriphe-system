package main

import (
	"strings"
	"testing"
	"time"
)

// An old skill's SKILL.md: its can, its when and its fence say session.
const oldSessionSkill = "---\r\nname: prod\r\ndescription: la prod\r\nwhen: monitor.received, session.ended key=getout\r\n" +
	"can:\r\n  - start-session\r\n---\r\nEnquête. Pour un travail long, termine par :\r\n```session\r\n{\"title\": \"x\"}\r\n```\r\n" +
	"Le mot start-session ici reste tel quel.\r\n"

// putOldBundle keeps a bundle as an older eriphe-core did, unchecked.
func putOldBundle(t *testing.T, c *core, files []skillFile) string {
	t.Helper()
	data, sha, err := packSkill(files)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.store.db.Exec(`INSERT INTO skill_bundles (sha, data, time) VALUES (?, ?, ?)`, sha, data, time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	return sha
}

// A skill that says session is rewritten in those words only, in a new
// version that takes the approved one's place; the old one is kept, the
// change is a learning entry, and undoing it puts the old one back.
// Run again, nothing changes.
func TestSessionSkillsRenamed(t *testing.T) {
	c, _ := testMemoryCore(t)
	ref := skillFile{Path: "references/notes.md", Data: []byte("can: [start-session]\n```session\n")}
	old := putOldBundle(t, c, []skillFile{{Path: "SKILL.md", Data: []byte(oldSessionSkill)}, ref})
	pend := putOldBundle(t, c, []skillFile{{Path: "SKILL.md", Data: []byte(strings.Replace(oldSessionSkill, "Enquête.", "Enquête vite.", 1))}, ref})
	c.store.db.Exec(`INSERT INTO skills (name, approved, pending, approved_at, updated) VALUES ('prod', ?, ?, 1, 1)`, old, pend)
	if _, err := c.store.skillVersion(old); err == nil {
		t.Fatal("the old version parses: the test proves nothing")
	}

	c.renameSessionSkills()
	k, err := c.store.skill("prod")
	if err != nil || k.Approved == old || k.Pending == pend || k.Pending == "" {
		t.Fatalf("skill %+v %v", k, err)
	}
	v, err := c.store.skillVersion(k.Approved)
	if err != nil || v.Manifest.Trigger == nil || !containsStr(v.Manifest.Trigger.Can, canThread) {
		t.Fatalf("the new version %+v %v", v, err)
	}
	files, _ := unpackSkill(mustBundle(t, c, k.Approved))
	want := strings.NewReplacer("start-session\r\n---", "open-thread\r\n---", "session.ended", "thread.ended", "```session", "```thread").Replace(oldSessionSkill)
	if len(files) != 2 || string(files[0].Data) != want || string(files[1].Data) != string(ref.Data) {
		t.Fatalf("files:\n%q\nwant\n%q", files[0].Data, want)
	}
	if !strings.Contains(string(files[0].Data), "Le mot start-session ici reste tel quel.") {
		t.Error("a word of the body changed")
	}
	if _, err := c.store.skillVersion(k.Pending); err != nil {
		t.Errorf("the pending version: %v", err)
	}
	e := learnedAll(t, c.mem)[0]
	if e.Kind != kindSkill || e.Skill != "prod" || e.Version != k.Approved || e.PrevVersion != old || e.Reason != skillRenamedReason || e.Action != learnChanged {
		t.Fatalf("learned %+v", e)
	}
	if got := len(learnedAll(t, c.mem)); got != 1 {
		t.Errorf("%d entries", got)
	}

	// Again: nothing to do.
	c.renameSessionSkills()
	if k2, _ := c.store.skill("prod"); k2.Approved != k.Approved || len(learnedAll(t, c.mem)) != 1 {
		t.Errorf("renamed twice: %+v", k2)
	}

	// The user undoes it: the old version is back, as it was.
	if _, err := c.unlearn(e.ID, "owner", "", nil); err != nil {
		t.Fatal(err)
	}
	if k, _ = c.store.skill("prod"); k.Approved != old {
		t.Fatalf("undone: %+v", k)
	}
}

func mustBundle(t *testing.T, c *core, sha string) []byte {
	t.Helper()
	b, err := c.store.skillBundle(sha)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// Only can and when are read in the front matter; a description that
// says start-session keeps it.
func TestRenameSessionMDOnlyItsWords(t *testing.T) {
	md := "---\nname: x\ndescription: replaces start-session and session.ended\nwhen: time daily 08:00\n---\nbody\n"
	if out, changed := renameSessionMD(md); changed || out != md {
		t.Errorf("changed: %q", out)
	}
	if _, changed := renameSessionMD("no front matter\n```session\n"); changed {
		t.Error("a file without front matter changed")
	}
}

// A default skill renamed stays a version Eriphe shipped: its new hash is
// recorded right after the old one, so the next release still updates it,
// and shipping the renamed version itself changes nothing.
func TestRenamedDefaultSkillStaysShipped(t *testing.T) {
	c, _ := testMemoryCore(t)
	md := func(when string) map[string][]skillFile {
		return map[string][]skillFile{"matin": {{Path: "SKILL.md", Data: []byte("---\nname: matin\ndescription: Le matin.\nwhen: " + when + "\n---\n\nPrépare la journée.\n")}}}
	}
	old := putOldBundle(t, c, md("session.ended")["matin"])
	c.store.db.Exec(`INSERT INTO skills (name, approved, source, approved_at, updated) VALUES ('matin', ?, ?, 1, 1)`, old, skillFromDefault)
	c.store.set(defaultSkillKey+"matin", "1")
	c.store.set(defaultSkillSHAKey+"matin", "earlier "+old+" offered")

	c.renameSessionSkills()
	renamed := md("thread.ended")
	k, _ := c.store.skill("matin")
	if k.Approved != shippedSHA(t, renamed) {
		t.Fatalf("not renamed: %+v", k)
	}
	if got, want := c.store.get(defaultSkillSHAKey+"matin"), "earlier "+old+" "+k.Approved+" offered"; got != want {
		t.Fatalf("shipped %q, want %q", got, want)
	}
	c.renameSessionSkills()
	if got := strings.Fields(c.store.get(defaultSkillSHAKey + "matin")); len(got) != 4 {
		t.Fatalf("recorded twice: %v", got)
	}

	id, _ := lastSkillEntry(c, "matin")
	c.installShippedSkills(renamed)
	if n, _ := lastSkillEntry(c, "matin"); n != id {
		t.Fatal("the renamed version updated again")
	}
	next := md("thread.ended, monitor.received")
	c.installShippedSkills(next)
	if k, _ := c.store.skill("matin"); k.Approved != shippedSHA(t, next) {
		t.Fatalf("the next release not installed: %+v", k)
	}
	if _, reason := lastSkillEntry(c, "matin"); reason != defaultSkillUpdated {
		t.Fatalf("journal %q", reason)
	}
}
