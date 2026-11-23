package main

import "github.com/modelcontextprotocol/go-sdk/mcp"

// What the tools of a service whose server Eriphe does not write would
// declare in their _meta, had Eriphe written it: how a card says their
// call ("eriphe/says", call_says.go) and reads what it names
// ("eriphe/reads", card_context.go). A preset's data, like its URL: the
// code reads it as it reads a tool's own _meta, and knows no tool.

// githubMeta is GitHub's (github.com/github/github-mcp-server): its pull
// requests, said in the user's words, and read by their number for a card.
var githubMeta = map[string]mcp.Meta{
	"*": {"eriphe/reads": map[string]any{
		"tools": []any{
			map[string]any{"tool": "pull_request_read", "args": map[string]any{"method": "get"}},
			map[string]any{"tool": "get_pull_request"},
		},
		"with": []any{"owner", "repo", "pullNumber"},
		"show": []any{
			map[string]any{"field": "title", "name": map[string]any{"fr": "Pull request", "en": "Pull request"}},
			map[string]any{"field": "body", "name": map[string]any{"fr": "Description", "en": "Description"}},
		},
	}},
	"create_pull_request": {"eriphe/says": map[string]any{
		"text": map[string]any{"fr": "Ouvrir une pull request[ : « {title} »]", "en": "Open a pull request[: “{title}”]"},
	}},
	"update_pull_request": {"eriphe/says": map[string]any{
		"text": map[string]any{"fr": "Modifier la pull request #{pullNumber}", "en": "Change pull request #{pullNumber}"},
	}},
	"merge_pull_request": {"eriphe/says": map[string]any{
		"text": map[string]any{"fr": "Fusionner la pull request #{pullNumber}", "en": "Merge pull request #{pullNumber}"},
	}},
}
