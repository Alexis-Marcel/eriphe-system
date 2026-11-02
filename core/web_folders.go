package main

import (
	"log"
	"net/http"
	"strings"
)

// Settings → Folders (folders.go): the folders given to Eriphe, by machine,
// each with a button that stops sharing it, and how to share one: on the
// machine that holds it, never from here.

func (w *web) folderRoutes(mux *http.ServeMux) {
	mux.Handle("GET /settings/folders", w.auth(w.handleFolders))
	mux.Handle("POST /settings/folders/delete", w.auth(w.handleDeleteFolder))
}

// folderMachine is a machine and the folders shared on it.
type folderMachine struct {
	Name    string
	Online  bool
	How     string // the text key of how to share a folder there
	Folders []folderView
	Legacy  []legacyFolder // given from here before: to share again there
}

type foldersData struct {
	Machines []folderMachine
	Drive    []folderView // the Google Drive folders (cloud.go)
	Legacy   bool         // some folders are to share again on their machine
}

// shareHow is the text key of how to share a folder on a machine, by how it
// was installed (web_pages.go, systemKey).
func shareHow(install, goos string) string {
	switch systemKey(install, goos) {
	case "system.macos-container":
		return "folders.how.mac"
	case "system.linux":
		return "folders.how.linux"
	case "system.docker":
		return "folders.how.docker"
	case "system.windows":
		return "folders.how.windows"
	case "system.macos-lima":
		return "folders.how.lima"
	}
	return "folders.how.node"
}

func (w *web) handleFolders(rw http.ResponseWriter, r *http.Request, s *session) {
	views, err := w.c.folderViews()
	if err != nil {
		http.Error(rw, err.Error(), http.StatusInternalServerError)
		return
	}
	nodes, err := w.c.store.nodes()
	if err != nil {
		http.Error(rw, err.Error(), http.StatusInternalServerError)
		return
	}
	legacy, err := w.c.store.legacyFolders()
	if err != nil {
		http.Error(rw, err.Error(), http.StatusInternalServerError)
		return
	}
	var d foldersData
	for _, n := range nodes {
		if n.Suspended {
			continue // its access is cut (node_access.go)
		}
		info := parseInfo(n)
		m := folderMachine{Name: n.Name, Online: n.Online, How: shareHow(nodeInstall(n, info), n.OS)}
		for _, v := range views {
			if v.Source == sourceMachine && v.Node == n.Name {
				m.Folders = append(m.Folders, v)
			}
		}
		for _, l := range legacy {
			// Shared again there under the same name: nothing more to do.
			if l.Node == n.Name && !hasFolder(m.Folders, l.Name) {
				m.Legacy = append(m.Legacy, l)
				d.Legacy = true
			}
		}
		d.Machines = append(d.Machines, m)
	}
	for _, v := range views {
		if v.Source == sourceDrive {
			d.Drive = append(d.Drive, v)
		}
	}
	w.render(rw, r, s, "settings", "", page{Title: "settings.folders", Nav: "settings", Tab: "folders", Data: d})
}

func hasFolder(fs []folderView, name string) bool {
	for _, f := range fs {
		if strings.EqualFold(f.Name, name) || strings.EqualFold(strings.TrimSuffix(f.Name, " ("+f.Node+")"), name) {
			return true
		}
	}
	return false
}

// handleDeleteFolder stops sharing a folder: on its machine (taken off its
// list there), in Google Drive, or one given from here before.
func (w *web) handleDeleteFolder(rw http.ResponseWriter, r *http.Request, s *session) {
	name := r.PostFormValue("name")
	var err error
	switch r.PostFormValue("source") {
	case sourceDrive:
		// A Drive folder lives in the files server's configuration (cloud.go).
		if d := w.c.driveFolder(name); d != nil {
			err = w.c.removeDriveFolder(d.ID)
		} else {
			err = errNoFolder
		}
	case "legacy":
		err = w.c.store.deleteLegacyFolder(name)
	default:
		err = w.c.removeFolder(r.Context(), name)
	}
	if err != nil {
		w.toast(rw, tr(w.lang(r), "folders.err.gone"), false)
		return
	}
	log.Printf("web: folder %s no longer shared", name)
	rw.Header().Set("HX-Refresh", "true")
	rw.WriteHeader(http.StatusOK)
}
