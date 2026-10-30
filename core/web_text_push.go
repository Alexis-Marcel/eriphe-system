package main

// The words of Settings → App & notifications (web_push.go, templates/push.html).

func init() {
	for lang, words := range pushStrings {
		for k, v := range words {
			webStrings[lang][k] = v
		}
	}
}

var pushStrings = map[string]map[string]string{
	"fr": {
		"settings.notifications": "Appli et notifications",
		"push.lead":              "Installe {{name}} sur ton téléphone comme une appli, et laisse-le te prévenir quand une tâche se termine ou qu'il attend ta réponse.",
		"install.title":          "Installer l'appli",
		"install.done":           "Installée",
		"install.lead":           "{{name}} s'ouvre alors en plein écran, depuis son icône, comme une application.",
		"install.button":         "Installer l'appli",
		"install.ios":            "Dans Safari, touche Partager, puis « Sur l'écran d'accueil ».",
		"install.android":        "Dans Chrome, ouvre le menu ⋮, puis « Installer l'application » ou « Ajouter à l'écran d'accueil ».",
		"install.computer":       "Ordinateur",
		"install.desktop":        "Dans Chrome ou Edge, l'icône d'installation au bout de la barre d'adresse.",
		"push.title":             "Notifications sur cet appareil",
		"push.toggle":            "Me prévenir sur cet appareil",
		"push.toggle.hint":       "Quand une tâche se termine ou échoue, et quand {{name}} attend ta réponse. Rien tant que l'espace est ouvert devant toi.",
		"push.unsupported":       "Ce navigateur ne reçoit pas de notifications.",
		"push.insecure":          "Les notifications demandent une adresse HTTPS reconnue par le navigateur : celle de l'accès à distance, ou de ton propre domaine.",
		"push.ios":               "Sur iPhone et iPad, les notifications arrivent dans l'appli installée seulement (iOS 16.4 ou plus) : installe-la, ouvre-la depuis son icône, puis reviens ici.",
		"push.denied":            "Les notifications sont bloquées pour ce site : autorise-les dans les réglages du navigateur, puis réessaie.",
		"push.failed":            "L'abonnement n'a pas abouti. Réessaie dans un instant.",
		"push.off":               "Les notifications ne sont pas disponibles sur ce serveur.",
		"push.on":                "Cet appareil reçoit les notifications.",
		"push.ioshint":           "Sur iPhone et iPad, il faut l'appli installée (iOS 16.4 ou plus) ; sur Android et ordinateur, le navigateur suffit.",
		"push.test":              "Envoyer un essai",
		"push.test.body":         "Les notifications arrivent bien sur cet appareil.",
		"push.test.sent":         "Essai envoyé.",
		"push.test.none":         "Active d'abord les notifications sur cet appareil.",
		"push.devices":           "Appareils prévenus",
		"push.none":              "Aucun appareil pour l'instant.",
		"push.this":              "cet appareil",
		"push.since":             "Depuis le %s",
		"push.remove":            "Retirer",
		"push.app":               "appli",
		"push.device":            "Appareil",
	},
	"en": {
		"settings.notifications": "App & notifications",
		"push.lead":              "Install {{name}} on your phone as an app, and let it tell you when a task ends or when it waits for your answer.",
		"install.title":          "Install the app",
		"install.done":           "Installed",
		"install.lead":           "{{name}} then opens full screen, from its icon, like an app.",
		"install.button":         "Install the app",
		"install.ios":            "In Safari, tap Share, then “Add to Home Screen”.",
		"install.android":        "In Chrome, open the ⋮ menu, then “Install app” or “Add to Home screen”.",
		"install.computer":       "Computer",
		"install.desktop":        "In Chrome or Edge, the install icon at the end of the address bar.",
		"push.title":             "Notifications on this device",
		"push.toggle":            "Notify me on this device",
		"push.toggle.hint":       "When a task ends or fails, and when {{name}} waits for your answer. Nothing while the workspace is open in front of you.",
		"push.unsupported":       "This browser does not receive notifications.",
		"push.insecure":          "Notifications need an HTTPS address the browser trusts: the remote access one, or your own domain.",
		"push.ios":               "On iPhone and iPad, notifications reach the installed app only (iOS 16.4 or later): install it, open it from its icon, then come back here.",
		"push.denied":            "Notifications are blocked for this site: allow them in the browser's settings, then try again.",
		"push.failed":            "The subscription did not go through. Try again in a moment.",
		"push.off":               "Notifications are not available on this server.",
		"push.on":                "This device receives the notifications.",
		"push.ioshint":           "On iPhone and iPad, the installed app is needed (iOS 16.4 or later); on Android and computers, the browser is enough.",
		"push.test":              "Send a test",
		"push.test.body":         "Notifications do reach this device.",
		"push.test.sent":         "Test sent.",
		"push.test.none":         "Turn the notifications on for this device first.",
		"push.devices":           "Devices notified",
		"push.none":              "No device yet.",
		"push.this":              "this device",
		"push.since":             "Since %s",
		"push.remove":            "Remove",
		"push.app":               "app",
		"push.device":            "Device",
	},
}
