package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"
)

// The device's local socket, on a Mac: <root>/local.sock, the device's user's
// only (in its root, mode 0600). eriphe-device share, run by the user in a
// terminal or from the Finder, asks the running device through it to open a
// folder it has just shared: macOS then asks the user, while they are
// there, whether eriphe-device may open it; a folder that may be copied is
// handed to Syncthing and scanned too, so that macOS asks for Syncthing at
// the same moment (access.go, syncshare.go). Nothing else goes
// through it: the device opens only a folder of its list, hands only such
// a folder to Syncthing (one that may be copied), and says only whether
// each could open it.

const localSocket = "local.sock"

// privacyFilesURL opens System Settings › Privacy & Security › Files and
// Folders, where the user allows a program a folder it was refused.
const privacyFilesURL = "x-apple.systempreferences:com.apple.preference.security?Privacy_FilesAndFolders"

// openPrivacy opens that pane on this Mac (a variable for the tests).
var openPrivacy = func() error { return exec.Command("open", privacyFilesURL).Run() }

// localAskTimeout is how long eriphe-device share waits for the device: macOS
// holds the device's first open of a protected folder until the user answers.
const localAskTimeout = 3 * time.Minute

// listenLocal serves the local socket.
func (n *device) listenLocal() error {
	p := filepath.Join(n.opt.root, localSocket)
	os.Remove(p) // a socket left by the device before
	l, err := net.Listen("unix", p)
	if err != nil {
		return err
	}
	if err := os.Chmod(p, 0o600); err != nil {
		l.Close()
		return err
	}
	go http.Serve(l, n.localRoutes())
	return nil
}

func (n *device) localRoutes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/local/access/{id}", func(w http.ResponseWriter, r *http.Request) {
		list, err := loadShared(n.opt.root)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		i := indexShared(list, r.PathValue("id"))
		if i < 0 {
			http.Error(w, "no such shared folder", http.StatusNotFound)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), localAskTimeout)
		defer cancel()
		reply := localReply{Device: n.probeShared(list[i])}
		reply.Syncthing = n.registerShared(ctx, list[i]) // syncshare.go
		writeJSON(w, http.StatusOK, reply)
	})
	return mux
}

// localReply is what the device saw of a folder just shared, and what
// Syncthing saw of it when it was handed to it (nil: it was not).
type localReply struct {
	Device    accessNote  `json:"device"`
	Syncthing *accessNote `json:"syncthing,omitempty"`
}

// errNoLocalDevice: no device answers on the socket (it is not running).
var errNoLocalDevice = errors.New("eriphe-device is not running on this machine")

// askLocalAccess asks the running device to open a shared folder, and to hand
// it to Syncthing if it may be copied, and what each saw.
func askLocalAccess(root, id string, timeout time.Duration) (localReply, error) {
	sock := filepath.Join(root, localSocket)
	c := &http.Client{Timeout: timeout, Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", sock)
		},
	}}
	resp, err := c.Post("http://device/v1/local/access/"+id, "application/json", nil)
	if err != nil {
		var op *net.OpError
		if errors.As(err, &op) && op.Op == "dial" {
			return localReply{}, errNoLocalDevice
		}
		return localReply{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return localReply{}, fmt.Errorf("eriphe-device: %s", resp.Status)
	}
	var reply localReply
	err = json.NewDecoder(resp.Body).Decode(&reply)
	return reply, err
}

// handlePrivacy opens, on this Mac, the pane where the user allows the
// device a folder (POST /v1/privacy/open, through the tunnel: the
// workspace's "Allow" button). Nothing else can be opened; elsewhere it is
// not found.
func (n *device) handlePrivacy(w http.ResponseWriter, r *http.Request) {
	if runtime.GOOS != "darwin" {
		http.Error(w, "not a Mac", http.StatusNotFound)
		return
	}
	if err := openPrivacy(); err != nil {
		http.Error(w, "could not open System Settings: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
