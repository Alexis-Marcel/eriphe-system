package main

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// checkHostFolder is `eriphe-node share check`: what eriphe-launcher asks
// before it mounts a folder of the host into the node (install/README.md,
// One container). It runs in a short-lived container of this image, with
// no network, the host's / seen read-only at host, so that the folder is
// checked as it is on the host, before the node ever sees it: an absolute
// path with no symlink anywhere on the way (the bind mount is then exactly
// what was checked), nothing hidden, sensitive or private on the way (an
// Unraid share's system or domains, a .eriphe-private in a parent), a
// directory, neither holding nor inside Eriphe's own data (own: its host
// paths), and nothing sensitive inside (checkShareable).
func checkHostFolder(host, folder string, own []string) error {
	if !filepath.IsAbs(folder) || filepath.Clean(folder) != folder || folder == "/" {
		return shareErr(http.StatusBadRequest, "invalid_path", "%s: not an absolute path to a folder", folder)
	}
	for _, p := range strings.Split(strings.TrimPrefix(folder, "/"), "/") {
		if unlisted(p) {
			return shareErr(http.StatusForbidden, "sensitive", "this folder is hidden or sensitive")
		}
	}
	if unraidPrivate(folder) != "" {
		return shareErr(http.StatusForbidden, "sensitive", "this folder is hidden or sensitive")
	}
	hp := filepath.Join(host, folder)
	real, err := filepath.EvalSymlinks(hp)
	if err != nil {
		return shareErr(http.StatusNotFound, "not_found", "no such folder")
	}
	hostReal, err := filepath.EvalSymlinks(host)
	if err != nil {
		return shareErr(http.StatusServiceUnavailable, "unavailable", "the host is not there")
	}
	if real != filepath.Join(hostReal, folder) {
		return shareErr(http.StatusForbidden, "symlink", "this folder leads elsewhere through a symlink: share the folder it leads to")
	}
	if fi, err := os.Stat(real); err != nil || !fi.IsDir() {
		return shareErr(http.StatusNotFound, "not_found", "no such folder")
	}
	for d := hostReal; ; {
		if marked(d) {
			return shareErr(http.StatusForbidden, "sensitive", "this folder is hidden or sensitive")
		}
		if d == real {
			break
		}
		rel, _ := filepath.Rel(d, real)
		d = filepath.Join(d, strings.SplitN(rel, string(filepath.Separator), 2)[0])
	}
	for _, o := range own {
		if o == "" {
			continue
		}
		p := filepath.Join(hostReal, o)
		if r, err := filepath.EvalSymlinks(p); err == nil {
			p = r
		}
		if within(real, p) || within(p, real) {
			return shareErr(http.StatusForbidden, "eriphe_data", "this folder holds Eriphe's own data, or is in it")
		}
	}
	return checkShareable(real)
}
