package main

import (
	"context"
	"errors"
	"testing"

	"github.com/Alexis-Marcel/eriphe-system/core/nodestore"
)

// Opening steps leaves no step without its node, nor a node without its
// step: a step that cannot be made is refused before anything is; a node
// that fails meanwhile takes the rows of the steps not made with it.
func TestOpenStepsNoOrphans(t *testing.T) {
	c := graphCore(t)
	ctx := context.Background()
	parent := workNode(t, c, "")
	rows := func() int {
		var n int
		c.store.db.QueryRow(`SELECT COUNT(*) FROM node_steps WHERE parent = ?`, parent).Scan(&n)
		return n
	}
	children := func() int {
		kids, _ := c.nodes.Children(parent)
		return len(kids)
	}
	// The second step's node cannot be made: nothing is.
	_, err := c.openSteps(ctx, parent, []stepSpec{
		{Key: "a", Title: "A", Prompt: "Fais A."},
		{Key: "b", Title: "B ghp_0123456789abcdefghijABCDEFGHIJ", Prompt: "Fais B."},
	}, 0, stepOpener{author: "brain"})
	if err == nil || rows() != 0 || children() != 0 {
		t.Fatalf("a step refused: %v, %d rows, %d nodes", err, rows(), children())
	}

	// The second node fails as it is made: the first stays with its row,
	// the second has neither, and the work runs the first.
	made := 0
	was := stepCreate
	stepCreate = func(s *nodestore.Store, nw nodestore.New) error {
		if made++; made == 2 {
			return errors.New("disk full")
		}
		return was(s, nw)
	}
	t.Cleanup(func() { stepCreate = was })
	ids, err := c.openSteps(ctx, parent, []stepSpec{
		{Key: "a", Title: "A", Prompt: "Fais A."},
		{Key: "b", Title: "B", Prompt: "Fais B.", After: []string{"a"}},
	}, 0, stepOpener{author: "brain"})
	if err == nil || len(ids) != 1 || rows() != 1 {
		t.Fatalf("a node failed: %v %v, %d rows", err, ids, rows())
	}
	kids, _ := c.nodes.Children(parent)
	for _, k := range kids {
		if _, ok := c.nodeStep(k.ID); !ok {
			t.Errorf("node %s without its step", k.ID)
		}
	}
	steps, _ := c.workSteps(parent)
	for _, st := range steps {
		if _, err := c.nodes.Get(st.Node); err != nil {
			t.Errorf("step %s without its node", st.Node)
		}
	}
	// The step made runs: its work is set up for it.
	if w, ok := c.work(parent); !ok || w.State != workRunning {
		t.Errorf("no running work for the step made: %+v %v", w, ok)
	}
}

// A step's task starts with stepMu let go: while it starts, another work
// moves and the step itself may be dropped, which stops the task started
// for it.
func TestStepStartsOutsideTheLock(t *testing.T) {
	c := graphCore(t)
	ctx := context.Background()
	a, b := workNode(t, c, ""), workNode(t, c, "")
	var bIDs map[string]string
	moved := false
	runStepTask = func(c *core, ctx context.Context, r runRequest) (*TaskRow, error) {
		if st, ok := c.nodeStep(r.inNode); ok && st.Parent == a && !moved {
			moved = true
			if !stepMu.TryLock() {
				t.Error("a step's task starts under stepMu")
			} else {
				stepMu.Unlock()
			}
			var err error
			if bIDs, err = c.openSteps(ctx, b, []stepSpec{{Key: "b", Title: "B", Prompt: "Fais B."}}, 0, stepOpener{author: byBrain}); err != nil {
				t.Error(err)
			}
			if _, err := c.settleStep(ctx, a, st.Node, settleDrop, "plus besoin"); err != nil {
				t.Error(err)
			}
		}
		return c.runTask(ctx, r)
	}
	t.Cleanup(func() { runStepTask = (*core).runTask })
	ids, err := c.openSteps(ctx, a, []stepSpec{{Key: "a", Title: "A", Prompt: "Fais A."}}, 0, stepOpener{author: byBrain})
	if err != nil {
		t.Fatal(err)
	}
	if !moved {
		t.Fatal("the step did not start")
	}
	if st := stepNow(t, c, ids["a"]); st.Status != stepDropped || st.Task != "" {
		t.Errorf("the dropped step: %+v", st)
	}
	if tasks := c.nodeTasks(ids["a"]); len(tasks) != 1 || c.store.get("brain_stopped:"+tasks[0]) == "" {
		t.Errorf("its task is not stopped: %v", tasks)
	}
	if st := stepNow(t, c, bIDs["b"]); st.Status != stepRunning || st.Task == "" {
		t.Errorf("the other work's step: %+v", st)
	}
	if c.claimed(ids["a"]) || c.claimed(bIDs["b"]) {
		t.Error("a claim is left")
	}
}
