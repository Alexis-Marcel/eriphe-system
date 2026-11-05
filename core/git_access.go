package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// git on the path of an access. A task lent a GitHub repository holds no
// token: eriphe-cred still asks (credentials.go), and gets a placeholder
// once the decision is made, approval included. The node's relay decrypts
// github.com for that task and hands each request here, as for any access
// (lent_access.go), where the real token goes in:
//
//	git → the node's relay → eriphe-core (decides, reads the refs, mints) → GitHub
//
// The decision is made again on each request, from what the request is
// rather than what the helper said: a placeholder is no proof, the task can
// write it down without asking.
//
// A token cannot be limited to a branch, so the refs of a push are read
// here, whoever signs it and whatever ran it: a script gets no further than
// a command line. A task lent a branch pushes that branch only, never to
// delete it; nobody pushes to main or master. What cannot be read is
// refused. Whether a push forces cannot be told from its refs: that stays
// the shell rule's (policy.go), which stops a mistake and no more.

const (
	gitTarget = "github.com:443"
	// gitAccessName and gitAccessEnv are how the node gets github.com, as an
	// access of the task's. Nothing reads the variable: git asks its helper.
	gitAccessName = "github"
	gitAccessEnv  = "ERIPHE_GIT_PLACEHOLDER"

	gitLogTool = "git"

	// gitCommandsLimit is how much of a push is read for its refs, and
	// gitDrainLimit how much of a refused one is read so that git hears why.
	gitCommandsLimit = 1 << 20
	gitDrainLimit    = 1 << 30

	gitReceivePack = "git-receive-pack"
	gitUploadPack  = "git-upload-pack"
)

// gitPlaceholder is what a task's git holds in place of a token.
func gitPlaceholder(taskID string) string { return "eriphe-git-" + taskID }

func isGitHubRepo(e ResourceRef) bool { return e.Kind == resRepo && githubRepo(e.Ref) != "" }

// lentGit is github.com as the node gets it, for a task lent a repository
// there. Reads go this way too: one path for all of a repository.
func lentGit(taskID string, list []ResourceRef) []nodeAccess {
	if !slices.ContainsFunc(list, isGitHubRepo) {
		return nil
	}
	return []nodeAccess{{Name: gitAccessName, Hosts: []string{gitTarget}, Env: gitAccessEnv, Placeholder: gitPlaceholder(taskID)}}
}

// gitRelayed: the relay of the task asking decrypts github.com, as its node
// says. Not so for a task started before, or on an older node: it gets its
// token as it always did, or it could not work at all.
func gitRelayed(req credRequest) bool {
	return req.Tool != "gh" && slices.Contains(req.Relayed, gitTarget)
}

// gitCall is what a request to github.com is, for git.
type gitCall struct {
	repo   string // owner/name, if it is a call of git's
	access string
	push   bool // it carries refs to change
}

// readGitCall reads a request's path. Large File Storage counts as a read:
// what it uploads is not told by its path, so a push of large files fails.
func readGitCall(method string, uri *url.URL) gitCall {
	parts := strings.SplitN(strings.TrimPrefix(uri.Path, "/"), "/", 3)
	if len(parts) != 3 {
		return gitCall{}
	}
	repo := githubRepo(parts[0] + "/" + parts[1])
	switch rest := parts[2]; {
	case rest == gitReceivePack && method == http.MethodPost:
		return gitCall{repo: repo, access: accessWrite, push: true}
	case rest == "info/refs" && uri.Query().Get("service") == gitReceivePack:
		return gitCall{repo: repo, access: accessWrite}
	case rest == "info/refs", rest == gitUploadPack, strings.HasPrefix(rest, "info/lfs/"):
		return gitCall{repo: repo, access: accessRead}
	}
	return gitCall{}
}

// unreadablePush: a request that changes something and is not plainly what
// it says, which GitHub might read as a push this code did not see. Git
// sends nothing of the kind.
func unreadablePush(method, path string) bool {
	if method == http.MethodGet || method == http.MethodHead {
		return false
	}
	p := strings.ToLower(path)
	return strings.Contains(p, "receive-pack") || strings.ContainsAny(p, `%\`) || strings.Contains(p, "..") || strings.Contains(p, "//")
}

// pushCommand is one ref a push changes.
type pushCommand struct {
	Old, New, Ref string
}

func (p pushCommand) deletes() bool { return strings.Trim(p.New, "0") == "" }

var gitObjectID = regexp.MustCompile(`^([0-9a-f]{40}|[0-9a-f]{64})$`)

// readPushCommands reads the pkt-lines a push starts with, up to the flush
// before its pack, and the capabilities the client asked for. Anything but
// plain commands is an error: a signed push holds them in another form, and
// what is not understood here must not be understood at GitHub.
func readPushCommands(r io.Reader) (cmds []pushCommand, caps []string, err error) {
	r = &io.LimitedReader{R: r, N: gitCommandsLimit}
	for first := true; ; first = false {
		var head [4]byte
		if _, err := io.ReadFull(r, head[:]); err != nil {
			if first && err == io.EOF {
				return nil, nil, nil
			}
			return nil, nil, fmt.Errorf("cut short: %w", err)
		}
		size, err := strconv.ParseUint(string(head[:]), 16, 16)
		switch {
		case err != nil:
			return nil, nil, fmt.Errorf("bad length %q", head[:])
		case size == 0:
			return cmds, caps, nil
		case size <= 4 || size > 65520:
			return nil, nil, fmt.Errorf("bad length %d", size)
		}
		raw := make([]byte, size-4)
		if _, err := io.ReadFull(r, raw); err != nil {
			return nil, nil, fmt.Errorf("cut short: %w", err)
		}
		line, features, found := strings.Cut(strings.TrimSuffix(string(raw), "\n"), "\x00")
		if found {
			caps = strings.Fields(features)
		}
		if strings.HasPrefix(line, "shallow ") {
			continue
		}
		f := strings.SplitN(line, " ", 3)
		if len(f) != 3 || !gitObjectID.MatchString(f[0]) || !gitObjectID.MatchString(f[1]) || len(f[0]) != len(f[1]) || f[2] == "" {
			return nil, nil, errors.New("not a command")
		}
		cmds = append(cmds, pushCommand{f[0], f[1], f[2]})
	}
}

// pushRefusal is why a push may not change its refs, if so: what the task
// was lent of this repository says. Every loan of it counts; one without a
// branch leaves all but main and master.
func pushRefusal(cmds []pushCommand, repos []repoGrant, repo string) string {
	var lent []string
	limited := false
	for _, g := range repos {
		if !strings.EqualFold(g.Repo, repo) {
			continue
		}
		if g.Branch == "" {
			limited = false
			break
		}
		limited = true
		lent = append(lent, g.Branch)
	}
	for _, c := range cmds {
		branch, isBranch := strings.CutPrefix(c.Ref, "refs/heads/")
		switch {
		case isBranch && (strings.EqualFold(branch, "main") || strings.EqualFold(branch, "master")):
			return "pushing to " + branch + " is refused: push a branch"
		case !limited:
		case !isBranch || !slices.Contains(lent, branch):
			return "it pushes to " + c.Ref + ", not to the branch it was lent (" + strings.Join(lent, ", ") + ")"
		case c.deletes():
			return "it deletes " + branch + ", the branch it was lent"
		}
	}
	return ""
}

// pktLine is one line of git's protocol.
func pktLine(b []byte) []byte { return append(fmt.Appendf(nil, "%04x", len(b)+4), b...) }

// rejectPush answers a push the way a server refuses one, so that git says
// why (" ! [remote rejected] …"). A client that asks for no report gets a
// plain refusal. All its refs are refused: they share one pack.
func rejectPush(w http.ResponseWriter, cmds []pushCommand, caps []string, why string) {
	if !slices.Contains(caps, "report-status") && !slices.Contains(caps, "report-status-v2") {
		http.Error(w, why, http.StatusForbidden)
		return
	}
	report := pktLine([]byte("unpack ok\n"))
	for _, c := range cmds {
		report = append(report, pktLine([]byte("ng "+c.Ref+" "+why+"\n"))...)
	}
	report = append(report, "0000"...)
	band := 0
	switch {
	case slices.Contains(caps, "side-band-64k"):
		band = 65000
	case slices.Contains(caps, "side-band"):
		band = 900
	}
	var out bytes.Buffer
	if band == 0 {
		out.Write(report)
	} else {
		for chunk := range slices.Chunk(report, band) {
			out.Write(pktLine(append([]byte{1}, chunk...)))
		}
		out.WriteString("0000")
	}
	w.Header().Set("Content-Type", "application/x-"+gitReceivePack+"-result")
	w.Header().Set("Cache-Control", "no-cache")
	w.Write(out.Bytes())
}

// gitLent: this request is one for the git path of the task.
func gitLent(t *TaskRow, target string) bool {
	return target == gitTarget && slices.ContainsFunc(t.Resources, isGitHubRepo)
}

// gitRequest decides a request to github.com and puts the token in. It tells
// what takes the token out of the answer's headers, and false when it has
// answered itself. A request that carries no placeholder asks for no token
// and goes as it is, but what it pushes is read all the same: gh holds a
// token of its own (credentials.go).
func (c *core) gitRequest(w http.ResponseWriter, t *TaskRow, r *http.Request, uri *url.URL) (*strings.Replacer, bool) {
	none := strings.NewReplacer()
	call := readGitCall(r.Method, uri)
	refuse := func(why string) {
		line := truncate(oneLine(why), 300)
		c.store.logCredential(t.ID, gitLogTool, "github.com", call.repo, orStr(call.access, r.Method), decideDeny, byPolicy, "", line)
		log.Printf("git: task %s: %s %s refused: %s", t.ID, r.Method, call.repo, line)
	}
	if !call.push && unreadablePush(r.Method, uri.Path) {
		refuse("a request that is not plainly what it says: " + uri.Path)
		http.Error(w, "this request to github.com cannot be read, so it is refused", http.StatusForbidden)
		return none, false
	}
	value := r.Header.Get("Authorization")
	placeholder := gitPlaceholder(t.ID)
	_, carried := swapPlaceholder(value, placeholder, "")
	carried = carried && call.repo != ""
	facts := c.credentialFacts(t)
	var d credDecision
	if carried {
		d = decideCredential(c.conf().Policy, credRequest{Protocol: "https", Host: "github.com", Path: call.repo, Access: call.access, Tool: gitLogTool}, facts)
		if d.Decision != decideAllow {
			why := d.Reason
			if d.Decision == decideAsk {
				why = "this needs the user's approval, which this task did not get: " + why
			}
			refuse(why)
			http.Error(w, why, http.StatusForbidden)
			return none, false
		}
	}
	if call.push && !readPush(w, r, call, facts.Repos, refuse) {
		return none, false
	}
	if !carried {
		return none, true
	}
	m, err := c.mintGitHub(r.Context(), t.ID, d.Repo, call.access, false)
	if err != nil {
		refuse("cannot give a credential: " + err.Error())
		http.Error(w, "eriphe-core cannot get a token for "+d.Repo, http.StatusBadGateway)
		return none, false
	}
	swapped, _ := swapPlaceholder(value, placeholder, m.Password)
	r.Header.Set("Authorization", swapped)
	c.store.logCredential(t.ID, gitLogTool, "github.com", d.Repo, call.access, decideAllow, byPolicy, m.Source, d.Reason)
	pairs := []string{swapped, value, m.Password, placeholder}
	if _, encoded, ok := strings.Cut(swapped, " "); ok {
		_, original, _ := strings.Cut(value, " ")
		pairs = append(pairs, encoded, original)
	}
	return strings.NewReplacer(pairs...), true
}

// readPush reads a push's refs and puts them back in front of its body.
// false: refused, and answered.
func readPush(w http.ResponseWriter, r *http.Request, call gitCall, repos []repoGrant, refuse func(string)) bool {
	unreadable := func(why string) bool {
		refuse("a push that cannot be read: " + why)
		http.Error(w, "this push cannot be read, so it is refused: "+why, http.StatusForbidden)
		return false
	}
	if call.repo == "" {
		return unreadable("which repository?")
	}
	// git never compresses a push: a body that says so would hide its refs.
	if e := r.Header.Get("Content-Encoding"); e != "" && !strings.EqualFold(e, "identity") {
		return unreadable("compressed")
	}
	var read bytes.Buffer
	cmds, caps, err := readPushCommands(io.TeeReader(r.Body, &read))
	if err != nil {
		return unreadable(err.Error())
	}
	if why := pushRefusal(cmds, repos, call.repo); why != "" {
		refuse(why)
		io.Copy(io.Discard, io.LimitReader(r.Body, gitDrainLimit))
		rejectPush(w, cmds, caps, why)
		return false
	}
	r.Body = struct {
		io.Reader
		io.Closer
	}{io.MultiReader(&read, r.Body), r.Body}
	return true
}
