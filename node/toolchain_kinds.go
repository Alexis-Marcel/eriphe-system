package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// The toolchains the node detects (toolchain.go). Another language is one
// more toolchainKind: what file names its version, where its releases and
// their checksums are, and what a task gets of it.

// goToolchain: go.mod's toolchain line, or else its go line; the releases
// of go.dev/dl, whose JSON index gives each archive's SHA-256.
type goToolchain struct{}

func (goToolchain) kind() string { return "go" }

// goVersionRe is a Go version as go.mod writes it: 1.25, 1.25.7, 1.26rc1.
var goVersionRe = regexp.MustCompile(`^1\.([0-9]{1,3})(?:\.([0-9]{1,3}))?(?:(rc|beta)([0-9]{1,2}))?$`)

func (goToolchain) detect(read func(string) ([]byte, bool)) (string, string) {
	b, ok := read("go.mod")
	if !ok {
		return "", ""
	}
	var goLine, toolchain string
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		line, _, _ := strings.Cut(sc.Text(), "//")
		f := strings.Fields(line)
		if len(f) != 2 {
			continue
		}
		switch f[0] {
		case "go":
			if goVersionRe.MatchString(f[1]) {
				goLine = f[1]
			}
		case "toolchain":
			if v, ok := strings.CutPrefix(f[1], "go"); ok && goVersionRe.MatchString(v) {
				toolchain = v
			}
		}
	}
	// The go command runs the newer of the two (GOTOOLCHAIN=auto).
	if toolchain != "" && (goLine == "" || goVersionLess(goLine, toolchain)) {
		return toolchain, "go.mod"
	}
	return goLine, "go.mod"
}

// goVersionKey orders Go versions: 1.25 < 1.25rc1 < 1.25.0 < 1.25.1 is
// how go.mod reads them (a bare 1.N is the language, before its release
// candidates).
func goVersionKey(v string) [4]int {
	m := goVersionRe.FindStringSubmatch(v)
	if m == nil {
		return [4]int{}
	}
	minor, _ := strconv.Atoi(m[1])
	k := [4]int{minor, -1, 0, 0}
	switch {
	case m[2] != "":
		patch, _ := strconv.Atoi(m[2])
		k[1], k[2] = 3, patch
	case m[3] == "beta":
		k[1] = 1
	case m[3] == "rc":
		k[1] = 2
	}
	if m[4] != "" {
		k[3], _ = strconv.Atoi(m[4])
	}
	return k
}

func goVersionLess(a, b string) bool {
	ka, kb := goVersionKey(a), goVersionKey(b)
	for i := range ka {
		if ka[i] != kb[i] {
			return ka[i] < kb[i]
		}
	}
	return false
}

// goExact: 1.25.7 or 1.26rc1 names one release; 1.25 is the newest 1.25.x.
func goExact(want string) bool {
	m := goVersionRe.FindStringSubmatch(want)
	return m != nil && (m[2] != "" || m[3] != "")
}

func (goToolchain) exact(want, arch string) (string, bool) {
	if !goExact(want) {
		return "", false
	}
	return "go" + want + "-linux-" + arch, true
}

type goRelease struct {
	Version string `json:"version"`
	Stable  bool   `json:"stable"`
	Files   []struct {
		Filename string `json:"filename"`
		OS       string `json:"os"`
		Arch     string `json:"arch"`
		SHA256   string `json:"sha256"`
		Kind     string `json:"kind"`
	} `json:"files"`
}

func (goToolchain) resolve(ctx context.Context, s *toolchainStore, want, arch string) (toolchainRelease, error) {
	if !goVersionRe.MatchString(want) {
		return toolchainRelease{}, fmt.Errorf("bad Go version %q", want)
	}
	data, err := s.index(ctx, s.goDL+"?mode=json&include=all")
	if err != nil {
		return toolchainRelease{}, err
	}
	var releases []goRelease
	if err := json.Unmarshal(data, &releases); err != nil {
		return toolchainRelease{}, fmt.Errorf("go.dev's index: %w", err)
	}
	var best *goRelease
	var bestV string
	for i := range releases {
		r := &releases[i]
		v, ok := strings.CutPrefix(r.Version, "go")
		if !ok || !goVersionRe.MatchString(v) {
			continue
		}
		if goExact(want) {
			if v == want {
				best, bestV = r, v
				break
			}
			continue
		}
		// 1.25: the newest stable 1.25.x.
		if r.Stable && strings.HasPrefix(v, want+".") && (best == nil || goVersionLess(bestV, v)) {
			best, bestV = r, v
		}
	}
	if best == nil {
		return toolchainRelease{}, fmt.Errorf("no Go release %s", want)
	}
	for _, f := range best.Files {
		if f.OS == "linux" && f.Arch == arch && f.Kind == "archive" && strings.HasSuffix(f.Filename, ".tar.gz") && !strings.Contains(f.Filename, "/") {
			return toolchainRelease{Name: "go" + bestV + "-linux-" + arch, URL: s.goDL + f.Filename, SHA256: f.SHA256, Top: "go"}, nil
		}
	}
	return toolchainRelease{}, fmt.Errorf("no Go %s archive for linux/%s", bestV, arch)
}

func (goToolchain) env(dir string) ([]string, map[string]string) {
	return []string{dir + "/bin"}, nil
}

// nodeToolchain: .nvmrc or .node-version, or else package.json's
// engines.node; the releases of nodejs.org/dist, whose index lists them
// and whose SHASUMS256.txt gives each archive's SHA-256.
type nodeToolchain struct{}

func (nodeToolchain) kind() string { return "node" }

var nodeWantRe = regexp.MustCompile(`^[0-9A-Za-z.*^~<>=|/ \t-]{1,100}$`)

func (nodeToolchain) detect(read func(string) ([]byte, bool)) (string, string) {
	for _, name := range []string{".nvmrc", ".node-version"} {
		b, ok := read(name)
		if !ok {
			continue
		}
		for _, line := range strings.Split(string(b), "\n") {
			line, _, _ = strings.Cut(line, "#")
			if line = strings.TrimSpace(line); line != "" {
				if nodeWantRe.MatchString(line) {
					return line, name
				}
				break
			}
		}
	}
	b, ok := read("package.json")
	if !ok {
		return "", ""
	}
	var pkg struct {
		Engines map[string]any `json:"engines"`
	}
	if json.Unmarshal(b, &pkg) != nil {
		return "", ""
	}
	if v, ok := pkg.Engines["node"].(string); ok {
		if v = strings.TrimSpace(v); nodeWantRe.MatchString(v) {
			return v, "package.json"
		}
	}
	return "", ""
}

func nodeArch(arch string) string {
	if arch == "amd64" {
		return "x64"
	}
	return arch
}

var nodeExactRe = regexp.MustCompile(`^v?([0-9]{1,4}\.[0-9]{1,4}\.[0-9]{1,4})$`)

func (nodeToolchain) exact(want, arch string) (string, bool) {
	m := nodeExactRe.FindStringSubmatch(strings.TrimSpace(want))
	if m == nil {
		return "", false
	}
	return "node-v" + m[1] + "-linux-" + nodeArch(arch), true
}

type nodeRelease struct {
	Version string   `json:"version"`
	Files   []string `json:"files"`
	LTS     any      `json:"lts"` // false, or the line's name
}

func (r nodeRelease) lts() string {
	s, _ := r.LTS.(string)
	return s
}

func (nodeToolchain) resolve(ctx context.Context, s *toolchainStore, want, arch string) (toolchainRelease, error) {
	a := nodeArch(arch)
	data, err := s.index(ctx, s.nodeDist+"index.json")
	if err != nil {
		return toolchainRelease{}, err
	}
	var releases []nodeRelease
	if err := json.Unmarshal(data, &releases); err != nil {
		return toolchainRelease{}, fmt.Errorf("nodejs.org's index: %w", err)
	}
	ver, err := pickNode(releases, want, "linux-"+a)
	if err != nil {
		return toolchainRelease{}, err
	}
	file := "node-" + ver + "-linux-" + a + ".tar.gz"
	sums, err := s.index(ctx, s.nodeDist+ver+"/SHASUMS256.txt")
	if err != nil {
		return toolchainRelease{}, err
	}
	sum := ""
	for _, line := range strings.Split(string(sums), "\n") {
		if f := strings.Fields(line); len(f) == 2 && f[1] == file {
			sum = f[0]
		}
	}
	if sum == "" {
		return toolchainRelease{}, fmt.Errorf("%s: not in SHASUMS256.txt", file)
	}
	return toolchainRelease{Name: "node-" + ver + "-linux-" + a, URL: s.nodeDist + ver + "/" + file, SHA256: sum, Top: "node-" + ver + "-linux-" + a}, nil
}

// pickNode chooses the release for what a repository wants: an exact
// version, an alias (lts/*, lts/<name>, node), or a range, satisfied by
// its newest LTS release, else by its newest release.
func pickNode(releases []nodeRelease, want, file string) (string, error) {
	want = strings.TrimSpace(want)
	alias := strings.ToLower(want)
	var r semverRange
	if !strings.HasPrefix(alias, "lts") && alias != "node" && alias != "latest" && alias != "current" && alias != "stable" {
		var err error
		if r, err = parseSemverRange(want); err != nil {
			return "", err
		}
	}
	var best, bestLTS semver
	var bestS, bestLTSS string
	for _, rel := range releases {
		v, ok := parseSemver(rel.Version)
		if !ok || !strings.HasPrefix(rel.Version, "v") || !slices.Contains(rel.Files, file) {
			continue
		}
		switch {
		case alias == "lts/*" || alias == "lts":
			if rel.lts() == "" {
				continue
			}
		case strings.HasPrefix(alias, "lts/"):
			if !strings.EqualFold(rel.lts(), strings.TrimPrefix(alias, "lts/")) {
				continue
			}
		case r != nil:
			if !r.match(v) {
				continue
			}
		}
		if bestS == "" || best.less(v) {
			best, bestS = v, rel.Version
		}
		if rel.lts() != "" && (bestLTSS == "" || bestLTS.less(v)) {
			bestLTS, bestLTSS = v, rel.Version
		}
	}
	if bestLTSS != "" && r != nil {
		return bestLTSS, nil
	}
	if bestS != "" {
		return bestS, nil
	}
	return "", fmt.Errorf("no Node.js release for %q", want)
}

func (nodeToolchain) env(dir string) ([]string, map[string]string) {
	return []string{dir + "/bin"}, nil
}

// semver is a release version, major.minor.patch (Node's have no
// pre-release).
type semver [3]int

func (a semver) less(b semver) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}

func parseSemver(s string) (semver, bool) {
	v, n, ok := parsePartial(s)
	return v, ok && n == 3
}

// parsePartial reads 20, 20.1, 20.x or 20.11.1 (v prefix allowed): the
// version, padded with zeros, and how many parts were numbers.
func parsePartial(s string) (semver, int, bool) {
	s = strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(s), "="), "v")
	var v semver
	if s == "" || s == "*" || s == "x" || s == "X" {
		return v, 0, true
	}
	parts := strings.Split(s, ".")
	if len(parts) > 3 {
		return v, 0, false
	}
	n := 0
	for i, p := range parts {
		if p == "x" || p == "X" || p == "*" {
			break
		}
		x, err := strconv.Atoi(p)
		if err != nil || x < 0 || x > 1<<20 {
			return v, 0, false
		}
		v[i] = x
		n = i + 1
	}
	return v, n, true
}

// semverRange is npm's range syntax: alternatives (||) of comparator sets.
type semverRange [][]comparator

type comparator struct {
	op string // >=, <, or = (exact)
	v  semver
}

func (r semverRange) match(v semver) bool {
	for _, set := range r {
		ok := true
		for _, c := range set {
			switch c.op {
			case ">=":
				ok = ok && !v.less(c.v)
			case "<":
				ok = ok && v.less(c.v)
			case "=":
				ok = ok && v == c.v
			}
		}
		if ok {
			return true
		}
	}
	return false
}

// upper is the first version above a partial one: 20 → 21.0.0, 20.1 →
// 20.2.0.
func upper(v semver, n int) semver {
	switch n {
	case 1:
		return semver{v[0] + 1, 0, 0}
	case 2:
		return semver{v[0], v[1] + 1, 0}
	}
	return semver{v[0], v[1], v[2] + 1}
}

var opSpaceRe = regexp.MustCompile(`(>=|<=|>|<|=|\^|~)\s+`)

func parseSemverRange(s string) (semverRange, error) {
	bad := fmt.Errorf("unsupported version range %q", s)
	var r semverRange
	for _, alt := range strings.Split(s, "||") {
		alt = opSpaceRe.ReplaceAllString(strings.TrimSpace(alt), "$1")
		f := strings.Fields(alt)
		var set []comparator
		if len(f) == 3 && f[1] == "-" { // 18 - 20.1
			lo, _, ok1 := parsePartial(f[0])
			hi, n, ok2 := parsePartial(f[2])
			if !ok1 || !ok2 {
				return nil, bad
			}
			set = append(set, comparator{">=", lo})
			if n > 0 {
				set = append(set, comparator{"<", upper(hi, n)})
			}
			r = append(r, set)
			continue
		}
		for _, c := range f {
			cs, err := parseComparator(c)
			if err != nil {
				return nil, bad
			}
			set = append(set, cs...)
		}
		r = append(r, set)
	}
	if len(r) == 0 {
		return nil, bad
	}
	return r, nil
}

func parseComparator(c string) ([]comparator, error) {
	op := ""
	for _, p := range []string{">=", "<=", ">", "<", "^", "~", "="} {
		if strings.HasPrefix(c, p) {
			op, c = p, c[len(p):]
			break
		}
	}
	v, n, ok := parsePartial(c)
	if !ok {
		return nil, errors.New("bad version")
	}
	if n == 0 { // *, x, >=*
		if op == "<" || op == ">" {
			return nil, errors.New("empty range")
		}
		return nil, nil
	}
	switch op {
	case "", "=":
		if n == 3 {
			return []comparator{{"=", v}}, nil
		}
		return []comparator{{">=", v}, {"<", upper(v, n)}}, nil
	case ">=":
		return []comparator{{">=", v}}, nil
	case ">":
		return []comparator{{">=", upper(v, n)}}, nil
	case "<":
		return []comparator{{"<", v}}, nil
	case "<=":
		return []comparator{{"<", upper(v, n)}}, nil
	case "~": // ~1.2.3 → <1.3.0, ~1 → <2.0.0
		if n == 1 {
			return []comparator{{">=", v}, {"<", upper(v, 1)}}, nil
		}
		return []comparator{{">=", v}, {"<", upper(v, 2)}}, nil
	case "^": // the first non-zero part stays
		switch {
		case v[0] > 0 || n == 1:
			return []comparator{{">=", v}, {"<", upper(v, 1)}}, nil
		case v[1] > 0 || n == 2:
			return []comparator{{">=", v}, {"<", upper(v, 2)}}, nil
		}
		return []comparator{{">=", v}, {"<", upper(v, 3)}}, nil
	}
	return nil, errors.New("bad operator")
}
