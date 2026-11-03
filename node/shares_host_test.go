package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// eriphe-launcher's check of a host folder, before it is mounted into the
// node: the host's / is a temporary folder here.
func TestCheckHostFolder(t *testing.T) {
	host, _ := filepath.EvalSymlinks(t.TempDir())
	old := unraidMnt
	unraidMnt = "/mnt"
	t.Cleanup(func() { unraidMnt = old })
	for _, d := range []string{"mnt/user/1 PROJECTS/app", "mnt/user/appdata/eriphe/core", "mnt/user/system/docker",
		"mnt/user/Keys/.ssh", "mnt/user/Private/Inside", "mnt/user/Media", "mnt/user/.hidden"} {
		os.MkdirAll(filepath.Join(host, d), 0o755)
	}
	os.WriteFile(filepath.Join(host, "mnt/user/Private/.eriphe-private"), nil, 0o600)
	os.Symlink(filepath.Join(host, "mnt/user/Media"), filepath.Join(host, "mnt/user/Link"))
	os.WriteFile(filepath.Join(host, "mnt/user/file"), nil, 0o600)
	own := []string{"/mnt/user/appdata/eriphe"}
	for folder, want := range map[string]string{
		"/mnt/user/1 PROJECTS":          "",
		"/mnt/user/Media":               "",
		"/mnt/user/appdata":             "sensitive",
		"/mnt/user/appdata/eriphe/core": "sensitive",
		"/mnt/user/system":              "sensitive",
		"/mnt/user/.hidden":             "sensitive",
		"/mnt/user/Keys":                "holds_sensitive",
		"/mnt/user/Private/Inside":      "sensitive",
		"/mnt/user/Link":                "symlink",
		"/mnt/user/file":                "not_found",
		"/mnt/user/missing":             "not_found",
		"/mnt/user":                     "eriphe_data",
		"/mnt/user/../user/Media":       "invalid_path",
		"mnt/user/Media":                "invalid_path",
		"/":                             "invalid_path",
	} {
		err := checkHostFolder(host, folder, own)
		got := ""
		if err != nil {
			got = err.(*shareError).Code
		}
		if got != want {
			t.Errorf("%s: %v, want %q", folder, err, want)
		}
	}
	// Eriphe's data, wherever it is: neither in a share nor holding one.
	os.MkdirAll(filepath.Join(host, "srv/eriphe/node"), 0o755)
	for _, folder := range []string{"/srv", "/srv/eriphe/node"} {
		if err := checkHostFolder(host, folder, []string{"/srv/eriphe"}); err == nil || err.(*shareError).Code != "eriphe_data" {
			t.Errorf("%s: %v, want eriphe_data", folder, err)
		}
	}
}

func TestRunShareCheck(t *testing.T) {
	host, _ := filepath.EvalSymlinks(t.TempDir())
	os.MkdirAll(filepath.Join(host, "srv", "Docs"), 0o755)
	var errs strings.Builder
	// No node's data needed.
	if code := runShare([]string{"-root", filepath.Join(host, "none"), "check", "-host", host, "/srv/Docs"}, &errs, &errs); code != 0 {
		t.Fatalf("%d %s", code, &errs)
	}
	if code := runShare([]string{"check", "-host", host, "-data", "/srv", "/srv/Docs"}, &errs, &errs); code != 1 || !strings.Contains(errs.String(), "Eriphe's own data") {
		t.Fatalf("own data: %d %s", code, &errs)
	}
	if code := runShare([]string{"check", "-host", host}, &errs, &errs); code != 2 {
		t.Fatalf("no folder: %d", code)
	}
}

// With the one-container install, a user's own eriphe-node share add points
// to the launcher's command, which mounts the folder first.
func TestShareAddThroughLauncher(t *testing.T) {
	n, root := shareFixture(t)
	os.WriteFile(filepath.Join(n.opt.root, "node.json"), []byte("{}"), 0o600)
	t.Setenv("ERIPHE_NODE_SHARES", "data="+root)
	t.Setenv("ERIPHE_SHARE_COMMAND", "docker exec eriphe eriphe share")
	var out, errs strings.Builder
	code := runShare([]string{"-root", n.opt.root, "add", "--drop", filepath.Join(root, "1 Photos")}, &out, &errs)
	if code != 1 || !strings.Contains(errs.String(), "docker exec eriphe eriphe share add --drop '"+filepath.Join(root, "1 Photos")+"'") {
		t.Fatalf("%d %s", code, &errs)
	}
	if list, _ := loadShared(n.opt.root); len(list) != 0 {
		t.Fatalf("shared anyway: %v", list)
	}
	// list, remove, snapshots and restore work as before.
	if code := runShare([]string{"-root", n.opt.root, "list"}, &out, &errs); code != 0 {
		t.Fatalf("list: %d %s", code, &errs)
	}
	// The launcher's own call, the variable cleared.
	t.Setenv("ERIPHE_SHARE_COMMAND", "")
	if code := runShare([]string{"-root", n.opt.root, "add", filepath.Join(root, "Photos")}, &out, &errs); code != 0 {
		t.Fatalf("from the launcher: %d %s", code, &errs)
	}
}
