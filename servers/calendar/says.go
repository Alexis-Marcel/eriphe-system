package main

import "github.com/modelcontextprotocol/go-sdk/mcp"

// says is how eriphe-core says one call of a tool on a card, in the user's
// language: the tool's _meta "eriphe/says" (core/README.md, Cards). text is
// the sentence by language: {item} the event it acts on (by its title when
// eriphe-core knows it, else item's words), {name} an argument's value, by
// its words in values when given, a date or time said as one; a part in
// [ ] is said only when its arguments are. A tool with none is said by its
// title.
var says = map[string]map[string]any{
	"create_event": {
		"text": map[string]string{"fr": "Créer « {title} »[, {start}]", "en": "Create “{title}”[, {start}]"},
	},
	"update_event": {
		"text":  map[string]string{"fr": "Modifier {item}[ : « {title} »][, {start}]", "en": "Change {item}[: “{title}”][, {start}]"},
		"item":  map[string]string{"fr": "l'événement", "en": "the event"},
		"items": map[string][]string{"fr": {"1 événement", "{n} événements"}, "en": {"1 event", "{n} events"}},
	},
	"delete_event": {
		"text":  map[string]string{"fr": "Supprimer {item}", "en": "Delete {item}"},
		"item":  map[string]string{"fr": "l'événement", "en": "the event"},
		"items": map[string][]string{"fr": {"1 événement", "{n} événements"}, "en": {"1 event", "{n} events"}},
		"done":  map[string][]string{"fr": {"1 supprimé", "{n} supprimés"}, "en": {"1 deleted", "{n} deleted"}},
	},
	"respond_event": {
		"text": map[string]string{"fr": "{response} {item}[ : « {comment} »]", "en": "{response} {item}[: “{comment}”]"},
		"item": map[string]string{"fr": "l'invitation", "en": "the invitation"},
		"values": map[string]any{"response": map[string]any{
			"accept":    map[string]string{"fr": "Accepter", "en": "Accept"},
			"decline":   map[string]string{"fr": "Refuser", "en": "Decline"},
			"tentative": map[string]string{"fr": "Répondre « peut-être » à", "en": "Answer “maybe” to"},
		}},
	},
}

// opens are the fields of an item a read tool says, for eriphe-core to
// show it opened: its _meta "eriphe/open" (core/README.md, Items).
var opens = map[string]map[string]string{
	"get_event": {"id": "id", "title": "title", "date": "start", "body": "description"},
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
