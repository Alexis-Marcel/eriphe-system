package main

// The words of "Add a tool" in two steps (web_tools_dialog.go): the
// back link, the toast and the row's line after a save that follow the
// tool, the step on the way to an OAuth service, and Google Drive's step.

func init() {
	for lang, words := range addFlowStrings {
		for k, v := range words {
			webStrings[lang][k] = v
		}
	}
}

var addFlowStrings = map[string]map[string]string{
	"fr": {
		"step.back":           "Retour",
		"saved.added":         "« %s » ajouté",
		"saved.edited":        "« %s » modifié",
		"saved.connected":     "« %s » connecté",
		"added.ready":         "Prêt — {{name}} peut l'utiliser.",
		"added.connecting":    "Connexion…",
		"added.startingon":    "Démarrage sur %s…",
		"added.failedhint":    "Regarde cet appareil dans Réglages → Appareils, ou modifie l'outil.",
		"added.needsauth":     "Il reste à autoriser {{name}} sur ce service.",
		"added.needsauthhint": "Utilise « Connecter », sur cette ligne.",
		"added.error":         "{{name}} n'arrive pas à se connecter à cet outil.",
		"added.errorhint":     "Vérifie son adresse et sa clé, puis modifie-le. Il réessaie de lui-même en attendant.",
		"added.refused":       "L'outil est prêt, mais ce compte ne se connecte pas.",
		"added.refusedhint":   "Vérifie l'identifiant et le mot de passe, puis modifie le compte.",
		"added.details":       "Détails techniques",
		"tools.notyet":        "Pas encore autorisé : utilise « Connecter » pour que {{name}} y ait accès.",
		"tools.expired":       "L'autorisation sur ce service a expiré, a été retirée ou ne suffit plus : reconnecte-le.",
		"leaving.title":       "Direction %s",
		"leaving.lead":        "%s te demande d'autoriser Eriphe. Une fois fait, tu reviens ici et {{name}} vérifie la connexion.",
		"leaving.go":          "Continuer vers %s",
	},
	"en": {
		"step.back":           "Back",
		"saved.added":         "“%s” added",
		"saved.edited":        "“%s” changed",
		"saved.connected":     "“%s” connected",
		"added.ready":         "Ready — {{name}} can use it.",
		"added.connecting":    "Connecting…",
		"added.startingon":    "Starting on %s…",
		"added.failedhint":    "Look at this device in Settings → Devices, or change the tool.",
		"added.needsauth":     "{{name}} still has to be authorized on this service.",
		"added.needsauthhint": "Use “Connect”, on this row.",
		"added.error":         "{{name}} cannot connect to this tool.",
		"added.errorhint":     "Check its address and key, then change it. It keeps trying meanwhile.",
		"added.refused":       "The tool is ready, but this account does not sign in.",
		"added.refusedhint":   "Check the login and the password, then change the account.",
		"added.details":       "Technical details",
		"tools.notyet":        "Not authorized yet: use “Connect” for {{name}} to have access.",
		"tools.expired":       "The authorization on this service expired, was revoked or no longer suffices: reconnect it.",
		"leaving.title":       "Off to %s",
		"leaving.lead":        "%s asks you to authorize Eriphe. Once done, you come back here and {{name}} checks the connection.",
		"leaving.go":          "Continue to %s",
	},
}
