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

// Incoming addresses: monitors that listen (monitors.go, way listen). Any
// system that has something to tell and can reach Eriphe (Grafana,
// Alertmanager, GitHub, a Home Assistant automation, an iPhone shortcut, a
// script) calls the address of one:
//
//	POST https://<workspace>/hooks/in/<token>
//
// eriphe-core knows no sender: each call writes one monitor.received keyed
// by the monitor's name, with the body's top-level "status" when it has one
// (firing, resolved: Grafana's and Alertmanager's convention, as many
// others) and the body as detail. A skill reacts to them (when:
// monitor.received key=getout status=firing).
//
// The token is 32 random bytes, shown once on the Monitors page; only its
// hash is kept, in the monitor's hook. A signed address also checks the
// body's HMAC-SHA256 with a secret made at the same time (kept in the vault,
// monitor-hook-<id>), in the header the user named for it
// (X-Eriphe-Signature unless the sender has its own), as hex, "sha256="
// before it or not.
//
// The body (64 KiB at most) is external content: the event is external,
// its detail cut to a few thousand characters. Past 60 calls an hour an
// address answers 429; a paused one 503. The brain never makes one: its
// token would pass through the conversation.

const (
	alertBodyMax   = 64 << 10
	alertDetailMax = 4000
	alertPerHour   = 60
	hookPath       = "/hooks/in/"
	urgentTextMax  = 160

	notifyFromUrgent  = "urgent" // a skill's fixed urgent text (skill_cards.go)
	evMonitorReceived = "monitor.received"
	defaultSigHeader  = "X-Eriphe-Signature"
)

// The addresses lived in alert_hooks before they were monitors (#360).
const alertHooksGone = `DROP TABLE IF EXISTS alert_hooks;`

func init() { extraSchema = append(extraSchema, alertHooksGone) }

var sigHeaderRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{0,63}$`)

func hookSecretName(id int64) string { return fmt.Sprintf("monitor-hook-%d", id) }

// newListenMonitor makes a monitor that listens, or a new token for one of
// that name (the old address stops working); its token and secret are
// returned this once. A signed one reads its signature from header,
// X-Eriphe-Signature if "".
func (c *core) newListenMonitor(name string, signed bool, header string) (token, secret string, err error) {
	name = strings.ToLower(strings.TrimSpace(name))
	if !monitorNameRe.MatchString(name) {
		return "", "", errors.New("a name of lowercase letters, digits and dashes: getout")
	}
	header = strings.TrimSpace(header)
	switch {
	case !signed:
		header = ""
	case header == "":
		header = defaultSigHeader
	case !sigHeaderRe.MatchString(header):
		return "", "", errors.New("a signature header is a header name: letters, digits and dashes (X-Hub-Signature-256)")
	}
	var v *Vault
	if signed {
		if v = currentVault(); v == nil {
			return "", "", errors.New("a signed address needs the vault, which is not open")
		}
	}
	spec, _ := json.Marshal(monitorSpec{Way: wayListen, Signed: signed, SigHeader: header})
	token = randomToken(32)
	now := time.Now().Unix()
	m, err := c.monitorNamed(name)
	var id int64
	switch {
	case err == sql.ErrNoRows:
		var n int
		c.store.db.QueryRow(`SELECT COUNT(*) FROM monitors`).Scan(&n)
		if n >= monitorsMax {
			return "", "", fmt.Errorf("%d monitors at most", monitorsMax)
		}
		res, err := c.store.db.Exec(`INSERT INTO monitors (name, state, spec, hook, created, updated) VALUES (?, ?, ?, ?, ?, ?)`,
			name, monitorOn, string(spec), hashToken(token), now, now)
		if err != nil {
			return "", "", err
		}
		id, _ = res.LastInsertId()
	case err != nil:
		return "", "", err
	case m.Spec.Way != wayListen:
		return "", "", fmt.Errorf("%q is already a monitor that polls a tool: pick another name", name)
	default:
		id = m.ID
		if _, err := c.store.db.Exec(`UPDATE monitors SET spec = ?, hook = ?, updated = ? WHERE id = ?`, string(spec), hashToken(token), now, id); err != nil {
			return "", "", err
		}
	}
	if signed {
		secret = randomToken(32)
		if err := v.set(hookSecretName(id), secret); err != nil {
			return "", "", err
		}
	} else if v := currentVault(); v != nil {
		v.remove(hookSecretName(id))
	}
	return token, secret, nil
}

// alertResult is what the webhook answers: a status, and a word for the sender.
type alertResult struct {
	Status int
	Text   string
}

// receiveAlert handles one call to an address.
func (c *core) receiveAlert(token string, h http.Header, body []byte) alertResult {
	notFound := alertResult{http.StatusNotFound, "not found"}
	if len(token) < 20 || len(token) > 100 {
		return notFound
	}
	m, err := scanMonitor(c.store.db.QueryRow(`SELECT `+monitorCols+` FROM monitors WHERE hook = ?`, hashToken(token)))
	if err != nil || m.Spec.Way != wayListen {
		return notFound
	}
	if m.Spec.Signed && !signedBy(m, h, body) {
		log.Printf("monitor %s: a call without a valid signature was refused", m.Name)
		return alertResult{http.StatusUnauthorized, "bad signature"}
	}
	if m.State != monitorOn {
		return alertResult{http.StatusServiceUnavailable, "paused"}
	}
	if c.monitorEventCount(m.Name, time.Now().Add(-time.Hour)) >= alertPerHour {
		return alertResult{http.StatusTooManyRequests, "too many calls"}
	}
	p := map[string]any{"detail": alertDetail(body)}
	if st := bodyStatus(body); st != "" {
		p["status"] = st
	}
	c.emitMonitor(m, evMonitorReceived, p)
	return alertResult{http.StatusAccepted, "ok"}
}

// signedBy checks the body's HMAC against the address's secret, in its header.
func signedBy(m *monitor, h http.Header, body []byte) bool {
	secret, err := vaultSecret(hookSecretName(m.ID))
	if err != nil || secret == "" {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	want := hex.EncodeToString(mac.Sum(nil))
	for _, name := range strings.Split(m.Spec.SigHeader, ",") {
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
	if !monitorNameRe.MatchString(s) {
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
