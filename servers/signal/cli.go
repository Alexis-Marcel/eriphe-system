package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"time"
)

// signal-cli, run in JSON-RPC mode on its stdin and stdout, in
// multi-account mode (no -a), which adds register and verify: one request
// per line, its response matched by id, and the messages it receives as
// "receive" notifications (signal-cli-jsonrpc(5)). The process is kept
// running: one that ends is started again, after a wait that doubles from
// a second to a minute, and every request then waiting fails.

var (
	backoffMin = time.Second
	backoffMax = time.Minute
)

// proc is a running signal-cli: what is written to it, what it writes, and
// how to wait for and stop it.
type proc struct {
	in   io.WriteCloser
	out  io.Reader
	wait func() error
	kill func()
}

// starter starts signal-cli; tests start a fake one.
type starter func() (*proc, error)

// execStarter starts the signal-cli binary, its data under dataDir.
func execStarter(bin, dataDir string) starter {
	return func() (*proc, error) {
		cmd := exec.Command(bin, "--output=json", "jsonRpc")
		// signal-cli keeps its data under $XDG_DATA_HOME/signal-cli.
		cmd.Env = append(os.Environ(), "XDG_DATA_HOME="+dataDir, "HOME="+dataDir)
		cmd.Stderr = os.Stderr
		in, err := cmd.StdinPipe()
		if err != nil {
			return nil, err
		}
		out, err := cmd.StdoutPipe()
		if err != nil {
			return nil, err
		}
		if err := cmd.Start(); err != nil {
			return nil, err
		}
		return &proc{in: in, out: out, wait: cmd.Wait, kill: func() { cmd.Process.Kill() }}, nil
	}
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string { return e.Message }

type rpcResponse struct {
	Result json.RawMessage `json:"result"`
	Error  *rpcError       `json:"error"`
}

// cli is signal-cli at work.
type cli struct {
	start   starter
	onEvent func(params json.RawMessage) // a "receive" notification's params

	// missing says why signal-cli cannot run here at all (none for this
	// architecture): every call fails with it, at once.
	missing string

	mu      sync.Mutex
	p       *proc
	next    int64
	waiting map[string]chan rpcResponse
	ready   chan struct{} // closed while a process runs; a new one while none does
}

var errNotRunning = errors.New("signal-cli is starting")

func newCLI(start starter, onEvent func(json.RawMessage)) *cli {
	return &cli{start: start, onEvent: onEvent, waiting: map[string]chan rpcResponse{}, ready: make(chan struct{})}
}

// run keeps signal-cli running until ctx ends.
func (c *cli) run(ctx context.Context) {
	if c.missing != "" {
		log.Print(c.missing)
		return
	}
	wait := time.Duration(0)
	for ctx.Err() == nil {
		if wait > 0 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(wait):
			}
		}
		began := time.Now()
		err := c.once(ctx)
		if ctx.Err() != nil {
			return
		}
		if time.Since(began) > backoffMax {
			wait = 0 // it ran a while: start it again at once
		}
		wait = min(max(wait*2, backoffMin), backoffMax)
		log.Printf("signal-cli stopped (%v); again in %s", err, wait)
	}
}

// once runs one signal-cli until it ends.
func (c *cli) once(ctx context.Context) error {
	p, err := c.start()
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.p = p
	close(c.ready)
	c.mu.Unlock()
	stop := context.AfterFunc(ctx, p.kill)
	defer stop()

	sc := bufio.NewScanner(p.out)
	sc.Buffer(make([]byte, 64<<10), 16<<20)
	for sc.Scan() {
		c.line(sc.Bytes())
	}
	p.in.Close()
	err = p.wait()

	c.mu.Lock()
	c.gone(p)
	for id, ch := range c.waiting {
		ch <- rpcResponse{Error: &rpcError{Message: "signal-cli stopped"}}
		delete(c.waiting, id)
	}
	c.mu.Unlock()
	if err == nil {
		err = errors.New("exited")
	}
	return err
}

// line is one line signal-cli wrote: a response, or a notification.
func (c *cli) line(raw []byte) {
	var m struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
		rpcResponse
	}
	if json.Unmarshal(raw, &m) != nil {
		return
	}
	if m.Method == "receive" {
		if c.onEvent != nil {
			c.onEvent(m.Params)
		}
		return
	}
	id, err := strconv.Unquote(string(m.ID))
	if err != nil {
		id = string(m.ID)
	}
	c.mu.Lock()
	ch := c.waiting[id]
	delete(c.waiting, id)
	c.mu.Unlock()
	if ch != nil {
		ch <- m.rpcResponse
	}
}

// up waits, up to ctx, for signal-cli to run.
func (c *cli) up(ctx context.Context) error {
	c.mu.Lock()
	ready := c.ready
	c.mu.Unlock()
	select {
	case <-ready:
		return nil
	case <-ctx.Done():
		return errNotRunning
	}
}

// call calls a method and decodes its result into out (when not nil).
func (c *cli) call(ctx context.Context, method string, params, out any) error {
	if c.missing != "" {
		return errors.New(c.missing)
	}
	wctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	err := c.up(wctx)
	cancel()
	if err != nil {
		return err
	}
	ch := make(chan rpcResponse, 1)
	c.mu.Lock()
	if c.p == nil {
		c.mu.Unlock()
		return errNotRunning
	}
	c.next++
	id := strconv.FormatInt(c.next, 10)
	req, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	c.waiting[id] = ch
	_, err = c.p.in.Write(append(req, '\n'))
	c.mu.Unlock()
	if err != nil {
		c.forget(id)
		return fmt.Errorf("signal-cli: %w", err)
	}
	select {
	case r := <-ch:
		if r.Error != nil {
			return r.Error
		}
		if out != nil && len(r.Result) > 0 {
			return json.Unmarshal(r.Result, out)
		}
		return nil
	case <-ctx.Done():
		c.forget(id)
		return ctx.Err()
	}
}

func (c *cli) forget(id string) {
	c.mu.Lock()
	delete(c.waiting, id)
	c.mu.Unlock()
}

// restart stops the running signal-cli; run starts it again at once. A
// newly registered account is received from once signal-cli starts with
// it.
func (c *cli) restart() {
	c.mu.Lock()
	p := c.p
	c.gone(p) // a call from now on waits for the next one
	c.mu.Unlock()
	if p != nil {
		p.kill()
	}
}

// gone forgets p as the running signal-cli, if it still is; c.mu is held.
func (c *cli) gone(p *proc) {
	if p != nil && c.p == p {
		c.p = nil
		c.ready = make(chan struct{})
	}
}
