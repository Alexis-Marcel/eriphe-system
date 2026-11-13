package main

import (
	"errors"
	"log"
	"net/http"
)

// Settings → Folders (folders.go): the folders given to Eriphe, by machine,
// with the machine's default rights and each folder's own (Lire, Modifier:
// folder_values.go), whether it may be copied to the user's other machines
// (turned off from here, never on), and a button that stops sharing it;
// and, in a dialog, how to share one: on the machine that holds it, never
// from here.

func (w *web) folderRoutes(mux *webMux) {
	mux.Handle("GET /settings/folders", w.auth(w.handleFolders))
	mux.Handle("POST /settings/folders/delete", w.auth(w.handleDeleteFolder))
	mux.Handle("GET /settings/folders/grid", w.auth(w.handleFolderGrid))
	mux.Handle("POST /settings/folders/case", w.auth(w.handleFolderCase)) // folder_values.go
	mux.Handle("POST /settings/folders/nocopy", w.auth(w.handleNoCopies))
	mux.Handle("POST /settings/folders/copies", w.auth(w.handleFolderCopies))
}

// folderMachine is a machine and the folders shared on it.
type folderMachine struct {
	Name    string
	Online  bool
	System  string   // its share guide (shareGuide)
	NAS     bool     // Eriphe runs there in Docker
	Lima    bool     // a Mac with the Lima VM
	Default caseGrid // its default rights, inline
	Folders []folderTile
}

// folderTile is a folder of a machine and its grid (the summary in its
// tile, the drawer).
type folderTile struct {
	folderView
	Grid caseGrid
}

type foldersData struct {
	Machines []folderMachine
	Places   []folderView // the places of tools (places.go)
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
	var d foldersData
	for _, n := range nodes {
		if n.Suspended {
			continue // its access is cut (node_access.go)
		}
		info := parseInfo(n)
		m := folderMachine{Name: n.Name, Online: n.Online, Default: w.deviceCaseGrid(n.Name)}
		m.System, m.NAS, m.Lima = shareSystem(nodeInstall(n, info), n.OS)
		for _, v := range views {
			if v.Source == sourceMachine && v.Node == n.Name {
				m.Folders = append(m.Folders, folderTile{v, w.folderGrid(v.Folder)})
			}
		}
		d.Machines = append(d.Machines, m)
	}
	d.Guides = shareGuides(d.Machines)
	for _, v := range views {
		if v.Source == sourcePlace {
			d.Places = append(d.Places, v)
		}
	}
	w.render(rw, r, s, "settings", "", page{Title: "settings.folders", Nav: "settings", Tab: "folders", Data: d})
}

// handleDeleteFolder stops sharing a machine's folder (taken off its list
// there). A tool's place is set on its account's page, in its tree.
func (w *web) handleDeleteFolder(rw http.ResponseWriter, r *http.Request, s *session) {
	name := r.PostFormValue("name")
	if err := w.c.removeFolder(r.Context(), name); err != nil {
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

// handleNoCopies turns a folder's copies off: never on, which only its
// machine can do.
func (w *web) handleNoCopies(rw http.ResponseWriter, r *http.Request, s *session) {
	name := r.PostFormValue("name")
	if err := w.c.noCopies(r.Context(), name); err != nil {
		if errors.Is(err, errNoFolder) {
			w.toast(rw, tr(w.lang(r), "folders.err.gone"), false)
		} else {
			w.toast(rw, err.Error(), false)
		}
		return
	}
	log.Printf("web: folder %s: no copies", name)
	rw.Header().Set("HX-Refresh", "true")
	rw.WriteHeader(http.StatusOK)
}

// deviceCaseGrid is a machine's default for its folders, inline on the
// page: no "as …" column.
func (w *web) deviceCaseGrid(node string) caseGrid {
	g := caseGrid{Key: caseKey("device:" + node), Post: "/settings/folders/case", Hidden: map[string]string{"node": node},
		Hint: "folders.cat.%s.hint"}
	dev := w.c.deviceGrid(node)
	for _, cat := range folderCases {
		g.Rows = append(g.Rows, caseRow{Category: cat, Follows: dev.value(cat)})
	}
	return g
}

// folderGrid is a folder's grid: its machine's default, and its own cases;
// Modifier is greyed on a folder shared read-only.
func (w *web) folderGrid(f *Folder) caseGrid {
	g := caseGrid{Key: caseKey("folder:" + f.Name), Title: f.Name, Sub: f.Node, Post: "/settings/folders/case",
		Hidden: map[string]string{"name": f.Name}, Follow: "folders.followcol", Hint: "folders.cat.%s.hint", Greyed: "folders.readonly"}
	dev, own := w.c.deviceGrid(f.Node), w.c.folderOwn(f)
	for _, cat := range folderCases {
		g.Rows = append(g.Rows, caseRow{Category: cat, Own: own.raw(cat), Follows: dev.value(cat),
			Greyed: cat == catChange && f.Access != accessWrite})
	}
	return g.greyed()
}

func (w *web) handleFolderGrid(rw http.ResponseWriter, r *http.Request, s *session) {
	f, err := w.c.store.folder(r.URL.Query().Get("name"))
	if err != nil {
		http.NotFound(rw, r)
		return
	}
	w.render(rw, r, s, "settings", "case-drawer", page{Data: w.folderGrid(f)})
}

// handleFolderCase sets the cases a grid changed: a machine's default
// (node), or a folder's own (name, "" to follow its machine).
func (w *web) handleFolderCase(rw http.ResponseWriter, r *http.Request, s *session) {
	node, name := r.PostFormValue("node"), r.PostFormValue("name")
	var err error
	if node != "" {
		for _, c := range postedCases(r, folderCases, w.c.deviceGrid(node).value) {
			if err = w.c.setDeviceCase(node, c[0], c[1]); err != nil {
				break
			}
			log.Printf("web: folders %s: %s %s", node, c[0], c[1])
		}
	} else {
		var f *Folder
		if f, err = w.c.store.folder(name); err == nil {
			for _, c := range postedCases(r, folderCases, w.c.folderOwn(f).raw) {
				if err = w.c.setFolderCase(name, c[0], c[1]); err != nil {
					break
				}
				log.Printf("web: folders %s: %s %s", name, c[0], cmpOr(c[1], "follows its machine"))
			}
		}
	}
	if err != nil {
		if errors.Is(err, errNoFolder) {
			w.toast(rw, tr(w.lang(r), "folders.err.gone"), false)
		} else {
			w.toast(rw, err.Error(), false)
		}
		return
	}
	if node != "" {
		rw.Header().Set("HX-Refresh", "true") // its folders' "as the machine" follow
		rw.WriteHeader(http.StatusOK)
		return
	}
	f, _ := w.c.store.folder(name)
	g := w.folderGrid(f)
	g.Saved = true
	w.render(rw, r, s, "settings", "case-grid-frag", page{Data: g})
}
