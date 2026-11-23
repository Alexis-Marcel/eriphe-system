package main

// The words of « Outils à revoir » (tool_review.go): the node's, written
// once in the user's language, the kinds of a change, and what its card
// says (call_words.go).

func init() {
	for lang, words := range toolReviewStrings {
		for k, v := range words {
			webStrings[lang][k] = v
		}
	}
}

var toolReviewStrings = map[string]map[string]string{
	"fr": {
		"review.title":        "Outils à revoir",
		"review.lead":         "Ce que les serveurs de tes outils ont changé depuis que tu les as ajoutés : un outil dont la définition a changé, un nouveau, un qu'ils ont retiré. Eriphe ne s'en sert pas tant que tu ne l'as pas accepté : Oui sur sa ligne l'accepte, ✕ le refuse.",
		"review.empty":        "Rien à revoir.",
		"review.before":       "Avant",
		"review.after":        "Maintenant",
		"review.kind.new":     "nouveau",
		"review.kind.changed": "changé",
		"review.kind.removed": "retiré par son serveur",
	},
	"en": {
		"review.title":        "Tools to review",
		"review.lead":         "What your tools' servers changed since you added them: a tool whose definition changed, a new one, one they dropped. Eriphe does not use it until you accept it: Yes on its row accepts it, ✕ refuses it.",
		"review.empty":        "Nothing to review.",
		"review.before":       "Before",
		"review.after":        "Now",
		"review.kind.new":     "new",
		"review.kind.changed": "changed",
		"review.kind.removed": "dropped by its server",
	},
}
