package main

// The user's language: what Eriphe writes for the user to read is in it,
// the fixed texts (the tr tables) as the models' lines (notifications, a
// task's result, the workspace's surfaces). One rule: the language of the
// last signed-in page that set it: the workspace opening (/espace/boot),
// the language switch in Settings, the channel settings, a channel's link
// code. Before any, the configured web.lang, else French. What only the
// models read stays in French (brain-prompt.md and their packets).

const userLangKey = "user_lang"

// userLang is the user's language: fr or en.
func (c *core) userLang() string {
	switch c.store.get(userLangKey) {
	case "en":
		return "en"
	case "fr":
		return "fr"
	}
	if cfg := c.conf(); cfg != nil && cfg.Web != nil && cfg.Web.Lang == "en" {
		return "en"
	}
	return "fr"
}

// noteUserLang keeps the user's language, from a signed-in request only;
// the workspace's queue is written in it again when it changes.
func (c *core) noteUserLang(lang string) {
	if lang != "fr" && lang != "en" {
		return
	}
	if c.store.get(userLangKey) != lang {
		c.store.set(userLangKey, lang)
		c.kickWorkspace()
	}
}

// userLangEN is the user's language for an instruction written in English.
func userLangEN(lang string) string {
	if lang == "en" {
		return "in the user's language, English"
	}
	return "in the user's language, French, with tutoiement (« tu », never « vous »)"
}

// langLine is the user's language as a model with a long session reads it
// at the head of each turn (the brain, the assistants, a thread's lead):
// the language can change between two turns.
func langLine(lang string) string {
	return "[langue de l'utilisateur : écris " + userLangFR(lang) + ", tout ce qu'il lit (notifications, surfaces, cartes, résumés)]"
}

// userLangFR is the user's language for an instruction written in French.
func userLangFR(lang string) string {
	if lang == "en" {
		return "en anglais, sa langue"
	}
	return "en français, sa langue, en le tutoyant"
}
