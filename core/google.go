package main

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/oauth2"
)

// Google accounts: one sign-in for Gmail, Google Calendar and Google Drive.
//
// The user's own Google app (cloud.go) is set up once for every service and
// every account. Connecting an account asks Google for the services ticked
// (any of mail, calendar, drive) and only their scopes, with
// include_granted_scopes: adding a service later to the same account asks
// for that one only, and Google's answer covers all the account granted. The
// refresh token is kept in the vault once per account (googleSecret), and
// each service's local server reads it from there in its environment:
//
//	mail      eriphe-mail, Gmail over IMAP and SMTP with XOAUTH2 (scope https://mail.google.com/)
//	calendar  eriphe-calendar, Google CalDAV (scope …/auth/calendar)
//	drive     eriphe-files, the folders given (cloud.go; scope …/auth/drive or drive.readonly)
//
// The servers refresh their access tokens themselves; the token never
// enters a task. Which servers belong to an account is read from their
// specs: the vault reference of their token (googleRefs).
//
// eriphe-core also checks each account's refresh token with Google now and
// then (checkGoogle): one Google refused (revoked, or expired as Google
// does after 7 days for an app in Testing) marks the account "to
// reconnect"; its servers are then held as needs_auth (the pool's
// authHold), list_tools says where to reconnect, and the brain is told
// once. Connecting it again lifts the hold.

const (
	googleAccountsKey = "google_accounts"

	googleMail     = "mail"
	googleCalendar = "calendar"
	googleDrive    = "drive"

	scopeMail     = "https://mail.google.com/"
	scopeCalendar = "https://www.googleapis.com/auth/calendar"

	mailGoogleEnv = "MAIL_GOOGLE_TOKEN"
	calGoogleEnv  = "CALENDAR_GOOGLE_TOKEN"

	googleCheckEvery = 30 * time.Minute
)

// googleServices are the services one Google account can give, in the
// order they are shown.
var googleServices = []string{googleMail, googleCalendar, googleDrive}

// googleRevokeURL revokes a token at Google; tests replace it.
var googleRevokeURL = "https://oauth2.googleapis.com/revoke"

// googleAccount is a Google account connected, as eriphe-core keeps it
// (its token is in the vault, never here).
type googleAccount struct {
	Email  string   `json:"email"`
	Scopes []string `json:"scopes,omitempty"` // what the account granted, all services together
	// Reconnect is why Google refused the account's token at the last
	// check ("revoked", "client"), "" while it works.
	Reconnect string `json:"reconnect,omitempty"`
	Checked   int64  `json:"checked,omitempty"` // unix seconds of the last check
}

// googleWatch is the background check of the accounts.
type googleWatch struct {
	mu       sync.Mutex // one change of the accounts at a time
	checking bool
}

// googleSecret is the vault name of an account's token.
func googleSecret(email string) string {
	h := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(email))))
	return "google." + hex.EncodeToString(h[:6]) + ".token"
}

func (c *core) googleAccounts() []googleAccount {
	var list []googleAccount
	json.Unmarshal([]byte(c.store.get(googleAccountsKey)), &list)
	sort.Slice(list, func(i, j int) bool { return list[i].Email < list[j].Email })
	return list
}

func (c *core) googleAccount(email string) (googleAccount, bool) {
	for _, a := range c.googleAccounts() {
		if strings.EqualFold(a.Email, email) {
			return a, true
		}
	}
	return googleAccount{}, false
}

// updateGoogleAccounts changes the list; c.google.mu is held.
func (c *core) updateGoogleAccounts(change func([]googleAccount) []googleAccount) {
	list := change(c.googleAccounts())
	raw, _ := json.Marshal(list)
	c.store.set(googleAccountsKey, string(raw))
}

// grants tells whether the scopes cover a service.
func grants(scopes []string, service string) bool {
	switch service {
	case googleMail:
		return slices.Contains(scopes, scopeMail)
	case googleCalendar:
		return slices.Contains(scopes, scopeCalendar)
	case googleDrive:
		return slices.Contains(scopes, scopeDrive) || slices.Contains(scopes, scopeDriveReadonly)
	}
	return false
}

// googleRefs are the Google accounts' tokens a local server's spec reads.
func googleRefs(spec RunSpec) []string {
	var refs []string
	for _, v := range spec.Env {
		if name, ok := strings.CutPrefix(v.File, vaultPrefix); ok && strings.HasPrefix(name, "google.") && strings.HasSuffix(name, ".token") {
			refs = append(refs, name)
		}
	}
	sort.Strings(refs)
	return refs
}

// googleServiceOf tells which Google service a server's image gives.
func googleServiceOf(spec RunSpec) string {
	switch {
	case strings.HasPrefix(spec.Image, mailImage):
		return googleMail
	case strings.HasPrefix(spec.Image, calendarImage):
		return googleCalendar
	case strings.HasPrefix(spec.Image, filesImage):
		return googleDrive
	}
	return ""
}

// googleTool is a catalog server that reads a Google account's token.
type googleTool struct {
	Name    string
	Service string // mail, calendar or drive
}

// googleTools are the servers of an account, by service.
func (c *core) googleTools(email string) []googleTool {
	ref := googleSecret(email)
	var out []googleTool
	for name, s := range c.conf().Servers {
		if !s.local() {
			continue
		}
		spec, err := decodeRun(s.Run)
		if err != nil || !slices.Contains(googleRefs(spec), ref) {
			continue
		}
		out = append(out, googleTool{Name: name, Service: googleServiceOf(spec)})
	}
	sort.Slice(out, func(i, j int) bool {
		return slices.Index(googleServices, out[i].Service) < slices.Index(googleServices, out[j].Service) ||
			out[i].Service == out[j].Service && out[i].Name < out[j].Name
	})
	return out
}

// googleOwners are the accounts whose token a server reads.
func (c *core) googleOwners(s Server) []googleAccount {
	if !s.local() {
		return nil
	}
	spec, err := decodeRun(s.Run)
	if err != nil {
		return nil
	}
	refs := googleRefs(spec)
	var out []googleAccount
	for _, a := range c.googleAccounts() {
		if slices.Contains(refs, googleSecret(a.Email)) {
			out = append(out, a)
		}
	}
	return out
}

// googleHold is why a server waits for its Google account to be connected
// again, and where the user does it; "" when it does not wait. It is the
// pool's authHold.
func (c *core) googleHold(name string) (why, where string) {
	s, ok := c.conf().Servers[name]
	if !ok {
		return "", ""
	}
	// Held when every account it reads is refused: a Drive of two accounts
	// still serves the folders of the one that works.
	owners := c.googleOwners(s)
	var emails []string
	for _, a := range owners {
		if a.Reconnect == "" {
			return "", ""
		}
		emails = append(emails, a.Email)
	}
	if len(emails) == 0 {
		return "", ""
	}
	list := strings.Join(emails, ", ")
	return fmt.Sprintf("the Google account %s must be connected again (Google refused its authorization: %s)", list, owners[0].Reconnect),
		"Settings → Tools → Google → Reconnect " + list
}

// The connection.

// googleConnect is what the workspace's Connect asks.
type googleConnect struct {
	Origin   string   // the workspace, as the browser reaches it
	Binding  string   // hash of the browser's flow cookie
	Services []string // mail, calendar, drive: at least one
	Write    bool     // drive: ask for the scope that also creates files
	Hint     string   // the account, to add a service to it or reconnect it
	Back     string   // the page the browser goes back to
	Lang     string   // the words of the tools' use, if they are created
}

// googleScopes are the scopes a Connect asks for.
func (c *core) googleScopes(in googleConnect) []string {
	scopes := []string{"openid", "email"}
	for _, s := range googleServices {
		if !slices.Contains(in.Services, s) {
			continue
		}
		switch s {
		case googleMail:
			scopes = append(scopes, scopeMail)
		case googleCalendar:
			scopes = append(scopes, scopeCalendar)
		case googleDrive:
			// A read-only tool gets a read-only token, whatever the page asked.
			scope := scopeDriveReadonly
			if srv, ok := c.conf().Servers[filesServer]; in.Write && (!ok || srv.level() != accessReadOnly) {
				scope = scopeDrive
			}
			scopes = append(scopes, scope)
		}
	}
	return scopes
}

// startGoogle starts a Google authorization and returns where to send the
// browser.
func (c *core) startGoogle(in googleConnect) (string, error) {
	in.Services = slices.DeleteFunc(slices.Clone(in.Services), func(s string) bool { return !slices.Contains(googleServices, s) })
	if len(in.Services) == 0 {
		return "", &catalogError{http.StatusBadRequest, "google.noservice", "", errors.New("pick at least one service")}
	}
	app, err := googleClient()
	if errors.Is(err, errNoGoogleClient) {
		return "", &catalogError{http.StatusBadRequest, "drive.noclient", "", err}
	}
	if err != nil {
		return "", err
	}
	if why := googleOrigin(in.Origin); why != "" {
		return "", &catalogError{http.StatusBadRequest, "drive.origin." + why, in.Origin, errors.New("Google takes no redirect to " + in.Origin)}
	}
	cfg := &oauth2.Config{ClientID: app.ID, ClientSecret: app.Secret, Endpoint: googleOAuth,
		RedirectURL: in.Origin + "/oauth/callback", Scopes: c.googleScopes(in)}
	nonce := randomToken(18)
	fl := &cloudFlow{origin: in.Origin, verifier: oauth2.GenerateVerifier(), binding: in.Binding, config: cfg,
		lang: in.Lang, expires: time.Now().Add(cloudFlowExpiry), services: in.Services, back: in.Back}
	c.cloud.put(nonce, fl)
	state := signState(c.stateKey(), statePayload{Origin: in.Origin, Nonce: nonce, Expires: fl.expires.Unix()})
	opts := []oauth2.AuthCodeOption{oauth2.AccessTypeOffline, oauth2.S256ChallengeOption(fl.verifier),
		// prompt=consent: Google gives a refresh token at every consent, not only the first.
		oauth2.SetAuthURLParam("prompt", "consent"),
		// The scopes granted before stay with the new token: a service added keeps the others.
		oauth2.SetAuthURLParam("include_granted_scopes", "true")}
	if in.Hint != "" {
		opts = append(opts, oauth2.SetAuthURLParam("login_hint", in.Hint))
	}
	return cfg.AuthCodeURL(state, opts...), nil
}

// googleResult is what a connection did.
type googleResult struct {
	Email   string
	Added   []string // the services connected now
	Missing []string // the services asked for that Google did not grant
	Back    string
}

// finishGoogle takes Google's answer, as the workspace's callback received
// it: it checks the state, the flow and the browser, exchanges the code,
// keeps the account's token and gives it to the services' servers.
func (c *core) finishGoogle(ctx context.Context, origin, state, code, errCode, binding string) (googleResult, error) {
	p, err := openState(c.stateKey(), state, time.Now())
	if err != nil {
		return googleResult{}, err
	}
	fl := c.cloud.take(p.Nonce)
	if fl == nil || fl.origin != p.Origin || p.Origin != origin ||
		subtle.ConstantTimeCompare([]byte(fl.binding), []byte(binding)) != 1 {
		return googleResult{}, errBadState
	}
	res := googleResult{Back: fl.back}
	if errCode != "" {
		if errCode == "access_denied" {
			return res, &catalogError{http.StatusBadRequest, "drive.denied", "", errors.New("access denied")}
		}
		return res, fmt.Errorf("Google refused: %s", errCode)
	}
	if code == "" {
		return res, errors.New("no code in the redirect")
	}
	tok, err := fl.config.Exchange(context.WithValue(ctx, oauth2.HTTPClient, cloudHTTP), code, oauth2.VerifierOption(fl.verifier))
	if err != nil {
		var re *oauth2.RetrieveError
		if errors.As(err, &re) {
			return res, fmt.Errorf("Google refused the code: %s %s", re.ErrorCode, re.ErrorDescription)
		}
		return res, fmt.Errorf("exchanging the code: %w", err)
	}
	if tok.RefreshToken == "" {
		return res, errors.New("Google gave no refresh token: remove Eriphe's access at myaccount.google.com/connections, then connect again")
	}
	email := idTokenEmail(fmt.Sprint(tok.Extra("id_token")))
	if email == "" {
		return res, &catalogError{http.StatusBadRequest, "google.noemail", "", errors.New("Google did not say which account was connected")}
	}
	res.Email = email
	scopes := strings.Fields(fmt.Sprint(tok.Extra("scope")))
	for _, s := range fl.services {
		if grants(scopes, s) {
			res.Added = append(res.Added, s)
		} else {
			res.Missing = append(res.Missing, s)
		}
	}
	if len(res.Added) == 0 {
		return res, &catalogError{http.StatusBadRequest, "google.noscope", "", errors.New("none of the services asked for was granted")}
	}
	secret, _ := json.Marshal(map[string]string{"client_id": fl.config.ClientID, "client_secret": fl.config.ClientSecret,
		"refresh_token": tok.RefreshToken})
	if err := c.connectGoogle(fl.lang, email, scopes, string(secret), res.Added); err != nil {
		return res, err
	}
	return res, nil
}

// idTokenEmail reads the address in an ID token. It came straight from
// Google's token endpoint over TLS, which OpenID Connect takes in place of
// checking its signature (Core 1.0, 3.1.3.7).
func idTokenEmail(idToken string) string {
	parts := strings.Split(idToken, ".")
	if len(parts) != 3 {
		return ""
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return ""
	}
	var claims struct {
		Email    string `json:"email"`
		Verified *bool  `json:"email_verified"`
	}
	if json.Unmarshal(raw, &claims) != nil || (claims.Verified != nil && !*claims.Verified) {
		return ""
	}
	e := strings.TrimSpace(claims.Email)
	if !strings.Contains(e, "@") || len(e) > 254 || strings.ContainsAny(e, " \t\r\n\"<>") {
		return ""
	}
	return e
}

// connectGoogle keeps an account's token and makes its services' servers
// read it: the ones it has already pick the new token up, the ones added
// are created.
func (c *core) connectGoogle(lang, email string, scopes []string, secret string, services []string) error {
	v := currentVault()
	if v == nil {
		return &catalogError{http.StatusServiceUnavailable, "accounts.novault", "", errNoVault}
	}
	ref := googleSecret(email)
	if err := v.set(ref, secret); err != nil {
		return err
	}
	c.google.mu.Lock()
	was, _ := c.googleAccount(email)
	c.updateGoogleAccounts(func(list []googleAccount) []googleAccount {
		list = slices.DeleteFunc(list, func(a googleAccount) bool { return strings.EqualFold(a.Email, email) })
		return append(list, googleAccount{Email: email, Scopes: scopes, Checked: time.Now().Unix()})
	})
	c.google.mu.Unlock()
	log.Printf("google: account connected (%s)", strings.Join(services, ", ")) // never the address nor the token

	have := map[string]bool{}
	for _, t := range c.googleTools(email) {
		have[t.Service] = true
	}
	var errs []error
	for _, s := range services {
		switch {
		case s == googleDrive:
			// Its scopes may have changed (read only, or not).
			errs = append(errs, c.putDriveAccount(lang, email, scopes))
		case have[s]:
		case s == googleMail:
			errs = append(errs, c.addGoogleServer(lang, email, "mail", googleMailRun(email)))
		case s == googleCalendar:
			errs = append(errs, c.addGoogleServer(lang, email, "agenda", googleCalendarRun(email, c.conf().TimeZone)))
		}
	}
	if was.Reconnect != "" {
		c.googleWoken(email)
	}
	c.bump() // the nodes fetch the services' new environment
	return errors.Join(errs...)
}

// googleWoken lets an account's servers connect again.
func (c *core) googleWoken(email string) {
	for _, t := range c.googleTools(email) {
		if u := c.pool.get(t.Name); u != nil {
			u.reconnect()
		}
	}
}

// googleMailRun is eriphe-mail for a Gmail account on its Google token.
func googleMailRun(email string) RunSpec {
	cfg := map[string]any{"accounts": []map[string]any{{"name": email, "address": email, "provider": "gmail",
		"auth": map[string]string{"oauth_token_env": mailGoogleEnv}}}}
	raw, _ := json.Marshal(cfg)
	return RunSpec{Image: mailImage, Transport: transportStdio, Env: map[string]EnvValue{
		"MAIL_CONFIG": {Value: string(raw)}, mailGoogleEnv: {File: vaultPrefix + googleSecret(email)}}}
}

// googleCalendarRun is eriphe-calendar for a Google Calendar on its token.
func googleCalendarRun(email, zone string) RunSpec {
	cfg := map[string]any{"accounts": []map[string]any{{"name": email, "provider": "google", "username": email,
		"auth": map[string]string{"oauth_token_env": calGoogleEnv}}}}
	if zone != "" {
		cfg["timezone"] = zone
	}
	raw, _ := json.Marshal(cfg)
	return RunSpec{Image: calendarImage, Transport: transportStdio, Env: map[string]EnvValue{
		"CALENDAR_CONFIG": {Value: string(raw)}, calGoogleEnv: {File: vaultPrefix + googleSecret(email)}}}
}

// googleAccountOfRun reads the address a Google server of mail or calendar
// is for.
func googleAccountOfRun(spec RunSpec) string {
	env := map[string]string{googleMail: "MAIL_CONFIG", googleCalendar: "CALENDAR_CONFIG"}[googleServiceOf(spec)]
	if env == "" || len(googleRefs(spec)) == 0 {
		return ""
	}
	var cfg struct {
		Accounts []struct {
			Address  string `json:"address"`
			Username string `json:"username"`
		} `json:"accounts"`
	}
	if json.Unmarshal([]byte(spec.Env[env].Value), &cfg) != nil || len(cfg.Accounts) != 1 {
		return ""
	}
	return cmpOr(cfg.Accounts[0].Address, cfg.Accounts[0].Username)
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// addGoogleServer creates the server of a service, under a name free in
// the catalog: base, then base-<the address's local part>.
func (c *core) addGoogleServer(lang, email, base string, spec RunSpec) error {
	name := c.freeServerName(base, email)
	use := map[string]string{"mail": "preset.mail.use", "agenda": "preset.calendar.use"}[base]
	srv := Server{Use: tr(lang, use) + " (" + email + ")", Run: marshalRun(spec)}.withLevel(accessReads)
	return c.putServer(putCreate, name, srv, nil)
}

func (c *core) freeServerName(base, email string) string {
	taken := func(n string) bool { _, ok := c.conf().Servers[n]; return ok }
	if !taken(base) {
		return base
	}
	local, _, _ := strings.Cut(email, "@")
	stem := base + "-" + slugName(local)
	if len(stem) > 28 {
		stem = strings.TrimRight(stem[:28], "-")
	}
	if stem == base+"-" {
		stem = base
	}
	if !taken(stem) && serverName.MatchString(stem) {
		return stem
	}
	for n := 2; ; n++ {
		if s := fmt.Sprintf("%s-%d", stem, n); !taken(s) {
			return s
		}
	}
}

// removeGoogleAccount takes an account away: its servers (and its Drive
// folders), its token in the vault, then its authorization at Google.
func (c *core) removeGoogleAccount(ctx context.Context, email string) error {
	if _, ok := c.googleAccount(email); !ok {
		return &catalogError{http.StatusNotFound, "google.unknown", "", errors.New("no such Google account")}
	}
	var errs []error
	for _, t := range c.googleTools(email) {
		if t.Service == googleDrive {
			errs = append(errs, c.dropDriveAccount(email))
			continue
		}
		if c.source(kindServer, t.Name) == sourceWorkspace {
			errs = append(errs, c.deleteServer(t.Name))
		}
	}
	if err := errors.Join(errs...); err != nil {
		return err
	}
	ref := googleSecret(email)
	if v := currentVault(); v != nil {
		if raw, err := v.get(ref); err == nil {
			var tok struct {
				RefreshToken string `json:"refresh_token"`
			}
			if json.Unmarshal([]byte(raw), &tok) == nil && tok.RefreshToken != "" {
				revokeGoogle(ctx, tok.RefreshToken)
			}
		}
		v.remove(ref)
	}
	c.google.mu.Lock()
	c.updateGoogleAccounts(func(list []googleAccount) []googleAccount {
		return slices.DeleteFunc(list, func(a googleAccount) bool { return strings.EqualFold(a.Email, email) })
	})
	c.google.mu.Unlock()
	log.Printf("google: account removed")
	return nil
}

// revokeGoogle asks Google to forget a token; a failure leaves it to the
// user (myaccount.google.com/connections).
func revokeGoogle(ctx context.Context, token string) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, googleRevokeURL, strings.NewReader(url.Values{"token": {token}}.Encode()))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if resp, err := cloudHTTP.Do(req); err == nil {
		resp.Body.Close()
	}
}

// The check of the accounts.

// googleTick checks the accounts due, in the background: called with the
// nodes' check (every 15 s), it does something every googleCheckEvery.
func (c *core) googleTick(ctx context.Context, now time.Time) {
	due := false
	for _, a := range c.googleAccounts() {
		due = due || now.Sub(time.Unix(a.Checked, 0)) >= googleCheckEvery
	}
	if !due {
		return
	}
	c.google.mu.Lock()
	if c.google.checking {
		c.google.mu.Unlock()
		return
	}
	c.google.checking = true
	c.google.mu.Unlock()
	go func() {
		defer func() { c.google.mu.Lock(); c.google.checking = false; c.google.mu.Unlock() }()
		for _, a := range c.googleAccounts() {
			if now.Sub(time.Unix(a.Checked, 0)) >= googleCheckEvery {
				c.checkGoogle(ctx, a.Email)
			}
		}
	}()
}

// checkGoogle asks Google for an access token with an account's refresh
// token, and marks the account to reconnect if Google refuses it. Google
// out of reach changes nothing.
func (c *core) checkGoogle(ctx context.Context, email string) {
	why := ""
	raw, err := readSecret(vaultPrefix + googleSecret(email))
	var tok struct {
		ClientID     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`
		RefreshToken string `json:"refresh_token"`
	}
	if err != nil || json.Unmarshal([]byte(raw), &tok) != nil || tok.RefreshToken == "" {
		why = googleNoToken
	} else {
		cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		code, _, _, err := googleToken(cctx, cloudHTTP, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {tok.RefreshToken},
			"client_id": {tok.ClientID}, "client_secret": {tok.ClientSecret}})
		cancel()
		switch {
		case err != nil:
			return // out of reach: checked again later
		case code == "invalid_grant":
			why = googleRevoked
		case code == "invalid_client" || code == "unauthorized_client" || code == "deleted_client":
			why = googleBadClient
		}
	}
	c.markGoogle(email, why)
}

// googleWhyFR words a refusal for the brain, in French.
var googleWhyFR = map[string]string{
	googleRevoked:   "révoquée, ou expirée",
	googleBadClient: "l'app Google avec laquelle il a été connecté est refusée",
	googleNoToken:   "son autorisation enregistrée est illisible",
}

const (
	googleRevoked   = "revoked or expired"
	googleBadClient = "the Google app it was connected with is refused"
	googleNoToken   = "no token"
)

// markGoogle records the outcome of a check; the first refusal holds the
// account's servers and tells the brain.
func (c *core) markGoogle(email, why string) {
	c.google.mu.Lock()
	was, ok := c.googleAccount(email)
	if !ok {
		c.google.mu.Unlock()
		return
	}
	c.updateGoogleAccounts(func(list []googleAccount) []googleAccount {
		for i := range list {
			if strings.EqualFold(list[i].Email, email) {
				list[i].Reconnect, list[i].Checked = why, time.Now().Unix()
			}
		}
		return list
	})
	c.google.mu.Unlock()
	if was.Reconnect == why {
		return
	}
	c.googleWoken(email) // held, or free again
	if why == "" {
		return
	}
	log.Printf("google: an account must be connected again (%s)", why)
	var names []string
	for _, t := range c.googleTools(email) {
		names = append(names, t.Name)
	}
	if c.conf().brain() == nil || len(names) == 0 {
		return
	}
	user := c.conf().User
	if user == "" {
		user = "l'utilisateur"
	}
	text := fmt.Sprintf("Google a retiré l'autorisation du compte %s (%s) : les outils %s ne marchent plus tant que %s ne l'a pas reconnecté (Réglages › Outils › Google › Reconnecter, un clic). Dis-le-lui une fois, au bon moment ; ne lance pas de tâche qui en a besoin d'ici là.",
		email, googleWhyFR[why], strings.Join(names, ", "), user)
	if _, err := c.store.addMessage(roleEvent, text, false); err == nil {
		c.ping()
	}
}
