package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
)

// The WireGuard network from the machine (wireguard.go), for its owner:
//
//	eriphe-core wireguard show    # on or off, the network, the devices
//	eriphe-core wireguard open    # the workspace on its usual port again
//
// open is the way back in when the workspace is served through WireGuard
// only and no device can reach it any more; it takes effect at the next
// start of eriphe-core.

const wireguardUsage = `usage:
  eriphe-core wireguard show [-config file | -data dir]
  eriphe-core wireguard open [-config file | -data dir]

show prints the WireGuard network's state; open serves the workspace on its
usual port again (from the next start), when it is served through WireGuard only.`

func wireguardCommand(args []string) {
	if err := wireguardCLI(args, os.Stdout); err != nil {
		if err == flag.ErrHelp {
			os.Exit(2)
		}
		log.Fatal(err)
	}
}

func wireguardCLI(args []string, out io.Writer) error {
	if len(args) == 0 || (args[0] != "show" && args[0] != "open") {
		fmt.Fprintln(os.Stderr, wireguardUsage)
		return flag.ErrHelp
	}
	fs, path, data := authFlags(args[0])
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	_, store, err := authStore(*path, *data)
	if err != nil {
		return err
	}
	defer store.db.Close()
	return wireguardCLIStore(store, args, out)
}

func wireguardCLIStore(store *Store, args []string, out io.Writer) error {
	var s wgSettings
	if raw := store.get(wgKey); raw != "" {
		if err := json.Unmarshal([]byte(raw), &s); err != nil {
			return err
		}
	}
	if args[0] == "open" {
		if !s.WorkspaceOnly {
			fmt.Fprintln(out, "The workspace is not served through WireGuard only: nothing to do.")
			return nil
		}
		s.WorkspaceOnly = false
		raw, _ := json.Marshal(s)
		if err := store.set(wgKey, string(raw)); err != nil {
			return err
		}
		fmt.Fprintln(out, "The workspace will be served on its usual port again: restart eriphe-core.")
		return nil
	}
	state := "off"
	if s.Enabled {
		state = "on"
	}
	fmt.Fprintf(out, "WireGuard network: %s\nnetwork: %s\nendpoint: %s\nworkspace through WireGuard only: %v\ndevices: %d\n",
		state, s.Network, s.Endpoint, s.WorkspaceOnly, len(s.Peers))
	for _, p := range s.Peers {
		fmt.Fprintf(out, "  %-15s %s (added %s)\n", p.Address, p.Name, p.Created.Format("2006-01-02"))
	}
	return nil
}
