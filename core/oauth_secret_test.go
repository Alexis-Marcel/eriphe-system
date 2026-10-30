package main

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
)

// A server that takes confidential clients only (Miro, Supabase, Hugging
// Face): Eriphe registers with a secret method it advertises, keeps the
// secret it gets, and sends it to the token endpoint, for the code, the
// refresh and the next connect.
func TestOAuthSecretOnlyServer(t *testing.T) {
	for _, tc := range []struct {
		name       string
		methods    []string
		omit       bool
		registered []string // the methods asked, in order
		auth       string
	}{
		{"post", []string{"client_secret_post", "client_secret_basic"}, false, []string{"client_secret_post"}, "post"},
		{"basic", []string{"client_secret_basic"}, false, []string{"client_secret_basic"}, "basic"},
		{"unadvertised", nil, true, []string{"none", "client_secret_basic"}, "basic"},
		// Its metadata says "none" but its registration refuses it (Miro's two documents).
		{"misadvertised", []string{"none"}, false, []string{"none", "client_secret_basic"}, "basic"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeAS(t)
			f.secretOnly, f.methods, f.omitMethod = true, tc.methods, tc.omit
			f.firstExpiry = 1 // the first use refreshes, with the secret
			store, err := openStore(filepath.Join(t.TempDir(), "core.db"))
			if err != nil {
				t.Fatal(err)
			}
			c, stop := oauthCore(t, store, f)
			defer stop()
			u := c.pool.get("notion")
			waitStatus(t, u, statusNeedsAuth)
			if done := connectFlow(t, c, f, "http://127.0.0.1:8976/callback"); done["status"] != statusReady {
				t.Fatalf("after connect: %v", done)
			}
			f.mu.Lock()
			regs := fmt.Sprint(f.regMethods)
			f.mu.Unlock()
			if regs != fmt.Sprint(tc.registered) {
				t.Fatalf("registered with %s, want %v", regs, tc.registered)
			}
			if reg := store.oauthClient("notion"); reg == nil || reg.ClientSecret != "secret-1" {
				t.Fatalf("stored client = %+v", reg)
			}
			if res, err := u.call(context.Background(), "search", nil); err != nil || res.IsError {
				t.Fatalf("call after a refresh = %+v, %v", res, err)
			}
			if _, tok, _ := store.oauthToken("notion"); tok == nil || tok.RefreshToken != "r2" {
				t.Fatalf("not refreshed: %+v", tok)
			}
			f.mu.Lock()
			auth := f.tokenAuth
			f.mu.Unlock()
			if auth != tc.auth {
				t.Fatalf("token endpoint authenticated by %s, want %s", auth, tc.auth)
			}
			// Connecting again reuses the client and its secret.
			if done := connectFlow(t, c, f, "http://127.0.0.1:8976/callback"); done["status"] != statusReady {
				t.Fatalf("second connect: %v", done)
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.registrations != 1 || f.tokenAuth == "none" {
				t.Fatalf("registrations %d, token auth %s", f.registrations, f.tokenAuth)
			}
		})
	}
}

// Scopes are read as reads and writes by their names.
func TestSplitScopes(t *testing.T) {
	for _, tc := range []struct {
		found []string
		read  string // "" not scoped
	}{
		{[]string{"read", "write"}, "[read]"},
		{[]string{"files.metadata.read", "file_requests.read", "files.content.write"}, "[files.metadata.read file_requests.read]"},
		{[]string{"read", "offline_access", "transactions:request", "budgets:manage"}, "[read offline_access]"},
		{[]string{"generate", "gamma:read"}, "[gamma:read]"},
		{[]string{"data:read_write"}, ""},
		{[]string{"default"}, ""},
		{[]string{"profile:read", "conversations:read"}, ""},
		{nil, ""},
	} {
		set, ok := splitScopes(tc.found)
		got := ""
		if ok {
			got = fmt.Sprint(set.read)
		}
		if got != tc.read {
			t.Errorf("splitScopes(%v) = %s, want %s", tc.found, got, tc.read)
		}
	}
}
