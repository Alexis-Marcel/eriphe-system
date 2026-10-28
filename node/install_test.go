package main

import (
	"os"
	"testing"
)

// Without ERIPHE_INSTALL, the node guesses how it was installed: a node of
// an old install.ps1 in WSL is a Windows machine, not a compose project.
func TestInstallKind(t *testing.T) {
	host := func(goos string, env, files map[string]string) hostView {
		return hostView{
			goos:   goos,
			getenv: func(k string) string { return env[k] },
			readFile: func(p string) ([]byte, error) {
				if s, ok := files[p]; ok {
					return []byte(s), nil
				}
				return nil, os.ErrNotExist
			},
			exists: func(p string) bool { _, ok := files[p]; return ok },
		}
	}
	const wslKernel = "5.15.167.4-microsoft-standard-WSL2"
	for _, tc := range []struct {
		name string
		h    hostView
		want string
	}{
		{"set by the installer", host("linux", map[string]string{"ERIPHE_INSTALL": "macos"}, map[string]string{"/.dockerenv": ""}), "macos"},
		{"WSL, in a shell", host("linux", map[string]string{"WSL_DISTRO_NAME": "Ubuntu"}, nil), "windows"},
		{"WSL, as a service", host("linux", nil, map[string]string{"/proc/sys/kernel/osrelease": wslKernel}), "windows"},
		{"WSL, older kernel", host("linux", nil, map[string]string{"/proc/version": "Linux version 4.4.0-19041-Microsoft"}), "windows"},
		{"Docker Desktop on Windows", host("linux", nil, map[string]string{"/.dockerenv": "", "/proc/sys/kernel/osrelease": wslKernel}), ""},
		{"compose project on a NAS", host("linux", nil, map[string]string{"/.dockerenv": ""}), ""},
		{"podman", host("linux", nil, map[string]string{"/run/.containerenv": ""}), ""},
		{"cgroup v1 container", host("linux", nil, map[string]string{"/proc/1/cgroup": "12:pids:/docker/0123abcd"}), ""},
		{"Linux by hand", host("linux", nil, map[string]string{"/proc/sys/kernel/osrelease": "6.8.0-45-generic", "/proc/1/cgroup": "0::/init.scope"}), "linux"},
		{"a Mac without it", host("darwin", nil, nil), ""},
	} {
		if got := installKind(tc.h); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}
}
