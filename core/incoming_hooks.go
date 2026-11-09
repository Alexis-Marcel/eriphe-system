package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// Incoming addresses: any system that has something to tell (Grafana,
// Alertmanager, GitHub, a Home Assistant automation, an iPhone shortcut, a
// script) calls an address of its own, on the workspace's HTTPS address:
//
//	POST https://<workspace>/hooks/in/<token>
//
// eriphe-core knows no sender: each address is named (getout), and each
// call writes one event hook.received keyed by that name, with the body's
// top-level "status" when it has one (firing, resolved: Grafana's and
// Alertmanager's convention, as many others) and the body as detail. A
// skill reacts to them (when: hook.received key=getout status=firing).
// /hooks/alert/<token>, the former path, still works.
//
// The token is 32 random bytes, shown once in Settings → Skills; only its
// hash is kept. A signed hook also checks the
// body's HMAC-SHA256 with a secret made at the same time (kept in the
// vault, alert-hook-<id>), in X-Hub-Signature-256 ("sha256=<hex>"),
// X-Grafana-Alerting-Signature or X-Eriphe-Signature.
//
// The body (64 KiB at most) is external content: the event is external,
// its detail cut to a few thousand characters. Past 60 calls an hour a
// hook answers 429.

const (
	alertBodyMax   = 64 << 10
	alertDetailMax = 4000
	alertPerHour   = 60
	hookPath       = "/hooks/in/"
	hookPathOld    = "/hooks/alert/"
	urgentTextMax  = 160

	notifyFromUrgent = "urgent" // a skill's fixed urgent text (skill_cards.go)
)

const alertHooksSchema = `CREATE TABLE IF NOT EXISTS alert_hooks (
  id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL UNIQUE, hook TEXT NOT NULL,
  signed INTEGER NOT NULL DEFAULT 0, created INTEGER NOT NULL);`

func init() { extraSchema = append(extraSchema, alertHooksSchema) }

var (
	signatureHeaders = []string{"X-Hub-Signature-256", "X-Grafana-Alerting-Signature", "X-Eriphe-Signature"}
	alertNameRe      = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)
)

func hookSecretName(id int64) string { return fmt.Sprintf("alert-hook-%d", id) }

// alertHook is an incoming alert's webhook.
type alertHook struct {
	ID      int64
	Name    string
	Signed  bool
	Created time.Time
}

func (c *core) alertHooks() []alertHook {
	rows, err := c.store.db.Query(`SELECT id, name, signed, created FROM alert_hooks ORDER BY name`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []alertHook
	for rows.Next() {
		var h alertHook
		var at int64
		if rows.Scan(&h.ID, &h.Name, &h.Signed, &at) == nil {
			h.Created = time.Unix(at, 0)
			out = append(out, h)
		}
	}
	return out
}

// newAlertHook makes a hook, or a new token for one of that name (the old
// address stops working); its token and secret are returned this once.
func (c *core) newAlertHook(name string, signed bool) (token, secret string, err error) {
	name = strings.ToLower(strings.TrimSpace(name))
	if !alertNameRe.MatchString(name) {
		return "", "", errors.New("a name of lowercase letters, digits and dashes: getout")
	}
	token = randomToken(32)
	var id int64
	err = c.store.db.QueryRow(`INSERT INTO alert_hooks (name, hook, signed, created) VALUES (?, ?, ?, ?)
		ON CONFLICT(name) DO UPDATE SET hook = excluded.hook, signed = excluded.signed RETURNING id`,
		name, hashToken(token), signed, time.Now().Unix()).Scan(&id)
	if err != nil {
		return "", "", err
	}
	if signed {
		v := currentVault()
		if v == nil {
			c.store.db.Exec(`DELETE FROM alert_hooks WHERE id = ?`, id)
			return "", "", errors.New("a signed webhook needs the vault, which is not open")
		}
		secret = randomToken(32)
		if err := v.set(hookSecretName(id), secret); err != nil {
			return "", "", err
		}
	}
	return token, secret, nil
}

func (c *core) deleteAlertHook(id int64) error {
	if v := currentVault(); v != nil {
		v.remove(hookSecretName(id))
	}
	_, err := c.store.db.Exec(`DELETE FROM alert_hooks WHERE id = ?`, id)
	return err
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
	var hk alertHook
	err := c.store.db.QueryRow(`SELECT id, name, signed FROM alert_hooks WHERE hook = ?`, hashToken(token)).Scan(&hk.ID, &hk.Name, &hk.Signed)
	if err == sql.ErrNoRows || err != nil {
		return notFound
	}
	if hk.Signed && !c.signedBy(hk.ID, h, body) {
		log.Printf("alert %s: a webhook call without a valid signature was refused", hk.Name)
		return alertResult{http.StatusUnauthorized, "bad signature"}
	}
	var n int
	c.store.db.QueryRow(`SELECT COUNT(*) FROM event_journal WHERE source = ? AND at >= ?`, alertSource(hk.ID),
		time.Now().Add(-time.Hour).Unix()).Scan(&n)
	if n >= alertPerHour {
		return alertResult{http.StatusTooManyRequests, "too many calls"}
	}
	p := map[string]any{"hook": hk.Name, "detail": alertDetail(body)}
	if st := bodyStatus(body); st != "" {
		p["status"] = st
	}
	c.emitLogged(journalEvent{Kind: evHookReceived, Key: hk.Name, Source: alertSource(hk.ID), External: true, Payload: p})
	return alertResult{http.StatusAccepted, "ok"}
}

func alertSource(id int64) string { return fmt.Sprintf("alert:%d", id) }

// signedBy checks the body's HMAC against the hook's secret.
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

// bodyStatus is the body's top-level "status", when it is a short word
// (firing, resolved), lowercased; "" otherwise.
func bodyStatus(body []byte) string {
	var v struct {
		Status any `json:"status"`
	}
	if json.Unmarshal(body, &v) != nil {
		return ""
	}
	s, _ := v.Status.(string)
	s = strings.ToLower(strings.TrimSpace(s))
	if !alertNameRe.MatchString(s) {
		return ""
	}
	return s
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

// handleAlertHook is the webhook, on the workspace's server: no session,
// the token in the path is the authorisation.
func (w *web) handleAlertHook(rw http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(rw, r.Body, alertBodyMax))
	if err != nil {
		http.Error(rw, "body too large", http.StatusRequestEntityTooLarge)
		return
	}
	res := w.c.receiveAlert(r.PathValue("token"), r.Header, body)
	http.Error(rw, res.Text, res.Status)
}
