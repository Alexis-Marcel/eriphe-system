package main

import (
	"fmt"
	"strings"
)

// What Eriphe settles for an assistant shows in the thread (#554).
//
// A task's question reaches the brain, not the user: the brain answers it
// with answer_task and the user never saw it. When the task is a step of
// a thread, code writes what the brain did on the thread's log, in plain
// words, so the user sees it there and can contradict it. Display only:
// nothing here answers a card, grants or releases anything. A release is
// logged as what it is, the brain asking the user on a card, then the
// user's own answer on it, never as a decision taken for them.

// logTaskAnswer writes a line on the thread of the task's step, if the
// task is one; who says who did it (brain, user).
func (c *core) logTaskAnswer(task, who, line string) {
	st, ok := c.stepOfTask(task)
	if !ok {
		return
	}
	c.logThread(st.Thread, who, fmt.Sprintf("Étape « %s » : %s", st.Title, line))
}

// logBrainReply is answer_task's reply, sent to the task.
func (c *core) logBrainReply(task, reply string) {
	c.logTaskAnswer(task, byBrain, "question de l'assistant → Eriphe a répondu : "+strings.TrimSpace(reply))
}

// logBrainChecks is answer_task's fix of a contract's checks.
func (c *core) logBrainChecks(task string, checks []taskCheck, reply string) {
	names := make([]string, 0, len(checks))
	for _, k := range checks {
		names = append(names, k.Name)
	}
	line := "Eriphe a corrigé les contrôles de l'assistant (" + strings.Join(names, ", ") + ")"
	if reply = strings.TrimSpace(reply); reply != "" {
		line += " et lui a répondu : " + reply
	} else {
		line += "."
	}
	c.logTaskAnswer(task, byBrain, line)
}

// logReleaseAsked is answer_task's release: an ask on a card, nothing more.
func (c *core) logReleaseAsked(task string, refs []string) {
	c.logTaskAnswer(task, byBrain, fmt.Sprintf("Eriphe a demandé à %s, sur une carte, de débloquer %s : rien n'est débloqué avant sa réponse.",
		c.conf().User, strings.Join(refs, ", ")))
}

// logReleaseDecided is the user's answer on that card.
func (c *core) logReleaseDecided(task string, refs []string, approve bool) {
	word := "a refusé de débloquer"
	if approve {
		word = "a débloqué"
	}
	c.logTaskAnswer(task, byUser, fmt.Sprintf("%s %s %s sur la carte.", c.conf().User, word, strings.Join(refs, ", ")))
}
