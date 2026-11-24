package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Alexis-Marcel/eriphe-system/core/nodestore"
)

// Every kind of card tells the brain what came of the user's yes or no
// (#750): one test per kind, and a kind without one fails, so that a new
// kind cannot forget to say what became of its cards.

// outcomeMakers raise a waiting card of each kind, each in a core of its
// own; they give its ref.
var outcomeMakers = map[string]func(t *testing.T) (*core, string){
	cardMemory: func(t *testing.T) (*core, string) {
		c, node := outcomeCore(t)
		x := mustAdd(t, c.mem, &Memory{Kind: kindFact, Text: "Le garage ferme lundi", Status: memProposed, SourceKind: srcTask})
		c.cardNow(cardMemory, x.ID, "", nodeRef(node))
		return c, proposalRef(cardMemory, x.ID)
	},
	cardIntention: func(t *testing.T) (*core, string) {
		c, node := outcomeCore(t)
		x := mustIntend(t, c.mem, &Intention{Text: "Relancer Sam", State: intentProposed, SourceKind: srcTask, SourceRef: "task:t-1", Trust: trustOwner})
		c.cardNow(cardIntention, x.ID, "", nodeRef(node))
		return c, proposalRef(cardIntention, x.ID)
	},
	cardRule: func(t *testing.T) (*core, string) {
		c, node := outcomeCore(t)
		r, err := c.addNotifyRule(notifyRule{Kind: ruleTopic, Match: "banque", Ceiling: 2, State: ruleProposed, By: "brain"})
		if err != nil {
			t.Fatal(err)
		}
		c.cardNow(cardRule, r.ID, "", nodeRef(node))
		return c, proposalRef(cardRule, r.ID)
	},
	cardSetting: func(t *testing.T) (*core, string) {
		c, node := outcomeCore(t)
		id, err := c.proposeSetting(settingProposal{Name: setCap, Value: "3", Why: "Trop de notifications."})
		if err != nil {
			t.Fatal(err)
		}
		c.cardNow(cardSetting, id, "", nodeRef(node))
		return c, proposalRef(cardSetting, id)
	},
	cardTaskStart: func(t *testing.T) (*core, string) {
		c, node := outcomeCore(t)
		task := workIn(t, c, node, statePending)
		c.store.updateTask(task, map[string]any{"desired": desiredHeld})
		if err := c.holdTask(&TaskRow{ID: task}, []startNeed{{Server: "nas", Category: "use"}}, nil, false); err != nil {
			t.Fatal(err)
		}
		var id int64
		c.store.db.QueryRow(`SELECT id FROM task_starts WHERE task_id = ?`, task).Scan(&id)
		return c, proposalRef(cardTaskStart, id)
	},
	cardUnlearn: func(t *testing.T) (*core, string) {
		c, node := outcomeCore(t)
		mustAdd(t, c.mem, &Memory{Kind: kindPreference, Text: "Les réunions le matin"})
		e := learnedAll(t, c.mem)[0]
		if err := c.mem.setUndo(e.ID, undoProposed, "demandé"); err != nil {
			t.Fatal(err)
		}
		c.cardNow(cardUnlearn, e.ID, "", nodeRef(node))
		return c, proposalRef(cardUnlearn, e.ID)
	},
	cardAccess: func(t *testing.T) (*core, string) { // what a task asks for itself
		c, node := outcomeCore(t)
		task := workIn(t, c, node, stateRunning)
		id := c.newAccessAsk(accessQuestion{TaskID: task, Need: accessNeed{Server: "nas", Category: catChange}})
		return c, proposalRef(cardAccess, id)
	},
	cardSkill: func(t *testing.T) (*core, string) {
		c, node := outcomeCore(t)
		k, _, err := c.store.addSkillVersion(skillFiles(skillMD), skillFromEriphe)
		if err != nil {
			t.Fatal(err)
		}
		var id int64
		c.store.db.QueryRow(`SELECT rowid FROM skills WHERE name = ?`, k.Name).Scan(&id)
		c.cardNow(cardSkill, id, "", nodeRef(node))
		return c, proposalRef(cardSkill, id)
	},
	cardRunCalls: func(t *testing.T) (*core, string) {
		c, now := skillCore(t)
		addTriggered(t, c, "---\nname: prod\ndescription: la prod\nwhen: monitor.received\ntools: [monitor]\n---\nEnquête.\n")
		r := startedRun(t, c, now, "prod")
		ev := []EventRow{{Seq: 1, Time: time.Now(), Type: "permission",
			Data: map[string]any{"id": "p1", "tool_name": "mcp__monitor__rollback", "input": map[string]any{"version": "2.0.0"}}}}
		c.store.addEvents(r.Task, ev)
		c.applyPolicy(r.Task, ev)
		endTurn(t, c, r.Task, "Je propose de remettre la 2.0.0.")
		p, ok := runCard(c, cardRunCalls)
		if !ok {
			t.Fatal("no run's card")
		}
		return c, proposalRef(cardRunCalls, p.ID)
	},
	cardCalls: func(t *testing.T) (*core, string) {
		c, _, _, r := mailViewOf(t)
		keep(c, r.Task, "p1", "mcp__monitor__move", map[string]any{"account": "sam@example.org", "ids": []any{"m2"}, "to": "archive"})
		endTurn(t, c, r.Task, "Un mail à archiver.")
		calls := cardsOf(c, cardCalls)
		if len(calls) != 1 {
			t.Fatalf("calls cards %+v", calls)
		}
		return c, proposalRef(cardCalls, calls[0].ID)
	},
	cardMonitor: func(t *testing.T) (*core, string) {
		c, node := outcomeCore(t)
		res, err := c.store.db.Exec(`INSERT INTO monitors (name, state, spec, created, updated) VALUES ('disque', ?, '{}', 1, 1)`, monitorProposed)
		if err != nil {
			t.Fatal(err)
		}
		id, _ := res.LastInsertId()
		c.cardNow(cardMonitor, id, "", nodeRef(node))
		return c, proposalRef(cardMonitor, id)
	},
	cardViewKind: func(t *testing.T) (*core, string) {
		c, _, _, ref := daySubject(t)
		return c, ref
	},
}

// outcomeCore is a core with a graph, and a node its cards wait in.
func outcomeCore(t *testing.T) (*core, string) {
	t.Helper()
	c, _ := testMemoryCore(t)
	openGraph(t, c)
	c.useNodes(c.nodes)
	return c, mustNode(t, c, nodestore.New{Title: "Le sujet"})
}

// toldSince are the events that told the brain what came of the user's
// act, after seq.
func toldSince(t *testing.T, c *core, seq int) []Message {
	t.Helper()
	msgs, err := c.store.queryMessages(`SELECT `+messageCols+` FROM messages WHERE seq > ? AND role = ? AND user_act = 1 ORDER BY seq`, seq, roleEvent)
	if err != nil {
		t.Fatal(err)
	}
	return msgs
}

func TestEveryCardKindTellsItsOutcome(t *testing.T) {
	for _, kind := range proposalKinds {
		mk, ok := outcomeMakers[kind]
		if !ok {
			t.Errorf("kind %s: no test that the brain is told what came of its cards (outcomeMakers)", kind)
			continue
		}
		for _, yes := range []bool{true, false} {
			word := map[bool]string{true: "oui", false: "non"}[yes]
			t.Run(kind+" "+word, func(t *testing.T) {
				c, ref := mk(t)
				before := c.store.lastMessageSeq()
				if _, err := c.decideCard(context.Background(), cardOfRef(t, c, ref).ID, yes, "web"); err != nil {
					t.Fatal(err)
				}
				c.yeses.Wait() // a call's yes is told once it ran
				told := toldSince(t, c, before)
				if len(told) != 1 || !strings.Contains(told[0].Text, "a dit "+word) {
					t.Fatalf("the brain was told %+v", told)
				}
			})
		}
	}
}

// A view's yes is told in the subject it came from: what the subject
// says of its draft is to be updated, and the view's node is named.
func TestViewOutcomeToldInItsSubject(t *testing.T) {
	c, subject, draft, ref := daySubject(t)
	before := c.store.lastMessageSeq()
	if _, err := c.decideCard(context.Background(), cardOfRef(t, c, ref).ID, true, "web"); err != nil {
		t.Fatal(err)
	}
	told := toldSince(t, c, before)
	if len(told) != 1 || told[0].Node != subject {
		t.Fatalf("told %+v, want an event of the subject %s", told, subject)
	}
	for _, want := range []string{ref, "nœud " + draft, "plus un brouillon", "(" + subject + ")"} {
		if !strings.Contains(told[0].Text, want) {
			t.Errorf("the event lacks %q: %s", want, told[0].Text)
		}
	}
	if strings.Contains(told[0].Text, "tourne") { // neither a monitor nor a skill was made
		t.Errorf("the event says something runs: %s", told[0].Text)
	}
	// No: the draft is archived, told in the subject too.
	c, subject, draft, ref = daySubject(t)
	before = c.store.lastMessageSeq()
	if _, err := c.decideCard(context.Background(), cardOfRef(t, c, ref).ID, false, "web"); err != nil {
		t.Fatal(err)
	}
	if told := toldSince(t, c, before); len(told) != 1 || told[0].Node != subject || !strings.Contains(told[0].Text, draft+", est archivé") {
		t.Fatalf("told %+v", told)
	}
}

// A plain « oui » in words is told with the user's message, which says
// what came of it; no event besides.
func TestOutcomeToldWithAPlainYes(t *testing.T) {
	c, _ := netCore(t, "Tu peux régler ça ?", "")
	r := proposedRule(t, c, "banque", "")
	ref := proposalRef(cardRule, r.ID)
	seq, _ := c.store.addMessage(roleBrain, "Je te propose une règle pour la banque.", true)
	c.attachTurnCards(int(seq))
	yes := ownerSays(c, "oui")
	if _, ok := c.resolveByText(context.Background(), yes); !ok {
		t.Fatal("the plain yes did not answer the card")
	}
	if told := toldSince(t, c, int(seq)); len(told) != 0 {
		t.Errorf("an event besides the message: %+v", told)
	}
	if line := c.cardAnswerLine(yes); !strings.Contains(line, ref) || !strings.Contains(line, "la règle est en vigueur") {
		t.Errorf("the message's line: %q", line)
	}
}
