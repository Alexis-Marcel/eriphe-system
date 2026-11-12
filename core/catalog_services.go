package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// The catalog as an owner of services (service_owners.go): its local
// servers, each the service of its name, run by a node and reached by the
// pool (pool.go). Its names are any but a channel's.

func init() {
	registerServiceOwner(serviceOwner{Name: "catalog",
		Owns:    func(name string) bool { return !strings.HasPrefix(name, channelServicePrefix) },
		Specs:   func(c *core) (map[string]RunSpec, error) { return c.conf().runSpecs, nil },
		Running: func(c *core, name string) { c.wakeUpstream(name) }})
}

// localServers reads the run specs of the catalog's local servers.
func (c *Config) localServers() (map[string]RunSpec, error) {
	specs := map[string]RunSpec{}
	for name, s := range c.Servers {
		if !s.local() {
			continue
		}
		var spec RunSpec
		dec := json.NewDecoder(bytes.NewReader(s.Run))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&spec); err != nil {
			return nil, fmt.Errorf("server %s: run: %w", name, err)
		}
		spec.Own = ownImage(spec.Image)
		specs[name] = spec
	}
	return checkServices(specs)
}

// ownImage tells one of Eriphe's own tools' images, by its card: it runs
// the version of eriphe-core (RunSpec.Own, pinnedImage).
func ownImage(image string) bool {
	return slices.ContainsFunc(toolCards, func(c toolCard) bool { return c.OwnVersion && c.Image == image })
}

// ownToolVersion is the version one of Eriphe's own tools must be, the
// one of eriphe-core; "" for another server, or a development build.
func ownToolVersion(s Server) string {
	if !s.local() || version == "" || version == "dev" {
		return ""
	}
	spec, err := decodeRun(s.Run)
	if err != nil {
		return ""
	}
	image := spec.Image
	if i := strings.LastIndexByte(image, ':'); i > strings.LastIndexByte(image, '/') {
		image = image[:i]
	}
	if !ownImage(image) {
		return ""
	}
	return version
}

// behind tells a tool that said its version, and not the one it must be.
func behind(got, want string) bool { return got != "" && got != "dev" && got != want }

// wakeUpstream tells the pool that a local server it waits for is up,
// rather than letting it wait for its backoff. A live session stays.
func (c *core) wakeUpstream(name string) {
	if c.pool == nil {
		return
	}
	u := c.pool.get(name)
	if u == nil {
		return
	}
	u.mu.Lock()
	idle := u.session == nil
	u.mu.Unlock()
	if idle {
		select {
		case u.wake <- struct{}{}:
		default:
		}
	}
}
