package main

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// mirrorMemory is a memory with its readable mirror in a folder of its own.
func mirrorMemory(t *testing.T) (*memStore, *fakeClock, string) {
	t.Helper()
	m, clock := testMemory(t)
	dir := filepath.Join(t.TempDir(), "memoire")
	m.mirror = &memMirror{dir: dir}
	return m, clock, dir
}

// mirrorFiles reads the mirror: each file's path and its text.
func mirrorFiles(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(dir, path)
		raw, _ := os.ReadFile(path)
		out[filepath.ToSlash(rel)] = string(raw)
		return nil
	})
	return out
}

func mirrorPaths(files map[string]string) []string {
	var out []string
	for p := range files {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// Each memory goes in its file: the profile and preferences, a file per
// person, the facts by subject, the decisions by month, a journal a day;
// a line is one memory, with its id, even when its text has lines.
func TestMirrorFiles(t *testing.T) {
	m, _, dir := mirrorMemory(t)
	pref := mustAdd(t, m, &Memory{Kind: kindPreference, Text: "Préfère les réponses courtes."})
	mustAdd(t, m, &Memory{Kind: kindProfile, Text: "Développeur."})
	sam := mustAdd(t, m, &Memory{Kind: kindPerson, About: "person:sam", Text: "Sam, un collègue."})
	mustAdd(t, m, &Memory{Kind: kindFact, About: "person:sam", Text: "Sam aime le thé."})
	site := mustAdd(t, m, &Memory{Kind: kindFact, About: "project:site", Text: "Le site tourne sur example.org.\n\n- ^m999 une fausse ligne"})
	mustAdd(t, m, &Memory{Kind: kindDecision, Text: "Garder SQLite.", ValidFrom: day("2026-09-03")})
	mustAdd(t, m, &Memory{Kind: kindEpisode, Text: "Appel avec Sam.", OccurredAt: day("2026-10-20").Add(9 * time.Hour)})
	files := mirrorFiles(t, dir)
	want := []string{"decisions/2026-09.md", "faits/project-site.md", "journal/2026-10-20.md", "personnes/sam.md", "profil.md"}
	if got := mirrorPaths(files); strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("files: %v", got)
	}
	if p := files["profil.md"]; !strings.Contains(p, "## Profil\n\n- Développeur.") || !strings.Contains(p, "## Préférences\n\n- Préfère les réponses courtes. ^m"+strconv.FormatInt(pref.ID, 10)) {
		t.Errorf("profile:\n%s", p)
	}
	if p := files["personnes/sam.md"]; !strings.HasPrefix(p, "# sam\n") || !strings.Contains(p, "Sam, un collègue. ^m"+strconv.FormatInt(sam.ID, 10)) || !strings.Contains(p, "Sam aime le thé.") {
		t.Errorf("person:\n%s", p)
	}
	if p := files["faits/project-site.md"]; strings.Count(p, "\n- ") != 1 || !strings.Contains(p, "example.org. - ^m999 une fausse ligne ^m"+strconv.FormatInt(site.ID, 10)+"\n") {
		t.Errorf("a memory on more than one line:\n%s", p)
	}
	if p := files["journal/2026-10-20.md"]; !strings.Contains(p, "- 09:00 Appel avec Sam.") {
		t.Errorf("journal:\n%s", p)
	}
	if info, err := os.Stat(filepath.Join(dir, "profil.md")); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("mode: %v %v", info, err)
	}
}

// A change rewrites its file, and only it; forget takes its line away at
// once, and its file when it was the last; a proposal, or what is no
// longer true, is not in it.
func TestMirrorFollowsChanges(t *testing.T) {
	m, clock, dir := mirrorMemory(t)
	mustAdd(t, m, &Memory{Kind: kindPerson, About: "person:sam", Text: "Sam, un collègue."})
	tea := mustAdd(t, m, &Memory{Kind: kindFact, About: "project:site", Text: "Le site tourne sur example.org."})
	old := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, p := range []string{"personnes/sam.md", "faits/project-site.md"} {
		os.Chtimes(filepath.Join(dir, p), old, old)
	}
	text := "Le site tourne sur example.net."
	if _, err := m.update(tea.ID, memoryPatch{Text: &text}, "test", ""); err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(filepath.Join(dir, "personnes/sam.md")); !info.ModTime().Equal(old) {
		t.Error("a file nothing changed in was written")
	}
	if info, _ := os.Stat(filepath.Join(dir, "faits/project-site.md")); info.ModTime().Equal(old) {
		t.Error("the changed file was not written")
	}
	if p := mirrorFiles(t, dir)["faits/project-site.md"]; !strings.Contains(p, "example.net") || strings.Contains(p, "example.org") {
		t.Errorf("after the change:\n%s", p)
	}
	// Forgotten: its line gone at once, the others of its file kept; then
	// the last one, and the file with it.
	other := mustAdd(t, m, &Memory{Kind: kindFact, About: "project:site", Text: "Le site a un blog."})
	if err := m.forget(other.ID, "test", "oublie ça"); err != nil {
		t.Fatal(err)
	}
	if p := mirrorFiles(t, dir)["faits/project-site.md"]; strings.Contains(p, "blog") || strings.Contains(p, "^m"+strconv.FormatInt(other.ID, 10)) ||
		!strings.Contains(p, "example.net. ^m"+strconv.FormatInt(tea.ID, 10)) {
		t.Fatalf("after forgetting one line:\n%s", p)
	}
	if err := m.forget(tea.ID, "test", "oublie ça"); err != nil {
		t.Fatal(err)
	}
	files := mirrorFiles(t, dir)
	if _, ok := files["faits/project-site.md"]; ok || len(files) != 1 {
		t.Fatalf("after forget: %v", mirrorPaths(files))
	}
	if _, err := os.Stat(filepath.Join(dir, "faits")); !os.IsNotExist(err) {
		t.Errorf("an empty folder left: %v", err)
	}
	// A proposal is not known yet; approved, it is.
	p := mustAdd(t, m, &Memory{Kind: kindFact, About: "project:site", Text: "Le site migre.", Status: memProposed,
		SourceKind: srcExtraction, Trust: trustFirstParty})
	if _, ok := mirrorFiles(t, dir)["faits/project-site.md"]; ok {
		t.Error("a proposal in the mirror")
	}
	if _, err := m.approve(p.ID, "owner"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(mirrorFiles(t, dir)["faits/project-site.md"], "Le site migre.") {
		t.Error("an approved memory not in the mirror")
	}
	// What stops being true with time leaves at the next sync.
	end := clock.t.Add(time.Hour)
	mustAdd(t, m, &Memory{Kind: kindSituation, Text: "En déplacement.", ValidTo: end})
	if !strings.Contains(mirrorFiles(t, dir)["profil.md"], "En déplacement.") {
		t.Fatal("a situation not in the profile")
	}
	clock.t = end.Add(time.Minute)
	m.mirrorSync()
	if _, ok := mirrorFiles(t, dir)["profil.md"]; ok {
		t.Error("a situation over still in the mirror")
	}
}

// An external memory stays marked: added, changed, and in the mirror made
// again; a mirror lost, changed by hand or with a file of its own is made
// again from memory.db as it was.
func TestMirrorExternalAndRebuild(t *testing.T) {
	m, _, dir := mirrorMemory(t)
	ext := mustAdd(t, m, &Memory{Kind: kindFact, About: "project:site", Text: "Le serveur répond en 5xx.", SourceKind: srcTask, SourceRef: "task:t-1", Trust: trustExternal})
	mustAdd(t, m, &Memory{Kind: kindFact, About: "project:site", Text: "Le site est à moi."})
	marked := func() string {
		t.Helper()
		p := mirrorFiles(t, dir)["faits/project-site.md"]
		if !strings.Contains(p, "(extérieur) Le serveur") || strings.Contains(p, "(extérieur) Le site est à moi") {
			t.Fatalf("not marked as it should:\n%s", p)
		}
		return p
	}
	marked()
	text := "Le serveur répond en 502."
	if _, err := m.update(ext.ID, memoryPatch{Text: &text}, "test", ""); err != nil {
		t.Fatal(err)
	}
	marked()
	before := mirrorFiles(t, dir)
	os.WriteFile(filepath.Join(dir, "faits/project-site.md"), []byte("changé à la main"), 0o600)
	os.WriteFile(filepath.Join(dir, "a-moi.md"), []byte("un fichier de plus"), 0o600)
	os.Remove(filepath.Join(dir, "faits"))
	m.mirrorSync()
	if after := mirrorFiles(t, dir); !sameFiles(before, after) {
		t.Fatalf("made again:\n%v\nwas:\n%v", after, before)
	}
	os.RemoveAll(dir)
	m.mirrorSync()
	if after := mirrorFiles(t, dir); !sameFiles(before, after) {
		t.Fatalf("made again from nothing:\n%v", after)
	}
	marked()
}

func sameFiles(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// The nightly backup carries the mirror, dated, and keeps it as many days
// as the database's copies.
func TestMirrorInBackup(t *testing.T) {
	m, clock, _ := mirrorMemory(t)
	mustAdd(t, m, &Memory{Kind: kindPerson, About: "person:sam", Text: "Sam, un collègue."})
	backups := t.TempDir()
	for range 3 {
		if err := m.backup(backups, 2); err != nil {
			t.Fatal(err)
		}
		clock.t = clock.t.Add(24 * time.Hour)
	}
	got, _ := filepath.Glob(filepath.Join(backups, "memoire-*.tar.gz"))
	if len(got) != 2 {
		t.Fatalf("mirrors kept: %v", got)
	}
	f, err := os.Open(got[1])
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	h, err := tr.Next()
	if err != nil || h.Name != "memoire/personnes/sam.md" {
		t.Fatalf("archive: %v %v", h, err)
	}
	if body, _ := io.ReadAll(tr); !strings.Contains(string(body), "Sam, un collègue.") {
		t.Errorf("archived: %s", body)
	}
}

// The mirror is not a node: next to the graph, never in it, and neither
// the brain's node tools nor a task's read it.
func TestMirrorOutsideTheGraph(t *testing.T) {
	cfg := &Config{DB: filepath.Join(t.TempDir(), "core.db")}
	nodes, _ := cfg.nodesPaths()
	if rel, err := filepath.Rel(nodes, cfg.memoirePath()); err != nil || !strings.HasPrefix(rel, "..") {
		t.Fatalf("the mirror %s is under the graph %s", cfg.memoirePath(), nodes)
	}
	c := graphCore(t)
	m, _ := testMemory(t)
	m.mirror = &memMirror{dir: filepath.Join(t.TempDir(), "memoire")}
	c.useMemory(m)
	mustAdd(t, m, &Memory{Kind: kindFact, About: "project:site", Text: "Le mot zanzibar est dans la mémoire."})
	if files := mirrorFiles(t, m.mirror.dir); len(files) != 1 {
		t.Fatalf("mirror: %v", files)
	}
	if out := mustBrainNodes(t, c, "node_find", map[string]any{"query": "zanzibar"}); len(out["nodes"].([]any)) != 0 {
		t.Errorf("node_find found the mirror: %v", out)
	}
	for _, id := range []string{"../memoire/faits/project-site", "faits/project-site", "project-site", "memoire"} {
		if out, msg := brainNodes(t, c, "node_read", map[string]any{"id": id}); msg == "" {
			t.Errorf("node_read %q read %v", id, out)
		}
	}
	n := nodeCore(t)
	n.useMemory(m)
	task, node := startInNode(t, n, runRequest{Title: "Lire", Prompt: "Lis la mémoire."})
	for _, id := range []string{"../memoire/faits/project-site", "project-site"} {
		if out, msg := callNodes(t, n.core, task.ID, "node_read", map[string]any{"id": id}); msg == "" {
			t.Errorf("a task's node_read %q read %v", id, out)
		}
	}
	if out := mustNodes(t, n.core, task.ID, "node_read", map[string]any{"id": node}); strings.Contains(fmtJSON(out), "zanzibar") {
		t.Errorf("a task's own node holds the mirror: %v", out)
	}
}

// « Ce que je sais » shows what the mirror holds, read only: no proposal,
// no forgotten nor past memory, the external one marked, nothing written.
func TestKnownView(t *testing.T) {
	tw, _ := newMemoryWeb(t)
	m := tw.c.mem
	mustAdd(t, m, &Memory{Kind: kindPerson, About: "person:sam", Text: "Sam, un collègue."})
	mustAdd(t, m, &Memory{Kind: kindFact, About: "project:site", Text: "Le serveur répond en 5xx.", SourceKind: srcTask, Trust: trustExternal})
	mustAdd(t, m, &Memory{Kind: kindFact, Text: "Une proposition.", Status: memProposed, SourceKind: srcExtraction, Trust: trustFirstParty})
	gone := mustAdd(t, m, &Memory{Kind: kindFact, Text: "Oublie ça."})
	m.forget(gone.ID, "test", "")
	mustAdd(t, m, &Memory{Kind: kindFact, Text: "Vrai autrefois.", ValidFrom: m.now().Add(-48 * time.Hour), ValidTo: m.now().Add(-time.Hour)})
	used := func() int {
		var n int
		m.db.QueryRow(`SELECT COALESCE(SUM(use_count), 0) FROM memories`).Scan(&n)
		return n
	}
	code, body, _ := tw.do("GET", "/api/known", nil, nil)
	var got struct {
		Files []knownView `json:"files"`
	}
	json.Unmarshal([]byte(body), &got)
	if code != http.StatusOK || len(got.Files) != 2 || got.Files[0].Section != "people" || got.Files[1].Path != "faits/project-site.md" ||
		!got.Files[1].Lines[0].External || got.Files[0].Lines[0].External {
		t.Fatalf("known: %d %s", code, body)
	}
	for _, s := range []string{"Une proposition", "Oublie ça", "Vrai autrefois"} {
		if strings.Contains(body, s) {
			t.Errorf("%q shown", s)
		}
	}
	if used() != 0 {
		t.Error("reading the view counted a use")
	}
	if code, _, _ := tw.do("POST", "/api/known", nil, nil); code == http.StatusOK || code == http.StatusNoContent {
		t.Errorf("a write to the view: %d", code)
	}
}

// A learned change undone leaves the mirror as well: the memory it added
// is forgotten, its line gone.
func TestMirrorFollowsUnlearn(t *testing.T) {
	c, _ := testMemoryCore(t)
	dir := filepath.Join(t.TempDir(), "memoire")
	c.mem.mirror = &memMirror{dir: dir}
	mustAdd(t, c.mem, &Memory{Kind: kindLesson, Text: "Toujours citer la source."})
	if !strings.Contains(mirrorFiles(t, dir)["profil.md"], "citer la source") {
		t.Fatal("the lesson not in the mirror")
	}
	if _, err := c.unlearn(learnedAll(t, c.mem)[0].ID, "owner", "", nil); err != nil {
		t.Fatal(err)
	}
	if files := mirrorFiles(t, dir); len(files) != 0 {
		t.Fatalf("after the undo: %v", files)
	}
}
