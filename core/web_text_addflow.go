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
		"added.details":       "Détails techniques",
		"tools.notyet":        "Pas encore autorisé : utilise « Connecter » pour que {{name}} y ait accès.",
		"tools.expired":       "L'autorisation sur ce service a expiré, a été retirée ou ne suffit plus : reconnecte-le.",
		"leaving.title":       "Direction %s",
		"leaving.lead":        "%s te demande d'autoriser Eriphe. Une fois fait, tu reviens ici et {{name}} vérifie la connexion.",
		"leaving.go":          "Continuer vers %s",
		"drivestep.needs":     "Il faut une app Google à toi, créée dans ta console Google Cloud (quelques minutes, pas à pas), ton accord sur le compte, puis les dossiers que tu donnes à {{name}}.",
		"drivestep.page":      "Tout cela tient sur une page à part, où tu reviens pour changer les dossiers.",
		"drivestep.go":        "Ouvrir le guide",
		"drivestep.manage":    "Gérer Google Drive",
		"ha.formhint":         "La voix de la maison (Assist) se règle sur la page de Home Assistant.",
		"ha.formlink":         "Page Home Assistant",
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
		"added.details":       "Technical details",
		"tools.notyet":        "Not authorized yet: use “Connect” for {{name}} to have access.",
		"tools.expired":       "The authorization on this service expired, was revoked or no longer suffices: reconnect it.",
		"leaving.title":       "Off to %s",
		"leaving.lead":        "%s asks you to authorize Eriphe. Once done, you come back here and {{name}} checks the connection.",
		"leaving.go":          "Continue to %s",
		"drivestep.needs":     "It takes a Google app of your own, made in your Google Cloud console (a few minutes, step by step), your consent on the account, then the folders you give to {{name}}.",
		"drivestep.page":      "All of it is on a page of its own, where you come back to change the folders.",
		"drivestep.go":        "Open the guide",
		"drivestep.manage":    "Manage Google Drive",
		"ha.formhint":         "The house's voice (Assist) is set on the Home Assistant page.",
		"ha.formlink":         "Home Assistant page",
	},
}
