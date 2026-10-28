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
		"welcome.title":        "Bienvenue dans Eriphe",
		"welcome.step.account": "Ton espace est créé",
		"welcome.step.agent":   "Relie l'IA d'Eriphe",
		"welcome.step.start":   "Eriphe démarre",
		"welcome.agenttitle":   "Avec quelle IA Eriphe réfléchit-il ?",
		"welcome.agentlead":    "Eriphe utilise ton compte d'IA pour te répondre et faire travailler ses agents. Le plus simple : ton abonnement Claude.",
		"welcome.otherkind":    "Utiliser un autre compte (ChatGPT, clé d'API…)",
		"welcome.agentok":      "Eriphe réfléchit avec %s.",
		"welcome.ready":        "Eriphe est prêt. Dis-lui bonjour !",
		"welcome.open":         "Commencer la conversation",
		"welcome.nomachine":    "Eriphe attend une machine pour démarrer. L'installation ajoute celle-ci d'habitude ; sinon, ajoutes-en une.",
		"welcome.starting":     "Eriphe démarre sur ta machine… Cela prend une minute la première fois.",
		"welcome.change":       "Tu pourras changer d'IA plus tard dans Réglages → Agents.",
		"welcome.skip":         "Plus tard",
		"machines.localurl":    "Cette adresse ne marche que sur cet ordinateur. Pour une autre machine, ouvre l'espace avec l'adresse de cet ordinateur sur le réseau (par exemple https://192.168.0.20:8443) et génère la commande à nouveau.",
	},
	"en": {
		"welcome.title":        "Welcome to Eriphe",
		"welcome.step.account": "Your workspace is ready",
		"welcome.step.agent":   "Connect Eriphe's AI",
		"welcome.step.start":   "Eriphe starts",
		"welcome.agenttitle":   "Which AI does Eriphe think with?",
		"welcome.agentlead":    "Eriphe uses your AI account to answer you and to put its agents to work. The simplest: your Claude subscription.",
		"welcome.otherkind":    "Use another account (ChatGPT, an API key…)",
		"welcome.agentok":      "Eriphe thinks with %s.",
		"welcome.ready":        "Eriphe is ready. Say hello!",
		"welcome.open":         "Start the conversation",
		"welcome.nomachine":    "Eriphe waits for a machine to start on. The installer usually adds this one; otherwise, add one.",
		"welcome.starting":     "Eriphe is starting on your machine… The first time takes a minute.",
		"welcome.change":       "You can change the AI later in Settings → Agents.",
		"welcome.skip":         "Later",
		"machines.localurl":    "This address works on this computer only. For another machine, open the workspace with this computer's address on the network (e.g. https://192.168.0.20:8443) and generate the command again.",
	},
}
