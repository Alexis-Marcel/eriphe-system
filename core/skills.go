package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"path"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

// The user's skills (agentskills.io): a folder with a SKILL.md, its
// instructions, and maybe scripts, references and assets. The user adds one
// in Settings → Skills (web_skills.go), reads it and approves it; from then
// on every worker task has it (node/skills.go), and the brain reads it
// with read_skill (skills_tools.go).
//
// A skill is kept as an immutable bundle: a tar of its files in a fixed
// form (sorted, no dates, no owners), named by its sha256. A skill's row
// points at the version the user approved, and at a new one waiting for
// approval: uploading a changed version never reaches a task before the
// user approves it, after reading its diff. Tasks get the approved
// version's name and hash; their node fetches the bundle and checks it.

const skillSchema = `CREATE TABLE IF NOT EXISTS skill_bundles (sha TEXT PRIMARY KEY, data BLOB NOT NULL, time INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS skills (name TEXT PRIMARY KEY, approved TEXT NOT NULL DEFAULT '', pending TEXT NOT NULL DEFAULT '',
  enabled INTEGER NOT NULL DEFAULT 1, source TEXT NOT NULL DEFAULT '', approved_at INTEGER NOT NULL DEFAULT 0, updated INTEGER NOT NULL);`

func init() { extraSchema = append(extraSchema, skillSchema) }

// Limits of one skill: a skill is instructions and small scripts.
const (
	maxSkillFiles = 200
	maxSkillBytes = 8 << 20 // all its files
	maxSkillFile  = 2 << 20 // one file
)

// skillFile is one file of a skill, by its path inside the skill's folder.
type skillFile struct {
	Path string
	Exec bool // its executable bit
	Data []byte
}

// skillError is a refusal the user reads: a text key and its argument.
type skillError struct {
	Key, Arg string
}

func (e *skillError) Error() string { return e.Key + ": " + e.Arg }

func skillErr(key, arg string) error { return &skillError{Key: key, Arg: arg} }

// junk are files a folder picks up from the system, never the skill's.
func junk(p string) bool {
	for _, part := range strings.Split(p, "/") {
		if part == ".DS_Store" || part == "__MACOSX" || part == ".git" || part == "Thumbs.db" || strings.HasPrefix(part, "._") {
			return true
		}
	}
	return false
}

// cleanSkillFiles checks the paths and the limits, drops the system's junk,
// and takes away the folder around the skill when it came in one
// ("my-skill/SKILL.md"): the paths are then the skill's own.
func cleanSkillFiles(in []skillFile) ([]skillFile, error) {
	var out []skillFile
	total := 0
	for _, f := range in {
		p := strings.TrimPrefix(strings.ReplaceAll(f.Path, "\\", "/"), "./")
		if p == "" || strings.HasSuffix(p, "/") || junk(p) {
			continue
		}
		if path.IsAbs(p) || path.Clean(p) != p || slices.Contains(strings.Split(p, "/"), "..") || strings.ContainsFunc(p, func(r rune) bool { return r < ' ' }) {
			return nil, skillErr("skills.err.path", f.Path)
		}
		if len(f.Data) > maxSkillFile {
			return nil, skillErr("skills.err.filesize", p)
		}
		total += len(f.Data)
		out = append(out, skillFile{Path: p, Exec: f.Exec, Data: f.Data})
	}
	if len(out) > maxSkillFiles {
		return nil, skillErr("skills.err.toomany", fmt.Sprint(maxSkillFiles))
	}
	if total > maxSkillBytes {
		return nil, skillErr("skills.err.toobig", fmt.Sprint(maxSkillBytes>>20))
	}
	// One folder around everything: its content is the skill.
	for {
		top := ""
		same := len(out) > 0
		for _, f := range out {
			dir, _, ok := strings.Cut(f.Path, "/")
			if !ok || (top != "" && dir != top) {
				same = false
				break
			}
			top = dir
		}
		if !same {
			break
		}
		for i := range out {
			out[i].Path = strings.TrimPrefix(out[i].Path, top+"/")
		}
	}
	slices.SortFunc(out, func(a, b skillFile) int { return strings.Compare(a.Path, b.Path) })
	for i := 1; i < len(out); i++ {
		if out[i].Path == out[i-1].Path {
			return nil, skillErr("skills.err.path", out[i].Path)
		}
	}
	if !slices.ContainsFunc(out, func(f skillFile) bool { return f.Path == "SKILL.md" }) {
		return nil, skillErr("skills.err.noskillmd", "")
	}
	return out, nil
}

// packSkill makes a skill's bundle and its hash: the same files always
// give the same bytes.
func packSkill(files []skillFile) ([]byte, string, error) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, f := range files {
		mode := int64(0o644)
		if f.Exec {
			mode = 0o755
		}
		h := &tar.Header{Name: f.Path, Mode: mode, Size: int64(len(f.Data)), ModTime: time.Unix(0, 0), Typeflag: tar.TypeReg, Format: tar.FormatPAX}
		if err := tw.WriteHeader(h); err != nil {
			return nil, "", err
		}
		if _, err := tw.Write(f.Data); err != nil {
			return nil, "", err
		}
	}
	if err := tw.Close(); err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(buf.Bytes())
	return buf.Bytes(), hex.EncodeToString(sum[:]), nil
}

// unpackSkill reads a bundle back.
func unpackSkill(data []byte) ([]skillFile, error) {
	tr := tar.NewReader(bytes.NewReader(data))
	var out []skillFile
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
		b, err := io.ReadAll(io.LimitReader(tr, maxSkillFile+1))
		if err != nil {
			return nil, err
		}
		out = append(out, skillFile{Path: h.Name, Exec: h.Mode&0o111 != 0, Data: b})
	}
}

// filesFromZip reads a zip of a skill.
func filesFromZip(data []byte) ([]skillFile, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, skillErr("skills.err.zip", "")
	}
	if len(zr.File) > 4*maxSkillFiles {
		return nil, skillErr("skills.err.toomany", fmt.Sprint(maxSkillFiles))
	}
	var out []skillFile
	total := 0
	for _, zf := range zr.File {
		if zf.FileInfo().IsDir() {
			continue
		}
		if !zf.Mode().IsRegular() {
			return nil, skillErr("skills.err.special", zf.Name) // a link would point anywhere
		}
		if zf.UncompressedSize64 > maxSkillFile {
			return nil, skillErr("skills.err.filesize", zf.Name)
		}
		rc, err := zf.Open()
		if err != nil {
			return nil, skillErr("skills.err.zip", "")
		}
		b, err := io.ReadAll(io.LimitReader(rc, maxSkillFile+1))
		rc.Close()
		if err != nil || len(b) > maxSkillFile {
			return nil, skillErr("skills.err.filesize", zf.Name)
		}
		if total += len(b); total > maxSkillBytes {
			return nil, skillErr("skills.err.toobig", fmt.Sprint(maxSkillBytes>>20))
		}
		out = append(out, skillFile{Path: zf.Name, Exec: zf.Mode()&0o111 != 0, Data: b})
	}
	return out, nil
}

// skillManifest is what a SKILL.md's front matter says.
type skillManifest struct {
	Name         string
	Description  string
	AllowedTools string // shown, never honoured: Eriphe's permissions decide
	Body         string // the instructions, after the front matter
}

// The name of a skill (agentskills.io): lowercase letters, digits and
// hyphens, 64 at most, no hyphen at either end nor two in a row.
var skillName = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9]|-[a-z0-9]){0,63}$`)

// parseSkillMD reads a SKILL.md: its YAML front matter (the keys of the
// top level, plain, quoted or as a block) and its body.
func parseSkillMD(raw []byte) (skillManifest, error) {
	var m skillManifest
	if !utf8.Valid(raw) {
		return m, skillErr("skills.err.frontmatter", "")
	}
	text := strings.ReplaceAll(strings.TrimPrefix(string(raw), "\uFEFF"), "\r\n", "\n")
	rest, ok := strings.CutPrefix(text, "---\n")
	if !ok {
		return m, skillErr("skills.err.frontmatter", "")
	}
	front, body, ok := strings.Cut(rest, "\n---")
	if !ok {
		return m, skillErr("skills.err.frontmatter", "")
	}
	_, body, _ = strings.Cut(body, "\n")
	m.Body = strings.TrimSpace(body)
	keys := map[string]string{}
	lines := strings.Split(front, "\n")
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		if line == "" || line[0] == ' ' || line[0] == '\t' || line[0] == '#' {
			continue
		}
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		// A block (> or |) or a list: its indented lines.
		var more []string
		for i+1 < len(lines) && (strings.HasPrefix(lines[i+1], " ") || strings.HasPrefix(lines[i+1], "\t") || lines[i+1] == "") {
			i++
			if s := strings.TrimSpace(lines[i]); s != "" {
				more = append(more, strings.TrimSpace(strings.TrimPrefix(s, "- ")))
			}
		}
		switch {
		case strings.HasPrefix(v, ">") || strings.HasPrefix(v, "|") || v == "":
			v = strings.Join(more, " ")
		case len(v) >= 2 && (v[0] == '"' && v[len(v)-1] == '"' || v[0] == '\'' && v[len(v)-1] == '\''):
			v = v[1 : len(v)-1]
		}
		keys[k] = v
	}
	m.Name, m.Description, m.AllowedTools = keys["name"], keys["description"], keys["allowed-tools"]
	switch {
	case !skillName.MatchString(m.Name):
		return m, skillErr("skills.err.name", m.Name)
	case m.Description == "" || utf8.RuneCountInString(m.Description) > 1024:
		return m, skillErr("skills.err.description", "")
	}
	return m, nil
}

// scriptExts are the files that run, whatever their folder.
var scriptExts = []string{".sh", ".bash", ".zsh", ".py", ".js", ".mjs", ".cjs", ".ts", ".rb", ".pl", ".php", ".ps1", ".bat", ".cmd", ".exe", ".bin", ".jar"}

// isScript tells whether a file is code a task could run: executable, in
// scripts/, with a script's extension, or not text.
func isScript(f skillFile) bool {
	return f.Exec || strings.HasPrefix(f.Path, "scripts/") || strings.Contains(f.Path, "/scripts/") ||
		slices.Contains(scriptExts, strings.ToLower(path.Ext(f.Path))) || !isText(f.Data)
}

// isText: valid UTF-8 without NUL.
func isText(b []byte) bool { return utf8.Valid(b) && !bytes.Contains(b, []byte{0}) }

// Skill is one of the user's skills, as the settings and the brain see it.
type Skill struct {
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Approved    string    `json:"sha256,omitempty"`  // the version tasks get, if approved
	Pending     string    `json:"pending,omitempty"` // a version waiting for approval
	Enabled     bool      `json:"enabled"`
	Source      string    `json:"source,omitempty"` // upload, or github:owner/repo/path@commit
	ApprovedAt  time.Time `json:"approved_at,omitzero"`
	Updated     time.Time `json:"updated"`
	Files       int       `json:"files"`
	HasScripts  bool      `json:"has_scripts"`
}

// Active: approved and on, so tasks get it.
func (s Skill) Active() bool { return s.Approved != "" && s.Enabled }

// skillVersion is one bundle, read: its files and its SKILL.md.
type skillVersion struct {
	SHA      string
	Files    []skillFile
	Manifest skillManifest
}

// Scripts are its files that run.
func (v *skillVersion) Scripts() []string {
	var out []string
	for _, f := range v.Files {
		if isScript(f) {
			out = append(out, f.Path)
		}
	}
	return out
}

func (s *Store) skillBundle(sha string) ([]byte, error) {
	var data []byte
	err := s.db.QueryRow(`SELECT data FROM skill_bundles WHERE sha = ?`, sha).Scan(&data)
	return data, err
}

// skillVersion reads a bundle, and checks it against its name.
func (s *Store) skillVersion(sha string) (*skillVersion, error) {
	data, err := s.skillBundle(sha)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != sha {
		return nil, fmt.Errorf("skill bundle %s does not match its hash", sha)
	}
	files, err := unpackSkill(data)
	if err != nil {
		return nil, err
	}
	v := &skillVersion{SHA: sha, Files: files}
	for _, f := range files {
		if f.Path == "SKILL.md" {
			v.Manifest, err = parseSkillMD(f.Data)
		}
	}
	return v, err
}

var errNoSkill = errors.New("no such skill")

func (s *Store) skills() ([]Skill, error) {
	rows, err := s.db.Query(`SELECT name, approved, pending, enabled, source, approved_at, updated FROM skills ORDER BY name`)
	if err != nil {
		return nil, err
	}
	var out []Skill
	for rows.Next() {
		var k Skill
		var at, up int64
		if err := rows.Scan(&k.Name, &k.Approved, &k.Pending, &k.Enabled, &k.Source, &at, &up); err != nil {
			rows.Close()
			return nil, err
		}
		if at > 0 {
			k.ApprovedAt = time.Unix(at, 0)
		}
		k.Updated = time.Unix(up, 0)
		out = append(out, k)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Each one's description and files.
	for i := range out {
		sha := orStr(out[i].Approved, out[i].Pending) // what tasks get, else the version to read
		if v, err := s.skillVersion(sha); err == nil {
			out[i].Description, out[i].Files, out[i].HasScripts = v.Manifest.Description, len(v.Files), len(v.Scripts()) > 0
		}
	}
	return out, nil
}

func (s *Store) skill(name string) (*Skill, error) {
	all, err := s.skills()
	if err != nil {
		return nil, err
	}
	for _, k := range all {
		if k.Name == name {
			return &k, nil
		}
	}
	return nil, errNoSkill
}

// addSkillVersion keeps a version of a skill: a new skill, or a new
// version of one, waits for the user's approval; the version already
// approved changes nothing (and drops a pending one).
func (s *Store) addSkillVersion(files []skillFile, source string) (*Skill, bool, error) {
	files, err := cleanSkillFiles(files)
	if err != nil {
		return nil, false, err
	}
	var m skillManifest
	for _, f := range files {
		if f.Path == "SKILL.md" {
			if m, err = parseSkillMD(f.Data); err != nil {
				return nil, false, err
			}
		}
	}
	data, sha, err := packSkill(files)
	if err != nil {
		return nil, false, err
	}
	now := time.Now().Unix()
	tx, err := s.db.Begin()
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT OR IGNORE INTO skill_bundles (sha, data, time) VALUES (?, ?, ?)`, sha, data, now); err != nil {
		return nil, false, err
	}
	var approved string
	err = tx.QueryRow(`SELECT approved FROM skills WHERE name = ?`, m.Name).Scan(&approved)
	switch {
	case err == sql.ErrNoRows:
		_, err = tx.Exec(`INSERT INTO skills (name, pending, source, updated) VALUES (?, ?, ?, ?)`, m.Name, sha, source, now)
	case err != nil:
	case approved == sha:
		_, err = tx.Exec(`UPDATE skills SET pending = '', source = ?, updated = ? WHERE name = ?`, source, now, m.Name)
	default:
		_, err = tx.Exec(`UPDATE skills SET pending = ?, source = ?, updated = ? WHERE name = ?`, sha, source, now, m.Name)
	}
	if err != nil {
		return nil, false, err
	}
	if err := gcSkillBundles(tx); err != nil {
		return nil, false, err
	}
	if err := tx.Commit(); err != nil {
		return nil, false, err
	}
	k, err := s.skill(m.Name)
	return k, approved != sha, err
}

// approveSkill makes the pending version the one tasks get: the version
// the user read (sha), not one uploaded since.
func (s *Store) approveSkill(name, sha string) error {
	res, err := s.db.Exec(`UPDATE skills SET approved = pending, pending = '', approved_at = ?, updated = ? WHERE name = ? AND pending = ? AND pending != ''`,
		time.Now().Unix(), time.Now().Unix(), name, sha)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errNoSkill
	}
	return s.gcSkills()
}

// rejectSkill drops the pending version; a skill never approved goes.
func (s *Store) rejectSkill(name string) error {
	if _, err := s.db.Exec(`DELETE FROM skills WHERE name = ? AND approved = ''`, name); err != nil {
		return err
	}
	if _, err := s.db.Exec(`UPDATE skills SET pending = '' WHERE name = ?`, name); err != nil {
		return err
	}
	return s.gcSkills()
}

func (s *Store) enableSkill(name string, on bool) error {
	res, err := s.db.Exec(`UPDATE skills SET enabled = ?, updated = ? WHERE name = ?`, on, time.Now().Unix(), name)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errNoSkill
	}
	return nil
}

func (s *Store) deleteSkill(name string) error {
	res, err := s.db.Exec(`DELETE FROM skills WHERE name = ?`, name)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errNoSkill
	}
	return s.gcSkills()
}

func (s *Store) gcSkills() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := gcSkillBundles(tx); err != nil {
		return err
	}
	return tx.Commit()
}

// gcSkillBundles drops the bundles no skill points at any more. A task
// that started with one is fetched it at its start; a restarted one
// whose version went away starts without it.
func gcSkillBundles(tx *sql.Tx) error {
	_, err := tx.Exec(`DELETE FROM skill_bundles WHERE sha NOT IN (SELECT approved FROM skills UNION SELECT pending FROM skills)`)
	return err
}

// activeSkills are the skills tasks get: approved and on.
func (c *core) activeSkills() []Skill {
	all, err := c.store.skills()
	if err != nil {
		return nil
	}
	var out []Skill
	for _, k := range all {
		if k.Active() {
			out = append(out, k)
		}
	}
	return out
}
