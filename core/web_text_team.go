package main

// The words of the team: an assistant's creation card (assistant_proposal.go)
// and the team's page (web_assistants.go).

func init() {
	for lang, words := range teamStrings {
		for k, v := range words {
			webStrings[lang][k] = v
		}
	}
}

var teamStrings = map[string]map[string]string{
	"fr": {
		"card.kind.newassistant":      "Nouvel assistant",
		"newassistant.card":           "Nouvel assistant « %s » : %s",
		"newassistant.none":           "aucun",
		"newassistant.f.assistant":    "Assistant",
		"newassistant.f.facts":        "Faits",
		"newassistant.f.events":       "Événements",
		"newassistant.f.rules":        "Règles",
		"newassistant.f.channels":     "Canaux",
		"newassistant.f.tools":        "Outils",
		"newassistant.f.why":          "Pourquoi un assistant",
		"newassistant.f.links":        "Où le voir",
		"newassistant.f.cost":         "Coût estimé",
		"newassistant.f.tainted":      "Proposé après avoir lu",
		"newassistant.facts":          "il écrit %s ; on lui prête %s",
		"newassistant.rows":           "%s (lignes : %s)",
		"newassistant.type.number":    "nombre",
		"newassistant.type.date":      "date",
		"newassistant.type.text":      "texte",
		"newassistant.why.boundary":   "il lit à ta place ce qui vient de l'extérieur",
		"newassistant.why.tools":      "il a ses propres outils ou droits",
		"newassistant.why.wake":       "il se réveille seul et a besoin d'un modèle pour juger",
		"newassistant.why.parallel":   "il travaille en parallèle de {{name}}",
		"newassistant.links":          "Réglages → Assistants (ton équipe), et ses règles dans Réglages → Règles",
		"newassistant.cost.wakes":     "≈ %d réveils par mois.",
		"newassistant.cost.wakesmore": "≈ %d réveils par mois à heure fixe, plus ceux de ses événements.",
		"newassistant.cost.events":    "Ses réveils dépendent de ce qui arrive.",
		"newassistant.cost.unknown":   "Le coût d'un réveil de son modèle n'est pas encore connu.",
		"newassistant.cost.turn":      "≈ %.3f $ par réveil (son modèle, ces 30 jours).",
		"newassistant.cost.month":     "Soit ≈ %.2f $ par mois.",
	},
	"en": {
		"card.kind.newassistant":      "New assistant",
		"newassistant.card":           "New assistant “%s”: %s",
		"newassistant.none":           "none",
		"newassistant.f.assistant":    "Assistant",
		"newassistant.f.facts":        "Facts",
		"newassistant.f.events":       "Events",
		"newassistant.f.rules":        "Rules",
		"newassistant.f.channels":     "Channels",
		"newassistant.f.tools":        "Tools",
		"newassistant.f.why":          "Why an assistant",
		"newassistant.f.links":        "Where to see it",
		"newassistant.f.cost":         "Estimated cost",
		"newassistant.f.tainted":      "Proposed after reading",
		"newassistant.facts":          "it writes %s; it is lent %s",
		"newassistant.rows":           "%s (rows: %s)",
		"newassistant.type.number":    "number",
		"newassistant.type.date":      "date",
		"newassistant.type.text":      "text",
		"newassistant.why.boundary":   "it reads what comes from outside so that you do not have to",
		"newassistant.why.tools":      "it has tools or rights of its own",
		"newassistant.why.wake":       "it wakes on its own and needs a model to judge",
		"newassistant.why.parallel":   "it works beside {{name}}",
		"newassistant.links":          "Settings → Assistants (your team), and its rules in Settings → Rules",
		"newassistant.cost.wakes":     "≈ %d wakes a month.",
		"newassistant.cost.wakesmore": "≈ %d wakes a month on a schedule, plus those of its events.",
		"newassistant.cost.events":    "Its wakes depend on what happens.",
		"newassistant.cost.unknown":   "What a wake of its model costs is not known yet.",
		"newassistant.cost.turn":      "≈ $%.3f a wake (its model, these 30 days).",
		"newassistant.cost.month":     "That is ≈ $%.2f a month.",
	},
}
