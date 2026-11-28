package main

import (
	"encoding/json"
	"html"
	"net/url"
	"strings"
	"testing"
)

// What the user types as a printer's address becomes the IPP address the
// server reads.
func TestIPPAddress(t *testing.T) {
	for in, want := range map[string]string{
		"192.0.2.10":                    "ipp://192.0.2.10/ipp/print",
		"192.0.2.10:631":                "ipp://192.0.2.10:631/ipp/print",
		"printer.local":                 "ipp://printer.local/ipp/print",
		"ipp://192.0.2.10/ipp/port1":    "ipp://192.0.2.10/ipp/port1",
		"ipps://192.0.2.10:443/":        "ipps://192.0.2.10:443/ipp/print",
		"ipp://[2001:db8::1]/ipp/print": "ipp://[2001:db8::1]/ipp/print",
	} {
		if got, err := ippAddress(in); err != nil || got != want {
			t.Errorf("%q: %q %v, want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"", "http://192.0.2.10/", "ipp://user@192.0.2.10/", "ipp://192.0.2.10/?x=1", "ipp://192.0.2.10:99999/", "192.0.2.10 x"} {
		if got, err := ippAddress(in); err == nil {
			t.Errorf("%q taken: %q", in, got)
		}
	}
}

// A printer is added with its address alone: its form opens on its fields
// (one kind of printer), asks no password, and the account the server
// reads names no secret. Once it runs, its row says the model the server
// found.
func TestAddPrinter(t *testing.T) {
	tw, c, csrf := filesCore(t, nil)
	_, body, _ := tw.do("GET", "/settings/tools/new?preset=printer", nil, nil)
	if !strings.Contains(body, `name="printer_url"`) || strings.Contains(body, `name="password"`) || !strings.Contains(body, `name="printer_kind" value="ipp"`) {
		t.Fatalf("form: %s", body)
	}
	form := url.Values{"preset": {"printer"}, "printer_name": {"bureau"}, "printer_url": {"192.0.2.10"}}
	if code, body, _ := tw.post("/settings/tools", url.Values{"preset": {"printer"}, "printer_url": {"http://x"}}, csrf); code != 200 || !strings.Contains(html.UnescapeString(body), "l'adresse de l'imprimante") {
		t.Fatalf("a bad address: %d %s", code, body)
	}
	code, body, h := tw.post("/settings/tools", form, csrf)
	if code != 200 || !strings.Contains(h.Get("HX-Redirect"), "saved="+printerService.Base) {
		t.Fatalf("save: %d %v %s", code, h, body)
	}
	srv := c.conf().Servers[printerService.Base]
	spec, err := decodeRun(srv.Run)
	if err != nil || !strings.HasPrefix(spec.Image, printerService.Image) {
		t.Fatalf("server %+v %v", spec, err)
	}
	if len(spec.Env) != 1 {
		t.Errorf("env %v: the configuration only", spec.Env)
	}
	var cfg struct {
		Accounts []map[string]any `json:"accounts"`
	}
	json.Unmarshal([]byte(spec.Env[toolConfigEnv].Value), &cfg)
	if len(cfg.Accounts) != 1 || cfg.Accounts[0]["name"] != "bureau" || cfg.Accounts[0]["url"] != "ipp://192.0.2.10/ipp/print" {
		t.Fatalf("accounts %v", cfg.Accounts)
	}
	if auth, _ := cfg.Accounts[0]["auth"].(map[string]any); len(auth) != 0 {
		t.Errorf("a printer names a secret: %v", auth)
	}
	if got := printerService.cases(); len(got) != 2 || got[0] != catRead || got[1] != catSend {
		t.Errorf("cases %v", got)
	}

	// Its edit: no list to change the kind of printer, no password.
	_, body, _ = tw.do("GET", "/settings/tools/"+printerService.Base+"/edit?account=bureau", nil, nil)
	if !strings.Contains(body, `value="ipp://192.0.2.10/ipp/print"`) || strings.Contains(body, "provider=choose") || strings.Contains(body, `name="password"`) {
		t.Errorf("edit: %s", body)
	}
	// Moved to another address: saved without a password.
	edit := url.Values{"preset": {"printer"}, "account": {"bureau"}, "printer_name": {"bureau"}, "printer_url": {"192.0.2.11"}}
	if code, body, h := tw.post("/settings/tools/"+printerService.Base, edit, csrf); code != 200 || h.Get("HX-Redirect") == "" {
		t.Fatalf("edit save: %d %s", code, body)
	}
	spec, _ = decodeRun(c.conf().Servers[printerService.Base].Run)
	if !strings.Contains(spec.Env[toolConfigEnv].Value, "ipp://192.0.2.11/ipp/print") {
		t.Errorf("edited: %s", spec.Env[toolConfigEnv].Value)
	}

	waitStatus(t, c.pool.get(printerService.Base), statusReady)
	_, row, _ := tw.do("GET", "/settings/tools/"+printerService.Base+"/row?account=bureau", nil, nil)
	if !strings.Contains(row, "Brother DCP-L2530DW series") {
		t.Errorf("row: %s", row)
	}
}
