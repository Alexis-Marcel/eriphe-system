package main

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

// The user's skills in a task. eriphe-core names in the spec the skills
// the user approved (name and sha256 of each bundle, a tar of its files).
// Before the container starts, the node fetches each bundle with its own
// secret, for that task only:
//
//	GET /v1/node/tasks/{id}/skills/{sha256}
//
// checks its hash, and unpacks it into the task's run directory, which is
// mounted read-only at /run/eriphe: the skills are at
// /run/eriphe/skills/.claude/skills/<name>. Each runtime is pointed there:
//
//	Claude Code  --add-dir /run/eriphe/skills (it loads <dir>/.claude/skills)
//	OpenCode     skills.paths in its configuration
//	Codex        $CODEX_HOME/skills/eriphe, a link to that folder
//	ACP          nothing: eriphe-core lists them in the task's instructions
//
// Claude Code and Codex load none in a task on a repository not vouched
// for (repoSkillsOff): eriphe-core lists them in its instructions then.
// Scripts run in the task's container only, under its permissions. A
// skill that cannot be fetched or does not match its hash is left out.

// TaskSkill is one of the user's skills, as the spec names it.
type TaskSkill struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
}

const (
	skillsRun     = "skills"                       // in the run directory
	skillsRoot    = runMount + "/" + skillsRun     // given to Claude Code's --add-dir
	skillsDirPath = skillsRoot + "/.claude/skills" // the skills, one folder each
	maxSkillTar   = 16 << 20                       // a bundle, at most
	maxSkillEntry = 4 << 20                        // one of its files
	maxSkillFiles = 500
)

var skillNameRe = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9]|-[a-z0-9]){0,63}$`)

// fetchSkill gets a bundle from eriphe-core.
func (s *state) fetchSkill(ctx context.Context, taskID, sha string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.base()+"/v1/node/tasks/"+taskID+"/skills/"+sha, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+s.Secret)
	req.Header.Set("Eriphe-Node", s.Name)
	resp, err := s.longClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("the skill is not in this task, or gone")
	}
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("eriphe-core: %s", resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxSkillTar+1))
	if err == nil && len(data) > maxSkillTar {
		err = fmt.Errorf("bundle larger than %d bytes", maxSkillTar)
	}
	return data, err
}

// mountSkills fetches the task's skills into its run directory. It runs
// after writeRunFiles, which empties that directory.
func (n *node) mountSkills(t *Task) {
	if len(t.Spec.Skills) == 0 || n.skillFetch == nil || t.dir == "" {
		return
	}
	for _, k := range t.Spec.Skills {
		if err := n.mountSkill(t, k); err != nil {
			log.Printf("task %s: skill %s left out: %v", t.ID, k.Name, err)
		}
	}
}

func (n *node) mountSkill(t *Task, k TaskSkill) error {
	if !skillNameRe.MatchString(k.Name) || len(k.SHA256) != sha256.Size*2 {
		return fmt.Errorf("bad name or hash")
	}
	data, err := n.skillFetch(context.Background(), t.ID, k.SHA256)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != k.SHA256 {
		return fmt.Errorf("the bundle does not match its hash")
	}
	dir := filepath.Join(runDir(t), skillsRun, ".claude", "skills", k.Name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := unpackSkill(data, dir); err != nil {
		os.RemoveAll(dir)
		return err
	}
	return nil
}

// unpackSkill writes a bundle's files under dir: regular files only, each
// inside dir.
func unpackSkill(data []byte, dir string) error {
	tr := tar.NewReader(bytes.NewReader(data))
	for count := 0; ; count++ {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if count >= maxSkillFiles {
			return fmt.Errorf("more than %d files", maxSkillFiles)
		}
		p := h.Name
		if h.Typeflag != tar.TypeReg || p == "" || path.IsAbs(p) || path.Clean(p) != p || slices.Contains(strings.Split(p, "/"), "..") || strings.Contains(p, "\\") {
			return fmt.Errorf("refused entry %q", p)
		}
		if h.Size > maxSkillEntry {
			return fmt.Errorf("%s: too large", p)
		}
		target := filepath.Join(dir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return err
		}
		mode := os.FileMode(0o600)
		if h.Mode&0o111 != 0 {
			mode = 0o700 // root in the container runs it
		}
		f, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
		if err != nil {
			return err
		}
		_, err = io.Copy(f, io.LimitReader(tr, maxSkillEntry))
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return err
		}
	}
}
