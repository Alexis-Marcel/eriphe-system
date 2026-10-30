package main

import (
	"encoding/json"
	"strings"
	"time"
)

// An AI account's real state. A key that the test refused when it was
// saved, or that its provider refused when an agent used it (a Claude
// subscription's token is only tried there), is kept on the account until
// a turn of one of its agents goes through: the account and its agents say
// so in Settings → Agents, instead of "Ready".

// providerHealth is what went wrong with an account's key; none when it works.
type providerHealth struct {
	Cause  string `json:"cause"`         // a problem's cause (problems.go)
	Raw    string `json:"raw,omitempty"` // the provider's own words
	Tested bool   `json:"tested,omitempty"`
	At     int64  `json:"at"`
}

// label is the status it shows: refused, unverified or down.
func (h providerHealth) label() string {
	switch {
	case h.Cause == causeAuth:
		return "refused"
	case h.Tested && !accountCause(h.Cause):
		return "unverified" // the test could not reach the provider
	}
	return "down"
}

func healthKey(provider string) string { return "provider_health:" + provider }

// providerHealth is what is known to be wrong with an account, or nil.
func (c *core) providerHealth(name string) *providerHealth {
	raw := c.store.get(healthKey(name))
	if raw == "" {
		return nil
	}
	var h providerHealth
	if json.Unmarshal([]byte(raw), &h) != nil || h.Cause == "" {
		return nil
	}
	return &h
}

func (c *core) setProviderHealth(name string, h providerHealth) {
	h.Raw = truncate(strings.TrimSpace(h.Raw), 600)
	if h.At == 0 {
		h.At = time.Now().Unix()
	}
	b, _ := json.Marshal(h)
	c.store.set(healthKey(name), string(b))
}

func (c *core) clearProviderHealth(name string) {
	c.store.db.Exec(`DELETE FROM kv WHERE key = ?`, healthKey(name))
}

// testedHealth keeps what a test of the key found: nothing when it passed.
func (c *core) testedHealth(name string, v testView) {
	switch v.State {
	case "ok":
		c.clearProviderHealth(name)
	case "err":
		c.setProviderHealth(name, providerHealth{Cause: v.Cause, Raw: v.Detail, Tested: true})
	}
}

// turnFailure tells whether an event says that a turn failed, with the
// raw words of the failure: a turn that ended on an error, an error a
// harness wrote as the agent's text (Claude Code's "API Error: …"), or an
// error of the node.
func turnFailure(e EventRow) (string, bool) {
	switch e.Type {
	case "turn_end":
		result, _ := e.Data["result"].(string)
		sub, _ := e.Data["subtype"].(string)
		auth, _ := e.Data["auth_error"].(bool)
		isErr, _ := e.Data["is_error"].(bool)
		if auth && classify(result) != causeAuth {
			result = strings.TrimSpace("authentication failed " + result)
		}
		if auth || isErr || strings.HasPrefix(sub, "error") || harnessError(result) {
			// A turn cut by a limit ends on the agent's last words, not on an error.
			if result == "" || strings.HasPrefix(sub, "error_max") && !auth && !isErr && !harnessError(result) {
				result = sub
			}
			return result, true
		}
	case "text":
		text, _ := e.Data["text"].(string)
		if flagged(e.Data["error"]) || harnessError(text) {
			return text, true
		}
	case "error":
		msg, _ := e.Data["message"].(string)
		return msg, msg != ""
	}
	return "", false
}

// harnessError tells a harness's own error, written where the agent's
// answer would be: Claude Code's "API Error: …", or "Failed to
// authenticate. API Error: 401 …".
func harnessError(text string) bool {
	t := strings.TrimSpace(text)
	return strings.HasPrefix(t, "API Error") || strings.HasPrefix(t, "Failed to authenticate")
}

// flagged is an event's error mark: true, or the kind of error.
func flagged(v any) bool {
	switch v := v.(type) {
	case bool:
		return v
	case string:
		return v != ""
	}
	return false
}

// noteAgentHealth keeps on a task's account what its turns say of it: an
// account error (its key, its quota, its model) marks it, a turn that went
// through clears it.
func (c *core) noteAgentHealth(t *TaskRow, events []EventRow) {
	provider := c.conf().Agents[t.Agent].Provider
	if provider == "" {
		return
	}
	for _, e := range events {
		if raw, bad := turnFailure(e); bad {
			if cause := classify(raw); accountCause(cause) {
				c.setProviderHealth(provider, providerHealth{Cause: cause, Raw: raw})
			}
		} else if e.Type == "turn_end" && e.Data["subtype"] == "success" {
			if c.providerHealth(provider) != nil {
				c.clearProviderHealth(provider)
			}
		}
	}
}
