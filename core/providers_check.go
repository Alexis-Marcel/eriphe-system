package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// "Test" on a provider: the credential typed in the form (or the stored
// one) is tried once with the cheapest call the provider has, a listing of
// its models, which costs nothing and reads nothing of the account. A
// Claude subscription's token is only checked for its shape: it is meant
// for Claude Code, and is tried on the first task. Nothing is saved, and
// the credential is never logged nor shown back.

// checkEndpoints are the providers' URLs, replaced in tests.
var checkEndpoints = map[string]string{
	"anthropic-api":  "https://api.anthropic.com/v1/models?limit=1",
	"openai-api":     "https://api.openai.com/v1/models",
	"google-api":     "https://generativelanguage.googleapis.com/v1beta/models?pageSize=1",
	"mistral-api":    "https://api.mistral.ai/v1/models",
	"openrouter-api": "https://openrouter.ai/api/v1/key",
}

// misplaced tells whether a credential looks like another kind's: an API
// key in the subscription card, or the reverse. The longest known prefix
// it starts with says whose it is.
func misplaced(kind, key string) string {
	best, whose := 0, ""
	for _, k := range providerKinds {
		if k.Prefix != "" && strings.HasPrefix(key, k.Prefix) && len(k.Prefix) > best {
			best, whose = len(k.Prefix), k.ID
		}
	}
	if whose == kind {
		return ""
	}
	return whose
}

func checkProvider(ctx context.Context, lang, kind, key string) testView {
	key = strings.TrimSpace(key)
	bad := func(k string, args ...any) testView { return testView{State: "err", Text: trf(lang, k, args...)} }
	if key == "" {
		return bad("ptest.nokey")
	}
	if other := misplaced(kind, key); other != "" {
		return bad("ptest.misplaced", tr(lang, "kind."+other))
	}
	if kind == "claude-subscription" {
		if !strings.HasPrefix(key, "sk-ant-oat") || len(key) < 40 {
			return bad("ptest.notoken")
		}
		return testView{State: "info", Text: tr(lang, "ptest.subscription")}
	}
	if kind == "chatgpt-subscription" {
		return testView{State: "info", Text: tr(lang, "ptest.chatgpt")}
	}
	endpoint, ok := checkEndpoints[kind]
	if !ok {
		return bad("ptest.unknown")
	}
	ctx, cancel := context.WithTimeout(ctx, testConnTimeout)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	switch kind {
	case "anthropic-api":
		req.Header.Set("x-api-key", key)
		req.Header.Set("anthropic-version", "2023-06-01")
	case "google-api":
		req.Header.Set("x-goog-api-key", key)
	default:
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := (&http.Client{Transport: testTransport()}).Do(req)
	if err != nil {
		if k := netErrorKey(err); k != "" {
			return bad(k)
		}
		if ctx.Err() != nil || errors.Is(err, context.DeadlineExceeded) {
			return bad("test.timeout")
		}
		v := bad("test.unreachable")
		v.Detail = truncate(err.Error(), 200)
		return v
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	switch {
	case resp.StatusCode == http.StatusOK:
		return testView{State: "ok", Text: tr(lang, "ptest.ok") + credit(lang, kind, body)}
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden ||
		resp.StatusCode == http.StatusBadRequest && kind == "google-api":
		return bad("ptest.denied")
	case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusPaymentRequired:
		return bad("ptest.quota")
	}
	return bad("test.status", resp.StatusCode)
}

// credit is what OpenRouter says of the key's spending limit.
func credit(lang, kind string, body []byte) string {
	if kind != "openrouter-api" {
		return ""
	}
	var out struct {
		Data struct {
			Limit          *float64 `json:"limit"`
			LimitRemaining *float64 `json:"limit_remaining"`
		} `json:"data"`
	}
	if json.Unmarshal(body, &out) != nil || out.Data.Limit == nil || out.Data.LimitRemaining == nil {
		return ""
	}
	return " " + trf(lang, "ptest.credit", *out.Data.LimitRemaining, *out.Data.Limit)
}

// trf is tr with arguments, when there are some.
func trf(lang, key string, args ...any) string {
	if len(args) == 0 {
		return tr(lang, key)
	}
	return fmt.Sprintf(tr(lang, key), args...)
}
