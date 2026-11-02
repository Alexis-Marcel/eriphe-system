package main

import (
	"fmt"
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

// Sharing on a Mac: the checks are the shares' ones, made by the explorer
// (TestShareCmd).
func TestAppleShareCmd(t *testing.T) {
	n, root := appleShareFixture(t)
	testShareCmd(t, n, root)
	// The node's own data in the root (a Mac's ~/.eriphe in the home): what
	// holds it is never shared; what is in it, never seen.
	n.opt.root = filepath.Join(n.opt.shares["data"].Path, "Photos", "eriphe")
	os.MkdirAll(n.opt.root, 0o700)
	for _, p := range []string{"Photos", "Photos/eriphe"} {
		if out, err := shareOut(t, n, "add", filepath.Join(root, p)); err == nil {
			t.Errorf("%s: shared %s", p, out)
		}
	}
	if _, err := n.folderMounts(&Task{Spec: TaskSpec{Folders: []TaskFolder{lend(t, n, "data", "Photos", "p", false)}}}); err == nil {
		t.Error("a folder holding the node's data is mounted")
	}
}

// Folders on a Mac: checked by the explorer, mounted with container's
// --mount, Eriphe/ made by the explorer, no group to add.
func TestAppleFolderMounts(t *testing.T) {
	n, root := appleShareFixture(t)
	task := &Task{Spec: TaskSpec{Folders: []TaskFolder{
		lend(t, n, "data", "Clients/ACME", "ACME", true),
		lend(t, n, "data", "Photos", "Photos", false),
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
	readOnly := lend(t, n, "data", "Clients", "x", false)
	readOnly.Write = true
	for name, f := range map[string]TaskFolder{
		"comma":      {Name: "A, B", ID: lend(t, n, "data", "Photos", "Photos", false).ID},
		"read-only":  readOnly,
		"not shared": {Name: "x", ID: "0123456789abcdef"},
		"escape":     lend(t, n, "data", "escape", "x", false),
		"through":    lend(t, n, "data", "Clients-link", "x", false),
		"sensitive":  lend(t, n, "data", "Home", "x", false),
		"in results": lend(t, n, "data", "Clients/Beta", "x", true),
	} {
		if _, err := n.folderMounts(&Task{Spec: TaskSpec{Folders: []TaskFolder{f}}}); err == nil {
			t.Errorf("%s: mounted", name)
		} else if name == "in results" && !strings.Contains(err.Error(), "not a folder") {
			t.Errorf("in results: %v", err)
		}
	}
}

// On a Mac, ~/Library is never shared from the home root; a root inside it
// (iCloud Drive) is.
func TestPrivateDirs(t *testing.T) {
	n, root := appleShareFixture(t)
	lib := filepath.Join(root, "Library")
	cloud := filepath.Join(lib, "Mobile Documents", "Cloud")
	os.MkdirAll(filepath.Join(lib, "Messages"), 0o755)
	os.MkdirAll(filepath.Join(cloud, "Work"), 0o755)
	old := privateDirs
	privateDirs = func() []string { return []string{lib} }
	defer func() { privateDirs = old }()
	for _, apple := range []bool{true, false} {
		if !apple {
			n.opt.engine = engineDocker
		}
		n.opt.shares = shareFlag{"data": {Path: root}}
		for _, p := range []string{lib, filepath.Join(lib, "Messages"), filepath.Join(cloud, "Work")} {
			if out, err := shareOut(t, n, "add", p); err == nil {
				t.Errorf("apple %v, %s: %s", apple, p, out)
			}
		}
		n.opt.shares["cloud"] = shareRoot{Path: cloud}
		if out, err := shareOut(t, n, "add", "--name", fmt.Sprintf("work-%v", apple), filepath.Join(cloud, "Work")); err != nil {
			t.Errorf("apple %v, a root inside it: %v %s", apple, err, out)
		}
		changeShared(n.opt.root, func([]sharedFolder) ([]sharedFolder, error) { return nil, nil })
	}
}

func TestApplePrivateMarker(t *testing.T) {
	n, root := appleShareFixture(t)
	testPrivateMarker(t, n, root)
}
