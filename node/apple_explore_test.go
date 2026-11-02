package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// fakeExplorer is a container CLI that runs the explorer's script here, on
// the folder it was asked to mount, as container would in its micro-VM.
func fakeExplorer(t *testing.T) string {
	t.Helper()
	if filepath.Separator != '/' {
		t.Skip("needs sh")
	}
	cli := filepath.Join(t.TempDir(), "container")
	script := `#!/bin/sh
while [ $# -gt 0 ]; do
  case $1 in
  --mount)
    src=${2#*source=}; src=${src%%,target=*}
    tgt=${2#*target=}; tgt=${tgt%%,*}
    case $tgt in /r) ERIPHE_R=$src; export ERIPHE_R ;; /f) ERIPHE_F=$src; export ERIPHE_F ;; esac
    shift 2 ;;
  -c) script=$2; shift 2; break ;;
  *) shift ;;
  esac
done
exec sh -c "$script" "$@"
`
	if err := os.WriteFile(cli, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return cli
}

func appleShareFixture(t *testing.T) (*node, string) {
	n, root := shareFixture(t)
	n.opt.engine, n.opt.docker, n.opt.downloadLimit = engineContainer, fakeExplorer(t), 1<<20
	return n, root
}

// The explorer's listing and checks are the shares' ones (TestShareDirs).
func TestAppleShareDirs(t *testing.T) {
	n, _ := appleShareFixture(t)
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
	var dirs []string
	for _, d := range out["dirs"].([]any) {
		dirs = append(dirs, d.(string))
	}
	if code != 200 || !slices.Equal(dirs, []string{"Clients", "Deep", "Deeper", "Home", "Photos"}) {
		t.Fatalf("%d %v", code, out)
	}
	code, out = get("/v1/shares/data/dirs?path=Clients&check=1")
	if code != 200 || out["files"] != 2.0 || out["shareable"] != true || out["path"] != "Clients" {
		t.Fatalf("%d %v", code, out)
	}
	if code, out = get("/v1/shares/data/dirs?path=Clients-link"); out["path"] != "Clients" {
		t.Errorf("a symlink inside the root: %d %v", code, out)
	}
	for _, p := range []string{"Home", "Deep", ""} {
		if code, out = get("/v1/shares/data/dirs?check=1&path=" + p); out["shareable"] != false || out["code"] != "holds_sensitive" {
			t.Errorf("%q: %d %v", p, code, out)
		}
	}
	if code, out = get("/v1/shares/data/dirs?check=1&path=Deeper"); out["shareable"] != true {
		t.Errorf("Deeper: %d %v", code, out)
	}
	for url, want := range map[string]string{
		"/v1/shares/data/dirs?path=escape":    "outside",
		"/v1/shares/data/dirs?path=keys":      "sensitive", // a symlink to .ssh
		"/v1/shares/data/dirs?path=..":        "invalid_path",
		"/v1/shares/data/dirs?path=Home/.ssh": "sensitive",
		"/v1/shares/data/dirs?path=nope":      "not_found",
		"/v1/shares/nope/dirs":                "unknown_share",
	} {
		if code, out = get(url); out["code"] != want {
			t.Errorf("%s: %d %v, want %s", url, code, out, want)
		}
	}
	// The node's own data in the root (a Mac's ~/.eriphe in the home): what
	// holds it is looked into, never shared; what is in it, never seen.
	n.opt.root = filepath.Join(n.opt.shares["data"].Path, "Photos", "eriphe")
	os.MkdirAll(n.opt.root, 0o700)
	if code, out = get("/v1/shares/data/dirs?path=Photos&check=1"); code != 200 || out["shareable"] != false || out["code"] != "eriphe_data" {
		t.Errorf("holding it: %d %v", code, out)
	}
	if code, out = get("/v1/shares/data/dirs?path=Photos/eriphe"); out["code"] != "eriphe_data" {
		t.Errorf("in it: %d %v", code, out)
	}
	if _, err := n.folderMounts(&Task{Spec: TaskSpec{Folders: []TaskFolder{{Name: "p", Share: "data", Path: "Photos"}}}}); err == nil {
		t.Error("a folder holding the node's data is mounted")
	}
}

// Folders on a Mac: checked by the explorer, mounted with container's
// --mount, Eriphe/ made by the explorer, no group to add.
func TestAppleFolderMounts(t *testing.T) {
	n, root := appleShareFixture(t)
	task := &Task{Spec: TaskSpec{Folders: []TaskFolder{
		{Name: "ACME", Share: "data", Path: "Clients/ACME", Write: true},
		{Name: "Photos", Share: "data", Path: "Photos"},
	}}}
	args, err := n.folderMounts(task)
	if err != nil {
		t.Fatal(err)
	}
	acme := filepath.Join(root, "Clients", "ACME")
	want := []string{
		"--mount", "type=bind,source=" + acme + ",target=/work/folders/ACME,readonly",
		"--mount", "type=bind,source=" + acme + "/Eriphe,target=/work/folders/ACME/Eriphe",
		"--mount", "type=bind,source=" + filepath.Join(root, "Photos") + ",target=/work/folders/Photos,readonly",
	}
	if !slices.Equal(args, want) {
		t.Errorf("got\n%v\nwant\n%v", args, want)
	}
	if fi, err := os.Lstat(filepath.Join(acme, "Eriphe")); err != nil || !fi.IsDir() {
		t.Errorf("Eriphe/: %v", err)
	}
	os.MkdirAll(filepath.Join(root, "Clients", "Beta"), 0o755)
	os.Symlink(filepath.Join(root, "Photos"), filepath.Join(root, "Clients", "Beta", "Eriphe"))
	for name, f := range map[string]TaskFolder{
		"comma":      {Name: "A, B", Share: "data", Path: "Photos"},
		"read-only":  {Name: "x", Share: "ro", Path: "Clients", Write: true},
		"escape":     {Name: "x", Share: "data", Path: "escape"},
		"sensitive":  {Name: "x", Share: "data", Path: "Home"},
		"in results": {Name: "x", Share: "data", Path: "Clients/Beta", Write: true},
	} {
		if _, err := n.folderMounts(&Task{Spec: TaskSpec{Folders: []TaskFolder{f}}}); err == nil {
			t.Errorf("%s: mounted", name)
		} else if name == "in results" && !strings.Contains(err.Error(), "not a folder") {
			t.Errorf("in results: %v", err)
		}
	}
}

// On a Mac, ~/Library is never listed nor shared from the home root; a root
// inside it (iCloud Drive) is.
func TestPrivateDirs(t *testing.T) {
	n, root := appleShareFixture(t)
	lib := filepath.Join(root, "Library")
	cloud := filepath.Join(lib, "Mobile Documents", "Cloud")
	os.MkdirAll(filepath.Join(lib, "Messages"), 0o755)
	os.MkdirAll(filepath.Join(cloud, "Work"), 0o755)
	n.opt.shares["cloud"] = shareRoot{Path: cloud}
	old := privateDirs
	privateDirs = func() []string { return []string{lib} }
	defer func() { privateDirs = old }()
	for _, apple := range []bool{true, false} {
		if !apple {
			n.opt.engine = engineDocker
		}
		mux := http.NewServeMux()
		n.routes(mux)
		get := func(url string) string {
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, httptest.NewRequest("GET", url, nil))
			return rec.Body.String()
		}
		for _, p := range []string{"Library", "Library/Messages", "Library/Mobile%20Documents/Cloud"} {
			if out := get("/v1/shares/data/dirs?path=" + p); !strings.Contains(out, `"sensitive"`) {
				t.Errorf("apple %v, %s: %s", apple, p, out)
			}
		}
		if out := get("/v1/shares/cloud/dirs?path=Work"); !strings.Contains(out, `"path":"Work"`) {
			t.Errorf("apple %v, a root inside it: %s", apple, out)
		}
		if out := get("/v1/shares/data/dirs"); strings.Contains(out, "Library") {
			t.Errorf("apple %v, listed: %s", apple, out)
		}
	}
}

func TestApplePrivateMarker(t *testing.T) {
	n, root := appleShareFixture(t)
	testPrivateMarker(t, n, root)
}
