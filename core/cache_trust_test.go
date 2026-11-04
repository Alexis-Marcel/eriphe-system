package main

import (
	"context"
	"testing"
)

// The brain and a worker started on a tainted turn get the untrusted cache;
// a worker the user started does not.
func TestTaskCacheTrust(t *testing.T) {
	c := brainCore(t, &BrainConfig{})
	ctx := context.Background()
	c.tendAssistants(ctx, "tok")
	if spec, _ := c.store.taskSpec(brainStatus(c.core).Task); !spec.Untrusted {
		t.Error("the brain shares the trusted cache")
	}
	for _, tc := range []struct {
		taint []string
		want  bool
	}{{nil, false}, {[]string{"WebFetch"}, true}} {
		task, err := c.runTask(ctx, runRequest{Prompt: "count the lines", Agent: "sonnet", taint: tc.taint})
		if err != nil {
			t.Fatal(err)
		}
		if spec, _ := c.store.taskSpec(task.ID); spec.Untrusted != tc.want {
			t.Errorf("taint %v: untrusted %v, want %v", tc.taint, spec.Untrusted, tc.want)
		}
	}
	if !taskUntrusted(taskKindMemory, nil) {
		t.Error("memory extraction shares the trusted cache")
	}
}
