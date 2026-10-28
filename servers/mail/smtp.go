package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"time"

	"github.com/emersion/go-sasl"
	"github.com/emersion/go-smtp"
)

// smtpSend submits a message over implicit TLS or STARTTLS; credentials are
// only sent once the connection is encrypted.
func (a *Account) smtpSend(ctx context.Context, from string, rcpts []string, msg []byte) error {
	ctx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()
	d := &net.Dialer{Timeout: 20 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", a.SMTP.addr())
	if err != nil {
		return fmt.Errorf("%s: connect to %s: %w", a.Name, a.SMTP.addr(), err)
	}
	if dl, ok := ctx.Deadline(); ok {
		conn.SetDeadline(dl)
	}
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()

	var c *smtp.Client
	if a.SMTP.Security == "tls" {
		tc := tls.Client(conn, a.tlsConfig(a.SMTP.Host))
		if err := tc.HandshakeContext(ctx); err != nil {
			conn.Close()
			return fmt.Errorf("%s: SMTP TLS: %w", a.Name, err)
		}
		c = smtp.NewClient(tc)
	} else if c, err = smtp.NewClientStartTLS(conn, a.tlsConfig(a.SMTP.Host)); err != nil {
		conn.Close()
		return fmt.Errorf("%s: SMTP STARTTLS: %w", a.Name, err)
	}
	defer c.Close()
	if _, ok := c.TLSConnectionState(); !ok {
		return fmt.Errorf("%s: SMTP connection is not encrypted", a.Name)
	}

	secret, err := a.secret()
	if err != nil {
		return fmt.Errorf("%s: %w", a.Name, err)
	}
	var auth sasl.Client
	switch {
	case a.Auth.oauth():
		auth = newXOAuth2Client(a.Username, secret)
	case !c.SupportsAuth("PLAIN") && c.SupportsAuth("LOGIN"):
		auth = sasl.NewLoginClient(a.Username, secret)
	default:
		auth = sasl.NewPlainClient("", a.Username, secret)
	}
	if err := c.Auth(auth); err != nil {
		return fmt.Errorf("%s: SMTP login failed: %w", a.Name, err)
	}
	if err := c.SendMail(from, rcpts, bytes.NewReader(msg)); err != nil {
		return fmt.Errorf("%s: send: %w", a.Name, err)
	}
	c.Quit()
	return nil
}
