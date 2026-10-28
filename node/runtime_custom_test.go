package main

import (
	"slices"
	"strings"
	"testing"
)

// Another ACP agent runs its command as is; a bad one fails, saying why.
func TestCustomACPCommand(t *testing.T) {
	n := newNode(options{callbackURL: "http://n"})
	task := &Task{Spec: TaskSpec{Runtime: "acp", Command: []string{"goose", "acp", "--with-builtin", "developer"}}}
	args, env := customACPRuntime.command(n, task)
	if !slices.Equal(args, task.Spec.Command) || len(env) != 0 {
		t.Errorf("args %v, env %v", args, env)
	}
	if rt, err := runtimeFor("acp"); err != nil || rt == nil {
		t.Fatal(err)
	}
	for _, bad := range [][]string{nil, {"sh", "-c", "goose acp"}, {"/usr/bin/env", "goose"}, {"goose acp"}, {"../goose"}, {"goose", "a\nb"}} {
		args, _ := customACPRuntime.command(n, &Task{Spec: TaskSpec{Command: bad}})
		if len(args) != 5 || args[0] != "sh" || !strings.Contains(args[2], "exit 64") || args[4] == "" {
			t.Errorf("%q runs: %v", bad, args)
		}
	}
	if err := checkACPCommand([]string{"/opt/qwen/bin/qwen", "--acp"}); err != nil {
		t.Errorf("absolute path refused: %v", err)
	}
}
