package main

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"slices"
	"strings"
	"time"
)

// Settings → Remote access (remote.go): turn it on, log in to Tailscale,
// enable HTTPS once, then the address, its QR code for the phone and how to
// install the app. The state follows tsnet live, over an event stream.

func (w *web) remoteRoutes(mux *http.ServeMux) {
	mux.Handle("GET /settings/remote", w.auth(w.handleRemote))
	mux.Handle("GET /settings/remote/events", w.auth(w.handleRemoteEvents))
	mux.Handle("POST /settings/remote/enable", w.auth(w.remoteAction(func(r *remoteAccess, _ *http.Request) error { return r.enable() })))
	mux.Handle("POST /settings/remote/disable", w.auth(w.remoteAction(func(r *remoteAccess, _ *http.Request) error { return r.disable() })))
	mux.Handle("POST /settings/remote/retry", w.auth(w.remoteAction(func(r *remoteAccess, _ *http.Request) error { r.retry(); return nil })))
	mux.Handle("POST /settings/remote/relogin", w.auth(w.remoteAction(func(r *remoteAccess, req *http.Request) error {
		ctx, cancel := context.WithTimeout(req.Context(), 10*time.Second)
		defer cancel()
		return r.relogin(ctx)
	})))
	mux.Handle("POST /settings/remote/advanced", w.auth(w.handleRemoteAdvanced))
	mux.Handle("POST /settings/remote/logout", w.auth(w.handleRemoteLogout))
}

// remotePage is the data of the remote access page and of its live part.
type remotePage struct {
	remoteView
	QR       template.HTML
	HasVault bool
}

func (w *web) remotePage() remotePage {
	p := remotePage{HasVault: currentVault() != nil}
	if w.c.remote == nil {
		return p
	}
	p.remoteView = w.c.remote.current()
	if p.Phase == phaseReady {
		if qr, err := qrSVG(p.URL(), p.URL()); err == nil {
			p.QR = qr
		}
	}
	return p
}

func (w *web) handleRemote(rw http.ResponseWriter, r *http.Request, s *session) {
	w.render(rw, r, s, "settings", "", page{Title: "settings.remote", Nav: "settings", Tab: "remote", Data: w.remotePage()})
}

// handleRemoteEvents sends the live part of the page whenever it changes.
func (w *web) handleRemoteEvents(rw http.ResponseWriter, r *http.Request, s *session) {
	flusher, ok := rw.(http.Flusher)
	if !ok || w.c.remote == nil {
		http.Error(rw, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	lang := w.lang(r)
	h := rw.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no")
	rw.WriteHeader(http.StatusOK)
	fmt.Fprint(rw, "retry: 3000\n\n")
	flusher.Flush()
	last := ""
	quiet := time.Now()
	for {
		wake := w.c.remote.waker()
		html, err := w.fragment(lang, "settings", "remote-status", page{Lang: lang, Data: w.remotePage()})
		if err != nil {
			log.Printf("web: remote-status: %v", err)
			return
		}
		if html != last {
			writeEvent(rw, "remote", 0, html)
			flusher.Flush()
			last, quiet = html, time.Now()
		} else if time.Since(quiet) > 25*time.Second {
			fmt.Fprint(rw, ": keep-alive\n\n")
			flusher.Flush()
			quiet = time.Now()
		}
		select {
		case <-r.Context().Done():
			return
		case <-wake:
		case <-time.After(30 * time.Second): // the expiry warning comes with time alone
		}
	}
}

// remoteAction runs an action, then answers with the live part of the page.
func (w *web) remoteAction(do func(*remoteAccess, *http.Request) error) authed {
	return func(rw http.ResponseWriter, r *http.Request, s *session) {
		if w.c.remote == nil {
			w.toast(rw, tr(w.lang(r), "remote.unavailable"), false)
			return
		}
		if err := do(w.c.remote, r); err != nil {
			log.Printf("web: remote: %v", err)
			w.toast(rw, tr(w.lang(r), "err.internal"), false)
			return
		}
		w.render(rw, r, s, "settings", "remote-status", page{Data: w.remotePage()})
	}
}

// handleRemoteAdvanced saves the machine name, the coordination server and
// the auth key (in the vault).
func (w *web) handleRemoteAdvanced(rw http.ResponseWriter, r *http.Request, s *session) {
	msg := func(key string, ok bool) {
		w.render(rw, r, s, "settings", "remote-advanced-result", page{Data: map[string]any{"Msg": key, "OK": ok}})
	}
	if w.c.remote == nil {
		msg("remote.unavailable", false)
		return
	}
	key := strings.TrimSpace(r.PostFormValue("authkey"))
	clearKey := r.PostFormValue("clear_authkey") != ""
	// Tailscale's keys start with tskey-; Headscale's do not.
	if key != "" && !strings.HasPrefix(key, "tskey-") && strings.TrimSpace(r.PostFormValue("control_url")) == "" {
		msg("remote.badkey", false)
		return
	}
	if key != "" || clearKey {
		v := currentVault()
		if v == nil {
			msg("remote.novault", false)
			return
		}
		var err error
		if clearKey {
			if err = v.remove(remoteAuthKeyName); errors.Is(err, errNoSecret) {
				err = nil
			}
		} else {
			err = v.set(remoteAuthKeyName, key)
		}
		if err != nil {
			log.Printf("web: remote: auth key: %v", err)
			msg("err.internal", false)
			return
		}
	}
	switch err := w.c.remote.configure(r.PostFormValue("hostname"), r.PostFormValue("control_url")); {
	case errors.Is(err, errBadHostname):
		msg("remote.badhost", false)
		return
	case errors.Is(err, errBadControl):
		msg("remote.badcontrol", false)
		return
	case err != nil:
		log.Printf("web: remote: %v", err)
		msg("err.internal", false)
		return
	}
	rw.Header().Set("HX-Refresh", "true")
	msg("remote.saved", true)
}

func (w *web) handleRemoteLogout(rw http.ResponseWriter, r *http.Request, s *session) {
	if w.c.remote == nil {
		w.toast(rw, tr(w.lang(r), "remote.unavailable"), false)
		return
	}
	if err := w.c.remote.logout(r.Context()); err != nil {
		log.Printf("web: remote: logout: %v", err)
	}
	rw.Header().Set("HX-Refresh", "true")
	rw.WriteHeader(http.StatusOK)
}

// remoteCoreURL is eriphe-core's API on the tailnet, for the join command,
// when remote access is ready.
func (w *web) remoteCoreURL() string {
	if v, ok := w.c.remote.ready(); ok {
		return v.APIURL()
	}
	return ""
}

// awayCoreURLs are eriphe-core's addresses for a device away from home:
// the second address of the settings (web_address.go), then the tailnet's
// when remote access is ready.
func (w *web) awayCoreURLs() []string {
	var out []string
	for _, u := range []string{w.deviceFallback(), w.remoteCoreURL()} {
		if u != "" && !slices.Contains(out, u) {
			out = append(out, u)
		}
	}
	return out
}

// joinCoreURL is the -core of a join command: the local address, the
// ones away from home, or all (the node tries them in order: at home, then
// away). Every installer takes such a list, comma-separated.
func (w *web) joinCoreURL(r *http.Request, via string) string {
	lan := w.coreURL(r)
	away := slices.DeleteFunc(w.awayCoreURLs(), func(u string) bool { return u == lan })
	switch {
	case len(away) == 0 || via == "lan":
		return lan
	case via == "remote":
		return strings.Join(away, ",")
	}
	return lan + "," + strings.Join(away, ",")
}
