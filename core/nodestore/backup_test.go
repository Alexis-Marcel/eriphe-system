package nodestore

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// longFrench is a body of about n bytes of accented text, many bytes a
// rune, as a long note in French is.
func longFrench(n int, round int) string {
	var b strings.Builder
	for i := 0; b.Len() < n; i++ {
		fmt.Fprintf(&b, "Ébauche %d.%d : les élèves relisent « déjà vu », ça coûte où ? Œuvre, naïveté, garçon, août.\n", round, i)
	}
	return b.String()
}

// manifest is every file under the repository's .git, with its content's
// hash: two equal manifests are the same repository, byte for byte.
func manifest(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(filepath.Join(dir, ".git"), func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out[path] = fmt.Sprintf("%x", sha256.Sum256(raw))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func sameManifest(t *testing.T, before, after map[string]string) {
	t.Helper()
	for p, h := range before {
		if after[p] != h {
			t.Errorf("%s changed or went away", p)
		}
	}
	for p := range after {
		if _, ok := before[p]; !ok {
			t.Errorf("%s appeared", p)
		}
	}
}

// largeGraph is a store with a long French node written several times.
func largeGraph(t *testing.T) (*testEnv, *Store, []Version) {
	t.Helper()
	e := newEnv(t)
	s, _ := e.open(t)
	d := mustCreate(t, s, New{Title: "Le long brouillon", Body: longFrench(300<<10, 0)})
	for i := 1; i <= 4; i++ {
		var err error
		if d, err = s.Write("n1", longFrench(300<<10, i), d.Version.Hash, "brain"); err != nil {
			t.Fatal(err)
		}
	}
	mustCreate(t, s, New{Title: "À côté", Body: "Une courte note."})
	vs, _ := s.Versions("n1")
	if len(vs) != 5 {
		t.Fatalf("versions: %d", len(vs))
	}
	return e, s, vs
}

// The night's backup only reads the repository: with a long multi-byte
// file and several versions, the repository stays byte for byte as it
// was and whole, and the bundle gives back every version.
func TestBackupKeepsALargeRepositoryWhole(t *testing.T) {
	e, s, vs := largeGraph(t)
	before := manifest(t, e.dir)
	backups := filepath.Join(t.TempDir(), "backups")
	for range 2 {
		if err := s.Backup(backups, 3); err != nil {
			t.Fatal(err)
		}
	}
	sameManifest(t, before, manifest(t, e.dir))
	if err := checkRepo(s.repo); err != nil {
		t.Fatal(err)
	}
	bundle := filepath.Join(backups, "nodes-2026-11-17.bundle")
	raw, err := os.ReadFile(bundle)
	if err != nil {
		t.Fatal(err)
	}
	got := decodeBundle(t, raw)
	d := mustRead(t, s, "n1")
	for i, v := range vs {
		c, err := object.GetCommit(got, plumbing.NewHash(v.Hash))
		if err != nil {
			t.Fatalf("the bundle lacks v%d: %v", v.N, err)
		}
		f, err := c.File(d.File)
		if err != nil {
			t.Fatalf("v%d: %v", v.N, err)
		}
		if text, _ := f.Contents(); !strings.Contains(text, fmt.Sprintf("Ébauche %d.0 :", i)) {
			t.Fatalf("v%d does not read back", v.N)
		}
	}
	// The store still reads and writes.
	if old, err := s.Read("n1", 1); err != nil || !strings.HasPrefix(old.Node.Text(), "Ébauche 0.0") {
		t.Fatalf("after the backup: %v", err)
	}
	if _, err := s.Write("n1", "Plus court.", d.Version.Hash, "brain"); err != nil {
		t.Fatalf("a write after the backup: %v", err)
	}

	// With git at hand: the repository passes fsck and the bundle clones.
	gitBin, err := exec.LookPath("git")
	if err != nil {
		t.Log("no git binary: fsck and clone not checked")
		return
	}
	run := func(dir string, args ...string) string {
		cmd := exec.Command(gitBin, args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return string(out)
	}
	run(e.dir, "fsck", "--full", "--strict")
	clone := filepath.Join(t.TempDir(), "clone")
	run(t.TempDir(), "clone", "-q", bundle, clone)
	run(clone, "fsck", "--full", "--strict")
	if n := strings.TrimSpace(run(clone, "rev-list", "--count", "HEAD")); n != fmt.Sprint(len(vs)+1) {
		t.Fatalf("the clone has %s commits", n)
	}
}

// A backup that fails leaves the repository as it was, and the copies
// made before it.
func TestFailedBackupLeavesTheRepository(t *testing.T) {
	e, s, _ := largeGraph(t)
	backups := filepath.Join(t.TempDir(), "backups")
	os.MkdirAll(backups, 0o700)
	older := filepath.Join(backups, "nodes-2026-11-10.bundle")
	os.WriteFile(older, []byte("last week's"), 0o600)
	before := manifest(t, e.dir)

	// The bundle cannot be written: its temporary name is taken.
	blocked := filepath.Join(backups, "nodes-2026-11-17.bundle.tmp")
	os.MkdirAll(filepath.Join(blocked, "x"), 0o700)
	if err := s.Backup(backups, 1); err == nil {
		t.Fatal("a backup whose bundle cannot be written")
	}
	sameManifest(t, before, manifest(t, e.dir))
	if _, err := os.Stat(older); err != nil {
		t.Fatal("the older copy went away")
	}
	os.RemoveAll(blocked)

	// The index's copy cannot be made.
	os.MkdirAll(filepath.Join(backups, "nodes-2026-11-17.db", "x"), 0o700)
	if err := s.Backup(backups, 1); err == nil {
		t.Fatal("a backup whose index copy cannot be made")
	}
	sameManifest(t, before, manifest(t, e.dir))
	if _, err := os.Stat(older); err != nil {
		t.Fatal("the older copy went away")
	}

	// The pack's writing fails midway.
	if _, _, err := s.bundle(&failingWriter{left: 4096}); err == nil {
		t.Fatal("a bundle written to a failing writer")
	}
	sameManifest(t, before, manifest(t, e.dir))
	if err := checkRepo(s.repo); err != nil {
		t.Fatal(err)
	}
}

type failingWriter struct{ left int }

func (w *failingWriter) Write(p []byte) (int, error) {
	if len(p) > w.left {
		n := w.left
		w.left = 0
		return n, errors.New("disk full")
	}
	w.left -= len(p)
	return len(p), nil
}

// A bundle that does not read back whole is refused and not kept.
func TestCheckBundleRefusesACorruptOne(t *testing.T) {
	_, s, _ := largeGraph(t)
	path := filepath.Join(t.TempDir(), "nodes.bundle")
	if err := s.writeBundle(path); err != nil {
		t.Fatal(err)
	}
	head, objs, err := s.bundle(io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if err := checkBundle(path, head, objs); err != nil {
		t.Fatalf("a good bundle: %v", err)
	}
	raw, _ := os.ReadFile(path)
	raw[len(raw)/2] ^= 0x55
	os.WriteFile(path, raw, 0o600)
	if err := checkBundle(path, head, objs); err == nil {
		t.Fatal("a corrupt bundle passed the check")
	}
	if err := checkBundle(path, head, append(objs, plumbing.NewHash("0123456789abcdef0123456789abcdef01234567"))); err == nil {
		t.Fatal("a bundle lacking an object passed the check")
	}
}

// A repository that lacks an object its branch reaches does not open: the
// error says so, and names it.
func TestOpenRefusesABrokenRepository(t *testing.T) {
	for _, what := range []string{"an old version's blob", "the last commit"} {
		t.Run(what, func(t *testing.T) {
			e := newEnv(t)
			s, _ := e.open(t)
			d := mustCreate(t, s, New{Title: "Plan", Body: "Un."})
			d2, err := s.Write("n1", "Deux.", d.Version.Hash, "brain")
			if err != nil {
				t.Fatal(err)
			}
			s.Close()
			gone := plumbing.NewHash(blobHash(d.Raw))
			if what == "the last commit" {
				gone = plumbing.NewHash(d2.Version.Hash)
			}
			h := gone.String()
			if err := os.Remove(filepath.Join(e.dir, ".git", "objects", h[:2], h[2:])); err != nil {
				t.Fatal(err)
			}
			_, _, err = Open(e.dir, e.index, e.opts)
			if !errors.Is(err, ErrBroken) || !strings.Contains(err.Error(), h) || !strings.Contains(err.Error(), "refs/heads/main") {
				t.Fatalf("open: %v", err)
			}
		})
	}
}
