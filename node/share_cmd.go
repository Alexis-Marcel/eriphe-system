package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"
)

// eriphe-node share: the user shares a folder with Eriphe on the machine
// that holds it (shared.go). It runs as the node's user, next to the
// running node, and changes the list in the node's data: nothing of it is
// reachable from eriphe-core.
//
//	eriphe-node share add [--name N] [--drop] <folder>...
//	eriphe-node share list [--json]
//	eriphe-node share remove <name or folder>...
//
// The installers wrap it: eriphe-share on Linux (docker exec in the node's
// container) and on a Mac, eriphe-share in the WSL distribution on Windows,
// and the Finder's and the Explorer's "Share with Eriphe".
const shareUsage = `eriphe-node share: share folders of this machine with Eriphe.

  eriphe-node share add [--name NAME] [--drop] FOLDER...
      share each folder, read-only; --drop also lets tasks drop their results
      in its results subfolder; --name gives the name Eriphe knows it by (one
      folder only; default: the folder's own name)
  eriphe-node share list [--json]
  eriphe-node share remove NAME|FOLDER...
      stop sharing; the tasks using it are stopped

Options, before the command: -root DIR (env ERIPHE_ROOT), -share NAME=/PATH
(env ERIPHE_NODE_SHARES: the places a folder may be shared from), -engine
(env ERIPHE_ENGINE), -docker CLI. A folder is never shared if it holds keys,
passwords or settings, a folder made private (.eriphe-private), or Eriphe's
own data.
`

// runShare is `eriphe-node share`: its exit code.
func runShare(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("share", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprint(stderr, shareUsage) }
	var o options
	fs.StringVar(&o.root, "root", envOr("ERIPHE_ROOT", "data"), "the node's data")
	fs.StringVar(&o.engine, "engine", envOr("ERIPHE_ENGINE", engineDocker), "docker or container")
	fs.StringVar(&o.docker, "docker", "", "the engine's CLI")
	o.shares = shareFlag{}
	if err := o.shares.Set(os.Getenv("ERIPHE_NODE_SHARES")); err != nil {
		fmt.Fprintln(stderr, "eriphe-node share:", err)
		return 2
	}
	fs.Var(o.shares, "share", "a place folders may be shared from")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() == 0 {
		fs.Usage()
		return 2
	}
	if o.docker == "" {
		o.docker = o.engine
	}
	var err error
	if o.root, err = filepath.Abs(o.root); err == nil {
		if _, serr := os.Stat(filepath.Join(o.root, "node.json")); serr != nil {
			err = fmt.Errorf("no eriphe-node in %s: give its data folder with -root", o.root)
		}
	}
	if err != nil {
		fmt.Fprintln(stderr, "eriphe-node share:", err)
		return 1
	}
	// Not newNode: nothing of the running node's state is touched (its
	// awake file, its agents), only the list.
	n := &node{opt: o, tasks: map[string]*Task{}}
	if err := n.shareCmd(fs.Arg(0), fs.Args()[1:], stdout); err != nil {
		if !errors.Is(err, errReported) {
			fmt.Fprintln(stderr, "eriphe-node share:", err)
		}
		if errors.Is(err, errUsage) {
			return 2
		}
		return 1
	}
	return 0
}

var (
	errUsage    = errors.New("usage: eriphe-node share add|list|remove (eriphe-node share -h)")
	errReported = errors.New("some folders were not shared")
)

// parseInterspersed parses flags anywhere among the arguments
// (`add FOLDER --drop` as `add --drop FOLDER`).
func parseInterspersed(fs *flag.FlagSet, args []string) ([]string, error) {
	var rest []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		if fs.NArg() == 0 {
			return rest, nil
		}
		rest = append(rest, fs.Arg(0))
		args = fs.Args()[1:]
	}
}

// shareCmd runs one command of eriphe-node share.
func (n *node) shareCmd(cmd string, args []string, out io.Writer) error {
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	switch cmd {
	case "add":
		name := fs.String("name", "", "")
		drop := fs.Bool("drop", false, "")
		paths, err := parseInterspersed(fs, args)
		if err != nil || len(paths) == 0 || (*name != "" && len(paths) > 1) {
			return errUsage
		}
		access := accessRead
		if *drop {
			access = accessDrop
		}
		failed := false
		for _, p := range paths {
			f, err := n.shareFolder(p, *name, access)
			if err != nil {
				fmt.Fprintf(out, "Not shared: %s: %v\n", p, err)
				failed = true
				continue
			}
			what := "read"
			if f.Access == accessDrop {
				what = "read, and results dropped in its results subfolder"
			}
			fmt.Fprintf(out, "Shared with Eriphe as %q (%s): %s\n", f.Name, what, f.Path)
		}
		if failed {
			return errReported
		}
		return nil
	case "list":
		asJSON := fs.Bool("json", false, "")
		if rest, err := parseInterspersed(fs, args); err != nil || len(rest) > 0 {
			return errUsage
		}
		list, err := loadShared(n.opt.root)
		if err != nil {
			return err
		}
		if *asJSON {
			enc := json.NewEncoder(out)
			enc.SetIndent("", "  ")
			return enc.Encode(map[string]any{"folders": list})
		}
		if len(list) == 0 {
			fmt.Fprintln(out, "No folder shared with Eriphe on this machine.")
			return nil
		}
		w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "NAME\tACCESS\tFOLDER\tSHARED")
		for _, f := range list {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", f.Name, f.Access, f.Path, f.Added.Local().Format("2006-01-02 15:04"))
		}
		return w.Flush()
	case "remove", "rm":
		rest, err := parseInterspersed(fs, args)
		if err != nil || len(rest) == 0 {
			return errUsage
		}
		failed := false
		for _, arg := range rest {
			removed, err := n.unshare(n.sharedMatch(arg), "local")
			if err != nil {
				return err
			}
			if len(removed) == 0 {
				fmt.Fprintf(out, "Not shared: %s\n", arg)
				failed = true
			}
			for _, f := range removed {
				fmt.Fprintf(out, "No longer shared: %q (%s)\n", f.Name, f.Path)
			}
		}
		if failed {
			return errReported
		}
		return nil
	case "-h", "help", "--help":
		fmt.Fprint(out, shareUsage)
		return nil
	}
	return errUsage
}

// sharedMatch matches a shared folder by its name (any case) or its folder.
func (n *node) sharedMatch(arg string) func(sharedFolder) bool {
	p := ""
	if filepath.IsAbs(arg) || strings.ContainsRune(arg, filepath.Separator) || arg == "." {
		p, _ = filepath.Abs(arg)
	}
	return func(f sharedFolder) bool {
		return strings.EqualFold(f.Name, arg) || (p != "" && filepath.Clean(f.Path) == p)
	}
}

// shareFolder checks a folder and adds it to the list.
func (n *node) shareFolder(path, name, access string) (*sharedFolder, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	f, err := n.checkShared(abs)
	if err != nil {
		return nil, err
	}
	if access == accessDrop && f.root.ReadOnly {
		return nil, errors.New("this place is read-only on this machine: share it without --drop")
	}
	if name == "" {
		name = filepath.Base(f.src)
	}
	name = strings.TrimSpace(name)
	if !validFolderName(name) || strings.Contains(name, ",") {
		return nil, fmt.Errorf("%q cannot be its name: up to 64 characters, without / \\ , nor a leading dot (--name)", name)
	}
	sf := sharedFolder{ID: newShareID(), Name: name, Path: f.src, Access: access, Added: time.Now().UTC().Truncate(time.Second)}
	err = changeShared(n.opt.root, func(list []sharedFolder) ([]sharedFolder, error) {
		for _, g := range list {
			switch {
			case filepath.Clean(g.Path) == filepath.Clean(sf.Path):
				return nil, fmt.Errorf("already shared as %q: remove it first to change it", g.Name)
			case strings.EqualFold(g.Name, sf.Name):
				return nil, fmt.Errorf("another folder is shared as %q: choose another name with --name", g.Name)
			}
		}
		return append(list, sf), nil
	})
	if err != nil {
		return nil, err
	}
	auditShare(n.opt.root, map[string]any{"event": "add", "id": sf.ID, "name": sf.Name, "path": sf.Path, "access": sf.Access, "by": "local"})
	return &sf, nil
}
