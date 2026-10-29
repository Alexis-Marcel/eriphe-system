package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"maps"
	"net/http"
	"slices"
	"sort"
	"strings"
	"time"
)

// The dynamic catalog: servers, providers and agents added from the
// workspace (or the API) are kept in eriphe-core's database and merged with
// the configuration file at runtime. The file wins: its entries are
// read-only here, and a workspace entry never shadows one of them. Every
// change goes through the configuration's own checks on the merged result,
// then applies live: the upstream pool adds, drops or reconnects servers,
// local servers are registered again as services, and the scheduler, the
// assistants' tools and list_tools/list_agents read the new snapshot.
//
// The merged configuration is an immutable snapshot (core.conf), swapped
// whole on every change: the many goroutines that read it never see a half
// change.
//
// Credentials are entered where they are used: a workspace entry's secrets
// live in the vault under a name derived from the entry
// (server.<name>.token, provider.<name>.key, service.<name>.<env>), are
// never shown back, and go away with the entry.

const catalogSchema = `CREATE TABLE IF NOT EXISTS catalog (
  kind TEXT NOT NULL, name TEXT NOT NULL, spec TEXT NOT NULL, updated INTEGER NOT NULL,
  PRIMARY KEY (kind, name));`

func init() { extraSchema = append(extraSchema, catalogSchema) }

const (
	kindServer   = "server"
	kindProvider = "provider"
	kindAgent    = "agent"
	kindSetting  = "setting"

	sourceConfig    = "config"
	sourceWorkspace = "workspace"

	settingDefaultAgent = "default_agent"
	// Before the brain was an assistant: its agent, now the workspace's
	// edit of the assistant eriphe (loadCatalog moves it).
	settingBrainAgent = "brain_agent"
)

// dynamicCatalog is what the workspace added.
type dynamicCatalog struct {
	Servers      map[string]Server
	Providers    map[string]Provider
	Agents       map[string]Agent
	DefaultAgent string // a preference, when the configuration sets none
	// The workspace's assistants, and its edits of the file's (assistants.go).
	Assistants map[string]Assistant
}

func newDynamic() dynamicCatalog {
	return dynamicCatalog{Servers: map[string]Server{}, Providers: map[string]Provider{}, Agents: map[string]Agent{},
		Assistants: map[string]Assistant{}}
}

func (d dynamicCatalog) clone() dynamicCatalog {
	return dynamicCatalog{Servers: maps.Clone(d.Servers), Providers: maps.Clone(d.Providers),
		Agents: maps.Clone(d.Agents), DefaultAgent: d.DefaultAgent, Assistants: maps.Clone(d.Assistants)}
}

type catalogRow struct {
	Kind, Name string
	Spec       json.RawMessage
}

func (s *Store) catalogRows() ([]catalogRow, error) {
	rows, err := s.db.Query(`SELECT kind, name, spec FROM catalog ORDER BY kind, name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []catalogRow
	for rows.Next() {
		var r catalogRow
		var spec string
		if err := rows.Scan(&r.Kind, &r.Name, &spec); err != nil {
			return nil, err
		}
		r.Spec = json.RawMessage(spec)
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) putCatalog(kind, name string, v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO catalog (kind, name, spec, updated) VALUES (?, ?, ?, ?)
		ON CONFLICT(kind, name) DO UPDATE SET spec = excluded.spec, updated = excluded.updated`,
		kind, name, string(raw), time.Now().Unix())
	return err
}

func (s *Store) deleteCatalog(kind, name string) error {
	_, err := s.db.Exec(`DELETE FROM catalog WHERE kind = ? AND name = ?`, kind, name)
	return err
}

// forgetOAuth drops a server's OAuth client and tokens: a server removed,
// or pointed elsewhere, must not lend them to whatever takes its name.
func (s *Store) forgetOAuth(server string) {
	s.db.Exec(`DELETE FROM oauth_tokens WHERE server = ?`, server)
	s.db.Exec(`DELETE FROM oauth_clients WHERE server = ?`, server)
}

// mergeCatalog is the configuration with the workspace's entries added.
// Without a default agent in the file, the workspace's preference is used,
// or else the first agent by name. The assistants are the file's (its
// brain section among them) with the workspace's edits, and the
// workspace's own (mergeAssistants).
func mergeCatalog(base *Config, d dynamicCatalog) *Config {
	m := *base
	m.Servers = maps.Clone(base.Servers)
	m.Providers = maps.Clone(base.Providers)
	m.Agents = maps.Clone(base.Agents)
	if m.Servers == nil {
		m.Servers = map[string]Server{}
	}
	if m.Providers == nil {
		m.Providers = map[string]Provider{}
	}
	if m.Agents == nil {
		m.Agents = map[string]Agent{}
	}
	for n, s := range d.Servers {
		if _, ok := base.Servers[n]; !ok {
			m.Servers[n] = s
		}
	}
	for n, p := range d.Providers {
		if _, ok := base.Providers[n]; !ok {
			m.Providers[n] = p
		}
	}
	for n, a := range d.Agents {
		if _, ok := base.Agents[n]; !ok {
			m.Agents[n] = a
		}
	}
	if base.DefaultAgent == "" {
		m.DefaultAgent = d.DefaultAgent
		if _, ok := m.Agents[m.DefaultAgent]; !ok {
			m.DefaultAgent = ""
			if names := slices.Sorted(maps.Keys(m.Agents)); len(names) > 0 {
				m.DefaultAgent = names[0]
			}
		}
	}
	m.Assistants = mergeAssistants(base, &m, d)
	m.Brain = nil // the file's, now the assistant eriphe: read conf().brain()
	m.runSpecs = nil
	return &m
}

// validateLoading is validate while the workspace's entries are loaded one
// by one: an assistant's agent may be one of those not loaded yet.
func (m *Config) validateLoading() error {
	m.fileOnly = true
	defer func() { m.fileOnly = false }()
	return m.validate()
}

// validate runs the configuration's own checks on a merged catalog, and
// reads its local servers' run specs.
func (m *Config) validate() error {
	if err := m.checkAgents(); err != nil {
		return err
	}
	if err := m.checkServers(); err != nil {
		return err
	}
	if err := m.checkAssistants(); err != nil {
		return err
	}
	specs, err := m.localServers()
	if err != nil {
		return err
	}
	m.runSpecs = specs
	return nil
}

// catalogError is a refusal the workspace can word for the user: Key is a
// message of web_text.go, with Arg.
type catalogError struct {
	Status int
	Key    string
	Arg    string
	Err    error
}

func (e *catalogError) Error() string { return e.Err.Error() }

func errReadOnly(kind, name string) error {
	return &catalogError{http.StatusConflict, "catalog.readonly", name,
		fmt.Errorf("%s %s is defined in the configuration file: change it there", kind, name)}
}

func errExists(kind, name string) error {
	return &catalogError{http.StatusConflict, "catalog.exists", name, fmt.Errorf("%s %s already exists", kind, name)}
}

func errNotFound(kind, name string) error {
	return &catalogError{http.StatusNotFound, "catalog.notfound", name, fmt.Errorf("no %s %s in the workspace's catalog", kind, name)}
}

func errInvalid(err error) error {
	return &catalogError{http.StatusBadRequest, "", "", err}
}

// Secrets of workspace entries.

// entrySecret is the vault name of one credential of a workspace entry.
func entrySecret(prefix, name, slot string) string {
	s := prefix + "." + name + "." + slot
	if len(s) > 64 {
		h := sha256.Sum256([]byte(s))
		s = s[:55] + "." + hex.EncodeToString(h[:4])
	}
	return s
}

func serverSecretPrefix(name string) []string {
	return []string{"server." + name + ".", "service." + name + "."}
}

// envSlot is the slot of a local server's environment variable.
func envSlot(env string) string { return strings.ToLower(env) }

// ownerOf tells which workspace entry a vault secret belongs to: its kind,
// entry name and slot, or ok false for a secret named by hand.
func ownerOf(secret string) (kind, name, slot string, ok bool) {
	prefix, rest, found := strings.Cut(secret, ".")
	if !found {
		return "", "", "", false
	}
	name, slot, found = strings.Cut(rest, ".")
	if !found || name == "" || slot == "" {
		return "", "", "", false
	}
	switch prefix {
	case "server", "service", "provider":
		return prefix, name, slot, true
	}
	return "", "", "", false
}

// credentials are secret values entered with an entry: for a server
// "token" (bearer), "client-id" and "client-secret" (a pre-registered
// OAuth client), "env:<NAME>" (a local server's variable); for a provider
// "key". An empty value keeps the current one.
type credentials map[string]string

// bindServerSecrets points a server's secret fields at the vault names of
// the credentials given, and returns what to write there.
func bindServerSecrets(name string, s *Server, creds credentials) (map[string]string, error) {
	writes := map[string]string{}
	for slot, value := range creds {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		switch {
		case slot == "token":
			if s.Auth == nil || s.Auth.OAuth != nil || s.Auth.None || s.local() {
				return nil, errInvalid(errors.New("a token goes with a bearer authentication"))
			}
			v := entrySecret("server", name, "token")
			s.Auth.BearerFile, writes[v] = vaultPrefix+v, value
		case slot == "client-id" || slot == "client-secret":
			if s.Auth == nil || s.Auth.OAuth == nil {
				return nil, errInvalid(errors.New("an OAuth client goes with an OAuth authentication"))
			}
			v := entrySecret("server", name, slot)
			if slot == "client-id" {
				s.Auth.OAuth.ClientIDFile = vaultPrefix + v
			} else {
				s.Auth.OAuth.ClientSecretFile = vaultPrefix + v
			}
			writes[v] = value
		case strings.HasPrefix(slot, "env:"):
			env := strings.TrimPrefix(slot, "env:")
			if !s.local() {
				return nil, errInvalid(errors.New("environment variables are for a local server"))
			}
			spec, err := decodeRun(s.Run)
			if err != nil {
				return nil, errInvalid(err)
			}
			if !envName.MatchString(env) {
				return nil, errInvalid(fmt.Errorf("bad environment variable name %q", env))
			}
			v := entrySecret("service", name, envSlot(env))
			if spec.Env == nil {
				spec.Env = map[string]EnvValue{}
			}
			spec.Env[env] = EnvValue{File: vaultPrefix + v}
			writes[v] = value
			s.Run, _ = json.Marshal(spec)
		default:
			return nil, errInvalid(fmt.Errorf("unknown credential %q", slot))
		}
	}
	return writes, nil
}

func decodeRun(raw json.RawMessage) (RunSpec, error) {
	var spec RunSpec
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&spec); err != nil {
		return spec, fmt.Errorf("run: %w", err)
	}
	return spec, nil
}

// serverRefs are the secret references of a server.
func serverRefs(s Server) []string {
	var out []string
	if a := s.Auth; a != nil {
		out = append(out, a.BearerFile)
		if o := a.OAuth; o != nil {
			out = append(out, o.ClientIDFile, o.ClientSecretFile)
		}
	}
	if s.local() {
		if spec, err := decodeRun(s.Run); err == nil {
			for _, v := range spec.Env {
				out = append(out, v.File)
			}
		}
	}
	return slices.DeleteFunc(out, func(r string) bool { return r == "" })
}

// checkVaultOnly refuses file paths in a workspace entry: the workspace may
// not make eriphe-core read (and send) a file of its own machine. Files
// belong to the configuration.
func checkVaultOnly(what string, refs ...string) error {
	for _, r := range refs {
		if r != "" && !strings.HasPrefix(r, vaultPrefix) {
			return errInvalid(fmt.Errorf("%s: a workspace entry keeps its secrets in the vault (vault:<name>), not in a file", what))
		}
	}
	return nil
}

func checkEntryName(kind, name string) error {
	if !serverName.MatchString(name) || slices.Contains(reserved, name) {
		return &catalogError{http.StatusBadRequest, "catalog.badname", name,
			fmt.Errorf("%s %q: a name of lowercase letters, digits and dashes, up to 32", kind, name)}
	}
	return nil
}

// loadCatalog reads the workspace's entries and keeps those that still
// check against the configuration; the others are left out, with why.
func loadCatalog(base *Config, store *Store) (dynamicCatalog, map[string]string) {
	d := newDynamic()
	skipped := map[string]string{}
	rows, err := store.catalogRows()
	if err != nil {
		log.Printf("catalog: %v", err)
		return d, skipped
	}
	order := map[string]int{kindSetting: 0, kindProvider: 1, kindAgent: 2, kindServer: 3, kindAssistant: 4}
	brainAgent := "" // the setting of before the brain was an assistant
	sort.SliceStable(rows, func(i, j int) bool { return order[rows[i].Kind] < order[rows[j].Kind] })
	for _, r := range rows {
		try := d.clone()
		var err error
		switch r.Kind {
		case kindSetting:
			switch r.Name {
			case settingDefaultAgent:
				json.Unmarshal(r.Spec, &d.DefaultAgent)
			case settingBrainAgent:
				json.Unmarshal(r.Spec, &brainAgent)
			}
			continue
		case kindAssistant:
			var a Assistant
			if err = json.Unmarshal(r.Spec, &a); err == nil {
				try.Assistants[r.Name] = a
			}
		case kindProvider:
			var p Provider
			if err = json.Unmarshal(r.Spec, &p); err == nil {
				if _, ok := base.Providers[r.Name]; ok {
					err = errors.New("shadowed by the configuration file")
				}
				try.Providers[r.Name] = p
			}
		case kindAgent:
			var a Agent
			if err = json.Unmarshal(r.Spec, &a); err == nil {
				if _, ok := base.Agents[r.Name]; ok {
					err = errors.New("shadowed by the configuration file")
				}
				try.Agents[r.Name] = a
			}
		case kindServer:
			var s Server
			if err = json.Unmarshal(r.Spec, &s); err == nil {
				if _, ok := base.Servers[r.Name]; ok {
					err = errors.New("shadowed by the configuration file")
				}
				try.Servers[r.Name] = s
			}
		default:
			continue
		}
		if err == nil {
			err = mergeCatalog(base, try).validateLoading()
		}
		if err != nil {
			log.Printf("catalog: %s %s left out: %v", r.Kind, r.Name, err)
			skipped[r.Kind+"/"+r.Name] = err.Error()
			continue
		}
		d = try
	}
	migrateBrainAgent(base, store, &d, brainAgent)
	return d, skipped
}

// conf is the current configuration: the file merged with the workspace's
// catalog. It is never changed in place.
func (c *core) conf() *Config { return c.snap.Load() }

// source tells where an entry comes from: the configuration file, the
// workspace, or "" if it does not exist.
func (c *core) source(kind, name string) string {
	c.catalogMu.Lock()
	defer c.catalogMu.Unlock()
	var inBase, inDyn bool
	switch kind {
	case kindServer:
		_, inBase = c.base.Servers[name]
		_, inDyn = c.dyn.Servers[name]
	case kindProvider:
		_, inBase = c.base.Providers[name]
		_, inDyn = c.dyn.Providers[name]
	case kindAgent:
		_, inBase = c.base.Agents[name]
		_, inDyn = c.dyn.Agents[name]
	}
	switch {
	case inBase:
		return sourceConfig
	case inDyn:
		return sourceWorkspace
	}
	return ""
}

// skippedEntries are the workspace's entries left out at start, and why.
func (c *core) skippedEntries() map[string]string {
	c.catalogMu.Lock()
	defer c.catalogMu.Unlock()
	return maps.Clone(c.skipped)
}

// reload rebuilds the snapshot from the configuration and the catalog,
// and applies it.
func (c *core) reload() error {
	c.catalogMu.Lock()
	defer c.catalogMu.Unlock()
	m := mergeCatalog(c.base, c.dyn)
	if err := m.validate(); err != nil {
		return err
	}
	c.snap.Store(m)
	c.applyCatalog(m)
	return nil
}

// change is one edit of the workspace's catalog: it edits d, and may name
// vault secrets to write first and servers whose OAuth state to forget.
type change struct {
	writes  map[string]string // vault name → value
	forget  []string          // servers whose OAuth client and tokens go
	event   string            // what the brain is told
	persist func() error      // writes the database
}

// commit checks d merged with the configuration, then writes the secrets,
// the database, swaps the snapshot and applies it. The caller holds catalogMu.
func (c *core) commit(d dynamicCatalog, ch change) error {
	m := mergeCatalog(c.base, d)
	if err := m.validate(); err != nil {
		return errInvalid(err)
	}
	if len(ch.writes) > 0 {
		v := currentVault()
		if v == nil {
			return &catalogError{http.StatusServiceUnavailable, "accounts.novault", "", errNoVault}
		}
		for name, value := range ch.writes {
			if err := v.set(name, value); err != nil {
				return errInvalid(err)
			}
		}
	}
	if err := ch.persist(); err != nil {
		return err
	}
	for _, s := range ch.forget {
		c.store.forgetOAuth(s)
	}
	c.dyn = d
	c.snap.Store(m)
	c.applyCatalog(m)
	c.dropOrphanSecrets(d)
	if ch.event != "" {
		log.Printf("catalog: %s", ch.event)
		if m.brain() != nil {
			c.store.addMessage(roleEvent, ch.event, false)
			c.ping()
		}
	}
	return nil
}

// applyCatalog makes the running parts follow a new snapshot.
func (c *core) applyCatalog(m *Config) {
	setAssistantName(m.assistantName())
	if c.pool != nil {
		c.pool.sync(m.Servers)
	}
	if err := c.registerServices(m.runSpecs); err != nil {
		log.Printf("catalog: services: %v", err)
	}
	c.placePending()
	if c.changed != nil {
		c.bump() // the nodes get ready for the agents as they are now (nodePrepare)
	}
}

// dropOrphanSecrets deletes the vault secrets of workspace entries that no
// longer use them: a removed entry's, or a token replaced by OAuth.
func (c *core) dropOrphanSecrets(d dynamicCatalog) {
	v := currentVault()
	if v == nil {
		return
	}
	used := map[string]bool{}
	use := func(s Server) {
		for _, r := range serverRefs(s) {
			used[strings.TrimPrefix(r, vaultPrefix)] = true
		}
	}
	for _, s := range d.Servers {
		use(s)
	}
	for _, p := range d.Providers {
		used[strings.TrimPrefix(p.Secret, vaultPrefix)] = true
	}
	// Entries left out at start are still stored: their secrets stay too.
	rows, err := c.store.catalogRows()
	if err != nil {
		return
	}
	for _, r := range rows {
		switch r.Kind {
		case kindServer:
			var s Server
			if json.Unmarshal(r.Spec, &s) == nil {
				use(s)
			}
		case kindProvider:
			var p Provider
			if json.Unmarshal(r.Spec, &p) == nil {
				used[strings.TrimPrefix(p.Secret, vaultPrefix)] = true
			}
		}
	}
	// What the configuration names stays, whatever its name.
	for _, r := range c.base.secretRefs() {
		used[strings.TrimPrefix(r.Ref, vaultPrefix)] = true
	}
	list, err := v.list()
	if err != nil {
		return
	}
	for _, s := range list {
		if _, _, _, owned := ownerOf(s.Name); owned && !used[s.Name] {
			if v.remove(s.Name) == nil {
				log.Printf("catalog: secret %s deleted with its entry", s.Name)
			}
		}
	}
}

// Put modes.
const (
	putCreate = "create"
	putUpdate = "update"
	putUpsert = "upsert"
)

func checkPut(mode, kind, name string, inBase, inDyn bool) error {
	if err := checkEntryName(kind, name); err != nil {
		return err
	}
	if inBase {
		return errReadOnly(kind, name)
	}
	if mode == putCreate && inDyn {
		return errExists(kind, name)
	}
	if mode == putUpdate && !inDyn {
		return errNotFound(kind, name)
	}
	return nil
}

// putServer adds or replaces a workspace server. Its credentials are
// stored in the vault; an empty one keeps the current reference.
func (c *core) putServer(mode, name string, s Server, creds credentials) error {
	c.catalogMu.Lock()
	defer c.catalogMu.Unlock()
	_, inBase := c.base.Servers[name]
	old, inDyn := c.dyn.Servers[name]
	if err := checkPut(mode, kindServer, name, inBase, inDyn); err != nil {
		return err
	}
	writes, err := bindServerSecrets(name, &s, creds)
	if err != nil {
		return err
	}
	if err := checkVaultOnly("server "+name, serverRefs(s)...); err != nil {
		return err
	}
	d := c.dyn.clone()
	d.Servers[name] = s
	ch := change{writes: writes, persist: func() error { return c.store.putCatalog(kindServer, name, s) }}
	if inDyn && (old.URL != s.URL || old.authKind() != s.authKind()) {
		ch.forget = []string{name}
	}
	if !inDyn {
		c.store.forgetOAuth(name) // whatever an older server of this name left
	}
	verb := "ajouté"
	if inDyn {
		verb = "modifié"
	}
	ch.event = fmt.Sprintf("Catalogue : l'outil « %s » a été %s depuis l'espace de travail (%s). list_tools le montre.", name, verb, oneLine(truncate(s.Use, 200)))
	return c.commit(d, ch)
}

func (c *core) deleteServer(name string) error {
	c.catalogMu.Lock()
	defer c.catalogMu.Unlock()
	if _, ok := c.base.Servers[name]; ok {
		return errReadOnly(kindServer, name)
	}
	if _, ok := c.dyn.Servers[name]; !ok {
		return errNotFound(kindServer, name)
	}
	d := c.dyn.clone()
	delete(d.Servers, name)
	return c.commit(d, change{forget: []string{name},
		persist: func() error { return c.store.deleteCatalog(kindServer, name) },
		event:   fmt.Sprintf("Catalogue : l'outil « %s » a été retiré depuis l'espace de travail ; ne le prête plus aux tâches.", name)})
}

// putProvider adds or replaces a workspace provider; key, if given, is its
// credential.
func (c *core) putProvider(mode, name string, p Provider, key string) error {
	return c.putProviderAgents(mode, name, p, key, nil, "")
}

func (c *core) deleteProvider(name string) error {
	c.catalogMu.Lock()
	defer c.catalogMu.Unlock()
	if _, ok := c.base.Providers[name]; ok {
		return errReadOnly(kindProvider, name)
	}
	if _, ok := c.dyn.Providers[name]; !ok {
		return errNotFound(kindProvider, name)
	}
	for an, a := range c.conf().Agents {
		if a.Provider == name {
			return &catalogError{http.StatusConflict, "catalog.inuse", an, fmt.Errorf("provider %s is used by agent %s", name, an)}
		}
	}
	d := c.dyn.clone()
	delete(d.Providers, name)
	return c.commit(d, change{persist: func() error { return c.store.deleteCatalog(kindProvider, name) }})
}

func (c *core) putAgent(mode, name string, a Agent) error {
	c.catalogMu.Lock()
	defer c.catalogMu.Unlock()
	_, inBase := c.base.Agents[name]
	_, inDyn := c.dyn.Agents[name]
	if err := checkPut(mode, kindAgent, name, inBase, inDyn); err != nil {
		return err
	}
	d := c.dyn.clone()
	d.Agents[name] = a
	verb := "ajouté"
	if inDyn {
		verb = "modifié"
	}
	return c.commit(d, change{persist: func() error { return c.store.putCatalog(kindAgent, name, a) },
		event: fmt.Sprintf("Catalogue : l'agent « %s » a été %s depuis l'espace de travail (%s, %s). list_agents le montre.",
			name, verb, a.Runtime, oneLine(truncate(a.Use, 200)))})
}

func (c *core) deleteAgent(name string) error {
	c.catalogMu.Lock()
	defer c.catalogMu.Unlock()
	if _, ok := c.base.Agents[name]; ok {
		return errReadOnly(kindAgent, name)
	}
	if _, ok := c.dyn.Agents[name]; !ok {
		return errNotFound(kindAgent, name)
	}
	if b := c.conf().brain(); b != nil && b.Agent == name {
		return &catalogError{http.StatusConflict, "catalog.brainagent", name, fmt.Errorf("agent %s runs the brain", name)}
	}
	for an, a := range c.conf().Assistants {
		if a.Agent == name {
			return &catalogError{http.StatusConflict, "catalog.inuse", an, fmt.Errorf("agent %s runs the assistant %s", name, an)}
		}
	}
	d := c.dyn.clone()
	delete(d.Agents, name)
	return c.commit(d, change{persist: func() error { return c.store.deleteCatalog(kindAgent, name) },
		event: fmt.Sprintf("Catalogue : l'agent « %s » a été retiré depuis l'espace de travail.", name)})
}

// setDefaultAgent picks the agent of a task that names none, unless the
// configuration file sets it.
func (c *core) setDefaultAgent(name string) error {
	c.catalogMu.Lock()
	defer c.catalogMu.Unlock()
	if c.base.DefaultAgent != "" {
		return errReadOnly("default_agent", c.base.DefaultAgent)
	}
	if _, ok := c.conf().Agents[name]; !ok {
		return errNotFound(kindAgent, name)
	}
	d := c.dyn.clone()
	d.DefaultAgent = name
	return c.commit(d, change{persist: func() error { return c.store.putCatalog(kindSetting, settingDefaultAgent, name) },
		event: fmt.Sprintf("Catalogue : l'agent par défaut est maintenant « %s ».", name)})
}

// migrateBrainAgent moves the brain's agent of before it was an assistant
// (the setting brain_agent) to the workspace's edit of the assistant
// eriphe, unless it has one.
func migrateBrainAgent(base *Config, store *Store, d *dynamicCatalog, agent string) {
	if agent == "" {
		return
	}
	defer store.deleteCatalog(kindSetting, settingBrainAgent)
	file, ok := fileAssistants(base)[brainAssistant]
	if ws, done := d.Assistants[brainAssistant]; !ok || file.Agent != "" || (done && ws.Agent != "") {
		return // no brain, or its agent is the file's, or already moved
	}
	ws := d.Assistants[brainAssistant]
	ws.Agent = agent
	if err := store.putCatalog(kindAssistant, brainAssistant, ws); err != nil {
		log.Printf("catalog: brain_agent: %v", err)
		return
	}
	d.Assistants[brainAssistant] = ws
	log.Printf("catalog: the brain's agent %s moved to the assistant %s", agent, brainAssistant)
}
