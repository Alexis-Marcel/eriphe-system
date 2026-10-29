package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// The gh wrapper. gh has no credential helper of its own: it reads GH_TOKEN.
// So eriphe-cred runs the real gh with GH_TOKEN set for that one process,
// a token for the one repository the command is about (-R, GH_REPO, or the
// git remote of the current directory), read or write as the command needs.

// ghEnv is what the wrapper needs from its system, replaced in tests.
type ghEnv interface {
	getenv(string) string
	realGh() (string, error)
	originURL() string
	exec(path string, args []string, env []string) error
	environ() []string
}

// eriphebin is where the node lends its programs to every task.
const eriphebin = "/opt/eriphe/bin"

type osEnv struct{}

func (osEnv) getenv(k string) string { return os.Getenv(k) }
func (osEnv) environ() []string      { return os.Environ() }

// realGh finds gh on PATH, past this program: installed as gh ahead of the
// real one, it must not run itself.
func (osEnv) realGh() (string, error) {
	self, _ := os.Executable()
	selfInfo, _ := os.Stat(self)
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if dir == eriphebin || (self != "" && dir == filepath.Dir(self)) {
			continue // a copy of this program, not a link to it
		}
		p := filepath.Join(dir, "gh")
		info, err := os.Stat(p)
		if err != nil || info.IsDir() || info.Mode()&0o111 == 0 {
			continue
		}
		if selfInfo != nil && os.SameFile(info, selfInfo) {
			continue
		}
		return p, nil
	}
	return "", errors.New("gh is not installed in this task: install it (apt-get install -y gh), then run gh again; Eriphe gives it its token")
}

func (osEnv) originURL() string {
	out, err := exec.Command("git", "remote", "get-url", "origin").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func (osEnv) exec(path string, args []string, env []string) error { return execve(path, args, env) }

func runGh(args []string, c *client, e ghEnv) int {
	gh, err := e.realGh()
	if err != nil {
		fmt.Fprintln(os.Stderr, "eriphe:", err)
		return 127
	}
	argv := append([]string{"gh"}, args...)
	env := e.environ()
	// A token given by hand, or no Eriphe here: gh as it is.
	if e.getenv("GH_TOKEN") != "" || e.getenv("GITHUB_TOKEN") != "" || !c.configured() || ghLocal(args) {
		return runExec(e, gh, argv, env)
	}
	repo := ghRepo(args, e)
	if repo == "" {
		fmt.Fprintln(os.Stderr, "eriphe: no GitHub repository for this gh command (no -R owner/name, GH_REPO or GitHub origin remote): running gh without a token")
		return runExec(e, gh, argv, env)
	}
	access := ghAccess(args)
	if v := e.getenv(envAccess); v == "read" || v == "write" {
		access = v
	}
	a, err := c.ask(request{Protocol: "https", Host: "api.github.com", Path: repo, Access: access, Tool: "gh"})
	var refused *refusedError
	switch {
	case errors.As(err, &refused):
		fmt.Fprintf(os.Stderr, "eriphe: no %s access to %s for gh: %s\n", access, repo, refused.reason)
		return 1
	case errors.Is(err, errNoService):
		return runExec(e, gh, argv, env)
	case err != nil:
		fmt.Fprintf(os.Stderr, "eriphe: cannot get a token for gh: %v\n", err)
		return 1
	}
	env = append(env, "GH_TOKEN="+a.Password, "GH_PROMPT_DISABLED=1")
	return runExec(e, gh, argv, env)
}

func runExec(e ghEnv, path string, argv, env []string) int {
	if err := e.exec(path, argv, env); err != nil {
		fmt.Fprintln(os.Stderr, "eriphe:", err)
		return 126
	}
	return 0
}

// ghRepo is the repository a gh command is about.
func ghRepo(args []string, e ghEnv) string {
	// gh api repos/OWNER/NAME/…: the repository of its endpoint.
	if words := ghWords(args); len(words) > 1 && words[0] == "api" {
		if p := strings.Split(strings.TrimPrefix(words[1], "/"), "/"); len(p) >= 3 && p[0] == "repos" {
			if r := repoOfURL(p[1] + "/" + p[2]); r != "" {
				return r
			}
		}
	}
	for i, a := range args {
		if a == "--" {
			break
		}
		if (a == "-R" || a == "--repo") && i+1 < len(args) {
			return repoOfURL(args[i+1])
		}
		if v, ok := strings.CutPrefix(a, "--repo="); ok {
			return repoOfURL(v)
		}
		if v, ok := strings.CutPrefix(a, "-R"); ok && v != "" {
			return repoOfURL(v)
		}
	}
	if v := e.getenv("GH_REPO"); v != "" {
		return repoOfURL(v)
	}
	return repoOfURL(e.originURL())
}

// ghReads are the gh commands that only read: anything else may write.
var ghReads = map[string][]string{
	"pr":       {"list", "view", "diff", "checks", "status"},
	"issue":    {"list", "view", "status"},
	"repo":     {"view", "list"},
	"run":      {"list", "view", "watch", "download"},
	"workflow": {"list", "view"},
	"release":  {"list", "view", "download"},
	"search":   nil, // every search reads
	"browse":   nil,
	"status":   nil,
}

// ghWords are a gh command's words, without its options.
func ghWords(args []string) []string {
	var words []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "-R" || a == "--repo" || a == "-X" || a == "--method" || a == "-f" || a == "-F" || a == "-H" ||
			a == "--field" || a == "--raw-field" || a == "--header" || a == "--input" || a == "-q" || a == "--jq" {
			i++
			continue
		}
		if !strings.HasPrefix(a, "-") {
			words = append(words, a)
		}
	}
	return words
}

// ghLocal: a gh command that calls no API (its version, help, its own
// settings), which needs no token.
func ghLocal(args []string) bool {
	for _, a := range args {
		if a == "--version" || a == "--help" || a == "-h" {
			return true
		}
	}
	words := ghWords(args)
	if len(words) == 0 {
		return true
	}
	switch words[0] {
	case "version", "help", "completion", "alias", "config", "auth":
		return true
	}
	return false
}

// ghAccess tells whether a gh command reads or writes.
func ghAccess(args []string) string {
	words := ghWords(args)
	if len(words) == 0 {
		return "read"
	}
	if words[0] == "api" {
		return ghAPIAccess(args)
	}
	subs, ok := ghReads[words[0]]
	if !ok {
		return "write"
	}
	if subs == nil {
		return "read"
	}
	if len(words) > 1 {
		for _, s := range subs {
			if words[1] == s {
				return "read"
			}
		}
	}
	return "write"
}

// ghAPIAccess: gh api reads with GET, its default unless it sends fields
// or a body.
func ghAPIAccess(args []string) string {
	for i, a := range args {
		switch {
		case a == "-X" || a == "--method":
			if i+1 < len(args) && !strings.EqualFold(args[i+1], "GET") {
				return "write"
			}
			if i+1 < len(args) {
				return "read" // an explicit GET sends its fields as a query
			}
		case strings.HasPrefix(a, "--method="):
			if !strings.EqualFold(strings.TrimPrefix(a, "--method="), "GET") {
				return "write"
			}
			return "read"
		case strings.HasPrefix(a, "-X") && len(a) > 2:
			if !strings.EqualFold(a[2:], "GET") {
				return "write"
			}
			return "read"
		}
	}
	for _, a := range args {
		if a == "-f" || a == "-F" || a == "--field" || a == "--raw-field" || a == "--input" ||
			strings.HasPrefix(a, "--field=") || strings.HasPrefix(a, "--raw-field=") || strings.HasPrefix(a, "--input=") {
			return "write"
		}
	}
	return "read"
}
