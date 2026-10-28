package main

// The words of Settings → Devices: what Eriphe sees and can do on each
// device, its access log and "Cut access" (web_devices.go, node_access.go).

func init() {
	for lang, words := range deviceStrings {
		for k, v := range words {
			webStrings[lang][k] = v
		}
	}
}

var deviceStrings = map[string]map[string]string{
	"fr": {
		"devices.sees":          "Ce qu'Eriphe voit ici",
		"devices.seesnone":      "Rien d'ouvert",
		"devices.access.read":   "Lecture",
		"devices.access.drop":   "Lecture + dépôt",
		"devices.sharefolder":   "Partager un dossier",
		"devices.can":           "Ce qu'Eriphe peut faire ici",
		"devices.agents":        "Faire travailler des agents",
		"devices.agentsnohint":  "Eriphe ne lance rien sur cet appareil : il en lit seulement les dossiers ouverts.",
		"devices.yes":           "Oui",
		"devices.no":            "Non",
		"devices.open":          "Détail et journal",
		"devices.access":        "Accès d'Eriphe",
		"devices.accesslead":    "Couper l'accès arrête tout de suite ce qu'Eriphe fait ici : plus de travail, plus aucun dossier lu, plus de connexion, jusqu'à ce que tu le rétablisses. Rien n'est désinstallé.",
		"devices.cut":           "Accès coupé",
		"devices.cutlead":       "Eriphe ne voit et ne fait plus rien sur cet appareil, et refuse sa connexion. Rien n'est désinstallé : rétablis l'accès quand tu veux.",
		"devices.cutaction":     "Couper l'accès",
		"devices.cutconfirm":    "Couper l'accès de %s ? Eriphe n'y envoie plus de travail, n'y lit plus aucun dossier et refuse sa connexion jusqu'à ce que tu le rétablisses. Ses tâches en cours s'arrêtent. Rien n'est désinstallé.",
		"devices.restore":       "Rétablir l'accès",
		"devices.log":           "Journal des accès",
		"devices.loglead":       "Ce qu'Eriphe a fait sur cet appareil, sur les 90 derniers jours.",
		"devices.lognone":       "Eriphe n'a encore rien fait ici.",
		"devices.logwhat":       "Quoi",
		"devices.logdetail":     "Détail",
		"devices.log.list":      "A parcouru un dossier",
		"devices.log.share":     "Dossier ouvert à Eriphe",
		"devices.log.unshare":   "Dossier refermé",
		"devices.log.task":      "A lancé une tâche",
		"devices.log.lend":      "A prêté un dossier à une tâche",
		"devices.log.result":    "A déposé un résultat",
		"devices.log.cut":       "Accès coupé",
		"devices.log.restore":   "Accès rétabli",
		"folders.err.suspended": "L'accès à cet appareil est coupé : rétablis-le dans Réglages → Appareils.",
	},
	"en": {
		"devices.sees":          "What Eriphe sees here",
		"devices.seesnone":      "Nothing open",
		"devices.access.read":   "Read",
		"devices.access.drop":   "Read + drop",
		"devices.sharefolder":   "Share a folder",
		"devices.can":           "What Eriphe can do here",
		"devices.agents":        "Put agents to work",
		"devices.agentsnohint":  "Eriphe runs nothing on this device: it only reads the folders opened there.",
		"devices.yes":           "Yes",
		"devices.no":            "No",
		"devices.open":          "Details and log",
		"devices.access":        "Eriphe's access",
		"devices.accesslead":    "Cutting access stops at once what Eriphe does here: no more work, no folder read, no connection, until you restore it. Nothing is uninstalled.",
		"devices.cut":           "Access cut",
		"devices.cutlead":       "Eriphe no longer sees or does anything on this device, and refuses its connection. Nothing is uninstalled: restore access whenever you like.",
		"devices.cutaction":     "Cut access",
		"devices.cutconfirm":    "Cut access to %s? Eriphe sends it no more work, reads none of its folders and refuses its connection until you restore it. Its running tasks stop. Nothing is uninstalled.",
		"devices.restore":       "Restore access",
		"devices.log":           "Access log",
		"devices.loglead":       "What Eriphe did on this device, over the last 90 days.",
		"devices.lognone":       "Eriphe has done nothing here yet.",
		"devices.logwhat":       "What",
		"devices.logdetail":     "Detail",
		"devices.log.list":      "Browsed a folder",
		"devices.log.share":     "Folder opened to Eriphe",
		"devices.log.unshare":   "Folder closed",
		"devices.log.task":      "Ran a task",
		"devices.log.lend":      "Lent a folder to a task",
		"devices.log.result":    "Dropped a result",
		"devices.log.cut":       "Access cut",
		"devices.log.restore":   "Access restored",
		"folders.err.suspended": "Access to this device is cut: restore it in Settings → Devices.",
	},
}
