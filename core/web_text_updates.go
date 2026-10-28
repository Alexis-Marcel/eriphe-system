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
		"updates.all":                    "Tout mettre à jour",
		"updates.allconfirm":             "Mettre à jour toutes les machines ? eriphe-core d'abord, puis les autres, chacune une fois ses tâches finies.",
		"updates.nothing":                "Aucune machine à mettre à jour.",
		"updates.night":                  "Mettre à jour automatiquement la nuit",
		"updates.nighthint":              "Toutes les machines, entre 3 h et 5 h, quand aucune tâche ne travaille.",
		"updates.nighton":                "Les machines se mettront à jour la nuit.",
		"updates.nightoff":               "Les machines ne se mettront plus à jour toutes seules.",
		"updates.help":                   "Pour revenir à la version précédente ou rester sur une version, relance la commande d'installation de la machine avec --rollback ou --version <version> (-Rollback, -Version sous Windows).",
		"updates.now":                    "Mettre à jour",
		"updates.aftercore":              "Après eriphe-core",
		"updates.manual":                 "Cette machine ne se met pas à jour toute seule. Lance cette commande dessus : les données et les réglages restent.",
		"updates.noversion":              "version inconnue",
		"updates.pinned":                 "épinglée sur %s",
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
		"update.outcome.refused":         "Mise à jour refusée",
		"update.outcome.skipped":         "Mise à jour ignorée : cette version a déjà échoué ici",
		"update.outcome.failed":          "La mise à jour a échoué, rien n'a changé",
		"update.outcome.rolled_back":     "La mise à jour a échoué, version précédente remise",
		"update.outcome.rollback_failed": "La mise à jour a échoué, et la version précédente ne repart pas",
	},
	"en": {
		"updates.title":                  "Updates",
		"updates.lead":                   "Each machine updates itself: eriphe-core first, then the other machines, never while a task is at work. A machine that does not come back after its update goes back to its previous version.",
		"updates.all":                    "Update all",
		"updates.allconfirm":             "Update all machines? eriphe-core first, then the others, each once its tasks are done.",
		"updates.nothing":                "No machine to update.",
		"updates.night":                  "Update automatically at night",
		"updates.nighthint":              "All machines, between 3 and 5 am, when no task is at work.",
		"updates.nighton":                "The machines will update at night.",
		"updates.nightoff":               "The machines will no longer update by themselves.",
		"updates.help":                   "To go back to the previous version or stay on one, run the machine's install command again with --rollback or --version <version> (-Rollback, -Version on Windows).",
		"updates.now":                    "Update",
		"updates.aftercore":              "After eriphe-core",
		"updates.manual":                 "This machine does not update itself. Run this command on it: its data and settings stay.",
		"updates.noversion":              "unknown version",
		"updates.pinned":                 "pinned to %s",
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
		"update.outcome.refused":         "Update refused",
		"update.outcome.skipped":         "Update skipped: this version failed here before",
		"update.outcome.failed":          "The update failed, nothing changed",
		"update.outcome.rolled_back":     "The update failed, previous version put back",
		"update.outcome.rollback_failed": "The update failed, and the previous version does not start",
	},
}
