package main

import (
	"context"
	"log"
	"slices"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// A server may tell when one of its resources changed
// (notifications/resources/updated), to a client that subscribed to it
// (resources/subscribe). eriphe-core follows the resources its surfaces
// read (surfaces.go): the pool keeps, per server, the URIs it is asked to
// follow, subscribes to them on each new session, and hands each update
// to resourceUpdated. A server that cannot subscribe is read at the
// surface's interval instead.

// subscribes tells whether the server's session offers resources/subscribe.
func (u *upstream) subscribes() bool {
	u.mu.Lock()
	sess := u.session
	u.mu.Unlock()
	if sess == nil {
		return false
	}
	r := sess.InitializeResult()
	return r != nil && r.Capabilities != nil && r.Capabilities.Resources != nil && r.Capabilities.Resources.Subscribe
}

// follow sets the resources eriphe-core wants to be told about: the new
// ones are subscribed to, those no longer wanted unsubscribed from.
func (u *upstream) follow(uris []string) {
	uris = slices.Clone(uris)
	slices.Sort(uris)
	uris = slices.Compact(uris)
	u.mu.Lock()
	old := u.followed
	u.followed = uris
	u.mu.Unlock()
	var add, drop []string
	for _, x := range uris {
		if !slices.Contains(old, x) {
			add = append(add, x)
		}
	}
	for _, x := range old {
		if !slices.Contains(uris, x) {
			drop = append(drop, x)
		}
	}
	u.subscribe(add, drop)
}

// resubscribe subscribes again to what is followed: a new session knows
// nothing of the last one's subscriptions.
func (u *upstream) resubscribe() {
	u.mu.Lock()
	uris := slices.Clone(u.followed)
	u.mu.Unlock()
	u.subscribe(uris, nil)
}

func (u *upstream) subscribe(add, drop []string) {
	if len(add)+len(drop) == 0 || !u.subscribes() {
		return
	}
	u.mu.Lock()
	sess := u.session
	u.mu.Unlock()
	if sess == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for _, x := range add {
		if err := sess.Subscribe(ctx, &mcp.SubscribeParams{URI: x}); err != nil {
			log.Printf("server %s: subscribe %s: %v", u.name, x, err)
		}
	}
	for _, x := range drop {
		sess.Unsubscribe(ctx, &mcp.UnsubscribeParams{URI: x})
	}
}

// updated hands a resource's update to the pool's hook.
func (u *upstream) updated(uri string) {
	if u.p != nil && u.p.resourceUpdated != nil {
		u.p.resourceUpdated(u.name, uri)
	}
}
