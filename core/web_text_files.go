package main

// The words of attachments in the conversation (web_files.go, attachments.go).

func init() {
	for lang, words := range fileStrings {
		for k, v := range words {
			webStrings[lang][k] = v
		}
	}
}

var fileStrings = map[string]map[string]string{
	"fr": {
		"chat.attach":       "Joindre une photo ou un fichier",
		"chat.drop":         "Dépose tes fichiers ici",
		"chat.file.remove":  "Retirer",
		"chat.file.open":    "Ouvrir",
		"chat.file.toobig":  "« %s » dépasse 20 Mo.",
		"chat.file.toomuch": "Ces fichiers sont trop lourds pour un seul message.",
		"chat.file.many":    "Cinq fichiers au plus par message.",
		"chat.file.empty":   "« %s » est vide.",
		"chat.file.type":    "« %s » n'est pas pris en charge : une image (PNG, JPEG, GIF, WebP), un PDF ou un texte (.txt, .md, .csv).",
	},
	"en": {
		"chat.attach":       "Attach a photo or a file",
		"chat.drop":         "Drop your files here",
		"chat.file.remove":  "Remove",
		"chat.file.open":    "Open",
		"chat.file.toobig":  "“%s” is over 20 MB.",
		"chat.file.toomuch": "These files are too large for one message.",
		"chat.file.many":    "Five files at most per message.",
		"chat.file.empty":   "“%s” is empty.",
		"chat.file.type":    "“%s” is not supported: an image (PNG, JPEG, GIF, WebP), a PDF or a text (.txt, .md, .csv).",
	},
}
