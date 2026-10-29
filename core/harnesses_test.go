package main

import (
	"context"
	"encoding/json"
	"maps"
	"slices"
	"strings"
	"testing"
)

var originalHarnesses = maps.Clone(harnesses)

// withHarness changes a runtime's card for one test.
func withHarness(t *testing.T, runtime string, edit func(*harness)) {
	t.Helper()
	harnesses = maps.Clone(harnesses)
	h := harnesses[runtime]
	edit(&h)
	harnesses[runtime] = h
	t.Cleanup(restoreHarnesses)
}

func restoreHarnesses() { harnesses = maps.Clone(originalHarnesses) }

func TestHarnessCards(t *testing.T) {
	// Every runtime a provider offers has a card, with a known way to take
	// standing instructions.
	for _, k := range providerKinds {
		if len(k.Runtimes) == 0 || k.Env == "" && !k.CustomEnv || k.Env != "" && k.CustomEnv {
			t.Errorf("%s: no runtime or no credential variable", k.ID)
		}
		for _, r := range k.Runtimes {
			if _, ok := harnesses[r]; !ok {
				t.Errorf("%s: runtime %s has no card", k.ID, r)
			}
		}
		for r := range k.Models {
			if !slices.Contains(k.Runtimes, r) {
				t.Errorf("%s: models for %s, a runtime it cannot run", k.ID, r)
			}
		}
		if k.ModelNS == "" && slices.ContainsFunc(k.Runtimes, func(r string) bool { return harnessOf(r).ProviderModels }) {
			t.Errorf("%s: a runtime wants provider/model, the kind has no name for it", k.ID)
		}
	}
	for name, h := range harnesses {
		if !slices.Contains([]string{promptPrepended, promptFlag, promptDeveloper, promptInstructions}, h.SystemPrompt) {
			t.Errorf("%s: system prompt %q", name, h.SystemPrompt)
		}
	}
	// Claude Code does all the core asks of a runtime, as before the card.
	if want := (harness{MCP: true, SystemPrompt: promptFlag, Streams: true, PDFInToolResult: true, ImageInToolResult: true,
		BuiltinWebSearch: true, BuiltinWebFetch: true, Resume: true, AutoModel: true}); harnessOf("claude-code") != want {
		t.Errorf("claude-code = %+v", harnessOf("claude-code"))
	}
	if h := harnessOf("codex"); h.PDFInToolResult || !h.ImageInToolResult || !h.canBeBrain() {
		t.Errorf("codex = %+v", h)
	}
	// Any other ACP agent: the MCP servers, and nothing else counted on.
	if h := harnessOf("acp"); h != (harness{MCP: true, SystemPrompt: promptPrepended, AutoModel: true}) || h.takesSystemPrompt() || h.canBeBrain() {
		t.Errorf("acp = %+v", h)
	}
	if harnessOf("magic") != (harness{}) || harnessOf("magic").canBeBrain() {
		t.Error("an unknown runtime can do something")
	}
	if (harness{MCP: true, SystemPrompt: promptPrepended}).canBeBrain() || (harness{SystemPrompt: promptFlag}).canBeBrain() {
		t.Error("a brain without MCP or a system prompt")
	}
}

func TestModelCard(t *testing.T) {
	cases := []struct {
		kind, runtime, model string
		want                 modelCaps
	}{
		{"claude-subscription", "claude-code", "sonnet", claudeCaps},
		{"claude-subscription", "claude-code", "", claudeCaps},            // the runtime's own
		{"anthropic-api", "claude-code", "claude-sonnet-5-5", claudeCaps}, // typed by hand
		{"openrouter-api", "opencode", "openrouter/z-ai/glm-5.3", modelCaps{Tools: true, Context: 1_048_576}},
		{"openrouter-api", "opencode", "openrouter/some/new-model", toolsOnlyCaps},
		{"google-api", "opencode", "google/gemini-5", geminiCaps},
		{"magic", "opencode", "x", modelCaps{}},
	}
	for _, c := range cases {
		if got := modelCard(c.kind, c.runtime, c.model); got != c.want {
			t.Errorf("%s %s %q = %+v, want %+v", c.kind, c.runtime, c.model, got, c.want)
		}
	}
	if fullModel("openrouter-api", "opencode", "kimi-k2") != "openrouter/kimi-k2" ||
		fullModel("anthropic-api", "claude-code", "claude-sonnet-5-5") != "claude-sonnet-5-5" ||
		fullModel("chatgpt-subscription", "codex", "gpt-6-sol") != "gpt-6-sol" {
		t.Error("fullModel")
	}
	if modelCard("custom-api", "acp", "") != toolsOnlyCaps {
		t.Error("acp models")
	}
	if !slices.Equal(runtimesOf("anthropic-api"), []string{"claude-code", "opencode"}) || len(runtimesOf("magic")) != 0 {
		t.Errorf("runtimesOf = %v", runtimesOf("anthropic-api"))
	}
}

// A worker whose runtime takes no system prompt reads it first; the others
// get it as such.
func TestWorkerSystemPrompt(t *testing.T) {
	c := newCatalogCore(t)
	c.store.registerNode("pc", hashSecret("node-secret"))
	c.store.setNodeStatus("pc", true, []byte(`{"runtimes":["claude-code","opencode"]}`))
	spec := func(agent string) nodeTaskSpec {
		t.Helper()
		task, err := c.runTask(context.Background(), runRequest{Prompt: "count the lines", Agent: agent})
		if err != nil {
			t.Fatal(err)
		}
		var raw string
		c.store.db.QueryRow(`SELECT spec FROM tasks WHERE id = ?`, task.ID).Scan(&raw)
		var s nodeTaskSpec
		json.Unmarshal([]byte(raw), &s)
		return s
	}
	if s := spec("sonnet"); s.AppendSystemPrompt != workerVoice(defaultDisplayName) || s.Prompt != "count the lines" {
		t.Errorf("claude-code: %+v", s)
	}
	if s := spec("kimi"); s.AppendSystemPrompt != workerVoice(defaultDisplayName) || s.Prompt != "count the lines" {
		t.Errorf("opencode: %+v", s)
	}
	withHarness(t, "opencode", func(h *harness) { h.SystemPrompt = promptPrepended })
	if s := spec("kimi"); s.AppendSystemPrompt != "" || !strings.HasPrefix(s.Prompt, workerVoice(defaultDisplayName)+"\n\n") ||
		!strings.HasSuffix(s.Prompt, "count the lines") {
		t.Errorf("prepended: %+v", s)
	}
}
