package main

// The words of the first-run wizard (web_welcome.go).

func init() {
	for lang, words := range welcomeStrings {
		for k, v := range words {
			webStrings[lang][k] = v
		}
	}
}

var welcomeStrings = map[string]map[string]string{
	"fr": {
		"welcome.title":        "Bienvenue",
		"welcome.steps":        "Étapes",
		"welcome.step.account": "Ton espace",
		"welcome.step.name":    "Son nom",
		"welcome.nametitle":    "Comment veux-tu appeler ton assistant ?",
		"welcome.namelead":     "C'est le nom sous lequel il se présente, ici et à la maison. Tu pourras le changer dans Réglages → Agents.",
		"welcome.namelabel":    "Son nom",
		"welcome.namehint":     "Jusqu'à 32 caractères.",
		"welcome.namebad":      "Ce nom ne convient pas : 1 à 32 caractères, lettres, chiffres, espaces, apostrophes ou tirets.",
		"welcome.namesubmit":   "Continuer",
		"welcome.step.agent":   "Son IA",
		"welcome.step.start":   "Démarrage",
		"welcome.agenttitle":   "Avec quelle IA {{name}} réfléchit-il ?",
		"welcome.agentlead":    "{{name}} utilise ton compte d'IA pour te répondre et faire travailler ses agents. Choisis celui que tu as : un abonnement, ou une clé API.",
		"welcome.otherkind":    "Choisir un autre compte",
		"welcome.agentok":      "{{name}} réfléchit avec %s.",
		"welcome.ready":        "{{name}} est prêt. Dis-lui bonjour !",
		"welcome.open":         "Commencer la conversation",
		"welcome.nomachine":    "{{name}} attend un appareil pour démarrer : aucun n'est connecté. Connectes-en un.",
		"welcome.starting":     "{{name}} démarre sur cette machine… Cela prend une minute la première fois.",
		"welcome.change":       "Tu pourras changer d'IA plus tard dans Réglages → Agents.",
		"welcome.skip":         "Terminer plus tard",
		"machines.localurl":    "Cette adresse ne marche que sur cet ordinateur. Pour un autre appareil, ouvre l'espace avec l'adresse de cet ordinateur sur le réseau (par exemple https://192.168.0.20:8443) et génère la commande à nouveau.",
	},
	"en": {
		"welcome.title":        "Welcome",
		"welcome.steps":        "Steps",
		"welcome.step.account": "Workspace",
		"welcome.step.name":    "Name",
		"welcome.nametitle":    "What do you want to call your assistant?",
		"welcome.namelead":     "It is the name it introduces itself by, here and at home. You can change it in Settings → Agents.",
		"welcome.namelabel":    "Its name",
		"welcome.namehint":     "Up to 32 characters.",
		"welcome.namebad":      "This name does not work: 1 to 32 characters, letters, digits, spaces, apostrophes or dashes.",
		"welcome.namesubmit":   "Continue",
		"welcome.step.agent":   "AI",
		"welcome.step.start":   "Start",
		"welcome.agenttitle":   "Which AI does {{name}} think with?",
		"welcome.agentlead":    "{{name}} uses your AI account to answer you and to put its agents to work. Pick the one you have: a subscription, or an API key.",
		"welcome.otherkind":    "Pick another account",
		"welcome.agentok":      "{{name}} thinks with %s.",
		"welcome.ready":        "{{name}} is ready. Say hello!",
		"welcome.open":         "Start the conversation",
		"welcome.nomachine":    "{{name}} waits for a device to start on: none is connected. Connect one.",
		"welcome.starting":     "{{name}} is starting on this machine… The first time takes a minute.",
		"welcome.change":       "You can change the AI later in Settings → Agents.",
		"welcome.skip":         "Finish later",
		"machines.localurl":    "This address works on this computer only. For another device, open the workspace with this computer's address on the network (e.g. https://192.168.0.20:8443) and generate the command again.",
	},
}
