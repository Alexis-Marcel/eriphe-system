package main

// Discord's own words (discord.go); the rest are a bot channel's
// (web_text_bot.go).

func init() {
	for lang, words := range discordStrings {
		for k, v := range words {
			webStrings[lang][k] = v
		}
	}
}

var discordStrings = map[string]map[string]string{
	"fr": {
		"acc.kind.discord":      "Discord",
		"acc.kind.discord.desc": "Parle à {{name}} et reçois ses messages en message privé sur Discord.",
		"discord.how":           "Dans le portail développeur de Discord (discord.com/developers), crée une application, puis dans Bot, réinitialise le jeton et colle-le ici. Il est gardé chiffré dans le coffre et ne s'affiche plus jamais. Discord ne laisse un bot t'écrire que si vous partagez un serveur : tu l'ajouteras ensuite à l'un des tiens.",
		"discord.privacy":       "Discord ne chiffre pas de bout en bout les messages privés avec un bot : ses serveurs voient ce que {{name}} t'y écrit et ce que tu lui réponds. Choisis « alertes seulement » dans Notifications pour n'y recevoir qu'un signal et un lien.",
	},
	"en": {
		"acc.kind.discord":      "Discord",
		"acc.kind.discord.desc": "Talk to {{name}} and get its messages as direct messages on Discord.",
		"discord.how":           "In Discord's Developer Portal (discord.com/developers), create an application, then under Bot, reset the token and paste it here. It is kept encrypted in the vault and never shown again. Discord lets a bot write to you only when you share a server: you will add it to one of yours next.",
		"discord.privacy":       "Discord does not encrypt direct messages with a bot end to end: its servers see what {{name}} writes to you there and what you answer. Choose “alerts only” in Notifications to get just a nudge and a link.",
	},
}
