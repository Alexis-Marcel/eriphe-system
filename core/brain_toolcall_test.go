package main

import (
	"strings"
	"testing"
)

func TestToolCallText(t *testing.T) {
	calls := []string{
		`eriphe-core_answer_task {"task_id":"t-1a2b","allow":true}`,
		"  eriphe-core_answer_task {\"task_id\": \"t-1\", \"allow\": true}\n",
		`eriphe-core_run_task({"prompt":"Range le dossier","node":"pc"})`,
		`mcp__eriphe-core__list_tasks {}`,
		`memory_search {"query":"anniversaire"}`,
		"eriphe-core_list_tasks {}\neriphe-core_answer_task {\"task_id\":\"t-1\",\"allow\":false}",
		`{"name":"answer_task","arguments":{"task_id":"t-1","allow":true}}`,
		`{"name": "eriphe-core_run_task", "arguments": "{\"prompt\":\"x\"}"}`,
		`{"type":"function","function":{"name":"run_task","arguments":{"prompt":"x"}}}`,
		`{"tool": "list_tasks", "parameters": {}}`,
		"<tool_call>\n{\"name\": \"answer_task\", \"arguments\": {\"task_id\": \"t-1\"}}\n</tool_call>",
		"```json\n{\"name\": \"answer_task\", \"arguments\": {\"task_id\": \"t-1\"}}\n```",
		"```\neriphe-core_answer_task {\"task_id\":\"t-1\",\"allow\":true}\n```",
		"{\n  \"name\": \"run_task\",\n  \"arguments\": {\n    \"prompt\": \"x\"\n  }\n}",
	}
	for _, s := range calls {
		if !toolCallText(s) {
			t.Errorf("%q is a tool call written as text", s)
		}
	}
	replies := []string{
		"",
		"Prêt.",
		"[rien]",
		"C'est fait : la tâche t-1 a la permission.",
		"Le fichier de config ressemble à ça : {\"port\": 8080}.",
		"Utilise la variable {nom} dans le modèle.",
		"Here is the JSON you asked for: {\"name\": \"Sam\", \"age\": 30}",
		`{"name": "Sam", "age": 30}`,
		`{"name": "Sam Example", "arguments": {}}`,
		`{"name":"answer_task","arguments":{"task_id":"t-1"},"note":"x"}`,
		"```go\nfunc main() { fmt.Println(\"hi\") }\n```",
		"```json\n{\"port\": 8080, \"host\": \"example.org\"}\n```",
		"Voici l'appel :\n```\neriphe-core_answer_task {\"task_id\":\"t-1\"}\n```\nC'est ce que j'ai fait.",
		"J'ai lancé eriphe-core_run_task {\"prompt\":\"x\"} pour toi.",
		"run_task {pas du json}",
		"Sam {\"a\":1}",
		"Deux étapes :\n- eriphe-core_list_tasks {}\n- puis je te réponds.",
		"set_x {\"a\":1}\nEt voilà, c'est fait.",
	}
	for _, s := range replies {
		if toolCallText(s) {
			t.Errorf("%q is a reply, not a tool call", s)
		}
	}
}

func TestToolCallStart(t *testing.T) {
	for _, s := range []string{`eriphe-core_answer_task {"task_id":"t-`, `{"name": "run_`, "<tool_call>\n{", "<tool"} {
		if !toolCallStart(s) {
			t.Errorf("%q starts a tool call", s)
		}
	}
	for _, s := range []string{"", "Je regarde", "Le fichier {\"a\"", "Voici : config_file {", `{"port": 80`} {
		if toolCallStart(s) {
			t.Errorf("%q is a reply being written", s)
		}
	}
}

// A tool call written as the brain's answer is not shown, and the brain is
// asked once to make it.
func TestBrainToolCallTextReasksOnce(t *testing.T) {
	c, _ := foldersCore(t)
	c.brainToolCallText(`eriphe-core_answer_task {"task_id":"t-1","allow":true}`)
	c.brainToolCallText(`eriphe-core_answer_task {"task_id":"t-1","allow":true}`)
	msgs, err := c.store.messagesAfter(0, 50)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, m := range msgs {
		if m.Role == roleBrain && strings.Contains(m.Text, "answer_task") {
			t.Errorf("the written call was shown: %q", m.Text)
		}
		if m.Role == roleEvent && m.Text == toolCallReask {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("asked %d times, want once", n)
	}
	// Something new in the conversation: a later written call is asked about again.
	c.store.addMessage(roleUser, "Et maintenant ?", false)
	c.brainToolCallText(`eriphe-core_list_tasks {}`)
	last, _ := c.store.messagesAfter(c.store.lastMessageSeq()-1, 1)
	if len(last) != 1 || last[0].Text != toolCallReask {
		t.Fatalf("not asked again after a new message: %+v", last)
	}
}
