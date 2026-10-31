package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"slices"
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
	// A refusal is the key's, unless it says otherwise.
	bad := func(k string, args ...any) testView {
		return testView{State: "err", Text: trf(lang, k, args...), Cause: causeAuth}
	}
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
		v := bad("test.unreachable")
		switch k := netErrorKey(err); {
		case k != "":
			v = bad(k)
		case ctx.Err() != nil || errors.Is(err, context.DeadlineExceeded):
			v = bad("test.timeout")
		default:
			v.Detail = truncate(err.Error(), 200)
		}
		v.Cause = causeNetwork // the key is not known to be wrong
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
		v := bad("ptest.quota")
		v.Cause = causeQuota
		return v
	}
	v := bad("test.status", resp.StatusCode)
	v.Cause, v.Detail = causeServer, resp.Status
	return v
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

// selfHostedDial dials a self-hosted model server: the transport's own,
// but for tests, whose fake answers on the loopback the form refuses.
var selfHostedDial func(ctx context.Context, network, addr string) (net.Conn, error)

// checkSelfHosted tries a self-hosted model server (Ollama, llama.cpp,
// vLLM, LM Studio) as OpenCode will reach it: GET <base>/models, with its
// key if it has one. It lists the models the server serves, to copy into
// the form.
func checkSelfHosted(ctx context.Context, lang, base, key string) testView {
	key = strings.TrimSpace(key)
	if checkBaseURL(base) != nil {
		return testView{State: "err", Text: tr(lang, "providers.badbaseurl"), Cause: causeNetwork}
	}
	if !selfHostedKey(key) {
		return testView{State: "err", Text: tr(lang, "providers.badkey"), Cause: causeAuth}
	}
	ctx, cancel := context.WithTimeout(ctx, testConnTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/models", nil)
	if err != nil {
		return testView{State: "err", Text: tr(lang, "providers.badbaseurl"), Cause: causeNetwork}
	}
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	t := testTransport()
	if selfHostedDial != nil {
		t.DialContext = selfHostedDial
	}
	// A redirect is not followed: the key would go along.
	client := &http.Client{Transport: t, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		v := testView{State: "err", Text: tr(lang, "ptest.selfunreachable"), Cause: causeNetwork}
		switch k := netErrorKey(err); {
		case k != "":
			v.Text = tr(lang, k)
		case ctx.Err() != nil || errors.Is(err, context.DeadlineExceeded):
			v.Text = tr(lang, "test.timeout")
		default:
			v.Detail = truncate(err.Error(), 200)
		}
		return v
	}
	return selfHostedModels(lang, key, resp)
}

// checkNodeModel tries the model server a node lends (its -model-server)
// as the tasks will reach it, through the node's tunnel: GET /v1/models,
// with the server's key if it has one.
func (c *core) checkNodeModel(ctx context.Context, lang, node, key string) testView {
	key = strings.TrimSpace(key)
	if !nodeName.MatchString(node) {
		return testView{State: "err", Text: tr(lang, "providers.nonode"), Cause: causeNetwork}
	}
	if !selfHostedKey(key) {
		return testView{State: "err", Text: tr(lang, "providers.badkey"), Cause: causeAuth}
	}
	ctx, cancel := context.WithTimeout(ctx, testConnTimeout)
	defer cancel()
	resp, err := c.nodeModels(ctx, node, key)
	switch {
	case errors.Is(err, errOffline):
		return testView{State: "err", Text: trf(lang, "ptest.nodeoffline", node), Cause: causeNetwork}
	case err != nil:
		v := testView{State: "err", Text: trf(lang, "ptest.nodeoffline", node), Cause: causeNetwork, Detail: truncate(err.Error(), 200)}
		if ctx.Err() != nil {
			v.Text = tr(lang, "test.timeout")
		}
		return v
	case resp.StatusCode == http.StatusBadGateway:
		resp.Body.Close()
		return testView{State: "err", Text: trf(lang, "ptest.nodemodeldown", node), Cause: causeServer}
	}
	return selfHostedModels(lang, key, resp)
}

// selfHostedModels reads a model server's answer to GET /v1/models: the
// models it serves, or why it does not.
func selfHostedModels(lang, key string, resp *http.Response) testView {
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		if key == "" {
			return testView{State: "err", Text: tr(lang, "ptest.selfneedskey"), Cause: causeAuth}
		}
		return testView{State: "err", Text: tr(lang, "ptest.denied"), Cause: causeAuth}
	case resp.StatusCode == http.StatusNotFound:
		return testView{State: "err", Text: tr(lang, "ptest.selfnotfound"), Cause: causeServer, Detail: resp.Status}
	case resp.StatusCode != http.StatusOK:
		return testView{State: "err", Text: trf(lang, "test.status", resp.StatusCode), Cause: causeServer, Detail: resp.Status}
	}
	var out struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out) != nil {
		return testView{State: "err", Text: tr(lang, "ptest.selfnotfound"), Cause: causeServer}
	}
	var ids []string
	for _, m := range out.Data {
		if checkModelIDs([]string{m.ID}) == nil && !slices.Contains(ids, m.ID) {
			ids = append(ids, m.ID)
		}
	}
	if len(ids) == 0 {
		return testView{State: "info", Text: tr(lang, "ptest.selfnomodels")}
	}
	const shown = 20
	v := testView{State: "ok", Text: trf(lang, "ptest.selfok", len(ids)), Tools: ids[:min(len(ids), shown)]}
	v.More = len(ids) - len(v.Tools)
	return v
}
