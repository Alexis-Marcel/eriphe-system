package main

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"
)

// Channels the user adds (Settings → Access, as API accesses): any app that talks to
// Eriphe with text, through one generic endpoint (channel_in.go,
// channel_out.go), known by its token. For Eriphe a channel is text in and
// text out; what matters is what the user declares of it, once and
// visibly:
//
//   - inbound, trusted or not. Trusted: what comes through it is the user's
//     own words, as in the workspace. Not trusted (the default): anyone, or
//     anything, may have written it: an automation, someone in the room, a
//     relay. Its turn is tainted, so a write goes on a card confirmed on a
//     trusted channel, and a yes sent through it never decides a card;
//   - outbound, what it may carry: nothing, or a neutral sentence the code
//     writes, said where the user is (a speaker in their room).
//
// Home Assistant's voice, through its integration (ha/), is one of them,
// like any other app. Its former token (voice_token_hash) became the
// channel "Home Assistant", not trusted, carrying a sentence.

const channelsSchema = `CREATE TABLE IF NOT EXISTS channels (
  id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL UNIQUE COLLATE NOCASE, token_hash TEXT NOT NULL,
  trusted INTEGER NOT NULL DEFAULT 0, carries TEXT NOT NULL DEFAULT '', created INTEGER NOT NULL);
INSERT INTO channels (name, token_hash, trusted, carries, created)
  SELECT 'Home Assistant', value, 0, 'sentence', CAST(strftime('%s', 'now') AS INTEGER) FROM kv
  WHERE key = 'voice_token_hash' AND value != '' AND NOT EXISTS (SELECT 1 FROM channels WHERE token_hash = kv.value);
DELETE FROM kv WHERE key = 'voice_token_hash';`

func init() {
	extraSchema = append(extraSchema, channelsSchema)
	// Every added channel is an API access, whatever app holds its token;
	// a kind of Home Assistant's own was folded into it.
	extraColumns = append(extraColumns, `ALTER TABLE channels ADD COLUMN kind TEXT NOT NULL DEFAULT ''`,
		`UPDATE channels SET kind = 'api' WHERE kind IN ('', 'home-assistant')`)
}

const (
	maxChannels       = 20
	channelNameMax    = 40
	channelTaintPrefx = "canal:" // the taint of a message from a channel not trusted
)

// addedChannel is a channel the user added.
type addedChannel struct {
	ID      int64
	Kind    string // apiGeneric (web_access_list.go)
	Name    string
	Trusted bool
	Carries string // "" (nothing) or carrySentence
	Created time.Time
}

// A channel's name is quoted into the brain's turn: letters, digits,
// spaces and a few marks, short; never one of eriphe-core's own channels'.
func reservedChannelNames() []string {
	out := []string{firstRunVia, "Eriphe"}
	for _, k := range channelKinds {
		if k.Label != "" {
			out = append(out, k.Label)
		}
	}
	return out
}

func checkChannelName(name string) (string, error) {
	name = strings.Join(strings.Fields(notPlace.ReplaceAllString(name, " ")), " ")
	if r := []rune(name); len(r) > channelNameMax {
		name = string(r[:channelNameMax])
	}
	if name == "" {
		return "", errors.New("a channel needs a name")
	}
	if slices.ContainsFunc(reservedChannelNames(), func(r string) bool { return strings.EqualFold(r, name) }) {
		return "", fmt.Errorf("%q is a name of Eriphe's own", name)
	}
	return name, nil
}

func checkCarries(s string) (string, error) {
	switch s {
	case "", carrySentence:
		return s, nil
	}
	return "", errors.New("a channel carries nothing or a sentence")
}

func newChannelToken() (tok, hash string, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", err
	}
	tok = "ech_" + hex.EncodeToString(b)
	return tok, tokenHash(tok), nil
}

func tokenHash(tok string) string {
	sum := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(sum[:])
}

// addChannel adds a channel and returns its token, shown once.
func (c *core) addChannel(name string, trusted bool, carries string) (addedChannel, string, error) {
	var ch addedChannel
	name, err := checkChannelName(name)
	if err != nil {
		return ch, "", err
	}
	if carries, err = checkCarries(carries); err != nil {
		return ch, "", err
	}
	if len(c.addedChannels()) >= maxChannels {
		return ch, "", fmt.Errorf("at most %d channels", maxChannels)
	}
	tok, hash, err := newChannelToken()
	if err != nil {
		return ch, "", err
	}
	res, err := c.store.db.Exec(`INSERT INTO channels (kind, name, token_hash, trusted, carries, created) VALUES (?, ?, ?, ?, ?, ?)`,
		apiGeneric, name, hash, trusted, carries, time.Now().Unix())
	if err != nil {
		return ch, "", fmt.Errorf("a channel is already named %q", name)
	}
	id, _ := res.LastInsertId()
	ch, _ = c.addedChannel(id)
	return ch, tok, nil
}

// newChannelToken replaces a channel's token: the former one stops at once.
func (c *core) replaceChannelToken(id int64) (string, error) {
	tok, hash, err := newChannelToken()
	if err != nil {
		return "", err
	}
	return tok, c.updateChannel(id, `token_hash = ?`, hash)
}

// setChannel changes what the user declares of a channel.
func (c *core) setChannel(id int64, name string, trusted bool, carries string) error {
	name, err := checkChannelName(name)
	if err != nil {
		return err
	}
	if carries, err = checkCarries(carries); err != nil {
		return err
	}
	if err := c.updateChannel(id, `name = ?, trusted = ?, carries = ?`, name, trusted, carries); err != nil {
		return fmt.Errorf("a channel is already named %q", name)
	}
	return nil
}

func (c *core) removeChannel(id int64) error {
	if err := c.updateChannel(id, ""); err != nil {
		return err
	}
	c.out.forget(id)
	return nil
}

func (c *core) updateChannel(id int64, set string, args ...any) error {
	var res sql.Result
	var err error
	if set == "" {
		res, err = c.store.db.Exec(`DELETE FROM channels WHERE id = ?`, id)
	} else {
		res, err = c.store.db.Exec(`UPDATE channels SET `+set+` WHERE id = ?`, append(args, id)...)
	}
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("no channel %d", id)
	}
	return nil
}

const channelCols = `id, kind, name, trusted, carries, created`

func scanChannel(row interface{ Scan(...any) error }) (addedChannel, error) {
	var ch addedChannel
	var created int64
	err := row.Scan(&ch.ID, &ch.Kind, &ch.Name, &ch.Trusted, &ch.Carries, &created)
	ch.Created = time.Unix(created, 0)
	return ch, err
}

func (c *core) addedChannel(id int64) (addedChannel, error) {
	return scanChannel(c.store.db.QueryRow(`SELECT `+channelCols+` FROM channels WHERE id = ?`, id))
}

// addedChannels are the user's channels, oldest first.
func (c *core) addedChannels() []addedChannel {
	rows, err := c.store.db.Query(`SELECT ` + channelCols + ` FROM channels ORDER BY id`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []addedChannel
	for rows.Next() {
		if ch, err := scanChannel(rows); err == nil {
			out = append(out, ch)
		}
	}
	return out
}

// channelOf is the channel whose token the request carries.
func (c *core) channelOf(r *http.Request) (addedChannel, bool) {
	tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || tok == "" {
		return addedChannel{}, false
	}
	want := tokenHash(tok)
	rows, err := c.store.db.Query(`SELECT token_hash, ` + channelCols + ` FROM channels`)
	if err != nil {
		return addedChannel{}, false
	}
	defer rows.Close()
	for rows.Next() {
		var hash string
		var ch addedChannel
		var created int64
		if rows.Scan(&hash, &ch.ID, &ch.Kind, &ch.Name, &ch.Trusted, &ch.Carries, &created) == nil &&
			subtle.ConstantTimeCompare([]byte(hash), []byte(want)) == 1 {
			ch.Created = time.Unix(created, 0)
			return ch, true
		}
	}
	return addedChannel{}, false
}

// channelTaint is the taint of a message from a channel not trusted.
func channelTaint(name string) string { return channelTaintPrefx + name }

// fromUntrusted tells whether message seq came through a channel the user
// did not trust: maybe not their words.
func (c *core) fromUntrusted(seq int) bool {
	return strings.HasPrefix(c.store.get(taintKey(seq)), channelTaintPrefx)
}
