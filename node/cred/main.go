// eriphe-cred gives the standard tools of a task the credentials they ask
// for, when they ask for them, from Eriphe: it is git's credential helper
// and a wrapper of gh. It asks the task's node, with the task's own secret;
// the node asks eriphe-core, which decides (is the service lent to the task,
// is the repository allowed, read or write, does the user approve) and
// mints a short-lived token for that one repository. No long-lived secret
// is ever in the task.
//
//	eriphe-cred get|store|erase   git's credential helper protocol (stdin, stdout)
//	eriphe-cred gh ARGS...        runs the real gh with a token for this one command
//	gh ARGS...                    the same, when installed as gh ahead of the real one on PATH
//
// It reads ERIPHE_CRED_URL (the node's endpoint) and ERIPHE_TASK_SECRET,
// which the node gives every task.
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

var version = "dev"

const (
	envURL    = "ERIPHE_CRED_URL"
	envSecret = "ERIPHE_TASK_SECRET"
	envAccess = "ERIPHE_CRED_ACCESS" // forces read or write, for a tool that cannot tell
)

func main() {
	name := filepath.Base(os.Args[0])
	args := os.Args[1:]
	if name == "gh" {
		os.Exit(runGh(args, newClient(), osEnv{}))
	}
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: eriphe-cred get|store|erase (git credential helper), eriphe-cred gh ARGS..., eriphe-cred version")
		os.Exit(2)
	}
	switch args[0] {
	case "get", "store", "erase":
		os.Exit(runGit(args[0], os.Stdin, os.Stdout, os.Stderr, newClient(), gitAccess))
	case "gh":
		os.Exit(runGh(args[1:], newClient(), osEnv{}))
	case "version", "--version":
		fmt.Println("eriphe-cred", version)
	default:
		// git ignores the operations it does not know; so do we.
		os.Exit(0)
	}
}

// request is what the node is asked: a credential for a host and a path,
// to read or to write.
type request struct {
	Protocol string `json:"protocol,omitempty"`
	Host     string `json:"host"`
	Path     string `json:"path,omitempty"`
	Access   string `json:"access"`
	Tool     string `json:"tool,omitempty"` // git or gh, for the log
}

// answer is the node's: a credential, or why not.
type answer struct {
	Username string    `json:"username,omitempty"`
	Password string    `json:"password,omitempty"`
	Expires  time.Time `json:"expires,omitzero"`
	Reason   string    `json:"reason,omitempty"`
}

// errNoService says that Eriphe has no credential for this host: another
// helper, or none, may answer.
var errNoService = errors.New("no credential service for this host")

type refusedError struct{ reason string }

func (e *refusedError) Error() string { return "refused: " + e.reason }

type client struct {
	url, secret string
	http        *http.Client
}

func newClient() *client {
	// The node is reached directly, never through the task's egress relay.
	tr := &http.Transport{Proxy: nil}
	// An approval can wait for the user: as long as a permission does.
	return &client{url: os.Getenv(envURL), secret: os.Getenv(envSecret),
		http: &http.Client{Transport: tr, Timeout: 65 * time.Minute}}
}

func (c *client) configured() bool { return c.url != "" && c.secret != "" }

func (c *client) ask(req request) (*answer, error) {
	if !c.configured() {
		return nil, errNoService
	}
	body, _ := json.Marshal(req)
	r, err := http.NewRequest(http.MethodPost, c.url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	r.Header.Set("Authorization", "Bearer "+c.secret)
	r.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(r)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	var a answer
	json.Unmarshal(raw, &a)
	switch {
	case resp.StatusCode == http.StatusOK && a.Password != "":
		return &a, nil
	case resp.StatusCode == http.StatusNotFound:
		return nil, errNoService
	case resp.StatusCode == http.StatusForbidden:
		if a.Reason == "" {
			a.Reason = "denied"
		}
		return nil, &refusedError{a.Reason}
	default:
		msg := a.Reason
		if msg == "" {
			msg = strings.TrimSpace(string(raw))
		}
		return nil, fmt.Errorf("node: %d %s", resp.StatusCode, msg)
	}
}

// runGit speaks git's credential helper protocol: attributes as key=value
// lines on stdin, up to a blank line; for get, the credential on stdout.
// store and erase have nothing to do: nothing is kept, a token lasts an
// hour and the next get asks again.
func runGit(op string, stdin io.Reader, stdout, stderr io.Writer, c *client, access func() string) int {
	attrs := readAttrs(stdin)
	if op != "get" {
		return 0
	}
	if attrs["protocol"] != "https" || attrs["host"] == "" {
		return 0 // not ours: git goes on to the next helper, or none
	}
	req := request{Protocol: attrs["protocol"], Host: attrs["host"], Path: attrs["path"], Access: access(), Tool: "git"}
	a, err := c.ask(req)
	var refused *refusedError
	switch {
	case errors.Is(err, errNoService):
		return 0
	case errors.As(err, &refused):
		fmt.Fprintf(stderr, "eriphe: no %s access to %s/%s: %s\n", req.Access, req.Host, strings.TrimSuffix(req.Path, ".git"), refused.reason)
		if req.Path == "" {
			fmt.Fprintln(stderr, "eriphe: git did not name the repository: set credential.useHttpPath=true")
		}
		fmt.Fprintln(stdout, "quit=1") // no other helper, no prompt: the refusal stands
		return 0
	case err != nil:
		fmt.Fprintf(stderr, "eriphe: cannot get a credential for %s: %v\n", req.Host, err)
		return 0
	}
	fmt.Fprintf(stdout, "username=%s\n", a.Username)
	fmt.Fprintf(stdout, "password=%s\n", a.Password)
	if !a.Expires.IsZero() {
		fmt.Fprintf(stdout, "password_expiry_utc=%d\n", a.Expires.Unix())
	}
	return 0
}

// readAttrs reads key=value lines up to a blank line or the end.
func readAttrs(r io.Reader) map[string]string {
	attrs := map[string]string{}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		if line == "" {
			break
		}
		if k, v, ok := strings.Cut(line, "="); ok {
			attrs[k] = v
		}
	}
	return attrs
}

// gitAccess tells a push from a fetch: git's credential protocol does not
// say, so the helper looks at the git command that started it (its parent
// is git-remote-https, whose parent is git push, fetch, clone…).
func gitAccess() string {
	if v := os.Getenv(envAccess); v == "read" || v == "write" {
		return v
	}
	for _, args := range ancestors() {
		if access := accessOfGit(args); access != "" {
			return access
		}
	}
	return "read"
}

// accessOfGit reads a git command line: write for push and send-pack,
// read for any other git subcommand, "" for something else.
func accessOfGit(args []string) string {
	if len(args) == 0 {
		return ""
	}
	base := filepath.Base(args[0])
	switch base {
	case "git-receive-pack", "git-send-pack":
		return "write"
	case "git-remote-https", "git-remote-http":
		return "" // look further up
	}
	if base != "git" {
		return ""
	}
	for i := 1; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-C" || a == "-c" || a == "--git-dir" || a == "--work-tree" || a == "--namespace":
			i++ // an option and its value
		case strings.HasPrefix(a, "-"):
		case a == "push" || a == "send-pack":
			return "write"
		case strings.HasPrefix(a, "remote-"):
			return "" // git remote-https, run by git push or fetch: look further up
		default:
			return "read"
		}
	}
	return "read"
}

// ancestors are the command lines of the helper's parent processes, the
// nearest first (Linux /proc; none elsewhere).
var ancestors = func() [][]string {
	var out [][]string
	pid := os.Getppid()
	for range 8 {
		if pid <= 1 {
			break
		}
		raw, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/cmdline")
		if err != nil {
			break
		}
		out = append(out, strings.Split(strings.TrimRight(string(raw), "\x00"), "\x00"))
		stat, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
		if err != nil {
			break
		}
		// pid (comm) state ppid …: comm may hold spaces, so after the last ')'.
		s := string(stat)
		fields := strings.Fields(s[strings.LastIndexByte(s, ')')+1:])
		if len(fields) < 2 {
			break
		}
		pid, _ = strconv.Atoi(fields[1])
	}
	return out
}

// repoOfURL reads owner/name from a GitHub remote: https://github.com/o/r.git,
// git@github.com:o/r.git, ssh://git@github.com/o/r, or o/r itself.
func repoOfURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	path := raw
	if u, err := url.Parse(raw); err == nil && u.Scheme != "" && u.Host != "" {
		if !isGitHub(u.Hostname()) {
			return ""
		}
		path = u.Path
	} else if host, p, ok := strings.Cut(raw, ":"); ok && strings.Contains(host, "@") {
		_, h, _ := strings.Cut(host, "@")
		if !isGitHub(h) {
			return ""
		}
		path = p
	} else if parts := strings.Split(strings.Trim(raw, "/"), "/"); len(parts) == 3 {
		// HOST/OWNER/REPO, as gh's -R takes it.
		if !isGitHub(parts[0]) {
			return ""
		}
		path = parts[1] + "/" + parts[2]
	}
	parts := strings.Split(strings.Trim(strings.TrimSuffix(strings.Trim(path, "/"), ".git"), "/"), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return ""
	}
	return parts[0] + "/" + parts[1]
}

func isGitHub(host string) bool {
	host = strings.ToLower(host)
	return host == "github.com" || host == "www.github.com" || host == "api.github.com"
}
