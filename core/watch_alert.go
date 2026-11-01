package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

// The source alerte: a service calls the watch's own webhook, on the
// workspace's HTTPS address:
//
//	POST https://<workspace>/hooks/watch/<token>
//
// The token is 32 random bytes, made in Settings and shown there once; only
// its hash is kept. The model never sees it. When the watch is signed, the
// sender also signs the body with a secret made at the same time (kept in
// the vault, watch-hook-<id>): HMAC-SHA256 of the raw body, in hex, in
// X-Hub-Signature-256 ("sha256=<hex>", GitHub's), X-Grafana-Alerting-Signature
// (Grafana's webhook contact point with an HMAC secret) or X-Eriphe-Signature.
//
// The body (64 KiB at most) is external content: kept with the run, cut to
// a few thousand characters, quoted to the brain as data, and the turn it
// wakes is tainted. One wake waits at a time per watch; past 60 calls an
// hour the hook answers 429. For an urgent watch, a call opens an incident
// and one whose top-level status is "resolved" ends it (watch_urgent.go).

const (
	alertBodyMax   = 64 << 10
	alertDetailMax = 4000
	alertPerHour   = 60
	causeAlert     = "alert"
	hookPath       = "/hooks/watch/"
)

var signatureHeaders = []string{"X-Hub-Signature-256", "X-Grafana-Alerting-Signature", "X-Eriphe-Signature"}

func hookSecretName(id int64) string { return fmt.Sprintf("watch-hook-%d", id) }

// newWatchHook makes an alerte watch's webhook token, and its signing
// secret if it is signed; both are returned this once. A new one replaces
// the old: the previous address stops working.
func (c *core) newWatchHook(id int64) (token, secret string, err error) {
	w, err := c.watch(id)
	if err != nil {
		return "", "", err
	}
	if w.Source != sourceAlert {
		return "", "", errors.New("only an alerte watch has a webhook")
	}
	token = randomToken(32)
	if w.Spec.Signed {
		v := currentVault()
		if v == nil {
			return "", "", errors.New("a signed webhook needs the vault, which is not open")
		}
		secret = randomToken(32)
		if err := v.set(hookSecretName(id), secret); err != nil {
			return "", "", err
		}
	}
	_, err = c.store.db.Exec(`UPDATE watches SET hook = ? WHERE id = ?`, hashToken(token), id)
	return token, secret, err
}

// alertResult is what the webhook answers: a status, and a word for the sender.
type alertResult struct {
	Status int
	Text   string
}

// receiveAlert handles one call to a webhook.
func (c *core) receiveAlert(token string, h http.Header, body []byte) alertResult {
	notFound := alertResult{http.StatusNotFound, "not found"}
	if len(token) < 20 || len(token) > 100 {
		return notFound
	}
	w, err := scanWatch(c.store.db.QueryRow(`SELECT `+watchCols+` FROM watches WHERE hook = ? AND source = ?`,
		hashToken(token), sourceAlert))
	if err != nil {
		return notFound
	}
	if w.Spec.Signed && !c.signedBy(w.ID, h, body) {
		log.Printf("watch %d: a webhook call without a valid signature was refused", w.ID)
		return alertResult{http.StatusUnauthorized, "bad signature"}
	}
	if w.State != watchActive {
		return alertResult{http.StatusConflict, "this watch is paused"}
	}
	var n int
	c.store.db.QueryRow(`SELECT COUNT(*) FROM watch_runs WHERE watch_id = ? AND fired >= ?`, w.ID,
		c.notifyTime().Add(-time.Hour).Unix()).Scan(&n)
	if n >= alertPerHour {
		return alertResult{http.StatusTooManyRequests, "too many calls"}
	}
	// An urgent watch's fixed message, before the brain looks (watch_urgent.go).
	urgent := c.urgentTransition(w, !alertResolved(body))
	c.fireWatch(w, &watchRun{Cause: causeAlert, Detail: alertDetail(body), External: true, Urgent: urgent})
	return alertResult{http.StatusAccepted, "ok"}
}

// signedBy checks the body's HMAC against the watch's secret.
func (c *core) signedBy(id int64, h http.Header, body []byte) bool {
	secret, err := vaultSecret(hookSecretName(id))
	if err != nil || secret == "" {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	want := hex.EncodeToString(mac.Sum(nil))
	for _, name := range signatureHeaders {
		got := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(h.Get(name))), "sha256=")
		if got != "" && subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1 {
			return true
		}
	}
	return false
}

// alertDetail is the body as kept: compact JSON, or text, without
// invisible characters, cut.
func alertDetail(body []byte) string {
	var buf bytes.Buffer
	text := string(body)
	if json.Compact(&buf, body) == nil {
		text = buf.String()
	}
	text = strings.Map(func(r rune) rune {
		if invisible(r) {
			return -1
		}
		return r
	}, strings.ToValidUTF8(text, ""))
	return truncate(text, alertDetailMax)
}

// handleWatchHook is the webhook, on the workspace's server: no session,
// the token in the path is the authorisation.
func (w *web) handleWatchHook(rw http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(rw, r.Body, alertBodyMax))
	if err != nil {
		http.Error(rw, "body too large", http.StatusRequestEntityTooLarge)
		return
	}
	res := w.c.receiveAlert(r.PathValue("token"), r.Header, body)
	http.Error(rw, res.Text, res.Status)
}
