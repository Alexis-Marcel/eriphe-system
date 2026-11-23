package main

import (
	"maps"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// says is how eriphe-core says one call of a tool on a card, in the user's
// language: the tool's _meta "eriphe/says" (core/README.md, Cards). text is
// the sentence by language: {item} the file it acts on (by its name when
// eriphe-core knows it, else item's words), {name} an argument's value; a
// part in [ ] is said only when its arguments are. items counts files (one,
// then many with {n}); done says what calls did once they ran. A tool with
// none is said by its title.
var says = map[string]map[string]any{
	"write_file": {
		"text": map[string]string{"fr": "Écrire « {name} »[ dans {folder}]", "en": "Write “{name}”[ in {folder}]"},
	},
	"update_file": {
		"text":  map[string]string{"fr": "Remplacer le contenu de {item}", "en": "Replace the content of {item}"},
		"item":  map[string]string{"fr": "le fichier", "en": "the file"},
		"items": files,
	},
	"trash_file": {
		"text":  map[string]string{"fr": "Mettre {item} à la corbeille", "en": "Move {item} to the trash"},
		"item":  map[string]string{"fr": "le fichier", "en": "the file"},
		"items": files,
		"done":  map[string][]string{"fr": {"1 mis à la corbeille", "{n} mis à la corbeille"}, "en": {"1 moved to the trash", "{n} moved to the trash"}},
	},
}

// files counts files, for items.
var files = map[string][]string{"fr": {"1 fichier", "{n} fichiers"}, "en": {"1 file", "{n} files"}}

// withMeta adds to a tool's _meta how eriphe-core says its call, when
// declared.
func withMeta(t *mcp.Tool) {
	s, ok := says[t.Name]
	if !ok {
		return
	}
	m := mcp.Meta{"eriphe/says": s}
	maps.Copy(m, t.Meta)
	t.Meta = m
}
