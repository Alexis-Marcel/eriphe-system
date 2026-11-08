package main

// The words of Settings → Channels (web_channels.go, templates/channels.html).

func init() {
	for lang, words := range channelStrings {
		for k, v := range words {
			webStrings[lang][k] = v
		}
	}
}

var channelStrings = map[string]map[string]string{
	"fr": {
		"settings.channels":       "Canaux",
		"chan.lead":               "Ce par quoi tu parles à {{name}} et par quoi il te prévient. Les outils agissent ; les canaux ne portent que votre conversation, chacun avec sa propre connexion et son propre secret.",
		"chan.name.workspace":     "L'espace et le terminal",
		"chan.name.push":          "Notifications sur tes appareils",
		"chan.name.telegram":      "Telegram",
		"chan.name.voice":         "La voix de la maison",
		"chan.dir.in":             "Tu lui parles",
		"chan.dir.out":            "Il te prévient",
		"chan.dir.both":           "Dans les deux sens",
		"chan.speaker.user":       "C'est toi : ta session ou ton jeton.",
		"chan.speaker.account":    "C'est toi : ton compte, vérifié par un code.",
		"chan.speaker.room":       "Quelqu'un dans la pièce, peut-être pas toi : ce qu'il ferait part sur une carte que tu confirmes par écrit.",
		"chan.carries.text":       "Porte le texte des notifications.",
		"chan.carries.sentence":   "Ne dit qu'une phrase écrite par {{name}}, jamais le texte d'une notification.",
		"chan.ready":              "Prêt",
		"chan.notset":             "Pas configuré",
		"chan.setup":              "Configurer",
		"chan.voice.lead":         "Home Assistant parle à {{name}} par son agent de conversation, et dit sur tes enceintes ce que {{name}} a pour toi, avec un seul jeton (l'intégration Eriphe, ha/).",
		"chan.voice.listening":    "Home Assistant écoute : les enceintes peuvent parler.",
		"chan.voice.deaf":         "Home Assistant n'écoute pas en ce moment : sans l'intégration Eriphe à jour et reliée, les enceintes restent muettes.",
		"chan.voice.speakers":     "Quelle enceinte est dans quelle pièce :",
		"chan.voice.speakerslink": "Ce que {{name}} sait de toi",
		"chan.voice.nospeaker":    "Aucune enceinte reliée à une pièce pour l'instant :",
		"ha.voicemoved":           "La voix de la maison est un canal : son jeton est dans",
	},
	"en": {
		"settings.channels":       "Channels",
		"chan.lead":               "How you talk to {{name}} and how it tells you things. Tools act; channels only carry your conversation, each with its own connection and its own secret.",
		"chan.name.workspace":     "The workspace and the CLI",
		"chan.name.push":          "Notifications on your devices",
		"chan.name.telegram":      "Telegram",
		"chan.name.voice":         "The house's voice",
		"chan.dir.in":             "You talk to it",
		"chan.dir.out":            "It tells you",
		"chan.dir.both":           "Both ways",
		"chan.speaker.user":       "It is you: your session or token.",
		"chan.speaker.account":    "It is you: your account, verified with a code.",
		"chan.speaker.room":       "Someone in the room, maybe not you: anything it would change goes on a card you confirm in writing.",
		"chan.carries.text":       "Carries the notifications' text.",
		"chan.carries.sentence":   "Says only a sentence written by {{name}}, never a notification's text.",
		"chan.ready":              "Ready",
		"chan.notset":             "Not set up",
		"chan.setup":              "Set up",
		"chan.voice.lead":         "Home Assistant talks to {{name}} through its conversation agent, and says on your speakers what {{name}} has for you, on one token (the Eriphe integration, ha/).",
		"chan.voice.listening":    "Home Assistant listens: the speakers can speak.",
		"chan.voice.deaf":         "Home Assistant is not listening now: without the Eriphe integration up to date and connected, the speakers stay silent.",
		"chan.voice.speakers":     "Which speaker is in which room:",
		"chan.voice.speakerslink": "What {{name}} knows about you",
		"chan.voice.nospeaker":    "No speaker tied to a room yet:",
		"ha.voicemoved":           "The house's voice is a channel: its token is in",
	},
}
