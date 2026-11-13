package main

import (
	"embed"
	"io/fs"
	"log"
	"path"
	"strconv"
	"time"
)

// Default skills (#427): skills Eriphe brings, installed once and then
// the user's like any other. Each is installed approved and on at the
// first start that has it, recorded in the learning journal (so the brain
// tells it, and "oublie ça" undoes it), and never again: removed,
// changed or turned off, it stays as the user left it. A skill of the same
// name the user already has is left alone.
//
// A default skill names no tool (TestNoToolInDefaultSkills): it works from
// whatever is linked. It is lent only what grants nothing beyond the
// user's access policy (the workspace, whose writes are its own surfaces):
// a tool it should read itself is lent the normal way, by a new version
// the user approves on its card.

//go:embed default_skills
var defaultSkillFS embed.FS

const (
	defaultSkillKey    = "default_skill:" // + name: when it was installed, unix
	skillFromDefault   = "default"        // a skills row's source: brought by Eriphe
	defaultSkillsDir   = "default_skills"
	defaultSkillReason = "installed by default"
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

// installDefaultSkills installs the default skills not installed yet.
func (c *core) installDefaultSkills() {
	for name, parts := range defaultSkills() {
		if c.store.get(defaultSkillKey+name) != "" {
			continue // installed once: the user's since
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
		c.learnSkill(k.Name, k.Pending, prev, "eriphe", defaultSkillReason)
		log.Printf("default skill %s installed", k.Name)
	}
}
