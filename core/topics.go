package main

import (
	"sort"
	"strings"
	"time"
)

// The topic vocabulary. The brain labels what it tells the user (notify's
// ref) and the user's rules match that label (topicRule): if the labels
// drift ("factures", "facture", "Facturation"), a rule silently stops
// matching. So the code keeps the labels comparable and small:
//
//   - a label is a subject, keyed by subjectKey (subject.go); a ref that
//     names one record (task:t-1, skill:x) is kept as written.
//   - knownTopics are the subjects in use: the rules', the notifications'
//     of the last 90 days, the live threads' and the memory's (project:,
//     person:, client:), so a notification about a project uses the same
//     key as its thread and its memories.
//   - reuseTopic maps a new label onto a known one of the same word family
//     (topic:facturation → topic:facture), and the tools list the known
//     topics to the brain when it invents one.

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
		if isRecordRef(ref) {
			return nil
		}
		ref, _ = subjectKey(ref)
		if ref == "" {
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
	if rows, err := c.store.db.Query(`SELECT subject, COUNT(*) FROM notifications WHERE created >= ? AND subject != '' GROUP BY subject`, since); err == nil {
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
	if rows, err := c.store.db.Query(`SELECT subject FROM threads WHERE subject != '' AND ` + activeThreads); err == nil {
		for rows.Next() {
			var s string
			if rows.Scan(&s) == nil {
				add(s)
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

// reuseTopic is the ref to use for a label: its subject key, or the
// known topic of the same word family (only one, else the key as is);
// known tells whether the result is in use already.
func reuseTopic(label string, topics []topicView) (ref string, known bool) {
	ref, _ = subjectKey(label)
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
