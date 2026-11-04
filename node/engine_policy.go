package main

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// What eriphe-node engine allows a task's container, and how it checks a
// bind mount on its real path: it sees the node's root and the shared
// places read-only, at the same paths as the Docker daemon (or the host's /
// at -host-root), resolves their symlinks and hands the daemon the
// resolved path. A directory replaced by a symlink between its check and
// the daemon's mount is mountClash's (below).
//
// With the one-container install, the places are the folders shared, one
// by one, and they change while it runs: eriphe-launcher writes them in a
// file (-bind-file, one path a line) the engine reads again at every
// create, in a folder of its data the node never sees.

// dockerPolicy is what eriphe-node engine allows (engine_server.go): the
// places bind mounts may come from, and how their paths are resolved.
type dockerPolicy struct {
	bindFrom []string // bind mounts only from these places, or under them
	bindFile string   // and those listed in this file, read at each create
	resolve  func(string) (string, error)
}

// nodeLabels are the labels of the containers the node makes: a container
// without one is not created, nor touched.
var nodeLabels = []string{"eriphe.task", "eriphe.service", "eriphe.wake", "eriphe.helper"}

// bindSource is a bind mount's source resolved, if it is one of the allowed
// places or under one; ro: only under places listed read-only (PATH:ro in
// the bind file), to be mounted read-only.
func (p *dockerPolicy) bindSource(src string) (string, bool, error) {
	if !filepath.IsAbs(src) {
		return "", false, fmt.Errorf("bind source %q is not absolute", src)
	}
	real, err := p.resolve(src)
	if err != nil {
		return "", false, fmt.Errorf("bind source %s cannot be resolved", src)
	}
	found, ro := false, true
	for _, from := range slices.Concat(p.bindFrom, p.listed()) {
		path, readOnly := strings.CutSuffix(from, ":ro")
		if r, err := p.resolve(path); err == nil && within(r, real) {
			found, ro = true, ro && readOnly
		}
	}
	if !found {
		return "", false, fmt.Errorf("bind source %s is outside the allowed places", src)
	}
	return real, ro, nil
}

// A bind mount is checked on its path, and the daemon mounts that path
// later, when the container starts: a task that can write a folder could
// put a link in place of what is under it in between, and have another
// container mount the host's / instead. So no container mounts a path
// strictly under a place a task can write, while that task is there; nor
// does a task get a place to write while a container not started yet mounts
// something under it. The container waits (409), the node tries it again.
// What a running container mounted is mounted: a link put there later
// changes nothing.

// errMountClash is a container that cannot be made yet (mountClash).
type errMountClash struct{ why string }

func (e errMountClash) Error() string { return e.why }

// mountClash is why a container with these mounts cannot be made now, among
// others (tasks and helpers); task: it is a task's, which runs anything, so
// what it may write counts.
func mountClash(binds []apiMount, task bool, others []apiListed) error {
	for _, o := range others {
		_, isTask := o.Labels["eriphe.task"]
		for _, om := range o.Mounts {
			if om.Type != "bind" {
				continue
			}
			for _, b := range binds {
				if b.Type != "bind" {
					continue
				}
				if isTask && om.RW && strictlyUnder(om.Source, b.Source) {
					return errMountClash{fmt.Sprintf("%s is under %s, which task %s can write: it waits until that task ends",
						b.Source, om.Source, o.Labels["eriphe.task"])}
				}
				if task && !b.ReadOnly && o.State == "created" && strictlyUnder(b.Source, om.Source) {
					return errMountClash{fmt.Sprintf("%s holds %s, which a container not started yet mounts: it waits until that one starts",
						b.Source, om.Source)}
				}
			}
		}
	}
	return nil
}

// strictlyUnder: p is under base, and not base itself.
func strictlyUnder(base, p string) bool {
	return within(base, p) && filepath.Clean(base) != filepath.Clean(p)
}

// listed is the places of -bind-file now (PATH, or PATH:ro for one that is
// mounted read-only only): none when it cannot be read.
func (p *dockerPolicy) listed() []string {
	if p.bindFile == "" {
		return nil
	}
	raw, err := os.ReadFile(p.bindFile)
	if err != nil {
		return nil
	}
	var out []string
	for _, l := range strings.Split(string(raw), "\n") {
		l = strings.TrimSpace(l)
		path, ro := strings.CutSuffix(l, ":ro")
		if filepath.IsAbs(path) && filepath.Clean(path) != "/" {
			if path = filepath.Clean(path); ro {
				path += ":ro"
			}
			out = append(out, path)
		}
	}
	return out
}

// resolveIn resolves p's symlinks as the host would, the host's / being
// root here: an absolute link leads back under root, never out of it.
func resolveIn(root, p string) (string, error) {
	if !filepath.IsAbs(p) {
		return "", fmt.Errorf("%s is not absolute", p)
	}
	todo := strings.Split(p, "/")
	cur, links := "/", 0
	for len(todo) > 0 {
		c := todo[0]
		todo = todo[1:]
		switch c {
		case "", ".":
			continue
		case "..":
			cur = filepath.Dir(cur)
			continue
		}
		next := filepath.Join(cur, c)
		fi, err := os.Lstat(filepath.Join(root, next))
		if err != nil {
			return "", err
		}
		if fi.Mode()&os.ModeSymlink == 0 {
			cur = next
			continue
		}
		if links++; links > 255 {
			return "", fmt.Errorf("%s: too many links", p)
		}
		target, err := os.Readlink(filepath.Join(root, next))
		if err != nil {
			return "", err
		}
		if strings.HasPrefix(target, "/") {
			cur = "/"
		}
		todo = append(strings.Split(target, "/"), todo...)
	}
	return cur, nil
}
