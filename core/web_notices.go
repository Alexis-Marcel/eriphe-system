package main

import (
	"regexp"
	"slices"
)

// The events of the conversation are notes for the brain: a tool or an
// agent added from the workspace, a task's turn over, a permission asked.
// The user does not see them as messages: they did the change themselves,
// or the brain tells them what matters. Only a task that ended badly gets
// a short line of its own, which opens its drawer.

// taskEndedEvent is the brain's note of a task that ended (notifyBrain),
// and taskEndedRe reads it back.
const taskEndedEvent = "La tâche %s (nœud %s) est maintenant %s."

var taskEndedRe = regexp.MustCompile(`^La tâche (\S+) \(nœud [^)]*\) est maintenant (\w+)\.`)

// eventNotice is the line the workspace shows for an event.
type eventNotice struct {
	Key   string // its words, with the task's title
	Title string
	Task  string // the task it opens
}

// notice is what the user sees of an event, nil for a note to the brain.
func (w *web) notice(m Message) *eventNotice {
	if m.Role != roleEvent {
		return nil
	}
	sub := taskEndedRe.FindStringSubmatch(m.Text)
	if sub == nil || !slices.Contains([]string{stateFailed, stateStopped, stateLost}, sub[2]) {
		return nil
	}
	n := &eventNotice{Key: "event.task." + sub[2], Title: sub[1], Task: sub[1]}
	if t, err := w.c.store.task(sub[1]); err == nil {
		n.Title = taskTitle(t.Prompt) // push.go
	}
	return n
}

// dress gives the events of a page of messages what the user sees of them.
func (w *web) dress(msgs []Message) {
	for i := range msgs {
		msgs[i].Notice = w.notice(msgs[i])
	}
}
