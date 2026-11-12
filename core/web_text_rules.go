package main

// The words of the user's rules on what the workspace shows
// (attention_rules.go): their card, and their page in the settings
// (web_rules.go, display.html).

func init() {
	for lang, words := range displayStrings {
		for k, v := range words {
			webStrings[lang][k] = v
		}
	}
}

var displayStrings = map[string]map[string]string{
	"fr": {
		"card.kind.showrule":    "Règle d'affichage",
		"showrule.key":          "Clé",
		"showrule.remark":       "Remarque",
		"showrule.more":         "Montrer plus en avant : %s",
		"showrule.less":         "Montrer moins en avant : %s",
		"showrule.never":        "Ne plus montrer : %s",
		"showrule.remarkonly":   "Remarque sur %s",
		"display.title":         "Règles d'affichage",
		"display.lead":          "Ce que ton espace te montre plus, moins ou plus du tout. Une règle naît d'une remarque (le menu ⋯ d'un module de l'Espace composé) ou d'une proposition d'Eriphe que tu as approuvée ; le code l'applique à chaque fois, avant de composer ton espace. Les règles sur un même objet s'additionnent ; ce qui tombe à zéro n'est plus montré.",
		"display.proposed":      "Proposées, à approuver",
		"display.none":          "Aucune règle : ton espace suit les poids que lui donnent Eriphe et ses assistants.",
		"display.remove":        "Retirer",
		"display.removeconfirm": "Retirer cette règle ?",
		"display.removed":       "Règle retirée",
		"display.approved":      "Règle en vigueur",
		"display.rejected":      "Proposition refusée",
		"display.gone":          "Cette règle n'est plus là",
	},
	"en": {
		"card.kind.showrule":    "Display rule",
		"showrule.key":          "Key",
		"showrule.remark":       "Remark",
		"showrule.more":         "Show more: %s",
		"showrule.less":         "Show less: %s",
		"showrule.never":        "Never show: %s",
		"showrule.remarkonly":   "A remark on %s",
		"display.title":         "Display rules",
		"display.lead":          "What your workspace shows you more, less or not at all. A rule comes from a remark (a module's ⋯ menu on the composed workspace) or from a proposal of Eriphe's you approved; code applies it every time, before your workspace is composed. Rules on the same object add up; what falls to zero is not shown.",
		"display.proposed":      "Proposed, to approve",
		"display.none":          "No rule: your workspace follows the weights Eriphe and its assistants give.",
		"display.remove":        "Remove",
		"display.removeconfirm": "Remove this rule?",
		"display.removed":       "Rule removed",
		"display.approved":      "Rule in force",
		"display.rejected":      "Proposal declined",
		"display.gone":          "This rule is no longer there",
	},
}
