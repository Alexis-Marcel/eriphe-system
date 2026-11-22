package nodestore

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/format/idxfile"
	"github.com/go-git/go-git/v5/plumbing/format/packfile"
	"github.com/go-git/go-git/v5/plumbing/revlist"
)

// Backup is the nightly copy: a git bundle of the repository and a copy of
// the index go into dir, nodes-<day>.bundle and nodes-<day>.db, the last
// keep days kept. Writes go on meanwhile: the store's lock is held only to
// read the branch's commit; the bundle packs what that commit reaches,
// objects no write changes, through a handle of its own on the repository.
// The index is copied first (VACUUM INTO, a snapshot of its own), so it
// is never ahead of the bundle: put back, it is behind at most, and the
// store reads the versions it lacks from the log (reconcile).
//
// The repository is only read: no gc, no repack, no object removed. The
// bundle is written aside, read back and checked before it takes its
// name; the index's copy takes its name last, so that nodes-<day>.db says
// the night's backup was made whole. The older copies are dropped only once the night's are made. A failed
// backup leaves the repository as it was, and the copies before it.
func (s *Store) Backup(dir string, keep int) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	keep = max(keep, 1)
	day := s.opts.Now().Format("2006-01-02")
	db := filepath.Join(dir, "nodes-"+day+".db")
	tmp := db + ".tmp"
	os.Remove(tmp)
	defer os.Remove(tmp)
	if _, err := s.db.Exec(`VACUUM INTO ?`, tmp); err != nil {
		return fmt.Errorf("index: %w", err)
	}
	s.mu.RLock()
	head, err := s.repo.Head()
	s.mu.RUnlock()
	switch {
	case errors.Is(err, plumbing.ErrReferenceNotFound): // no commit yet: no bundle
	case err != nil:
		return fmt.Errorf("bundle: %w", err)
	default:
		r, err := git.PlainOpen(s.dir)
		if err != nil {
			return fmt.Errorf("bundle: %w", err)
		}
		if err := writeBundle(r, head.Hash(), filepath.Join(dir, "nodes-"+day+".bundle")); err != nil {
			return fmt.Errorf("bundle: %w", err)
		}
	}
	os.Remove(db)
	if err := os.Rename(tmp, db); err != nil {
		return fmt.Errorf("index: %w", err)
	}
	for _, pattern := range []string{"nodes-*.bundle", "nodes-*.db"} {
		files, _ := filepath.Glob(filepath.Join(dir, pattern))
		sort.Strings(files)
		for len(files) > keep {
			os.Remove(files[0])
			files = files[1:]
		}
	}
	return nil
}

// writeBundle writes the bundle of r at its commit head at path: through
// a temporary file, read back and checked (checkBundle) before it is
// renamed.
func writeBundle(r *git.Repository, head plumbing.Hash, path string) error {
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	w := bufio.NewWriter(f)
	objs, err := bundle(r, head, w)
	if err == nil {
		err = w.Flush()
	}
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = checkBundle(tmp, head, objs)
	}
	if err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}

// bundle writes r at its commit head as a git bundle (v2): its branch and
// HEAD, and a pack of every object head reaches, encoded from the objects
// as they are. `git clone <file>` reads it back. It gives the objects
// packed.
func bundle(r *git.Repository, head plumbing.Hash, w io.Writer) ([]plumbing.Hash, error) {
	objs, err := revlist.Objects(r.Storer, []plumbing.Hash{head}, nil)
	if err != nil {
		return nil, err
	}
	if _, err := io.WriteString(w, bundleHeader(head)); err != nil {
		return nil, err
	}
	if _, err := packfile.NewEncoder(w, r.Storer, false).Encode(objs, 10); err != nil {
		return nil, err
	}
	return objs, nil
}

// bundleHeader names the branch and HEAD, so that a clone checks it out.
func bundleHeader(head plumbing.Hash) string {
	return fmt.Sprintf("# v2 git bundle\n%s %s\n%s HEAD\n\n", head, plumbing.NewBranchReferenceName(branch), head)
}

// checkBundle reads a bundle file back whole: its header names head, its
// pack parses (every object inflates, the checksum holds) and holds every
// one of objs.
func checkBundle(path string, head plumbing.Hash, objs []plumbing.Hash) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	header := bundleHeader(head)
	got := make([]byte, len(header))
	if _, err := io.ReadFull(f, got); err != nil || string(got) != header {
		return fmt.Errorf("check: the header does not read back")
	}
	fi, err := f.Stat()
	if err != nil {
		return err
	}
	pack := io.NewSectionReader(f, int64(len(header)), fi.Size()-int64(len(header)))
	idx := new(idxfile.Writer)
	p, err := packfile.NewParser(packfile.NewScanner(pack), idx)
	if err != nil {
		return fmt.Errorf("check: %w", err)
	}
	if _, err := p.Parse(); err != nil {
		return fmt.Errorf("check: %w", err)
	}
	mi, err := idx.Index()
	if err != nil {
		return fmt.Errorf("check: %w", err)
	}
	if n, _ := mi.Count(); n != int64(len(objs)) {
		return fmt.Errorf("check: %d objects packed, %d expected", n, len(objs))
	}
	for _, h := range append(objs, head) {
		if ok, _ := mi.Contains(h); !ok {
			return fmt.Errorf("check: object %s is not in the pack", h)
		}
	}
	return nil
}
