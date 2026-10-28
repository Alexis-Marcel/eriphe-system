package main

import (
	"html/template"
	"strconv"
	"strings"
)

// Versions (README, Versions). A release is X.Y.Z, from the Git tag vX.Y.Z;
// a build of main is X.Y.Z-dev+<commit>, X.Y.Z being the last release; a
// local build is "dev". Builds from before the releases were sha-<commit>:
// older than any release.

// semver is a version as semver.org has it; build metadata (after the +)
// never orders.
type semver struct {
	major, minor, patch int
	pre                 []string // the prerelease identifiers (after the -)
	build               string
}

// parseVersion reads X.Y.Z[-pre][+build], with or without a leading v.
func parseVersion(s string) (semver, bool) {
	s = strings.TrimPrefix(s, "v")
	var v semver
	s, build, hasBuild := strings.Cut(s, "+")
	v.build = build
	core, pre, hasPre := strings.Cut(s, "-")
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return semver{}, false
	}
	nums := [3]*int{&v.major, &v.minor, &v.patch}
	for i, p := range parts {
		n, ok := numeric(p)
		if !ok {
			return semver{}, false
		}
		*nums[i] = n
	}
	if hasPre {
		v.pre = strings.Split(pre, ".")
		for _, id := range v.pre {
			if !validIdent(id) {
				return semver{}, false
			}
		}
	}
	if hasBuild {
		for _, id := range strings.Split(v.build, ".") {
			if !validIdent(id) {
				return semver{}, false
			}
		}
	}
	return v, true
}

// numeric reads a number without leading zeros.
func numeric(s string) (int, bool) {
	if s == "" || len(s) > 1 && s[0] == '0' {
		return 0, false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, false
		}
	}
	n, err := strconv.Atoi(s)
	return n, err == nil
}

func validIdent(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '-') {
			return false
		}
	}
	return true
}

// release tells whether it is a release (no prerelease, no build).
func (v semver) release() bool { return len(v.pre) == 0 && v.build == "" }

// dev tells whether it is a build of main (X.Y.Z-dev+<commit>).
func (v semver) dev() bool { return len(v.pre) > 0 && v.pre[0] == "dev" }

// compare is semver's precedence: -1, 0 or 1. A prerelease comes before its
// release; build metadata is ignored.
func (v semver) compare(w semver) int {
	for _, d := range [][2]int{{v.major, w.major}, {v.minor, w.minor}, {v.patch, w.patch}} {
		if d[0] != d[1] {
			return cmpInt(d[0], d[1])
		}
	}
	switch {
	case len(v.pre) == 0 && len(w.pre) == 0:
		return 0
	case len(v.pre) == 0:
		return 1
	case len(w.pre) == 0:
		return -1
	}
	for i := 0; i < len(v.pre) && i < len(w.pre); i++ {
		a, b := v.pre[i], w.pre[i]
		an, aNum := numeric(a)
		bn, bNum := numeric(b)
		switch {
		case aNum && bNum:
			if an != bn {
				return cmpInt(an, bn)
			}
		case aNum:
			return -1 // numbers before words
		case bNum:
			return 1
		case a != b:
			return strings.Compare(a, b)
		}
	}
	return cmpInt(len(v.pre), len(w.pre))
}

func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// legacyVersion tells whether a version is a build from before the
// releases (sha-<commit>).
func legacyVersion(s string) bool { return strings.HasPrefix(s, "sha-") }

// publishedVersion tells whether a version comes from CI (a release, a
// build of main, or a build from before the releases), not a local build.
func publishedVersion(s string) bool {
	_, ok := parseVersion(s)
	return ok || legacyVersion(s)
}

// versionBehind tells whether a build is older than the latest release. A
// build of main (or a local one) never is: it is someone testing; a build
// from before the releases always is.
func versionBehind(running, latest string) bool {
	l, ok := parseVersion(latest)
	if !ok || !l.release() {
		return false
	}
	if legacyVersion(running) {
		return true
	}
	r, ok := parseVersion(running)
	if !ok || r.dev() {
		return false
	}
	return r.compare(l) < 0
}

// displayVersion is a version for the workspace: 0.1.0 for a release;
// 0.1.0-dev and its commit in small for a build of main.
func displayVersion(s string) template.HTML {
	v, ok := parseVersion(s)
	if !ok || v.build == "" {
		return template.HTML(template.HTMLEscapeString(s))
	}
	base, _, _ := strings.Cut(strings.TrimPrefix(s, "v"), "+")
	return template.HTML(template.HTMLEscapeString(base) + ` <small class="mono">` + template.HTMLEscapeString(v.build) + `</small>`)
}
