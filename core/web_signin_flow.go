package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// The browser's side of an account's sign-in (connection_tools.go): off to
// the provider, and back.

// startSignInFlow sends the browser to the provider, or shows why not in
// the fragment given.
func (w *web) startSignInFlow(rw http.ResponseWriter, r *http.Request, s *session, in connectAsk, frag string, view func(error) any) {
	if !w.atAddress(rw, r) {
		return
	}
	in.Origin, in.Lang = w.origin(r), w.lang(r)
	in.Binding = w.oauthBinding(rw, r)
	authURL, err := w.c.startAccountSignIn(in)
	if err != nil {
		w.render(rw, r, s, "settings", frag, page{Data: view(err)})
		return
	}
	rw.Header().Set("HX-Redirect", authURL)
	rw.WriteHeader(http.StatusOK)
}

// handleSignInCallback is the end of an account's sign-in, at the
// workspace's /oauth/callback, where the provider sends the browser back.
func (w *web) handleSignInCallback(rw http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	binding := ""
	if c, err := r.Cookie(oauthCookie); err == nil {
		binding = hashToken(c.Value)
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
	defer cancel()
	res, err := w.c.finishAccountSignIn(ctx, w.origin(r), q.Get("state"), q.Get("code"), q.Get("error"), binding)
	back := cmpOr(res.Back, "/settings/tools")
	v := oauthDone{Name: providerLabel(res.Provider), Back: back}
	if err != nil {
		if errors.Is(err, errBadState) {
			rw.WriteHeader(http.StatusBadRequest)
			err = errors.New(tr(w.lang(r), "oauth.expired"))
		}
		v.Error = w.connError(r, err)
		log.Printf("web: a sign-in failed")
	} else {
		// A page of this site, then the settings: the session cookie
		// (SameSite=Strict) is not sent on the redirect from the provider.
		u := url.Values{"connected": {"1"}}
		if len(res.Missing) > 0 {
			u.Set("missing", strings.Join(res.Missing, ","))
		}
		v.Next = back + "?" + u.Encode()
		log.Printf("web: an account signed in")
	}
	w.notify()
	w.render(rw, r, nil, "oauth", "", page{Title: "oauth.title", Data: v})
}
