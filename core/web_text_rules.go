package main

// The words of the rules (rule.go, web_rules.go, templates/rules.html)
// and of their runs in the feed of what happened (since.go).

func init() {
	for lang, words := range watchStrings {
		for k, v := range words {
			webStrings[lang][k] = v
		}
	}
}

var watchStrings = map[string]map[string]string{
	"fr": {
		"settings.rules":      "Règles",
		"happened.kind.watch": "Règles",
	},
	"en": {
		"settings.rules":      "Rules",
		"happened.kind.watch": "Rules",
	},
}
