package main

// The words of a task's receipt (receipt.go) on its card and in its drawer.

func init() {
	for lang, words := range taskStrings {
		for k, v := range words {
			webStrings[lang][k] = v
		}
	}
}

var taskStrings = map[string]map[string]string{
	"fr": {
		"receipt.title":              "Compte rendu",
		"receipt.none":               "Sans compte rendu",
		"receipt.none.long":          "L'agent n'a pas rendu de compte rendu lisible : rien de ce qu'il dit n'est vérifié.",
		"receipt.status.done":        "Fini",
		"receipt.status.partial":     "En partie",
		"receipt.status.blocked":     "Bloqué",
		"receipt.verified":           "Vérifié par Eriphe",
		"receipt.verified.none":      "Aucun contrôle n'a tourné.",
		"receipt.verified.short":     "vérifié(s)",
		"receipt.open":               "Non vérifié",
		"receipt.open.none":          "Rien.",
		"receipt.open.short":         "non vérifié(s)",
		"receipt.claimed":            "affirmé, sans contrôle",
		"receipt.unsupported":        "Affirmation non étayée",
		"receipt.mismatches":         "Ce qu'Eriphe a vu ne correspond pas :",
		"receipt.checks":             "Contrôles",
		"receipt.check.ok":           "réussi",
		"receipt.check.failed":       "échoué (code %d)",
		"receipt.changed":            "Changé",
		"receipt.diff":               "Fichiers modifiés selon le dépôt",
		"receipt.needsapproval":      "Attend ton accord",
		"receipt.decisions":          "Décisions",
		"receipt.risks":              "Risques",
		"receipt.lessons":            "Leçons proposées",
		"receipt.mm.verified_failed": "« %s » est dit vérifié, mais le contrôle %s a échoué.",
		"receipt.mm.done_failed":     "La tâche se dit finie, mais le contrôle %s a échoué.",
		"receipt.mm.not_in_diff":     "« %s » est dit changé, mais le dépôt n'a pas ce changement.",
		"receipt.mm.unreported":      "%d fichier(s) modifié(s) dans le dépôt ne sont pas dans le compte rendu.",
	},
	"en": {
		"receipt.title":              "Report",
		"receipt.none":               "No report",
		"receipt.none.long":          "The agent gave no readable report: nothing it says is verified.",
		"receipt.status.done":        "Done",
		"receipt.status.partial":     "Partly done",
		"receipt.status.blocked":     "Blocked",
		"receipt.verified":           "Verified by Eriphe",
		"receipt.verified.none":      "No check ran.",
		"receipt.verified.short":     "verified",
		"receipt.open":               "Not verified",
		"receipt.open.none":          "Nothing.",
		"receipt.open.short":         "not verified",
		"receipt.claimed":            "claimed, not checked",
		"receipt.unsupported":        "Unsupported claim",
		"receipt.mismatches":         "What Eriphe saw does not match:",
		"receipt.checks":             "Checks",
		"receipt.check.ok":           "passed",
		"receipt.check.failed":       "failed (code %d)",
		"receipt.changed":            "Changed",
		"receipt.diff":               "Files changed in the repository",
		"receipt.needsapproval":      "Waits for your approval",
		"receipt.decisions":          "Decisions",
		"receipt.risks":              "Risks",
		"receipt.lessons":            "Proposed lessons",
		"receipt.mm.verified_failed": "\"%s\" is said verified, but the check %s failed.",
		"receipt.mm.done_failed":     "The task says it is done, but the check %s failed.",
		"receipt.mm.not_in_diff":     "\"%s\" is said changed, but the repository does not have that change.",
		"receipt.mm.unreported":      "%d file(s) changed in the repository are not in the report.",
	},
}
