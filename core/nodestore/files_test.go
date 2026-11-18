package nodestore

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	git "github.com/go-git/go-git/v5"
)

// A node's media file is one commit under files/<id>/, read back by its
// name for that node only; a restart leaves it alone.
func TestNodeFiles(t *testing.T) {
	e := newEnv(t)
	s, _ := e.open(t)
	mustCreate(t, s, New{Title: "Prod"})
	mustCreate(t, s, New{Title: "Other"})
	png := []byte("\x89PNG\r\n\x1a\nimage")
	name, err := s.PutFile("n1", "task:t1", "png", png)
	if err != nil || !ValidFileName(name) || !strings.HasSuffix(name, ".png") {
		t.Fatalf("put: %q %v", name, err)
	}
	if got, err := s.File("n1", name); err != nil || !bytes.Equal(got, png) {
		t.Fatalf("read: %v", err)
	}
	if _, err := os.Stat(filepath.Join(e.dir, "files", "n1", name)); err != nil {
		t.Fatal("not under files/n1/")
	}
	// Another node's, or no file at all, is not found.
	for _, c := range [][2]string{{"n2", name}, {"n1", "med_00000000000000000000000000000000.png"}, {"n1", "../prod-n1.md"}, {"n9", name}} {
		if _, err := s.File(c[0], c[1]); err == nil {
			t.Errorf("%s/%s read", c[0], c[1])
		}
	}
	// One commit, by its author, the index at its head.
	r, _ := git.PlainOpen(e.dir)
	head, _ := r.Head()
	c, _ := r.CommitObject(head.Hash())
	if c.Author.Name != "task:t1" {
		t.Fatalf("commit by %s", c.Author.Name)
	}
	if _, err := c.File("files/n1/" + name); err != nil {
		t.Fatal("not committed")
	}
	if v, _ := s.Versions("n1"); len(v) != 1 {
		t.Fatalf("a file made a version: %+v", v)
	}
	s.Close()
	s2, rep := e.open(t)
	if !reflect.DeepEqual(rep, Report{}) {
		t.Fatalf("restart: %+v", rep)
	}
	if h, _ := r.Head(); h.Hash() != head.Hash() {
		t.Fatal("the restart committed")
	}
	if _, err := s2.File("n1", name); err != nil {
		t.Fatal("lost on restart")
	}
	// Archived, it keeps its files and takes no more.
	if err := s2.Archive("n1", "user"); err != nil {
		t.Fatal(err)
	}
	if _, err := s2.File("n1", name); err != nil {
		t.Fatal("lost when archived")
	}
	if _, err := s2.PutFile("n1", "task:t1", "png", png); !errors.Is(err, ErrArchived) {
		t.Fatalf("put on an archived node: %v", err)
	}
}

func TestNodeFilesLimits(t *testing.T) {
	e := newEnv(t)
	s, _ := e.open(t)
	mustCreate(t, s, New{Title: "Prod"})
	for _, c := range []struct {
		ext  string
		data []byte
	}{{"gif", []byte("x")}, {"png", nil}, {"png", make([]byte, MaxFile+1)}} {
		if _, err := s.PutFile("n1", "task:t1", c.ext, c.data); err == nil {
			t.Errorf("%s of %d bytes kept", c.ext, len(c.data))
		}
	}
	if _, err := s.PutFile("n9", "task:t1", "png", []byte("x")); !errors.Is(err, ErrNotFound) {
		t.Errorf("no such node: %v", err)
	}
	for range MaxFiles {
		if _, err := s.PutFile("n1", "task:t1", "jpg", []byte("x")); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.PutFile("n1", "task:t1", "jpg", []byte("x")); err == nil {
		t.Fatal("more than MaxFiles kept")
	}
}
