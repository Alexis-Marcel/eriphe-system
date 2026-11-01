package main

import (
	"sort"
	"strings"
	"time"
	"unicode"
)

// The topic vocabulary. The brain labels what it tells the user (notify's
// ref) and the user's rules match that label (topicRule): if the labels
// drift ("factures", "facture", "Facturation"), a rule silently stops
// matching. So the code keeps the labels comparable and small:
//
//   - canonRef is the one form of a ref: lower case, accents folded, spaces
//     as hyphens, French kind names in English (projet:x → project:x), and
//     a one-word topic in the singular (factures → facture). Entity refs keep
//     their slug as written (person:jules stays person:jules).
//   - knownTopics are the refs in use: the rules', the notifications' of
//     the last 90 days, and the memory's subjects (project:, person:,
//     client:), so a notification about a project uses the same key as
//     its progress card.
//   - reuseTopic maps a new label onto a known one of the same word family
//     (facturation → facture), and the tools list the known topics to the
//     brain when it invents one.

// refKinds are the kinds of entity refs; their slug is not singularised.
var refKinds = map[string]bool{"project": true, "person": true, "client": true, "task": true, "intention": true, "source": true,
	"watch": true, "notification": true, "msg": true, "owner": true, "home": true, "tool": true}

// refKindAlias are the other names of a kind; "" drops the prefix (a bare topic).
var refKindAlias = map[string]string{"projet": "project", "projets": "project", "personne": "person", "tache": "task",
	"veille": "watch", "outil": "tool", "topic": "", "sujet": "", "theme": ""}

// idKinds name one record, not a topic: never offered as vocabulary.
var idKinds = map[string]bool{"task": true, "intention": true, "notification": true, "msg": true, "watch": true}

// canonRef is the one form of a ref, or "" if it holds other characters
// than letters, digits, spaces and . _ @ / - :.
func canonRef(s string) string {
	s = foldAccents(strings.TrimSpace(s))
	if s == "" {
		return ""
	}
	for _, r := range s {
		if !(r < unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r))) && !strings.ContainsRune(" ._@/-:", r) {
			return ""
		}
	}
	var segs []string
	for _, seg := range strings.Split(s, ":") {
		seg = strings.Join(strings.FieldsFunc(seg, func(r rune) bool { return r == ' ' || r == '_' || r == '-' }), "-")
		if seg != "" {
			segs = append(segs, seg)
		}
	}
	if len(segs) == 0 {
		return ""
	}
	if alias, ok := refKindAlias[segs[0]]; ok && len(segs) > 1 {
		if alias == "" {
			segs = segs[1:]
		} else {
			segs[0] = alias
		}
	}
	if !(len(segs) > 1 && refKinds[segs[0]]) {
		for i, seg := range segs {
			segs[i] = singular(seg)
		}
	}
	return strings.Join(segs, ":")
}

// singular drops the plural s of a one-word topic, past four letters:
// factures → facture, invoices → invoice. A longer name (notes-de-frais)
// is kept as written; its family (topicFamily) still finds its variants.
// Words ending in -ss, -is, -us are left (devis, colis, status).
func singular(seg string) string {
	if len(seg) > 4 && !strings.Contains(seg, "-") && strings.HasSuffix(seg, "s") &&
		!strings.HasSuffix(seg, "ss") && !strings.HasSuffix(seg, "is") && !strings.HasSuffix(seg, "us") {
		return seg[:len(seg)-1]
	}
	return seg
}

// topicFamily is a ref's word family: its kind and the stems of its words,
// so facturation and facture are one family.
func topicFamily(ref string) string {
	var parts []string
	for _, seg := range strings.Split(ref, ":") {
		var stems []string
		for _, w := range memWords(seg) {
			stems = append(stems, stemWord(w))
		}
		parts = append(parts, strings.Join(stems, "-"))
	}
	return strings.Join(parts, ":")
}

// topicView is a known topic as the brain sees it.
type topicView struct {
	Ref  string `json:"ref"`
	Uses int    `json:"notifications_90_days,omitempty"`
	Rule bool   `json:"has_rule,omitempty"`
	From string `json:"from,omitempty"` // memory: a subject of the memory
}

// knownTopics are the refs in use, the ruled and the most used first.
func (c *core) knownTopics() []topicView {
	byRef := map[string]*topicView{}
	add := func(ref string) *topicView {
		ref = canonRef(ref)
		kind, _, _ := strings.Cut(ref, ":")
		if ref == "" || idKinds[kind] && strings.Contains(ref, ":") {
			return nil
		}
		if byRef[ref] == nil {
			byRef[ref] = &topicView{Ref: ref}
		}
		return byRef[ref]
	}
	if rules, err := c.notifyRules(); err == nil {
		for _, r := range rules {
			if r.Kind == ruleTopic {
				if t := add(r.Match); t != nil {
					t.Rule = true
				}
			}
		}
	}
	since := c.notifyTime().Add(-90 * 24 * time.Hour).Unix()
	if rows, err := c.store.db.Query(`SELECT ref, COUNT(*) FROM notifications WHERE created >= ? AND ref != '' GROUP BY ref`, since); err == nil {
		for rows.Next() {
			var ref string
			var n int
			if rows.Scan(&ref, &n) == nil {
				if t := add(ref); t != nil {
					t.Uses += n
				}
			}
		}
		rows.Close()
	}
	if c.mem != nil {
		if rows, err := c.mem.db.Query(`SELECT DISTINCT subject FROM memories WHERE status = 'active' AND
			(subject LIKE 'project:%' OR subject LIKE 'person:%' OR subject LIKE 'client:%')`); err == nil {
			for rows.Next() {
				var s string
				if rows.Scan(&s) == nil {
					if t := add(s); t != nil && t.From == "" && !t.Rule && t.Uses == 0 {
						t.From = "memory"
					}
				}
			}
			rows.Close()
		}
	}
	out := make([]topicView, 0, len(byRef))
	for _, t := range byRef {
		out = append(out, *t)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Rule != b.Rule {
			return a.Rule
		}
		if a.Uses != b.Uses {
			return a.Uses > b.Uses
		}
		return a.Ref < b.Ref
	})
	if len(out) > 40 {
		out = out[:40]
	}
	return out
}

// reuseTopic is the ref to use for a label: its canonical form, or the
// known topic of the same word family (only one, else the label as is);
// known tells whether the result is in use already.
func reuseTopic(label string, topics []topicView) (ref string, known bool) {
	ref = canonRef(label)
	if ref == "" {
		return "", false
	}
	var same []string
	fam := topicFamily(ref)
	for _, t := range topics {
		if t.Ref == ref || strings.HasPrefix(ref, t.Ref+":") {
			return ref, true
		}
		if topicFamily(t.Ref) == fam {
			same = append(same, t.Ref)
		}
	}
	if len(same) == 1 {
		return same[0], true
	}
	return ref, false
}

// isIDRef is a ref that names one record (task:12), not a topic.
func isIDRef(ref string) bool {
	kind, _, ok := strings.Cut(ref, ":")
	return ok && idKinds[kind]
}

// topicRefs are the refs of the topics, for a note.
func topicRefs(ts []topicView, max int) string {
	var refs []string
	for i, t := range ts {
		if i == max {
			break
		}
		refs = append(refs, t.Ref)
	}
	if len(refs) == 0 {
		return "none yet"
	}
	return strings.Join(refs, ", ")
}
