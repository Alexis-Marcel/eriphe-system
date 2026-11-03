package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"text/tabwriter"
	"time"
)

// eriphe-node share: the user shares a folder with Eriphe on the machine
// that holds it (shared.go). It runs as the node's user, next to the
// running node, and changes the list in the node's data: nothing of it is
// reachable from eriphe-core.
//
//	eriphe-node share add [--name N] [--drop|--manage] [--copyable] <folder>...
//	eriphe-node share list [--json]
//	eriphe-node share remove <name or folder>...
//	eriphe-node share snapshots <name or folder>
//	eriphe-node share restore <name or folder> <snapshot>
//
// The installers wrap it: eriphe-share on Linux (docker exec in the node's
// container) and on a Mac, eriphe-share in the WSL distribution on Windows,
// and the Finder's and the Explorer's "Share with Eriphe". With the
// one-container install, eriphe-launcher shares a folder (eriphe share add,
// in its container): it mounts that folder alone into the node, then runs
// this; ERIPHE_SHARE_COMMAND, set on the node's container, points a user's
// own eriphe-node share add to it.
const shareUsage = `eriphe-node share: share folders of this machine with Eriphe.

  eriphe-node share add [--name NAME] [--read|--drop|--manage] [--copyable] FOLDER...
      share each folder, read-only by default (--read); --drop also lets tasks drop
      their results in its results subfolder ("next to it"); --manage lets
      them create, change, rename and delete anything in it, a snapshot being
      taken before each such task; --copyable allows copies of it on your
      other machines; --name gives the name Eriphe knows it by (one folder
      only; default: the folder's own name). The level chosen here is the
      most Eriphe gets: the workspace may lower it, never raise it. Sharing
      a folder already shared again sets its level and copies anew.
  eriphe-node share list [--json]
  eriphe-node share remove NAME|FOLDER...
      stop sharing; the tasks using it are stopped
  eriphe-node share snapshots NAME|FOLDER
      the snapshots of a folder, taken before the tasks that manage it
  eriphe-node share restore NAME|FOLDER SNAPSHOT|latest
      put the folder back as it was in that snapshot (a snapshot of it as it
      is now is taken first); stop the tasks using it before
  eriphe-node share check [-host DIR] [-data PATH]... FOLDER
      eriphe-launcher's check of a folder of the host, seen under DIR
      (default /host), before it mounts it into the node: no symlink on
      the way, nothing hidden, sensitive or private, not Eriphe's own data
      (-data, host paths)

Options, before the command: -root DIR (env ERIPHE_ROOT), -share NAME=/PATH
(env ERIPHE_NODE_SHARES: the places a folder may be shared from), -engine
(env ERIPHE_ENGINE), -docker CLI, -snapshot-days N (env ERIPHE_SNAPSHOT_DAYS),
-snapshot-limit SIZE (env ERIPHE_SNAPSHOT_LIMIT). A folder is never shared if it holds keys,
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
	fs.StringVar(&o.folderHelper, "folder-helper", os.Getenv("ERIPHE_FOLDER_HELPER"), "the node's own image, for the helpers that work in the shared folders")
	o.shares = shareFlag{}
	if err := o.shares.Set(os.Getenv("ERIPHE_NODE_SHARES")); err != nil {
		fmt.Fprintln(stderr, "eriphe-node share:", err)
		return 2
	}
	fs.Var(o.shares, "share", "a place folders may be shared from")
	fs.IntVar(&o.snapshotDays, "snapshot-days", envInt("ERIPHE_SNAPSHOT_DAYS", defaultSnapshotDays), "days a snapshot is kept")
	limit := fs.String("snapshot-limit", envOr("ERIPHE_SNAPSHOT_LIMIT", defaultSnapshotLimit), "what a snapshot copies at most")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	var lerr error
	if o.snapshotLimit, lerr = parseBytes(*limit); lerr != nil {
		fmt.Fprintln(stderr, "eriphe-node share: -snapshot-limit:", lerr)
		return 2
	}
	if fs.NArg() == 0 {
		fs.Usage()
		return 2
	}
	switch fs.Arg(0) {
	case "check":
		return runShareCheck(fs.Args()[1:], stderr)
	case "add":
		// eriphe-launcher mounts into the node only the folders shared, and
		// so shares them itself: it calls this with ERIPHE_SHARE_COMMAND
		// cleared, once the folder is mounted.
		if c := os.Getenv("ERIPHE_SHARE_COMMAND"); c != "" {
			fmt.Fprintf(stderr, "eriphe-node share: on this machine, a folder is shared through Eriphe's launcher, which gives eriphe-node that folder only:\n  %s add%s\n", c, shellWords(fs.Args()[1:]))
			return 1
		}
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

// runShareCheck is `eriphe-node share check -host DIR [-data PATH]... FOLDER`
// (shares_host.go): no node's data is needed, nor touched.
func runShareCheck(args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	fs.SetOutput(stderr)
	host := fs.String("host", "/host", "where the host's / is seen")
	var own stringList
	fs.Var(&own, "data", "a host path of Eriphe's own data, never shared nor holding a share (repeatable)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, "usage: eriphe-node share check -host DIR [-data PATH]... FOLDER")
		return 2
	}
	if err := checkHostFolder(*host, fs.Arg(0), own); err != nil {
		fmt.Fprintf(stderr, "eriphe-node share: %s: %v\n", fs.Arg(0), err)
		return 1
	}
	return 0
}

const shellSafe = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_./=:,+@%"

// shellWords are args as a shell reads them back, each after a space.
func shellWords(args []string) string {
	var b strings.Builder
	for _, a := range args {
		b.WriteByte(' ')
		if a != "" && strings.Trim(a, shellSafe) == "" {
			b.WriteString(a)
			continue
		}
		b.WriteString("'" + strings.ReplaceAll(a, "'", `'\''`) + "'")
	}
	return b.String()
}

// stringList reads a repeated flag.
type stringList []string

func (l *stringList) String() string     { return strings.Join(*l, ",") }
func (l *stringList) Set(s string) error { *l = append(*l, s); return nil }

var (
	errUsage    = errors.New("usage: eriphe-node share add|list|remove|snapshots|restore (eriphe-node share -h)")
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
		read := fs.Bool("read", false, "") // the default, said: the right click passes it
		drop := fs.Bool("drop", false, "")
		manage := fs.Bool("manage", false, "")
		copyable := fs.Bool("copyable", false, "")
		paths, err := parseInterspersed(fs, args)
		if err != nil || len(paths) == 0 || (*name != "" && len(paths) > 1) {
			return errUsage
		}
		if (*drop && *manage) || (*read && (*drop || *manage)) {
			fmt.Fprintln(out, "--read, --drop and --manage: choose one")
			return errUsage
		}
		access := accessRead
		switch {
		case *drop:
			access = accessDrop
		case *manage:
			access = accessManage
		}
		failed := false
		if shareAsksNode && *copyable {
			fmt.Fprintln(out, "macOS may ask up to twice, to allow Eriphe (eriphe-node) and Syncthing (its copies) to access this folder: click Allow both times.")
		} else if shareAsksNode {
			fmt.Fprintln(out, "macOS may ask you to allow Eriphe (eriphe-node) to access this folder: click Allow.")
		}
		for _, p := range paths {
			f, changed, err := n.shareFolder(p, *name, access, *copyable)
			if err != nil {
				fmt.Fprintf(out, "Not shared: %s: %v\n", p, err)
				failed = true
				continue
			}
			what := map[string]string{accessRead: "read only", accessDrop: "read, and results dropped in its results subfolder",
				accessManage: "read and changed, a snapshot taken before each task that may change it"}[f.Access]
			if f.Copyable {
				what += "; may be copied to your other machines"
			}
			verb := "Shared with Eriphe"
			if changed {
				verb = "Still shared with Eriphe, now"
			}
			fmt.Fprintf(out, "%s as %q (%s): %s\n", verb, f.Name, what, f.Path)
			if shareAsksNode {
				n.nodeOpens(*f, out) // shared all the same: the workspace shows it blocked
			}
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
		fmt.Fprintln(w, "NAME\tACCESS\tCOPYABLE\tFOLDER\tSHARED")
		for _, f := range list {
			access := f.Access
			if f.ceiling() != f.Access {
				access += " (" + f.ceiling() + " here, lowered)"
			}
			copyable := "no"
			if f.Copyable {
				copyable = "yes"
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", f.Name, access, copyable, f.Path, f.Added.Local().Format("2006-01-02 15:04"))
		}
		return w.Flush()
	case "snapshots":
		rest, err := parseInterspersed(fs, args)
		if err != nil || len(rest) != 1 {
			return errUsage
		}
		id, name, path, err := n.snapshotTarget(rest[0])
		if err != nil {
			return err
		}
		list := n.snapshots(id)
		if len(list) == 0 {
			fmt.Fprintf(out, "No snapshot of %q (%s): one is taken before each task that may change it.\n", name, path)
			return nil
		}
		w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "SNAPSHOT\tTAKEN\tBEFORE\tHOW")
		for _, m := range list {
			how := m.Method
			if m.Copied > 0 {
				how += " " + size(m.Copied)
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", m.Stamp, m.Time.Local().Format("2006-01-02 15:04:05"), m.Task, how)
		}
		w.Flush()
		fmt.Fprintf(out, "Kept %d days. To put %q back as it was: eriphe-node share restore %q SNAPSHOT\n", n.snapshotDays(), name, name)
		return nil
	case "restore":
		rest, err := parseInterspersed(fs, args)
		if err != nil || len(rest) != 2 {
			return errUsage
		}
		id, name, path, err := n.snapshotTarget(rest[0])
		if err != nil {
			return err
		}
		list := n.snapshots(id)
		i := -1
		for j, m := range list {
			if m.Stamp == rest[1] || (rest[1] == "latest" && m.Task != "restore") {
				i = j
				break
			}
		}
		if i < 0 {
			return fmt.Errorf("no snapshot %s of %q: see eriphe-node share snapshots %q", rest[1], name, name)
		}
		before, err := n.restoreSnapshot(list[i], path)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "%q (%s) is back as it was at %s.\nAs it was just before: snapshot %s (eriphe-node share restore %q %s undoes this).\n",
			name, path, list[i].Time.Local().Format("2006-01-02 15:04:05"), before.Stamp, name, before.Stamp)
		return nil
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

// shareAsksNode: on a Mac, eriphe-node share add has the running node open
// each folder it shares, so that macOS asks the user now, for the node
// (local.go); askNode is how (a variable for the tests).
var (
	shareAsksNode = runtime.GOOS == "darwin"
	askNode       = askLocalAccess
)

// nodeOpens has the running node open a folder just shared (and Syncthing
// scan it, if it may be copied), and says how it went: refused, which
// program to allow and where, with System Settings opened there; the node
// not running, to share it again once it runs.
func (n *node) nodeOpens(f sharedFolder, out io.Writer) {
	reply, err := askNode(n.opt.root, f.ID, localAskTimeout)
	switch {
	case errors.Is(err, errNoLocalNode):
		fmt.Fprintln(out, "Eriphe is not running on this Mac now: share the folder again once it runs, so that macOS asks you then.")
		return
	case err != nil:
		fmt.Fprintf(out, "Could not ask Eriphe to open %q: %v\n", f.Name, err)
		return
	}
	notes := []accessNote{reply.Node}
	if reply.Syncthing != nil {
		notes = append(notes, *reply.Syncthing)
	}
	denied := false
	for _, note := range notes {
		program := note.Program
		if program == "" {
			program = programNode
		}
		switch {
		case note.Problem == problemDenied:
			fmt.Fprintf(out, "macOS does not let %s open %q. To allow it: System Settings › Privacy & Security › Files and Folders, then turn on %s.\n", program, f.Name, program)
			denied = true
		case note.Problem != "":
			fmt.Fprintf(out, "%s cannot use %q now (%s): %s\n", program, f.Name, note.Problem, note.Error)
		case note.Error != "":
			fmt.Fprintf(out, "%s could not check %q: %s\n", program, f.Name, note.Error)
		}
	}
	if denied {
		fmt.Fprintln(out, "Opening System Settings there now.")
		if err := openPrivacy(); err != nil {
			fmt.Fprintf(out, "(System Settings did not open: %v)\n", err)
		}
	}
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

// snapshotTarget is the folder a name or a path names, for its snapshots:
// shared now, or shared before and snapshotted then.
func (n *node) snapshotTarget(arg string) (id, name, path string, err error) {
	list, err := loadShared(n.opt.root)
	if err != nil {
		return "", "", "", err
	}
	match := n.sharedMatch(arg)
	for _, f := range list {
		if match(f) {
			return f.ID, f.Name, f.Path, nil
		}
	}
	ids, _ := os.ReadDir(filepath.Join(n.opt.root, snapshotsDir))
	for _, d := range ids {
		for _, m := range n.snapshots(d.Name()) {
			if match(sharedFolder{Name: m.Name, Path: m.Path}) {
				return m.ID, m.Name, m.Path, nil
			}
		}
	}
	return "", "", "", fmt.Errorf("%s: no folder shared, nor snapshotted, under this name", arg)
}

// shareFolder checks a folder and adds it to the list; one shared already
// gets the level and copies given (changed), its name too if given.
func (n *node) shareFolder(path, name, access string, copyable bool) (*sharedFolder, bool, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, false, err
	}
	f, err := n.checkShared(abs)
	if err != nil {
		return nil, false, err
	}
	if access != accessRead && f.root.ReadOnly {
		return nil, false, errors.New("this place is read-only on this machine: share it without --drop or --manage")
	}
	named := name != ""
	if name == "" {
		name = filepath.Base(f.src)
	}
	name = strings.TrimSpace(name)
	if !validFolderName(name) || strings.Contains(name, ",") {
		return nil, false, fmt.Errorf("%q cannot be its name: up to 64 characters, without / \\ , nor a leading dot (--name)", name)
	}
	sf := sharedFolder{ID: newShareID(), Name: name, Path: f.src, Access: access, Max: access, Copyable: copyable, Added: time.Now().UTC().Truncate(time.Second)}
	changed, from := false, ""
	err = changeShared(n.opt.root, func(list []sharedFolder) ([]sharedFolder, error) {
		i := slices.IndexFunc(list, func(g sharedFolder) bool { return filepath.Clean(g.Path) == filepath.Clean(sf.Path) })
		for j, g := range list {
			if j != i && strings.EqualFold(g.Name, sf.Name) && (i < 0 || named) {
				return nil, fmt.Errorf("another folder is shared as %q: choose another name with --name", g.Name)
			}
		}
		if i < 0 {
			return append(list, sf), nil
		}
		// Shared already: the machine sets its level and copies anew.
		changed, from = true, list[i].Access
		if named {
			list[i].Name = sf.Name
		}
		list[i].Access, list[i].Max, list[i].Copyable = access, access, copyable
		sf = list[i]
		return list, nil
	})
	if err != nil {
		return nil, false, err
	}
	if changed {
		auditShare(n.opt.root, map[string]any{"event": "change", "id": sf.ID, "name": sf.Name, "path": sf.Path, "from": from, "access": sf.Access, "copyable": sf.Copyable, "by": "local"})
	} else {
		auditShare(n.opt.root, map[string]any{"event": "add", "id": sf.ID, "name": sf.Name, "path": sf.Path, "access": sf.Access, "copyable": sf.Copyable, "by": "local"})
	}
	return &sf, changed, nil
}
