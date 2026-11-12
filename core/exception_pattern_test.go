package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// An exception named with a * holds for the tools it matches; a tool's
// own exception, then the longest pattern, comes first.
func TestPatternExceptions(t *testing.T) {
	no := false
	tool := func(name string) *mcp.Tool {
		return &mcp.Tool{Name: name, Annotations: &mcp.ToolAnnotations{OpenWorldHint: &no, DestructiveHint: &no}}
	}
	s := Server{Grid: &accessGrid{Change: gridFree}, Exceptions: map[string]string{
		"agents_*": gridNever, "agents_list_*": gridApproval, "agents_get": gridFree}}
	for name, want := range map[string]string{"creative_speech": gridFree, "agents_create": gridNever,
		"agents_list_voices": gridApproval, "agents_get": gridFree} {
		if _, v := s.baseAccess().of(tool(name)); v != want {
			t.Errorf("%s = %s, want %s", name, v, want)
		}
	}
	if lends(s, tool("agents_create")) || !lends(s, tool("creative_speech")) {
		t.Error("a pattern at Never does not leave its tools out")
	}
	if err := (Server{Exceptions: map[string]string{"agents_[": gridNever}}).checkAccess(); err == nil {
		t.Error("a broken pattern is taken")
	}
}

// On the tool's page, a tool a pattern covers shows its value; saving the
// page as it was keeps the pattern alone, a tool set otherwise takes its
// own, and following its category is a value of its own too.
func TestPatternExceptionsOnThePage(t *testing.T) {
	post := func(body string) *http.Request {
		r := httptest.NewRequest("POST", "/", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		return r
	}
	own := map[string]string{"agents_*": gridNever}
	v := toolAccess{Own: own, Tools: []toolException{{Name: "agents_create", Category: catChange, Value: gridNever},
		{Name: "agents_delete", Category: catDelete, Value: gridNever}, {Name: "agents_*", Value: gridNever}}}
	ex := formExceptions(post("exception-agents_create=never&exception-agents_delete=never&exception-agents_*=never"), accessGrid{}, v)
	if len(ex) != 1 || ex["agents_*"] != gridNever {
		t.Fatalf("unchanged = %v", ex)
	}
	ex = formExceptions(post("exception-agents_create=free&exception-agents_delete=&exception-agents_*=never"), accessGrid{Delete: gridApproval}, v)
	if len(ex) != 3 || ex["agents_create"] != gridFree || ex["agents_delete"] != gridApproval {
		t.Fatalf("changed = %v", ex)
	}
	// The pattern let go: its tools follow their category.
	ex = formExceptions(post("exception-agents_create=never&exception-agents_delete=never&exception-agents_*="), accessGrid{}, v)
	if len(ex) != 0 {
		t.Fatalf("pattern removed = %v", ex)
	}
}

// A new ElevenLabs tool starts with its voice agent tools at Never.
func TestPresetLeavesToolsOut(t *testing.T) {
	p, ok := presetByID("elevenlabs")
	if !ok || len(p.Never) == 0 || !p.Vendor() {
		t.Fatalf("preset = %+v", p)
	}
	r := httptest.NewRequest("POST", "/", nil)
	f := toolForm{Name: "elevenlabs", Use: "voix", Kind: "remote", URL: p.URL, Auth: p.Auth, Access: accessReads, Preset: p}
	s, _, err := f.server(r, nil)
	if err != nil {
		t.Fatal(err)
	}
	if s.Exceptions["agents_*"] != gridNever {
		t.Fatalf("exceptions = %v", s.Exceptions)
	}
	s.Exceptions = map[string]string{"agents_*": gridFree}
	if again, _, _ := f.server(r, &s); again.Exceptions["agents_*"] != gridFree {
		t.Fatalf("an edit put the preset's back: %v", again.Exceptions)
	}
}
