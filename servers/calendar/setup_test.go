package main

import (
	"strings"
	"testing"
)

// test_account signs the account in as the server uses it, says why not,
// and never repeats its secret; it is a setup tool.
func TestTestAccount(t *testing.T) {
	e := newEnv(t)
	t.Setenv("NC_PW", "wrong-pw")
	cs := e.connect(t)
	for _, name := range []string{"icloud", "work"} {
		if out := call[testAccountOut](t, cs, "test_account", map[string]any{"account": name}); !out.OK || out.Error != "" {
			t.Errorf("%s: %+v", name, out)
		}
	}
	out := call[testAccountOut](t, cs, "test_account", map[string]any{"account": "cloud"})
	if out.OK || out.Error == "" || strings.Contains(out.Error, "wrong-pw") {
		t.Errorf("cloud: %+v", out)
	}
	tools, _ := cs.ListTools(t.Context(), nil)
	for _, tl := range tools.Tools {
		if (tl.Name == "test_account") != (tl.Meta["eriphe/setup"] == true) {
			t.Errorf("%s: meta %v", tl.Name, tl.Meta)
		}
	}
}
