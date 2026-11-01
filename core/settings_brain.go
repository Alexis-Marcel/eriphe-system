package main

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The user's notification settings, through the brain: the device order,
// the lines turned off, the delay before the next line, the daily cap, the
// time zone and how much Telegram is told. They were only in Settings; the
// brain changes them the way it writes a rule: at once when the user asks
// in this clean turn, otherwise only proposed, for the user to approve in
// the workspace (À valider). A setting is kept in one place, the store's
// key Settings reads; a proposal waits in setting_proposals until approved
// (then applied and gone) or rejected.

const settingProposalsSchema = `CREATE TABLE IF NOT EXISTS setting_proposals (
  id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL, value TEXT NOT NULL,
  why TEXT NOT NULL DEFAULT '', source_ref TEXT NOT NULL DEFAULT '', tainted TEXT NOT NULL DEFAULT '',
  created INTEGER NOT NULL);`

func init() { extraSchema = append(extraSchema, settingProposalsSchema) }

const (
	setOrder    = "device_order"          // the lines in the user's order: phone,computer,…
	setOff      = "lines_off"             // the lines turned off, or none
	setDelay    = "move_on_after_minutes" // before a now not seen goes to the next line
	setCap      = "daily_cap"             // "now" notifications a day
	setZone     = "time_zone"             // the user's IANA zone, or default
	setTgDetail = "telegram_detail"       // full or alert
)

var settingNames = []string{setOrder, setOff, setDelay, setCap, setZone, setTgDetail}

// settingProposal is a setting the brain proposed.
type settingProposal struct {
	ID        int64
	Name      string
	Value     string
	Why       string
	SourceRef string
	Tainted   string
	Created   time.Time
}

// checkSetting validates a value and gives its stored form.
func checkSetting(name, value string) (string, error) {
	raw := strings.TrimSpace(value)
	value = strings.ToLower(raw)
	switch name {
	case setOrder:
		var order []string
		for _, s := range strings.Split(value, ",") {
			s = strings.TrimSpace(s)
			if s == "" || slices.Contains(order, s) {
				continue
			}
			if !slices.Contains(routeSteps, s) {
				return "", errors.New("device_order lists lines: inapp, computer, voice, phone, telegram")
			}
			order = append(order, s)
		}
		if len(order) == 0 {
			return "", errors.New("device_order lists at least one line")
		}
		for _, s := range routeSteps { // the lines not named keep their place after
			if !slices.Contains(order, s) {
				order = append(order, s)
			}
		}
		return strings.Join(order, ","), nil
	case setOff:
		if value == "" || value == "none" {
			return "none", nil
		}
		ch, err := checkSteps(value)
		if err != nil {
			return "", err
		}
		return ch, nil
	case setDelay:
		n, err := strconv.Atoi(value)
		if err != nil || n < 1 || n > maxDelayMinutes {
			return "", fmt.Errorf("move_on_after_minutes is 1 to %d", maxDelayMinutes)
		}
		return strconv.Itoa(n), nil
	case setCap:
		n, err := strconv.Atoi(value)
		if err != nil || n < 0 || n > maxNotifyCap {
			return "", fmt.Errorf("daily_cap is 0 to %d", maxNotifyCap)
		}
		return strconv.Itoa(n), nil
	case setZone:
		if value == "" || value == "default" {
			return "", nil
		}
		zone := raw // as written when it has capitals (Europe/Paris), else europe/paris → Europe/Paris
		if zone == value {
			zone = ianaCase(value)
		}
		if len(zone) <= 64 && !strings.HasPrefix(zone, "/") && !strings.Contains(zone, "..") && zone != "Local" {
			if _, err := time.LoadLocation(zone); err == nil {
				return zone, nil
			}
		}
		return "", errors.New("time_zone is an IANA zone like Europe/Paris, or default")
	case setTgDetail:
		if value != detailFull && value != detailAlert {
			return "", errors.New("telegram_detail is full (the text) or alert (only that something waits)")
		}
		return value, nil
	}
	return "", fmt.Errorf("name is one of %s; a notification rule about a topic or the quiet hours is rule", strings.Join(settingNames, ", "))
}

// ianaCase writes a lower-case zone as IANA mostly names it:
// america/new_york → America/New_York, utc → UTC.
func ianaCase(z string) string {
	if z == "utc" {
		return "UTC"
	}
	parts := strings.Split(z, "/")
	for i, p := range parts {
		words := strings.Split(p, "_")
		for j, w := range words {
			if w != "" {
				words[j] = strings.ToUpper(w[:1]) + w[1:]
			}
		}
		parts[i] = strings.Join(words, "_")
	}
	return strings.Join(parts, "/")
}

// settingKey is the store key a setting is kept under.
func settingKey(name string) string {
	return map[string]string{setOrder: routeOrderKey, setOff: routeOffKey, setDelay: routeDelayKey, setCap: "notify_cap",
		setZone: "notify_zone", setTgDetail: tgDetailKey}[name]
}

// applySetting writes a checked value where Settings reads it.
func (c *core) applySetting(name, value string) { c.store.set(settingKey(name), value) }

// currentSettings are the settings in force, as the brain sees them.
func (c *core) currentSettings() map[string]any {
	set := c.routeSettings()
	zone := c.store.get("notify_zone")
	if zone == "" {
		zone = "default (" + time.Local.String() + ")"
	}
	detail := detailFull
	if c.store.get(tgDetailKey) == detailAlert {
		detail = detailAlert
	}
	off := strings.Join(set.Off, ",")
	if off == "" {
		off = "none"
	}
	return map[string]any{setOrder: strings.Join(set.Order, ","), setOff: off, setDelay: int(set.Delay / time.Minute),
		setCap: c.notifyCap(), setZone: zone, setTgDetail: detail}
}

// settingRequest: wording of the user's about how and where to be told.
var settingRequest = regexp.MustCompile(`(?i)(` + ruleRequestWords +
	`|ordre|order|t[ée]l[ée]phone|phone|portable|mobile|ordi|computer|\bmac\b|\bpc\b|telegram|fuseau|time ?zone|plafond|\bcap\b|limite|d[ée]tail|r[ée]glage|setting|appareil|device)`)

type settingIn struct {
	Name      string `json:"name" jsonschema:"device_order (lines first to last: inapp, computer, voice, phone, telegram), lines_off (lines never used, or none), move_on_after_minutes (before a now not seen goes to the next line), daily_cap (now notifications a day, 0 to 20), time_zone (IANA, e.g. Europe/Paris, or default), telegram_detail (full: the text; alert: only that something waits)"`
	Value     string `json:"value" jsonschema:"the new value, as the name describes"`
	Why       string `json:"why" jsonschema:"why, in French, one sentence: what the user said, or the feedback it comes from"`
	SourceRef string `json:"source_ref,omitempty" jsonschema:"the user's message of this turn that asks for it, msg:N; without it the setting is only proposed"`
}

func registerSettingTools(s *mcp.Server, c *core) {
	tool(s, "setting", "Change one of the user's notification settings: the device order, the lines turned off, the delay before the next line, the daily cap of interruptions, the time zone, or how much Telegram is told. Applied only when the user asked for it in this turn (cite their message); otherwise only proposed, for the user to approve in the workspace (À valider). What to do about one topic, or the quiet hours, is a rule (rule). The current values are in notifications (settings).",
		func(ctx context.Context, in settingIn) (map[string]any, error) {
			return c.brainSetting(in)
		})
}

func (c *core) brainSetting(in settingIn) (map[string]any, error) {
	name := strings.ToLower(strings.TrimSpace(in.Name))
	value, err := checkSetting(name, in.Value)
	if err != nil {
		return nil, err
	}
	why := truncate(strings.TrimSpace(in.Why), notifyWhyMax)
	if why == "" {
		return nil, errors.New("why is needed")
	}
	state, reason, err := c.askedWrite(in.SourceRef, scanMemoryText(in.Value, why), settingRequest, "does not speak of how or where to be told")
	if err != nil {
		return nil, err
	}
	if state == ruleActive {
		c.applySetting(name, value)
		return map[string]any{"applied": name, "value": value, "settings": c.currentSettings()}, nil
	}
	p := settingProposal{Name: name, Value: value, Why: why, SourceRef: strings.TrimSpace(in.SourceRef), Tainted: strings.Join(c.turnTaint(), ", ")}
	id, err := c.proposeSetting(p)
	if err != nil {
		return nil, err
	}
	return map[string]any{"proposed": id, "name": name, "value": value,
		"note": "Only proposed: " + reason + ". " + c.conf().User + " approves it in the workspace (À valider). Do not say it is in force."}, nil
}

// proposeSetting keeps a proposal; it replaces the one waiting for the same setting.
func (c *core) proposeSetting(p settingProposal) (int64, error) {
	now := c.notifyTime().Unix() // before the transaction: it reads the store
	tx, err := c.store.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM setting_proposals WHERE name = ?`, p.Name); err != nil {
		return 0, err
	}
	res, err := tx.Exec(`INSERT INTO setting_proposals (name, value, why, source_ref, tainted, created) VALUES (?, ?, ?, ?, ?, ?)`,
		p.Name, p.Value, p.Why, p.SourceRef, p.Tainted, now)
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	return id, tx.Commit()
}

func (c *core) settingProposals() ([]settingProposal, error) {
	rows, err := c.store.db.Query(`SELECT id, name, value, why, source_ref, tainted, created FROM setting_proposals ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []settingProposal
	for rows.Next() {
		var p settingProposal
		var created int64
		if err := rows.Scan(&p.ID, &p.Name, &p.Value, &p.Why, &p.SourceRef, &p.Tainted, &created); err != nil {
			return nil, err
		}
		p.Created = time.Unix(created, 0)
		out = append(out, p)
	}
	return out, rows.Err()
}

// decideSetting approves (applies) or rejects a proposal: the user's say.
func (c *core) decideSetting(id int64, approve bool) error {
	var name, value string
	if err := c.store.db.QueryRow(`SELECT name, value FROM setting_proposals WHERE id = ?`, id).Scan(&name, &value); err != nil {
		return fmt.Errorf("no proposed setting %d", id)
	}
	if approve {
		// Checked again: a value is applied only in its checked form.
		checked, err := checkSetting(name, value)
		if err != nil {
			return err
		}
		c.applySetting(name, checked)
	}
	_, err := c.store.db.Exec(`DELETE FROM setting_proposals WHERE id = ?`, id)
	return err
}
