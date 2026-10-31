package main

import (
	"slices"
	"strconv"
	"strings"
)

// What the core knows of each kind of provider: the runtimes that can use
// it, the variable that carries its credential and, to help someone who is
// not a developer, who pays (a subscription, or by use), where its
// credential comes from, the models worth offering and the agents worth
// creating with it.

// billing says who pays for an agent's work.
const (
	billSubscription = "subscription" // the user's subscription, within its limits
	billUsage        = "usage"        // by use, on the provider's bill
	billSelfHosted   = "selfhosted"   // the user's own machine: nothing per request
)

// selfHostedKind is a model server the user runs (Ollama, llama.cpp, vLLM,
// LM Studio), reached at its OpenAI-compatible /v1 address: its models are
// the provider's own list (Provider.Models), not the kind's.
const selfHostedKind = "openai-compatible"

// modelChoice is a model offered in the agent form. ID is what the runtime
// takes: an alias of Claude Code, or OpenCode's provider/model.
type modelChoice struct {
	ID    string
	Label string // its usual name, the same in every language
	Note  string // a text key: what it is good at
	modelCaps
}

// modelCaps is what a model reads and does, from models.dev (read on
// 2026-10-27): its input modalities, tool_call and limit.context.
type modelCaps struct {
	Vision  bool // reads images
	PDF     bool // reads a PDF whole, its layout and scans
	Tools   bool // calls tools
	Context int  // tokens it reads at once; 0 when unknown
}

var (
	claudeCaps    = modelCaps{Vision: true, PDF: true, Tools: true, Context: 1_000_000}
	gptCaps       = modelCaps{Vision: true, PDF: true, Tools: true, Context: 1_050_000}
	geminiCaps    = modelCaps{Vision: true, PDF: true, Tools: true, Context: 1_048_576}
	toolsOnlyCaps = modelCaps{Tools: true} // all that is assumed of a model nothing is known of
	// Codex runs the same models as OpenAI's API, but its own context limit
	// through a ChatGPT plan is not published: unknown.
	codexCaps = modelCaps{Vision: true, PDF: true, Tools: true}
)

// agentPreset is an agent the workspace offers to create with a provider.
type agentPreset struct {
	Name    string // the agent's name, suffixed with the provider's if taken
	Model   string
	Use     string // a text key: when the brain should pick it, for the brain
	Default bool   // the agent to use by default, when there is none yet
}

type providerKind struct {
	ID       string
	Billing  string
	Name     string   // the provider's name when it is the first of its kind
	Runtimes []string // the runtimes that can use it; its agents run the first unless told otherwise
	Env      string   // the variable that carries its credential
	// CustomEnv: the variable is the provider's own (Provider.Env), for an
	// agent the core knows nothing of: an ACP agent run by its command.
	CustomEnv bool
	ModelNS   string // starts its model names in a harness with ProviderModels (openrouter/<model>)
	Prefix    string // how its credential starts, to catch a paste in the wrong card
	Models    map[string][]modelChoice
	// Unlisted is what a model it does not list (typed by hand, or the
	// runtime's own) is assumed to do: the big providers' current models all
	// read images and PDFs; of an open catalog, only tool calling is assumed.
	Unlisted modelCaps
	Presets  []agentPreset
}

// Runtime is the runtime its agents run unless told otherwise.
func (k providerKind) Runtime() string {
	if len(k.Runtimes) == 0 {
		return ""
	}
	return k.Runtimes[0]
}

// claudeModels are Claude Code's aliases: they follow Anthropic's latest models.
var claudeModels = []modelChoice{
	{"opus", "Claude Opus", "model.opus", claudeCaps},
	{"sonnet", "Claude Sonnet", "model.sonnet", claudeCaps},
	{"haiku", "Claude Haiku", "model.haiku", claudeCaps},
}

var claudePresets = []agentPreset{
	{"opus", "opus", "preset.agent.opus", false},
	{"sonnet", "sonnet", "preset.agent.sonnet", true},
	{"haiku", "haiku", "preset.agent.haiku", false},
}

// providerKinds in the order the workspace offers them: subscriptions
// first, then pay-per-use keys.
var providerKinds = []providerKind{
	{ID: "claude-subscription", Billing: billSubscription, Name: "claude", Runtimes: []string{"claude-code"},
		Env: "CLAUDE_CODE_OAUTH_TOKEN", Prefix: "sk-ant-oat", Unlisted: claudeCaps,
		Models: map[string][]modelChoice{"claude-code": claudeModels}, Presets: claudePresets},
	{ID: "chatgpt-subscription", Billing: billSubscription, Name: "chatgpt", Runtimes: []string{"codex"},
		Env: codexAuthEnv, Unlisted: codexCaps, // chatgpt.go
		Models: map[string][]modelChoice{"codex": {
			{"gpt-6.1-sol", "GPT-6.1 Sol", "model.gpt", codexCaps},
			{"gpt-6-luna", "GPT-6 Luna", "model.small", codexCaps},
		}}, Presets: []agentPreset{
			{"gpt", "gpt-6.1-sol", "preset.agent.gpt", true},
			{"gpt-luna", "gpt-6-luna", "preset.agent.gptlight", false},
		}},
	{ID: "anthropic-api", Billing: billUsage, Name: "anthropic", Runtimes: []string{"claude-code", "opencode"},
		Env: "ANTHROPIC_API_KEY", ModelNS: "anthropic", Prefix: "sk-ant-api", Unlisted: claudeCaps,
		Models: map[string][]modelChoice{"claude-code": claudeModels, "opencode": {
			{"anthropic/claude-opus-5-5", "Claude Opus 5.5", "model.opus", claudeCaps},
			{"anthropic/claude-sonnet-5-5", "Claude Sonnet 5.5", "model.sonnet", claudeCaps},
			{"anthropic/claude-haiku-5-5", "Claude Haiku 5.5", "model.haiku", claudeCaps},
		}}, Presets: claudePresets},
	{ID: "openai-api", Billing: billUsage, Name: "openai", Runtimes: []string{"opencode"},
		Env: "OPENAI_API_KEY", ModelNS: "openai", Prefix: "sk-", Unlisted: gptCaps,
		Models: map[string][]modelChoice{"opencode": {
			{"openai/gpt-6.1-sol", "GPT-6.1 Sol", "model.gpt", gptCaps},
			{"openai/gpt-6-luna", "GPT-6 Luna", "model.small", gptCaps},
		}}, Presets: []agentPreset{
			{"gpt", "openai/gpt-6.1-sol", "preset.agent.second", true},
			{"gpt-luna", "openai/gpt-6-luna", "preset.agent.small", false},
		}},
	{ID: "google-api", Billing: billUsage, Name: "gemini", Runtimes: []string{"opencode"},
		Env: "GOOGLE_GENERATIVE_AI_API_KEY", ModelNS: "google", Prefix: "AIza", Unlisted: geminiCaps,
		Models: map[string][]modelChoice{"opencode": {
			{"google/gemini-4-argon", "Gemini 4 Argon", "model.gemini", geminiCaps},
			{"google/gemini-flash-latest", "Gemini Flash", "model.small", geminiCaps},
		}}, Presets: []agentPreset{
			{"gemini", "google/gemini-4-argon", "preset.agent.gemini", true},
			{"gemini-flash", "google/gemini-flash-latest", "preset.agent.small", false},
		}},
	{ID: "mistral-api", Billing: billUsage, Name: "mistral", Runtimes: []string{"opencode"},
		Env: "MISTRAL_API_KEY", ModelNS: "mistral", Unlisted: toolsOnlyCaps,
		Models: map[string][]modelChoice{"opencode": {
			{"mistral/mistral-large-latest", "Mistral Large", "model.mistral", modelCaps{Vision: true, PDF: true, Tools: true, Context: 262_144}},
			{"mistral/devstral-latest", "Devstral", "model.devstral", modelCaps{Tools: true, Context: 262_144}},
		}}, Presets: []agentPreset{
			{"mistral", "mistral/mistral-large-latest", "preset.agent.mistral", true},
			{"devstral", "mistral/devstral-latest", "preset.agent.devstral", false},
		}},
	{ID: "openrouter-api", Billing: billUsage, Name: "openrouter", Runtimes: []string{"opencode"},
		Env: "OPENROUTER_API_KEY", ModelNS: "openrouter", Prefix: "sk-or-", Unlisted: toolsOnlyCaps,
		Models: map[string][]modelChoice{"opencode": {
			{"openrouter/moonshotai/kimi-k3.1-pro", "Kimi K3.1 Pro", "model.kimi", modelCaps{Vision: true, Tools: true, Context: 1_048_576}},
			{"openrouter/deepseek/deepseek-v4-pro", "DeepSeek V4 Pro", "model.deepseek", modelCaps{Tools: true, Context: 1_048_576}},
			{"openrouter/z-ai/glm-5.3", "GLM 5.3", "model.glm", modelCaps{Tools: true, Context: 1_048_576}},
		}}, Presets: []agentPreset{
			{"kimi", "openrouter/moonshotai/kimi-k3.1-pro", "preset.agent.kimi", true},
			{"deepseek", "openrouter/deepseek/deepseek-v4-pro", "preset.agent.deepseek", false},
		}},
	// The user's own model server: an address, an optional key, the models
	// it serves. Nothing is assumed of them but tool calling.
	{ID: selfHostedKind, Billing: billSelfHosted, Name: "selfhosted", Runtimes: []string{"opencode"},
		Env: "SELFHOSTED_API_KEY", ModelNS: "selfhosted", Unlisted: toolsOnlyCaps},
	// Any credential, in the variable the user names, for an ACP agent of
	// their choice: nothing is assumed of its models but tool calling.
	{ID: "custom-api", Billing: billUsage, Name: "custom", Runtimes: []string{"acp"}, CustomEnv: true, Unlisted: toolsOnlyCaps},
}

func kindOf(id string) (providerKind, bool) {
	i := slices.IndexFunc(providerKinds, func(k providerKind) bool { return k.ID == id })
	if i < 0 {
		return providerKind{}, false
	}
	return providerKinds[i], true
}

// runtimesOf are the runtimes a kind of provider can run, its own first.
func runtimesOf(kind string) []string {
	k, _ := kindOf(kind)
	return slices.Clone(k.Runtimes)
}

// modelLabel is the usual name of an agent's model, or the model itself
// (a self-hosted one without its selfhosted/).
func modelLabel(kind, runtime, model string) string {
	k, _ := kindOf(kind)
	for _, m := range k.Models[runtime] {
		if m.ID == model {
			return m.Label
		}
	}
	if kind == selfHostedKind {
		return strings.TrimPrefix(model, k.ModelNS+"/")
	}
	return model
}

// selfHostedChoices are the models a self-hosted provider serves, as the
// agent form offers them.
func selfHostedChoices(p Provider) []modelChoice {
	k, _ := kindOf(selfHostedKind)
	var out []modelChoice
	for _, m := range p.Models {
		out = append(out, modelChoice{ID: k.ModelNS + "/" + m, Label: m, Note: "model.selfhosted", modelCaps: toolsOnlyCaps})
	}
	return out
}

// modelCard is what an agent's model reads and does: its choice's, or
// what its provider's unlisted models are assumed to.
func modelCard(kind, runtime, model string) modelCaps {
	k, _ := kindOf(kind)
	for _, m := range k.Models[runtime] {
		if m.ID == model {
			return m.modelCaps
		}
	}
	return k.Unlisted
}

// fullModel completes a model typed without its provider, in a harness
// that wants it, with the agent's provider's (kimi-k2 with OpenRouter on
// OpenCode: openrouter/kimi-k2).
func fullModel(kind, runtime, model string) string {
	model = strings.TrimSpace(model)
	k, _ := kindOf(kind)
	prefix := k.ModelNS
	if !harnessOf(runtime).ProviderModels || model == "" || prefix == "" || strings.HasPrefix(model, prefix+"/") {
		return model
	}
	return prefix + "/" + model
}

// presetName is the name a preset agent gets: its own, or with the
// provider's name when taken.
func presetName(p agentPreset, provider string, taken func(string) bool) string {
	if !taken(p.Name) {
		return p.Name
	}
	n := p.Name + "-" + provider
	if len(n) > 32 {
		n = n[:32]
	}
	return n
}

// defaultProviderName is a name for a new provider of a kind: the kind's,
// then numbered.
func defaultProviderName(kind string, taken func(string) bool) string {
	k, _ := kindOf(kind)
	base := k.Name
	if base == "" {
		base = "provider"
	}
	if !taken(base) {
		return base
	}
	for i := 2; ; i++ {
		if n := base + "-" + strconv.Itoa(i); !taken(n) {
			return n
		}
	}
}
