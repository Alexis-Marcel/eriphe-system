package main

import (
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"
)

// placement is what a task asks of its device.
type placement struct {
	Runtime string
	Service bool     // a local MCP server: any device that runs services, whatever its runtimes
	Needs   []string // labels required
	Prefers []string // soft: cpu, memory, or a label
	Image   string   // worker image; the device's default if empty
	Arch    string   // a service whose image runs on this architecture only (amd64); none: any
	// Bound: the task has the user's folders. Hosts are then the only
	// devices that fit, those holding each of them up to date now
	// (folderHosts, folder_copies.go); Holders those holding them at all,
	// up to date or not: while none of Hosts is online, the task waits for
	// one of them.
	Bound   bool
	Hosts   []string
	Holders []string
	// Resident: an assistant that someone waits on (placement.go). Hard
	// rule: an online always-on device that fits, if there is one.
	Resident bool
}

func (p placement) String() string {
	s := p.Runtime
	if p.Service {
		s = "MCP services"
	}
	if p.Arch != "" {
		s += " on " + p.Arch
	}
	if len(p.Needs) > 0 {
		s += " with " + strings.Join(p.Needs, ", ")
	}
	switch {
	case p.Bound && len(p.Hosts) > 0:
		s += " on " + strings.Join(p.Hosts, " or ") + ", which hold its folders up to date"
	case p.Bound:
		s += " on a machine that holds its folders up to date"
	}
	return s
}

// deviceInfo is what a device reports about itself (GET /v1/device).
type deviceInfo struct {
	CPUs     int      `json:"cpus"`
	Arch     string   `json:"arch"` // amd64 or arm64
	Memory   int64    `json:"memory_bytes"`
	Image    string   `json:"image"` // its default worker image
	Images   []string `json:"images"`
	Runtimes []string `json:"runtimes"`
	// The runtimes whose agent the device has unpacked: their tasks start at
	// once (device/inject.go). None from an older device.
	AgentsReady []string    `json:"agents_ready"`
	Labels      []string    `json:"labels"`
	Services    bool        `json:"services"`  // it runs local MCP servers
	AlwaysOn    bool        `json:"always_on"` // it never sleeps: the tools go there first
	Version     string      `json:"version"`   // eriphe-device's; empty from an older one
	Install     string      `json:"install"`   // how it was installed: linux, macos, windows, or empty (by hand)
	Update      updaterInfo `json:"update"`    // its updater, from the installer (machine_updates.go)
	// The folders shared on it, by the user there (device/shared.go); none
	// from an older one.
	Folders []sharedFolder `json:"folders"`
	// It lends a model server through its tunnel (its -model-server,
	// model_relay.go).
	ModelServer bool `json:"model_server"`
	// Its Syncthing, for the copies of shared folders: its device ID and
	// the state of each folder synced there (folder_copies.go); nil
	// without one.
	Syncthing *deviceSyncthing `json:"syncthing"`
}

func parseInfo(n DeviceRow) deviceInfo {
	var info deviceInfo
	json.Unmarshal(n.Info, &info)
	return info
}

// waitingError means no online device can run a task now, but a registered
// one could by what it last reported: the task can wait for it.
type waitingError struct{ reason string }

func (e *waitingError) Error() string { return e.reason }

// Scores, in two passes like the Kubernetes scheduler. The filter keeps the
// online devices that offer the runtime and carry every needed label. Each one
// left then scores: +1 per CPU if the task prefers cpu, +1 per GiB of RAM if
// it prefers memory, +8 per preferred label it carries, +2 if the task's
// image is already there, +2 if its agent is unpacked there, and -4 per
// active task. The highest score wins; ties go to the first name.
const (
	scoreLabel  = 8
	scoreImage  = 2
	scoreAgent  = 2
	scoreActive = -4
)

// schedule picks the device for a task: the requested one if it is online and
// fits, otherwise the best scoring device; for a resident task, the best of
// the online always-on devices that fit when there is one. If none is online
// but one would fit, it returns a *waitingError.
func schedule(devices []DeviceRow, want string, p placement, active func(string) (int, error)) (string, error) {
	if want != "" {
		for _, n := range devices {
			if n.Name != want {
				continue
			}
			if !n.Online {
				return "", fmt.Errorf("device %s is offline", want)
			}
			if !p.fits(n) {
				return "", fmt.Errorf("device %s cannot run %s", want, p)
			}
			return n.Name, nil
		}
		return "", fmt.Errorf("unknown device %s", want)
	}
	if p.Resident {
		if steady := steadyDevices(devices, p); len(steady) > 0 {
			devices = steady
		}
	}
	devices = slices.Clone(devices)
	sort.Slice(devices, func(i, j int) bool { return devices[i].Name < devices[j].Name })
	best, bestScore := "", 0
	var offline []string
	for _, n := range devices {
		if !p.fits(n) {
			continue
		}
		if !n.Online {
			offline = append(offline, n.Name)
			continue
		}
		load, err := active(n.Name)
		if err != nil {
			return "", err
		}
		if s := score(parseInfo(n), p, load); best == "" || s > bestScore {
			best, bestScore = n.Name, s
		}
	}
	if best == "" && len(offline) == 0 && p.Bound {
		// Its folders are on a machine that can run it, not up to date
		// there yet: it waits for them.
		free := p
		free.Bound = false
		for _, n := range devices {
			if slices.Contains(p.Holders, n.Name) && free.fits(n) {
				offline = append(offline, n.Name)
			}
		}
		if len(offline) > 0 {
			return "", &waitingError{fmt.Sprintf("waiting for the folders to be up to date on a machine that can run %s (%s)", free, strings.Join(offline, ", "))}
		}
	}
	switch {
	case best != "":
		return best, nil
	case len(offline) > 0:
		return "", &waitingError{fmt.Sprintf("waiting for a device that can run %s (offline: %s)", p, strings.Join(offline, ", "))}
	}
	return "", fmt.Errorf("no device can run %s", p)
}

func score(info deviceInfo, p placement, load int) int {
	s := scoreActive * load
	for _, pref := range p.Prefers {
		switch pref {
		case "cpu":
			s += info.CPUs
		case "memory":
			s += int(info.Memory >> 30)
		default:
			if slices.Contains(info.Labels, pref) {
				s += scoreLabel
			}
		}
	}
	image := p.Image
	if image == "" {
		image = info.Image
	}
	if image != "" && slices.ContainsFunc(info.Images, func(i string) bool { return sameImage(i, image) }) {
		s += scoreImage
	}
	if p.Runtime != "" && slices.Contains(info.AgentsReady, p.Runtime) {
		s += scoreAgent
	}
	return s
}

// sameImage compares image references, an untagged one meaning :latest.
func sameImage(a, b string) bool {
	tag := func(s string) string {
		if i := strings.LastIndex(s, ":"); i < 0 || strings.Contains(s[i:], "/") {
			return s + ":latest"
		}
		return s
	}
	return strings.EqualFold(tag(a), tag(b))
}
