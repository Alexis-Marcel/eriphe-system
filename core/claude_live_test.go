package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"time"
)

// What the live benches share: one turn through the local Claude Code CLI
// (`claude` signed in on this machine), no node.

type claudeResult struct {
	Result  string  `json:"result"`
	IsError bool    `json:"is_error"`
	Cost    float64 `json:"total_cost_usd"`
}

// runClaude runs one turn: the instructions after Claude Code's own, as a
// task gets them, no tool, low effort.
func runClaude(cli, dir, model, system, turn string) (claudeResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, cli, "-p", "--model", model, "--effort", "low", "--append-system-prompt", system,
		"--tools", "", "--output-format", "json", "--no-session-persistence", "--strict-mcp-config", "--disable-slash-commands")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "CLAUDE_CODE_EFFORT_LEVEL=low")
	cmd.Stdin = bytes.NewBufferString(turn)
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	if err := cmd.Run(); err != nil {
		return claudeResult{}, fmt.Errorf("%v: %s", err, stderr.String())
	}
	var res claudeResult
	if err := json.Unmarshal(out.Bytes(), &res); err != nil {
		return claudeResult{}, fmt.Errorf("output %.200q: %v", out.String(), err)
	}
	if res.IsError {
		return claudeResult{}, fmt.Errorf("the CLI failed: %.200q", res.Result)
	}
	return res, nil
}
