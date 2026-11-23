package main

import (
	"context"
	"fmt"
)

// The kinds of proposals, one table: what differs from one kind to another
// is only in its row (how it is read, which wait, how a yes or a no is
// applied, how it is withdrawn, which node it belongs to, whom a decision
// tells). Reading a card, deciding it, withdrawing it and listing what
// waits all go through the same few functions of convo_cards.go, which
// look the kind up here.

// proposalKind is one kind of proposal.
type proposalKind struct {
	// read fills p (its Kind and ID set, Outcome gone) from its records: a
	// missing one stays gone.
	read func(c *core, p proposal) proposal
	// waiting are the ids of its proposals that may wait for the user, the
	// oldest first; each is read again (read) before it is said waiting.
	waiting func(c *core) []int64
	// decides applies the user's answer; it is only called while the
	// proposal waits (decideProposal).
	decides func(c *core, ctx context.Context, id int64, approve bool) error
	// withdraw makes it wait no more, undecided by the user (its node
	// archived, code took it back); nil: decides' no, which only records it.
	withdraw func(c *core, ctx context.Context, id int64) error
	// node is the node it belongs to by its records (a call's item's, a
	// run's), whoever raised it; nil or "": none of its own (node_turn.go).
	node func(c *core, id int64) string
	// decided runs once its card said a decision: by is who answered
	// ("text" for a plain yes or no, told with the user's message).
	decided func(c *core, id int64, approve bool, by string)
}

// proposalKinds names the kinds in the order what waits is listed.
var proposalKinds = []string{cardMemory, cardIntention, cardRule, cardSetting, cardTaskStart, cardUnlearn, cardAccess, cardSkill, cardRunCalls, cardCalls, cardMonitor, cardViewKind}

// kindsOf is the table, by kind; filled in init, since its rows call the
// functions that read it.
var kindsOf map[string]proposalKind

func init() {
	kindsOf = map[string]proposalKind{
		cardMemory: {read: (*core).memoryProposal, waiting: (*core).proposedMemories,
			decides: func(c *core, _ context.Context, id int64, yes bool) error {
				if yes {
					_, err := c.mem.approve(id, "owner")
					return err
				}
				return c.mem.forget(id, "owner", "rejected")
			},
			withdraw: func(c *core, _ context.Context, id int64) error {
				return c.mem.forget(id, "owner", "withdrawn: its node was archived")
			}},
		cardIntention: {read: (*core).intentionProposal, waiting: (*core).proposedIntentions,
			decides: func(c *core, _ context.Context, id int64, yes bool) error {
				ch, action := intentionChange{State: intentOpen}, "approve"
				if !yes {
					ch, action = intentionChange{State: intentCancelled}, "reject"
				}
				_, err := c.mem.changeIntention(id, ch, "owner", action)
				return err
			}},
		cardRule: {read: (*core).ruleProposal, waiting: (*core).proposedRules,
			decides: func(c *core, _ context.Context, id int64, yes bool) error {
				if yes {
					_, err := c.approveNotifyRule(id)
					return err
				}
				return c.deleteNotifyRule(id)
			}},
		cardSetting: {read: (*core).settingProposal, waiting: (*core).proposedSettings,
			decides: func(c *core, _ context.Context, id int64, yes bool) error { return c.decideSetting(id, yes) }},
		cardTaskStart: {read: (*core).taskStartProposal, waiting: (*core).proposedStarts, // task_start.go
			decides: func(c *core, _ context.Context, id int64, yes bool) error { return c.decideTaskStart(id, yes) },
			decided: func(c *core, id int64, yes bool, by string) {
				if !yes && by != byText {
					c.taskStartDeclined(id) // a "non" in words is told with the message
				}
			}},
		cardUnlearn: {read: (*core).unlearnProposal, waiting: (*core).proposedUndos,
			decides: func(c *core, _ context.Context, id int64, yes bool) error {
				e, err := c.mem.learning(id)
				if err != nil {
					return errCardDecided
				}
				if yes {
					_, err := c.unlearn(id, "owner", e.UndoWhy, nil)
					return err
				}
				return c.mem.setUndo(id, undoRejected, e.UndoWhy)
			}},
		cardAccess: {read: (*core).accessProposal, waiting: (*core).proposedAccess, decides: (*core).decideAccess, // access_card.go
			withdraw: func(c *core, ctx context.Context, id int64) error {
				a, err := c.accessAskOf(id)
				if err != nil {
					return errCardDecided
				}
				c.withdrawAsk(ctx, a)
				return nil
			},
			node: func(c *core, id int64) string {
				if a, err := c.accessAskOf(id); err == nil {
					return cmpOr(a.Node, c.taskNode(a.TaskID)) // its call's node, else its task's
				}
				return ""
			},
			decided: func(c *core, id int64, yes bool, by string) {
				if by != byText {
					c.accessDecidedTold(id, yes)
				}
			}},
		cardSkill: {read: (*core).skillProposal, waiting: (*core).proposedSkills, // skill_write.go
			decides: func(c *core, _ context.Context, id int64, yes bool) error { return c.decideSkill(id, yes) }},
		cardRunCalls: {read: (*core).runCallsProposal, waiting: (*core).runsWithCalls, decides: (*core).decideRunCalls, // skill_cards.go
			withdraw: func(c *core, ctx context.Context, id int64) error {
				for _, a := range c.runAsks(id) {
					c.withdrawAsk(ctx, a)
				}
				return nil
			},
			node: func(c *core, id int64) string {
				if r := c.runByID(id); r != nil {
					return c.taskNode(r.Task) // the node its run works in
				}
				return ""
			}},
		cardCalls: {read: (*core).callsProposal, waiting: (*core).proposedBatches, decides: (*core).decideCalls, // item_cards.go
			withdraw: func(c *core, ctx context.Context, id int64) error {
				for _, a := range c.batchAsks(id) {
					c.withdrawAsk(ctx, a)
				}
				return nil
			},
			node: func(c *core, id int64) string {
				if asks := c.batchAsks(id); len(asks) > 0 {
					return cmpOr(asks[0].Node, c.taskNode(asks[0].TaskID)) // its item's, else its run's
				}
				return ""
			},
			decided: func(c *core, id int64, _ bool, _ string) { c.itemCardMoved(id) }}, // its item's row
		cardMonitor: {read: (*core).monitorProposal, waiting: (*core).proposedMonitors, // monitors.go
			decides: func(c *core, _ context.Context, id int64, yes bool) error { return c.decideMonitor(id, yes) }},
		cardViewKind: {read: (*core).viewProposalOf, waiting: (*core).proposedViews, // view_card.go
			decides: func(c *core, _ context.Context, id int64, yes bool) error { return c.decideView(id, yes) },
			withdraw: func(c *core, _ context.Context, id int64) error {
				_, err := c.store.db.Exec(`UPDATE view_proposals SET state = ? WHERE id = ? AND state = ?`, viewDropped, id, viewProposed)
				return err
			},
			node: func(c *core, id int64) string { // its draft: the card waits on it
				var node string
				c.store.db.QueryRow(`SELECT node FROM view_proposals WHERE id = ?`, id).Scan(&node)
				return node
			}},
	}
	for _, k := range proposalKinds {
		if kindsOf[k].read == nil || kindsOf[k].waiting == nil || kindsOf[k].decides == nil {
			panic("proposal kind " + k + " is incomplete")
		}
	}
}

// byText is who answered a card with a plain yes or no in words
// (resolveByText).
const byText = "text"

// The kinds whose records are read here: a memory, an intention, a
// notification rule, a setting, an undo of the learning journal.

func (c *core) memoryProposal(p proposal) proposal {
	if c.mem == nil {
		return p
	}
	x, err := c.mem.memory(p.ID)
	if err != nil {
		return p
	}
	p.Pending, p.Extracted = x.Status == memProposed, x.SourceKind == srcExtraction
	p.Tainted = externalWhy(x.Trust, x.Flags, x.SourceRef)
	switch x.Status {
	case memDeleted:
		p.Outcome = cardRejected
	case memActive, memArchived:
		p.Outcome = cardApproved
	}
	p.text = func(string) string { return x.Text }
	return p
}

// proposedMemories are the memories that wait for a card: the
// extraction's get none, they wait in À valider and expire undecided
// (memory_extract.go).
func (c *core) proposedMemories() []int64 {
	if c.mem == nil {
		return nil
	}
	xs, err := c.mem.list(`status = 'proposed' AND source_kind != ?`, []any{srcExtraction}, `recorded_at, id`, 200)
	if err != nil {
		return nil
	}
	var out []int64
	for _, x := range xs {
		out = append(out, x.ID)
	}
	return out
}

func (c *core) intentionProposal(p proposal) proposal {
	if c.mem == nil {
		return p
	}
	x, err := c.mem.intention(p.ID)
	if err != nil {
		return p
	}
	p.Pending, p.Extracted = x.State == intentProposed, x.SourceKind == srcExtraction
	p.Tainted = externalWhy(x.Trust, x.Flags, x.SourceRef)
	p.Outcome = cardApproved
	if x.State == intentCancelled {
		p.Outcome = cardRejected
	}
	p.text = func(lang string) string {
		if x.DueAt.IsZero() {
			return x.Text
		}
		return x.Text + " · " + x.DueAt.In(c.notifyZone()).Format("2006-01-02 15:04")
	}
	return p
}

func (c *core) proposedIntentions() []int64 {
	if c.mem == nil {
		return nil
	}
	xs, err := c.mem.intentions(intentProposed)
	if err != nil {
		return nil
	}
	var out []int64
	for _, x := range xs {
		out = append(out, x.ID)
	}
	return out
}

func (c *core) ruleProposal(p proposal) proposal {
	r, err := c.notifyRule(p.ID)
	if err != nil {
		return p
	}
	p.Pending, p.Tainted, p.Why, p.Outcome = r.State == ruleProposed, r.Tainted, r.Why, cardApproved
	p.text = func(lang string) string { return ruleText(r, lang) }
	return p
}

func (c *core) proposedRules() []int64 {
	rs, err := c.notifyRules(ruleProposed)
	if err != nil {
		return nil
	}
	var out []int64
	for _, r := range rs {
		out = append(out, r.ID)
	}
	return out
}

func (c *core) settingProposal(p proposal) proposal {
	var name, value, why, tainted string
	err := c.store.db.QueryRow(`SELECT name, value, why, tainted FROM setting_proposals WHERE id = ?`, p.ID).Scan(&name, &value, &why, &tainted)
	if err != nil {
		return p
	}
	p.Pending, p.Tainted, p.Why = true, tainted, why
	p.text = func(lang string) string { return tr(lang, "setting.name."+name) + " : " + value }
	return p
}

func (c *core) proposedSettings() []int64 {
	return c.store.ids(`SELECT id FROM setting_proposals ORDER BY id`)
}

func (c *core) proposedSkills() []int64 {
	return c.store.ids(`SELECT rowid FROM skills WHERE pending != '' AND source = ? ORDER BY rowid`, skillFromEriphe)
}

func (c *core) unlearnProposal(p proposal) proposal {
	if c.mem == nil {
		return p
	}
	e, err := c.mem.learning(p.ID)
	if err != nil {
		return p
	}
	p.Pending, p.Why = e.Undo == undoProposed && e.Status != learnReverted, e.UndoWhy
	p.Outcome = cardApproved
	if e.Undo == undoRejected {
		p.Outcome = cardRejected
	}
	if e.Trust == trustExternal {
		p.Tainted = "external:" + e.Target()
	}
	p.text = func(lang string) string { return fmt.Sprintf(tr(lang, "card.unlearn"), learnedSummary(e, lang)) }
	return p
}

func (c *core) proposedUndos() []int64 {
	if c.mem == nil {
		return nil
	}
	rows, err := c.mem.db.Query(`SELECT id FROM learning_log WHERE undo = ? AND status != ? ORDER BY id`, undoProposed, learnReverted)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			out = append(out, id)
		}
	}
	return out
}

// ids reads a column of ids.
func (s *Store) ids(query string, args ...any) []int64 {
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			out = append(out, id)
		}
	}
	return out
}
