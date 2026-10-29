package main

import "log"

// The first-run interview: once the wizard is over and the user opens the
// workspace, eriphe-core gives the brain one turn marked « premier
// lancement », and the brain writes first, offering a short interview
// (brain-prompt.md, « Premier lancement »). What it learns goes to its
// memory. It is offered once per install, never on an install from before
// this release; the Memory page sends the same turn again on demand.

const (
	// firstRunKey is "due" on a new install until the interview is
	// offered, then "done"; an install from before is "done" at once.
	firstRunKey  = "first_run"
	firstRunDue  = "due"
	firstRunDone = "done"
	// firstRunVia marks the turn, as the voice marks its room.
	firstRunVia  = "premier lancement"
	firstRunText = "Propose-moi l'interview de premier lancement."
)

// noteFirstRun decides, at the first start of this release, whether the
// install is new: one with a conversation already, or whose brain already
// has an agent (the wizard finished, or the file gives it), is not.
func (c *core) noteFirstRun(m *Config) {
	if c.store == nil || c.store.get(firstRunKey) != "" {
		return
	}
	state := firstRunDue
	if c.store.lastMessageSeq() > 0 {
		state = firstRunDone
	} else if b := m.brain(); b != nil && b.Agent != "" {
		state = firstRunDone
	}
	c.store.set(firstRunKey, state)
}

// firstRun gives the brain its first-run turn when it is due: a brain with
// an agent, the wizard over, and nothing said by the brain yet. It fires
// once, even for two pages opened together.
func (c *core) firstRun() {
	b := c.conf().brain()
	if b == nil || b.Agent == "" || c.store.get(firstRunKey) != firstRunDue {
		return
	}
	if c.store.hasBrainMessage() {
		c.store.set(firstRunKey, firstRunDone) // it has spoken already: an install in use
		return
	}
	if !c.store.swap(firstRunKey, firstRunDue, firstRunDone) {
		return
	}
	if err := c.offerInterview(); err != nil {
		log.Printf("first run: %v", err)
	}
}

// offerInterview sends the brain the first-run turn: at first run, or when
// the user asks for the interview again from the Memory page.
func (c *core) offerInterview() error {
	if _, err := c.store.addMessageVia(roleUser, firstRunText, firstRunVia, false); err != nil {
		return err
	}
	c.ping()
	return nil
}

// Hidden tells whether the conversation shows the message: the first-run
// turn is eriphe-core's, not words the user typed.
func (m Message) Hidden() bool { return m.Role == roleUser && m.Via == firstRunVia }
