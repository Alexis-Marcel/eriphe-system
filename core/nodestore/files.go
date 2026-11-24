package nodestore

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
)

// A node's media: images a task gives it (a page it captured), or a file
// the user attached to a message and the brain kept with it (a photo, a
// PDF), kept in files/<id>/ of the repository, each a commit like any
// write. A block
// names one by its file's name (media:<name>); only the node's own are
// read. They are not versions of the node, and they stay with it when it
// is archived.

// Media limits: one file's bytes, and the files of a node.
const (
	MaxFile  = 5 << 20
	MaxFiles = 20
)

// FileExts are the extensions a media file may have: images, and PDF.
var FileExts = []string{"png", "jpg", "gif", "webp", "pdf"}

// fileName is a media file's name: med_, 32 hex digits, its extension.
var fileName = regexp.MustCompile(`^med_[0-9a-f]{32}\.(png|jpg|gif|webp|pdf)$`)

// ValidFileName tells whether name is a media file's name.
func ValidFileName(name string) bool { return fileName.MatchString(name) }

func filesDir(id string) string { return path.Join("files", id) }

// PutFile keeps a media file for a node, under a new name made from ext
// (one of FileExts), in one commit by author. It gives the name.
func (s *Store) PutFile(id, author, ext string, data []byte) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if author == "" {
		return "", errors.New("a write has an author")
	}
	if !slices.Contains(FileExts, ext) {
		return "", errors.New("a media file is an image (png, jpg, gif, webp) or a pdf")
	}
	if len(data) == 0 {
		return "", errors.New("an empty file")
	}
	if len(data) > MaxFile {
		return "", fmt.Errorf("a media file is %d MiB at most", MaxFile>>20)
	}
	sum, err := s.summary(id)
	if err != nil {
		return "", err
	}
	if !sum.Archived.IsZero() {
		return "", ErrArchived
	}
	have, err := os.ReadDir(filepath.Join(s.dir, filepath.FromSlash(filesDir(id))))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	if len(have) >= MaxFiles {
		return "", fmt.Errorf("a node keeps %d media files at most", MaxFiles)
	}
	b := make([]byte, 16)
	rand.Read(b)
	name := "med_" + hex.EncodeToString(b) + "." + ext
	hash, err := s.commit(fmt.Sprintf("file %s %s", id, name), author, s.opts.Now(),
		[]change{{path: path.Join(filesDir(id), name), data: data}}, nil)
	if err != nil {
		return "", err
	}
	if err := setIndexed(s.db, hash); err != nil {
		return "", err
	}
	return name, nil
}

// File is one of a node's media files; ErrNotFound for none.
func (s *Store) File(id, name string) ([]byte, error) {
	if !ValidFileName(name) {
		return nil, ErrNotFound
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, err := s.summary(id); err != nil {
		return nil, err
	}
	b, err := os.ReadFile(filepath.Join(s.dir, filepath.FromSlash(filesDir(id)), name))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNotFound
	}
	return b, err
}

// Files are the names of a node's media files, none for a node that has
// none.
func (s *Store) Files(id string) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, err := s.summary(id); err != nil {
		return nil, err
	}
	have, err := os.ReadDir(filepath.Join(s.dir, filepath.FromSlash(filesDir(id))))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	var names []string
	for _, f := range have {
		if ValidFileName(f.Name()) {
			names = append(names, f.Name())
		}
	}
	return names, nil
}
