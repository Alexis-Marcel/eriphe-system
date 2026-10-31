package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const skillMD = `---
name: release-notes
description: >
  Write release notes from merged pull requests.
  Use when asked for a changelog.
allowed-tools: Bash(git log *)
---
# Release notes

Read the merged pull requests, then write the notes.
`

func skillFiles(md string, more ...skillFile) []skillFile {
	return append([]skillFile{{Path: "release-notes/SKILL.md", Data: []byte(md)}}, more...)
}

func testSkillStore(t *testing.T) *Store {
	t.Helper()
	s, err := openStore(filepath.Join(t.TempDir(), "core.db"))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestParseSkillMD(t *testing.T) {
	m, err := parseSkillMD([]byte(skillMD))
	if err != nil {
		t.Fatal(err)
	}
	if m.Name != "release-notes" || m.Description != "Write release notes from merged pull requests. Use when asked for a changelog." ||
		m.AllowedTools != "Bash(git log *)" || !strings.HasPrefix(m.Body, "# Release notes") {
		t.Fatalf("%+v", m)
	}
	quoted, err := parseSkillMD([]byte("---\r\nname: \"pdf\"\r\ndescription: 'Fill PDF forms: when asked.'\r\n---\r\nbody"))
	if err != nil || quoted.Name != "pdf" || quoted.Description != "Fill PDF forms: when asked." {
		t.Fatalf("quoted: %+v %v", quoted, err)
	}
	for key, md := range map[string]string{
		"skills.err.frontmatter": "# no header",
		"skills.err.name":        "---\nname: Bad_Name\ndescription: x\n---\n",
		"skills.err.description": "---\nname: ok\n---\n",
	} {
		var se *skillError
		if _, err := parseSkillMD([]byte(md)); !errors.As(err, &se) || se.Key != key {
			t.Errorf("%q: %v, want %s", md, err, key)
		}
	}
	for _, bad := range []string{"-a", "a-", "a--b", "A", strings.Repeat("a", 65)} {
		if skillName.MatchString(bad) {
			t.Errorf("name %q accepted", bad)
		}
	}
}

// The folder around a skill goes, the system's junk too; a path out of the
// skill, or no SKILL.md, is refused.
func TestCleanSkillFiles(t *testing.T) {
	got, err := cleanSkillFiles(skillFiles(skillMD,
		skillFile{Path: "release-notes/scripts/run.sh", Data: []byte("echo")},
		skillFile{Path: "release-notes/.DS_Store", Data: []byte{0}},
		skillFile{Path: "__MACOSX/release-notes/._SKILL.md", Data: []byte{0}}))
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, f := range got {
		paths = append(paths, f.Path)
	}
	if !slices.Equal(paths, []string{"SKILL.md", "scripts/run.sh"}) {
		t.Fatalf("paths %v", paths)
	}
	for key, files := range map[string][]skillFile{
		"skills.err.path":      skillFiles(skillMD, skillFile{Path: "release-notes/../../etc/passwd"}),
		"skills.err.noskillmd": {{Path: "x/README.md"}},
		"skills.err.filesize":  skillFiles(skillMD, skillFile{Path: "release-notes/big.bin", Data: make([]byte, maxSkillFile+1)}),
	} {
		var se *skillError
		if _, err := cleanSkillFiles(files); !errors.As(err, &se) || se.Key != key {
			t.Errorf("%s: %v", key, err)
		}
	}
}

// The same files give the same bundle and hash, whatever their order.
func TestPackSkillStable(t *testing.T) {
	a, _ := cleanSkillFiles(skillFiles(skillMD, skillFile{Path: "release-notes/b.md", Data: []byte("b")}))
	b, _ := cleanSkillFiles([]skillFile{{Path: "release-notes/b.md", Data: []byte("b")}, {Path: "release-notes/SKILL.md", Data: []byte(skillMD)}})
	da, sa, _ := packSkill(a)
	db, sb, _ := packSkill(b)
	sum := sha256.Sum256(da)
	if sa != sb || !bytes.Equal(da, db) || hex.EncodeToString(sum[:]) != sa {
		t.Fatalf("not stable: %s %s", sa, sb)
	}
	back, err := unpackSkill(da)
	if err != nil || len(back) != 2 || back[0].Path != "SKILL.md" {
		t.Fatalf("unpack: %+v %v", back, err)
	}
}

// A new skill waits for approval; a changed version waits too, with the
// approved one still the one tasks get; the same version again changes
// nothing; refusing the update keeps the old one; old bundles go.
func TestSkillLifecycle(t *testing.T) {
	s := testSkillStore(t)
	k, changed, err := s.addSkillVersion(skillFiles(skillMD), "upload")
	if err != nil || !changed || k.Approved != "" || k.Pending == "" || k.Active() {
		t.Fatalf("added: %+v %v", k, err)
	}
	v1 := k.Pending
	if err := s.approveSkill(k.Name, "0000"); err == nil {
		t.Fatal("approved another version than the one read")
	}
	if err := s.approveSkill(k.Name, v1); err != nil {
		t.Fatal(err)
	}
	k, _ = s.skill("release-notes")
	if k.Approved != v1 || k.Pending != "" || !k.Active() || k.Description == "" || k.Files != 1 || k.HasScripts {
		t.Fatalf("approved: %+v", k)
	}
	if _, changed, _ := s.addSkillVersion(skillFiles(skillMD), "upload"); changed {
		t.Error("the same version again is a change")
	}
	k, _, err = s.addSkillVersion(skillFiles(strings.Replace(skillMD, "then write", "then draft", 1),
		skillFile{Path: "release-notes/scripts/fetch.py", Data: []byte("print(1)\n")}), "upload")
	if err != nil || k.Approved != v1 || k.Pending == "" || k.Pending == v1 || !k.Active() {
		t.Fatalf("update: %+v %v", k, err)
	}
	old, _ := s.skillVersion(v1)
	cur, _ := s.skillVersion(k.Pending)
	diff := diffSkills(old, cur)
	if len(diff) != 2 || diff[0].Path != "SKILL.md" || diff[0].Status != "changed" || diff[1].Path != "scripts/fetch.py" || !diff[1].Script || diff[1].Status != "added" {
		t.Fatalf("diff %+v", diff)
	}
	var plus, minus []string
	for _, l := range diff[0].Lines {
		switch l.Op {
		case "+":
			plus = append(plus, l.Text)
		case "-":
			minus = append(minus, l.Text)
		}
	}
	if !slices.Equal(plus, []string{"Read the merged pull requests, then draft the notes."}) || !slices.Equal(minus, []string{"Read the merged pull requests, then write the notes."}) {
		t.Fatalf("lines: + %v - %v", plus, minus)
	}
	if err := s.rejectSkill(k.Name); err != nil {
		t.Fatal(err)
	}
	k, _ = s.skill("release-notes")
	if k.Approved != v1 || k.Pending != "" {
		t.Fatalf("rejected update: %+v", k)
	}
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM skill_bundles`).Scan(&n)
	if n != 1 {
		t.Errorf("%d bundles kept, want 1", n)
	}
	if err := s.enableSkill(k.Name, false); err != nil {
		t.Fatal(err)
	}
	if k, _ = s.skill("release-notes"); k.Active() {
		t.Error("a skill turned off is active")
	}
	if err := s.deleteSkill(k.Name); err != nil {
		t.Fatal(err)
	}
	s.db.QueryRow(`SELECT COUNT(*) FROM skill_bundles`).Scan(&n)
	if _, err := s.skill("release-notes"); err != errNoSkill || n != 0 {
		t.Errorf("deleted: %v, %d bundles", err, n)
	}
}

func approvedSkill(t *testing.T, s *Store, files ...skillFile) *Skill {
	t.Helper()
	k, _, err := s.addSkillVersion(files, "upload")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.approveSkill(k.Name, k.Pending); err != nil {
		t.Fatal(err)
	}
	k, _ = s.skill(k.Name)
	return k
}

// Worker tasks get the active skills; an agent that does not load them
// itself is told where they are.
func TestLendSkills(t *testing.T) {
	c := newCore(&Config{}, testSkillStore(t))
	spec := nodeTaskSpec{Runtime: "claude-code"}
	c.lendSkills(&spec)
	if spec.Skills != nil || spec.AppendSystemPrompt != "" {
		t.Fatalf("no skills: %+v", spec)
	}
	k := approvedSkill(t, c.store, skillFiles(skillMD)...)
	for _, tc := range []struct {
		spec  nodeTaskSpec
		index bool
	}{
		{nodeTaskSpec{Runtime: "claude-code"}, false},
		{nodeTaskSpec{Runtime: "claude-code", Repo: "owner/repo", RepoSkills: true}, false},
		{nodeTaskSpec{Runtime: "claude-code", Repo: "someone/tool"}, true},
		{nodeTaskSpec{Runtime: "codex", Repo: "someone/tool"}, true},
		{nodeTaskSpec{Runtime: "opencode", Repo: "someone/tool"}, false},
		{nodeTaskSpec{Runtime: "acp"}, true},
	} {
		spec := tc.spec
		c.lendSkills(&spec)
		if len(spec.Skills) != 1 || spec.Skills[0] != (nodeSkill{Name: "release-notes", SHA256: k.Approved}) {
			t.Errorf("%+v: skills %+v", tc.spec, spec.Skills)
		}
		listed := strings.Contains(spec.AppendSystemPrompt, skillsDir+"/release-notes/SKILL.md") &&
			strings.Contains(spec.AppendSystemPrompt, "Write release notes")
		if listed != tc.index {
			t.Errorf("%+v: index %v: %q", tc.spec, listed, spec.AppendSystemPrompt)
		}
	}
	c.store.enableSkill(k.Name, false)
	spec = nodeTaskSpec{Runtime: "acp"}
	if c.lendSkills(&spec); spec.Skills != nil {
		t.Error("a skill turned off is lent")
	}
}

// A node gets a skill of its own task only, as the bundle the hash names.
func TestTaskSkillEndpoint(t *testing.T) {
	c, task := credCore(t, "")
	k := approvedSkill(t, c.store, skillFiles(skillMD)...)
	c.store.setSpec(task.ID, nodeTaskSpec{Runtime: "claude-code", Skills: []nodeSkill{{Name: k.Name, SHA256: k.Approved}}})
	secret := "node-secret"
	if err := c.store.registerNode("pc", hashSecret(secret)); err != nil {
		t.Fatal(err)
	}
	get := func(node, sec, id, sha string) (int, []byte) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/v1/node/tasks/"+id+"/skills/"+sha, nil)
		req.SetPathValue("id", id)
		req.SetPathValue("sha", sha)
		req.Header.Set("Authorization", "Bearer "+sec)
		req.Header.Set("Eriphe-Node", node)
		c.handleTaskSkill(rec, req)
		return rec.Code, rec.Body.Bytes()
	}
	code, body := get("pc", secret, task.ID, k.Approved)
	if code != 200 {
		t.Fatalf("the task's node: %d %s", code, body)
	}
	sum := sha256.Sum256(body)
	if hex.EncodeToString(sum[:]) != k.Approved {
		t.Fatal("served bytes do not match the hash")
	}
	if code, _ := get("pc", "wrong", task.ID, k.Approved); code != http.StatusUnauthorized {
		t.Errorf("wrong secret: %d", code)
	}
	if code, _ := get("pc", secret, task.ID, strings.Repeat("0", 64)); code != http.StatusNotFound {
		t.Errorf("a skill not in the task: %d", code)
	}
}

// The brain lists the active skills and reads their text, never a script.
func TestSkillTools(t *testing.T) {
	c := newCatalogCore(t)
	approvedSkill(t, c.store, skillFiles(skillMD,
		skillFile{Path: "release-notes/references/style.md", Data: []byte("Short lines.")},
		skillFile{Path: "release-notes/scripts/fetch.sh", Data: []byte("gh pr list")})...)
	cs := connectTo(t, newMCPServer(c))
	call := func(name string, args map[string]any) (string, bool) {
		res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(res.StructuredContent)
		if res.IsError {
			text := ""
			for _, c := range res.Content {
				if tc, ok := c.(*mcp.TextContent); ok {
					text += tc.Text
				}
			}
			return text, true
		}
		return string(raw), false
	}
	out, _ := call("list_skills", nil)
	if !strings.Contains(out, `"name":"release-notes"`) || !strings.Contains(out, `"has_scripts":true`) || !strings.Contains(out, "scripts/fetch.sh") {
		t.Fatalf("list_skills: %s", out)
	}
	if out, isErr := call("read_skill", map[string]any{"name": "release-notes"}); isErr || !strings.Contains(out, "# Release notes") {
		t.Fatalf("read SKILL.md: %s", out)
	}
	if out, isErr := call("read_skill", map[string]any{"name": "release-notes", "path": "references/style.md"}); isErr || !strings.Contains(out, "Short lines.") {
		t.Fatalf("read a reference: %s", out)
	}
	if out, isErr := call("read_skill", map[string]any{"name": "release-notes", "path": "scripts/fetch.sh"}); !isErr || !strings.Contains(out, "runs in a task") {
		t.Fatalf("read a script: %s", out)
	}
	if out, isErr := call("read_skill", map[string]any{"name": "nope"}); !isErr {
		t.Fatalf("an unknown skill: %s", out)
	}
	if tainted := c.turnTaint(); len(tainted) > 0 {
		t.Errorf("reading the user's skill taints the turn: %v", tainted)
	}
}

// uploadSkill posts files to Settings → Skills, as the page's form does.
func (tw *testWeb) uploadSkill(csrf string, paths []string, files map[string][]byte) (int, string, http.Header) {
	tw.t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	raw, _ := json.Marshal(paths)
	mw.WriteField("paths", string(raw))
	for _, p := range paths {
		w, _ := mw.CreateFormFile("files", filepath.Base(p))
		w.Write(files[p])
	}
	if len(paths) == 0 {
		mw.CreateFormFile("files", "") // the folder input, not picked
		for name, data := range files {
			w, _ := mw.CreateFormFile("files", name)
			w.Write(data)
		}
	}
	mw.Close()
	req, _ := http.NewRequest("POST", tw.srv.URL+"/settings/skills", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("X-CSRF-Token", csrf)
	req.Header.Set("HX-Request", "true")
	resp, err := tw.hc.Do(req)
	if err != nil {
		tw.t.Fatal(err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(out), resp.Header
}

// Settings → Skills: a folder is uploaded, read and approved; a zip of a
// new version shows its diff and waits; a bad skill is refused in the form.
func TestSkillsSettings(t *testing.T) {
	tw := newTestWeb(t)
	csrf := tw.setup()
	code, body, _ := tw.do("GET", "/settings/skills", nil, nil)
	if code != 200 || !strings.Contains(body, "Aucun skill") || !strings.Contains(body, `href="/settings/skills" aria-current="page"`) {
		t.Fatalf("empty page: %d", code)
	}
	code, _, h := tw.uploadSkill(csrf, []string{"release-notes/SKILL.md", "release-notes/scripts/fetch.sh"},
		map[string][]byte{"release-notes/SKILL.md": []byte(skillMD), "release-notes/scripts/fetch.sh": []byte("gh pr list\n")})
	if code != 200 || h.Get("HX-Redirect") != "/settings/skills/release-notes" {
		t.Fatalf("upload: %d %v", code, h)
	}
	code, body, _ = tw.do("GET", "/settings/skills/release-notes", nil, nil)
	for _, want := range []string{"Nouveau skill à approuver", "scripts/fetch.sh", "1 script(s)", "Bash(git log *)", `name="sha"`, "Release notes"} {
		if code != 200 || !strings.Contains(body, want) {
			t.Errorf("review page lacks %q", want)
		}
	}
	k, _ := tw.c.store.skill("release-notes")
	if code, _, _ := tw.post("/settings/skills/release-notes/approve", url.Values{"sha": {k.Pending}}, csrf); code != 200 {
		t.Fatalf("approve: %d", code)
	}
	if k, _ = tw.c.store.skill("release-notes"); !k.Active() {
		t.Fatalf("not active: %+v", k)
	}
	_, body, _ = tw.do("GET", "/settings/skills", nil, nil)
	if !strings.Contains(body, "Actif") || !strings.Contains(body, "Write release notes") {
		t.Error("the list does not show the skill as on")
	}

	// A new version, as a zip: its diff, and the approved one still lent.
	var zbuf bytes.Buffer
	zw := zip.NewWriter(&zbuf)
	f, _ := zw.Create("release-notes/SKILL.md")
	f.Write([]byte(strings.Replace(skillMD, "then write", "then draft", 1)))
	zw.Close()
	if code, _, h := tw.uploadSkill(csrf, nil, map[string][]byte{"release-notes.zip": zbuf.Bytes()}); code != 200 || h.Get("HX-Redirect") == "" {
		t.Fatalf("zip: %d", code)
	}
	_, body, _ = tw.do("GET", "/settings/skills/release-notes", nil, nil)
	for _, want := range []string{"Nouvelle version à approuver", "Ce qui a changé", `class="d add"`, "then draft", "supprimé"} {
		if !strings.Contains(body, want) {
			t.Errorf("update page lacks %q", want)
		}
	}
	if k2, _ := tw.c.store.skill("release-notes"); k2.Approved != k.Approved || k2.Pending == "" {
		t.Errorf("the update replaced the approved version: %+v", k2)
	}

	code, body, h = tw.uploadSkill(csrf, []string{"x/README.md"}, map[string][]byte{"x/README.md": []byte("hi")})
	if code != 200 || h.Get("HX-Retarget") != "#skill-result" || !strings.Contains(body, "SKILL.md") {
		t.Errorf("no SKILL.md: %d %q", code, body)
	}

	if code, _, _ := tw.post("/settings/skills/release-notes/enable", url.Values{}, csrf); code != 200 {
		t.Fatalf("turn off: %d", code)
	}
	if k, _ = tw.c.store.skill("release-notes"); k.Enabled {
		t.Error("still on")
	}
	if code, _, h := tw.post("/settings/skills/release-notes/delete", nil, csrf); code != 200 || h.Get("HX-Redirect") != "/settings/skills" {
		t.Fatalf("delete: %d", code)
	}
}
