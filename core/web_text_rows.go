package main

// The words of a list's rows and of the calls cards keep (list_rows.go,
// call_words.go, item_open.go): code's (act.*, open.*), and the page's
// (espace.*, sent to the app with the others).

func init() {
	for lang, words := range rowStrings {
		for k, v := range words {
			webStrings[lang][k] = v
			if len(k) > 7 && k[:7] == "espace." {
				espaceStrings[lang][k] = v
			}
		}
	}
}

var rowStrings = map[string]map[string]string{
	"fr": {
		"act.verb.other": "Utiliser « %s » sur %s",
		"act.yes":        "oui",
		"act.no":         "non",
		"act.done.one":   "Fait pour 1 élément",
		"act.done.many":  "Fait pour %d éléments",
		"act.gone":       "Cet outil n'est plus là : rien n'a été fait.",
		"act.account":    "Ses comptes n'ont pas le même accès : cette action doit nommer son compte. Rien n'a été fait.",
		"act.never":      "Cet outil n'est jamais permis (Réglages › Outils) : rien n'a été fait.",
		"act.failed":     "L'outil a échoué : %s",

		"open.none":    "Cette liste n'ouvre pas ses éléments.",
		"open.stale":   "Cet élément n'est plus dans la liste.",
		"open.notfree": "Le lire demande ton oui : demande-le à {{name}}, qui te le proposera sur une carte.",

		"espace.open.show":    "Ouvrir « %s »",
		"espace.open.loading": "Lecture…",
		"espace.open.close":   "Fermer",
		"espace.open.group":   "Classé « %s »",
		"espace.open.note":    "Note",
		"espace.open.error":   "Impossible de joindre {{name}}.",
		"espace.open.onyes":   "Si tu acceptes",
		"espace.open.more":    "détails",
		"espace.open.call":    "Ce qui sera lancé",
		"espace.open.raw":     "L'élément, tel que lu",

		"espace.batch.keep": "Décocher « %s » : le garder",
		"espace.batch.kept": "%d gardé(s)",

		"espace.row.no":       "Non, pas ça",
		"espace.row.drop":     "Non, pas celui-ci : les autres attendent toujours",
		"espace.row.done":     "Fait",
		"espace.row.running":  "En cours…",
		"espace.row.shared":   "Une seule carte pour ces %d lignes : un Oui sur l'une les fait toutes",
		"espace.row.failed":   "N'a pas pu se faire",
		"espace.row.rejected": "Écarté",

		"espace.page.settled":           "Déjà décidé",
		"espace.page.settled.done":      "Fait",
		"espace.page.settled.failed":    "Échec",
		"espace.page.settled.declined":  "Refusé",
		"espace.page.settled.withdrawn": "Retiré",
		"espace.a2.proposal.raw":        "Appel exact",
		"espace.page.big":               "Agrandir",
		"espace.page.small":             "Revenir à sa taille",
		"espace.page.resize":            "Redimensionner : glisser, ou flèches du clavier",
	},
	"en": {
		"act.verb.other": "Use “%s” on %s",
		"act.yes":        "yes",
		"act.no":         "no",
		"act.done.one":   "Done for 1 item",
		"act.done.many":  "Done for %d items",
		"act.gone":       "This tool is no longer there: nothing was done.",
		"act.account":    "Its accounts do not have the same access: this action must name its account. Nothing was done.",
		"act.never":      "This tool is never allowed (Settings › Tools): nothing was done.",
		"act.failed":     "The tool failed: %s",

		"open.none":    "This list does not open its items.",
		"open.stale":   "This item is no longer in the list.",
		"open.notfree": "Reading it asks your yes: ask {{name}}, who will propose it on a card.",

		"espace.open.show":    "Open “%s”",
		"espace.open.loading": "Reading…",
		"espace.open.close":   "Close",
		"espace.open.group":   "Filed under “%s”",
		"espace.open.note":    "Note",
		"espace.open.error":   "Cannot reach {{name}}.",
		"espace.open.onyes":   "If you accept",
		"espace.open.more":    "details",
		"espace.open.call":    "What it runs",
		"espace.open.raw":     "The item, as read",

		"espace.batch.keep": "Untick “%s”: keep it",
		"espace.batch.kept": "%d kept",

		"espace.row.no":       "No, not that",
		"espace.row.drop":     "No, not this one: the others still wait",
		"espace.row.done":     "Done",
		"espace.row.running":  "Running…",
		"espace.row.shared":   "One card for these %d rows: a Yes on one does them all",
		"espace.row.failed":   "Could not be done",
		"espace.row.rejected": "Set aside",

		"espace.page.settled":           "Already decided",
		"espace.page.settled.done":      "Done",
		"espace.page.settled.failed":    "Failed",
		"espace.page.settled.declined":  "Declined",
		"espace.page.settled.withdrawn": "Withdrawn",
		"espace.a2.proposal.raw":        "Exact call",
		"espace.page.big":               "Enlarge",
		"espace.page.small":             "Back to its size",
		"espace.page.resize":            "Resize: drag, or use the arrow keys",
	},
}
