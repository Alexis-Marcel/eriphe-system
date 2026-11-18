package nodestore

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// Backup is the nightly copy: the repository is packed (gc), then a git
// bundle of it and a copy of the index go into dir, nodes-<day>.bundle and
// nodes-<day>.db, the last keep days kept. Writes wait meanwhile.
func (s *Store) Backup(dir string, keep int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	keep = max(keep, 1)
	if err := s.gc(); err != nil {
		return fmt.Errorf("gc: %w", err)
	}
	day := s.opts.Now().Format("2006-01-02")
	bundle := filepath.Join(dir, "nodes-"+day+".bundle")
	f, err := os.OpenFile(bundle+".tmp", os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	err = s.bundle(f)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	switch {
	case errors.Is(err, errEmptyRepo):
		os.Remove(bundle + ".tmp")
	case err != nil:
		os.Remove(bundle + ".tmp")
		return err
	default:
		if err := os.Rename(bundle+".tmp", bundle); err != nil {
			return err
		}
	}
	db := filepath.Join(dir, "nodes-"+day+".db")
	os.Remove(db)
	if _, err := s.db.Exec(`VACUUM INTO ?`, db); err != nil {
		return err
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
