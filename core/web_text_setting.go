package main

// The words of the settings the brain proposes (settings_brain.go), in
// « À valider ».

func init() {
	for lang, words := range settingStrings {
		for k, v := range words {
			webStrings[lang][k] = v
		}
	}
}

var settingStrings = map[string]map[string]string{
	"fr": {
		"approve.settings":                   "Réglages",
		"setting.did.approve":                "Réglage appliqué.",
		"setting.did.reject":                 "Réglage refusé.",
		"setting.name.device_order":          "Ordre des appareils",
		"setting.name.lines_off":             "Lignes coupées",
		"setting.name.move_on_after_minutes": "Minutes avant l'appareil suivant",
		"setting.name.daily_cap":             "Notifications « maintenant » par jour",
		"setting.name.time_zone":             "Fuseau horaire",
		"setting.name.telegram_detail":       "Ce que Telegram reçoit (full : le texte ; alert : un signal)",
		"setting.name.context_level":         "Ce qu'Eriphe sait de toi (0 : rien ; 1 : présence ; 2 : contexte)",
		"setting.name.context_sources_off":   "Sources du contexte coupées",
	},
	"en": {
		"approve.settings":                   "Settings",
		"setting.did.approve":                "Setting applied.",
		"setting.did.reject":                 "Setting rejected.",
		"setting.name.device_order":          "Device order",
		"setting.name.lines_off":             "Lines turned off",
		"setting.name.move_on_after_minutes": "Minutes before the next device",
		"setting.name.daily_cap":             "“Now” notifications a day",
		"setting.name.time_zone":             "Time zone",
		"setting.name.telegram_detail":       "What Telegram gets (full: the text; alert: a signal)",
		"setting.name.context_level":         "What Eriphe knows about you (0: nothing; 1: presence; 2: context)",
		"setting.name.context_sources_off":   "Context sources turned off",
	},
}
