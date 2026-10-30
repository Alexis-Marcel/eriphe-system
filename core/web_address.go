package main

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
)

// The workspace's address, and the proxies in front of it (Settings → My
// space → Workspace address; docs/reverse-proxy.md).
//
// The address is where the owner opens the workspace, e.g.
// https://eriphe.example.org behind their own reverse proxy. It is one
// value: the sign-in's external_url (web.auth.external_url, or the newer
// web.external_url of the configuration file, which wins; else the
// workspace's own). The provider's sign-in comes back to it, the OAuth
// callbacks of the tools and of Google Drive are made on it, and forms sent
// from it are the workspace's own. Without it: the request's own origin, as
// before; the page shows the tailnet's address when remote access is on.
//
// A flow bound to a cookie of the browser (a sign-in, an authorization)
// must start where it comes back: from another origin, the workspace says
// to open it at its address.
//
// The trusted proxies are one list, the sign-in's proxy.trusted: from them
// only, X-Forwarded-For, X-Forwarded-Proto and X-Forwarded-Host are
// believed, for the client's address (the login limiter) and the
// request's origin; and the proxy sign-in's header too. From anywhere else
// they are ignored.

const deviceFallbackKey = "device_fallback_url" // kv: a second address of eriphe-core for the devices

// normOrigin is scheme://host in lower case, without the scheme's default port.
func normOrigin(scheme, host string) string {
	scheme, host = strings.ToLower(scheme), strings.ToLower(host)
	switch {
	case scheme == "https":
		host = strings.TrimSuffix(host, ":443")
	case scheme == "http":
		host = strings.TrimSuffix(host, ":80")
	}
	return scheme + "://" + host
}

// originOf is an address's normalized origin, or "".
func originOf(s string) string {
	u, err := url.Parse(s)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	return normOrigin(u.Scheme, u.Host)
}

// forwardedHost is what X-Forwarded-Host may hold: a name or an address, and a port.
var forwardedHost = regexp.MustCompile(`^(\[[0-9a-fA-F:.]+\]|[A-Za-z0-9.-]+)(:[0-9]{1,5})?$`)

// firstValue is the first of a header's comma-separated values: the one the
// proxy facing the browser wrote.
func firstValue(r *http.Request, name string) string {
	v, _, _ := strings.Cut(r.Header.Get(name), ",")
	return strings.TrimSpace(v)
}

// trusts tells whether an address is one of the trusted proxies.
func (si *signin) trusts(a netip.Addr) bool {
	a = a.Unmap()
	for _, p := range si.trusted {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// clientIP is the browser's address: the connection's, or, from a trusted
// proxy, the last one of X-Forwarded-For that is not a trusted proxy.
func (si *signin) clientIP(r *http.Request) string {
	remote := r.RemoteAddr
	if host, _, err := net.SplitHostPort(remote); err == nil {
		remote = host
	}
	if !si.fromProxy(r) {
		return remote
	}
	var hops []string
	for _, v := range r.Header.Values("X-Forwarded-For") {
		hops = append(hops, strings.Split(v, ",")...)
	}
	ip := remote
	for i := len(hops) - 1; i >= 0; i-- {
		a, ok := hostAddr(hops[i])
		if !ok {
			break // not an address: what is left of it cannot be believed
		}
		ip = a.String()
		if !si.trusts(a) {
			break
		}
	}
	return ip
}

// requestOrigin is the origin the browser used: the request's, or, from a
// trusted proxy, the one X-Forwarded-Proto and X-Forwarded-Host say.
func (si *signin) requestOrigin(r *http.Request) string {
	scheme, host := "https", r.Host // the workspace serves HTTPS only
	if si.fromProxy(r) {
		if p := strings.ToLower(firstValue(r, "X-Forwarded-Proto")); p == "https" || p == "http" {
			scheme = p
		}
		if h := firstValue(r, "X-Forwarded-Host"); forwardedHost.MatchString(h) {
			host = h
		}
	}
	return normOrigin(scheme, host)
}

// address is the workspace's address, if one is set: scheme://host.
func (w *web) address() string {
	return strings.TrimRight(w.signin().cfg.ExternalURL, "/")
}

// addressLocked: the address and the trusted proxies come from the configuration file.
func (w *web) addressLocked() bool {
	return w.fileAuth != nil || w.cfg.ExternalURL != ""
}

// origin is the workspace's own address for what comes back to it (the
// OAuth callbacks, an app's registration): its address, else the
// request's origin.
func (w *web) origin(r *http.Request) string {
	if a := w.address(); a != "" {
		return a
	}
	return w.signin().requestOrigin(r)
}

// callbackURL is the workspace's own OAuth redirect.
func (w *web) callbackURL(r *http.Request) string {
	return w.origin(r) + "/oauth/callback"
}

// publicURL is the address to open the workspace at: its address, else
// the tailnet's when remote access is on, else the request's origin.
func (w *web) publicURL(r *http.Request) string {
	if a := w.address(); a != "" {
		return a
	}
	if v, ok := w.c.remote.ready(); ok {
		return v.URL()
	}
	return w.signin().requestOrigin(r)
}

// elsewhere is the workspace's address when the request comes from
// another origin: a flow bound to this browser's cookies would come back
// there without them.
func (w *web) elsewhere(r *http.Request) string {
	a := w.address()
	if a == "" || originOf(a) == w.signin().requestOrigin(r) {
		return ""
	}
	return a
}

// atAddress tells whether a flow may start from this request; otherwise it
// answers with a message that says where to open the workspace.
func (w *web) atAddress(rw http.ResponseWriter, r *http.Request) bool {
	a := w.elsewhere(r)
	if a == "" {
		return true
	}
	w.toast(rw, fmt.Sprintf(tr(w.lang(r), "address.elsewhere"), a), false)
	return false
}

// ownOrigin tells whether a browser's Origin is the workspace's: the
// request's own host, the origin a trusted proxy says, the address, or the
// tailnet's.
func (w *web) ownOrigin(r *http.Request, o string) bool {
	u, err := url.Parse(o)
	if err != nil || u.Host == "" {
		return false
	}
	if u.Host == r.Host {
		return true
	}
	got := normOrigin(u.Scheme, u.Host)
	si := w.signin()
	if got == si.requestOrigin(r) || si.origin != "" && got == si.origin {
		return true
	}
	if v, ok := w.c.remote.ready(); ok && got == originOf(v.URL()) {
		return true
	}
	return false
}

// deviceFallback is the second address of eriphe-core the connect commands
// give, besides the tailnet's, e.g. Tailscale on the host itself.
func (w *web) deviceFallback() string {
	return w.c.store.get(deviceFallbackKey)
}

// checkDeviceURL tells whether an address can be one of eriphe-core's for
// the devices: http or https, a host and maybe a port, nothing else.
func checkDeviceURL(s string) bool {
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Hostname() != "" && u.User == nil &&
		strings.Trim(u.Path, "/") == "" && u.RawQuery == "" && u.Fragment == "" && !loopbackURL(s)
}

// addressView is the address panel of My space.
type addressView struct {
	Address  string // as set
	InUse    string // what the workspace uses now
	Trusted  string // one per line
	Fallback string // the devices' second address
	Locked   bool
	Proxied  bool   // this page came through a trusted proxy
	Client   string // the client's address, as believed
}

func (w *web) addressView(r *http.Request) addressView {
	si := w.signin()
	return addressView{Address: w.address(), InUse: w.publicURL(r), Trusted: strings.Join(si.cfg.Proxy.Trusted, "\n"),
		Fallback: w.deviceFallback(), Locked: w.addressLocked(), Proxied: si.fromProxy(r), Client: si.clientIP(r)}
}

func (w *web) addressMsg(rw http.ResponseWriter, r *http.Request, s *session, ok bool, msg string) {
	w.render(rw, r, s, "settings", "address-result", page{Data: signinResult{Msg: msg, OK: ok}})
}

// handleSaveAddress saves the address, the trusted proxies and the
// devices' second address. As the sign-in's settings, it cannot shut the
// owner out: in the proxy's mode this very page must still come in.
func (w *web) handleSaveAddress(rw http.ResponseWriter, r *http.Request, s *session) {
	lang := w.lang(r)
	fallback := strings.TrimRight(strings.TrimSpace(r.PostFormValue("device_fallback")), "/")
	if fallback != "" && !checkDeviceURL(fallback) {
		w.addressMsg(rw, r, s, false, tr(lang, "address.badfallback"))
		return
	}
	si := w.signin()
	changed := false
	if !w.addressLocked() {
		a := si.cfg
		a.ExternalURL = strings.TrimRight(strings.TrimSpace(r.PostFormValue("external_url")), "/")
		a.Proxy.Trusted = lines(r.PostFormValue("trusted"))
		if err := a.check(); err != nil {
			w.addressMsg(rw, r, s, false, w.authErrText(r, err))
			return
		}
		changed = a.ExternalURL != si.cfg.ExternalURL
		if changed && a.mode() == modeOIDC && !si.keepsPassword() {
			// The provider knows the redirect URI of the old address only.
			w.addressMsg(rw, r, s, false, tr(lang, "address.oidclocked"))
			return
		}
		next, _ := newSignin(a, false)
		if v, _ := w.decide(r, s, next); a.mode() == modeProxy && v != admit && v != admitNew {
			w.addressMsg(rw, r, s, false, tr(lang, "signin.lockout.proxy"))
			return
		}
		if err := w.saveSignin(a); err != nil {
			w.addressMsg(rw, r, s, false, tr(lang, "err.internal"))
			return
		}
	}
	if err := w.c.store.set(deviceFallbackKey, fallback); err != nil {
		w.addressMsg(rw, r, s, false, tr(lang, "err.internal"))
		return
	}
	log.Printf("web: workspace address %q, %d trusted proxies, devices' second address %q",
		w.address(), len(w.signin().trusted), fallback)
	msg := tr(lang, "address.saved")
	if changed && si.cfg.mode() == modeOIDC {
		msg += " " + fmt.Sprintf(tr(lang, "address.oidcnew"), w.address()+"/auth/callback")
	}
	w.addressMsg(rw, r, s, true, msg)
}
