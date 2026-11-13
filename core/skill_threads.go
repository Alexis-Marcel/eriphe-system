package main

import (
	"log"
	"regexp"
	"slices"
	"strings"
	"time"
)

// Skills written when threads were work sessions (before #465) name them
// by their old words: can: [start-session], when: session.ended, and a
// ```session block in their instructions. The same capability has a new
// name, so the user's approval stands: each such version is rewritten
// mechanically, in SKILL.md only and in those words only, into a new
// bundle that takes its place. The approved version replaced is kept as
// any replaced version, and the change is a learning entry the user sees
// and can undo (Settings → What Eriphe learned).

// skillRenamedReason is the learning entry's reason (web_text_learned.go).
const skillRenamedReason = "renamed: session → thread"

var (
	frontCanRE   = regexp.MustCompile(`\bstart-session\b`)
	frontWhenRE  = regexp.MustCompile(`\bsession\.ended\b`)
	bodyBlockRE  = regexp.MustCompile("^(\\s*)```session\\b")
	frontKeyLine = regexp.MustCompile(`^([A-Za-z_-]+)\s*:`)
)

// renameSessionMD rewrites a SKILL.md's old words; changed says whether
// anything did. Only the front matter's can and when, and a ```session
// fence in the body, are touched: every other byte stays.
func renameSessionMD(md string) (string, bool) {
	lines := strings.Split(md, "\n")
	if len(lines) == 0 || strings.TrimSpace(strings.TrimPrefix(lines[0], "\uFEFF")) != "---" {
		return md, false
	}
	front, key := true, ""
	for i := 1; i < len(lines); i++ {
		line := lines[i]
		if front && strings.TrimRight(line, "\r") == "---" {
			front = false
			continue
		}
		if !front {
			lines[i] = bodyBlockRE.ReplaceAllString(line, "${1}```thread")
			continue
		}
		if m := frontKeyLine.FindStringSubmatch(line); m != nil {
			key = m[1]
		}
		switch key {
		case "can":
			lines[i] = frontCanRE.ReplaceAllString(line, "open-thread")
		case "when":
			lines[i] = frontWhenRE.ReplaceAllString(line, "thread.ended")
		}
	}
	if front {
		return md, false // no end to the front matter
	}
	out := strings.Join(lines, "\n")
	return out, out != md
}

// renameSessionBundle is a bundle with its SKILL.md rewritten, and its
// hash; ok is false when nothing changes.
func renameSessionBundle(data []byte) ([]byte, string, bool) {
	files, err := unpackSkill(data)
	if err != nil {
		return nil, "", false
	}
	changed := false
	for i, f := range files {
		if f.Path != "SKILL.md" {
			continue
		}
		if md, ok := renameSessionMD(string(f.Data)); ok {
			files[i].Data, changed = []byte(md), true
		}
	}
	if !changed {
		return nil, "", false
	}
	out, sha, err := packSkill(files)
	if err != nil {
		return nil, "", false
	}
	return out, sha, true
}

// renameSessionSkills rewrites the skills' approved and pending versions
// that still say session; each approved one changed is a learning entry.
func (c *core) renameSessionSkills() {
	all, err := c.store.skills()
	if err != nil {
		return
	}
	for _, k := range all {
		approved, pending := k.Approved, k.Pending
		newApproved, newPending := approved, pending
		for _, v := range []*string{&newApproved, &newPending} {
			if *v == "" {
				continue
			}
			data, err := c.store.skillBundle(*v)
			if err != nil {
				continue
			}
			if out, sha, ok := renameSessionBundle(data); ok {
				if _, err := c.store.db.Exec(`INSERT OR IGNORE INTO skill_bundles (sha, data, time) VALUES (?, ?, ?)`, sha, out, time.Now().Unix()); err != nil {
					log.Printf("skill %s: %v", k.Name, err)
					return
				}
				*v = sha
			}
		}
		if newApproved == approved && newPending == pending {
			continue
		}
		if err := c.store.renameSkillVersions(k.Name, approved, newApproved, pending, newPending); err != nil {
			log.Printf("skill %s: renamed: %v", k.Name, err)
			continue
		}
		c.shippedRenamed(k.Name, approved, newApproved)
		c.shippedRenamed(k.Name, pending, newPending)
		if newApproved != approved {
			c.learnSkill(k.Name, newApproved, approved, "eriphe", skillRenamedReason)
		}
		log.Printf("skill %s: session renamed thread in its words", k.Name)
	}
}

// shippedRenamed records the rewritten version of a default skill's
// shipped version as shipped too, right after it, so a later update still
// sees the skill unchanged and the version offered last stays the last.
func (c *core) shippedRenamed(name, sha, renamed string) {
	xs := c.shippedVersions(name)
	i := slices.Index(xs, sha)
	if sha == renamed || i < 0 || slices.Contains(xs, renamed) {
		return
	}
	c.store.set(defaultSkillSHAKey+name, strings.Join(slices.Insert(xs, i+1, renamed), " "))
}

// renameSkillVersions puts a skill's rewritten versions in place of the
// old ones, if it did not change meanwhile; the approved one replaced is
// kept as any replaced version.
func (s *Store) renameSkillVersions(name, approved, newApproved, pending, newPending string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if newPending == newApproved {
		newPending = ""
	}
	now := time.Now().Unix()
	res, err := tx.Exec(`UPDATE skills SET approved = ?, pending = ?, updated = ? WHERE name = ? AND approved = ? AND pending = ?`,
		newApproved, newPending, now, name, approved, pending)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errNoSkill
	}
	if newApproved != approved {
		if err := keepSkillVersion(tx, name, approved); err != nil {
			return err
		}
	}
	if err := gcSkillBundles(tx); err != nil {
		return err
	}
	return tx.Commit()
}
