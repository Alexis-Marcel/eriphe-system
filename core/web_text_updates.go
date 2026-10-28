package main

// The words of Settings → Machines → Updates (web_updates.go).

func init() {
	for lang, words := range updatesStrings {
		for k, v := range words {
			webStrings[lang][k] = v
		}
	}
}

var updatesStrings = map[string]map[string]string{
	"fr": {
		"updates.title":                  "Mises à jour",
		"updates.lead":                   "Chaque machine se met à jour elle-même : eriphe-core d'abord, puis les autres machines, jamais pendant qu'une tâche travaille. Si une machine ne revient pas après sa mise à jour, elle reprend sa version précédente.",
		"updates.all":                    "Pour toutes les machines",
		"updates.policy.notify":          "Me prévenir",
		"updates.policy.night":           "Automatiquement la nuit",
		"updates.policy.never":           "Jamais",
		"updates.policy.inherit":         "Comme les autres (%s)",
		"updates.policyfor":              "Mises à jour de %s",
		"updates.now":                    "Mettre à jour maintenant",
		"updates.rollback":               "Revenir à la version précédente",
		"updates.rollbackconfirm":        "Revenir à la version précédente sur %s ? La machine redémarre Eriphe ; ses tâches reprennent ensuite.",
		"updates.noversion":              "version inconnue",
		"updates.pinned":                 "épinglée sur %s",
		"updates.available":              "Version %s disponible",
		"updates.noupdater":              "Cette machine ne sait pas encore se mettre à jour : relance une fois sa commande d'installation.",
		"updates.waiting":                "Mise à jour demandée : elle attend la fin des tâches en cours.",
		"updates.requested":              "Mise à jour demandée…",
		"updates.running":                "Mise à jour en cours…",
		"update.already":                 "Une demande est déjà en cours pour cette machine.",
		"update.noupdater":               "Cette machine n'a pas de programme de mise à jour : relance une fois sa commande d'installation.",
		"update.corefirst":               "eriphe-core se met à jour d'abord : une machine n'est jamais plus récente que lui.",
		"update.pinnedno":                "Cette machine est épinglée sur une version.",
		"update.outcome.running":         "En cours",
		"update.outcome.ok":              "À jour",
		"update.outcome.rollback_ok":     "Version précédente remise",
		"update.outcome.current":         "Déjà à jour",
		"update.outcome.pinned":          "Épinglée, rien à faire",
		"update.outcome.refused":         "Refusée",
		"update.outcome.skipped":         "Ignorée : cette version a déjà échoué ici",
		"update.outcome.failed":          "Échec, rien n'a changé",
		"update.outcome.rolled_back":     "Échec, version précédente remise",
		"update.outcome.rollback_failed": "Échec, et la version précédente ne repart pas",
	},
	"en": {
		"updates.title":                  "Updates",
		"updates.lead":                   "Each machine updates itself: eriphe-core first, then the other machines, never while a task is at work. A machine that does not come back after its update goes back to its previous version.",
		"updates.all":                    "For all machines",
		"updates.policy.notify":          "Tell me",
		"updates.policy.night":           "Automatically at night",
		"updates.policy.never":           "Never",
		"updates.policy.inherit":         "Like the others (%s)",
		"updates.policyfor":              "Updates of %s",
		"updates.now":                    "Update now",
		"updates.rollback":               "Go back to the previous version",
		"updates.rollbackconfirm":        "Go back to the previous version on %s? The machine restarts Eriphe; its tasks then resume.",
		"updates.noversion":              "unknown version",
		"updates.pinned":                 "pinned to %s",
		"updates.available":              "Version %s available",
		"updates.noupdater":              "This machine cannot update itself yet: run its install command again, once.",
		"updates.waiting":                "Update asked: it waits for the running tasks to end.",
		"updates.requested":              "Update asked…",
		"updates.running":                "Updating…",
		"update.already":                 "A request is already under way for this machine.",
		"update.noupdater":               "This machine has no updater: run its install command again, once.",
		"update.corefirst":               "eriphe-core updates first: a machine is never newer than it.",
		"update.pinnedno":                "This machine is pinned to a version.",
		"update.outcome.running":         "Under way",
		"update.outcome.ok":              "Updated",
		"update.outcome.rollback_ok":     "Previous version back",
		"update.outcome.current":         "Already up to date",
		"update.outcome.pinned":          "Pinned, nothing to do",
		"update.outcome.refused":         "Refused",
		"update.outcome.skipped":         "Skipped: this version failed here before",
		"update.outcome.failed":          "Failed, nothing changed",
		"update.outcome.rolled_back":     "Failed, previous version put back",
		"update.outcome.rollback_failed": "Failed, and the previous version does not start",
	},
}
