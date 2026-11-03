package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sync"
)

// A container's stream, between eriphe-node engine and the node, and from
// the Docker daemon to the engine: frames of 8 bytes of header, its kind
// then 3 zero bytes then its size (big-endian), and the bytes. The Docker
// daemon frames a container's output the same way, kinds 1 and 2; the
// engine adds 0 (the node's input; an empty one closes it) and 3 (the end:
// the container's exit, in JSON).
const (
	frameStdin  = 0
	frameStdout = 1
	frameStderr = 2
	frameEnd    = 3

	maxFrame = 1 << 20
)

// streamEnd is the last frame of a container's stream: its exit code, or
// why it could not run.
type streamEnd struct {
	Exit  int    `json:"exit"`
	Error string `json:"error,omitempty"`
}

// readFrame reads one frame.
func readFrame(r io.Reader) (kind byte, p []byte, err error) {
	var h [8]byte
	if _, err := io.ReadFull(r, h[:]); err != nil {
		return 0, nil, err
	}
	size := binary.BigEndian.Uint32(h[4:])
	if size > maxFrame {
		return 0, nil, fmt.Errorf("frame of %d bytes", size)
	}
	p = make([]byte, size)
	if _, err := io.ReadFull(r, p); err != nil {
		return 0, nil, err
	}
	return h[0], p, nil
}

// frameWriter writes frames, whole, from several goroutines.
type frameWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (f *frameWriter) frame(kind byte, p []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for first := true; first || len(p) > 0; first = false {
		n := min(len(p), maxFrame)
		var h [8]byte
		h[0] = kind
		binary.BigEndian.PutUint32(h[4:], uint32(n))
		if _, err := f.w.Write(append(h[:], p[:n]...)); err != nil {
			return err
		}
		p = p[n:]
	}
	return nil
}

// stdinWriter is a container's input over a frameWriter: Close sends the
// empty frame that closes it.
type stdinWriter struct {
	f      *frameWriter
	once   sync.Once
	closed bool
}

func (s *stdinWriter) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	s.f.mu.Lock()
	closed := s.closed
	s.f.mu.Unlock()
	if closed {
		return 0, errors.New("write to a closed input")
	}
	if err := s.f.frame(frameStdin, p); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (s *stdinWriter) Close() error {
	var err error
	s.once.Do(func() {
		err = s.f.frame(frameStdin, nil)
		s.f.mu.Lock()
		s.closed = true
		s.f.mu.Unlock()
	})
	return err
}
