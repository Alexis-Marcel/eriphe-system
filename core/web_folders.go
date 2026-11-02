package main

import (
	"errors"
	"log"
	"net/http"
	"strings"
)

// Settings → Folders (folders.go): the folders given to Eriphe, by machine,
// each with its level (which may only be lowered from here), whether it may
// be copied to the user's other machines (turned off from here, never on),
// and a button that stops sharing it; and, in a dialog, how to share one:
// on the machine that holds it, never from here.

func (w *web) folderRoutes(mux *http.ServeMux) {
	mux.Handle("GET /settings/folders", w.auth(w.handleFolders))
	mux.Handle("POST /settings/folders/delete", w.auth(w.handleDeleteFolder))
	mux.Handle("POST /settings/folders/level", w.auth(w.handleLowerFolder))
	mux.Handle("POST /settings/folders/nocopy", w.auth(w.handleLowerFolder))
	mux.Handle("POST /settings/folders/copies", w.auth(w.handleFolderCopies))
}

// folderMachine is a machine and the folders shared on it.
type folderMachine struct {
	Name    string
	Online  bool
	System  string // its share guide (shareGuide)
	NAS     bool   // Eriphe runs there in Docker
	Lima    bool   // a Mac with the Lima VM
	Folders []folderView
	Legacy  []legacyFolder // given from here before: to share again there
}

type foldersData struct {
	Machines []folderMachine
	Drive    []folderView // the Google Drive folders (cloud.go)
	Legacy   bool         // some folders are to share again on their machine
	Guides   []shareGuide // how to share a folder, one guide per system
}

// shareGuide is how to share a folder on one system, and the user's devices
// that run it (open when there is one).
type shareGuide struct {
	System   string // mac, windows, linux
	Machines string // the user's devices of that system, comma-separated
	NAS      bool   // one of them runs Eriphe in Docker (a NAS)
	Lima     bool   // one of them is a Mac with the Lima VM, which sees no folder
}

// shareSystem is the guide for a machine, by how it was installed
// (web_pages.go, systemKey): the system, and whether it runs in Docker or
// on Lima.
func shareSystem(install, goos string) (system string, nas, lima bool) {
	switch systemKey(install, goos) {
	case "system.macos-container":
		return "mac", false, false
	case "system.macos-lima":
		return "mac", false, true
	case "system.windows":
		return "windows", false, false
	case "system.docker":
		return "linux", true, false
	}
	if goos == "darwin" {
		return "mac", false, false
	}
	if goos == "windows" {
		return "windows", false, false
	}
	return "linux", false, false
}

// shareGuides are the three guides, with the devices of each.
func shareGuides(ms []folderMachine) []shareGuide {
	gs := []shareGuide{{System: "mac"}, {System: "windows"}, {System: "linux"}}
	for _, m := range ms {
		for i := range gs {
			if gs[i].System != m.System {
				continue
			}
			if gs[i].Machines != "" {
				gs[i].Machines += ", "
			}
			gs[i].Machines += m.Name
			gs[i].NAS = gs[i].NAS || m.NAS
			gs[i].Lima = gs[i].Lima || m.Lima
		}
	}
	return gs
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
		m := folderMachine{Name: n.Name, Online: n.Online}
		m.System, m.NAS, m.Lima = shareSystem(nodeInstall(n, info), n.OS)
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
	d.Guides = shareGuides(d.Machines)
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

// handleFolderCopies sets the machines that keep a copy of a folder (the
// boxes ticked): copies added are made, those unticked deleted there.
func (w *web) handleFolderCopies(rw http.ResponseWriter, r *http.Request, s *session) {
	if err := r.ParseForm(); err != nil {
		http.Error(rw, err.Error(), http.StatusBadRequest)
		return
	}
	name := r.PostFormValue("name")
	if err := w.c.setCopies(r.Context(), name, r.PostForm["node"]); err != nil {
		var fe *folderError
		switch {
		case errors.Is(err, errNoFolder):
			w.toast(rw, tr(w.lang(r), "folders.err.gone"), false)
		case errors.As(err, &fe) && fe.Code == "not_copyable":
			w.toast(rw, tr(w.lang(r), "folders.err.notcopyable"), false)
		case errors.As(err, &fe) && fe.Code == "no_syncthing":
			w.toast(rw, tr(w.lang(r), "folders.err.nosyncthing"), false)
		default:
			w.toast(rw, err.Error(), false)
		}
		return
	}
	log.Printf("web: copies of folder %s on %v", name, r.PostForm["node"])
	rw.Header().Set("HX-Refresh", "true")
	rw.WriteHeader(http.StatusOK)
}

// handleLowerFolder lowers a folder's level (/level, with access) or turns
// its copies off (/nocopy): never higher, which only its machine can do.
func (w *web) handleLowerFolder(rw http.ResponseWriter, r *http.Request, s *session) {
	name := r.PostFormValue("name")
	access, nocopy := r.PostFormValue("access"), strings.HasSuffix(r.URL.Path, "/nocopy")
	if nocopy {
		access = ""
	}
	if err := w.c.lowerFolder(r.Context(), name, access, nocopy); err != nil {
		var fe *folderError
		switch {
		case errors.As(err, &fe) && fe.Code == "raise":
			w.toast(rw, tr(w.lang(r), "folders.err.raise"), false)
		case errors.Is(err, errNoFolder):
			w.toast(rw, tr(w.lang(r), "folders.err.gone"), false)
		default:
			w.toast(rw, err.Error(), false)
		}
		return
	}
	log.Printf("web: folder %s lowered (access %q, no copies %v)", name, access, nocopy)
	rw.Header().Set("HX-Refresh", "true")
	rw.WriteHeader(http.StatusOK)
}
