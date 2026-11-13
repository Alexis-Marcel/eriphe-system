package main

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

// A subject is what something is about: a project, a person, a pull
// request, an alert. It has one key, kind:slug, and one function makes it
// (subjectKey): threads, memories, intentions, notifications and their
// rules all keep the key it gives, so one matter is one key everywhere.
// A ref names one record (card:12, task:t-…, intention:5): a ref is never
// a subject.
//
//   - The kind is one of subjectKinds; a French or plural name is its
//     alias (projet → project), and an unknown kind, or a bare word, is a
//     topic (brand:velora → topic:brand-velora, factures → topic:facture).
//     owner and home stand alone: the user, the home.
//   - A name's slug (project, person, client, home, tool, topic) is lower
//     case, accents folded, spaces as hyphens; sub-parts after another
//     colon are kept (project:site:prod). A topic's one word is singular.
//   - A pull request is owner/repo#number, lower case.
//   - An id (mail, event, alert) is kept as written: it names an object.

// The kinds of subjects.
const (
	subjProject = "project"
	subjPerson  = "person"
	subjClient  = "client"
	subjPR      = "pr"
	subjMail    = "mail"
	subjEvent   = "event"
	subjAlert   = "alert"
	subjHome    = "home"
	subjOwner   = "owner"
	subjTool    = "tool"
	subjTopic   = "topic"
)

// subjectKinds are the kinds of subjects, in the order the tools list them.
var subjectKinds = []string{subjProject, subjPerson, subjClient, subjPR, subjMail, subjEvent, subjAlert, subjHome, subjOwner, subjTool, subjTopic}

// subjectKindAlias are the other names of a kind.
var subjectKindAlias = map[string]string{
	"projet": subjProject, "projets": subjProject, "projects": subjProject,
	"personne": subjPerson, "personnes": subjPerson, "people": subjPerson, "persons": subjPerson,
	"clients": subjClient, "cliente": subjClient,
	"pull": subjPR, "pull-request": subjPR, "prs": subjPR,
	"mails": subjMail, "courriel": subjMail, "email": subjMail,
	"evenement": subjEvent, "events": subjEvent, "rdv": subjEvent,
	"alerte": subjAlert, "alertes": subjAlert, "alerts": subjAlert,
	"maison": subjHome,
	"outil":  subjTool, "outils": subjTool, "tools": subjTool,
	"topics": subjTopic, "sujet": subjTopic, "theme": subjTopic,
}

// recordKinds name one record, never a subject: a ref.
var recordKinds = map[string]bool{"task": true, "intention": true, "notification": true, "msg": true, "skill": true,
	"thread": true, "card": true, "surface": true, "agenda": true, "memory": true, "run": true}

// subjectMax is the longest key kept.
const subjectMax = 200

var prSlugRe = regexp.MustCompile(`^[a-z0-9_.-]+/[a-z0-9_.-]+#[0-9]+$`)

// isSubjectKind tells a kind of subject, as subjectKey writes it.
func isSubjectKind(kind string) bool { return containsStr(subjectKinds, kind) }

// isRecordRef tells a ref that names one record (task:t-1), not a subject.
func isRecordRef(s string) bool {
	_, ok := recordRef(s)
	return ok
}

// recordRef is a ref that names one record, as written but trimmed; ok
// false for anything else, a subject included.
func recordRef(s string) (string, bool) {
	s = strings.TrimSpace(s)
	kind, id, ok := strings.Cut(s, ":")
	if !ok || id == "" || !recordKinds[strings.ToLower(kind)] || strings.ContainsFunc(s, unicode.IsSpace) {
		return "", false
	}
	return s, true
}

// subjectKey is the one key of a subject, or an error that says how to
// write it. "" is no subject.
func subjectKey(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", nil
	}
	if strings.ContainsAny(s, "\r\n\t") {
		return "", errors.New("a subject is one line")
	}
	rawKind, rest, hasColon := strings.Cut(s, ":")
	kind := subjectKindOf(rawKind)
	if !hasColon {
		if kind == subjOwner || kind == subjHome {
			return kind, nil
		}
		kind, rest = subjTopic, s
	}
	rest = strings.TrimSpace(rest)
	switch {
	case recordKinds[kind]:
		return "", fmt.Errorf("%s is a record, not a subject: a subject is kind:slug, kind %s", s, strings.Join(subjectKinds, ", "))
	case kind == "":
		kind, rest = subjTopic, s
	case !isSubjectKind(kind):
		kind, rest = subjTopic, rawKind+"-"+rest // brand:velora → topic:brand-velora
	}
	var slug string
	var err error
	switch kind {
	case subjOwner, subjHome:
		if rest == "" {
			return kind, nil
		}
		if kind == subjOwner {
			return subjOwner, nil // the user alone
		}
		slug, err = subjectName(rest, false)
	case subjPR:
		slug = strings.ToLower(strings.Join(strings.Fields(rest), ""))
		if !prSlugRe.MatchString(slug) {
			return "", fmt.Errorf("%s: a pull request is pr:owner/repo#number", s)
		}
	case subjMail, subjEvent, subjAlert:
		slug = rest
		if strings.ContainsFunc(slug, func(r rune) bool { return unicode.IsSpace(r) || !unicode.IsPrint(r) }) {
			return "", fmt.Errorf("%s: a %s is its id, without spaces", s, kind)
		}
	default:
		slug, err = subjectName(rest, kind == subjTopic)
	}
	if err != nil {
		return "", fmt.Errorf("%s: %w", s, err)
	}
	if slug == "" {
		return "", fmt.Errorf("%s: a subject is kind:slug (project:site, person:sam, pr:owner/repo#12)", s)
	}
	key := kind + ":" + slug
	if len(key) > subjectMax {
		return "", fmt.Errorf("a subject is %d characters at most", subjectMax)
	}
	return key, nil
}

// subjectKindOf is a kind as written, its alias resolved; "" if it holds
// more than letters and hyphens.
func subjectKindOf(raw string) string {
	k := strings.ToLower(foldAccents(strings.TrimSpace(raw)))
	k = strings.Join(strings.FieldsFunc(k, func(r rune) bool { return r == ' ' || r == '_' }), "-")
	if k == "" || strings.ContainsFunc(k, func(r rune) bool { return !(r >= 'a' && r <= 'z' || r == '-') }) {
		return ""
	}
	if a, ok := subjectKindAlias[k]; ok {
		return a
	}
	return k
}

// subjectName is a name's slug: lower case, accents folded, spaces as
// hyphens, sub-parts kept; a topic's single word in the singular.
func subjectName(s string, topic bool) (string, error) {
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

// subjectFilter reads a subject to search by: a key, or every subject of
// a kind (person:, person:*), or of a key's sub-parts (project:site*).
func subjectFilter(s string) (key string, prefix bool, err error) {
	s = strings.TrimSpace(s)
	if t := strings.TrimSuffix(s, "*"); t != s {
		s, prefix = t, true
	}
	if k, rest, ok := strings.Cut(s, ":"); ok && strings.TrimSpace(rest) == "" {
		kind := subjectKindOf(k)
		if !isSubjectKind(kind) {
			return "", false, fmt.Errorf("no kind %q: %s", k, strings.Join(subjectKinds, ", "))
		}
		return kind + ":", true, nil
	}
	key, err = subjectKey(s)
	return key, prefix, err
}

// subjectLabel is a key as the user reads it in a sentence: a topic
// without its kind (topic:facture → facture), any other key as it is.
func subjectLabel(key string) string {
	if w, ok := strings.CutPrefix(key, subjTopic+":"); ok {
		return w
	}
	return key
}
