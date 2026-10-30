package main

// What each runtime, as the node drives it, can do: the core asks the card,
// never the runtime's name. A new harness is one more entry here, and its
// name in the Runtimes of the providers it can use (agents_kinds.go). The
// card describes our adaptor, not the program alone: it changes with the
// node's code. See the study "ACP pour tous les harnais" (2026-10-27).

// How a harness takes standing instructions (a task's AppendSystemPrompt).
const (
	promptPrepended    = "prepended"    // it takes none: they are put before the task's prompt
	promptFlag         = "flag"         // Claude Code's --append-system-prompt-file
	promptDeveloper    = "developer"    // Codex's developerInstructions, on thread/start
	promptInstructions = "instructions" // a file in OpenCode's instructions
)

type harness struct {
	MCP               bool   // takes the task's MCP servers: Eriphe's, the catalog's
	SystemPrompt      string // how it takes standing instructions: prompt*
	Streams           bool   // its text arrives while written: the brain's draft
	PDFInToolResult   bool   // a PDF a tool returns reaches the model whole
	ImageInToolResult bool   // an image a tool returns reaches the model
	BuiltinWebSearch  bool   // searches the web by itself, outside the catalog
	BuiltinWebFetch   bool   // reads a web page by itself, outside the catalog
	Resume            bool   // takes up a previous session again
	AutoModel         bool   // runs a model of its own choice when the agent names none
	ProviderModels    bool   // its model names start with the provider's (openrouter/<model>)
}

// harnesses by runtime. MCP for codex and opencode, and their standing
// instructions, are the node's: they reach the program with the node's
// release that sends them.
var harnesses = map[string]harness{
	// Native stream-json. A PDF is saved and read with its own Read,
	// without asking (tested with 2.1.311).
	"claude-code": {MCP: true, SystemPrompt: promptFlag, Streams: true, PDFInToolResult: true, ImageInToolResult: true,
		BuiltinWebSearch: true, BuiltinWebFetch: true, Resume: true, AutoModel: true},
	// Native app-server. A PDF becomes a JSON text of its bytes: it gets the text.
	"codex": {MCP: true, SystemPrompt: promptDeveloper, Streams: true, ImageInToolResult: true,
		BuiltinWebSearch: true, Resume: true},
	// ACP. A PDF is passed to the model inline. Its webfetch is built in
	// (asked for); its websearch is denied (node/runtime_opencode.go).
	"opencode": {MCP: true, SystemPrompt: promptInstructions, Streams: true, PDFInToolResult: true, ImageInToolResult: true,
		BuiltinWebFetch: true, Resume: true, ProviderModels: true},
	// Any other ACP agent, by its command (Goose, Qwen Code…). What ACP
	// itself gives, the MCP servers; nothing else is counted on: no system
	// prompt in ACP v1 (its RFD is a draft), so the instructions come first
	// in the prompt, and it cannot be the brain. Its model is its own.
	"acp": {MCP: true, SystemPrompt: promptPrepended, AutoModel: true},
}

// harnessOf is a runtime's card; an unknown runtime can do nothing.
func harnessOf(runtime string) harness {
	return harnesses[runtime]
}

// takesSystemPrompt tells whether standing instructions reach it as such,
// not only as the start of the prompt.
func (h harness) takesSystemPrompt() bool {
	return h.SystemPrompt != "" && h.SystemPrompt != promptPrepended
}

// canBeBrain tells whether the brain can run on it: the brain's tools are
// MCP servers, and its prompt standing instructions.
func (h harness) canBeBrain() bool {
	return h.MCP && h.takesSystemPrompt()
}
