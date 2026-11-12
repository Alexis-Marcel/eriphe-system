package main

// The words of the composer's panel in Settings → Agents
// (web_composition.go, templates/agents.html).

func init() {
	for lang, words := range composerStrings {
		for k, v := range words {
			webStrings[lang][k] = v
		}
	}
}

var composerStrings = map[string]map[string]string{
	"fr": {
		"composer.title":           "Compositeur de l'espace",
		"composer.lead":            "Il choisit ce que l'espace te montre et en quelle taille, à partir de ce qui t'attend : quelques secondes à chaque changement, à ton retour et quand tu écris. Sans lui, l'espace se range par importance.",
		"composer.on":              "Composer avec une IA",
		"composer.onhint":          "Chaque composition est un court tour de l'agent choisi. Éteint, rien n'est envoyé à une IA.",
		"composer.agenthint":       "Sonnet conseillé : rapide et exact. Un modèle plus petit invente des objets, que le code retire.",
		"composer.noagent":         "Aucun agent ne peut composer : ajoute d'abord un compte IA et un agent.",
		"composer.saved":           "Compositeur enregistré.",
		"composer.lastby":          "Dernière composition par l'IA,",
		"composer.lastfallback":    "Dernière composition par importance,",
		"assistants.role.composer": "compositeur",
	},
	"en": {
		"composer.title":           "Workspace composer",
		"composer.lead":            "It picks what the workspace shows you and how big, from what waits for you: a few seconds on each change, on your return and when you write. Without it, the workspace is ordered by importance.",
		"composer.on":              "Compose with an AI",
		"composer.onhint":          "Each composition is a short turn of the chosen agent. Off, nothing is sent to an AI.",
		"composer.agenthint":       "Sonnet recommended: fast and exact. A smaller model invents objects, which the code removes.",
		"composer.noagent":         "No agent can compose: add an AI account and an agent first.",
		"composer.saved":           "Composer saved.",
		"composer.lastby":          "Last composition by the AI,",
		"composer.lastfallback":    "Last composition by importance,",
		"assistants.role.composer": "composer",
	},
}
