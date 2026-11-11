package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func setup(t *testing.T, scopes ...string) (*fakeDrive, *Files) {
	return setupAccount(t, false, scopes...)
}

func setupAccount(t *testing.T, readOnly bool, scopes ...string) (*fakeDrive, *Files) {
	t.Helper()
	if len(scopes) == 0 {
		scopes = []string{scopeDrive}
	}
	return setupPlaces(t, func(a *AccountConfig) { a.ReadOnly, a.Scopes = readOnly, scopes }, PlaceConfig{Name: "Compta", ID: "compta"})
}

// setupPlaces runs the fake Drive with one account, changed by edit, and
// these places.
func setupPlaces(t *testing.T, edit func(*AccountConfig), places ...PlaceConfig) (*fakeDrive, *Files) {
	t.Helper()
	d := newFakeDrive(t)
	d.tree()
	t.Setenv("FILES_TEST_TOKEN", `{"client_id":"client-1","client_secret":"secret-1","refresh_token":"`+fakeRefresh+`"}`)
	acct := d.account(scopeDrive)
	if edit != nil {
		edit(&acct)
	}
	cfg := &Config{Accounts: []AccountConfig{acct}, Places: places}
	if err := cfg.normalize(); err != nil {
		t.Fatal(err)
	}
	f, err := NewFiles(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return d, f
}

// session runs the server in process and connects a client to it.
func session(t *testing.T, f *Files) *mcp.ClientSession {
	t.Helper()
	ct, st := mcp.NewInMemoryTransports()
	ctx := context.Background()
	if _, err := newServer(f, "test").Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

func call(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) (*mcp.CallToolResult, string) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text + "\n")
		}
	}
	return res, b.String()
}

func TestOnlyTheGivenFolders(t *testing.T) {
	_, f := setup(t)
	cs := session(t, f)
	for _, c := range []struct {
		tool string
		args map[string]any
		ok   bool
		want string
	}{
		{"read_file", map[string]any{"id": "facture"}, true, "facture 42"},
		{"read_file", map[string]any{"id": "deep"}, true, "deep budget"},            // two levels down
		{"read_file", map[string]any{"id": "secret"}, false, "not in the folder"},   // elsewhere in the drive
		{"read_file", map[string]any{"id": "link"}, false, "not in the folder"},     // a shortcut to elsewhere
		{"read_file", map[string]any{"id": "nope"}, false, "not in the folder"},     // unknown
		{"list_files", map[string]any{"id": "perso"}, false, "not in the folder"},   // a folder elsewhere
		{"list_files", map[string]any{"id": "root-id"}, false, "not in the folder"}, // the parent
		{"list_files", map[string]any{"id": "sub"}, true, "deep.txt"},               // a subfolder
		{"read_file", map[string]any{"folder": "Perso", "id": "secret"}, false, "no folder"},
	} {
		res, text := call(t, cs, c.tool, c.args)
		if res.IsError == c.ok || !strings.Contains(text, c.want) {
			t.Errorf("%s %v: error=%v %q, want ok=%v with %q", c.tool, c.args, res.IsError, text, c.ok, c.want)
		}
	}
	_, text := call(t, cs, "list_files", nil)
	for _, name := range []string{"facture.txt", "Notes", "sub"} {
		if !strings.Contains(text, name) {
			t.Errorf("list_files: no %s in %s", name, text)
		}
	}
	if strings.Contains(text, "secret") {
		t.Errorf("list_files shows what is not in the folder: %s", text)
	}
}

func TestAncestryCache(t *testing.T) {
	d, f := setup(t)
	ctx := context.Background()
	fo := f.folders[0]
	if _, err := fo.item(ctx, "deep"); err != nil {
		t.Fatal(err)
	}
	// Moved out of the folder: refused once the cache has expired.
	d.mu.Lock()
	d.files["sub"].Parents = []string{"perso"}
	d.mu.Unlock()
	if _, err := fo.item(ctx, "deep"); err != nil {
		t.Fatalf("within the cache's minute: %v", err)
	}
	clear(fo.acct.cache)
	if _, err := fo.item(ctx, "deep"); !errors.Is(err, errOutside) {
		t.Fatalf("moved out: %v, want refused", err)
	}
}

func TestSearchKeepsTheFolders(t *testing.T) {
	_, f := setup(t)
	cs := session(t, f)
	_, text := call(t, cs, "search_files", map[string]any{"text": "budget"})
	var out searchOut
	json.Unmarshal([]byte(text), &out)
	got := map[string]bool{}
	for _, it := range out.Items {
		got[it.ID] = true
		if it.Place != "Compta" {
			t.Errorf("%s: place %q", it.ID, it.Place)
		}
	}
	for _, id := range []string{"facture", "notes", "deep"} {
		if !got[id] {
			t.Errorf("search: no %s in %s", id, text)
		}
	}
	if got["secret"] {
		t.Errorf("search found a file outside the folder: %s", text)
	}
}

func TestExportGoogleDocuments(t *testing.T) {
	_, f := setup(t)
	cs := session(t, f)
	_, text := call(t, cs, "read_file", map[string]any{"id": "notes"})
	if !strings.Contains(text, untrustedStart+"\n# Notes\n\nbudget 2026\n"+untrustedEnd) || !strings.Contains(text, `"mime_type":"text/markdown"`) {
		t.Errorf("doc as markdown: %s", text)
	}
	_, text = call(t, cs, "read_file", map[string]any{"id": "notes", "format": "text"})
	if !strings.Contains(text, "Notes budget 2026") {
		t.Errorf("doc as text: %s", text)
	}
	res, _ := call(t, cs, "read_file", map[string]any{"id": "notes", "format": "pdf"})
	if r, ok := res.Content[len(res.Content)-1].(*mcp.EmbeddedResource); !ok || r.Resource.MIMEType != "application/pdf" || string(r.Resource.Blob) != "%PDF-1.7 notes" {
		t.Errorf("doc as pdf: %+v", res.Content)
	}
	_, text = call(t, cs, "read_file", map[string]any{"id": "sheet"})
	if !strings.Contains(text, "a,b\n1,2") || !strings.Contains(text, "first sheet") {
		t.Errorf("sheet as csv: %s", text)
	}
	res, text = call(t, cs, "read_file", map[string]any{"id": "notes", "format": "csv"})
	if !res.IsError || !strings.Contains(text, "markdown, text, pdf") {
		t.Errorf("doc as csv: %s", text)
	}
	_, text = call(t, cs, "read_file", map[string]any{"id": "notes", "max_chars": 3})
	if !strings.Contains(text, `"truncated":true`) || !strings.Contains(text, untrustedStart+"\n# N\n") {
		t.Errorf("max_chars: %s", text)
	}
}

func TestWriteInTheFolder(t *testing.T) {
	d, f := setup(t)
	cs := session(t, f)
	_, text := call(t, cs, "write_file", map[string]any{"name": "résumé.md", "content": "# Résumé"})
	var out writeOut
	json.Unmarshal([]byte(text), &out)
	if out.Folder != "Compta" || d.files[out.ID].Parents[0] != "compta" {
		t.Fatalf("write: %s", text)
	}
	if got := d.files[out.ID].content["text/markdown"]; got != "# Résumé" {
		t.Errorf("content %q", got)
	}
	// Same name: a new file next to it, never a replacement.
	_, text = call(t, cs, "write_file", map[string]any{"name": "résumé.md", "content": "v2", "convert": true})
	json.Unmarshal([]byte(text), &out)
	if out.Name != "résumé (2).md" || d.files[out.ID].MimeType != mimeDoc {
		t.Errorf("second write: %s", text)
	}
	// In a subfolder, never outside the folder.
	_, text = call(t, cs, "write_file", map[string]any{"name": "x.txt", "content": "x", "parent": "sub"})
	json.Unmarshal([]byte(text), &out)
	if d.files[out.ID].Parents[0] != "sub" {
		t.Errorf("in a subfolder: %s", text)
	}
	for _, parent := range []string{"perso", "root-id", "facture"} {
		if res, text := call(t, cs, "write_file", map[string]any{"name": "x.txt", "content": "x", "parent": parent}); !res.IsError {
			t.Errorf("parent %s: %s", parent, text)
		}
	}
	// A name cannot climb out.
	_, text = call(t, cs, "write_file", map[string]any{"name": "../../Perso/x.txt", "content": "x"})
	json.Unmarshal([]byte(text), &out)
	if d.files[out.ID].Parents[0] != "compta" || strings.Contains(out.Name, "/") {
		t.Errorf("name with a path: %s", text)
	}
}

func TestUpdateFile(t *testing.T) {
	d, f := setup(t)
	cs := session(t, f)
	for _, c := range []struct {
		id, content, mime string
	}{
		{"facture", "facture 43", "text/plain"},
		{"notes", "# Notes v2", "text/markdown"},
		{"sheet", "a,b\n3,4", "text/csv"},
	} {
		res, text := call(t, cs, "update_file", map[string]any{"id": c.id, "content": c.content})
		if res.IsError {
			t.Fatalf("update %s: %s", c.id, text)
		}
		d.mu.Lock()
		got := d.files[c.id].uploaded
		d.mu.Unlock()
		if got != c.mime+" "+c.content {
			t.Errorf("update %s: uploaded %q", c.id, got)
		}
	}
	for _, c := range []struct{ id, want string }{
		{"secret", "not in the folder"},
		{"compta", "folder given to Eriphe itself"},
		{"sub", "folder, not a file"},
	} {
		if res, text := call(t, cs, "update_file", map[string]any{"id": c.id, "content": "x"}); !res.IsError || !strings.Contains(text, c.want) {
			t.Errorf("update %s: %s, want %q", c.id, text, c.want)
		}
	}
}

func TestTrashFile(t *testing.T) {
	d, f := setup(t)
	cs := session(t, f)
	if res, text := call(t, cs, "trash_file", map[string]any{"id": "deep"}); res.IsError || !strings.Contains(text, "30 days") {
		t.Fatalf("trash: %s", text)
	}
	d.mu.Lock()
	trashed := d.files["deep"].Trashed
	d.mu.Unlock()
	if !trashed {
		t.Error("not in the trash")
	}
	if res, _ := call(t, cs, "read_file", map[string]any{"id": "deep"}); !res.IsError {
		t.Error("a trashed file is still read")
	}
	for _, id := range []string{"compta", "secret", "root-id"} {
		if res, text := call(t, cs, "trash_file", map[string]any{"id": id}); !res.IsError {
			t.Errorf("trash %s: %s", id, text)
		}
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, w := range d.writes {
		if strings.Contains(w, "DELETE") {
			t.Errorf("deleted for good: %s", w)
		}
	}
}

func TestChangeRefused(t *testing.T) {
	for _, c := range []struct {
		readOnly bool
		scope    string
		want     string
	}{
		{true, scopeDrive, "read only for Eriphe"},
		{false, scopeDriveReadonly, "reading only"},
	} {
		d, f := setupAccount(t, c.readOnly, c.scope)
		cs := session(t, f)
		for _, call1 := range []struct {
			tool string
			args map[string]any
		}{
			{"write_file", map[string]any{"name": "a.txt", "content": "x"}},
			{"update_file", map[string]any{"id": "facture", "content": "x"}},
			{"trash_file", map[string]any{"id": "facture"}},
		} {
			if res, text := call(t, cs, call1.tool, call1.args); !res.IsError || !strings.Contains(text, c.want) {
				t.Errorf("%s (read only %v, %s): %s", call1.tool, c.readOnly, c.scope, text)
			}
		}
		if len(d.writes) != 0 {
			t.Errorf("writes: %v", d.writes)
		}
	}
}

func TestTokenRefresh(t *testing.T) {
	d, f := setup(t)
	d.expiresIn = 1 // within oauth2's expiry delta: every request refreshes
	cs := session(t, f)
	for range 2 {
		if res, text := call(t, cs, "read_file", map[string]any{"id": "facture"}); res.IsError {
			t.Fatal(text)
		}
	}
	if d.refreshes < 2 || d.refreshed[0] != fakeRefresh {
		t.Errorf("short-lived: %d refreshes with %v", d.refreshes, d.refreshed)
	}

	// A token still valid is reused.
	d, f = setup(t)
	cs = session(t, f)
	call(t, cs, "list_files", nil)
	call(t, cs, "read_file", map[string]any{"id": "deep"})
	if d.refreshes != 1 {
		t.Errorf("long-lived: %d refreshes, want 1", d.refreshes)
	}

	// Revoked: the error says what to do.
	d, f = setup(t)
	d.revoked = true
	res, text := call(t, session(t, f), "list_files", nil)
	if !res.IsError || !strings.Contains(text, "reconnect Google Drive") {
		t.Errorf("revoked: %s", text)
	}
}

// The lent token says where the provider refreshes it; the account's own
// token_url (tests) comes first, and Google's is the default.
func TestTokenURLFromTheToken(t *testing.T) {
	d, _ := setup(t)
	acc := d.account(scopeDrive)
	acc.TokenURL = ""
	t.Setenv("FILES_TEST_TOKEN", `{"client_id":"client-1","client_secret":"secret-1","refresh_token":"`+fakeRefresh+`","token_url":"`+d.srv.URL+`/token"}`)
	cfg := &Config{Accounts: []AccountConfig{acc}, Places: []PlaceConfig{{Name: "Compta", ID: "compta"}}}
	if err := cfg.normalize(); err != nil {
		t.Fatal(err)
	}
	f, err := NewFiles(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if res, text := call(t, session(t, f), "list_files", nil); res.IsError || d.refreshes != 1 {
		t.Fatalf("refreshed %d times: %s", d.refreshes, text)
	}
	if c, _ := acc.credentials(); cmpOr(acc.TokenURL, c.TokenURL, googleToken) != d.srv.URL+"/token" {
		t.Error("the token's token_url")
	}
	t.Setenv("FILES_TEST_TOKEN", `{"client_id":"client-1","refresh_token":"r"}`)
	if c, _ := acc.credentials(); cmpOr(acc.TokenURL, c.TokenURL, googleToken) != googleToken {
		t.Error("Google by default")
	}
}

func TestSetupToolsAreSetupOnly(t *testing.T) {
	_, f := setup(t)
	cs := session(t, f)
	tools, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tl := range tools.Tools {
		if (tl.Name == "browse_places" || tl.Name == "test_account") != (tl.Meta["eriphe/setup"] == true) {
			t.Errorf("%s: meta %v", tl.Name, tl.Meta)
		}
	}
	_, text := call(t, cs, "browse_places", nil)
	if !strings.Contains(text, `"id":"root-id"`) || !strings.Contains(text, sharedWithMe) || !strings.Contains(text, `"Team"`) {
		t.Errorf("roots: %s", text)
	}
	if strings.Contains(text, `"parent"`) {
		t.Errorf("a top place with a parent: %s", text)
	}
	_, text = call(t, cs, "browse_places", map[string]any{"parent": "root-id"})
	if !strings.Contains(text, "Compta") || !strings.Contains(text, "Perso") || strings.Contains(text, "facture") {
		t.Errorf("folders only: %s", text)
	}
	var out browseOut
	json.Unmarshal([]byte(text), &out)
	for _, p := range out.Places {
		if p.Parent != "root-id" {
			t.Errorf("%s: parent %q, want root-id", p.Name, p.Parent)
		}
	}
	_, text = call(t, cs, "browse_places", map[string]any{"parent": "teams"})
	if !strings.Contains(text, `"id":"backend"`) || !strings.Contains(text, `"parent":"teams"`) {
		t.Errorf("a subfolder's parent: %s", text)
	}
	off := false
	f.cfg.Setup = &off
	tools, _ = session(t, f).ListTools(context.Background(), nil)
	for _, tl := range tools.Tools {
		if tl.Name == "browse_places" || tl.Name == "test_account" {
			t.Errorf("%s with setup off", tl.Name)
		}
	}
}

func TestNoFolderYet(t *testing.T) {
	_, f := setup(t)
	f.folders = nil
	res, text := call(t, session(t, f), "list_files", nil)
	if !res.IsError || !strings.Contains(text, "no folder was given") {
		t.Errorf("%s", text)
	}
}

func TestDocxText(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("word/document.xml")
	w.Write([]byte(`<w:document xmlns:w="x"><w:body><w:p><w:r><w:t>Bonjour</w:t></w:r><w:r><w:tab/><w:t xml:space="preserve"> le monde</w:t></w:r></w:p><w:p><w:r><w:t>Fin</w:t></w:r></w:p></w:body></w:document>`))
	zw.Close()
	got, err := docxText(buf.Bytes())
	if err != nil || got != "Bonjour\t le monde\nFin" {
		t.Errorf("%q %v", got, err)
	}
	if _, err := docxText([]byte("nope")); err == nil {
		t.Error("not a zip")
	}
}

func TestConfig(t *testing.T) {
	acct := `{"name":"drive","provider":"google","auth":{"token_env":"X"}}`
	for _, c := range []struct {
		json, err string
	}{
		{`{"accounts":[` + acct + `],"places":[{"name":"A","id":"1"}]}`, ""},
		{`{"accounts":[` + acct + `],"places":[{"name":"A","id":"1","parents":["top","mid"]}]}`, ""},
		{`{"accounts":[{"name":"drive","provider":"google","auth":{"token_env":"X"},"all":true}],"places":[{"name":"A","id":"1"}]}`, ""},
		{`{"accounts":[{"name":"drive","provider":"google","auth":{"token_env":"X"},"all":true}],"places":[{"name":"A","id":"1"},{"name":"Drive","id":"2"}]}`, "duplicate"},
		{`{"accounts":[]}`, "no accounts"},
		{`{"accounts":[{"provider":"dropbox","auth":{"token_env":"X"}}]}`, "provider is one of"},
		{`{"accounts":[{"provider":"google","auth":{}}]}`, "exactly one"},
		{`{"accounts":[` + acct + `],"places":[{"name":"A","id":"1","access":"read"}]}`, "unknown field"},
		{`{"accounts":[` + acct + `],"places":[{"name":"A","id":"1"},{"name":"a","id":"2"}]}`, "duplicate"},
		{`{"accounts":[` + acct + `],"places":[{"name":"A","id":"1","account":"other"}]}`, "no account"},
		{`{"accounts":[` + acct + `],"places":[{"name":"A"}]}`, "name and id"},
		{`{"accounts":[` + acct + `],"oops":1}`, "unknown field"},
	} {
		cfg, err := LoadConfig(c.json, "")
		switch {
		case c.err == "" && err != nil:
			t.Errorf("%s: %v", c.json, err)
		case c.err != "" && (err == nil || !strings.Contains(err.Error(), c.err)):
			t.Errorf("%s: %v, want %q", c.json, err, c.err)
		case c.err == "" && cfg.Places[0].Account != "drive":
			t.Errorf("defaults: %+v", cfg.Places[0])
		}
	}
}

func TestCleanName(t *testing.T) {
	for in, want := range map[string]string{"a/b.txt": "a-b.txt", "..": "", " x\n.md ": "x-.md", "../../etc": "-..-etc"} {
		if got := cleanName(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}

// test_account signs the account in as the server uses it, and says why
// not: a revoked token.
func TestTestAccount(t *testing.T) {
	_, f := setup(t)
	if _, text := call(t, session(t, f), "test_account", map[string]any{"account": ""}); !strings.Contains(text, `"ok":true`) {
		t.Errorf("ok: %s", text)
	}
	d, f := setup(t)
	d.revoked = true
	_, text := call(t, session(t, f), "test_account", nil)
	if !strings.Contains(text, `"ok":false`) || !strings.Contains(text, "reconnect") {
		t.Errorf("revoked: %s", text)
	}
}

// nested gives Engineering and Backend, which is inside it; all gives the
// whole account too.
func nested(t *testing.T, all bool) (*fakeDrive, *Files) {
	return setupPlaces(t, func(a *AccountConfig) { a.All = all },
		PlaceConfig{Name: "Engineering", ID: "eng", Parents: []string{"root-id"}},
		PlaceConfig{Name: "Backend", ID: "backend", Parents: []string{"root-id", "eng", "teams"}})
}

type check struct {
	tool string
	args map[string]any
	ok   bool
	want string
}

func runChecks(t *testing.T, cs *mcp.ClientSession, checks []check) {
	t.Helper()
	for _, c := range checks {
		res, text := call(t, cs, c.tool, c.args)
		if res.IsError == c.ok || !strings.Contains(text, c.want) {
			t.Errorf("%s %v: error=%v %q, want ok=%v with %q", c.tool, c.args, res.IsError, text, c.ok, c.want)
		}
	}
}

const inBackend = `this item is in folder "Backend": name that folder`

func TestWholeAccount(t *testing.T) {
	d, f := nested(t, true)
	cs := session(t, f)
	_, text := call(t, cs, "list_folders", nil)
	if !strings.Contains(text, `{"account":"drive","all":true,"name":"drive"}`) {
		t.Errorf("list_folders: %s", text)
	}
	runChecks(t, cs, []check{
		{"read_file", map[string]any{"folder": "drive", "id": "secret"}, true, "secret budget"},  // anywhere in the drive
		{"read_file", map[string]any{"folder": "drive", "id": "facture"}, true, "facture 42"},    // in a folder not given
		{"read_file", map[string]any{"folder": "drive", "id": "shared"}, true, "roadmap shared"}, // shared with the user
		{"read_file", map[string]any{"folder": "drive", "id": "link"}, true, "secret budget"},    // a shortcut, by its target
		{"read_file", map[string]any{"folder": "drive", "id": "nope"}, false, "not in the folder"},
		{"read_file", map[string]any{"folder": "drive", "id": "plan"}, false, `this item is in folder "Engineering"`},
		{"read_file", map[string]any{"folder": "drive", "id": "api"}, false, inBackend},
		{"list_files", map[string]any{"folder": "drive", "id": "perso"}, true, "secret.txt"},
		{"list_files", map[string]any{"folder": "drive", "id": sharedWithMe}, true, "shared.txt"},
		{"list_files", map[string]any{"folder": "drive", "id": "eng"}, false, `this item is in folder "Engineering"`},
		{"trash_file", map[string]any{"folder": "drive", "id": "root-id"}, false, "folder given to Eriphe itself"},
		{"trash_file", map[string]any{"folder": "drive", "id": "root"}, false, "folder given to Eriphe itself"},
		{"trash_file", map[string]any{"folder": "drive", "id": sharedWithMe}, false, "folder given to Eriphe itself"},
		{"trash_file", map[string]any{"folder": "drive", "id": "eng"}, false, `this item is in folder "Engineering"`},
		{"write_file", map[string]any{"folder": "drive", "parent": sharedWithMe, "name": "x.txt", "content": "x"}, false, "Shared with me"},
	})
	// Its top level: the user's own drive, Shared with me, the shared drives.
	_, text = call(t, cs, "list_files", map[string]any{"folder": "drive"})
	for _, want := range []string{`"id":"root-id"`, `"id":"` + sharedWithMe + `"`, `"Team"`, `"place":"drive"`} {
		if !strings.Contains(text, want) {
			t.Errorf("top level: no %s in %s", want, text)
		}
	}
	// Written at its top level: in the user's own drive.
	_, text = call(t, cs, "write_file", map[string]any{"folder": "drive", "name": "top.txt", "content": "x"})
	var out writeOut
	json.Unmarshal([]byte(text), &out)
	if f := d.files[out.ID]; f == nil || f.Parents[0] != "root-id" {
		t.Errorf("write at the top: %s", text)
	}
	// Search without a folder searches the whole account too.
	_, text = call(t, cs, "search_files", map[string]any{"text": "budget"})
	if !strings.Contains(text, `"id":"secret"`) || !strings.Contains(text, `"place":"drive"`) {
		t.Errorf("search: %s", text)
	}

	// The account not given whole: only its places, and no place named after it.
	_, f = nested(t, false)
	cs = session(t, f)
	runChecks(t, cs, []check{
		{"read_file", map[string]any{"folder": "Engineering", "id": "secret"}, false, "not in the folder"},
		{"read_file", map[string]any{"folder": "Engineering", "id": "shared"}, false, "not in the folder"},
		{"read_file", map[string]any{"folder": "drive", "id": "secret"}, false, "no folder"},
	})
	_, text = call(t, cs, "search_files", map[string]any{"text": "budget"})
	if strings.Contains(text, `"id":"secret"`) || strings.Contains(text, `"id":"facture"`) {
		t.Errorf("search outside the places: %s", text)
	}
}

func TestDeepestPlaceDecides(t *testing.T) {
	d, f := nested(t, false)
	cs := session(t, f)
	runChecks(t, cs, []check{
		{"read_file", map[string]any{"folder": "Engineering", "id": "plan"}, true, "roadmap plan"},
		{"read_file", map[string]any{"folder": "Engineering", "id": "api"}, false, inBackend},
		{"read_file", map[string]any{"folder": "Engineering", "id": "main"}, false, inBackend}, // two levels into Backend
		{"read_file", map[string]any{"folder": "Engineering", "id": "link-api"}, false, inBackend},
		{"read_file", map[string]any{"folder": "Backend", "id": "api"}, true, "roadmap api"},
		{"read_file", map[string]any{"folder": "Backend", "id": "main"}, true, "roadmap main"},
		{"read_file", map[string]any{"folder": "Backend", "id": "plan"}, false, "not in the folder"},
		{"list_files", map[string]any{"folder": "Engineering", "id": "teams"}, true, "Backend"},
		{"list_files", map[string]any{"folder": "Engineering", "id": "backend"}, false, inBackend},
		{"list_files", map[string]any{"folder": "Engineering", "id": "srv"}, false, inBackend},
		{"list_files", map[string]any{"folder": "Backend", "id": "srv"}, true, "main.txt"},
		{"write_file", map[string]any{"folder": "Engineering", "parent": "srv", "name": "x.txt", "content": "x"}, false, inBackend},
		{"write_file", map[string]any{"folder": "Engineering", "parent": "backend", "name": "x.txt", "content": "x"}, false, inBackend},
		{"update_file", map[string]any{"folder": "Engineering", "id": "api", "content": "x"}, false, inBackend},
		{"trash_file", map[string]any{"folder": "Engineering", "id": "api"}, false, inBackend},
		{"trash_file", map[string]any{"folder": "Engineering", "id": "backend"}, false, inBackend},
		{"trash_file", map[string]any{"folder": "Backend", "id": "backend"}, false, "folder given to Eriphe itself"},
		// A folder holding a nested place is not trashed through its parent.
		{"trash_file", map[string]any{"folder": "Engineering", "id": "teams"}, false, `holds folder "Backend"`},
	})
	_, text := call(t, cs, "write_file", map[string]any{"folder": "Backend", "parent": "srv", "name": "x.txt", "content": "x"})
	var out writeOut
	json.Unmarshal([]byte(text), &out)
	if f := d.files[out.ID]; f == nil || f.Parents[0] != "srv" || out.Folder != "Backend" {
		t.Errorf("write in Backend: %s", text)
	}
	if res, text := call(t, cs, "trash_file", map[string]any{"folder": "Backend", "id": "api"}); res.IsError {
		t.Errorf("trash in Backend: %s", text)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.files["api"].Trashed || d.files["teams"].Trashed || d.files["backend"].Trashed {
		t.Error("trashed the wrong items")
	}
}

func TestPlacesInListingsAndSearch(t *testing.T) {
	_, f := nested(t, true)
	cs := session(t, f)
	for _, c := range []struct {
		tool string
		args map[string]any
		want map[string]string // id: its place; "" for not there
	}{
		{"list_files", map[string]any{"folder": "Engineering"}, map[string]string{"plan": "Engineering", "teams": "Engineering", "link-api": "Engineering"}},
		{"list_files", map[string]any{"folder": "Engineering", "id": "teams"}, map[string]string{"backend": "Backend"}},
		{"list_files", map[string]any{"folder": "Backend"}, map[string]string{"api": "Backend", "srv": "Backend"}},
		{"list_files", map[string]any{"folder": "drive", "id": "root-id"}, map[string]string{"eng": "Engineering", "perso": "drive", "compta": "drive"}},
		{"search_files", map[string]any{"text": "roadmap"}, map[string]string{"plan": "Engineering", "api": "Backend", "main": "Backend", "shared": "drive"}},
		{"search_files", map[string]any{"folder": "Engineering", "text": "roadmap"}, map[string]string{"plan": "Engineering", "api": "Backend", "main": "Backend", "shared": ""}},
		{"search_files", map[string]any{"folder": "Backend", "text": "roadmap"}, map[string]string{"api": "Backend", "main": "Backend", "plan": ""}},
		{"search_files", map[string]any{"folder": "Backend", "text": "Backend"}, map[string]string{"backend": ""}}, // never the folder itself
	} {
		res, text := call(t, cs, c.tool, c.args)
		if res.IsError {
			t.Errorf("%s %v: %s", c.tool, c.args, text)
			continue
		}
		var out struct {
			Items []placedItem `json:"items"`
		}
		if err := json.Unmarshal([]byte(text), &out); err != nil {
			t.Fatalf("%v: %s", err, text)
		}
		got := map[string]string{}
		for _, it := range out.Items {
			got[it.ID] = it.Place
		}
		for id, want := range c.want {
			if got[id] != want {
				t.Errorf("%s %v: %s in %q, want %q", c.tool, c.args, id, got[id], want)
			}
		}
	}
}
