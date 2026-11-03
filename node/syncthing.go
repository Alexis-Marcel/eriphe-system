package main

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Syncthing on this machine, for the copies of shared folders. It is not
// the node's child: it runs beside it, started by the installation (a
// container sharing the node's network on Linux, its own LaunchAgent on a
// Mac, so that macOS grants folder access to Syncthing's signed binary and
// not to the node; a process of the WSL distribution on Windows). The node
// drives it through its REST API only, on the loopback, with the API key
// of its configuration, which never leaves the machine.
//
// The installation writes Syncthing's first configuration (config.xml in
// its home, -syncthing-home): the API on 127.0.0.1 with a generated key,
// listening on 127.0.0.1 only, and discovery, relays, NAT traversal, usage
// and crash reports and upgrades off, from its very first start. The node
// reads the API's address and key there, refuses an API or a listening
// address off the loopback, and sets those options again at every start:
// whatever changed them, Syncthing reaches no one but through eriphe-core
// (syncpeer.go).

// syncthingConfig is what the node reads of Syncthing's config.xml.
type syncthingConfig struct {
	GUI struct {
		Address string `xml:"address"`
		APIKey  string `xml:"apikey"`
		TLS     bool   `xml:"tls,attr"`
	} `xml:"gui"`
	Options struct {
		Listen []string `xml:"listenAddress"`
	} `xml:"options"`
}

// readSyncthingConfig reads home/config.xml: the API's address and key,
// and where Syncthing listens for its peers, all on the loopback.
func readSyncthingConfig(home string) (api, key, listen string, err error) {
	raw, err := os.ReadFile(filepath.Join(home, "config.xml"))
	if err != nil {
		return "", "", "", err
	}
	var c syncthingConfig
	if err := xml.Unmarshal(raw, &c); err != nil {
		return "", "", "", fmt.Errorf("Syncthing's config.xml: %v", err)
	}
	if !loopbackHostPort(c.GUI.Address) || c.GUI.TLS {
		return "", "", "", fmt.Errorf("Syncthing's API is at %q: it must be on the loopback, without TLS (127.0.0.1:18384)", c.GUI.Address)
	}
	if len(c.GUI.APIKey) < 16 {
		return "", "", "", errors.New("Syncthing's configuration has no API key: install again")
	}
	if len(c.Options.Listen) != 1 {
		return "", "", "", fmt.Errorf("Syncthing listens at %q: one address on the loopback is expected (tcp://127.0.0.1:22100)", c.Options.Listen)
	}
	listen, ok := strings.CutPrefix(c.Options.Listen[0], "tcp://")
	if !ok || !loopbackHostPort(listen) {
		return "", "", "", fmt.Errorf("Syncthing listens at %q: it must be tcp:// on the loopback", c.Options.Listen[0])
	}
	return "http://" + c.GUI.Address, c.GUI.APIKey, listen, nil
}

// loopbackHostPort: an IP address of the loopback, and a port.
func loopbackHostPort(addr string) bool {
	return checkSyncthingListen(addr) == nil && addr != ""
}

// syncthing is a client of Syncthing's REST API.
type syncthing struct {
	base   string // http://127.0.0.1:<port>
	key    string
	listen string // where it listens for peers: tcp:// on the loopback
	http   *http.Client
}

func newSyncthing(api, key, listen string) *syncthing {
	return &syncthing{base: api, key: key, listen: listen, http: &http.Client{
		Timeout: 30 * time.Second,
		// Never a proxy of the environment: the API is on the loopback.
		Transport: &http.Transport{DialContext: (&net.Dialer{Timeout: 5 * time.Second}).DialContext},
	}}
}

// syncthingError is an answer of Syncthing's other than a success.
type syncthingError struct {
	status int
	msg    string
}

func (e *syncthingError) Error() string { return fmt.Sprintf("Syncthing: %d %s", e.status, e.msg) }

// call sends a request with the API key; out, if not nil, gets the JSON answer.
func (st *syncthing) call(ctx context.Context, method, path string, query url.Values, in, out any) error {
	var body io.Reader
	if in != nil {
		raw, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(raw)
	}
	u := st.base + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return err
	}
	req.Header.Set("X-API-Key", st.key)
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := st.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 500))
		return &syncthingError{resp.StatusCode, strings.TrimSpace(string(msg))}
	}
	if out != nil {
		return json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(out)
	}
	return nil
}

// safeOptions are the options the node sets at every start: Syncthing
// listens on the loopback only and reaches no one by itself.
func (st *syncthing) safeOptions() map[string]any {
	return map[string]any{
		"listenAddresses":       []string{"tcp://" + st.listen},
		"globalAnnounceEnabled": false,
		"localAnnounceEnabled":  false,
		"relaysEnabled":         false,
		"natEnabled":            false,
		"announceLANAddresses":  false,
		"urAccepted":            -1, // usage reports declined
		"crashReportingEnabled": false,
		"autoUpgradeIntervalH":  0,
		"startBrowser":          false,
	}
}

// secure sets the safe options.
func (st *syncthing) secure(ctx context.Context) error {
	return st.call(ctx, http.MethodPatch, "/rest/config/options", nil, st.safeOptions(), nil)
}

// deviceID is this Syncthing's device ID: the hash of its certificate, a
// public key that its peers pin.
func (st *syncthing) deviceID(ctx context.Context) (string, error) {
	var out struct {
		MyID string `json:"myID"`
	}
	if err := st.call(ctx, http.MethodGet, "/rest/system/status", nil, nil, &out); err != nil {
		return "", err
	}
	if !validDeviceID(out.MyID) {
		return "", fmt.Errorf("Syncthing gives no valid device ID (%q)", out.MyID)
	}
	return out.MyID, nil
}

// validDeviceID: Syncthing's form, eight groups of seven characters of
// base32 joined by dashes.
func validDeviceID(id string) bool {
	parts := strings.Split(id, "-")
	if len(parts) != 8 {
		return false
	}
	for _, p := range parts {
		if len(p) != 7 {
			return false
		}
		for _, r := range p {
			if !(r >= 'A' && r <= 'Z' || r >= '2' && r <= '7') {
				return false
			}
		}
	}
	return true
}

// stDevice is a peer as Syncthing's configuration holds it.
type stDevice struct {
	DeviceID          string   `json:"deviceID"`
	Name              string   `json:"name"`
	Addresses         []string `json:"addresses"`
	Compression       string   `json:"compression"`
	Introducer        bool     `json:"introducer"`
	AutoAcceptFolders bool     `json:"autoAcceptFolders"`
	Paused            bool     `json:"paused"`
}

// stFolderDevice is a device a folder is shared with.
type stFolderDevice struct {
	DeviceID string `json:"deviceID"`
}

// stVersioning keeps what a peer deleted or replaced, on this machine.
type stVersioning struct {
	Type             string            `json:"type"` // "" (none), staggered, trashcan
	Params           map[string]string `json:"params"`
	CleanupIntervalS int               `json:"cleanupIntervalS"`
	FSPath           string            `json:"fsPath"`
	FSType           string            `json:"fsType"`
}

// stFolder is a folder as Syncthing's configuration holds it.
type stFolder struct {
	ID                      string           `json:"id"`
	Label                   string           `json:"label"`
	Path                    string           `json:"path"`
	Type                    string           `json:"type"` // sendreceive, sendonly, receiveonly
	Devices                 []stFolderDevice `json:"devices"`
	RescanIntervalS         int              `json:"rescanIntervalS"`
	FSWatcherEnabled        bool             `json:"fsWatcherEnabled"`
	FSWatcherDelayS         float64          `json:"fsWatcherDelayS"`
	IgnorePerms             bool             `json:"ignorePerms"`
	Versioning              stVersioning     `json:"versioning"`
	CopyOwnershipFromParent bool             `json:"copyOwnershipFromParent"`
	MaxConflicts            int              `json:"maxConflicts"`
	Paused                  bool             `json:"paused"`
}

func (st *syncthing) devices(ctx context.Context) ([]stDevice, error) {
	var out []stDevice
	err := st.call(ctx, http.MethodGet, "/rest/config/devices", nil, nil, &out)
	return out, err
}

func (st *syncthing) putDevice(ctx context.Context, d stDevice) error {
	return st.call(ctx, http.MethodPut, "/rest/config/devices/"+url.PathEscape(d.DeviceID), nil, d, nil)
}

func (st *syncthing) deleteDevice(ctx context.Context, id string) error {
	return st.call(ctx, http.MethodDelete, "/rest/config/devices/"+url.PathEscape(id), nil, nil, nil)
}

func (st *syncthing) folders(ctx context.Context) ([]stFolder, error) {
	var out []stFolder
	err := st.call(ctx, http.MethodGet, "/rest/config/folders", nil, nil, &out)
	return out, err
}

func (st *syncthing) putFolder(ctx context.Context, f stFolder) error {
	return st.call(ctx, http.MethodPut, "/rest/config/folders/"+url.PathEscape(f.ID), nil, f, nil)
}

func (st *syncthing) deleteFolder(ctx context.Context, id string) error {
	return st.call(ctx, http.MethodDelete, "/rest/config/folders/"+url.PathEscape(id), nil, nil, nil)
}

// scan has Syncthing scan a folder now.
func (st *syncthing) scan(ctx context.Context, folder string) error {
	return st.call(ctx, http.MethodPost, "/rest/db/scan", url.Values{"folder": {folder}}, nil, nil)
}

// setIgnores replaces a folder's ignore patterns (its .stignore).
func (st *syncthing) setIgnores(ctx context.Context, folder string, patterns []string) error {
	if patterns == nil {
		patterns = []string{}
	}
	return st.call(ctx, http.MethodPost, "/rest/db/ignores", url.Values{"folder": {folder}}, map[string]any{"ignore": patterns}, nil)
}

// stFolderStatus is a folder's state on this machine.
type stFolderStatus struct {
	State       string `json:"state"` // idle, scanning, syncing, error...
	Error       string `json:"error"`
	GlobalBytes int64  `json:"globalBytes"`
	LocalBytes  int64  `json:"localBytes"`
	NeedBytes   int64  `json:"needBytes"`
	PullErrors  int    `json:"pullErrors"`
}

func (st *syncthing) folderStatus(ctx context.Context, folder string) (stFolderStatus, error) {
	var out stFolderStatus
	err := st.call(ctx, http.MethodGet, "/rest/db/status", url.Values{"folder": {folder}}, nil, &out)
	return out, err
}

// completion is how much of a folder a peer has, as this machine knows.
func (st *syncthing) completion(ctx context.Context, folder, device string) (float64, int64, error) {
	var out struct {
		Completion float64 `json:"completion"`
		NeedBytes  int64   `json:"needBytes"`
	}
	err := st.call(ctx, http.MethodGet, "/rest/db/completion", url.Values{"folder": {folder}, "device": {device}}, nil, &out)
	return out.Completion, out.NeedBytes, err
}

// stFileError is a file Syncthing could not read or write.
type stFileError struct {
	Path  string `json:"path"`
	Error string `json:"error"`
}

func (st *syncthing) folderErrors(ctx context.Context, folder string) ([]stFileError, error) {
	var out struct {
		Errors []stFileError `json:"errors"`
	}
	err := st.call(ctx, http.MethodGet, "/rest/folder/errors", url.Values{"folder": {folder}}, nil, &out)
	return out.Errors, err
}

// startSyncthing reads Syncthing's configuration in home, secures it and
// learns its device ID, retrying while it is not up yet (it starts beside
// the node, maybe after it). The listening address of the configuration
// is the one peers are handed to.
func (n *node) startSyncthing(home string) {
	if home == "" {
		return
	}
	for wait := 2 * time.Second; ; wait = min(wait*2, time.Minute) {
		api, key, listen, err := readSyncthingConfig(home)
		if err == nil {
			st := newSyncthing(api, key, listen)
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			err = st.secure(ctx)
			var id string
			if err == nil {
				id, err = st.deviceID(ctx)
			}
			cancel()
			if err == nil {
				n.mu.Lock()
				n.st, n.stID = st, id
				n.mu.Unlock()
				n.syncNet.setLocal(listen)
				log.Printf("Syncthing on this machine: device %s, listening on %s (its peers through eriphe-core)", id, listen)
				go n.watchSync(5 * time.Second) // syncfolders.go
				return
			}
		}
		log.Printf("Syncthing: %v; trying again in %s", err, wait)
		time.Sleep(wait)
	}
}

// syncthingInfo is what GET /v1/node says of Syncthing: its device ID,
// which eriphe-core hands to the peers it sets up copies with, and the
// state of each folder synced (syncfolders.go).
type syncthingInfo struct {
	DeviceID string            `json:"device_id"`
	Folders  []syncFolderState `json:"folders,omitempty"`
}

func (n *node) syncthingInfo(ctx context.Context) *syncthingInfo {
	n.mu.Lock()
	id, up := n.stID, n.st != nil
	n.mu.Unlock()
	if !up {
		return nil
	}
	return &syncthingInfo{DeviceID: id, Folders: n.syncState(ctx)}
}
