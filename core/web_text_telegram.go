package main

// Telegram's own words (telegram.go); the rest are a bot channel's
// (web_text_bot.go).

func init() {
	for lang, words := range telegramStrings {
		for k, v := range words {
			webStrings[lang][k] = v
		}
	}
}

var telegramStrings = map[string]map[string]string{
	"fr": {
		"acc.kind.telegram":      "Telegram",
		"acc.kind.telegram.desc": "Parle à {{name}} et reçois ses messages dans Telegram.",
		"telegram.how":           "Crée un bot avec @BotFather sur Telegram (commande /newbot), puis colle ici le jeton qu'il te donne. Il est gardé chiffré dans le coffre et ne s'affiche plus jamais.",
		"telegram.privacy":       "Telegram ne chiffre pas de bout en bout les conversations avec un bot : ses serveurs voient ce que {{name}} t'y écrit et ce que tu lui réponds. Choisis « alertes seulement » dans Notifications pour n'y recevoir qu'un signal et un lien.",
	},
	"en": {
		"acc.kind.telegram":      "Telegram",
		"acc.kind.telegram.desc": "Talk to {{name}} and get its messages in Telegram.",
		"telegram.how":           "Create a bot with @BotFather on Telegram (/newbot), then paste the token it gives you here. It is kept encrypted in the vault and never shown again.",
		"telegram.privacy":       "Telegram does not encrypt chats with a bot end to end: its servers see what {{name}} writes to you there and what you answer. Choose “alerts only” in Notifications to get just a nudge and a link.",
	},
}
