package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Eriphe takes back his own cards (#750). A card the work made obsolete
// (a skill's batch once the items changed, a call no longer needed) is
// withdrawn by the brain, not left for the user to refuse one by one. It
// only takes back: the card is gone, nothing it asked runs, and nothing
// is decided for the user, no yes and no no of theirs. It never touches a
// card Eriphe did not make: what a task asks for itself (a grant, a
// release), a task's start (stop_task stops it), the review of a tool's
// changes. To replace one, the brain proposes the new one as usual.

// cardByEriphe is who decided a card Eriphe withdrew.
const cardByEriphe = "eriphe"

var errNotEriphes = errors.New("this card is not one you made: only the user answers it (a task's start: stop_task)")

type cardWithdrawIn struct {
	Ref string `json:"ref" jsonschema:"the card's ref, as its tool or the pending cards give it (view:3, calls:12)"`
}

func registerCardWithdrawTool(s *mcp.Server, c *core) {
	tool(s, "withdraw_proposal", "Withdraw a card of yours, still waiting, that the work made obsolete: nothing of it runs, nothing is decided for the user. Propose its replacement as usual.",
		func(ctx context.Context, in cardWithdrawIn) (map[string]any, error) {
			return c.brainWithdrawCard(ctx, in.Ref)
		})
}

// brainWithdrawCard withdraws a card of Eriphe's that waits.
func (c *core) brainWithdrawCard(ctx context.Context, ref string) (map[string]any, error) {
	kind, id, ok := parseProposalRef(ref)
	if !ok {
		return nil, fmt.Errorf("%q is no card's ref", ref)
	}
	if c.cardState(ref) != cardPending {
		return nil, errCardDecided
	}
	if !c.eriphesCard(kind, id) {
		return nil, errNotEriphes
	}
	c.withdrawCard(ctx, ref, cardByEriphe)
	if c.cardState(ref) == cardPending {
		return nil, fmt.Errorf("%s still waits: it could not be withdrawn", ref)
	}
	return map[string]any{"withdrawn": ref, "note": "The user no longer sees it; nothing of it runs."}, nil
}

// eriphesCard tells a proposal Eriphe made: what the brain proposed, a
// call of its own, and what its skills' runs keep for a yes. Not what a
// task asks for itself, a task's start, or a review of tools.
func (c *core) eriphesCard(kind string, id int64) bool {
	switch kind {
	case cardTaskStart:
		return false
	case cardAccess:
		a, err := c.accessAskOf(id)
		if err != nil || a.Call == "" {
			return false // a grant or a release a task waits on
		}
		t, err := c.store.task(a.TaskID)
		return err == nil && t.Kind == "brain"
	case cardCalls:
		asks := c.batchAsks(id)
		return len(asks) > 0 && asks[0].TaskID != "" // a tools review's has no task
	}
	return true
}
