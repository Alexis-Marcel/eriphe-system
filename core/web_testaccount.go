package main

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
)

// "Test the connection" for a mailbox or a calendar account: the password
// typed (or the one stored, on an edit of the same account) is tried
// against the provider's own server, as eriphe-mail and eriphe-calendar
// will use it. A mailbox is tried with an IMAP LOGIN over TLS (port 993),
// a calendar with a CalDAV PROPFIND. Nothing is kept; the password is
// never logged nor shown back.

// testRoots replaces the system's roots in the tests of this file.
var testRoots *x509.CertPool

// accountSecret is the password to try: the one typed, else the stored one
// when the edit keeps the same account.
func accountSecret(r *http.Request, f toolForm, old *Server, env string, same func(RunSpec) bool) (string, error) {
	if pw := accountPassword(r, f); strings.TrimSpace(pw) != "" {
		return pw, nil
	}
	if old == nil || !old.local() {
		return "", nil
	}
	spec, err := decodeRun(old.Run)
	if err != nil || spec.Env[env].File == "" || !same(spec) {
		return "", nil
	}
	return readSecret(spec.Env[env].File)
}

func (w *web) testMail(r *http.Request, f toolForm, old *Server) testView {
	lang := w.lang(r)
	if err := f.Mail.normalize(nil); err != nil && err.Error() != "presets.soon" {
		return testView{State: "err", Text: w.formError(r, err)}
	}
	if f.Mail.Choice().Soon {
		return testView{State: "err", Text: tr(lang, "test.mailsoon")}
	}
	pw, err := accountSecret(r, f, old, mailSecretEnv, func(spec RunSpec) bool {
		prev, ok := mailFormOf(spec)
		return ok && prev.same(f.Mail)
	})
	if err != nil {
		return testView{State: "err", Text: tr(lang, "test.nostored")}
	}
	if pw == "" {
		return testView{State: "err", Text: tr(lang, "form.nopassword")}
	}
	host, port := f.Mail.imapEndpoint()
	if port == 143 { // STARTTLS: not tried here
		return testView{State: "info", Text: tr(lang, "test.mailport")}
	}
	user := f.Mail.Address
	if f.Mail.Username != "" {
		user = f.Mail.Username
	}
	ctx, cancel := context.WithTimeout(r.Context(), testConnTimeout)
	defer cancel()
	ok, reply, err := imapLogin(ctx, net.JoinHostPort(host, strconv.Itoa(port)), host, user, pw)
	switch {
	case err != nil:
		if key := netErrorKey(err); key != "" {
			return testView{State: "err", Text: tr(lang, key)}
		}
		return testView{State: "err", Text: tr(lang, "test.unreachable"), Detail: truncate(err.Error(), 200)}
	case !ok:
		v := testView{State: "err", Text: tr(lang, "test.maildenied")}
		if reply != "" {
			v.Detail = truncate(strings.ReplaceAll(reply, pw, "••••"), 200)
		}
		return v
	}
	return testView{State: "ok", Text: fmt.Sprintf(tr(lang, "test.mailok"), f.Mail.Address)}
}

// imapLogin opens an IMAP session over TLS and logs in: ok is the server's
// answer, reply its words when it refuses.
func imapLogin(ctx context.Context, addr, host, user, pw string) (ok bool, reply string, err error) {
	if strings.ContainsAny(user+pw, "\r\n\x00") {
		return false, "", nil
	}
	d := tls.Dialer{NetDialer: &net.Dialer{}, Config: &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12, RootCAs: testRoots}}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return false, "", err
	}
	defer conn.Close()
	if dl, ok := ctx.Deadline(); ok {
		conn.SetDeadline(dl)
	}
	br := bufio.NewReader(io.LimitReader(conn, 1<<20))
	if line, err := br.ReadString('\n'); err != nil {
		return false, "", err
	} else if !strings.HasPrefix(line, "* OK") {
		return false, strings.TrimSpace(line), nil
	}
	quote := func(s string) string {
		return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
	}
	if _, err := fmt.Fprintf(conn, "a1 LOGIN %s %s\r\n", quote(user), quote(pw)); err != nil {
		return false, "", err
	}
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return false, "", err
		}
		if rest, found := strings.CutPrefix(line, "a1 "); found {
			fmt.Fprintf(conn, "a2 LOGOUT\r\n")
			rest = strings.TrimSpace(rest)
			return strings.HasPrefix(rest, "OK"), rest, nil
		}
	}
}

func (w *web) testCalendar(r *http.Request, f toolForm, old *Server) testView {
	lang := w.lang(r)
	if err := f.Calendar.normalize(); err != nil {
		return testView{State: "err", Text: w.formError(r, err)}
	}
	pw, err := accountSecret(r, f, old, calSecretEnv, func(spec RunSpec) bool {
		prev, ok := calendarFormOf(spec)
		return ok && prev.same(f.Calendar)
	})
	if err != nil {
		return testView{State: "err", Text: tr(lang, "test.nostored")}
	}
	if pw == "" {
		return testView{State: "err", Text: tr(lang, "form.nopassword")}
	}
	ctx, cancel := context.WithTimeout(r.Context(), testConnTimeout)
	defer cancel()
	body := `<?xml version="1.0" encoding="utf-8"?><d:propfind xmlns:d="DAV:"><d:prop><d:current-user-principal/></d:prop></d:propfind>`
	req, err := http.NewRequestWithContext(ctx, "PROPFIND", f.Calendar.davURL(), strings.NewReader(body))
	if err != nil {
		return testView{State: "err", Text: tr(lang, "presets.badcaldav")}
	}
	req.Header.Set("Depth", "0")
	req.Header.Set("Content-Type", "application/xml; charset=utf-8")
	req.SetBasicAuth(f.Calendar.Username, pw)
	hc := &http.Client{Transport: testTransport(), CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) > 3 || req.URL.Scheme != "https" || req.URL.Host != via[0].URL.Host {
			return http.ErrUseLastResponse
		}
		req.SetBasicAuth(f.Calendar.Username, pw) // the same server's own redirect (/.well-known)
		return nil
	}}
	resp, err := hc.Do(req)
	if err != nil {
		if key := netErrorKey(err); key != "" {
			return testView{State: "err", Text: tr(lang, key)}
		}
		return testView{State: "err", Text: tr(lang, "test.unreachable"), Detail: truncate(err.Error(), 200)}
	}
	io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusMultiStatus || resp.StatusCode == http.StatusOK:
		return testView{State: "ok", Text: fmt.Sprintf(tr(lang, "test.calok"), f.Calendar.Username)}
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return testView{State: "err", Text: tr(lang, "test.caldenied")}
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusMethodNotAllowed:
		return testView{State: "err", Text: tr(lang, "test.calnotfound")}
	}
	return testView{State: "err", Text: fmt.Sprintf(tr(lang, "test.status"), resp.StatusCode)}
}
