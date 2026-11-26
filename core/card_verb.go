package main

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// What a card's yes button says: what the yes does, its verb first,
// never a bare « Oui » (« Envoyer », « Archiver », « Jeter les 102 »).
// Code says it, never a model: a call's verb is its tool's (its says'
// verb, else the first word of its sentence, which a tool writes as a
// verb: call_says.go), with how many items it acts on; any other kind has
// the verb of its kind. The page, a channel's buttons and the item opened
// all show it (proposalView's Verb).

// kindVerbs are the words of each kind's yes, by kind: every kind of
// proposalKinds has one (TestEveryCardHasItsVerb).
var kindVerbs = map[string]string{
	cardMemory:    "card.verb.keep",
	cardIntention: "card.verb.keep",
	cardRule:      "card.verb.apply",
	cardSetting:   "card.verb.change",
	cardTaskStart: "card.verb.start",
	cardUnlearn:   "card.verb.undo",
	cardAccess:    "card.verb.allow",
	cardSkill:     "card.verb.approve",
	cardCalls:     "card.verb.do",
	cardMonitor:   "card.verb.turnon",
}

// VerbIn is what the proposal's yes button says, in lang: its own verb
// when it has one that says something, else its kind's.
func (p proposal) VerbIn(lang string) string {
	if p.verb != nil {
		if v := p.verb(lang); v != "" && !bareYes(v) {
			return v
		}
	}
	key, ok := kindVerbs[p.Kind]
	if !ok {
		key = "card.verb.do"
	}
	return tr(lang, key)
}

// callVerb is the verb of a call's yes for n items: its tool's declared
// verb, else the first word of its sentence when it is one; with how many
// when more than one (« Jeter les 102 »). "" when it has none: the
// kind's says it.
func (c *core) callVerb(a accessQuestion, n int, lang string) string {
	name, _, _ := strings.Cut(a.Call, " ")
	v := ""
	if s := c.toolSaysOf(name); s != nil {
		v = inLang(s.Verb, lang)
	}
	if v == "" {
		v = firstVerb(c.callText(a, lang))
	}
	return countedVerb(lang, v, n)
}

// countedVerb is a verb for n items: « Jeter les 102 » for more than one.
func countedVerb(lang, v string, n int) string {
	if v == "" || n <= 1 {
		return v
	}
	return fmt.Sprintf(tr(lang, "card.verb.n"), v, n)
}

// firstVerb is a sentence's first word when it reads as a verb, as a
// tool writes its sentence (« Envoyer un message à Sam »): letters only,
// a capital first; "" otherwise (« « Dentiste » jeudi »).
func firstVerb(s string) string {
	w, _, _ := strings.Cut(strings.TrimSpace(s), " ")
	r, _ := utf8.DecodeRuneInString(w)
	if w == "" || !unicode.IsUpper(r) || strings.IndexFunc(w, func(r rune) bool { return !unicode.IsLetter(r) && r != '-' && r != '\'' }) >= 0 {
		return ""
	}
	return w
}

// bareYesWords are the words that are only a yes or a no: a verb that
// says only one of them asks nothing (a yes says its verb).
var bareYesWords = map[string]bool{"oui": true, "non": true, "ok": true, "okay": true, "d'accord": true, "ouais": true, "vas-y": true, "go": true, "yes": true, "no": true, "sure": true}

// bareYes tells whether a button's words are only a yes or a no.
func bareYes(text string) bool {
	t := strings.ToLower(strings.Trim(strings.TrimSpace(text), " .!?…✓"))
	return t == "" || bareYesWords[strings.ReplaceAll(t, "’", "'")]
}
