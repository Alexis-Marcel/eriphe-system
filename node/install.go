package main

import (
	"os"
	"runtime"
	"strings"
)

// hostView is what installKind reads of the machine, so a test can stand in for it.
type hostView struct {
	goos     string
	getenv   func(string) string
	readFile func(string) ([]byte, error)
	exists   func(string) bool
}

func thisHost() hostView {
	return hostView{
		goos:     runtime.GOOS,
		getenv:   os.Getenv,
		readFile: os.ReadFile,
		exists:   func(p string) bool { _, err := os.Stat(p); return err == nil },
	}
}

// installKind tells eriphe-core how this node was installed, so it shows the
// machine's system and the command that updates it: linux, macos or windows,
// as install.sh and install.ps1 set it in ERIPHE_INSTALL. A node installed
// before they did, or updated by copying the binary, has no ERIPHE_INSTALL:
// it is then guessed. In a container it is a compose project of one's own
// (empty); in WSL it came from install.ps1 (windows); elsewhere on Linux, from
// install.sh (linux). A Mac without it stays unknown.
func installKind(h hostView) string {
	if v := h.getenv("ERIPHE_INSTALL"); v != "" {
		return v
	}
	if h.goos != "linux" || inContainer(h) {
		return ""
	}
	if inWSL(h) {
		return "windows"
	}
	return "linux"
}

func inContainer(h hostView) bool {
	if h.exists("/.dockerenv") || h.exists("/run/.containerenv") {
		return true
	}
	cg, _ := h.readFile("/proc/1/cgroup")
	s := string(cg)
	return strings.Contains(s, "docker") || strings.Contains(s, "containerd") || strings.Contains(s, "kubepods") || strings.Contains(s, "libpod")
}

// inWSL: the variable WSL sets in a shell, or its kernel, which a service
// started by systemd sees too.
func inWSL(h hostView) bool {
	if h.getenv("WSL_DISTRO_NAME") != "" {
		return true
	}
	for _, p := range []string{"/proc/sys/kernel/osrelease", "/proc/version"} {
		if b, err := h.readFile(p); err == nil && strings.Contains(strings.ToLower(string(b)), "microsoft") {
			return true
		}
	}
	return false
}
