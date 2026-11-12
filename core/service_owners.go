package main

import (
	"log"
	"strings"
)

// Who wants services (services.go): its owners, registered at start. The
// catalog wants its local servers (catalog_services.go); a channel that
// needs a container of its own wants it while it is on, named
// channel-<id> (channelServiceName), outside the catalog: not a tool, in
// no list of tools, reached by the channel alone (service_client.go).
//
// reconcileServices asks every owner for what it wants now and makes their
// union the desired services: no owner can drop another's, and none has
// to be asked first. A name is one owner's (Owns): one an owner does not
// own is left out, and said. An owner that cannot say keeps what it has.

// serviceOwner is who wants services.
type serviceOwner struct {
	Name string
	// Owns tells whether a service's name is its own.
	Owns func(name string) bool
	// Specs are the services it wants now, by name.
	Specs func(c *core) (map[string]RunSpec, error)
	// Running is told that one of its services runs, its node said so;
	// nil: nothing to do.
	Running func(c *core, name string)
}

var serviceOwners []serviceOwner

// registerServiceOwner registers an owner, from an init.
func registerServiceOwner(o serviceOwner) { serviceOwners = append(serviceOwners, o) }

// channelServicePrefix starts the names of the channels' services, which
// the catalog never holds.
const channelServicePrefix = "channel-"

// channelServiceName is the name of a channel's service.
func channelServiceName(id string) string { return channelServicePrefix + id }

// serviceOwnerOf is the owner of a service's name.
func serviceOwnerOf(name string) (serviceOwner, bool) {
	for _, o := range serviceOwners {
		if o.Owns(name) {
			return o, true
		}
	}
	return serviceOwner{}, false
}

// reconcileServices makes every owner's services the desired ones.
func (c *core) reconcileServices() error {
	rows, err := c.store.services()
	if err != nil {
		return err
	}
	want := map[string]RunSpec{}
	for _, o := range serviceOwners {
		specs, err := o.Specs(c)
		if err != nil {
			log.Printf("services: %s: %v; its services stay as they are", o.Name, err)
			for _, r := range rows {
				if o.Owns(r.Name) {
					want[r.Name] = r.Spec
				}
			}
			continue
		}
		for name, spec := range specs {
			if !o.Owns(name) {
				log.Printf("services: %s wants %s, a name that is not its own: left out", o.Name, name)
				continue
			}
			want[name] = spec
		}
	}
	return c.registerServices(want)
}

// serviceRunning tells a service's owner that it runs.
func (c *core) serviceRunning(name string) {
	if o, ok := serviceOwnerOf(name); ok && o.Running != nil {
		o.Running(c, name)
	}
}

// isChannelService tells a name of a channel's service, which no catalog
// server takes.
func isChannelService(name string) bool { return strings.HasPrefix(name, channelServicePrefix) }
