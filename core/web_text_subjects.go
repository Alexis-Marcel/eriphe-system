package main

// The words of « Sujets » (web_subjects.go, templates/subjects.html).

func init() {
	for lang, words := range subjectsStrings {
		for k, v := range words {
			webStrings[lang][k] = v
		}
	}
}

var subjectsStrings = map[string]map[string]string{
	"fr": {
		"subjects.title":   "Sujets",
		"subjects.lead":    "Les sujets que {{name}} connaît, les plus récents d'abord. Choisis-en un pour voir sa fiche telle que le modèle la reçoit : compilée par le code depuis ses fils, sa mémoire, ses intentions et ses cartes, sans modèle.",
		"subjects.thread":  "fil %d",
		"subjects.size":    "%d caractères, %d lignes",
		"subjects.dropped": "%d ligne(s) laissée(s) pour la taille",
		"subjects.unknown": "Ce sujet n'est pas connu.",
		"subjects.empty":   "Rien de connu sur ce sujet pour l'instant : pas de fiche.",
		"subjects.aliases": "Aussi appelé :",
		"subjects.more":    "Et %d sujet(s) plus ancien(s).",
		"subjects.none":    "Aucun sujet pour l'instant.",
	},
	"en": {
		"subjects.title":   "Subjects",
		"subjects.lead":    "The subjects {{name}} knows, the latest first. Pick one to see its card as the model gets it: compiled by the code from its threads, memory, intentions and cards, with no model.",
		"subjects.thread":  "thread %d",
		"subjects.size":    "%d characters, %d lines",
		"subjects.dropped": "%d line(s) left out for the size",
		"subjects.unknown": "This subject is not known.",
		"subjects.empty":   "Nothing known of this subject yet: no card.",
		"subjects.aliases": "Also called:",
		"subjects.more":    "And %d older subject(s).",
		"subjects.none":    "No subject yet.",
	},
}
