package main

// The words of « Outils à revoir » (tool_review.go): the node's, written
// in the user's language, the kinds of a change, what changed in a few
// words and its detail.

func init() {
	for lang, words := range toolReviewStrings {
		for k, v := range words {
			webStrings[lang][k] = v
		}
	}
}

var toolReviewStrings = map[string]map[string]string{
	"fr": {
		"review.title":              "Outils à revoir",
		"review.lead":               "Ce que les serveurs de tes outils ont changé depuis que tu les as ajoutés. Eriphe ne s'en sert pas tant que tu ne l'as pas accepté. Ce qui donne plus de pouvoir à un outil vient en premier ; ouvre une ligne pour voir ce qui a changé, ✕ pour la refuser.",
		"review.empty":              "Rien à revoir.",
		"review.kind.new":           "nouveau",
		"review.kind.changed":       "changé",
		"review.kind.removed":       "retiré par son serveur",
		"review.what.new":           "nouvel outil",
		"review.what.unkept":        "ancienne version non conservée",
		"review.what.description":   "description modifiée",
		"review.what.definition":    "définition modifiée",
		"review.definition.detail":  "Même nom, même description, mêmes paramètres : un autre détail de sa définition a changé (la description d'un paramètre, un type).",
		"review.params":             "Paramètres :",
		"review.params.added":       "paramètres ajoutés :",
		"review.params.dropped":     "paramètres retirés :",
		"review.words.added":        "Ajouté : « %s »",
		"review.words.dropped":      "Retiré : « %s »",
		"review.words.added.more":   "… et %d autres ajouts",
		"review.words.dropped.more": "… et %d autres retraits",
		"review.says":               "Dit : « %s »",
		"review.said":               "Disait : « %s »",
		"review.cat.moved":          "Avant : %s ; maintenant : %s.",
		"review.cat.read":           "lecture",
		"review.cat.change":         "modification",
		"review.cat.delete":         "suppression",
		"review.cat.send":           "envoi",
		"review.risky":              "plus de pouvoir",
	},
	"en": {
		"review.title":              "Tools to review",
		"review.lead":               "What your tools' servers changed since you added them. Eriphe does not use it until you accept it. What gives a tool more power comes first; open a line to see what changed, ✕ to refuse it.",
		"review.empty":              "Nothing to review.",
		"review.kind.new":           "new",
		"review.kind.changed":       "changed",
		"review.kind.removed":       "dropped by its server",
		"review.what.new":           "new tool",
		"review.what.unkept":        "previous version not kept",
		"review.what.description":   "description changed",
		"review.what.definition":    "definition changed",
		"review.definition.detail":  "Same name, description and parameters: another part of its definition changed (a parameter's description, a type).",
		"review.params":             "Parameters:",
		"review.params.added":       "parameters added:",
		"review.params.dropped":     "parameters removed:",
		"review.words.added":        "Added: “%s”",
		"review.words.dropped":      "Removed: “%s”",
		"review.words.added.more":   "… and %d more added",
		"review.words.dropped.more": "… and %d more removed",
		"review.says":               "Says: “%s”",
		"review.said":               "Said: “%s”",
		"review.cat.moved":          "Before: %s; now: %s.",
		"review.cat.read":           "read",
		"review.cat.change":         "change",
		"review.cat.delete":         "delete",
		"review.cat.send":           "send",
		"review.risky":              "more power",
	},
}
