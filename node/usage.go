package main

// A turn's usage, as its harness reports it, in one shape whatever the
// runtime: turn_end's "usage" {input_tokens, output_tokens,
// cache_read_tokens, cache_write_tokens} and "cost_usd" when the harness
// gives a cost. Claude Code gives both in its result; Codex its tokens
// (thread/tokenUsage/updated, the turn's share of the thread's total);
// OpenCode and other ACP agents nothing yet.

// claudeUsage reads the usage of a Claude Code result.
func claudeUsage(ev map[string]any, data map[string]any) {
	if u, ok := ev["usage"].(map[string]any); ok {
		usage := map[string]any{
			"input_tokens":       num(u["input_tokens"]),
			"output_tokens":      num(u["output_tokens"]),
			"cache_read_tokens":  num(u["cache_read_input_tokens"]),
			"cache_write_tokens": num(u["cache_creation_input_tokens"]),
		}
		data["usage"] = usage
	}
	if cost, ok := ev["total_cost_usd"].(float64); ok {
		data["cost_usd"] = cost
	}
	if d, ok := ev["duration_ms"].(float64); ok {
		data["duration_ms"] = int64(d)
	}
}

// codexTokens is a thread's token count, as Codex reports it.
type codexTokens struct {
	InputTokens       int64 `json:"inputTokens"`
	CachedInputTokens int64 `json:"cachedInputTokens"`
	OutputTokens      int64 `json:"outputTokens"`
}

// minus is the usage between two totals, in turn_end's shape. Codex counts
// cached input within its input.
func (t codexTokens) minus(before codexTokens) map[string]any {
	in := t.InputTokens - before.InputTokens
	cached := t.CachedInputTokens - before.CachedInputTokens
	return map[string]any{
		"input_tokens":       max(in-cached, 0),
		"output_tokens":      max(t.OutputTokens-before.OutputTokens, 0),
		"cache_read_tokens":  max(cached, 0),
		"cache_write_tokens": int64(0),
	}
}

func num(v any) int64 {
	if f, ok := v.(float64); ok {
		return int64(f)
	}
	return 0
}
