package main

// The words of the errors (problems.go), of the assistant's error card in
// the conversation, of the accounts' and agents' real statuses, and of the
// events of the conversation shown to the user.

func init() {
	for lang, words := range problemStrings {
		for k, v := range words {
			webStrings[lang][k] = v
		}
	}
}

var problemStrings = map[string]map[string]string{
	"fr": {
		"problem.auth":    "Le service a refusé la clé ou le jeton : il est faux, expiré ou révoqué.",
		"problem.quota":   "Le service a atteint sa limite : quota ou crédit épuisé, ou trop de demandes à la fois.",
		"problem.model":   "Le modèle demandé n'est pas disponible avec ce compte.",
		"problem.network": "Le service est injoignable : vérifie la connexion à Internet de l'appareil (pare-feu, proxy).",
		"problem.server":  "Le service a un problème passager : réessaie dans un instant.",
		"problem.pending": "Il démarre : il sera prêt dans un instant.",
		"problem.offline": "Son appareil est hors ligne : il reprendra quand l'appareil sera de retour.",
		"problem.nonode":  "Aucun appareil ne peut le lancer pour l'instant.",
		"problem.unknown": "Une erreur inattendue est survenue.",

		"problem.brain.auth":    "Le compte IA de {{name}} a refusé sa clé ou son jeton : il est faux, expiré ou révoqué.",
		"problem.brain.quota":   "Le compte IA de {{name}} a atteint sa limite : quota ou crédit épuisé, ou trop de demandes à la fois.",
		"problem.brain.model":   "Le modèle de {{name}} n'est pas disponible avec son compte IA, ou sa clé ne permet pas de l'utiliser.",
		"problem.brain.network": "Le service d'IA est injoignable depuis l'appareil où tourne {{name}} : vérifie sa connexion à Internet (pare-feu, proxy).",
		"problem.brain.server":  "Le service d'IA a un problème passager : réessaie dans un instant.",
		"problem.brain.unknown": "{{name}} a rencontré une erreur inattendue.",
		"problem.brain.start":   "Son agent s'arrête dès le démarrage : vérifie son compte IA, ou l'appareil qui le fait tourner.",

		"problem.task.auth":    "Le compte IA de l'agent a refusé sa clé : elle est fausse, expirée ou révoquée.",
		"problem.task.quota":   "Le compte IA de l'agent a atteint sa limite : quota ou crédit épuisé, ou trop de demandes à la fois.",
		"problem.task.model":   "L'agent n'a pas pu utiliser son modèle : la clé de son compte IA est peut-être absente ou refusée, ou le modèle n'existe plus.",
		"problem.task.network": "Le service d'IA est injoignable depuis l'appareil de la tâche : vérifie sa connexion à Internet.",
		"problem.task.unknown": "La tâche a rencontré une erreur.",

		"problem.tool.auth":    "Le service a refusé la connexion : reconnecte-le, ou change son jeton.",
		"problem.tool.network": "Le service est injoignable : vérifie son adresse, et qu'il est en marche.",
		"problem.tool.pending": "Il démarre sur son appareil : il sera prêt dans un instant.",
		"problem.tool.unknown": "Le service ne répond pas comme prévu.",

		"problem.account.auth":    "La clé a été refusée : elle est fausse, expirée ou révoquée.",
		"problem.account.quota":   "Le compte a atteint sa limite : quota ou crédit épuisé.",
		"problem.account.model":   "Un modèle de ce compte n'est pas disponible avec cette clé.",
		"problem.account.network": "La clé n'a pas pu être vérifiée : le service était injoignable.",
		"problem.account.server":  "La clé n'a pas pu être vérifiée : le service avait un problème passager.",
		"problem.account.unknown": "La clé n'a pas pu être vérifiée.",

		"problem.checkaccount": "Vérifier le compte IA",
		"problem.details":      "Détails",
		"problem.retry":        "Réessayer",

		"chat.error.title":      "{{name}} n'a pas pu répondre",
		"chat.error.starttitle": "{{name}} n'arrive pas à démarrer",
		"chat.error.retried":    "Nouvel essai demandé.",

		"health.refused":       "Clé refusée",
		"health.unverified":    "Non vérifiée",
		"health.down":          "Indisponible",
		"health.fix":           "Modifier la clé",
		"providers.createdbad": "Le compte « %s » est ajouté, mais sa clé n'a pas passé le test. %s Ses agents sont marqués comme tels tant qu'elle ne marche pas.",
		"providers.savedbad":   "Le compte « %s » est enregistré, mais sa clé n'a pas passé le test. %s",

		"event.task.failed":  "La tâche « %s » a échoué.",
		"event.task.stopped": "La tâche « %s » a été arrêtée.",
		"event.task.lost":    "La tâche « %s » s'est interrompue : son appareil ne répond plus.",
		"event.task.see":     "Voir",
	},
	"en": {
		"problem.auth":    "The service refused the key or token: it is wrong, expired or revoked.",
		"problem.quota":   "The service reached its limit: quota or credit used up, or too many requests at once.",
		"problem.model":   "The model asked for is not available with this account.",
		"problem.network": "The service cannot be reached: check the device's Internet connection (firewall, proxy).",
		"problem.server":  "The service has a passing problem: try again in a moment.",
		"problem.pending": "It is starting: it will be ready in a moment.",
		"problem.offline": "Its device is offline: it will resume when the device is back.",
		"problem.nonode":  "No device can run it for now.",
		"problem.unknown": "An unexpected error happened.",

		"problem.brain.auth":    "{{name}}'s AI account refused its key or token: it is wrong, expired or revoked.",
		"problem.brain.quota":   "{{name}}'s AI account reached its limit: quota or credit used up, or too many requests at once.",
		"problem.brain.model":   "{{name}}'s model is not available with its AI account, or its key cannot use it.",
		"problem.brain.network": "The AI service cannot be reached from the device {{name}} runs on: check its Internet connection (firewall, proxy).",
		"problem.brain.server":  "The AI service has a passing problem: try again in a moment.",
		"problem.brain.unknown": "{{name}} ran into an unexpected error.",
		"problem.brain.start":   "Its agent stops as soon as it starts: check its AI account, or the device it runs on.",

		"problem.task.auth":    "The agent's AI account refused its key: it is wrong, expired or revoked.",
		"problem.task.quota":   "The agent's AI account reached its limit: quota or credit used up, or too many requests at once.",
		"problem.task.model":   "The agent could not use its model: its AI account's key may be missing or refused, or the model no longer exists.",
		"problem.task.network": "The AI service cannot be reached from the task's device: check its Internet connection.",
		"problem.task.unknown": "The task ran into an error.",

		"problem.tool.auth":    "The service refused the connection: reconnect it, or change its token.",
		"problem.tool.network": "The service cannot be reached: check its address, and that it is running.",
		"problem.tool.pending": "It is starting on its device: it will be ready in a moment.",
		"problem.tool.unknown": "The service does not answer as expected.",

		"problem.account.auth":    "The key was refused: it is wrong, expired or revoked.",
		"problem.account.quota":   "The account reached its limit: quota or credit used up.",
		"problem.account.model":   "A model of this account is not available with this key.",
		"problem.account.network": "The key could not be checked: the service could not be reached.",
		"problem.account.server":  "The key could not be checked: the service had a passing problem.",
		"problem.account.unknown": "The key could not be checked.",

		"problem.checkaccount": "Check the AI account",
		"problem.details":      "Details",
		"problem.retry":        "Try again",

		"chat.error.title":      "{{name}} could not answer",
		"chat.error.starttitle": "{{name}} cannot start",
		"chat.error.retried":    "Trying again.",

		"health.refused":       "Key refused",
		"health.unverified":    "Not verified",
		"health.down":          "Unavailable",
		"health.fix":           "Change the key",
		"providers.createdbad": "The account “%s” is added, but its key did not pass the test. %s Its agents are marked so until it works.",
		"providers.savedbad":   "The account “%s” is saved, but its key did not pass the test. %s",

		"event.task.failed":  "The task “%s” failed.",
		"event.task.stopped": "The task “%s” was stopped.",
		"event.task.lost":    "The task “%s” was interrupted: its device no longer answers.",
		"event.task.see":     "See",
	},
}
