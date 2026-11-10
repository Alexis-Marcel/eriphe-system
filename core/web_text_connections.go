package main

// The words of connected accounts and of a tool's places
// (web_connections.go, web_places.go, templates/places.html).

func init() {
	for lang, words := range connectionStrings {
		for k, v := range words {
			webStrings[lang][k] = v
		}
	}
}

var connectionStrings = map[string]map[string]string{
	"fr": {
		"conn.reconnect":     "Reconnecter",
		"conn.toreconnect":   "À reconnecter",
		"conn.rowreconnect":  "Le compte %s %s est à reconnecter : cet outil est en pause.",
		"conn.remove":        "Retirer ce compte",
		"conn.removeconfirm": "Retirer le compte %s ? Ses outils sont retirés, et son autorisation est révoquée chez son fournisseur.",
		"conn.places":        "Lieux",

		"places.title":      "Lieux",
		"places.heading":    "Lieux de %s",
		"places.lead":       "Ce que {{name}} peut atteindre dans cet outil, chaque lieu avec son droit. Une tâche à qui tu prêtes un lieu reçoit l'outil, qui atteint tous les lieux donnés ici, chacun avec son droit.",
		"places.given":      "Donnés à {{name}}",
		"places.none":       "Aucun lieu pour l'instant : choisis-en ci-dessous.",
		"places.pick":       "Choisir des lieux",
		"places.notready":   "L'outil démarre : les lieux se choisissent quand il est prêt.",
		"places.noaccount":  "Aucun compte connecté à cet outil.",
		"places.accounts":   "Compte :",
		"places.top":        "Début",
		"places.open":       "Ouvrir",
		"places.give":       "Donner en lecture",
		"places.givewrite":  "Lecture + dépôt",
		"places.allowwrite": "Permettre le dépôt",
		"places.readonly":   "Lecture seule",
		"places.empty":      "Rien ici.",
		"places.more":       "Plus",

		"places.access.read": "Lecture",
		"places.access.drop": "Lecture + dépôt",
	},
	"en": {
		"conn.reconnect":     "Reconnect",
		"conn.toreconnect":   "To reconnect",
		"conn.rowreconnect":  "The %s account %s must be reconnected: this tool is paused.",
		"conn.remove":        "Remove this account",
		"conn.removeconfirm": "Remove the account %s? Its tools are removed, and its authorization is revoked at its provider.",
		"conn.places":        "Places",

		"places.title":      "Places",
		"places.heading":    "Places of %s",
		"places.lead":       "What {{name}} can reach in this tool, each place with its own right. A task you lend a place gets the tool, which reaches every place given here, each with its own right.",
		"places.given":      "Given to {{name}}",
		"places.none":       "No place yet: pick some below.",
		"places.pick":       "Pick places",
		"places.notready":   "The tool is starting: places are picked once it is ready.",
		"places.noaccount":  "No account is connected to this tool.",
		"places.accounts":   "Account:",
		"places.top":        "Top",
		"places.open":       "Open",
		"places.give":       "Give to read",
		"places.givewrite":  "Read + drop",
		"places.allowwrite": "Allow drops",
		"places.readonly":   "Read only",
		"places.empty":      "Nothing here.",
		"places.more":       "More",

		"places.access.read": "Read",
		"places.access.drop": "Read + drop",
	},
}
