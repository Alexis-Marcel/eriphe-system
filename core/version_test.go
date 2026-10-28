package main

import (
	"context"
	"testing"
)

func TestParseVersion(t *testing.T) {
	for _, c := range []struct {
		in                  string
		major, minor, patch int
		pre                 int // identifiers
		build               string
		release, dev        bool
	}{
		{"0.1.0", 0, 1, 0, 0, "", true, false},
		{"v1.22.333", 1, 22, 333, 0, "", true, false},
		{"0.1.0-dev+d06ac28", 0, 1, 0, 1, "d06ac28", false, true},
		{"0.0.0-dev+abc1234", 0, 0, 0, 1, "abc1234", false, true},
		{"1.0.0-rc.1", 1, 0, 0, 2, "", false, false},
		{"1.0.0+build.5", 1, 0, 0, 0, "build.5", false, false},
	} {
		v, ok := parseVersion(c.in)
		if !ok || v.major != c.major || v.minor != c.minor || v.patch != c.patch || len(v.pre) != c.pre ||
			v.build != c.build || v.release() != c.release || v.dev() != c.dev {
			t.Errorf("parseVersion(%q) = %+v, %t", c.in, v, ok)
		}
	}
	for _, in := range []string{"", "dev", "latest", "edge", "sha-d06ac28", "1.2", "1.2.3.4", "01.2.3", "1.02.3",
		"1.2.3-", "1.2.3+", "1.2.3-a..b", "1.2.3-a_b", "1.2.x", "-1.2.3", "1.2.3 "} {
		if v, ok := parseVersion(in); ok {
			t.Errorf("parseVersion(%q) = %+v, want an error", in, v)
		}
	}
}

// semver.org's own example of precedence, and builds of main.
func TestCompareVersions(t *testing.T) {
	ordered := []string{"0.0.0-dev+aaaaaaa", "0.1.0-dev+bbbbbbb", "0.1.0", "0.1.1", "0.2.0", "1.0.0-alpha", "1.0.0-alpha.1",
		"1.0.0-alpha.beta", "1.0.0-beta", "1.0.0-beta.2", "1.0.0-beta.11", "1.0.0-rc.1", "1.0.0", "1.9.0", "1.10.0", "10.0.0"}
	for i := range ordered {
		for j := range ordered {
			a, _ := parseVersion(ordered[i])
			b, _ := parseVersion(ordered[j])
			if got, want := a.compare(b), cmpInt(i, j); got != want {
				t.Errorf("compare(%s, %s) = %d, want %d", ordered[i], ordered[j], got, want)
			}
		}
	}
	// Build metadata does not order.
	a, _ := parseVersion("0.1.0-dev+aaaaaaa")
	b, _ := parseVersion("0.1.0-dev+bbbbbbb")
	if a.compare(b) != 0 {
		t.Error("two builds of main after the same release are not equal in precedence")
	}
}

// A release older than the latest one is behind, and a build from before
// the releases; a build of main, a local one, or a newer release never is.
func TestVersionBehind(t *testing.T) {
	for _, c := range []struct {
		running, latest string
		behind          bool
	}{
		{"0.1.0", "0.2.0", true},
		{"0.1.9", "0.1.10", true},
		{"0.2.0", "0.2.0", false},
		{"0.3.0", "0.2.0", false},
		{"0.2.0-rc.1", "0.2.0", true}, // a prerelease, once its release is out
		{"0.1.0-dev+d06ac28", "0.2.0", false},
		{"0.0.0-dev+d06ac28", "0.1.0", false},
		{"dev", "0.2.0", false},
		{"", "0.2.0", false},
		{"unknown", "0.2.0", false},
		{"sha-d06ac28", "0.1.0", true}, // from before the releases
		// latest is not a release (unknown yet, or an image of before them).
		{"0.1.0", "", false},
		{"0.1.0", "sha-d06ac28", false},
		{"0.1.0", "0.2.0-dev+abc1234", false},
		{"sha-1111111", "sha-2222222", false},
	} {
		if got := versionBehind(c.running, c.latest); got != c.behind {
			t.Errorf("versionBehind(%q, %q) = %t", c.running, c.latest, got)
		}
	}
}

// A node asks for an update when it runs another published build than
// eriphe-core; a local build never does.
func TestSameVersion(t *testing.T) {
	for _, c := range []struct {
		node, core string
		same       bool
	}{
		{"0.1.0", "0.1.0", true},
		{"0.1.0", "0.2.0", false},
		{"0.2.0", "0.1.0", false},
		{"0.1.0-dev+aaaaaaa", "0.1.0-dev+bbbbbbb", false},
		{"sha-d06ac28", "0.1.0", false},
		{"dev", "0.1.0", true},
		{"0.1.0", "dev", true},
		{"", "0.1.0", true},
	} {
		if got := sameVersion(c.node, c.core); got != c.same {
			t.Errorf("sameVersion(%q, %q) = %t", c.node, c.core, got)
		}
	}
}

func TestDisplayVersion(t *testing.T) {
	for in, want := range map[string]string{
		"0.1.0":             "0.1.0",
		"0.1.0-dev+d06ac28": `0.1.0-dev <small class="mono">d06ac28</small>`,
		"1.0.0-rc.1":        "1.0.0-rc.1",
		"dev":               "dev",
		"sha-d06ac28":       "sha-d06ac28",
		"<b>":               "&lt;b&gt;",
	} {
		if got := string(displayVersion(in)); got != want {
			t.Errorf("displayVersion(%q) = %q, want %q", in, got, want)
		}
	}
}

// The registry's latest is read from its label whatever the version is;
// a machine on a build of main is never told to update, one on an older
// release is.
func TestUpdatesReleases(t *testing.T) {
	f := newFakeRegistry(t, map[string]string{"eriphe-core": "0.2.0", "eriphe-node": "0.2.0"})
	u := newUpdates(f.srv.URL)
	u.check(context.Background())
	if u.version("eriphe-core") != "0.2.0" {
		t.Fatalf("latest eriphe-core = %q", u.version("eriphe-core"))
	}
	for running, behind := range map[string]bool{"0.1.0": true, "0.2.0": false, "0.2.1": false,
		"0.2.0-dev+abc1234": false, "0.1.0-dev+abc1234": false, "sha-abc1234": true} {
		if got := u.behind("eriphe-core", running); got != behind {
			t.Errorf("behind(%s) = %t", running, got)
		}
	}
}

// A node from before the releases (sha-<commit>) gets its request without
// eriphe-core's version, which it would refuse to read.
func TestNodeUpdateRequestLegacyNode(t *testing.T) {
	c := newUpdateCore(t, "0.1.0")
	setNode(t, c, "old", `{"version":"sha-d06ac28","update":{"updater":true}}`)
	setNode(t, c, "new", `{"version":"0.0.1","update":{"updater":true}}`)
	for _, n := range []string{"old", "new"} {
		if _, err := c.requestUpdate("node:"+n, "update", false); err != nil {
			t.Fatal(err)
		}
	}
	if u := assignmentUpdate(t, c, "old"); u == nil || u.Core != "" {
		t.Errorf("old node: %+v", u)
	}
	if u := assignmentUpdate(t, c, "new"); u == nil || u.Core != "0.1.0" {
		t.Errorf("new node: %+v", u)
	}
}
