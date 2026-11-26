package main

// The words code writes in a task's node (task_node.go): its title when
// the task has none, and the lines of its trace about what Eriphe or the
// user settled for it; why a node a monitor follows asks for the user
// (node_live.go); the title of a node the safety net opens (node_net.go);
// a work's last step (node_steps.go).

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
		"tasknode.goal":        "Le travail de la tâche %s",
		"net.goal":             "Garder la réponse donnée, pour la retrouver",
		"tasknode.reply":       "Eriphe a répondu : %s",
		"tasknode.checks":      "Eriphe a corrigé les contrôles (%s).",
		"tasknode.checksreply": "Eriphe a corrigé les contrôles (%s) et a répondu : %s",
		"tasknode.release":     "Eriphe a demandé, sur une carte, de débloquer %s : rien n'est débloqué avant ta réponse.",
		"tasknode.released":    "Tu as débloqué %s sur la carte.",
		"tasknode.kept":        "Tu as refusé de débloquer %s sur la carte.",
		"node.monitor.crossed": "« %s » a franchi son seuil, dans « %s ».",
		"node.monitor.failed":  "« %s » ne répond plus, dans « %s ».",
		"node.net.title":       "Réponse du %s",
		"step.final.title":     "Contrôle de l'objectif",
		"alive.skill.monitor":  "lancé quand la surveillance « %s » voit quelque chose",
		"alive.skill.events":   "lancé à chaque événement %s",
		"alive.skill.schedule": "lancé à ses heures",
		"alive.skill.any":      "lancé à la demande",
		"alive.skill.every":    ", au plus une fois toutes les %s",
		"origin.user":          "Ouvert à ta demande.",
		"origin.user.said":     "Ouvert à ta demande : « %s »",
		"origin.user.card":     "Ouvert à ta demande : tu as dit oui à sa carte.",
		"origin.skill":         "Ouvert par le skill %s, %s, sans demande de ta part.",
		"origin.event":         "Ouvert après un événement, sans demande de ta part.",
		"origin.unknown":       "Origine inconnue.",
		"origin.eriphe":        "Une place d'Eriphe, ouverte par lui à son démarrage.",
		"origin.on.schedule":   "à son heure",
		"origin.on.monitor":    "après ce qu'a vu la surveillance « %s »",
		"origin.on.alert":      "après l'alerte « %s »",
		"origin.on.event":      "après un événement",
	},
	"en": {
		"tasknode.title":       "Task %s",
		"tasknode.goal":        "The work of task %s",
		"net.goal":             "Keep the answer given, to find it again",
		"tasknode.reply":       "Eriphe answered: %s",
		"tasknode.checks":      "Eriphe fixed the checks (%s).",
		"tasknode.checksreply": "Eriphe fixed the checks (%s) and answered: %s",
		"tasknode.release":     "Eriphe asked, on a card, to release %s: nothing is released before your answer.",
		"tasknode.released":    "You released %s on the card.",
		"tasknode.kept":        "You refused to release %s on the card.",
		"node.monitor.crossed": "“%s” crossed its threshold, in “%s”.",
		"node.monitor.failed":  "“%s” stopped answering, in “%s”.",
		"node.net.title":       "Answer of %s",
		"step.final.title":     "Goal check",
		"alive.skill.monitor":  "started when the monitor “%s” sees something",
		"alive.skill.events":   "started on each %s event",
		"alive.skill.schedule": "started on its schedule",
		"alive.skill.any":      "started on demand",
		"alive.skill.every":    ", at most once every %s",
		"origin.user":          "Opened at your request.",
		"origin.user.said":     "Opened at your request: “%s”",
		"origin.user.card":     "Opened at your request: you said yes to its card.",
		"origin.skill":         "Opened by the skill %s, %s, without a request of yours.",
		"origin.event":         "Opened after an event, without a request of yours.",
		"origin.unknown":       "Unknown origin.",
		"origin.eriphe":        "One of Eriphe's own places, opened by it at its start.",
		"origin.on.schedule":   "on its schedule",
		"origin.on.monitor":    "after what the monitor “%s” saw",
		"origin.on.alert":      "after the alert “%s”",
		"origin.on.event":      "after an event",
	},
}
