package nodestore

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// A node's trace: what its work said, entry after entry, never cut. It is
// not a version of the node, nor in the repository: a plain file of its
// own, <id>.trace.md in the traces' folder, each entry appended at its
// end (O_APPEND). Past MaxTraceFile, the file becomes its older part,
// <id>.trace.1.md (the one before dropped), and a new file begins. An
// archived node keeps its trace, as one reopened does.

// tracePath is a node's trace's file; part 1 is its older part.
func (s *Store) tracePath(id string, part int) string {
	if part > 0 {
		return filepath.Join(s.traces, fmt.Sprintf("%s.trace.%d.md", id, part))
	}
	return filepath.Join(s.traces, id+".trace.md")
}

// AppendTrace adds an entry at the end of a node's trace: when, who, and
// its text, never cut. No version is made; the node is changed now all
// the same (Summary.Updated).
func (s *Store) AppendTrace(id, author, text string) error {
	if strings.TrimSpace(author) == "" {
		return errors.New("a write has an author")
	}
	if len(text) > MaxTrace {
		return fmt.Errorf("a trace entry is %d KiB at most", MaxTrace>>10)
	}
	if err := s.refuse(text); err != nil {
		return err
	}
	if _, err := s.summary(id); err != nil {
		return err
	}
	now := s.opts.Now()
	entry := fmt.Sprintf("## %s · %s\n\n%s\n\n", now.Format(time.RFC3339), author, strings.TrimRight(text, "\n"))
	s.traceMu.Lock()
	err := s.appendTrace(id, entry)
	s.traceMu.Unlock()
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// The node moved: it is among the last changed (Recent).
	if _, err := s.db.Exec(`UPDATE nodes SET updated = ? WHERE id = ?`, now.Unix(), id); err != nil {
		return err
	}
	s.changed(ChangedTrace, id)
	return nil
}

// appendTrace writes an entry at the end of a node's trace's file, which
// first becomes its older part when the entry would take it past
// MaxTraceFile. The traces' lock is held.
func (s *Store) appendTrace(id, entry string) error {
	path := s.tracePath(id, 0)
	if fi, err := os.Stat(path); err == nil && fi.Size() > 0 && fi.Size()+int64(len(entry)) > MaxTraceFile {
		if err := os.Rename(path, s.tracePath(id, 1)); err != nil {
			return err
		}
	} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(entry); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// tracesMoved gives an index made again from the files when each node
// last moved: a trace appended to after its last version moved it too
// (the time of its file).
func (s *Store) tracesMoved() {
	entries, err := os.ReadDir(s.traces)
	if err != nil {
		return
	}
	for _, e := range entries {
		id, ok := strings.CutSuffix(e.Name(), ".trace.md")
		if !ok {
			continue
		}
		if fi, err := e.Info(); err == nil {
			s.db.Exec(`UPDATE nodes SET updated = MAX(updated, ?) WHERE id = ?`, fi.ModTime().Unix(), id)
		}
	}
}

// Trace is a node's trace, its older part first; empty while it has none.
func (s *Store) Trace(id string) ([]byte, error) {
	if _, err := s.summary(id); err != nil {
		return nil, err
	}
	s.traceMu.Lock()
	defer s.traceMu.Unlock()
	var out []byte
	for _, part := range []int{1, 0} {
		b, err := os.ReadFile(s.tracePath(id, part))
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
		out = append(out, b...)
	}
	return out, nil
}
