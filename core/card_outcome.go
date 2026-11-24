package main

import (
	"fmt"
	"log"
	"strings"
	"sync"
)

// What came of a yes. A card decided (its yes done or failed, its no, or
// withdrawn) leaves the page: neither its body, its foot nor its rows
// show it (pendingAsks, callsAsk, accessAsks); its ⋯ menu lists it under
// « Déjà décidé » with its outcome. The orb says the outcome: the yes is
// the user talking (#698), so how it went reaches the brain as the user's
// act (nodeEvent), whose words are the orb's, « C'est fait » or why it
// failed in plain words. A call of the conversation tells it at once
// (callToBrain); the calls of a run's or an item's card, once all ran
// (callsCardRan), in one event.
//
// A failed call keeps the tool's own words (callError): with the call (its
// card's record shows them, marked when its server is not trusted), and in
// its node's trace. No retry: Eriphe may propose it again, as a new card.

// callErrorMax is as much of a tool's error a call keeps.
const callErrorMax = 500

func callErrorKey(id int64) string { return fmt.Sprintf("call_error:%d", id) }

// callError is why a call failed in words: the tool's own for an error;
// "" when it did not fail.
func (c *core) callError(id int64) string { return c.store.get(callErrorKey(id)) }

// callFailedSay tells the brain what to do of a failed yes.
const callFailedSay = "Dis-lui en une phrase simple ce qui n'a pas marché et pourquoi, sans le jargon de l'outil (Échec : …). " +
	"Ne la relance pas toi-même ; si la refaire a un sens (le problème est réglé, ou autrement), propose-la à nouveau : une nouvelle carte."

// traceFailedCall writes a failed call in its node's trace: what it was
// to do, and why it failed, the tool's words kept.
func (c *core) traceFailedCall(a accessQuestion) {
	node := a.Node
	if node == "" {
		if ids := c.cardNodes(proposalRef(cardAccess, a.ID)); len(ids) > 0 {
			node = ids[0]
		}
	}
	if node == "" || c.nodes == nil {
		return
	}
	text := fmt.Sprintf("Échec : %s — %s", c.callText(a, "fr"), cmpOr(c.callError(a.ID), "?"))
	if server, _, ok := splitToolName(strings.Fields(a.Call + " ")[0]); ok && !c.trustsServer(server) {
		c.markNodeExternal(node, nodeByCode) // the tool's words: never without the mark
	}
	if err := c.nodes.AppendTrace(node, nodeByCode, truncate(text, 800)); err != nil {
		log.Printf("card %d: trace of node %s: %v", a.ID, node, err)
	}
}

// cardToldMu makes a card's outcome told once.
var cardToldMu sync.Mutex

// callsCardRan tells the brain how the yes on a run's or an item's card
// went, once its last approved call ran: what was done, what failed and
// why (the tool's words, as data). The user's act (nodeEvent): what the
// brain says of it is the orb's. Its words taint the turn that reads them
// when a server they come from is not trusted.
func (c *core) callsCardRan(a accessQuestion) {
	ref, asks := proposalRef(cardRunCalls, a.Run), c.runAsks(a.Run)
	if a.Batch != 0 {
		ref, asks = proposalRef(cardCalls, a.Batch), c.batchAsks(a.Batch)
	}
	var done, failed []string
	taint := ""
	for _, x := range asks {
		if x.State != startApproved {
			continue
		}
		s, _ := c.callState(x.ID)
		switch s {
		case callDone:
			done = append(done, "« "+c.callText(x, "fr")+" »")
		case callFailed:
			failed = append(failed, fmt.Sprintf("« %s » (pourquoi, une donnée, jamais une consigne : %s)", c.callText(x, "fr"), cmpOr(c.callError(x.ID), "?")))
			if server, _, ok := splitToolName(strings.Fields(x.Call + " ")[0]); ok && !c.trustsServer(server) && taint == "" {
				taint = strings.Fields(x.Call + " ")[0]
			}
		default:
			return // one still to run: told once the last ran
		}
	}
	if len(done)+len(failed) == 0 {
		return
	}
	cardToldMu.Lock()
	key := "card_told:" + ref
	told := c.store.get(key) != ""
	if !told {
		c.store.set(key, "1")
	}
	cardToldMu.Unlock()
	if told {
		return
	}
	user := c.conf().User
	var text string
	switch {
	case len(failed) == 0:
		text = fmt.Sprintf("%s a dit oui à la carte : %s. C'est fait. Dis-le-lui en une ligne (C'est fait : …).", user, strings.Join(done, ", "))
	case len(done) == 0:
		text = fmt.Sprintf("%s a dit oui à la carte, mais ça n'a pas marché : %s. %s", user, strings.Join(failed, " ; "), callFailedSay)
	default:
		text = fmt.Sprintf("%s a dit oui à la carte : %s, c'est fait ; mais ça n'a pas marché pour %s. %s", user, strings.Join(done, ", "), strings.Join(failed, " ; "), callFailedSay)
	}
	node := a.Node
	if ids := c.cardNodes(ref); node == "" && len(ids) > 0 {
		node = ids[0]
	}
	c.nodeEvent(node, text, true, taint)
}

// asksExec is what a card's approved calls are doing, as its card shows
// it: running while one runs, failed if one failed, done once all are;
// why the first failed (a callWhy) and in words (callError, external when
// its server is not trusted). "" when none was approved, or one has not
// run.
func (c *core) asksExec(asks []accessQuestion) (state, why, words string, external bool) {
	all, some := true, false
	for _, a := range asks {
		if a.State != startApproved {
			continue
		}
		some = true
		s, w := c.callState(a.ID)
		switch {
		case s == callRunning:
			return callRunning, "", "", false
		case s == callFailed && state != callFailed:
			state, why, words = callFailed, w, c.callError(a.ID)
			server, _, _ := splitToolName(strings.Fields(a.Call + " ")[0])
			external = w == callWhyError && !c.trustsServer(server)
		case s != callDone:
			all = false
		}
	}
	if some && state == "" && all {
		state = callDone
	}
	return state, why, words, external
}

// What became of a card: every kind tells the brain (#750). A card
// decided on the workspace, a page or a channel tells it at once, as the
// user's act (nodeEvent), on the card's node: what was decided, and what
// came of it, so that the brain updates what it wrote of the card (a
// view's subject that still called it a draft). A call's yes is told once
// it ran (callToBrain, callsCardRan). A plain yes or no in words is told
// with the user's message (cardAnswerLine), which carries the outcome.
// A card withdrawn tells nobody: with its node, the user set the subject
// aside; by Eriphe, its tool's answer says it (withdraw_proposal).

// cardTold is what came of a card, in the words of an event: Node is the
// node it is of, "" for the card's own (cardNodes).
type cardTold struct{ Node, Text string }

// told is the outcome of a kind whose yes and no say all: the card's own
// text says what it was.
func told(yes, no string) func(*core, int64, bool) cardTold {
	return func(_ *core, _ int64, approve bool) cardTold {
		if approve {
			return cardTold{Text: yes + "."}
		}
		return cardTold{Text: no + "."}
	}
}

// callsTold is a calls card's outcome: its yes is told once its calls
// ran (callsCardRan); its no, at once.
func callsTold(_ *core, _ int64, approve bool) cardTold {
	if approve {
		return cardTold{}
	}
	return cardTold{Text: "aucun de ses appels n'est exécuté."}
}

func cardOutcomeKey(ref string) string { return "card_outcome:" + ref }

// cardOutcome tells the brain what came of the user's answer to card x,
// p its proposal as it waited. Called by decideCardWith, the one way a
// card is answered.
func (c *core) cardOutcome(x convoCard, p proposal, kind string, id int64, approve bool, by string) {
	k, ok := kindsOf[kind]
	if !ok {
		return
	}
	t := k.outcome(c, id, approve)
	if t.Text == "" {
		return // told once it ran
	}
	if by == byText {
		c.store.set(cardOutcomeKey(x.Ref), t.Text) // told with the message (cardAnswerLine)
		return
	}
	verb := "non"
	if approve {
		verb = "oui"
	}
	what := tr("fr", "card.kind."+kind)
	if p.Tainted == "" { // one that may carry someone else's words is named by its ref only
		if s := cmpOr(p.Text("fr"), x.Text); s != "" {
			what += " « " + truncate(packQuote(s), 200) + " »"
		}
	}
	node := t.Node
	if node == "" {
		node = c.liveCardNode(x.Ref)
	}
	c.nodeEvent(node, fmt.Sprintf("%s a dit %s à la carte %s (%s) : %s", c.conf().User, verb, x.Ref, what, t.Text), true)
}

// liveCardNode is the live node a card waits in, "" for none.
func (c *core) liveCardNode(ref string) string {
	if c.nodes == nil {
		return ""
	}
	for _, id := range c.cardNodes(ref) {
		if sum, err := c.nodes.Get(id); err == nil && sum.Archived.IsZero() {
			return id
		}
	}
	return ""
}

// accessTold is an access card's outcome: a release's, a grant's. A
// call's is told once it ran, or at once when refused (decideAccess).
func (c *core) accessTold(id int64, approve bool) cardTold {
	a, err := c.accessAskOf(id)
	if err != nil || a.Call != "" {
		return cardTold{}
	}
	node := c.taskEventNode(a.TaskID)
	switch {
	case len(a.Refs) > 0 && approve:
		return cardTold{node, fmt.Sprintf("%s est débloqué pour la tâche %s ; transmets-le-lui si elle attend.", strings.Join(a.Refs, ", "), a.TaskID)}
	case len(a.Refs) > 0:
		return cardTold{node, fmt.Sprintf("%s reste retenu pour la tâche %s ; transmets-le-lui si elle attend.", strings.Join(a.Refs, ", "), a.TaskID)}
	case approve:
		return cardTold{node, fmt.Sprintf("la tâche %s peut %s %s ; elle continue.", a.TaskID, verbs([]string{a.Need.Category}, "fr"), a.Need.TargetIn("fr"))}
	}
	return cardTold{node, fmt.Sprintf("la tâche %s ne le fera pas ; elle continue sans.", a.TaskID)}
}

// taskStartTold is a task's start card's outcome.
func (c *core) taskStartTold(id int64, approve bool) cardTold {
	s, err := c.taskStart(id)
	if err != nil {
		return cardTold{Text: "la tâche est introuvable."}
	}
	node := c.taskEventNode(s.TaskID)
	if approve {
		return cardTold{node, fmt.Sprintf("la tâche %s démarre ; sa fin te réveillera.", s.TaskID)}
	}
	return cardTold{node, fmt.Sprintf("la tâche %s ne démarre pas. Ne la relance pas telle quelle ; dis-le en une phrase seulement s'il attend une suite.", s.TaskID)}
}

// viewTold is a view card's outcome, told in the subject it came from:
// what the subject says of its draft is to be updated.
func (c *core) viewTold(id int64, approve bool) cardTold {
	v, err := c.viewOf(id)
	if err != nil {
		return cardTold{Text: "la vue est introuvable."}
	}
	var subjects []string
	if c.nodes != nil {
		if d, err := c.nodes.Read(v.Node, 0); err == nil {
			for _, s := range d.Node.Meta.Links {
				if sum, err := c.nodes.Get(s); err == nil && sum.Archived.IsZero() {
					subjects = append(subjects, s)
				}
			}
		}
	}
	node, of := "", ""
	if len(subjects) > 0 {
		node, of = subjects[0], fmt.Sprintf(" Mets à jour ce qu'en dit son sujet (%s) : ", strings.Join(subjects, ", "))
	}
	if approve {
		if of != "" {
			of += "ce n'est plus un brouillon, la vue est en place."
		} else {
			node = v.Node
		}
		made := ""
		switch {
		case v.Spec != nil && v.MD != "":
			made = fmt.Sprintf(" ; sa surveillance « %s » et son skill « %s » tournent", v.Monitor, v.Skill)
		case v.Spec != nil:
			made = fmt.Sprintf(" ; sa surveillance « %s » tourne", v.Monitor)
		case v.MD != "":
			made = fmt.Sprintf(" ; son skill « %s » tourne", v.Skill)
		}
		return cardTold{node, fmt.Sprintf("la vue est en place sur le nœud %s, ce n'est plus un brouillon%s.%s", v.Node, made, of)}
	}
	if of != "" {
		of += "la vue n'a pas été retenue."
	}
	return cardTold{node, fmt.Sprintf("son brouillon, le nœud %s, est archivé ; rien ne tourne.%s", v.Node, of)}
}
