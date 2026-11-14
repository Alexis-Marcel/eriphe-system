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
		"subjects.title":         "Sujets",
		"subjects.lead":          "Les sujets que {{name}} connaît, les plus récents d'abord. Choisis-en un pour voir sa fiche telle que le modèle la reçoit : compilée par le code depuis ses fils, sa mémoire, ses intentions et ses cartes, sans modèle.",
		"subjects.thread":        "fil %d",
		"subjects.size":          "%d caractères, %d lignes",
		"subjects.dropped":       "%d ligne(s) laissée(s) pour la taille",
		"subjects.unknown":       "Ce sujet n'est pas connu.",
		"subjects.empty":         "Rien de connu sur ce sujet pour l'instant : pas de fiche.",
		"subjects.aliases":       "Aussi appelé :",
		"subjects.more":          "Et %d sujet(s) plus ancien(s).",
		"subjects.none":          "Aucun sujet pour l'instant.",
		"subjects.uses":          "Fiches poussées · 30 jours",
		"subjects.uses.how":      "Écrit : dans la demi-heure (deux heures pour une tâche ou un chef de fil), le modèle qui l'a reçue a écrit ou changé quelque chose du sujet (un souvenir, une intention, son fil). Répondu : la fiche du sujet d'un tour de {{name}}, quand il a répondu à ce tour sans redemander ce sujet (recall, read_subject) ; la fiche a suffi, ou le sujet ne comptait pas : on ne sait pas lequel. Non : ni l'un ni l'autre. Un chef de fil écrit presque toujours dans son fil : son compte dit peu.",
		"subjects.uses.place":    "Où",
		"subjects.uses.pushed":   "Poussées",
		"subjects.uses.wrote":    "Écrit",
		"subjects.uses.answered": "Répondu",
		"subjects.uses.no":       "Non",
		"subjects.uses.waiting":  "En attente",
		"subjects.place.turn":    "Tour de {{name}}",
		"subjects.place.pack":    "Session neuve",
		"subjects.place.task":    "Tâche",
		"subjects.place.lead":    "Chef de fil",
	},
	"en": {
		"subjects.title":         "Subjects",
		"subjects.lead":          "The subjects {{name}} knows, the latest first. Pick one to see its card as the model gets it: compiled by the code from its threads, memory, intentions and cards, with no model.",
		"subjects.thread":        "thread %d",
		"subjects.size":          "%d characters, %d lines",
		"subjects.dropped":       "%d line(s) left out for the size",
		"subjects.unknown":       "This subject is not known.",
		"subjects.empty":         "Nothing known of this subject yet: no card.",
		"subjects.aliases":       "Also called:",
		"subjects.more":          "And %d older subject(s).",
		"subjects.none":          "No subject yet.",
		"subjects.uses":          "Cards pushed · 30 days",
		"subjects.uses.how":      "Wrote: within half an hour (two hours for a task or a thread lead), the model that got it wrote or changed something of the subject (a memory, an intention, its thread). Answered: the card of the subject of a turn of {{name}}'s, when it answered that turn without asking for that subject again (recall, read_subject); the card was enough, or the subject did not matter: there is no telling which. No: neither. A thread lead nearly always writes in its thread: its count says little.",
		"subjects.uses.place":    "Where",
		"subjects.uses.pushed":   "Pushed",
		"subjects.uses.wrote":    "Wrote",
		"subjects.uses.answered": "Answered",
		"subjects.uses.no":       "No",
		"subjects.uses.waiting":  "Waiting",
		"subjects.place.turn":    "{{name}}'s turn",
		"subjects.place.pack":    "Fresh session",
		"subjects.place.task":    "Task",
		"subjects.place.lead":    "Thread lead",
	},
}
