package main

// The words of the memory's readable mirror, memoire/ (memory_mirror.go):
// its titles, groups and marks, in the user's language.

func init() {
	for lang, words := range mirrorStrings {
		for k, v := range words {
			webStrings[lang][k] = v
		}
	}
}

var mirrorStrings = map[string]map[string]string{
	"fr": {
		"mirror.profile":          "Profil et préférences",
		"mirror.facts.general":    "En général",
		"mirror.people.unnamed":   "Sans nom",
		"mirror.group.profile":    "Profil",
		"mirror.group.preference": "Préférences",
		"mirror.group.owner":      "À ton sujet",
		"mirror.group.lesson":     "Leçons",
		"mirror.group.situation":  "En ce moment",
		"mirror.until":            "jusqu'au %s :",
		"mirror.external":         "extérieur",
		"mirror.note":             "> Miroir de la mémoire d'Eriphe, réécrit par eriphe-core à chaque changement : le modifier ici ne change rien. Chaque ligne finit par l'identifiant de sa mémoire, ^m et son numéro.",
	},
	"en": {
		"mirror.profile":          "Profile and preferences",
		"mirror.facts.general":    "In general",
		"mirror.people.unnamed":   "Unnamed",
		"mirror.group.profile":    "Profile",
		"mirror.group.preference": "Preferences",
		"mirror.group.owner":      "About you",
		"mirror.group.lesson":     "Lessons",
		"mirror.group.situation":  "For now",
		"mirror.until":            "until %s:",
		"mirror.external":         "external",
		"mirror.note":             "> Mirror of Eriphe's memory, rewritten by eriphe-core on every change: editing it here changes nothing. Each line ends with its memory's id, ^m and its number.",
	},
}
