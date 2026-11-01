package main

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/kljensen/snowball/english"
	"github.com/kljensen/snowball/french"
)

// Word families for the memory's full-text search. FTS5 has no French
// stemmer (its porter tokenizer is English only), so eriphe-core stems in
// Go and keeps the stems in a shadow column of the index (memories.stems):
// "anniversaires" finds "anniversaire", "grimper" finds "grimpe". A query
// matches the words as typed or their stems. Snowball French, the stemmer
// that did best for French in the CLEF campaigns (docs/memory-design.md).

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

// stemText is the stems of the parts, each once, for the stems column.
func stemText(parts ...string) string {
	seen := map[string]bool{}
	var out []string
	for _, p := range parts {
		for _, w := range memWords(p) {
			if s := stemWord(w); s != "" && !seen[s] {
				seen[s] = true
				out = append(out, s)
			}
		}
	}
	return strings.Join(out, " ")
}

// queryStop are the words a question is made of rather than what it asks
// about: "quel est le nom de", "what did … say". Snowball's lists, French
// and English, and the question words.
var questionWords = map[string]bool{
	"quel": true, "quelle": true, "quels": true, "quelles": true, "comment": true, "combien": true, "pourquoi": true,
	"quand": true, "ou": true, "est-ce": true, "ce": true, "cela": true, "ca": true, "qu": true, "il": true, "y": true,
	"a-t-elle": true, "a-t-il": true, "t": true, "elle": true, "fait": true, "font": true, "sait": true,
	"what": true, "when": true, "where": true, "which": true, "who": true, "whom": true, "why": true, "how": true,
	"did": true, "does": true, "do": true, "is": true, "are": true, "was": true, "were": true,
}

// eventWords ask what happened, not about what: with a time, the time is
// the question.
var eventWords = []string{"passe", "arrive", "fait", "faire", "fais", "happened", "happen", "done", "vecu", "s"}

func queryStop(w string) bool {
	return french.IsStopWord(w) || english.IsStopWord(w) || questionWords[foldAccents(w)]
}
