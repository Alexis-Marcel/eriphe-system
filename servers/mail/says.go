package main

import "github.com/modelcontextprotocol/go-sdk/mcp"

// says is how eriphe-core says one call of a tool on a card, in the user's
// language: the tool's _meta "eriphe/says" (core/README.md, Cards). text is
// the sentence by language: {item} the mails it acts on (by their subject
// when eriphe-core knows them, else counted), {name} an argument's value,
// by its words in values when given ("" for an argument left out); a part
// in [ ] is said only when its arguments are. A tool with none is said by
// its title.
var says = map[string]map[string]any{
	"move": {
		"text": map[string]string{"fr": "Déplacer {item} vers {to}", "en": "Move {item} to {to}"},
		"values": map[string]any{"to": map[string]any{
			"archive": map[string]string{"fr": "les archives", "en": "the archive"},
			"inbox":   map[string]string{"fr": "la boîte de réception", "en": "the inbox"},
		}},
	},
	"trash": {
		"text": map[string]string{"fr": "Mettre {item} {junk}", "en": "Move {item} {junk}"},
		"values": map[string]any{"junk": map[string]any{
			"":      map[string]string{"fr": "à la corbeille", "en": "to the trash"},
			"false": map[string]string{"fr": "à la corbeille", "en": "to the trash"},
			"true":  map[string]string{"fr": "dans les indésirables", "en": "to junk"},
		}},
	},
	"mark": {
		"text": map[string]string{"fr": "Marquer {item}[ {read}][ {flagged}]", "en": "Mark {item}[ {read}][ {flagged}]"},
		"values": map[string]any{
			"read": map[string]any{
				"true":  map[string]string{"fr": "comme lu", "en": "as read"},
				"false": map[string]string{"fr": "comme non lu", "en": "as unread"},
			},
			"flagged": map[string]any{
				"true":  map[string]string{"fr": "d'une étoile", "en": "with a star"},
				"false": map[string]string{"fr": "sans étoile", "en": "without a star"},
			},
		},
	},
	"send_message": {
		"text": map[string]string{"fr": "Envoyer un message[ à {to}][ : « {subject} »]", "en": "Send a message[ to {to}][: “{subject}”]"},
	},
	"create_draft": {
		"text": map[string]string{"fr": "Écrire un brouillon[ à {to}][ : « {subject} »]", "en": "Write a draft[ to {to}][: “{subject}”]"},
	},
}

// metaOf is a tool's _meta: how eriphe-core says its call, when declared.
func metaOf(name string) mcp.Meta {
	if s, ok := says[name]; ok {
		return mcp.Meta{"eriphe/says": s}
	}
	return nil
}
