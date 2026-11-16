package a2ui

import (
	"encoding/json"
	"strings"
	"testing"
)

// A mark, as an author writes it: on the thread whose surface it is, ids
// only, the mark a literal; list and item literal ids or bound.
func TestCheckActionsMark(t *testing.T) {
	button := func(ctx string) json.RawMessage {
		return json.RawMessage(`[{"id":"root","component":"Button","label":"Garder","action":{"event":{"name":"mark","context":` + ctx + `}}}]`)
	}
	for _, ok := range []string{
		`{"thread":12,"list":"options","item":"a","mark":"keep"}`,
		`{"thread":12,"list":"options","item":{"@path":"id"},"mark":"drop"}`,
		`{"thread":12,"list":{"@path":"/list"},"item":"msg-18c3:1@x","mark":"done"}`,
		`{"thread":12,"list":"options","item":"a","mark":""}`,
	} {
		if errs := CheckActions("thread-12", 12, button(ok)); len(errs) > 0 {
			t.Errorf("%s: %+v", ok, errs)
		}
	}
	for name, bad := range map[string]string{
		"another thread":  `{"thread":13,"list":"options","item":"a","mark":"keep"}`,
		"no thread":       `{"list":"options","item":"a","mark":"keep"}`,
		"a bound thread":  `{"thread":{"@path":"/t"},"list":"options","item":"a","mark":"keep"}`,
		"another mark":    `{"thread":12,"list":"options","item":"a","mark":"star"}`,
		"a bound mark":    `{"thread":12,"list":"options","item":"a","mark":{"@path":"/m"}}`,
		"text":            `{"thread":12,"list":"options","item":"garde tout","mark":"keep"}`,
		"a path":          `{"thread":12,"list":"a/b","item":"a","mark":"keep"}`,
		"too long":        `{"thread":12,"list":"options","item":"` + strings.Repeat("a", 129) + `","mark":"keep"}`,
		"a function call": `{"thread":12,"list":"options","item":{"@call":"formatString","args":{"value":"x"}},"mark":"keep"}`,
		"more":            `{"thread":12,"list":"options","item":"a","mark":"keep","text":"x"}`,
	} {
		errs := CheckActions("thread-12", 12, button(bad))
		if len(errs) != 1 || errs[0].Path != "/components/0/action/event/context" {
			t.Errorf("%s: %+v", name, errs)
		}
	}
	if errs := CheckActions("q-x", 0, button(`{"thread":12,"list":"options","item":"a","mark":"keep"}`)); len(errs) != 1 || !strings.Contains(errs[0].Message, "thread's surface only") {
		t.Errorf("a mark on no thread's surface: %+v", errs)
	}
}
