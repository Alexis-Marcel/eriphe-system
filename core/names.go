package main

import (
	"log"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/kljensen/snowball/french"
)

// The names index (#573): what a record is about (its about) is a node,
// node:<id>, or an entity, a key kind:slug (pr:owner/repo#12,
// project:site, topic:<monitor>, person:lina), the user (owner), the home;
// the entities' keys are in one index, names(name, target), in core.db,
// each naming itself: a row that names another target is none of the
// code's, dropped when the store opens, and an about kept is a node or a
// key (validAbout), never anything else. The code finds in the user's messages the entities
// they name (named), by the words of their keys.

const namesSchema = `CREATE TABLE IF NOT EXISTS names (
  name   TEXT NOT NULL,     -- a key (kind:slug)
  target TEXT NOT NULL,     -- what it names: the key itself (an entity)
  seen   INTEGER NOT NULL,  -- when it was last written
  PRIMARY KEY (name, target));
CREATE UNIQUE INDEX IF NOT EXISTS names_key ON names (name) WHERE instr(name, ':') > 0 OR name IN ('owner', 'home');
CREATE INDEX IF NOT EXISTS names_target ON names (target);
DELETE FROM names WHERE name != target;`

func init() { extraSchema = append(extraSchema, namesSchema) }

// namedMax is the most entities a message names.
const namedMax = 5

// isKeyName tells a name that is a key: one target only.
func isKeyName(name string) bool {
	return strings.Contains(name, ":") || name == aliasOwner || name == aliasHome
}

// resolveAbout is the target an about is kept under: what its key names;
// "" stays "".
func (s *Store) resolveAbout(about string) string {
	for range 5 {
		var t string
		if about == "" || !isKeyName(about) || s.db.QueryRow(`SELECT target FROM names WHERE name = ?`, about).Scan(&t) != nil || t == about {
			return about
		}
		about = t
	}
	return about
}

// noteAbout is the target an about written now is kept under, its key
// noted as seen. A record ref that is no about is kept as given.
func (s *Store) noteAbout(about string) string {
	if k, err := aboutKey(about); err != nil || k != about || k == "" {
		return about // none, or not a key: a record's ref
	}
	t := s.resolveAbout(about)
	now := time.Now().Unix()
	if t == about {
		s.db.Exec(`INSERT OR IGNORE INTO names (name, target, seen) VALUES (?, ?, ?)`, about, about, now)
	}
	s.db.Exec(`UPDATE names SET seen = MAX(seen, ?) WHERE name = ?`, now, about)
	return t
}

// validAbout is an about as a record may keep it: a live node
// (node:<id>) or a key as aliasKey writes it; "" for anything else, such
// as a ref of a record that is no more.
func (c *core) validAbout(s string) string {
	k, err := aboutKey(s)
	if err != nil || k != s {
		return ""
	}
	if id, ok := strings.CutPrefix(k, refNode+":"); ok {
		if c.nodes == nil {
			return ""
		}
		if sum, err := c.nodes.Get(id); err != nil || !sum.Archived.IsZero() {
			return ""
		}
	}
	return k
}

// aboutIs is what an about given by someone is kept under, noted as seen:
// a node, or a key (aboutKey).
func (c *core) aboutIs(s string) (string, error) {
	k, err := aboutKey(s)
	if err != nil {
		return "", err
	}
	return c.store.noteAbout(k), nil
}

// aboutLookup is what to look an about up by; nothing is noted.
func (c *core) aboutLookup(s string) (string, error) {
	k, err := aboutKey(s)
	if err != nil {
		return "", err
	}
	return c.store.resolveAbout(k), nil
}

// A name told in a message. A key's words are its slug's, for a kind
// that names (project, person, client, tool, topic); the user, the home
// and ids have none.

// keyWords is the slug of a key that is told in a message, "" if none.
func keyWords(key string) string {
	kind, slug, _ := strings.Cut(key, ":")
	switch kind {
	case aliasProject, aliasPerson, aliasClient, aliasTool, aliasTopic:
		return slug
	}
	return ""
}

// nameStems are the stems of a name's words, in order.
func nameStems(name string) []string {
	var out []string
	for _, w := range memWords(foldAccents(name)) {
		out = append(out, stemWord(w))
	}
	return out
}

// nameMin is the fewest letters of a name told in a message: a shorter
// one would be found everywhere.
const nameMin = 3

// textStems are the stems of a text's words, as a set.
func textStems(text string) map[string]bool {
	set := map[string]bool{}
	for _, w := range memWords(foldAccents(text)) {
		set[stemWord(w)] = true
	}
	return set
}

// namedIn tells whether every word of a name is in the text.
func namedIn(text map[string]bool, name []string) bool {
	if len(name) == 0 {
		return false
	}
	for _, w := range name {
		if !text[w] {
			return false
		}
	}
	return true
}

func setOf(xs []string) map[string]bool {
	m := map[string]bool{}
	for _, x := range xs {
		m[x] = true
	}
	return m
}

// named are the entities a text names, by the words of their keys: the latest seen first, at most namedMax. One whose
// name is part of another's that is named too (site in site web) gives way
// to it.
func (c *core) named(text string) []string {
	words := textStems(text)
	if len(words) == 0 {
		return nil
	}
	rows, err := c.store.db.Query(`SELECT name, target FROM names ORDER BY seen DESC, name`)
	if err != nil {
		log.Printf("names: %v", err)
		return nil
	}
	type hit struct {
		target string
		name   []string
	}
	var hits []hit
	best := map[string]int{} // target → its hit
	for rows.Next() {
		var name, target string
		if rows.Scan(&name, &target) != nil {
			continue
		}
		told := name
		if isKeyName(name) {
			told = strings.ReplaceAll(keyWords(name), "-", " ")
		}
		s := nameStems(told)
		if len(strings.Join(s, "")) < nameMin || !namedIn(words, s) {
			continue
		}
		if i, ok := best[target]; ok {
			if len(s) > len(hits[i].name) {
				hits[i].name = s
			}
			continue
		}
		best[target] = len(hits)
		hits = append(hits, hit{target, s})
	}
	rows.Close()
	var out []string
	for _, h := range hits {
		inside := slices.ContainsFunc(hits, func(o hit) bool {
			return o.target != h.target && len(o.name) > len(h.name) && namedIn(setOf(o.name), h.name)
		})
		if t := c.validAbout(c.store.resolveAbout(h.target)); t != "" && !inside && len(out) < namedMax && !slices.Contains(out, t) {
			out = append(out, t)
		}
	}
	return out
}

// namedBlock is what the user's messages name of the entities known, for
// the brain, and the turn is about them; "" if none. Only keys are
// written.
func (c *core) namedBlock(msgs []Message) string {
	var named []string
	for _, m := range msgs {
		if m.Role != roleUser {
			continue
		}
		for _, k := range c.named(m.Text) {
			if !slices.Contains(named, k) {
				named = append(named, k)
			}
		}
	}
	c.turn.name(named)
	if len(named) == 0 {
		return ""
	}
	return "[nommés] Ce message nomme : " + strings.Join(named, ", ") + "."
}

// name records the entities the turn's messages name; a new
// delivery never keeps those of the turn before.
func (t *brainTurn) name(keys []string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !slices.Equal(t.namedRefs, keys) {
		t.namedRefs = slices.Clone(keys)
		t.save()
	}
}

// named are the entities the turn's messages name.
func (t *brainTurn) named() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return slices.Clone(t.namedRefs)
}

// memWords splits text into lower-case words of letters and digits: an
// apostrophe splits (l'anniversaire: l, anniversaire), a hyphen too.
func memWords(s string) []string {
	return strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
}

// stemWord is a word's stem, accents folded like the index's tokenizer.
func stemWord(w string) string {
	if utf8.RuneCountInString(w) < 3 || strings.IndexFunc(w, unicode.IsDigit) >= 0 {
		return foldAccents(w)
	}
	s := foldAccents(french.Stem(w, true))
	// Snowball leaves the plural of a short word (vélos): a plural s goes,
	// as Savoy's minimal stemmer does.
	if utf8.RuneCountInString(s) > 3 && strings.HasSuffix(s, "s") && !strings.HasSuffix(s, "ss") {
		s = s[:len(s)-1]
	}
	return s
}
