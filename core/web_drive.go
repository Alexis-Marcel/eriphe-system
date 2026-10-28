package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"
)

// Settings → Tools → Google Drive: connect the account (the Eriphe app
// through eriphe.com, or the user's own Google client), then browse its
// folders through eriphe-files and pick which become Folders, read only or
// read and add results to their Eriphe/ subfolder (cloud.go).

type driveView struct {
	driveState
	Origin    string // this workspace, as the browser reaches it
	AppReady  bool   // the Eriphe app's client is installed
	OriginOK  bool   // eriphe.com bounces to this address
	Callback  string // the redirect to register for an own client
	Status    string // the files server's: ready, connecting…
	Connected bool   // just back from Google
	Error     string
}

func (w *web) driveRoutes(mux *http.ServeMux) {
	mux.Handle("GET /settings/drive", w.auth(w.handleDrive))
	mux.Handle("POST /settings/drive/connect", w.auth(w.handleDriveConnect))
	mux.Handle("GET /settings/drive/browse", w.auth(w.handleDriveBrowse))
	mux.Handle("POST /settings/drive/folders", w.auth(w.handleDriveAdd))
	mux.Handle("POST /settings/drive/folders/remove", w.auth(w.handleDriveRemove))
}

// origin is the workspace's own address, as the browser reaches it.
func (w *web) origin(r *http.Request) string {
	return "https://" + strings.TrimSuffix(strings.ToLower(r.Host), ":443")
}

func (w *web) driveView(r *http.Request) driveView {
	_, appErr := w.c.googleApp()
	v := driveView{driveState: w.c.driveState(), Origin: w.origin(r), AppReady: appErr == nil,
		Callback: w.origin(r) + "/oauth/callback", Connected: r.URL.Query().Get("connected") != ""}
	v.OriginOK = bounceOrigin(v.Origin)
	if u := w.c.pool.get(filesServer); u != nil {
		v.Status, _, _ = u.snapshot()
	}
	return v
}

func (w *web) handleDrive(rw http.ResponseWriter, r *http.Request, s *session) {
	w.render(rw, r, s, "settings", "", page{Title: "drive.title", Nav: "settings", Tab: "tools", View: "drive", Data: w.driveView(r)})
}

func (w *web) handleDriveConnect(rw http.ResponseWriter, r *http.Request, s *session) {
	in := driveConnect{Origin: w.origin(r), Write: r.PostFormValue("write") != "", Lang: w.lang(r)}
	if r.PostFormValue("own") != "" {
		in.Own = &oauthApp{ID: strings.TrimSpace(r.PostFormValue("client_id")), Secret: strings.TrimSpace(r.PostFormValue("client_secret"))}
	}
	in.Binding = w.oauthBinding(rw, r)
	authURL, err := w.c.startDrive(in)
	if err != nil {
		v := w.driveView(r)
		v.Error = w.driveError(r, err)
		w.render(rw, r, s, "settings", "drive-connect-result", page{Data: v})
		return
	}
	rw.Header().Set("HX-Redirect", authURL)
	rw.WriteHeader(http.StatusOK)
}

// handleDriveCallback is the end of a Google authorization, at the
// workspace's /oauth/callback (from eriphe.com's bounce, or from Google
// for an own client).
func (w *web) handleDriveCallback(rw http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	binding := ""
	if c, err := r.Cookie(oauthCookie); err == nil {
		binding = hashToken(c.Value)
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
	defer cancel()
	err := w.c.finishDrive(ctx, w.origin(r), q.Get("state"), q.Get("code"), q.Get("error"), binding)
	v := oauthDone{Name: "Google Drive", Back: "/settings/drive"}
	if err != nil {
		if errors.Is(err, errBadState) {
			rw.WriteHeader(http.StatusBadRequest)
			err = errors.New(tr(w.lang(r), "oauth.expired"))
		}
		v.Error = w.driveError(r, err)
		log.Printf("web: Google Drive: authorization failed")
	} else {
		// A page of this site, then the settings: the session cookie
		// (SameSite=Strict) is not sent on the redirect from Google.
		v.Next = "/settings/drive?connected=1"
		log.Printf("web: Google Drive: connected")
	}
	w.notify()
	w.render(rw, r, nil, "oauth", "", page{Title: "oauth.title", Data: v})
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
	v.CanWrite = st.CanWrite
	for _, f := range st.Folders {
		v.Given[f.ID] = f.Access
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	var err error
	if v.Folders, v.Next, err = w.c.browseDrive(ctx, v.Parent, q.Get("page")); err != nil {
		v.Error = w.driveError(r, err)
	}
	w.render(rw, r, s, "settings", "drive-browse", page{Data: v})
}

func (w *web) handleDriveAdd(rw http.ResponseWriter, r *http.Request, s *session) {
	err := w.c.addDriveFolder(r.PostFormValue("id"), r.PostFormValue("name"), r.PostFormValue("access"))
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
