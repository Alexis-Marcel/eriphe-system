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
		"text": map[string]string{"fr": "Modifier {item}[ : « {title} »][, {start}]", "en": "Change {item}[: “{title}”][, {start}]"},
		"item": map[string]string{"fr": "l'événement", "en": "the event"},
	},
	"delete_event": {
		"text": map[string]string{"fr": "Supprimer {item}", "en": "Delete {item}"},
		"item": map[string]string{"fr": "l'événement", "en": "the event"},
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

// metaOf is a tool's _meta: how eriphe-core says its call, when declared.
func metaOf(name string) mcp.Meta {
	if s, ok := says[name]; ok {
		return mcp.Meta{"eriphe/says": s}
	}
	return nil
}
