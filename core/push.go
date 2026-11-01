package main

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"
)

// Web Push (RFC 8030, the message encryption of RFC 8291, VAPID of RFC
// 8292): eriphe-core tells the devices where the user turned notifications
// on that a task is done or that the assistant waits for their answer. The
// browser gives a subscription (its push service's endpoint and the keys
// to encrypt for it), eriphe-core keeps it, and signs each push with its
// own VAPID key, kept in its data folder and never logged.
//
// Everything goes through tell, the one channel: what it sends is skipped
// while a workspace tab is in front of the user, and a push service that
// no longer knows a subscription (404, 410) has it removed.

const pushSchema = `CREATE TABLE IF NOT EXISTS push_subscriptions (
  id TEXT PRIMARY KEY, endpoint TEXT NOT NULL UNIQUE, p256dh TEXT NOT NULL, auth TEXT NOT NULL,
  lang TEXT NOT NULL, label TEXT NOT NULL, created INTEGER NOT NULL);`

func init() { extraSchema = append(extraSchema, pushSchema) }

const (
	pushKeyFile = "push-vapid.key"
	// The contact the push services may use about our pushes (RFC 8292's
	// "sub"): the project, as no address of the user's is known here.
	pushContact = "https://github.com/Alexis-Marcel/eriphe-system"
	pushTTL     = 12 * 60 * 60 // seconds a push service keeps a push for an offline device
	maxPushSubs = 20
	// A tab says it is in front at least this often while it is.
	frontFor = 45 * time.Second
)

// pushNote is one notification, in the user's languages.
type pushNote struct {
	Title   string            // the assistant's name when empty
	Body    map[string]string // by language: fr, en
	URL     string            // a path of the workspace, opened on a click
	Tag     string            // a newer notification with the same tag replaces it
	Topic   string            // the push service keeps only the last one of a topic
	Urgent  bool              // the user is waited for
	Anyway  bool              // even with a tab in front (a test the user asked for)
	OnlyFor string            // one subscription's id; all of them when empty
	React   string            // where its 👍 / 👎 / « pas maintenant » buttons answer (notify.go)
}

// pushPayload is what the service worker receives (sw.js).
type pushPayload struct {
	Title string `json:"title"`
	Body  string `json:"body"`
	URL   string `json:"url"`
	Tag   string `json:"tag,omitempty"`
	// The buttons of a notification of the brain's, and where they answer.
	React   string       `json:"react,omitempty"`
	Actions []pushAction `json:"actions,omitempty"`
}

type pushAction struct {
	Action string `json:"action"`
	Title  string `json:"title"`
}

type pushSub struct {
	ID       string
	Endpoint string
	P256dh   string
	Auth     string
	Lang     string
	Label    string
	Created  time.Time
}

type pusher struct {
	store   *Store
	priv    string // VAPID keys, base64url without padding
	pub     string
	client  *http.Client
	now     func() time.Time
	pending sync.WaitGroup // pushes on their way (tests wait for them)

	mu   sync.Mutex
	tabs map[string]tabSeen // the workspace tabs that said where they are
}

type tabSeen struct {
	front bool
	at    time.Time
}

// newPusher reads the VAPID key of the data folder, or creates it.
func newPusher(s *Store) (*pusher, error) {
	priv, pub, err := vapidKeys(filepath.Join(s.dir, pushKeyFile))
	if err != nil {
		return nil, err
	}
	return &pusher{store: s, priv: priv, pub: pub, client: &http.Client{Timeout: 20 * time.Second},
		now: time.Now, tabs: map[string]tabSeen{}}, nil
}

// vapidKeys reads a P-256 private key (32 bytes, base64url) from file, or
// creates the file (0600) with a new one, and returns both keys as the
// Push API wants them: the public one is the uncompressed point.
func vapidKeys(file string) (priv, pub string, err error) {
	raw, err := os.ReadFile(file)
	if errors.Is(err, os.ErrNotExist) {
		key, err := ecdh.P256().GenerateKey(rand.Reader)
		if err != nil {
			return "", "", err
		}
		f, err := os.OpenFile(file, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return "", "", err
		}
		_, err = f.WriteString(base64.RawURLEncoding.EncodeToString(key.Bytes()) + "\n")
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return "", "", err
		}
		raw = []byte(base64.RawURLEncoding.EncodeToString(key.Bytes()))
	} else if err != nil {
		return "", "", err
	}
	d, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil {
		return "", "", fmt.Errorf("%s: not a key", file)
	}
	key, err := ecdh.P256().NewPrivateKey(d)
	if err != nil {
		return "", "", fmt.Errorf("%s: not a P-256 key", file)
	}
	return base64.RawURLEncoding.EncodeToString(key.Bytes()), base64.RawURLEncoding.EncodeToString(key.PublicKey().Bytes()), nil
}

// tell is the channel every notification goes through: nothing while the
// user is looking at the workspace, else a push to each device, sent in
// the background.
func (c *core) tell(n pushNote) {
	p := c.push
	if p == nil || (!n.Anyway && p.inFront()) || !p.any() {
		return
	}
	p.pending.Add(1)
	go func() {
		defer p.pending.Done()
		p.send(n)
	}()
}

// seen records where a workspace tab is: in front of the user (visible and
// focused) or not. A tab that stops saying so is not in front after a while.
func (p *pusher) seen(tab string, front bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.now()
	for id, t := range p.tabs {
		if now.Sub(t.at) > frontFor {
			delete(p.tabs, id)
		}
	}
	if _, known := p.tabs[tab]; known || len(p.tabs) < 100 {
		p.tabs[tab] = tabSeen{front: front, at: now}
	}
}

func (p *pusher) inFront() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.now()
	for _, t := range p.tabs {
		if t.front && now.Sub(t.at) <= frontFor {
			return true
		}
	}
	return false
}

func (p *pusher) any() bool {
	var n int
	p.store.db.QueryRow(`SELECT COUNT(*) FROM push_subscriptions`).Scan(&n)
	return n > 0
}

func (p *pusher) subscriptions() ([]pushSub, error) {
	rows, err := p.store.db.Query(`SELECT id, endpoint, p256dh, auth, lang, label, created FROM push_subscriptions ORDER BY created DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []pushSub
	for rows.Next() {
		var s pushSub
		var created int64
		if err := rows.Scan(&s.ID, &s.Endpoint, &s.P256dh, &s.Auth, &s.Lang, &s.Label, &created); err != nil {
			return nil, err
		}
		s.Created = time.Unix(created, 0)
		out = append(out, s)
	}
	return out, rows.Err()
}

// pushID names a subscription by its endpoint, which the page can compute
// too (to tell which one is this device) without the list showing endpoints.
func pushID(endpoint string) string {
	h := sha256.Sum256([]byte(endpoint))
	return hex.EncodeToString(h[:16])
}

var errBadSubscription = errors.New("not a push subscription")

// subscribe keeps a device's subscription, or refreshes it.
func (p *pusher) subscribe(s pushSub) (string, error) {
	u, err := url.Parse(s.Endpoint)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || len(s.Endpoint) > 1024 {
		return "", errBadSubscription
	}
	if k, err := pushKeyBytes(s.P256dh); err != nil || len(k) != 65 || k[0] != 4 {
		return "", errBadSubscription
	} else if _, err := ecdh.P256().NewPublicKey(k); err != nil {
		return "", errBadSubscription
	}
	if k, err := pushKeyBytes(s.Auth); err != nil || len(k) != 16 {
		return "", errBadSubscription
	}
	if s.Lang != "en" {
		s.Lang = "fr"
	}
	id := pushID(s.Endpoint)
	_, err = p.store.db.Exec(`INSERT INTO push_subscriptions (id, endpoint, p256dh, auth, lang, label, created) VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET p256dh = excluded.p256dh, auth = excluded.auth, lang = excluded.lang, label = excluded.label`,
		id, s.Endpoint, s.P256dh, s.Auth, s.Lang, truncate(s.Label, 80), p.now().Unix())
	if err != nil {
		return "", err
	}
	// The oldest go beyond a few devices.
	p.store.db.Exec(`DELETE FROM push_subscriptions WHERE id NOT IN (SELECT id FROM push_subscriptions ORDER BY created DESC LIMIT ?)`, maxPushSubs)
	return id, nil
}

func (p *pusher) unsubscribe(id string) bool {
	res, err := p.store.db.Exec(`DELETE FROM push_subscriptions WHERE id = ?`, id)
	if err != nil {
		return false
	}
	n, _ := res.RowsAffected()
	return n > 0
}

// pushKeyBytes reads the browser's keys: base64url, with or without padding.
func pushKeyBytes(s string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(strings.TrimRight(s, "="))
}

// send pushes a note to the subscribed devices, each in its language.
func (p *pusher) send(n pushNote) {
	subs, err := p.subscriptions()
	if err != nil {
		log.Printf("push: %v", err)
		return
	}
	for _, s := range subs {
		if n.OnlyFor != "" && s.ID != n.OnlyFor {
			continue
		}
		p.sendOne(n, s)
	}
}

func (p *pusher) sendOne(n pushNote, s pushSub) {
	payload, _ := json.Marshal(n.payload(s.Lang))
	opts := &webpush.Options{HTTPClient: p.client, Subscriber: pushContact, VAPIDPublicKey: p.pub,
		VAPIDPrivateKey: p.priv, TTL: pushTTL, Urgency: webpush.UrgencyNormal, Topic: pushTopic(n.Topic)}
	if n.Urgent {
		opts.Urgency = webpush.UrgencyHigh
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	host := pushHost(s.Endpoint)
	resp, err := webpush.SendNotificationWithContext(ctx, payload, &webpush.Subscription{Endpoint: s.Endpoint,
		Keys: webpush.Keys{P256dh: s.P256dh, Auth: s.Auth}}, opts)
	if err != nil {
		log.Printf("push: %s: %v", host, err)
		return
	}
	defer resp.Body.Close()
	answer, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	why := strings.Join(strings.Fields(truncate(string(answer), 160)), " ")
	switch {
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone:
		// The device unsubscribed, or its subscription expired.
		p.unsubscribe(s.ID)
		log.Printf("push: %s no longer knows the subscription %s (%s): removed (%s %s)", host, s.ID[:8], s.Label, resp.Status, why)
	case resp.StatusCode >= 300:
		log.Printf("push: %s answered %s %s", host, resp.Status, why)
	}
}

func (n pushNote) payload(lang string) pushPayload {
	body := n.Body[lang]
	if body == "" {
		body = n.Body["fr"]
	}
	title := n.Title
	if title == "" {
		title = assistantName()
	}
	p := pushPayload{Title: title, Body: truncate(body, 240), URL: n.URL, Tag: n.Tag, React: n.React}
	if n.React != "" {
		for _, fb := range feedbacks {
			p.Actions = append(p.Actions, pushAction{Action: fb, Title: tr(lang, "notif.fb."+fb)})
		}
	}
	return p
}

var topicChars = regexp.MustCompile(`[^A-Za-z0-9_-]`)

// pushTopic is a Topic header: at most 32 characters of base64url's alphabet.
func pushTopic(s string) string {
	s = topicChars.ReplaceAllString(s, "")
	if len(s) > 32 {
		s = s[:32]
	}
	return s
}

func pushHost(endpoint string) string {
	if u, err := url.Parse(endpoint); err == nil {
		return u.Host
	}
	return "?"
}

// The notes eriphe-core sends.

// taskTitle is a task's first line, short.
func taskTitle(prompt string) string {
	title := strings.TrimSpace(prompt)
	if i := strings.IndexByte(title, '\n'); i > 0 {
		title = title[:i]
	}
	return truncate(title, 90)
}

// pushTaskEnded tells that a worker task finished its turn, or failed.
func (c *core) pushTaskEnded(id, state string) {
	if c.push == nil || (state != stateIdle && state != stateFailed && state != stateLost) {
		return
	}
	t, err := c.store.task(id)
	if err != nil || t.Kind != "worker" {
		return
	}
	c.tell(taskEndedNote(t, state))
}

func taskEndedNote(t *TaskRow, state string) pushNote {
	title := taskTitle(t.Prompt)
	n := pushNote{URL: "/?task=" + url.QueryEscape(t.ID), Tag: "task-" + t.ID, Topic: "task-" + t.ID}
	if state == stateIdle {
		n.Body = map[string]string{"fr": "Tâche finie : " + title, "en": "Task done: " + title}
	} else {
		n.Body = map[string]string{"fr": "La tâche « " + title + " » a échoué", "en": "The task “" + title + "” failed"}
	}
	return n
}

// waitingNote tells that the assistant waits for the user's answer.
func waitingNote(short, url, tag string) pushNote {
	name := assistantName()
	return pushNote{URL: url, Tag: tag, Urgent: true, Body: map[string]string{
		"fr": withName("fr", "{{name}} attend ta réponse : ", name) + short,
		"en": name + " is waiting for your answer: " + short,
	}}
}

// asksUser tells whether a message of the assistant ends on a question to
// the user, and returns that question.
func asksUser(text string) (string, bool) {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(strings.Trim(strings.TrimSpace(lines[i]), "*_>`#- "))
		if line == "" {
			continue
		}
		if strings.Contains(line, "?") {
			return truncate(line, 140), true
		}
		return "", false
	}
	return "", false
}
