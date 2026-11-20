package main

// The words of « Outils à revoir » (tool_review.go): the node's, written
// once in the user's language, the kinds of a change, and its actions'
// buttons and lines (list_actions.go).

func init() {
	for lang, words := range toolReviewStrings {
		for k, v := range words {
			webStrings[lang][k] = v
		}
	}
}

var toolReviewStrings = map[string]map[string]string{
	"fr": {
		"review.title":                 "Outils à revoir",
		"review.lead":                  "Ce que les serveurs de tes outils ont changé depuis que tu les as ajoutés : un outil dont la définition a changé, un nouveau, un qu'ils ont retiré. Eriphe ne s'en sert pas tant que tu ne l'as pas accepté. Coche, puis accepte ou refuse.",
		"review.empty":                 "Rien à revoir.",
		"review.before":                "Avant",
		"review.after":                 "Maintenant",
		"review.kind.new":              "nouveau",
		"review.kind.changed":          "changé",
		"review.kind.removed":          "retiré par son serveur",
		"act.verb.catalog_accept":      "Accepter %s",
		"act.verb.catalog_refuse":      "Refuser %s",
		"act.done.catalog_accept.one":  "1 accepté",
		"act.done.catalog_accept.many": "%d acceptés",
		"act.done.catalog_refuse.one":  "1 refusé",
		"act.done.catalog_refuse.many": "%d refusés",
	},
	"en": {
		"review.title":                 "Tools to review",
		"review.lead":                  "What your tools' servers changed since you added them: a tool whose definition changed, a new one, one they dropped. Eriphe does not use it until you accept it. Pick, then accept or refuse.",
		"review.empty":                 "Nothing to review.",
		"review.before":                "Before",
		"review.after":                 "Now",
		"review.kind.new":              "new",
		"review.kind.changed":          "changed",
		"review.kind.removed":          "dropped by its server",
		"act.verb.catalog_accept":      "Accept %s",
		"act.verb.catalog_refuse":      "Refuse %s",
		"act.done.catalog_accept.one":  "1 accepted",
		"act.done.catalog_accept.many": "%d accepted",
		"act.done.catalog_refuse.one":  "1 refused",
		"act.done.catalog_refuse.many": "%d refused",
	},
}
