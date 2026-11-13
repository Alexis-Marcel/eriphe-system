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

// setServer changes a catalog server in a new snapshot of the configuration,
// as a saved change does, and returns its servers: the pool's controllers
// read the current snapshot meanwhile, which never changes.
func setServer(c *core, name string, f func(*Server)) map[string]Server {
	m := *c.conf()
	m.Servers = maps.Clone(m.Servers)
	s := m.Servers[name]
	f(&s)
	m.Servers[name] = s
	c.snap.Store(&m)
	return m.Servers
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
