package main

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// What a record is about (#573): a thread, thread:<id>, or a key,
// kind:slug, made by one function (aliasKey). A key is a thread's alias
// (pr:owner/repo#12, project:site, alert:backup, topic:<monitor>), or an
// entity of the memory: a person (person:lina), the user (owner), the
// home, a tool (tool:x), Eriphe's own topics. A key no thread has yet is
// kept as it is; once a thread takes it as its alias, what is about it is
// about the thread (names.go). A ref names one record (card:12, task:t-…,
// intention:5): a ref is never what a record is about, but thread:<id>.
// A mail or an event is an item of a thread's source, never an about.
//
//   - The kind is one of aliasKinds; a French or plural name is its
//     alias (projet → project), and an unknown kind, or a bare word, is a
//     topic (brand:velora → topic:brand-velora, factures → topic:facture).
//     owner and home stand alone: the user, the home.
//   - A name's slug (project, person, client, home, tool, topic) is lower
//     case, accents folded, spaces as hyphens; sub-parts after another
//     colon are kept (project:site:prod). A topic's one word is singular.
//   - A pull request is owner/repo#number, lower case.
//   - An alert's id is kept as written: it names an object.

// The kinds of keys.
const (
	aliasProject = "project"
	aliasPerson  = "person"
	aliasClient  = "client"
	aliasPR      = "pr"
	aliasAlert   = "alert"
	aliasHome    = "home"
	aliasOwner   = "owner"
	aliasTool    = "tool"
	aliasTopic   = "topic"
)

// aliasKinds are the kinds of keys, in the order the tools list them.
var aliasKinds = []string{aliasProject, aliasPerson, aliasClient, aliasPR, aliasAlert, aliasHome, aliasOwner, aliasTool, aliasTopic}

// itemKinds are a source's items, never an about: a thread notes them.
var itemKinds = map[string]bool{"mail": true, "mails": true, "courriel": true, "email": true, "event": true, "evenement": true, "events": true, "rdv": true}

// aliasKindNames are the other names of a kind.
var aliasKindNames = map[string]string{
	"projet": aliasProject, "projets": aliasProject, "projects": aliasProject,
	"personne": aliasPerson, "personnes": aliasPerson, "people": aliasPerson, "persons": aliasPerson,
	"clients": aliasClient, "cliente": aliasClient,
	"pull": aliasPR, "pull-request": aliasPR, "prs": aliasPR,
	"alerte": aliasAlert, "alertes": aliasAlert, "alerts": aliasAlert,
	"maison": aliasHome,
	"outil":  aliasTool, "outils": aliasTool, "tools": aliasTool,
	"topics": aliasTopic, "theme": aliasTopic,
}

// recordKinds name one record: a ref, never an about but thread:<id>.
var recordKinds = map[string]bool{"task": true, "intention": true, "notification": true, "msg": true, "skill": true,
	"thread": true, "card": true, "surface": true, "agenda": true, "memory": true, "run": true}

// aboutMax is the longest about kept.
const aboutMax = 200

var prSlugRe = regexp.MustCompile(`^[a-z0-9_.-]+/[a-z0-9_.-]+#[0-9]+$`)

// isAliasKind tells a kind of key, as aliasKey writes it.
func isAliasKind(kind string) bool { return containsStr(aliasKinds, kind) }

// isRecordRef tells a ref that names one record (task:t-1), not a key.
func isRecordRef(s string) bool {
	_, ok := recordRef(s)
	return ok
}

// recordRef is a ref that names one record, as written but trimmed; ok
// false for anything else, a key included.
func recordRef(s string) (string, bool) {
	s = strings.TrimSpace(s)
	kind, id, ok := strings.Cut(s, ":")
	if !ok || id == "" || !recordKinds[strings.ToLower(kind)] || strings.ContainsFunc(s, unicode.IsSpace) {
		return "", false
	}
	return s, true
}

// aboutKey is what a record is about, as given: thread:<id>, or a key
// (aliasKey); "" is none.
func aboutKey(s string) (string, error) {
	t := strings.TrimSpace(s)
	if id, ok := strings.CutPrefix(t, refThread+":"); ok {
		if n, err := strconv.ParseInt(strings.TrimSpace(id), 10, 64); err == nil && n > 0 {
			return threadRef(n), nil
		}
		return "", fmt.Errorf("%s: a thread is thread:<id>", t)
	}
	return aliasKey(t)
}

// aliasKey is the one key of a matter or an entity, or an error that says
// how to write it. "" is none.
func aliasKey(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", nil
	}
	if strings.ContainsAny(s, "\r\n\t") {
		return "", errors.New("a key is one line")
	}
	rawKind, rest, hasColon := strings.Cut(s, ":")
	kind := aliasKindOf(rawKind)
	if !hasColon {
		if kind == aliasOwner || kind == aliasHome {
			return kind, nil
		}
		kind, rest = aliasTopic, s
	}
	rest = strings.TrimSpace(rest)
	switch {
	case recordKinds[kind]:
		return "", fmt.Errorf("%s is a record, not a key: a key is kind:slug, kind %s", s, strings.Join(aliasKinds, ", "))
	case hasColon && itemKinds[strings.ToLower(strings.TrimSpace(rawKind))]:
		return "", fmt.Errorf("%s: a mail or an event is an item of a node's source (item_note), not a key: give the matter's key", s)
	case kind == "":
		kind, rest = aliasTopic, s
	case !isAliasKind(kind):
		kind, rest = aliasTopic, rawKind+"-"+rest // brand:velora → topic:brand-velora
	}
	var slug string
	var err error
	switch kind {
	case aliasOwner, aliasHome:
		if rest == "" {
			return kind, nil
		}
		if kind == aliasOwner {
			return aliasOwner, nil // the user alone
		}
		slug, err = aliasName(rest, false)
	case aliasPR:
		slug = strings.ToLower(strings.Join(strings.Fields(rest), ""))
		if !prSlugRe.MatchString(slug) {
			return "", fmt.Errorf("%s: a pull request is pr:owner/repo#number", s)
		}
	case aliasAlert:
		slug = rest
		if strings.ContainsFunc(slug, func(r rune) bool { return unicode.IsSpace(r) || !unicode.IsPrint(r) }) {
			return "", fmt.Errorf("%s: a %s is its id, without spaces", s, kind)
		}
	default:
		slug, err = aliasName(rest, kind == aliasTopic)
	}
	if err != nil {
		return "", fmt.Errorf("%s: %w", s, err)
	}
	if slug == "" {
		return "", fmt.Errorf("%s: a key is kind:slug (project:site, person:sam, pr:owner/repo#12)", s)
	}
	key := kind + ":" + slug
	if len(key) > aboutMax {
		return "", fmt.Errorf("a key is %d characters at most", aboutMax)
	}
	return key, nil
}

// aliasKindOf is a kind as written, its alias resolved; "" if it holds
// more than letters and hyphens.
func aliasKindOf(raw string) string {
	k := strings.ToLower(foldAccents(strings.TrimSpace(raw)))
	k = strings.Join(strings.FieldsFunc(k, func(r rune) bool { return r == ' ' || r == '_' }), "-")
	if k == "" || strings.ContainsFunc(k, func(r rune) bool { return !(r >= 'a' && r <= 'z' || r == '-') }) {
		return ""
	}
	if a, ok := aliasKindNames[k]; ok {
		return a
	}
	return k
}

// aliasName is a name's slug: lower case, accents folded, spaces as
// hyphens, sub-parts kept; a topic's single word in the singular.
func aliasName(s string, topic bool) (string, error) {
	s = strings.ToLower(foldAccents(s))
	for _, r := range s {
		if !(r < unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r))) && !strings.ContainsRune(" ._@/-:", r) {
			return "", errors.New("a name holds letters, digits, spaces and . _ @ / - only")
		}
	}
	var segs []string
	for _, seg := range strings.Split(s, ":") {
		seg = strings.Join(strings.FieldsFunc(seg, func(r rune) bool { return r == ' ' || r == '_' || r == '-' }), "-")
		if seg != "" {
			segs = append(segs, seg)
		}
	}
	if topic {
		for i, seg := range segs {
			segs[i] = singular(seg)
		}
	}
	return strings.Join(segs, ":"), nil
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

// aboutFilter reads an about to search by: thread:<id>, a key, or every
// key of a kind (person:, person:*), or of a key's sub-parts
// (project:site*).
func aboutFilter(s string) (key string, prefix bool, err error) {
	s = strings.TrimSpace(s)
	if t := strings.TrimSuffix(s, "*"); t != s {
		s, prefix = t, true
	}
	if k, rest, ok := strings.Cut(s, ":"); ok && strings.TrimSpace(rest) == "" {
		kind := aliasKindOf(k)
		if !isAliasKind(kind) {
			return "", false, fmt.Errorf("no kind %q: %s", k, strings.Join(aliasKinds, ", "))
		}
		return kind + ":", true, nil
	}
	key, err = aboutKey(s)
	return key, prefix, err
}

// aboutLabel is a key as the user reads it in a sentence: a topic
// without its kind (topic:facture → facture), any other key as it is.
func aboutLabel(key string) string {
	if w, ok := strings.CutPrefix(key, aliasTopic+":"); ok {
		return w
	}
	return key
}

// aboutOf is a stored value's about, "" if it gives none: for a lookup.
func aboutOf(s string) string {
	k, _ := aboutKey(s)
	return k
}
