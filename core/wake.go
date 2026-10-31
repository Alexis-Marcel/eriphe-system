package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Waking a device (Wake-on-LAN), when the user allows it for that device
// (Settings → Devices, off by default). A node reports the MAC address of
// its network card and its subnet's broadcast address (node/wake.go), kept
// with what it last reported while it is offline. wake sends the magic
// packets twice over:
//
//   - from eriphe-core itself, which reaches the home network when it runs
//     on the host's network (or on the machine itself);
//   - through the machines that stay on (the NAS's node, next to
//     eriphe-core), POST /v1/wake: in its container, the node sends them
//     from a short-lived container on the host's network. eriphe-core, on a
//     Docker bridge network as the installers run it, cannot: a broadcast
//     does not leave the bridge.
//
// Only on the home network: a magic packet is a broadcast, which no router
// carries to another network.
//
// The scheduler wakes the device a task waits for, when the task needs it
// (its folders are there): wakeFor, once per wakeBackoff and device.

func init() {
	extraSchema = append(extraSchema, `
CREATE TABLE IF NOT EXISTS node_wake (
  name TEXT PRIMARY KEY, allowed INTEGER NOT NULL DEFAULT 0,
  last_sent INTEGER NOT NULL DEFAULT 0, last_auto INTEGER NOT NULL DEFAULT 0);`)
}

// wakeBackoff is how long the scheduler waits before waking the same
// device again for a task.
const wakeBackoff = 15 * time.Minute

// wakeWait is how long the workspace waits for a woken device to connect.
const wakeWait = 2 * time.Minute

// wakeInfo is what a node reports so that it can be woken.
type wakeInfo struct {
	MACs      []string `json:"macs,omitempty"`
	Broadcast string   `json:"broadcast,omitempty"`
}

// nodeWakeInfo reads a node's card from what it last reported.
func nodeWakeInfo(n NodeRow) wakeInfo {
	var info struct {
		Wake wakeInfo `json:"wake"`
	}
	json.Unmarshal(n.Info, &info)
	return info.Wake
}

// wakeAllowed says whether the user lets Eriphe wake a device.
func (s *Store) wakeAllowed(name string) bool {
	var v bool
	s.db.QueryRow(`SELECT allowed FROM node_wake WHERE name = ?`, name).Scan(&v)
	return v
}

func (s *Store) setWakeAllowed(name string, allowed bool) error {
	if !s.hasNode(name) {
		return errNoNode
	}
	_, err := s.db.Exec(`INSERT INTO node_wake (name, allowed) VALUES (?, ?)
		ON CONFLICT(name) DO UPDATE SET allowed = excluded.allowed`, name, allowed)
	return err
}

// wakeAutoDue records that the scheduler wakes a device now, unless it did
// within wakeBackoff: false then.
func (s *Store) wakeAutoDue(name string, now time.Time) bool {
	res, err := s.db.Exec(`UPDATE node_wake SET last_auto = ? WHERE name = ? AND allowed = 1 AND last_auto <= ?`,
		now.Unix(), name, now.Add(-wakeBackoff).Unix())
	if err != nil {
		return false
	}
	n, _ := res.RowsAffected()
	return n == 1
}

func (s *Store) wakeSent(name string, now time.Time) {
	s.db.Exec(`UPDATE node_wake SET last_sent = ? WHERE name = ?`, now.Unix(), name)
}

func (s *Store) forgetWake(name string) {
	s.db.Exec(`DELETE FROM node_wake WHERE name = ?`, name)
}

var (
	errWakeNotAllowed = errors.New("waking this device is not allowed: turn it on in Settings → Devices")
	errWakeNoMAC      = errors.New("this device has not reported its network card's MAC address: run its install command again")
	errWakeOnline     = errors.New("this device is already on")
)

// sendWakeHere sends the magic packets from eriphe-core's own network; a
// test replaces it.
var sendWakeHere = func(w wakeInfo) error {
	c, err := net.ListenUDP("udp4", nil)
	if err != nil {
		return err
	}
	defer c.Close()
	sent := 0
	var last error
	for _, mac := range w.MACs {
		p, err := magicPacket(mac)
		if err != nil {
			return err
		}
		for _, t := range wakeTargets(w.Broadcast) {
			addr, err := net.ResolveUDPAddr("udp4", t)
			if err == nil {
				_, err = c.WriteToUDP(p, addr)
			}
			if err != nil {
				last = err
				continue
			}
			sent++
		}
	}
	if sent == 0 {
		return fmt.Errorf("no magic packet sent: %v", last)
	}
	return nil
}

// magicPacket is Wake-on-LAN's packet: six 0xff bytes, then the MAC
// address 16 times.
func magicPacket(mac string) ([]byte, error) {
	hw, err := net.ParseMAC(mac)
	if err != nil || len(hw) != 6 {
		return nil, fmt.Errorf("%q is not a MAC address", mac)
	}
	p := bytes.Repeat([]byte{0xff}, 6)
	for range 16 {
		p = append(p, hw...)
	}
	return p, nil
}

// wakeTargets: the limited broadcast and the device's subnet broadcast,
// on ports 9 and 7.
func wakeTargets(broadcast string) []string {
	hosts := []string{"255.255.255.255"}
	if ip := net.ParseIP(broadcast).To4(); ip != nil && !ip.Equal(net.IPv4bcast) {
		hosts = append(hosts, ip.String())
	}
	var out []string
	for _, h := range hosts {
		out = append(out, net.JoinHostPort(h, "9"), net.JoinHostPort(h, "7"))
	}
	return out
}

// wake sends a device the magic packets, if the user allows it and it is
// off. It returns the ways they went: "eriphe-core" and the names of the
// machines that relayed them.
func (c *core) wake(ctx context.Context, name string) ([]string, error) {
	nodes, err := c.store.nodes()
	if err != nil {
		return nil, err
	}
	var target *NodeRow
	for i := range nodes {
		if nodes[i].Name == name {
			target = &nodes[i]
		}
	}
	switch {
	case target == nil:
		return nil, errNoNode
	case target.Suspended:
		return nil, errNodeSuspended
	case !c.store.wakeAllowed(name):
		return nil, errWakeNotAllowed
	case target.Online:
		return nil, errWakeOnline
	}
	w := nodeWakeInfo(*target)
	if len(w.MACs) == 0 {
		return nil, errWakeNoMAC
	}
	var via, errs []string
	if err := sendWakeHere(w); err != nil {
		errs = append(errs, "eriphe-core: "+err.Error())
	} else {
		via = append(via, "eriphe-core")
	}
	// The machines that stay on, at home with eriphe-core, at the same time.
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, n := range nodes {
		if n.Name == name || !n.Online || n.Suspended || !parseInfo(n).AlwaysOn {
			continue
		}
		wg.Add(1)
		go func(relay string) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(ctx, 40*time.Second)
			defer cancel()
			err := c.node(relay).call(ctx, http.MethodPost, "/v1/wake", w, nil)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs = append(errs, relay+": "+err.Error())
				return
			}
			via = append(via, relay)
		}(n.Name)
	}
	wg.Wait()
	c.store.wakeSent(name, time.Now())
	log.Printf("wake %s (%s): sent through %s%s", name, strings.Join(w.MACs, ", "), orStr(strings.Join(via, ", "), "nothing"),
		map[bool]string{true: "; " + strings.Join(errs, "; "), false: ""}[len(errs) > 0])
	if len(via) == 0 {
		return nil, fmt.Errorf("the wake-up signal could not be sent: %s", strings.Join(errs, "; "))
	}
	return via, nil
}

// wakeFor is the scheduler's hook (placePending): a task waits for a node;
// if it needs one device, the one its folders are on, and that device is
// off, may be woken and was not woken for a task within wakeBackoff, it is
// woken, in the background.
func (c *core) wakeFor(t *TaskRow) {
	if t.Folders == "" {
		return
	}
	_, machine, err := c.folderGrant(splitNames(t.Folders))
	if err != nil || machine == "" {
		return
	}
	nodes, err := c.store.nodes()
	if err != nil {
		return
	}
	for _, n := range nodes {
		if n.Name != machine {
			continue
		}
		if n.Online || n.Suspended || len(nodeWakeInfo(n).MACs) == 0 || !c.store.wakeAutoDue(machine, time.Now()) {
			return
		}
		log.Printf("task %s waits for %s, which is off: waking it", t.ID, machine)
		go func() {
			if _, err := c.wake(context.Background(), machine); err != nil {
				log.Printf("wake %s for task %s: %v", machine, t.ID, err)
			}
		}()
		return
	}
}
