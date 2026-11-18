package main

// The words code writes in a task's node (task_node.go): its title when
// the task has none, and the lines of its trace about what Eriphe or the
// user settled for it.

func init() {
	for lang, words := range nodeStrings {
		for k, v := range words {
			webStrings[lang][k] = v
		}
	}
}

var nodeStrings = map[string]map[string]string{
	"fr": {
		"tasknode.title":       "Tâche %s",
		"tasknode.reply":       "Eriphe a répondu : %s",
		"tasknode.checks":      "Eriphe a corrigé les contrôles (%s).",
		"tasknode.checksreply": "Eriphe a corrigé les contrôles (%s) et a répondu : %s",
		"tasknode.release":     "Eriphe a demandé, sur une carte, de débloquer %s : rien n'est débloqué avant ta réponse.",
		"tasknode.released":    "Tu as débloqué %s sur la carte.",
		"tasknode.kept":        "Tu as refusé de débloquer %s sur la carte.",
	},
	"en": {
		"tasknode.title":       "Task %s",
		"tasknode.reply":       "Eriphe answered: %s",
		"tasknode.checks":      "Eriphe fixed the checks (%s).",
		"tasknode.checksreply": "Eriphe fixed the checks (%s) and answered: %s",
		"tasknode.release":     "Eriphe asked, on a card, to release %s: nothing is released before your answer.",
		"tasknode.released":    "You released %s on the card.",
		"tasknode.kept":        "You refused to release %s on the card.",
	},
}
