package main

import (
	"errors"
	"log"
	"net/http"
	"path"
	"strings"
)

// Settings → Folders (folders.go): the folders given to Eriphe, and "Share
// a folder": pick a machine, browse its shared roots from any device (the
// node lists directories only, through its tunnel), name the folder and
// choose its right.

func (w *web) folderRoutes(mux *http.ServeMux) {
	mux.Handle("GET /settings/folders", w.auth(w.handleFolders))
	mux.Handle("GET /settings/folders/browse", w.auth(w.handleBrowse))
	mux.Handle("POST /settings/folders", w.auth(w.handleShareFolder))
	mux.Handle("POST /settings/folders/delete", w.auth(w.handleDeleteFolder))
}

// folderMachine is a machine in the share form: online, and its roots.
type folderMachine struct {
	Name   string
	Online bool
	Shares []nodeShare
}

type foldersData struct {
	Folders  []folderView
	Machines []folderMachine // those that share a root
	Others   []string        // online machines that share none
}

func (w *web) handleFolders(rw http.ResponseWriter, r *http.Request, s *session) {
	fs, err := w.c.folderViews()
	if err != nil {
		http.Error(rw, err.Error(), http.StatusInternalServerError)
		return
	}
	nodes, err := w.c.store.nodes()
	if err != nil {
		http.Error(rw, err.Error(), http.StatusInternalServerError)
		return
	}
	d := foldersData{Folders: fs}
	for _, n := range nodes {
		if shares := parseInfo(n).Shares; len(shares) > 0 {
			d.Machines = append(d.Machines, folderMachine{Name: n.Name, Online: n.Online, Shares: shares})
		} else if n.Online {
			d.Others = append(d.Others, n.Name)
		}
	}
	w.render(rw, r, s, "settings", "", page{Title: "settings.folders", Nav: "settings", Tab: "folders", Data: d})
}

// crumb is one step of the explorer's path.
type crumb struct {
	Label string
	Path  string
}

// explorerView is the explorer's fragment: a machine's roots, or a folder
// of one with its directories and the form that shares it.
type explorerView struct {
	Node    string
	Shares  []nodeShare // with no Share chosen yet
	Share   string
	Listing *dirListing
	Crumbs  []crumb
	Parent  *crumb // the folder above, if any
	Name    string // the name proposed for the folder
	Error   string // a text key
}

func (w *web) handleBrowse(rw http.ResponseWriter, r *http.Request, s *session) {
	q := r.URL.Query()
	v := explorerView{Node: q.Get("node"), Share: q.Get("share")}
	all, err := w.c.machineShares()
	if err != nil {
		http.Error(rw, err.Error(), http.StatusInternalServerError)
		return
	}
	shares, ok := all[v.Node]
	switch {
	case v.Node == "":
		// No machine chosen: nothing to show.
	case !ok:
		v.Error = "folders.err.noshares"
	case v.Share == "":
		v.Shares = shares
	default:
		l, err := w.c.browse(r.Context(), v.Node, v.Share, q.Get("path"), false)
		if err != nil {
			v.Error = folderErrorKey(err)
			break
		}
		v.Listing = l
		v.Crumbs = []crumb{{Label: v.Share}}
		if l.Path != "" {
			parts := strings.Split(l.Path, "/")
			for i, p := range parts {
				v.Crumbs = append(v.Crumbs, crumb{Label: p, Path: strings.Join(parts[:i+1], "/")})
			}
			v.Parent = &v.Crumbs[len(v.Crumbs)-2]
		}
		v.Name = path.Base("/" + l.Path)
		if l.Path == "" {
			v.Name = v.Share
		}
	}
	w.render(rw, r, s, "settings", "folder-explorer", page{CSRF: s.CSRF, Data: v})
}

// folderErrorKey is the text of a refusal.
func folderErrorKey(err error) string {
	var fe *folderError
	if errors.As(err, &fe) {
		switch fe.Code {
		case "offline", "unknown_share", "invalid_path", "not_found", "outside", "sensitive", "eriphe_data",
			"unavailable", "unreadable", "holds_sensitive", "old_node", "bad_name", "bad_access", "exists", "read_only_share":
			return "folders.err." + fe.Code
		}
	}
	log.Printf("web: folders: %v", err)
	return "err.internal"
}

func (w *web) handleShareFolder(rw http.ResponseWriter, r *http.Request, s *session) {
	f := Folder{Name: r.PostFormValue("name"), Node: r.PostFormValue("node"), Share: r.PostFormValue("share"),
		Path: r.PostFormValue("path"), Access: r.PostFormValue("access")}
	if _, err := w.c.addFolder(r.Context(), f); err != nil {
		w.render(rw, r, s, "settings", "folder-share-error", page{Data: folderErrorKey(err)})
		return
	}
	log.Printf("web: folder %s shared from %s (%s)", f.Name, f.Node, f.Access)
	rw.Header().Set("HX-Refresh", "true")
	rw.WriteHeader(http.StatusOK)
}

func (w *web) handleDeleteFolder(rw http.ResponseWriter, r *http.Request, s *session) {
	name := r.PostFormValue("name")
	var err error
	if r.PostFormValue("source") == sourceDrive {
		// A Drive folder lives in the files server's configuration (cloud.go).
		if d := w.c.driveFolder(name); d != nil {
			err = w.c.removeDriveFolder(d.ID)
		} else {
			err = errNoFolder
		}
	} else {
		err = w.c.store.deleteFolder(name)
	}
	if err != nil {
		w.toast(rw, tr(w.lang(r), "folders.err.gone"), false)
		return
	}
	log.Printf("web: folder %s no longer shared", name)
	rw.Header().Set("HX-Refresh", "true")
	rw.WriteHeader(http.StatusOK)
}
