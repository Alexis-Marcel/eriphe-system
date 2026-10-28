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

func setup(t *testing.T, right string, scopes ...string) (*fakeDrive, *Files) {
	t.Helper()
	d := newFakeDrive(t)
	d.tree()
	t.Setenv("FILES_TEST_TOKEN", `{"client_id":"client-1","client_secret":"secret-1","refresh_token":"`+fakeRefresh+`"}`)
	if len(scopes) == 0 {
		scopes = []string{scopeDrive}
	}
	cfg := &Config{Accounts: []AccountConfig{d.account(scopes...)},
		Folders: []FolderConfig{{Name: "Compta", ID: "compta", Access: right}}}
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
	_, f := setup(t, accessRead)
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
	d, f := setup(t, accessRead)
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
	_, f := setup(t, accessRead)
	cs := session(t, f)
	_, text := call(t, cs, "search_files", map[string]any{"text": "budget"})
	var out searchOut
	json.Unmarshal([]byte(text), &out)
	got := map[string]bool{}
	for _, it := range out.Items {
		got[it.ID] = true
		if it.Folder != "Compta" {
			t.Errorf("%s: folder %q", it.ID, it.Folder)
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
	_, f := setup(t, accessRead)
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

func TestWriteOnlyIntoEriphe(t *testing.T) {
	d, f := setup(t, accessDrop)
	cs := session(t, f)
	_, text := call(t, cs, "write_file", map[string]any{"name": "résumé.md", "content": "# Résumé"})
	var out writeOut
	json.Unmarshal([]byte(text), &out)
	if out.Path != "Compta/Eriphe/résumé.md" {
		t.Fatalf("write: %s", text)
	}
	d.mu.Lock()
	dir := d.files[d.files[out.ID].Parents[0]]
	d.mu.Unlock()
	if dir.Name != resultsFolder || dir.Parents[0] != "compta" || dir.MimeType != mimeFolder {
		t.Errorf("written into %+v, want Compta/Eriphe", dir.driveFile)
	}
	if got := d.files[out.ID].content["text/markdown"]; got != "# Résumé" {
		t.Errorf("content %q", got)
	}
	// Same name: a new file next to it, never a replacement; Eriphe/ made once.
	_, text = call(t, cs, "write_file", map[string]any{"name": "résumé.md", "content": "v2", "convert": true})
	json.Unmarshal([]byte(text), &out)
	if out.Name != "résumé (2).md" || d.files[out.ID].MimeType != mimeDoc {
		t.Errorf("second write: %s", text)
	}
	// A name cannot climb out.
	_, text = call(t, cs, "write_file", map[string]any{"name": "../../Perso/x.txt", "content": "x"})
	json.Unmarshal([]byte(text), &out)
	if d.files[out.ID].Parents[0] != dir.ID || strings.Contains(out.Name, "/") {
		t.Errorf("name with a path: %s", text)
	}
	folders := 0
	d.mu.Lock()
	for _, w := range d.writes {
		if !strings.HasPrefix(w, "POST ") {
			t.Errorf("a call that is not a creation: %s", w)
		}
		if w == "POST /drive/v3/files" {
			folders++
		}
	}
	d.mu.Unlock()
	if folders != 1 {
		t.Errorf("%d folders created, want one Eriphe/", folders)
	}
}

func TestWriteRefused(t *testing.T) {
	d, f := setup(t, accessRead)
	res, text := call(t, session(t, f), "write_file", map[string]any{"name": "a.txt", "content": "x"})
	if !res.IsError || !strings.Contains(text, "read only") {
		t.Errorf("read folder: %s", text)
	}
	_, f = setup(t, accessDrop, scopeDriveReadonly)
	res, text = call(t, session(t, f), "write_file", map[string]any{"name": "a.txt", "content": "x"})
	if !res.IsError || !strings.Contains(text, "reading only") {
		t.Errorf("read-only scope: %s", text)
	}
	if len(d.writes) != 0 {
		t.Errorf("writes: %v", d.writes)
	}
}

func TestTokenRefresh(t *testing.T) {
	d, f := setup(t, accessRead)
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
	d, f = setup(t, accessRead)
	cs = session(t, f)
	call(t, cs, "list_files", nil)
	call(t, cs, "read_file", map[string]any{"id": "deep"})
	if d.refreshes != 1 {
		t.Errorf("long-lived: %d refreshes, want 1", d.refreshes)
	}

	// Revoked: the error says what to do.
	d, f = setup(t, accessRead)
	d.revoked = true
	res, text := call(t, session(t, f), "list_files", nil)
	if !res.IsError || !strings.Contains(text, "reconnect Google Drive") {
		t.Errorf("revoked: %s", text)
	}
}

func TestBrowseIsSetupOnly(t *testing.T) {
	_, f := setup(t, accessRead)
	cs := session(t, f)
	tools, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tl := range tools.Tools {
		if (tl.Name == "browse_folders") != (tl.Meta["eriphe/setup"] == true) {
			t.Errorf("%s: meta %v", tl.Name, tl.Meta)
		}
	}
	_, text := call(t, cs, "browse_folders", nil)
	if !strings.Contains(text, `"id":"root-id"`) || !strings.Contains(text, sharedWithMe) || !strings.Contains(text, `"Team"`) {
		t.Errorf("roots: %s", text)
	}
	_, text = call(t, cs, "browse_folders", map[string]any{"parent": "root-id"})
	if !strings.Contains(text, "Compta") || !strings.Contains(text, "Perso") || strings.Contains(text, "facture") {
		t.Errorf("folders only: %s", text)
	}
	off := false
	f.cfg.Setup = &off
	tools, _ = session(t, f).ListTools(context.Background(), nil)
	for _, tl := range tools.Tools {
		if tl.Name == "browse_folders" {
			t.Error("browse_folders with setup off")
		}
	}
}

func TestNoFolderYet(t *testing.T) {
	_, f := setup(t, accessRead)
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
		{`{"accounts":[` + acct + `],"folders":[{"name":"A","id":"1"}]}`, ""},
		{`{"accounts":[]}`, "no accounts"},
		{`{"accounts":[{"provider":"dropbox","auth":{"token_env":"X"}}]}`, "provider is one of"},
		{`{"accounts":[{"provider":"google","auth":{}}]}`, "exactly one"},
		{`{"accounts":[` + acct + `],"folders":[{"name":"A","id":"1","access":"delete"}]}`, "access is read or drop"},
		{`{"accounts":[` + acct + `],"folders":[{"name":"A","id":"1"},{"name":"a","id":"2"}]}`, "duplicate"},
		{`{"accounts":[` + acct + `],"folders":[{"name":"A","id":"1","account":"other"}]}`, "no account"},
		{`{"accounts":[` + acct + `],"folders":[{"name":"A"}]}`, "name and id"},
		{`{"accounts":[` + acct + `],"oops":1}`, "unknown field"},
	} {
		cfg, err := LoadConfig(c.json, "")
		switch {
		case c.err == "" && err != nil:
			t.Errorf("%s: %v", c.json, err)
		case c.err != "" && (err == nil || !strings.Contains(err.Error(), c.err)):
			t.Errorf("%s: %v, want %q", c.json, err, c.err)
		case c.err == "" && (cfg.Folders[0].Access != accessRead || cfg.Folders[0].Account != "drive"):
			t.Errorf("defaults: %+v", cfg.Folders[0])
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
