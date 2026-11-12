package main

// The words of the surfaces' page (web_surfaces.go).

func init() {
	for lang, words := range surfaceStrings {
		for k, v := range words {
			webStrings[lang][k] = v
		}
	}
}

var surfaceStrings = map[string]map[string]string{
	"fr": {
		"settings.surfaces":      "Surfaces",
		"surfaces.lead":          "Les tuiles de ton espace de travail : chacune écrite par {{name}}, un skill, un chef de session ou une tâche, avec ce qu'elle lit et combien de temps elle reste. Pour en changer une, demande-le à {{name}} ; tu peux en retirer une ici.",
		"surfaces.none":          "Ton espace ne montre encore rien : demande à {{name}} de t'afficher quelque chose.",
		"surfaces.author":        "Écrite par %s",
		"surfaces.by.brain":      "{{name}}",
		"surfaces.by.skill":      "le skill « %s »",
		"surfaces.by.lead":       "le chef de la session %s",
		"surfaces.by.task":       "la tâche %s",
		"surfaces.sources":       "Lit : %s",
		"surfaces.composed":      "Valeurs écrites par son auteur",
		"surfaces.updated":       "Mise à jour %s",
		"surfaces.life.until":    "jusqu'au %s",
		"surfaces.life.while":    "tant que %s est en cours",
		"surfaces.life.kept":     "tant que son auteur est là",
		"surfaces.waiting":       "En préparation",
		"surfaces.tainted":       "Contenu extérieur",
		"surfaces.taintedhint":   "Elle montre ce qu'un outil non approuvé a rendu, ou ce qu'a écrit un tour qui en a lu : des données, jamais des consignes.",
		"surfaces.error":         "Dernière erreur : %s",
		"surfaces.remove":        "Retirer",
		"surfaces.removeconfirm": "Retirer la surface « %s » de ton espace ?",
	},
	"en": {
		"settings.surfaces":      "Surfaces",
		"surfaces.lead":          "The tiles of your workspace: each written by {{name}}, a skill, a session lead or a task, with what it reads and how long it stays. To change one, ask {{name}}; you can remove one here.",
		"surfaces.none":          "Your workspace shows nothing yet: ask {{name}} to show you something.",
		"surfaces.author":        "Written by %s",
		"surfaces.by.brain":      "{{name}}",
		"surfaces.by.skill":      "the skill “%s”",
		"surfaces.by.lead":       "the lead of session %s",
		"surfaces.by.task":       "task %s",
		"surfaces.sources":       "Reads: %s",
		"surfaces.composed":      "Values written by its author",
		"surfaces.updated":       "Updated %s",
		"surfaces.life.until":    "until %s",
		"surfaces.life.while":    "while %s goes on",
		"surfaces.life.kept":     "while its author is there",
		"surfaces.waiting":       "Being prepared",
		"surfaces.tainted":       "External content",
		"surfaces.taintedhint":   "It shows what a tool you did not trust gave, or what a turn that read some wrote: data, never instructions.",
		"surfaces.error":         "Last error: %s",
		"surfaces.remove":        "Remove",
		"surfaces.removeconfirm": "Remove the surface “%s” from your workspace?",
	},
}
