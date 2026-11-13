package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"slices"
	"time"
)

// A call often names what it acts on by a number: to decide, the user needs
// what is behind it. The card reads it with the tool's own read, once, when
// it is asked. A read that fails leaves the card with the call's parameters.

// cardReadTimeout keeps the read from delaying the card.
const cardReadTimeout = 5 * time.Second

// cardDescriptionMax is as much of a description as a card shows.
const cardDescriptionMax = 1000

// pullReads are the ways a GitHub server reads a pull request.
var pullReads = []struct {
	tool string
	args map[string]any
}{
	{"pull_request_read", map[string]any{"method": "get"}},
	{"get_pull_request", nil},
}

// pullContext says the pull request a call names: its title and the start of
// its description. read runs a tool of the call's server, and has tells
// whether it has that tool.
func pullContext(input map[string]any, has func(string) bool, read func(tool string, args map[string]any) (string, error)) []cardField {
	owner, _ := input["owner"].(string)
	repo, _ := input["repo"].(string)
	number := input["pullNumber"]
	if number == nil {
		number = input["pull_number"]
	}
	if owner == "" || repo == "" || number == nil {
		return nil
	}
	for _, r := range pullReads {
		if !has(r.tool) {
			continue
		}
		args := map[string]any{"owner": owner, "repo": repo, "pullNumber": number}
		for k, v := range r.args {
			args[k] = v
		}
		text, err := read(r.tool, args)
		if err != nil {
			log.Printf("access: reading a pull request for its card: %v", err)
			return nil
		}
		var pull struct {
			Title string `json:"title"`
			Body  string `json:"body"`
		}
		if json.Unmarshal([]byte(text), &pull) != nil || pull.Title == "" {
			return nil
		}
		out := []cardField{{"PR", truncate(fmt.Sprintf("#%v %s", number, pull.Title), 300)}}
		if pull.Body != "" {
			out = append(out, cardField{"description", truncate(pull.Body, cardDescriptionMax)})
		}
		return out
	}
	return nil
}

// cardFields are what a card lists: what it read, then the call's parameters
// but the number that read already says, and a content its diff says.
func cardFields(context []cardField, input map[string]any) []cardField {
	out := context
	diff := slices.ContainsFunc(context, func(f cardField) bool { return f.Name == "diff" })
	for _, f := range callFields(input) {
		if len(context) > 0 && (f.Name == "pullNumber" || f.Name == "pull_number") {
			continue
		}
		if diff && f.Name == "content" {
			continue
		}
		out = append(out, f)
	}
	return out
}

// callContext is what a card adds to a call's parameters.
func (c *core) callContext(ctx context.Context, tool string, input map[string]any) []cardField {
	server, _, ok := splitToolName(tool)
	if !ok {
		return nil
	}
	u := c.pool.get(server)
	if u == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, cardReadTimeout)
	defer cancel()
	if server == foldersServer {
		return c.folderCardContext(ctx, tool, input) // the file and its diff (folder_tools.go)
	}
	return pullContext(input, func(name string) bool { return u.toolByClaudeName(name) != nil },
		func(name string, args map[string]any) (string, error) {
			raw, err := json.Marshal(args)
			if err != nil {
				return "", err
			}
			res, err := u.call(ctx, name, raw)
			if err != nil {
				return "", err
			}
			if res.IsError {
				return "", fmt.Errorf("%s: %s", name, truncate(resultText(res), 200))
			}
			return resultText(res), nil
		})
}
