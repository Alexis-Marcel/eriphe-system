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
	// the second has neither.
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
}
