package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"
)

// A folder its machine cannot use (node/access.go): macOS refused it to
// eriphe-node or to Syncthing, its permissions, a read-only volume, an
// unplugged disk. The node reports it (Folder.Problem and Program,
// syncState.Problem and Program); Settings → Folders and the device's card
// say so, naming on a Mac the program to turn on, and offer "Allow", which
// asks that Mac's node to open System Settings › Privacy & Security › Files and
// Folders there (POST /v1/privacy/open): nothing else can be opened, and
// the user decides there.

var (
	errNotMac  = errors.New("not a Mac")
	errNodeOld = errors.New("its eriphe-node is too old to open its Settings")
)

// openPrivacy asks a Mac's node to open the pane where its user allows it a
// folder.
func (c *core) openPrivacy(ctx context.Context, name string) error {
	rows, err := c.store.nodes()
	if err != nil {
		return err
	}
	found := false
	for _, n := range rows {
		if n.Name == name {
			found = true
			if n.OS != "darwin" {
				return errNotMac
			}
		}
	}
	if !found {
		return errNoNode
	}
	n := c.node(name)
	if !n.connected() {
		return errOffline
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := n.call(ctx, http.MethodPost, "/v1/privacy/open", nil, nil); err != nil {
		var ne *nodeError
		if errors.As(err, &ne) && ne.status == http.StatusNotFound {
			return errNodeOld
		}
		return err
	}
	return nil
}

// handlePrivacy is the "Allow" button of a folder macOS keeps from its node.
func (w *web) handlePrivacy(rw http.ResponseWriter, r *http.Request, s *session) {
	name := r.PathValue("name")
	lang := w.lang(r)
	program := macProgram(r.FormValue("program"))
	err := w.c.openPrivacy(r.Context(), name)
	switch {
	case err == nil:
		log.Printf("web: System Settings opened on %s, for a folder", name)
		w.toast(rw, fmt.Sprintf(tr(lang, "folders.allowopened"), name, program), true)
	case errors.Is(err, errNodeOld):
		w.toast(rw, fmt.Sprintf(tr(lang, "folders.allowold"), name), false)
	case errors.Is(err, errOffline):
		w.toast(rw, fmt.Sprintf(tr(lang, "folders.allowoffline"), name, program), false)
	case errors.Is(err, errNoNode), errors.Is(err, errNotMac):
		w.toast(rw, tr(lang, "machines.nonode"), false)
	default:
		w.toast(rw, err.Error(), false)
	}
}

// macProgram is the program to turn on in Files and Folders: syncthing
// when it is the one refused, else eriphe-node (also for an older node,
// which does not say).
func macProgram(p string) string {
	if p == "syncthing" {
		return p
	}
	return "eriphe-node"
}
