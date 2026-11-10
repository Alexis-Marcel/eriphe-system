package main

import (
	"strings"
	"testing"
)

// test_account signs the account in as the server uses it, says why not,
// and never repeats its secret; it is a setup tool.
func TestTestAccount(t *testing.T) {
	e := newEnv(t, rev2)
	t.Setenv("BOB_PW", "wrong-pw")
	cs := connect(t, e.cfg)
	if out := call[testAccountOut](t, cs, "test_account", map[string]any{"account": "perso"}); !out.OK || out.Error != "" {
		t.Errorf("perso: %+v", out)
	}
	out := call[testAccountOut](t, cs, "test_account", map[string]any{"account": "pro"})
	if out.OK || out.Error == "" || strings.Contains(out.Error, "wrong-pw") {
		t.Errorf("pro: %+v", out)
	}
	if out := call[testAccountOut](t, cs, "test_account", map[string]any{"account": "other"}); out.OK || !strings.Contains(out.Error, "no account") {
		t.Errorf("unknown: %+v", out)
	}
	tools, _ := cs.ListTools(t.Context(), nil)
	for _, tl := range tools.Tools {
		if (tl.Name == "test_account") != (tl.Meta["eriphe/setup"] == true) {
			t.Errorf("%s: meta %v", tl.Name, tl.Meta)
		}
	}
}
