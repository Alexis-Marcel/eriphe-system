package main

import "github.com/modelcontextprotocol/go-sdk/mcp"

// says is how eriphe-core says one call of a tool on a card, in the user's
// language: the tool's _meta "eriphe/says" (core/README.md, Cards). text is
// the sentence by language: {item} the mails it acts on (by their subject
// when eriphe-core knows them, else counted by items: one, then many with
// {n}), {name} an argument's value, by its words in values when given (""
// for an argument left out); a part in [ ] is said only when its arguments
// are. done says what calls did once they ran. A tool with none is said by
// its title.
var says = map[string]map[string]any{
	"move": {
		"text":  map[string]string{"fr": "Déplacer {item} vers {to}", "en": "Move {item} to {to}"},
		"items": map[string][]string{"fr": {"1 mail", "{n} mails"}, "en": {"1 email", "{n} emails"}},
		"done":  map[string][]string{"fr": {"1 déplacé", "{n} déplacés"}, "en": {"1 moved", "{n} moved"}},
		"values": map[string]any{"to": map[string]any{
			"archive": map[string]string{"fr": "les archives", "en": "the archive"},
			"inbox":   map[string]string{"fr": "la boîte de réception", "en": "the inbox"},
		}},
	},
	"trash": {
		"text":  map[string]string{"fr": "Mettre {item} {junk}", "en": "Move {item} {junk}"},
		"items": map[string][]string{"fr": {"1 mail", "{n} mails"}, "en": {"1 email", "{n} emails"}},
		"done":  map[string][]string{"fr": {"1 retiré", "{n} retirés"}, "en": {"1 removed", "{n} removed"}},
		"values": map[string]any{"junk": map[string]any{
			"":      map[string]string{"fr": "à la corbeille", "en": "to the trash"},
			"false": map[string]string{"fr": "à la corbeille", "en": "to the trash"},
			"true":  map[string]string{"fr": "dans les indésirables", "en": "to junk"},
		}},
	},
	"mark": {
		"text":  map[string]string{"fr": "Marquer {item}[ {read}][ {flagged}]", "en": "Mark {item}[ {read}][ {flagged}]"},
		"items": map[string][]string{"fr": {"1 mail", "{n} mails"}, "en": {"1 email", "{n} emails"}},
		"done":  map[string][]string{"fr": {"1 marqué", "{n} marqués"}, "en": {"1 marked", "{n} marked"}},
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

// opens are the fields of an item a read tool says, for eriphe-core to
// show it opened: its _meta "eriphe/open" (core/README.md, Items).
var opens = map[string]map[string]string{
	"read_message": {"title": "subject", "from": "from", "date": "date", "body": "body"},
}

// metaOf is a tool's _meta: how eriphe-core says its call, and the fields
// of what it reads, when declared.
func metaOf(name string) mcp.Meta {
	m := mcp.Meta{}
	if s, ok := says[name]; ok {
		m["eriphe/says"] = s
	}
	if o, ok := opens[name]; ok {
		m["eriphe/open"] = o
	}
	if len(m) == 0 {
		return nil
	}
	return m
}
