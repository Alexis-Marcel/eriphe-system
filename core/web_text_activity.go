package main

// The words of the Journal and « À valider » (web_activity.go, templates/activity.html): the
// feed, « À valider », and how the pages say to ask the brain.

func init() {
	for lang, words := range activityStrings {
		for k, v := range words {
			webStrings[lang][k] = v
		}
	}
}

var activityStrings = map[string]map[string]string{
	"fr": {
		"activity.approve":     "À valider",
		"happened.kind.access": "Accès aux outils",
		"approve.lead":         "Ce que {{name}} propose n'entre pas en vigueur sans ton accord. Pour changer une proposition, dis-le-lui dans la conversation.",
		"approve.perms":        "Permissions",
		"approve.memory":       "Mémoire et intentions",
		"approve.rules":        "Règles de notification",
		"approve.watches":      "Routines et veilles",
		"approve.quiet":        "Suggestions",
		"approve.adopt":        "Adopter",
		"approve.none":         "Rien à valider. Quand {{name}} te propose quelque chose, il te le demande dans la conversation.",
		"ask.go":               "Lui demander",
		"notif.sent":           "Envoyées",
		"rule.towait":          "Des propositions attendent dans À valider",
		"rule.ask":             "Pour une règle, dis-le à {{name}} : « ne me préviens plus des factures avant le brief », « calme de 23 h à 8 h le week-end ».",
	},
	"en": {
		"activity.approve":     "To approve",
		"happened.kind.access": "Tool accesses",
		"approve.lead":         "Nothing {{name}} proposes is in force without your approval. To change a proposal, tell it in the conversation.",
		"approve.perms":        "Permissions",
		"approve.memory":       "Memory and intentions",
		"approve.rules":        "Notification rules",
		"approve.watches":      "Routines and watches",
		"approve.quiet":        "Suggestions",
		"approve.adopt":        "Adopt",
		"approve.none":         "Nothing to approve. When {{name}} proposes something, it asks you in the conversation.",
		"ask.go":               "Ask",
		"notif.sent":           "Sent",
		"rule.towait":          "Proposals wait in To approve",
		"rule.ask":             "For a rule, tell {{name}}: “stop telling me about invoices before the brief”, “quiet from 11 pm to 8 am at the weekend”.",
	},
}
