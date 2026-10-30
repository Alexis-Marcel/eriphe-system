package main

import (
	"crypto/subtle"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
)

// The catalog in the workspace: tools (catalog servers), providers and
// agents, added, edited and removed by the user. Entries of the
// configuration file are shown read-only. Credentials are typed where they
// are used, kept in the vault under a name the catalog derives, and never
// shown back: a form only says whether one is set.

// formError is a refusal shown on a form: a text key, or a message as is.
func (w *web) formError(r *http.Request, err error) string {
	lang := w.lang(r)
	var ce *catalogError
	if errors.As(err, &ce) && ce.Key != "" {
		if strings.Contains(tr(lang, ce.Key), "%s") {
			return fmt.Sprintf(tr(lang, ce.Key), ce.Arg)
		}
		return tr(lang, ce.Key)
	}
	if k := err.Error(); strings.HasPrefix(k, "presets.") || strings.HasPrefix(k, "form.") {
		return tr(lang, k)
	}
	return tr(lang, "form.invalid") + " " + err.Error()
}

func words(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' || r == '\n' || r == '\r' || r == '\t' })
}

// Advanced secrets: what the vault holds and what each secret is for,
// with replace and delete only, and the configuration's secret files.

type secretView struct {
	SecretInfo
	Owner string // kind of owner: server, service, provider, config or none
	Of    string // the entry or configuration field it belongs to
	Slot  string
}

type secretsView struct {
	Open    bool
	Secrets []secretView
	Refs    []secretRefView
	Flash   string
	OK      bool
}

type secretRefView struct {
	secretRef
	Missing bool // a vault: reference with no value yet
}

func (w *web) secrets(flash string, ok bool) secretsView {
	v := secretsView{Flash: flash, OK: ok}
	refs := w.c.base.secretRefs()
	byRef := map[string]string{}
	for _, r := range refs {
		byRef[r.Ref] = r.Field
	}
	vault := currentVault()
	have := map[string]bool{}
	if vault != nil {
		v.Open = true
		list, _ := vault.list()
		for _, s := range list {
			have[s.Name] = true
			sv := secretView{SecretInfo: s, Owner: "none"}
			if field, ok := byRef[vaultPrefix+s.Name]; ok {
				sv.Owner, sv.Of = "config", field
			} else if kind, name, slot, ok := ownerOf(s.Name); ok {
				sv.Owner, sv.Of, sv.Slot = kind, name, slot
				if kind == "service" {
					sv.Slot = strings.ToUpper(slot)
				}
			}
			v.Secrets = append(v.Secrets, sv)
		}
	}
	for _, r := range refs {
		v.Refs = append(v.Refs, secretRefView{secretRef: r, Missing: r.InVault() && !have[strings.TrimPrefix(r.Ref, vaultPrefix)]})
	}
	return v
}

func (w *web) secretResult(rw http.ResponseWriter, r *http.Request, s *session, flash string, ok bool) {
	w.render(rw, r, s, "settings", "secrets", page{Data: w.secrets(flash, ok)})
}

// handleSetSecret replaces a secret the vault holds, or sets one the
// configuration names. Its value is never echoed or logged.
func (w *web) handleSetSecret(rw http.ResponseWriter, r *http.Request, s *session) {
	lang := w.lang(r)
	v := currentVault()
	if v == nil {
		w.secretResult(rw, r, s, tr(lang, "accounts.novault"), false)
		return
	}
	name := strings.TrimSpace(r.PostFormValue("name"))
	known := false
	for _, sv := range w.secrets("", false).Secrets {
		known = known || sv.Name == name
	}
	for _, ref := range w.c.base.secretRefs() {
		known = known || ref.Ref == vaultPrefix+name
	}
	if !known {
		w.secretResult(rw, r, s, tr(lang, "err.internal"), false)
		return
	}
	if err := v.set(name, r.PostFormValue("value")); err != nil {
		w.secretResult(rw, r, s, err.Error(), false)
		return
	}
	log.Printf("web: secret %s set", name)
	w.secretResult(rw, r, s, tr(lang, "accounts.replaced"), true)
}

// The OAuth flow of the workspace: Connect sends the browser to the
// authorization page, which comes back to /oauth/callback. The session
// cookie is SameSite=Strict, so it is not sent on that redirect from the
// vendor's site: the flow is bound instead to a cookie of its own
// (SameSite=Lax, sent on a top-level navigation back), whose hash the
// flow keeps, and found by its state, which only this browser saw.

const oauthCookie = "__Host-eriphe-oauth"

func (w *web) oauthBinding(rw http.ResponseWriter, r *http.Request) string {
	value := ""
	if c, err := r.Cookie(oauthCookie); err == nil && len(c.Value) >= 32 {
		value = c.Value
	} else {
		value = randomToken(32)
	}
	http.SetCookie(rw, &http.Cookie{Name: oauthCookie, Value: value, Path: "/", MaxAge: int(flowTimeout.Seconds()) + 300,
		Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode})
	return hashToken(value)
}

type oauthDone struct {
	Next   string // a pending tool: where the page goes at once
	Back   string // where the button leads; the tools by default
	Name   string
	Status string
	Tools  int
	Error  string
	Moving bool // only on the way to Next (a sign-in, web_oidc.go)
}

func (w *web) handleOAuthCallback(rw http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if isCloudState(q.Get("state")) {
		w.handleDriveCallback(rw, r) // Google Drive (web_drive.go)
		return
	}
	u, f := w.c.pool.flowByState(q.Get("state"))
	c, err := r.Cookie(oauthCookie)
	if f == nil || err != nil || subtle.ConstantTimeCompare([]byte(hashToken(c.Value)), []byte(f.binding)) != 1 {
		rw.WriteHeader(http.StatusBadRequest)
		w.render(rw, r, nil, "oauth", "", page{Title: "oauth.title", Data: oauthDone{Error: tr(w.lang(r), "oauth.expired")}})
		return
	}
	ps := w.c.pool.pendingOf(u)
	v := oauthDone{Name: u.name}
	if ps != nil {
		v.Name = ps.name
	}
	status, errText, tools, err := finishFlow(r.Context(), u, f, q.Get("code"), q.Get("state"), q.Get("iss"),
		q.Get("error"), q.Get("error_description"))
	switch {
	case err != nil:
		v.Error = err.Error()
	case status != statusReady:
		v.Error = errText
		if v.Error == "" {
			v.Error = status
		}
	default:
		v.Status, v.Tools = status, tools
	}
	if ps != nil {
		// A pending tool: back to the list at once, saved or dropped.
		// … where the dialog follows it until it is up (web_tools_dialog.go)
		v.Next = "/settings/tools?connected=" + url.QueryEscape(ps.name) + "#added/" + url.PathEscape(ps.name)
		if v.Error != "" || f.err != nil {
			why := "failed"
			if q.Get("error") == "access_denied" {
				why = "cancelled"
			}
			v.Next = "/settings/tools?notconnected=" + url.QueryEscape(ps.name) + "&why=" + why
			if ps.mode == putUpdate {
				v.Next += "&kept=1"
			}
		}
	}
	log.Printf("web: server %s: authorization from the workspace: %s", v.Name, map[bool]string{true: "connected", false: "failed"}[v.Error == ""])
	w.notify()
	w.render(rw, r, nil, "oauth", "", page{Title: "oauth.title", Data: v})
}

// connectError words why an authorization could not start: the service
// unreachable, its certificate, or else the cause as is.
func connectError(err error) error {
	var ce *catalogError
	if errors.As(err, &ce) {
		return err
	}
	if key := netErrorKey(err); key != "" {
		return &catalogError{http.StatusBadGateway, key, "", err}
	}
	return &catalogError{http.StatusBadGateway, "tools.connectfailed", truncate(err.Error(), 200), err}
}
