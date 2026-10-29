package main

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strconv"
	"strings"
)

// What this machine offers besides its CPUs and runtimes: memory, named
// folders, and the images already pulled, so the scheduler can prefer it.

const maxImages = 50

// memoryBytes is the machine's total RAM, or 0 if unknown.
func memoryBytes() int64 {
	if f, err := os.Open("/proc/meminfo"); err == nil {
		defer f.Close()
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			var kb int64
			if _, err := fmt.Sscanf(sc.Text(), "MemTotal: %d kB", &kb); err == nil {
				return kb * 1024
			}
		}
		return 0
	}
	if runtime.GOOS == "darwin" {
		out, err := exec.Command("sysctl", "-n", "hw.memsize").Output()
		if err == nil {
			n, _ := strconv.ParseInt(string(bytes.TrimSpace(out)), 10, 64)
			return n
		}
	}
	return 0
}

// localImages lists the images the Docker daemon has, as repo:tag.
func localImages(ctx context.Context, docker string) []string {
	out, err := exec.CommandContext(ctx, docker, "images", "--format", "{{.Repository}}:{{.Tag}}").Output()
	if err != nil {
		return []string{}
	}
	images := []string{}
	for _, l := range strings.Split(string(out), "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.Contains(l, "<none>") || len(images) == maxImages {
			continue
		}
		images = append(images, l)
	}
	return images
}

// volumeNames are the names of the folders offered to tasks; never their paths.
func (o options) volumeNames() []string {
	names := []string{}
	for name := range o.volumes {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// volumeFlag reads repeated -volume name=/host/path flags.
type volumeFlag map[string]string

func (v volumeFlag) String() string { return "" }

func (v volumeFlag) Set(s string) error {
	for _, item := range strings.Split(s, ",") {
		if item = strings.TrimSpace(item); item == "" {
			continue
		}
		name, path, ok := strings.Cut(item, "=")
		if !ok || !validName(name) || path == "" {
			return fmt.Errorf("volume %q: want name=/host/path, the name in lowercase letters, digits, - or _", item)
		}
		v[name] = path
	}
	return nil
}
