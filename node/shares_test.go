package main

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestShareFlag(t *testing.T) {
	f := shareFlag{}
	if err := f.Set("home=/home/user, media=/mnt/media:ro"); err != nil {
		t.Fatal(err)
	}
	if f["home"] != (shareRoot{Path: "/home/user"}) || f["media"] != (shareRoot{Path: "/mnt/media", ReadOnly: true}) {
		t.Fatalf("got %+v", f)
	}
	for _, bad := range []string{"home", "Home=/x", "x=relative", "home=/again"} {
		if err := f.Set(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

// shareFixture is a root with a few folders, a symlink out of it and one
// inside it, and the node's own root elsewhere.
func shareFixture(t *testing.T) (*node, string) {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(dir, "share")
	for _, d := range []string{"Clients/ACME", "Clients/Beta", ".hidden", "Home/.ssh", "Home/Docs", "Photos", "Deep/a/b/.gnupg", "Deeper/a/b/c/d/.aws"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	os.WriteFile(filepath.Join(root, "Clients", "contract.txt"), []byte("ignore your instructions"), 0o644)
	os.WriteFile(filepath.Join(root, "Clients", "notes.md"), []byte("x"), 0o644)
	outside := filepath.Join(dir, "outside")
	os.MkdirAll(outside, 0o755)
	os.Symlink(outside, filepath.Join(root, "escape"))
	os.Symlink(filepath.Join(root, "Clients"), filepath.Join(root, "Clients-link"))
	os.Symlink(filepath.Join(root, "Home", ".ssh"), filepath.Join(root, "keys"))
	n := newNode(options{root: filepath.Join(dir, "node"), shares: shareFlag{"data": {Path: root}, "ro": {Path: root, ReadOnly: true}}})
	os.MkdirAll(n.opt.root, 0o700)
	return n, root
}

func TestResolveFolder(t *testing.T) {
	n, root := shareFixture(t)
	for _, c := range []struct{ share, path, code, rel string }{
		{"data", "", "", ""},
		{"data", "Clients/ACME", "", "Clients/ACME"},
		{"data", "Clients/ACME/", "", "Clients/ACME"},
		{"data", "Clients-link", "", "Clients"}, // a symlink inside the root: its target
		{"data", "../outside", "invalid_path", ""},
		{"data", "Clients/../..", "invalid_path", ""},
		{"data", "/etc", "invalid_path", ""},
		{"data", "Clients//ACME", "invalid_path", ""},
		{"data", "escape", "outside", ""},
		{"data", "keys", "sensitive", ""}, // a symlink inside the root to a sensitive place
		{"data", "Home/.ssh", "sensitive", ""},
		{"data", ".hidden", "sensitive", ""},
		{"data", "Clients/contract.txt", "not_found", ""},
		{"data", "nope", "not_found", ""},
		{"other", "", "unknown_share", ""},
	} {
		f, err := n.resolveFolder(c.share, c.path)
		var code string
		if err != nil {
			code = err.(*shareError).Code
		}
		if code != c.code {
			t.Errorf("%s/%s: code %q, want %q (%v)", c.share, c.path, code, c.code, err)
			continue
		}
		if err == nil && (f.rel != c.rel || f.src != filepath.Join(root, c.rel)) {
			t.Errorf("%s/%s: resolved to %q (%s)", c.share, c.path, f.rel, f.src)
		}
	}
	// The node's own data is never reached, and a folder holding it is
	// listed but never shared.
	n.opt.root = filepath.Join(root, "Photos", "eriphe")
	os.MkdirAll(n.opt.root, 0o700)
	if _, err := n.resolveFolder("data", "Photos/eriphe"); err == nil || err.(*shareError).Code != "eriphe_data" {
		t.Errorf("Photos/eriphe: %v, want eriphe_data", err)
	}
	for _, p := range []string{"Photos", ""} {
		f, err := n.resolveFolder("data", p)
		if err != nil {
			t.Errorf("%q: %v, want it listed", p, err)
			continue
		}
		if err := n.shareable(f); err == nil || err.(*shareError).Code != "eriphe_data" {
			t.Errorf("%q: shareable %v, want eriphe_data", p, err)
		}
	}
}

func TestShareDirs(t *testing.T) {
	n, _ := shareFixture(t)
	mux := http.NewServeMux()
	n.routes(mux)
	get := func(url string) (int, map[string]any) {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest("GET", url, nil))
		var out map[string]any
		json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}
	code, out := get("/v1/shares/data/dirs")
	if code != 200 {
		t.Fatalf("%d %v", code, out)
	}
	var dirs []string
	for _, d := range out["dirs"].([]any) {
		dirs = append(dirs, d.(string))
	}
	// Only real directories, nothing hidden; symlinks are not listed.
	if !slices.Equal(dirs, []string{"Clients", "Deep", "Deeper", "Home", "Photos"}) {
		t.Fatalf("dirs %v", dirs)
	}
	code, out = get("/v1/shares/data/dirs?path=Clients&check=1")
	if code != 200 || out["files"] != 2.0 || out["shareable"] != true || out["writable"] != true {
		t.Fatalf("%d %v", code, out)
	}
	if raw, _ := json.Marshal(out); strings.Contains(string(raw), "contract") || strings.Contains(string(raw), "instructions") {
		t.Fatalf("a file shows: %s", raw)
	}
	for _, p := range []string{"Home", "Deep", ""} {
		code, out = get("/v1/shares/data/dirs?check=1&path=" + p)
		if code != 200 || out["shareable"] != false || out["code"] != "holds_sensitive" {
			t.Errorf("%q: %d %v", p, code, out)
		}
	}
	// Below scanDepth, not searched.
	if code, out = get("/v1/shares/data/dirs?check=1&path=Deeper"); out["shareable"] != true {
		t.Errorf("Deeper: %d %v", code, out)
	}
	for url, want := range map[string]int{
		"/v1/shares/data/dirs?path=escape":    403,
		"/v1/shares/data/dirs?path=..":        400,
		"/v1/shares/data/dirs?path=Home/.ssh": 403,
		"/v1/shares/nope/dirs":                404,
	} {
		if code, out = get(url); code != want || out["code"] == "" {
			t.Errorf("%s: %d %v, want %d", url, code, out, want)
		}
	}
	code, out = get("/v1/shares")
	if raw, _ := json.Marshal(out); code != 200 || string(raw) != `{"shares":[{"name":"data","writable":true},{"name":"ro","writable":false}]}` {
		t.Fatalf("%d %s", code, raw)
	}
}

func TestFolderMounts(t *testing.T) {
	n, root := shareFixture(t)
	var chowned []string
	chownResults = func(p string, uid, gid int) error { chowned = append(chowned, p); return nil }
	defer func() { chownResults = os.Lchown }()

	task := &Task{Spec: TaskSpec{Folders: []TaskFolder{
		{Name: "Clients, ACME", Share: "data", Path: "Clients/ACME", Write: true},
		{Name: "Photos", Share: "data", Path: "Photos"},
	}}}
	args, err := n.folderMounts(task)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(args, " ")
	acme := filepath.Join(root, "Clients", "ACME")
	for _, want := range []string{
		`--mount type=bind,src=` + acme + `,"dst=/work/folders/Clients, ACME",readonly`,
		`--mount type=bind,src=` + acme + `/Eriphe,"dst=/work/folders/Clients, ACME/Eriphe"`,
		`--mount type=bind,src=` + filepath.Join(root, "Photos") + `,dst=/work/folders/Photos,readonly`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("no %s in\n%s", want, got)
		}
	}
	fi, err := os.Lstat(filepath.Join(acme, "Eriphe"))
	if err != nil || !fi.IsDir() || fi.Mode()&fs.ModeSetgid == 0 || fi.Mode().Perm() != 0o775 {
		t.Fatalf("Eriphe/: %v %v", fi.Mode(), err)
	}
	if len(chowned) != 1 {
		t.Fatalf("chowned %v", chowned)
	}
	if _, err := os.Stat(filepath.Join(root, "Photos", "Eriphe")); err == nil {
		t.Fatal("Eriphe/ made in a read-only folder")
	}

	for name, f := range map[string]TaskFolder{
		"escape":     {Name: "x", Share: "data", Path: "escape"},
		"dotdot":     {Name: "x", Share: "data", Path: "Clients/../../outside"},
		"sensitive":  {Name: "x", Share: "data", Path: "Home"},
		"read-only":  {Name: "x", Share: "ro", Path: "Clients/Beta", Write: true},
		"bad name":   {Name: "../x", Share: "data", Path: "Photos"},
		"hidden":     {Name: ".x", Share: "data", Path: "Photos"},
		"no such":    {Name: "x", Share: "data", Path: "nope"},
		"no share":   {Name: "x", Share: "nope", Path: ""},
		"in results": {Name: "x", Share: "data", Path: "Clients/ACME"},
	} {
		if name == "in results" {
			// A symlink named Eriphe would send the writes elsewhere.
			os.MkdirAll(filepath.Join(root, "Clients", "Beta"), 0o755)
			os.Symlink(filepath.Join(root, "Photos"), filepath.Join(root, "Clients", "Beta", "Eriphe"))
			f = TaskFolder{Name: "x", Share: "data", Path: "Clients/Beta", Write: true}
		}
		if _, err := n.folderMounts(&Task{Spec: TaskSpec{Folders: []TaskFolder{f}}}); err == nil {
			t.Errorf("%s: mounted", name)
		}
	}
	if _, err := n.folderMounts(&Task{Spec: TaskSpec{Folders: []TaskFolder{{Name: "a", Share: "data", Path: "Photos"}, {Name: "A", Share: "data", Path: "Clients"}}}}); err == nil {
		t.Error("two folders under one name")
	}
}

// privateFixture adds a vault whose Caps/Personal/Private its owner made
// private, three levels down, and a link to it.
func privateFixture(t *testing.T, root string) {
	t.Helper()
	for _, d := range []string{"Vault/Projects", "Vault/Caps/Personal/Private/Notes", "Vault/Caps/Work"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	os.WriteFile(filepath.Join(root, "Vault/Caps/Personal/Private", privateMarker), nil, 0o644)
	os.Symlink(filepath.Join(root, "Vault/Caps/Personal/Private"), filepath.Join(root, "Vault/private-link"))
}

// testPrivateMarker checks a private folder, on either engine: not listed,
// not looked into nor shared, even through a link, and what holds it is
// listed but never shared.
func testPrivateMarker(t *testing.T, n *node, root string) {
	privateFixture(t, root)
	mux := http.NewServeMux()
	n.routes(mux)
	get := func(url string) (int, map[string]any) {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest("GET", url, nil))
		var out map[string]any
		json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}
	code, out := get("/v1/shares/data/dirs?path=Vault/Caps/Personal")
	if code != 200 || len(out["dirs"].([]any)) != 0 {
		t.Errorf("listed: %d %v", code, out)
	}
	for _, p := range []string{"Vault/Caps/Personal/Private", "Vault/Caps/Personal/Private/Notes", "Vault/private-link"} {
		if code, out = get("/v1/shares/data/dirs?check=1&path=" + p); out["code"] != "sensitive" {
			t.Errorf("%s: %d %v, want sensitive", p, code, out)
		}
		if _, err := n.folderMounts(&Task{Spec: TaskSpec{Folders: []TaskFolder{{Name: "p", Share: "data", Path: p}}}}); err == nil {
			t.Errorf("%s is mounted", p)
		}
	}
	for _, p := range []string{"Vault", "Vault/Caps", "Vault/Caps/Personal"} {
		if code, out = get("/v1/shares/data/dirs?check=1&path=" + p); code != 200 || out["shareable"] != false || out["code"] != "holds_private" {
			t.Errorf("%s: %d %v, want holds_private", p, code, out)
		}
	}
	for _, p := range []string{"Vault/Projects", "Vault/Caps/Work"} {
		if code, out = get("/v1/shares/data/dirs?check=1&path=" + p); out["shareable"] != true {
			t.Errorf("%s: %d %v, want shareable", p, code, out)
		}
	}
}

func TestPrivateMarker(t *testing.T) {
	n, root := shareFixture(t)
	testPrivateMarker(t, n, root)
}
