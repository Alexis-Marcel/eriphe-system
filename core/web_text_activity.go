package main

// The words of Activité (web_activity.go, templates/activity.html): the
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
		"nav.activity":         "Activité",
		"activity.lead":        "Ce qui s'est passé, et ce qui attend ton accord. {{name}} lit le même fil.",
		"activity.feed":        "Fil",
		"activity.approve":     "À valider",
		"activity.waiting":     "%d à valider",
		"happened.kind.access": "Accès aux outils",
		"approve.lead":         "Ce que {{name}} propose n'entre pas en vigueur sans ton accord. Pour changer une proposition, dis-le-lui dans la conversation.",
		"approve.perms":        "Permissions",
		"approve.memory":       "Mémoire et intentions",
		"approve.rules":        "Règles de notification",
		"approve.watches":      "Routines et veilles",
		"approve.quiet":        "Suggestions",
		"approve.adopt":        "Adopter",
		"approve.none":         "Rien à valider. Quand {{name}} te propose quelque chose, ça arrive ici.",
		"ask.go":               "Lui demander",
		"notif.sent":           "Envoyées",
		"rule.towait":          "Des propositions attendent dans À valider",
		"rule.ask":             "Pour une règle, dis-le à {{name}} : « ne me préviens plus des factures avant le brief », « calme de 23 h à 8 h le week-end ».",
		"watch.towait":         "%d proposition(s) attendent dans À valider",
		"watch.routines":       "Routines",
		"watch.routines.lead":  "À une heure fixe, ou quand tu reviens après une absence.",
		"watch.routines.none":  "Aucune routine.",
		"watch.routines.ask":   "Demande à {{name}} : « fais-moi un brief à 7 h en semaine », « fais le point quand je reviens après 3 h ».",
		"watch.list.lead":      "Un outil en lecture seule qu'il surveille, ou une alerte qu'un service lui envoie.",
		"watch.ask":            "Demande à {{name}} : « surveille l'état du site toutes les 10 minutes », « crée une alerte pour GitHub » ; l'adresse d'une alerte se crée ensuite ici.",
		"watch.runs":           "Derniers réveils",
	},
	"en": {
		"nav.activity":         "Activity",
		"activity.lead":        "What happened, and what waits for your approval. {{name}} reads the same feed.",
		"activity.feed":        "Feed",
		"activity.approve":     "To approve",
		"activity.waiting":     "%d to approve",
		"happened.kind.access": "Tool accesses",
		"approve.lead":         "Nothing {{name}} proposes is in force without your approval. To change a proposal, tell it in the conversation.",
		"approve.perms":        "Permissions",
		"approve.memory":       "Memory and intentions",
		"approve.rules":        "Notification rules",
		"approve.watches":      "Routines and watches",
		"approve.quiet":        "Suggestions",
		"approve.adopt":        "Adopt",
		"approve.none":         "Nothing to approve. When {{name}} proposes something, it comes here.",
		"ask.go":               "Ask",
		"notif.sent":           "Sent",
		"rule.towait":          "Proposals wait in To approve",
		"rule.ask":             "For a rule, tell {{name}}: “stop telling me about invoices before the brief”, “quiet from 11 pm to 8 am at the weekend”.",
		"watch.towait":         "%d proposal(s) wait in To approve",
		"watch.routines":       "Routines",
		"watch.routines.lead":  "At a time of day, or when you come back after an absence.",
		"watch.routines.none":  "No routine.",
		"watch.routines.ask":   "Ask {{name}}: “brief me at 7 on weekdays”, “give me the point when I come back after 3 hours”.",
		"watch.list.lead":      "A read-only tool it keeps an eye on, or an alert a service sends it.",
		"watch.ask":            "Ask {{name}}: “check the site's status every 10 minutes”, “make an alert for GitHub”; an alert's address is then made here.",
		"watch.runs":           "Last wakes",
	},
}
