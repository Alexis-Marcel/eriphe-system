package main

import (
	"embed"
	"fmt"
	"io/fs"
	"log"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Default skills (#427): skills Eriphe brings, installed once and then
// the user's like any other. Each is installed approved and on at the
// first start that has it, recorded in the learning journal (so the brain
// tells it, and "oublie ça" undoes it). A skill of the same name the user
// already has is left alone.
//
// A release may change a default skill (#476). At start, a default skill
// still exactly a version Eriphe shipped (its hash among those it
// installed or offered here, nothing pending, on) is updated to the
// shipped one, recorded in the journal as "updated by default", which
// undoes it. Removed, changed by the user or turned off, it stays as the
// user left it. Each shipped version is offered once: an update undone or
// refused is not offered again.
//
// A default skill names no tool (TestNoToolInDefaultSkills): it works from
// whatever is linked. It is lent only what grants nothing beyond the
// user's access policy (the workspace, whose writes are its own surfaces;
// the user's folders, each call held to their rights: folder_tools.go):
// a tool it should read itself is lent the normal way, by a new version
// the user approves on its card. So an update that adds one of those
// servers is installed as any update; one that asks for more (another
// tool, a can, an urgent text, a script) waits for the user's yes on the
// skill's card.

//go:embed default_skills
var defaultSkillFS embed.FS

const (
	defaultSkillKey     = "default_skill:"     // + name: when it was installed, unix
	defaultSkillSHAKey  = "default_skill_sha:" // + name: the shipped versions installed or offered, by hash
	skillFromDefault    = "default"            // a skills row's source: brought by Eriphe
	defaultSkillsDir    = "default_skills"
	defaultSkillReason  = "installed by default"
	defaultSkillUpdated = "updated by default"
)

// defaultSkills are the skills Eriphe brings, each its parts.
func defaultSkills() map[string][]skillFile {
	out := map[string][]skillFile{}
	entries, _ := fs.ReadDir(defaultSkillFS, defaultSkillsDir)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := path.Join(defaultSkillsDir, e.Name())
		var parts []skillFile
		fs.WalkDir(defaultSkillFS, dir, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			data, err := defaultSkillFS.ReadFile(p)
			if err == nil {
				parts = append(parts, skillFile{Path: p[len(dir)+1:], Data: data})
			}
			return err
		})
		out[e.Name()] = parts
	}
	return out
}

// installDefaultSkills installs the default skills not installed yet,
// and updates those the user has not changed.
func (c *core) installDefaultSkills() { c.installShippedSkills(defaultSkills()) }

func (c *core) installShippedSkills(shipped map[string][]skillFile) {
	for name, raw := range shipped {
		parts, sha, err := skillHash(raw)
		if err != nil {
			log.Printf("default skill %s: %v", name, err)
			continue
		}
		if c.store.get(defaultSkillKey+name) != "" {
			c.updateDefaultSkill(name, parts, sha)
			continue
		}
		mark := func() { c.store.set(defaultSkillKey+name, strconv.FormatInt(time.Now().Unix(), 10)) }
		if _, err := c.store.skill(name); err == nil {
			mark() // the user has one of that name
			continue
		}
		k, _, err := c.store.addSkillVersion(parts, skillFromDefault)
		if err != nil {
			log.Printf("default skill %s: %v", name, err)
			continue
		}
		prev, err := c.store.approveSkillVersion(k.Name, k.Pending)
		if err != nil {
			log.Printf("default skill %s: %v", name, err)
			continue
		}
		mark()
		c.shipped(name, sha)
		c.learnSkill(k.Name, k.Pending, prev, "eriphe", defaultSkillReason)
		log.Printf("default skill %s installed", k.Name)
	}
}

// shippedVersions are the hashes of the versions of a default skill
// installed or offered here.
func (c *core) shippedVersions(name string) []string {
	return strings.Fields(c.store.get(defaultSkillSHAKey + name))
}

// shipped records a version of a default skill as installed or offered,
// the last.
func (c *core) shipped(name, sha string) {
	xs := slices.DeleteFunc(c.shippedVersions(name), func(x string) bool { return x == sha })
	c.store.set(defaultSkillSHAKey+name, strings.Join(append(xs, sha), " "))
}

// updateDefaultSkill brings an installed default skill to the shipped
// version sha, if the user has not changed it and it was not offered yet.
func (c *core) updateDefaultSkill(name string, parts []skillFile, sha string) {
	k, err := c.store.skill(name)
	if err != nil {
		return // removed
	}
	offered := c.shippedVersions(name)
	if k.Approved == sha {
		c.shipped(name, sha) // up to date (approved on its card, say)
		return
	}
	if len(offered) > 0 && offered[len(offered)-1] == sha {
		return // offered last: undone, refused or waiting
	}
	// Installed before the versions were recorded: the one installed by
	// default, while nothing changed it since.
	legacy := len(offered) == 0 && k.Source == skillFromDefault
	if k.Approved == "" || k.Pending != "" || !k.Enabled || !(legacy || slices.Contains(offered, k.Approved)) {
		return // the user's now
	}
	old, err := c.store.skillVersion(k.Approved)
	if err != nil {
		log.Printf("default skill %s: %v", name, err)
		return
	}
	nk, _, err := c.store.addSkillVersion(parts, skillFromDefault)
	if err != nil {
		log.Printf("default skill %s: %v", name, err)
		return
	}
	if legacy {
		c.shipped(name, k.Approved)
	}
	c.shipped(name, sha)
	next, err := c.store.skillVersion(nk.Pending)
	if err != nil {
		log.Printf("default skill %s: %v", name, err)
		return
	}
	if more := askedMore(old, next); len(more) > 0 {
		text := fmt.Sprintf(tr(c.userLang(), "card.skill.default"), name, strings.Join(more, ", "))
		c.cardNow(cardSkill, c.skillRowID(name), text, "eriphe")
		log.Printf("default skill %s: new version proposed, it asks for %s", name, strings.Join(more, ", "))
		return
	}
	prev, err := c.store.approveSkillVersion(name, nk.Pending)
	if err != nil {
		log.Printf("default skill %s: %v", name, err)
		return
	}
	if old.Manifest.When != next.Manifest.When {
		c.store.set(skillFiredKey+name, "") // a time trigger starts from its next time
	}
	c.learnSkill(name, nk.Pending, prev, "eriphe", defaultSkillUpdated)
	log.Printf("default skill %s updated", name)
}

// askedMore is what a version asks for beyond the one before: a tool
// Eriphe does not provide, a can, an urgent text, a script. The servers
// Eriphe provides grant nothing beyond the user's access policy.
func askedMore(old, next *skillVersion) []string {
	var ot, nt skillTrigger
	if old.Manifest.Trigger != nil {
		ot = *old.Manifest.Trigger
	}
	if next.Manifest.Trigger != nil {
		nt = *next.Manifest.Trigger
	}
	var out []string
	for _, t := range nt.Tools {
		if !providedServer(t) && !slices.Contains(ot.Tools, t) {
			out = append(out, t)
		}
	}
	for _, x := range nt.Can {
		if !slices.Contains(ot.Can, x) {
			out = append(out, "can: "+x)
		}
	}
	if nt.Urgent != "" && nt.Urgent != ot.Urgent {
		out = append(out, "urgent")
	}
	scripts := old.Scripts()
	for _, s := range next.Scripts() {
		if !slices.Contains(scripts, s) {
			out = append(out, s)
		}
	}
	return out
}
