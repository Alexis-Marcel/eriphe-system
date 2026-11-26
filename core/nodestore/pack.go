package nodestore

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"time"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/format/idxfile"
	"github.com/go-git/go-git/v5/plumbing/format/packfile"
	"github.com/go-git/go-git/v5/plumbing/revlist"
	"github.com/go-git/go-git/v5/plumbing/storer"
)

// Packing: each version stores its file as a loose object, compressed
// alone. Pack puts every object a reference reaches into one pack, with
// deltas between a file's versions: git gc, without prune. go-git's own
// repack removes the loose objects before its pack is written whole, so a
// failure there loses the history; Pack does it in the order that loses
// nothing:
//
//  1. the pack and its index are written aside, read back whole (every
//     object inflates, the checksum holds, every object a reference reaches
//     is in it), and only then take their names;
//  2. the repository is opened again, and reads every object the branch
//     reaches;
//  3. only then are the loose copies of what the pack holds removed, and
//     the packs before it (what they hold that a reference reaches, the new
//     one holds too). An object no branch reaches (a write that stopped
//     before its commit) stays loose.
//
// A failure before 3 leaves the repository as it was, and the new pack
// beside the old objects at most; one during 3 leaves an object both
// loose and packed.

// PackLoose is how many loose objects the repository holds before Pack
// packs them.
const PackLoose = 500

// Pack packs the repository when it holds PackLoose loose objects or more;
// it tells whether it packed. Writes wait meanwhile.
func (s *Store) Pack() (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	loose, err := s.looseObjects()
	if err != nil || len(loose) < PackLoose {
		return false, err
	}
	return true, s.pack()
}

// looseObjects are the repository's loose objects.
func (s *Store) looseObjects() (map[plumbing.Hash]bool, error) {
	out := map[plumbing.Hash]bool{}
	ls, ok := s.repo.Storer.(storer.LooseObjectStorer)
	if !ok {
		return out, nil
	}
	return out, ls.ForEachObjectHash(func(h plumbing.Hash) error { out[h] = true; return nil })
}

// pack packs every object a reference reaches, the lock held.
func (s *Store) pack() error {
	refs, err := s.repo.References()
	if err != nil {
		return err
	}
	var tips []plumbing.Hash
	err = refs.ForEach(func(ref *plumbing.Reference) error {
		if ref.Type() == plumbing.HashReference {
			tips = append(tips, ref.Hash())
		}
		return nil
	})
	if err != nil || len(tips) == 0 {
		return err
	}
	objs, err := revlist.Objects(s.repo.Storer, tips, nil)
	if err != nil {
		return err
	}
	dir := filepath.Join(s.dir, ".git", "objects", "pack")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	name, err := writePack(s.repo, objs, append(objs, tips...), dir)
	if err != nil {
		return fmt.Errorf("pack: %w", err)
	}
	// The repository again, with its new pack: it must read the branch
	// whole before anything goes.
	if err := s.reopen(); err != nil {
		return fmt.Errorf("pack: %w", err)
	}
	loose, err := s.looseObjects()
	if err != nil {
		return err
	}
	ls, ok := s.repo.Storer.(storer.LooseObjectStorer)
	if !ok {
		return nil
	}
	for _, h := range objs {
		if loose[h] {
			if err := ls.DeleteLooseObject(h); err != nil {
				return fmt.Errorf("pack: %w", err)
			}
		}
	}
	if ps, ok := s.repo.Storer.(storer.PackedObjectStorer); ok {
		packs, err := ps.ObjectPacks()
		if err != nil {
			return err
		}
		for _, h := range packs {
			if h != name {
				if err := ps.DeleteOldObjectPackAndIndex(h, time.Time{}); err != nil {
					return fmt.Errorf("pack: %w", err)
				}
			}
		}
	}
	return s.reopen()
}

// reopen opens the repository again (go-git keeps the packs it found) and
// checks it reads every object a reference reaches.
func (s *Store) reopen() error {
	r, err := git.PlainOpen(s.dir)
	if err != nil {
		return err
	}
	if err := checkRepo(r); err != nil {
		return err
	}
	s.repo = r
	return nil
}

// writePack writes the pack of objs into dir, with its index: both
// written aside, the pack read back whole and checked to hold every one of
// want before either takes its name (pack-<checksum>). It gives the
// pack's checksum.
func writePack(r *git.Repository, objs, want []plumbing.Hash, dir string) (plumbing.Hash, error) {
	tmpPack := filepath.Join(dir, "tmp-eriphe.pack")
	tmpIdx := filepath.Join(dir, "tmp-eriphe.idx")
	defer os.Remove(tmpPack)
	defer os.Remove(tmpIdx)
	f, err := os.OpenFile(tmpPack, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return plumbing.ZeroHash, err
	}
	w := bufio.NewWriter(f)
	sum, err := packfile.NewEncoder(w, r.Storer, false).Encode(objs, 10)
	if err == nil {
		err = w.Flush()
	}
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return plumbing.ZeroHash, err
	}
	idx, err := checkPack(tmpPack, want)
	if err != nil {
		return plumbing.ZeroHash, err
	}
	g, err := os.OpenFile(tmpIdx, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return plumbing.ZeroHash, err
	}
	if _, err = idxfile.NewEncoder(g).Encode(idx); err == nil {
		err = g.Sync()
	}
	if cerr := g.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return plumbing.ZeroHash, err
	}
	base := filepath.Join(dir, "pack-"+sum.String())
	if err := os.Rename(tmpPack, base+".pack"); err != nil {
		return plumbing.ZeroHash, err
	}
	if err := os.Rename(tmpIdx, base+".idx"); err != nil {
		return plumbing.ZeroHash, err
	}
	return sum, nil
}

// checkPack reads a pack file back whole (every object inflates, the
// checksum holds) and gives its index, once it holds every one of want.
func checkPack(path string, want []plumbing.Hash) (*idxfile.MemoryIndex, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	idx := new(idxfile.Writer)
	p, err := packfile.NewParser(packfile.NewScanner(f), idx)
	if err != nil {
		return nil, fmt.Errorf("check: %w", err)
	}
	if _, err := p.Parse(); err != nil {
		return nil, fmt.Errorf("check: %w", err)
	}
	mi, err := idx.Index()
	if err != nil {
		return nil, fmt.Errorf("check: %w", err)
	}
	for _, h := range want {
		if ok, _ := mi.Contains(h); !ok {
			return nil, fmt.Errorf("check: object %s is not in the pack", h)
		}
	}
	return mi, nil
}
