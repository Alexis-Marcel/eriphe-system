package main

// Signal's own words (signal.go); the rest are a bot channel's
// (web_text_bot.go).

func init() {
	for lang, words := range signalStrings {
		for k, v := range words {
			webStrings[lang][k] = v
		}
	}
}

var signalStrings = map[string]map[string]string{
	"fr": {
		"acc.kind.signal":      "Signal",
		"acc.kind.signal.desc": "Parle à {{name}} et reçois ses messages dans Signal.",
		"signal.how":           "Signal n'a pas de bots : {{name}} y aura son propre compte, avec un numéro à lui, que tu n'utilises pas déjà dans Signal. Un fixe convient : Signal le vérifie par un appel. Activer Signal lance son service sur une de tes machines (Intel ou AMD), qui garde le compte et ses clés.",
		"signal.privacy":       "Les messages sont chiffrés de bout en bout jusqu'au service Signal d'Eriphe, qui les garde, avec les clés du compte, sur ta machine. Signal n'a pas de boutons : pour répondre à une question, réagis au message avec l'emoji indiqué (👍, 👎…).",
		"signal.add":           "Activer Signal",
		"signal.err.add":       "Le service Signal n'a pas pu être ajouté.",
		"signal.starting":      "Le service Signal démarre sur une de tes machines…",
		"signal.number.lead":   "Le numéro de {{name}} dans Signal, au format international. Signal y envoie un code par SMS, ou par un appel.",
		"signal.number":        "Numéro",
		"signal.voice":         "Par un appel (pour un fixe)",
		"signal.captcha.lead":  "Signal demande d'abord un captcha : résous-le sur",
		"signal.captcha":       "puis colle ici le lien « Open Signal » (clic droit, copier le lien)",
		"signal.register":      "Recevoir le code",
		"signal.code.lead":     "Signal envoie un code au %s.",
		"signal.code":          "Code",
		"signal.pin.old":       "PIN Signal du numéro, s'il en a déjà un",
		"signal.pin.new":       "Nouveau PIN pour protéger le numéro (conseillé)",
		"signal.pin.hint":      "Sans PIN, quelqu'un qui obtiendrait ce numéro pourrait réinscrire le compte. Garde-le : Signal le redemande parfois.",
		"signal.verify":        "Valider",
		"signal.again":         "Changer de numéro",
		"signal.registered":    "Numéro inscrit.",
		"signal.err.pin":       "Numéro inscrit, mais le PIN n'a pas été posé.",
		"signal.remove":        "Retirer Signal",
		"signal.removeconfirm": "Retirer Signal ? Le numéro est désinscrit de Signal, ses clés effacées et le compte délié.",
	},
	"en": {
		"acc.kind.signal":      "Signal",
		"acc.kind.signal.desc": "Talk to {{name}} and get its messages in Signal.",
		"signal.how":           "Signal has no bots: {{name}} gets an account of its own there, with a number of its own that you do not already use in Signal. A landline works: Signal verifies it by a call. Turning Signal on runs its service on one of your machines (Intel or AMD), which keeps the account and its keys.",
		"signal.privacy":       "Messages are end-to-end encrypted up to Eriphe's Signal service, which keeps them, with the account's keys, on your machine. Signal has no buttons: to answer a question, react to the message with the emoji shown (👍, 👎…).",
		"signal.add":           "Turn Signal on",
		"signal.err.add":       "The Signal service could not be added.",
		"signal.starting":      "The Signal service is starting on one of your machines…",
		"signal.number.lead":   "{{name}}'s number in Signal, in international format. Signal sends a code to it by SMS, or by a call.",
		"signal.number":        "Number",
		"signal.voice":         "By a call (for a landline)",
		"signal.captcha.lead":  "Signal asks for a captcha first: solve it at",
		"signal.captcha":       "then paste the “Open Signal” link here (right-click, copy link)",
		"signal.register":      "Get the code",
		"signal.code.lead":     "Signal sends a code to %s.",
		"signal.code":          "Code",
		"signal.pin.old":       "The number's Signal PIN, if it has one",
		"signal.pin.new":       "A new PIN to protect the number (advised)",
		"signal.pin.hint":      "Without a PIN, someone who got this number could register the account again. Keep it: Signal asks for it now and then.",
		"signal.verify":        "Confirm",
		"signal.again":         "Use another number",
		"signal.registered":    "Number registered.",
		"signal.err.pin":       "Number registered, but the PIN was not set.",
		"signal.remove":        "Remove Signal",
		"signal.removeconfirm": "Remove Signal? The number is unregistered from Signal, its keys deleted and the account unlinked.",
	},
}
