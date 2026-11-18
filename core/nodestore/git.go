package nodestore

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/format/packfile"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/revlist"
)

// The graph's repository: go-git only, no git binary. Every write is one
// commit on main, made from the files written on disk. A commit says the
// node versions it makes in its message, one trailer a version
// ("Node: <id> <n>"), so that the index's versions are rebuilt from one
// walk of the log, never by asking git for a path's history.

const branch = "main"

// trailer starts a commit message's line that names a node's version.
const trailer = "Node: "

// openRepo opens the repository in dir, made when it is not one yet.
func openRepo(dir string) (*git.Repository, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	r, err := git.PlainOpen(dir)
	if errors.Is(err, git.ErrRepositoryNotExists) {
		return git.PlainInitWithOptions(dir, &git.PlainInitOptions{
			InitOptions: git.InitOptions{DefaultBranch: plumbing.NewBranchReferenceName(branch)},
		})
	}
	return r, err
}

// change is a file a commit writes: its content, or nil to remove it.
type change struct {
	path string // slash-separated, from the repository's root
	data []byte
}

// stamp is a node version a commit makes.
type stamp struct {
	id string
	n  int
}

// commit writes the files on disk and commits them, with their author and
// the versions they make. It gives the commit's hash.
func (s *Store) commit(subject, author string, at time.Time, changes []change, stamps []stamp) (string, error) {
	wt, err := s.repo.Worktree()
	if err != nil {
		return "", err
	}
	for _, c := range changes {
		full := filepath.Join(s.dir, filepath.FromSlash(c.path))
		if c.data == nil {
			if _, err := wt.Remove(c.path); err != nil {
				return "", err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
			return "", err
		}
		if err := writeFile(full, c.data); err != nil {
			return "", err
		}
		if err := wt.AddWithOptions(&git.AddOptions{Path: c.path, SkipStatus: true}); err != nil {
			return "", err
		}
	}
	var msg strings.Builder
	msg.WriteString(subject)
	if len(stamps) > 0 {
		msg.WriteString("\n\n")
		for _, st := range stamps {
			fmt.Fprintf(&msg, "%s%s %d\n", trailer, st.id, st.n)
		}
	}
	sig := &object.Signature{Name: author, Email: authorEmail(author), When: at}
	h, err := wt.Commit(msg.String(), &git.CommitOptions{Author: sig, Committer: sig, AllowEmptyCommits: true})
	if err != nil {
		return "", err
	}
	return h.String(), nil
}

// authorEmail is the address a commit's author is given: git needs one.
func authorEmail(author string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(author) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '.' || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	return b.String() + "@eriphe.invalid"
}

// writeFile writes a file whole, through a temporary one, so that a crash
// never leaves half of it.
func writeFile(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// fileAt is a file's content at a commit.
func (s *Store) fileAt(hash, path string) ([]byte, error) {
	c, err := s.repo.CommitObject(plumbing.NewHash(hash))
	if err != nil {
		return nil, fmt.Errorf("commit %s: %w", hash, err)
	}
	f, err := c.File(path)
	if err != nil {
		return nil, fmt.Errorf("%s at %s: %w", path, hash, err)
	}
	r, err := f.Reader()
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return io.ReadAll(r)
}

// headFiles are the blob hashes of the files at the root of the last
// commit; empty before the first one.
func (s *Store) headFiles() (map[string]plumbing.Hash, error) {
	out := map[string]plumbing.Hash{}
	head, err := s.repo.Head()
	if errors.Is(err, plumbing.ErrReferenceNotFound) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	c, err := s.repo.CommitObject(head.Hash())
	if err != nil {
		return nil, err
	}
	t, err := c.Tree()
	if err != nil {
		return nil, err
	}
	for _, e := range t.Entries {
		if e.Mode.IsFile() {
			out[e.Name] = e.Hash
		}
	}
	return out, nil
}

// blobHash is the hash git gives a file's content.
func blobHash(data []byte) string {
	return plumbing.ComputeHash(plumbing.BlobObject, data).String()
}

// loggedVersion is a node's version read in a commit's message.
type loggedVersion struct {
	id     string
	n      int
	hash   string
	author string
	at     time.Time
}

// logVersions reads every node version the log names, oldest first.
func (s *Store) logVersions() ([]loggedVersion, error) {
	head, err := s.repo.Head()
	if errors.Is(err, plumbing.ErrReferenceNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	it, err := s.repo.Log(&git.LogOptions{From: head.Hash()})
	if err != nil {
		return nil, err
	}
	var out []loggedVersion
	err = it.ForEach(func(c *object.Commit) error {
		sc := bufio.NewScanner(strings.NewReader(c.Message))
		for sc.Scan() {
			rest, ok := strings.CutPrefix(sc.Text(), trailer)
			if !ok {
				continue
			}
			id, num, _ := strings.Cut(rest, " ")
			n, err := strconv.Atoi(num)
			if err != nil || n < 1 {
				continue
			}
			out = append(out, loggedVersion{id: id, n: n, hash: c.Hash.String(), author: c.Author.Name, at: c.Author.When})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].at.Before(out[j].at) })
	return out, nil
}

// bundle writes the repository as a git bundle (v2): its branch, and a
// pack of every object it reaches. `git clone <file>` reads it back. Just
// after gc, the repository's one pack is that pack: it is copied as it is.
func (s *Store) bundle(w io.Writer) error {
	head, err := s.repo.Head()
	if errors.Is(err, plumbing.ErrReferenceNotFound) {
		return errEmptyRepo
	}
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "# v2 git bundle\n%s %s\n\n", head.Hash(), plumbing.NewBranchReferenceName(branch)); err != nil {
		return err
	}
	packs, _ := filepath.Glob(filepath.Join(s.dir, ".git", "objects", "pack", "pack-*.pack"))
	loose, _ := filepath.Glob(filepath.Join(s.dir, ".git", "objects", "??", "*"))
	if len(packs) == 1 && len(loose) == 0 {
		f, err := os.Open(packs[0])
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(w, f)
		return err
	}
	objs, err := revlist.Objects(s.repo.Storer, []plumbing.Hash{head.Hash()}, nil)
	if err != nil {
		return err
	}
	_, err = packfile.NewEncoder(w, s.repo.Storer, false).Encode(objs, 10)
	return err
}

var errEmptyRepo = errors.New("the graph has no commit yet")

// gc packs the repository's objects and drops those no commit reaches.
func (s *Store) gc() error {
	if err := s.repo.Prune(git.PruneOptions{Handler: s.repo.DeleteObject}); err != nil {
		return err
	}
	return s.repo.RepackObjects(&git.RepackConfig{})
}
