package main

import (
	"context"
	"encoding/json"
	"testing"
	"unicode/utf8"
)

// The brain's fixed context, what it reads before any work (#706): its
// standing prompt, its tools' schemas as the MCP client lists them (name,
// description, input schema, annotations), and its tools' instructions.
// Each is bounded: a rule the code enforces is code, a shape is said once
// (sharedShapes), a block's grammar is catalog_read's.
const (
	brainPromptMax  = 33700 // runes
	brainToolsMax   = 45000 // bytes of JSON, every tool
	brainInstrucMax = 5000  // bytes
)

func TestBrainFixedContextSize(t *testing.T) {
	prompt := utf8.RuneCountInString(brainPrompt)
	res, err := connectServer(t, newMCPServer(graphCore(t))).ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	tools, largest, largestName := 0, 0, ""
	for _, tl := range res.Tools {
		raw, _ := json.Marshal(tl)
		tools += len(raw)
		if len(raw) > largest {
			largest, largestName = len(raw), tl.Name
		}
	}
	t.Logf("brain prompt %d runes (max %d); %d tools, %d bytes of schemas (max %d; largest %s, %d); instructions %d bytes (max %d)",
		prompt, brainPromptMax, len(res.Tools), tools, brainToolsMax, largestName, largest, len(instructions), brainInstrucMax)
	if prompt > brainPromptMax {
		t.Errorf("the brain's prompt is %d runes, past %d", prompt, brainPromptMax)
	}
	if tools > brainToolsMax {
		t.Errorf("the brain's tools are %d bytes of schemas, past %d", tools, brainToolsMax)
	}
	if len(instructions) > brainInstrucMax {
		t.Errorf("the brain's instructions are %d bytes, past %d", len(instructions), brainInstrucMax)
	}
}
