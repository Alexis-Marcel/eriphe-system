package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"slices"
	"time"
)

// Settings → Tools → Google Drive: the user's own Google app first (a
// guide through the Google Cloud console, then its client id and secret,
// kept in the vault), then the account's consent, then its folders,
// browsed through eriphe-files, each given read only or read and add
// results to its Eriphe/ subfolder (cloud.go).

type driveView struct {
	driveState
	Origin    string // this workspace, as the browser reaches it
	Callback  string // the redirect URI to register in the Google app
	OriginWhy string // why Google refuses that redirect (googleOrigin), or ""
	Remote    string // the tailnet address, when remote access gives one Google takes
	ClientID  string // the saved Google app's client id, or ""
	Status    string // the files server's: ready, connecting…
	Connected bool   // just back from Google
	Saved     string // just saved the Google app: "ok", or the redirect URI its file lacks
	Error     string
}

func (w *web) driveRoutes(mux *http.ServeMux) {
	mux.Handle("GET /settings/drive", w.auth(w.handleDrive))
	mux.Handle("POST /settings/drive/client", w.auth(w.handleDriveClient))
	mux.Handle("POST /settings/drive/connect", w.auth(w.handleDriveConnect))
	mux.Handle("POST /settings/drive/test", w.auth(w.handleDriveTest)) // web_drive_check.go
	mux.Handle("GET /settings/drive/browse", w.auth(w.handleDriveBrowse))
	mux.Handle("POST /settings/drive/folders", w.auth(w.handleDriveAdd))
	mux.Handle("POST /settings/drive/folders/remove", w.auth(w.handleDriveRemove))
}

func (w *web) driveView(r *http.Request) driveView {
	v := driveView{driveState: w.c.driveState(), Origin: w.origin(r), Connected: r.URL.Query().Get("connected") != ""}
	v.Callback = v.Origin + "/oauth/callback"
	v.OriginWhy = googleOrigin(v.Origin)
	if v.OriginWhy != "" && w.c.remote != nil {
		if u := w.c.remote.current().URL(); u != "" && googleOrigin(u) == "" {
			v.Remote = u
		}
	}
	if app, err := googleClient(); err == nil {
		v.ClientID = app.ID
	}
	if u := w.c.pool.get(filesServer); u != nil {
		v.Status, _, _ = u.snapshot()
	}
	return v
}

func (w *web) handleDrive(rw http.ResponseWriter, r *http.Request, s *session) {
	w.render(rw, r, s, "settings", "", page{Title: "drive.title", Nav: "settings", Tab: "tools", View: "drive", Data: w.driveView(r)})
}

// handleDriveClient keeps the user's Google app: the JSON the console
// downloads, or its client id and secret typed. The secret is never shown
// back nor logged.
func (w *web) handleDriveClient(rw http.ResponseWriter, r *http.Request, s *session) {
	app, redirects, err := readGoogleClient(rw, r) // web_google.go
	if err == nil {
		err = storeGoogleClient(app)
	}
	v := w.driveView(r)
	if err != nil {
		v.Error = w.driveError(r, err)
	} else {
		log.Printf("web: Google Drive: Google app saved")
		v.Saved = "ok"
		if len(redirects) > 0 && !slices.Contains(redirects, v.Callback) {
			v.Saved = v.Callback
		}
	}
	w.render(rw, r, s, "settings", "drive-wrap-frag", page{Data: v})
}

// handleDriveConnect connects a Google account for Drive: the Google flow
// (web_google.go) with Drive alone, back to this page.
func (w *web) handleDriveConnect(rw http.ResponseWriter, r *http.Request, s *session) {
	in := googleConnect{Services: []string{googleDrive}, Write: r.PostFormValue("write") != "",
		Hint: r.PostFormValue("account"), Back: "/settings/drive"}
	w.startGoogleFlow(rw, r, s, in, "drive-connect-result", func(err error) any {
		v := w.driveView(r)
		v.Error = w.driveError(r, err)
		return v
	})
}

// driveError words a refusal: one of ours by its key, else as it is.
func (w *web) driveError(r *http.Request, err error) string {
	var ce *catalogError
	if errors.As(err, &ce) && ce.Key != "" {
		return w.formError(r, err)
	}
	return err.Error()
}

type driveBrowse struct {
	Account      string   // the account browsed
	Accounts     []string // the accounts, when there are several
	Parent, Name string
	Folders      []driveItem
	Next         string
	CanWrite     bool
	Given        map[string]string // folder id → its access, for the ones already given
	Error        string
}

func (w *web) handleDriveBrowse(rw http.ResponseWriter, r *http.Request, s *session) {
	q := r.URL.Query()
	v := driveBrowse{Parent: q.Get("parent"), Name: q.Get("name"), Given: map[string]string{}}
	st := w.c.driveState()
	v.Account, _ = st.driveAccountName(q.Get("account"))
	if v.Account == "" && len(st.Accounts) > 0 {
		v.Account = st.Accounts[0].Name
	}
	if len(st.Accounts) > 1 {
		for _, a := range st.Accounts {
			v.Accounts = append(v.Accounts, a.Name)
		}
	}
	v.CanWrite = st.canWrite(v.Account)
	for _, f := range st.Folders {
		v.Given[f.ID] = f.Access
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	var err error
	if v.Folders, v.Next, err = w.c.browseDrive(ctx, v.Account, v.Parent, q.Get("page")); err != nil {
		v.Error = w.driveError(r, err)
	}
	w.render(rw, r, s, "settings", "drive-browse", page{Data: v})
}

func (w *web) handleDriveAdd(rw http.ResponseWriter, r *http.Request, s *session) {
	err := w.c.addDriveFolder(r.PostFormValue("account"), r.PostFormValue("id"), r.PostFormValue("name"), r.PostFormValue("access"))
	w.driveFolders(rw, r, s, err)
}

func (w *web) handleDriveRemove(rw http.ResponseWriter, r *http.Request, s *session) {
	w.driveFolders(rw, r, s, w.c.removeDriveFolder(r.PostFormValue("id")))
}

// driveCount says how many Folders a Drive gives.
func driveCount(lang string, n int) string {
	switch n {
	case 0:
		return tr(lang, "drive.count0")
	case 1:
		return tr(lang, "drive.count1")
	}
	return fmt.Sprintf(tr(lang, "drive.count"), n)
}

// driveFolders answers a change with the list of Folders again.
func (w *web) driveFolders(rw http.ResponseWriter, r *http.Request, s *session, err error) {
	v := w.driveView(r)
	if err != nil {
		v.Error = w.driveError(r, err)
	} else {
		w.notify()
	}
	w.render(rw, r, s, "settings", "drive-folders-frag", page{Data: v})
}
