package main

import (
	"fmt"
	"maps"
	"slices"
	"strings"
)

// putProviderAgents adds or replaces a workspace provider and, in the same
// change, the agents created with it; def, if set, becomes the default
// agent when there was no agent yet. key, if given, is the credential: for
// a ChatGPT subscription, the tokens of its sign-in.
func (c *core) putProviderAgents(mode, name string, p Provider, key string, agents map[string]Agent, def string) error {
	c.catalogMu.Lock()
	defer c.catalogMu.Unlock()
	_, inBase := c.base.Providers[name]
	_, inDyn := c.dyn.Providers[name]
	if err := checkPut(mode, kindProvider, name, inBase, inDyn); err != nil {
		return err
	}
	writes := map[string]string{}
	if key = strings.TrimSpace(key); key != "" {
		v := entrySecret("provider", name, "key")
		p.Secret, writes[v] = vaultPrefix+v, key
	}
	if err := checkVaultOnly("provider "+name, p.Secret); err != nil {
		return err
	}
	d := c.dyn.clone()
	d.Providers[name] = p
	for _, n := range slices.Sorted(maps.Keys(agents)) {
		_, inBase := c.base.Agents[n]
		_, inDyn := c.dyn.Agents[n]
		if err := checkPut(putCreate, kindAgent, n, inBase, inDyn); err != nil {
			return err
		}
		d.Agents[n] = agents[n]
	}
	// The first agents of the workspace: the preset's default is the default.
	if def != "" && c.base.DefaultAgent == "" && len(c.conf().Agents) == 0 {
		d.DefaultAgent = def
	}
	ch := change{writes: writes, persist: func() error {
		if err := c.store.putCatalog(kindProvider, name, p); err != nil {
			return err
		}
		for n, a := range agents {
			if err := c.store.putCatalog(kindAgent, n, a); err != nil {
				return err
			}
		}
		if d.DefaultAgent != c.dyn.DefaultAgent {
			return c.store.putCatalog(kindSetting, settingDefaultAgent, d.DefaultAgent)
		}
		return nil
	}}
	if len(agents) > 0 {
		names := slices.Sorted(maps.Keys(agents))
		ch.event = fmt.Sprintf("Catalogue : le compte « %s » a été ajouté depuis l'espace de travail, avec les agents %s. list_agents les montre.",
			name, strings.Join(names, ", "))
	}
	return c.commit(d, ch)
}
