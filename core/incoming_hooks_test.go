package main

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"testing"
)

// The public webhook over HTTP, with no session: an unknown token is not
// found, a body past alertBodyMax is refused, and a signed address takes
// only a body signed with its secret, which must be in the vault.
func TestAlertHookHTTP(t *testing.T) {
	c, _, _ := pollCore(t)
	tw := newTestWebFor(t, c)
	call := func(token string, body []byte, header map[string]string) int {
		t.Helper()
		req, _ := http.NewRequest("POST", tw.srv.URL+hookPath+token, bytes.NewReader(body))
		for k, v := range header {
			req.Header.Set(k, v)
		}
		// No cookie jar: a sender has no session.
		resp, err := tw.srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		return resp.StatusCode
	}
	body := []byte(`{"status": "firing"}`)

	if code := call("short", body, nil); code != http.StatusNotFound {
		t.Fatalf("short token: %d", code)
	}
	if code := call(strings.Repeat("x", 43), body, nil); code != http.StatusNotFound {
		t.Fatalf("unknown token: %d", code)
	}

	plain, _, err := c.newListenMonitor("nas", false, "")
	if err != nil {
		t.Fatal(err)
	}
	if code := call(plain, bytes.Repeat([]byte("a"), alertBodyMax+1), nil); code != http.StatusRequestEntityTooLarge {
		t.Fatalf("body past alertBodyMax: %d", code)
	}
	if code := call(plain, bytes.Repeat([]byte("a"), alertBodyMax), nil); code != http.StatusAccepted {
		t.Fatalf("body of alertBodyMax: %d", code)
	}

	signed, secret, err := c.newListenMonitor("deploy", true, "")
	if err != nil {
		t.Fatal(err)
	}
	if code := call(signed, body, nil); code != http.StatusUnauthorized {
		t.Fatalf("no signature: %d", code)
	}
	if code := call(signed, body, map[string]string{defaultSigHeader: sign("wrong", body)}); code != http.StatusUnauthorized {
		t.Fatalf("wrong signature: %d", code)
	}
	if code := call(signed, body, map[string]string{defaultSigHeader: sign(secret, []byte("other"))}); code != http.StatusUnauthorized {
		t.Fatalf("signature of another body: %d", code)
	}
	if code := call(signed, body, map[string]string{defaultSigHeader: sign(secret, body)}); code != http.StatusAccepted {
		t.Fatalf("signed: %d", code)
	}

	// Its secret gone from the vault, nothing passes, not even a body
	// signed with the old secret, nor one signed with an empty key.
	m, err := c.monitorNamed("deploy")
	if err != nil {
		t.Fatal(err)
	}
	currentVault().remove(hookSecretName(m.ID))
	for _, sig := range []string{sign(secret, body), sign("", body)} {
		if code := call(signed, body, map[string]string{defaultSigHeader: sig}); code != http.StatusUnauthorized {
			t.Fatalf("secret missing: %d", code)
		}
	}
}
