package main

import (
	"context"
	"maps"
)

// setBrain makes b the brain of the current snapshot, as a test sets it;
// nil leaves the core without one.
func setBrain(c *core, b *BrainConfig) {
	m := *c.conf()
	m.Assistants = maps.Clone(m.Assistants)
	if m.Assistants == nil {
		m.Assistants = map[string]Assistant{}
	}
	delete(m.Assistants, brainAssistant)
	if b != nil {
		m.Assistants[brainAssistant] = b.assistant()
	}
	c.snap.Store(&m)
}

// editBrain changes the brain of the current snapshot.
func editBrain(c *core, f func(*Assistant)) {
	m := *c.conf()
	m.Assistants = maps.Clone(m.Assistants)
	a := m.Assistants[brainAssistant]
	f(&a)
	m.Assistants[brainAssistant] = a
	c.snap.Store(&m)
}

// setAssistant adds an assistant to the current snapshot.
func setAssistant(c *core, a Assistant) {
	m := *c.conf()
	m.Assistants = maps.Clone(m.Assistants)
	if m.Assistants == nil {
		m.Assistants = map[string]Assistant{}
	}
	m.Assistants[a.Name] = a
	c.snap.Store(&m)
}

// brainStatus is the brain's status; setBrainStatus changes it.
func brainStatus(c *core) AssistantStatus { return c.assistantStatus(brainAssistant) }

func setBrainStatus(c *core, f func(*AssistantStatus)) { c.updateAssistant(brainAssistant, f) }

func setBrainTask(c *core, id string) {
	setBrainStatus(c, func(st *AssistantStatus) { st.Task = id })
}

// tendBrain is one pass of the controller over the brain.
func tendBrain(ctx context.Context, c *core) error {
	return c.tendAssistant(ctx, *c.conf().brain(), "tok")
}

// checkAll is the configuration's checks of its agents and its assistants
// (the brain among them).
func checkAll(c *Config) error {
	if err := c.checkAgents(); err != nil {
		return err
	}
	return mergeCatalog(c, newDynamic()).checkAssistants()
}

// theBrain is the brain's spec.
func theBrain(c *core) Assistant { return *c.conf().brain() }
