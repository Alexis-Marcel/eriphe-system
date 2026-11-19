package nodestore

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/format/idxfile"
	"github.com/go-git/go-git/v5/plumbing/format/packfile"
	"github.com/go-git/go-git/v5/plumbing/revlist"
)

// Backup is the nightly copy: a git bundle of the repository and a copy of
// the index go into dir, nodes-<day>.bundle and nodes-<day>.db, the last
// keep days kept. Writes wait meanwhile.
//
// The repository is only read: no gc, no repack, no object removed. The
// bundle is written aside, read back whole and checked before it takes its
// name; the older copies are dropped only once the night's are made. A
// failed backup leaves the repository as it was, and the copies before it.
func (s *Store) Backup(dir string, keep int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	keep = max(keep, 1)
	day := s.opts.Now().Format("2006-01-02")
	if err := s.writeBundle(filepath.Join(dir, "nodes-"+day+".bundle")); err != nil && !errors.Is(err, errEmptyRepo) {
		return fmt.Errorf("bundle: %w", err)
	}
	db := filepath.Join(dir, "nodes-"+day+".db")
	os.Remove(db)
	if _, err := s.db.Exec(`VACUUM INTO ?`, db); err != nil {
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

var errEmptyRepo = errors.New("the graph has no commit yet")

// writeBundle writes the repository's bundle at path: through a temporary
// file, read back and checked (checkBundle) before it is renamed.
func (s *Store) writeBundle(path string) error {
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	w := bufio.NewWriter(f)
	head, objs, err := s.bundle(w)
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

// bundle writes the repository as a git bundle (v2): its branch and HEAD, and a
// pack of every object the branch reaches, encoded from the objects as
// they are. `git clone <file>` reads it back. It gives the branch's commit
// and the objects packed.
func (s *Store) bundle(w io.Writer) (plumbing.Hash, []plumbing.Hash, error) {
	head, err := s.repo.Head()
	if errors.Is(err, plumbing.ErrReferenceNotFound) {
		return plumbing.ZeroHash, nil, errEmptyRepo
	}
	if err != nil {
		return plumbing.ZeroHash, nil, err
	}
	objs, err := revlist.Objects(s.repo.Storer, []plumbing.Hash{head.Hash()}, nil)
	if err != nil {
		return plumbing.ZeroHash, nil, err
	}
	if _, err := io.WriteString(w, bundleHeader(head.Hash())); err != nil {
		return plumbing.ZeroHash, nil, err
	}
	if _, err := packfile.NewEncoder(w, s.repo.Storer, false).Encode(objs, 10); err != nil {
		return plumbing.ZeroHash, nil, err
	}
	return head.Hash(), objs, nil
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
