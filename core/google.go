package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"slices"
	"sort"
	"strings"
)

// Google accounts: one sign-in for Gmail, Google Calendar and Google Drive.
//
// The user's own Google app (cloud.go) is set up once for every service and
// every account. Each service has one server for all the accounts (Gmail
// beside the other mailboxes in the mail, Google Calendar in the calendar:
// accounts.go; Drive in eriphe-files). Connecting an account asks Google for the services ticked
// (any of mail, calendar, drive) and only their scopes, with
// include_granted_scopes: adding a service later to the same account asks
// for that one only, and Google's answer covers all the account granted. The
// account is a connection (connections.go): its refresh token is kept in
// the vault once, and each service's local server reads it from there in
// its environment:
//
//	mail      eriphe-mail, Gmail over IMAP and SMTP with XOAUTH2 (scope https://mail.google.com/)
//	calendar  eriphe-calendar, Google CalDAV (scope …/auth/calendar)
//	drive     eriphe-files, the folders given (cloud.go; scope …/auth/drive or drive.readonly)
//
// The servers refresh their access tokens themselves; the token never
// enters a task. Which servers belong to an account is read from their
// specs: the vault reference of their token (connectionTools). The
// connection's check, and the hold of its servers when Google refuses it,
// are the connections' own.

const (
	googleMail     = "mail"
	googleCalendar = "calendar"
	googleDrive    = "drive"

	scopeMail     = "https://mail.google.com/"
	scopeCalendar = "https://www.googleapis.com/auth/calendar"

	mailGoogleEnv = "MAIL_GOOGLE_TOKEN"
	calGoogleEnv  = "CALENDAR_GOOGLE_TOKEN"
)

// googleServices are the services one Google account can give, in the
// order they are shown.
var googleServices = []string{googleMail, googleCalendar, googleDrive}

// googleAccounts are the Google accounts connected.
func (c *core) googleAccounts() []connection { return c.connectionsAt(providerGoogle.Name) }

func (c *core) googleAccount(email string) (connection, bool) {
	return c.connectionOf(providerGoogle.Name, email)
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
	cn, ok := c.googleAccount(email)
	if !ok {
		return nil
	}
	var out []googleTool
	for _, name := range c.connectionTools(cn) {
		spec, _ := decodeRun(c.conf().Servers[name].Run)
		out = append(out, googleTool{Name: name, Service: googleServiceOf(spec)})
	}
	sort.Slice(out, func(i, j int) bool {
		return slices.Index(googleServices, out[i].Service) < slices.Index(googleServices, out[j].Service) ||
			out[i].Service == out[j].Service && out[i].Name < out[j].Name
	})
	return out
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

// googleScopes are the services' scopes a Connect asks for.
func (c *core) googleScopes(in googleConnect) []string {
	var scopes []string // beside the identity, which the sign-in asks for
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
	return c.startSignIn(providerGoogle, app, signInStart{Origin: in.Origin, Binding: in.Binding,
		Scopes: c.googleScopes(in), Hint: in.Hint, Lang: in.Lang, Back: in.Back, Asked: in.Services})
}

// googleResult is what a connection did.
type googleResult struct {
	Email   string
	Added   []string // the services connected now
	Missing []string // the services asked for that Google did not grant
	Back    string
}

// finishGoogle takes Google's answer, as the workspace's callback received
// it: the neutral flow checks it and exchanges the code, then the account's
// token is kept and given to the services' servers.
func (c *core) finishGoogle(ctx context.Context, origin, state, code, errCode, binding string) (googleResult, error) {
	fl, tok, err := c.finishSignIn(ctx, origin, state, code, errCode, binding)
	var res googleResult
	if fl != nil {
		res.Back = fl.back
	}
	if err != nil {
		return res, err
	}
	res.Email = tok.Identity
	for _, s := range fl.asked {
		if grants(tok.Scopes, s) {
			res.Added = append(res.Added, s)
		} else {
			res.Missing = append(res.Missing, s)
		}
	}
	if len(res.Added) == 0 {
		return res, &catalogError{http.StatusBadRequest, "google.noscope", "", errors.New("none of the services asked for was granted")}
	}
	if err := c.connectGoogle(fl.lang, tok.Identity, tok.Scopes, tok.secret(fl), res.Added); err != nil {
		return res, err
	}
	return res, nil
}

// connectGoogle keeps an account's token and makes its services' servers
// read it: the ones it has already pick the new token up, the ones added
// are created.
func (c *core) connectGoogle(lang, email string, scopes []string, secret string, services []string) error {
	cn, wasLapsed, err := c.keepConnection(providerGoogle.Name, email, scopes, secret)
	if err != nil {
		return err
	}
	log.Printf("google: account connected (%s)", strings.Join(services, ", ")) // never the address nor the token

	have := map[string]bool{}
	for _, t := range c.googleTools(email) {
		have[t.Service] = true
	}
	ref := &EnvValue{File: vaultPrefix + cn.Secret}
	var errs []error
	for _, s := range services {
		switch {
		case s == googleDrive:
			// Its scopes may have changed (read only, or not).
			errs = append(errs, c.putDriveAccount(lang, email, scopes, cn.Secret))
		case have[s]:
		default:
			if svc, ok := serviceByGoogle(s); ok {
				errs = append(errs, c.putAccount(svc, accountPut{Lang: lang, Account: googleAccountFor(svc, email), Secret: ref}))
			}
		}
	}
	if wasLapsed {
		c.connectionWoken(cn)
	}
	c.bump() // the nodes fetch the services' new environment
	return errors.Join(errs...)
}

// googleAccountFor is a Google account as an account of the mail (Gmail
// over IMAP with XOAUTH2) or of the calendar (Google CalDAV).
func googleAccountFor(svc accountService, email string) svcAccount {
	if svc == mailService {
		return svcAccount{Name: email, Address: email, Provider: "gmail"}
	}
	return svcAccount{Name: email, Provider: "google", Username: email}
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// freeServerName is a name free in the catalog: base, then
// base-<the address's local part>.
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
	cn, ok := c.googleAccount(email)
	if !ok {
		return &catalogError{http.StatusNotFound, "google.unknown", "", errors.New("no such Google account")}
	}
	var errs []error
	for _, t := range c.googleTools(email) {
		if t.Service == googleDrive {
			errs = append(errs, c.dropDriveAccount(email))
			continue
		}
		if svc, ok := serviceByGoogle(t.Service); ok && c.source(kindServer, t.Name) == sourceWorkspace {
			errs = append(errs, c.dropAccount(svc, email))
		}
	}
	if err := errors.Join(errs...); err != nil {
		return err
	}
	c.dropConnection(ctx, cn)
	log.Printf("google: account removed")
	return nil
}
