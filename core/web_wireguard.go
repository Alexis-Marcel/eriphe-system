package main

import (
	"encoding/base64"
	"errors"
	"html/template"
	"log"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// Settings → Remote access → the WireGuard network (wireguard.go): turn it
// on, add a device (a QR code for the WireGuard app, once), trust the CA,
// see and revoke the devices. Its own part of the page, loaded on its own
// (hx-get), so that it stays apart from the Tailscale part above it.

func (w *web) wireguardRoutes(mux *http.ServeMux) {
	mux.Handle("GET /settings/remote/wireguard", w.auth(w.handleWG))
	mux.Handle("GET /settings/remote/wireguard/peers", w.auth(w.handleWGPeers))
	mux.Handle("POST /settings/remote/wireguard/enable", w.auth(w.handleWGEnable))
	mux.Handle("POST /settings/remote/wireguard/disable", w.auth(w.wgAction(func(a *wgAccess, _ *http.Request) error { return a.disable() })))
	mux.Handle("POST /settings/remote/wireguard/endpoint", w.auth(w.wgAction(func(a *wgAccess, r *http.Request) error {
		return a.setEndpoint(r.PostFormValue("endpoint"))
	})))
	mux.Handle("POST /settings/remote/wireguard/only", w.auth(w.wgAction(func(a *wgAccess, r *http.Request) error {
		return a.setWorkspaceOnly(r.PostFormValue("on") == "1", r)
	})))
	mux.Handle("POST /settings/remote/wireguard/peers", w.auth(w.handleWGAddPeer))
	mux.Handle("POST /settings/remote/wireguard/peers/{addr}/revoke", w.auth(w.wgAction(func(a *wgAccess, r *http.Request) error {
		return a.removePeer(r.PathValue("addr"))
	})))
	mux.Handle("GET /settings/remote/wireguard/eriphe.mobileconfig", w.auth(w.handleWGCA("mobileconfig")))
	mux.Handle("GET /settings/remote/wireguard/eriphe-ca.crt", w.auth(w.handleWGCA("crt")))
}

// wgPage is the data of the WireGuard part of the page.
type wgPage struct {
	wgView
	Guess string // where the devices could dial, from the address in the browser
	Msg   string // a message key, after an action
	Bad   bool
	New   *wgNew // the device just added, shown once
}

type wgNew struct {
	Peer   wgPeer
	QR     template.HTML
	Conf   template.URL // the .conf, as a data: link
	File   string
	CAQR   template.HTML // the CA page, for the phone once the tunnel is up
	CAPage string
}

func (w *web) wgPage(r *http.Request) wgPage {
	p := wgPage{wgView: w.c.wg.view()}
	if p.Endpoint == "" {
		p.Guess = endpointGuess(r.Host)
	}
	return p
}

// endpointGuess is the browser's host when it is a public one: on a VPS
// reached by its address or name, that is where the devices dial too. A
// private address (at home) is no guess: the devices need the router's
// public address.
func endpointGuess(host string) string {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	if host == "" || strings.EqualFold(host, "localhost") {
		return ""
	}
	if a, err := netip.ParseAddr(host); err == nil && (localAddr(a) || !a.IsGlobalUnicast()) {
		return ""
	}
	if !strings.Contains(host, ".") && !strings.Contains(host, ":") {
		return "" // a bare machine name, only known on the local network
	}
	if strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".lan") || strings.HasSuffix(host, ".home.arpa") || strings.HasSuffix(host, ".ts.net") {
		return ""
	}
	return host
}

func (w *web) renderWG(rw http.ResponseWriter, r *http.Request, s *session, p wgPage) {
	w.render(rw, r, s, "settings", "wg-section", page{Data: p})
}

func (w *web) handleWG(rw http.ResponseWriter, r *http.Request, s *session) {
	if w.c.wg == nil {
		rw.WriteHeader(http.StatusNoContent)
		return
	}
	w.renderWG(rw, r, s, w.wgPage(r))
}

func (w *web) handleWGPeers(rw http.ResponseWriter, r *http.Request, s *session) {
	if w.c.wg == nil {
		rw.WriteHeader(http.StatusNoContent)
		return
	}
	w.render(rw, r, s, "settings", "wg-peers-frag", page{Data: w.wgPage(r)})
}

func (w *web) handleWGEnable(rw http.ResponseWriter, r *http.Request, s *session) {
	if w.c.wg == nil {
		w.toast(rw, tr(w.lang(r), "remote.unavailable"), false)
		return
	}
	endpoint := strings.TrimSpace(r.PostFormValue("endpoint"))
	if endpoint == "" {
		endpoint = endpointGuess(r.Host)
	}
	err := w.c.wg.enable(endpoint)
	p := w.wgPage(r)
	if err != nil {
		log.Printf("web: wireguard: %v", err)
		p.Msg, p.Bad = "wg.err.start", true
	}
	w.renderWG(rw, r, s, p)
}

// wgAction runs an action, then answers with the whole part again.
func (w *web) wgAction(do func(*wgAccess, *http.Request) error) authed {
	return func(rw http.ResponseWriter, r *http.Request, s *session) {
		if w.c.wg == nil {
			w.toast(rw, tr(w.lang(r), "remote.unavailable"), false)
			return
		}
		err := do(w.c.wg, r)
		p := w.wgPage(r)
		switch {
		case err == nil:
		case errors.Is(err, errBadEndpoint):
			p.Msg, p.Bad = "wg.err.endpoint", true
		case errors.Is(err, errNotThroughWG):
			p.Msg, p.Bad = "wg.err.notwg", true
		default:
			log.Printf("web: wireguard: %v", err)
			p.Msg, p.Bad = "err.internal", true
		}
		w.renderWG(rw, r, s, p)
	}
}

func (w *web) handleWGAddPeer(rw http.ResponseWriter, r *http.Request, s *session) {
	if w.c.wg == nil {
		w.toast(rw, tr(w.lang(r), "remote.unavailable"), false)
		return
	}
	peer, conf, err := w.c.wg.addPeer(r.PostFormValue("name"))
	p := w.wgPage(r)
	switch {
	case errors.Is(err, errNoEndpoint):
		p.Msg, p.Bad = "wg.err.noendpoint", true
	case errors.Is(err, errFull):
		p.Msg, p.Bad = "wg.err.full", true
	case err != nil:
		log.Printf("web: wireguard: add: %v", err)
		p.Msg, p.Bad = "err.internal", true
	default:
		n := &wgNew{Peer: peer, CAPage: p.CAPage, File: "eriphe-" + strings.ReplaceAll(peer.Address, ".", "-") + ".conf",
			Conf: template.URL("data:application/octet-stream;base64," + base64.StdEncoding.EncodeToString([]byte(conf)))}
		if qr, err := qrSVG(conf, "WireGuard"); err == nil {
			n.QR = qr
		}
		if qr, err := qrSVG(p.CAPage, p.CAPage); err == nil {
			n.CAQR = qr
		}
		p.New = n
	}
	// The configuration holds the device's private key: never cached.
	rw.Header().Set("Cache-Control", "no-store")
	w.renderWG(rw, r, s, p)
}

func (w *web) handleWGCA(form string) authed {
	return func(rw http.ResponseWriter, r *http.Request, s *session) {
		if w.c.wg == nil {
			http.NotFound(rw, r)
			return
		}
		w.c.wg.serveCA(rw, form)
	}
}
