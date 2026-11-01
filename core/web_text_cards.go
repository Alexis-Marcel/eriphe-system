package main

// The words of the conversation's cards (convo.html, convo_cards.go), and
// of the Journal and « À valider » in the settings (activity.html).

func init() {
	for lang, words := range cardStrings {
		for k, v := range words {
			webStrings[lang][k] = v
		}
	}
}

var cardStrings = map[string]map[string]string{
	"fr": {
		"card.kind.memory":    "À retenir",
		"card.kind.intention": "Rappel",
		"card.kind.rule":      "Règle de notification",
		"card.kind.watch":     "Veille",
		"card.kind.setting":   "Réglage",
		"card.kind.presence":  "Présence",
		"card.yes":            "Oui",
		"card.no":             "Non",
		"card.edit":           "Modifier",
		"card.edit.hint":      "Pour « %s » : ",
		"card.more":           "Plus",
		"card.allwaiting":     "Tout ce qui attend ton accord",
		"card.tainted.hint":   "Proposé après un contenu extérieur (un mail, une page) : seuls les boutons le décident.",
		"card.state.approved": "Validé",
		"card.state.rejected": "Refusé",
		"card.state.gone":     "N'est plus d'actualité",
		"card.did.approved":   "C'est validé.",
		"card.did.rejected":   "C'est refusé.",
		"card.already":        "Cette proposition n'attend plus de réponse.",
		"digest.line":         "%d chose(s) depuis le %s",
		"digest.none":         "Rien de particulier depuis le %s",
		"digest.journal":      "Tout le journal",
		"chat.why":            "Pourquoi ?",
		"card.tg.change":      "Pour changer quelque chose, réponds-moi ici.",
		"card.tg.failed":      "Impossible pour l'instant : réessaie depuis l'espace.",
		"card.voice.ask":      "Je te propose : %s. Oui ou non ?",
		"settings.journal":    "Journal",
		"journal.pagelead":    "La boîte noire : tout ce qui s'est passé, et chaque accès aux outils. {{name}} te le raconte dans la conversation ; ici, tu peux tout relire.",
		"approve.pagelead":    "{{name}} te demande ton accord dans la conversation ; tout ce qui attend encore est aussi ici. Rien n'entre en vigueur sans toi.",
	},
	"en": {
		"card.kind.memory":    "To remember",
		"card.kind.intention": "Reminder",
		"card.kind.rule":      "Notification rule",
		"card.kind.watch":     "Watch",
		"card.kind.setting":   "Setting",
		"card.kind.presence":  "Presence",
		"card.yes":            "Yes",
		"card.no":             "No",
		"card.edit":           "Change",
		"card.edit.hint":      "About “%s”: ",
		"card.more":           "More",
		"card.allwaiting":     "Everything waiting for your approval",
		"card.tainted.hint":   "Proposed after external content (a mail, a page): only the buttons decide it.",
		"card.state.approved": "Approved",
		"card.state.rejected": "Declined",
		"card.state.gone":     "No longer current",
		"card.did.approved":   "Approved.",
		"card.did.rejected":   "Declined.",
		"card.already":        "This proposal no longer waits for an answer.",
		"digest.line":         "%d thing(s) since %s",
		"digest.none":         "Nothing in particular since %s",
		"digest.journal":      "The whole journal",
		"chat.why":            "Why?",
		"card.tg.change":      "To change something, just reply here.",
		"card.tg.failed":      "Not possible right now: try again from the workspace.",
		"card.voice.ask":      "I suggest: %s. Yes or no?",
		"settings.journal":    "Journal",
		"journal.pagelead":    "The black box: everything that happened, and every tool access. {{name}} tells you in the conversation; here you can read it all again.",
		"approve.pagelead":    "{{name}} asks for your approval in the conversation; whatever still waits is here too. Nothing is in force without you.",
	},
}
