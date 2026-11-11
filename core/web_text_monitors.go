package main

// The words of the monitors (monitors.go): their cards and how they are said.

func init() {
	for lang, words := range monitorStrings {
		for k, v := range words {
			webStrings[lang][k] = v
		}
	}
}

var monitorStrings = map[string]map[string]string{
	"fr": {
		"card.kind.monitor":    "Une surveillance",
		"card.monitor.new":     "Surveiller « %s » : à approuver avant qu'il ne regarde",
		"card.monitor.update":  "Changer la surveillance « %s » : à approuver",
		"card.monitor.what":    "Ce qu'il fait",
		"card.monitor.events":  "Ses événements",
		"card.monitor.key":     "clé %s : un skill y réagit, lui ne fait que détecter",
		"monitor.text.changed": "toutes les %s, appelle %s et note un changement de %s",
		"monitor.text.new":     "toutes les %s, appelle %s et note chaque nouvel élément de %s (repéré par %s)",
		"monitor.text.whole":   "son résultat",
		"monitor.text.above":   "toutes les %s, appelle %s et note quand %s passe au-dessus de %s, et quand il redescend",
		"monitor.text.below":   "toutes les %s, appelle %s et note quand %s passe en dessous de %s, et quand il remonte",
	},
	"en": {
		"card.kind.monitor":    "A monitor",
		"card.monitor.new":     "Monitor “%s”: to approve before it looks",
		"card.monitor.update":  "Change the monitor “%s”: to approve",
		"card.monitor.what":    "What it does",
		"card.monitor.events":  "Its events",
		"card.monitor.key":     "key %s: a skill reacts to them, it only detects",
		"monitor.text.changed": "every %s, calls %s and notes a change of %s",
		"monitor.text.new":     "every %s, calls %s and notes each new item of %s (told apart by %s)",
		"monitor.text.whole":   "its result",
		"monitor.text.above":   "every %s, calls %s and notes when %s goes above %s, and when it comes back",
		"monitor.text.below":   "every %s, calls %s and notes when %s goes below %s, and when it comes back",
	},
}
