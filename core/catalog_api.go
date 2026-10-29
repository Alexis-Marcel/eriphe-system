package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"sort"
)

// The catalog API (bearer token): what the workspace manages, for the CLI
// and scripts. Entries of the configuration file are listed with source
// "config" and refused on writes; secrets go in "secrets" (servers) or
// "key" (providers), are stored in the vault and never come back.
//
//	GET    /v1/catalog              servers, providers, agents, default agent, with their source
//	PUT    /v1/servers/{name}       a server as in the configuration, plus {"secrets": {"token": "…"}}
//	DELETE /v1/servers/{name}
//	PUT    /v1/providers/{name}     {"kind", "key"}
//	DELETE /v1/providers/{name}
//	PUT    /v1/agents/{name}        an agent as in the configuration
//	DELETE /v1/agents/{name}
//	PUT    /v1/default-agent        {"name"}
//	PUT    /v1/brain-agent          {"name"}: the agent of the assistant eriphe (assistants.go)

func (c *core) catalogRoutes(mux *http.ServeMux, token string) {
	mux.Handle("GET /v1/catalog", bearer(token, http.HandlerFunc(c.handleCatalog)))
	mux.Handle("PUT /v1/servers/{name}", bearer(token, http.HandlerFunc(c.handlePutServer)))
	mux.Handle("DELETE /v1/servers/{name}", bearer(token, http.HandlerFunc(c.handleDeleteServer)))
	mux.Handle("PUT /v1/providers/{name}", bearer(token, http.HandlerFunc(c.handlePutProvider)))
	mux.Handle("DELETE /v1/providers/{name}", bearer(token, http.HandlerFunc(c.handleDeleteProvider)))
	mux.Handle("PUT /v1/agents/{name}", bearer(token, http.HandlerFunc(c.handlePutAgent)))
	mux.Handle("DELETE /v1/agents/{name}", bearer(token, http.HandlerFunc(c.handleDeleteAgent)))
	mux.Handle("PUT /v1/default-agent", bearer(token, http.HandlerFunc(c.handleDefaultAgent)))
	mux.Handle("PUT /v1/brain-agent", bearer(token, http.HandlerFunc(c.handleBrainAgent)))
}

type catalogEntry[T any] struct {
	Name   string `json:"name"`
	Source string `json:"source"` // config or workspace
	Spec   T      `json:"spec"`
}

type catalogView struct {
	Servers      []catalogEntry[Server]   `json:"servers"`
	Providers    []catalogEntry[Provider] `json:"providers"`
	Agents       []catalogEntry[Agent]    `json:"agents"`
	DefaultAgent string                   `json:"default_agent"`
	// DefaultSource is config when the file sets the default agent.
	DefaultSource string            `json:"default_source"`
	BrainAgent    string            `json:"brain_agent,omitempty"`
	BrainSource   string            `json:"brain_source,omitempty"` // config when the file names it
	Skipped       map[string]string `json:"skipped,omitempty"`      // workspace entries left out at start
}

func entries[T any](all map[string]T, base map[string]T) []catalogEntry[T] {
	out := []catalogEntry[T]{}
	for n, v := range all {
		src := sourceWorkspace
		if _, ok := base[n]; ok {
			src = sourceConfig
		}
		out = append(out, catalogEntry[T]{Name: n, Source: src, Spec: v})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (c *core) catalog() catalogView {
	m := c.conf()
	v := catalogView{Servers: entries(m.Servers, c.base.Servers), Providers: entries(m.Providers, c.base.Providers),
		Agents: entries(m.Agents, c.base.Agents), DefaultAgent: m.DefaultAgent, DefaultSource: sourceWorkspace,
		Skipped: c.skippedEntries()}
	if c.base.DefaultAgent != "" {
		v.DefaultSource = sourceConfig
	}
	if b := m.brain(); b != nil {
		v.BrainAgent, v.BrainSource = b.Agent, c.brainAgentSource(b.Name)
	}
	return v
}

func (c *core) handleCatalog(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, c.catalog())
}

func catalogFail(w http.ResponseWriter, err error) {
	var ce *catalogError
	if errors.As(err, &ce) {
		http.Error(w, err.Error(), ce.Status)
		return
	}
	http.Error(w, err.Error(), http.StatusInternalServerError)
}

func decodeBody(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		http.Error(w, "invalid JSON body: "+err.Error(), http.StatusBadRequest)
		return false
	}
	return true
}

func (c *core) handlePutServer(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Server
		Secrets credentials `json:"secrets,omitempty"`
	}
	if !decodeBody(w, r, &in) {
		return
	}
	if err := c.putServer(putUpsert, r.PathValue("name"), in.Server, in.Secrets); err != nil {
		catalogFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, c.listServers(r.PathValue("name"), false))
}

func (c *core) handleDeleteServer(w http.ResponseWriter, r *http.Request) {
	if err := c.deleteServer(r.PathValue("name")); err != nil {
		catalogFail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (c *core) handlePutProvider(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Provider
		Key string `json:"key,omitempty"`
	}
	if !decodeBody(w, r, &in) {
		return
	}
	if err := c.putProvider(putUpsert, r.PathValue("name"), in.Provider, in.Key); err != nil {
		catalogFail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (c *core) handleDeleteProvider(w http.ResponseWriter, r *http.Request) {
	if err := c.deleteProvider(r.PathValue("name")); err != nil {
		catalogFail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (c *core) handlePutAgent(w http.ResponseWriter, r *http.Request) {
	var a Agent
	if !decodeBody(w, r, &a) {
		return
	}
	if err := c.putAgent(putUpsert, r.PathValue("name"), a); err != nil {
		catalogFail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (c *core) handleDeleteAgent(w http.ResponseWriter, r *http.Request) {
	if err := c.deleteAgent(r.PathValue("name")); err != nil {
		catalogFail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (c *core) handleBrainAgent(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name string `json:"name"`
	}
	if !decodeBody(w, r, &in) {
		return
	}
	if err := c.setBrainAgent(in.Name); err != nil {
		catalogFail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (c *core) handleDefaultAgent(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name string `json:"name"`
	}
	if !decodeBody(w, r, &in) {
		return
	}
	if err := c.setDefaultAgent(in.Name); err != nil {
		catalogFail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
