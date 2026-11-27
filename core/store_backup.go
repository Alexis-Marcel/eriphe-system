package main

import (
	"errors"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// The nightly copy of core.db: the intentions live in it, with the cards,
// the tasks and the conversation. It goes next to the graph's backup
// (nodes-<day>.*), as core-<day>.db, the same number of days kept.

// storeNight copies core.db into the backup folder once a night, from the
// backup's hour (extract_at, 03:00 by default): a copy of the day there
// says it is made. One that failed is tried again at the next minute.
func (c *core) storeNight(now time.Time) {
	b := c.conf().backupConfig()
	if now.Before(b.nightly(now)) {
		return
	}
	made, err := c.store.backup(b.BackupDir, b.BackupKeep, now)
	switch {
	case err != nil:
		log.Printf("core.db: backup: %v", err)
	case made:
		log.Printf("core.db: copied into %s", b.BackupDir)
	}
}

// backup writes core-<day>.db into dir, unless it is there already, and
// keeps the last keep copies. The copy is a snapshot (VACUUM INTO) written
// aside, then renamed: core-<day>.db is whole or absent. It tells whether
// it made one.
func (s *Store) backup(dir string, keep int, now time.Time) (bool, error) {
	path := filepath.Join(dir, "core-"+now.Format("2006-01-02")+".db")
	if _, err := os.Stat(path); err == nil {
		return false, nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return false, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return false, err
	}
	tmp := path + ".tmp"
	os.Remove(tmp)
	defer os.Remove(tmp)
	if _, err := s.db.Exec(`VACUUM INTO ?`, tmp); err != nil {
		return false, err
	}
	if err := os.Chmod(tmp, 0o600); err != nil {
		return false, err
	}
	if err := os.Rename(tmp, path); err != nil {
		return false, err
	}
	files, _ := filepath.Glob(filepath.Join(dir, "core-*.db"))
	sort.Strings(files)
	for len(files) > max(keep, 1) {
		os.Remove(files[0])
		files = files[1:]
	}
	return true, nil
}
