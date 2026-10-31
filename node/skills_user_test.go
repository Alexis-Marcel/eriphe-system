package main

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

type skillEntry struct {
	name string
	mode int64
	data string
	typ  byte
}

func skillTar(t *testing.T, entries ...skillEntry) ([]byte, string) {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, e := range entries {
		typ := e.typ
		if typ == 0 {
			typ = tar.TypeReg
		}
		h := &tar.Header{Name: e.name, Mode: e.mode, Size: int64(len(e.data)), Typeflag: typ}
		if typ == tar.TypeSymlink {
			h.Size, h.Linkname = 0, "/etc/passwd"
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		tw.Write([]byte(e.data))
	}
	tw.Close()
	sum := sha256.Sum256(buf.Bytes())
	return buf.Bytes(), hex.EncodeToString(sum[:])
}

// The node fetches each skill of the task, checks its hash and unpacks it
// in the run directory; a bundle that does not match is left out.
func TestMountSkills(t *testing.T) {
	good, goodSHA := skillTar(t, skillEntry{name: "SKILL.md", mode: 0o644, data: "---\nname: notes\n---\n"},
		skillEntry{name: "scripts/run.sh", mode: 0o755, data: "echo hi"})
	bad, _ := skillTar(t, skillEntry{name: "SKILL.md", mode: 0o644, data: "changed"})
	_, badSHA := skillTar(t, skillEntry{name: "SKILL.md", mode: 0o644, data: "approved"})
	n := newNode(options{})
	asked := []string{}
	n.skillFetch = func(_ context.Context, taskID, sha string) ([]byte, error) {
		asked = append(asked, taskID+"/"+sha)
		switch sha {
		case goodSHA:
			return good, nil
		case badSHA:
			return bad, nil // not what was approved
		}
		return nil, errors.New("gone")
	}
	task := &Task{ID: "t-1", dir: t.TempDir(), Spec: TaskSpec{Skills: []TaskSkill{
		{Name: "notes", SHA256: goodSHA}, {Name: "tampered", SHA256: badSHA}, {Name: "../x", SHA256: goodSHA},
	}}}
	n.mountSkills(task)
	if !slices.Equal(asked, []string{"t-1/" + goodSHA, "t-1/" + badSHA}) {
		t.Errorf("asked %v", asked)
	}
	base := filepath.Join(runDir(task), "skills", ".claude", "skills")
	if b, err := os.ReadFile(filepath.Join(base, "notes", "SKILL.md")); err != nil || !strings.Contains(string(b), "name: notes") {
		t.Fatalf("SKILL.md: %q %v", b, err)
	}
	fi, err := os.Stat(filepath.Join(base, "notes", "scripts", "run.sh"))
	if err != nil || runtime.GOOS != "windows" && fi.Mode().Perm() != 0o700 {
		t.Errorf("script: %v %v", fi, err)
	}
	if _, err := os.Stat(filepath.Join(base, "tampered")); !os.IsNotExist(err) {
		t.Error("a bundle that does not match its hash was mounted")
	}
	if skillsDirPath != "/run/eriphe/skills/.claude/skills" {
		t.Errorf("skills at %s", skillsDirPath)
	}
}

// A bundle holds regular files inside the skill, nothing else.
func TestUnpackSkillRefuses(t *testing.T) {
	for name, e := range map[string]skillEntry{
		"out":      {name: "../evil", data: "x"},
		"absolute": {name: "/etc/cron.d/x", data: "x"},
		"link":     {name: "SKILL.md", typ: tar.TypeSymlink},
	} {
		data, _ := skillTar(t, e)
		if err := unpackSkill(data, t.TempDir()); err == nil {
			t.Errorf("%s: unpacked", name)
		}
	}
}

// Each runtime is pointed at the user's skills, unless it keeps skills off
// (eriphe-core lists them in the task's instructions then).
func TestUserSkillsPointers(t *testing.T) {
	n := newNode(options{callbackURL: "http://eriphe-node:8790"})
	skills := []TaskSkill{{Name: "notes", SHA256: strings.Repeat("a", 64)}}
	args, _ := claudeCode{}.command(n, &Task{ID: "t-1", Spec: TaskSpec{Skills: skills}})
	if !hasSeq(args, "--add-dir", "/run/eriphe/skills") {
		t.Errorf("claude: %v", args)
	}
	args, _ = claudeCode{}.command(n, &Task{ID: "t-1", Spec: TaskSpec{Skills: skills, Repo: "someone/tool"}})
	if slices.Contains(args, "--add-dir") || !slices.Contains(args, "--disable-slash-commands") {
		t.Errorf("claude, someone else's repository: %v", args)
	}
	if args, _ := (claudeCode{}).command(n, &Task{ID: "t-1"}); slices.Contains(args, "--add-dir") {
		t.Errorf("claude without skills: %v", args)
	}

	env := opencodeSetup(&Task{Spec: TaskSpec{Skills: skills, Repo: "someone/tool"}})
	var cfg struct {
		Skills struct {
			Paths []string `json:"paths"`
		} `json:"skills"`
	}
	json.Unmarshal([]byte(env["OPENCODE_CONFIG_CONTENT"]), &cfg)
	if !slices.Equal(cfg.Skills.Paths, []string{skillsDirPath}) || env["OPENCODE_DISABLE_EXTERNAL_SKILLS"] != "1" {
		t.Errorf("opencode: %v", env)
	}

	task := newTestTask()
	task.Spec.Skills = skills
	args, _ = codexRuntime{}.command(newNode(options{}), task)
	if line := strings.Join(args, " "); !strings.Contains(line, `ln -sfn /run/eriphe/skills/.claude/skills "$CODEX_HOME/skills/eriphe"`) {
		t.Errorf("codex: %s", line)
	}
}
