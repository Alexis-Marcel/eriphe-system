package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// The readable mirror of the memory (#635): memoire/ next to core.db,
// markdown files rewritten by code from memory.db after every change, so
// that the user can read what Eriphe knows outside Eriphe. It is outside
// git, so that « oublie ça » stays true: a forgotten memory leaves its
// file at once, and nothing keeps it but the nightly backups (14 days,
// like the database's copies). It is not a node: the node tools never see
// it. memory.db stays the truth; a file changed by hand is written over.
//
//   - profil.md: the profile, the preferences, the lessons, what holds
//     for now and what is known about the user;
//   - personnes/<name>.md: one file per person;
//   - faits/<subject>.md: the facts, by what they are about;
//   - decisions/<YYYY-MM>.md: the decisions, by month;
//   - journal/<YYYY-MM-DD>.md: the episodes, one journal per day.
//
// What is in it is what recall gives by default (knownMemories): the
// active memories true now; a proposal, a forgotten or a past one is not.
// Each line is one memory, with its id; an external one (read in a mail,
// a page, a task) is marked.

// knownSection is a part of what Eriphe knows: a group of the mirror's
// files, and of the workspace's « Ce que je sais ».
type knownSection string

const (
	knownProfile   knownSection = "profile"
	knownPeople    knownSection = "people"
	knownFacts     knownSection = "facts"
	knownDecisions knownSection = "decisions"
	knownJournal   knownSection = "journal"
)

// knownSections in the order they are shown.
var knownSections = []knownSection{knownProfile, knownPeople, knownFacts, knownDecisions, knownJournal}

// knownFile is one file of the mirror: where it is, its section, its
// title (a key of the words, or a name), its lines in order.
type knownFile struct {
	Path    string
	Section knownSection
	Title   string // a name, a subject, a month or a day
	Lines   []knownLine
}

// knownLine is one memory as the mirror shows it: its id, its group in
// its file (profil.md's), its date, its text on one line, whether it was
// read outside.
type knownLine struct {
	ID       int64
	Group    string // profil.md's: profile, preference, lesson, situation, owner
	When     string
	Text     string
	External bool
}

// knownMemories is what Eriphe knows, with recall's rules: active, true
// now, never a proposal nor a forgotten one.
func (m *memStore) knownMemories() ([]*Memory, error) {
	return m.list(`status = 'active' AND (valid_to IS NULL OR valid_to > ?)`, []any{m.now().Unix()}, `id`, 1<<30)
}

// knownFiles sorts the memories into the mirror's files, in order: the
// sections, then the files' paths (the latest month and day first).
func knownFiles(xs []*Memory) []knownFile {
	byPath := map[string]*knownFile{}
	var order []string
	put := func(path string, sec knownSection, title string, l knownLine) {
		f := byPath[path]
		if f == nil {
			f = &knownFile{Path: path, Section: sec, Title: title}
			byPath[path] = f
			order = append(order, path)
		}
		f.Lines = append(f.Lines, l)
	}
	for _, x := range xs {
		// One line per memory, never two, whatever its text says (oneLine).
		l := knownLine{ID: x.ID, Text: oneLine(x.Text), External: x.Trust == trustExternal}
		switch {
		case x.Kind == kindEpisode:
			at := x.OccurredAt
			if at.IsZero() {
				at = x.RecordedAt
			}
			at = at.Local()
			l.When = at.Format("15:04")
			put("journal/"+at.Format("2006-01-02")+".md", knownJournal, at.Format("2006-01-02"), l)
		case x.Kind == kindDecision:
			at := x.ValidFrom.Local()
			l.When = at.Format("2006-01-02")
			put("decisions/"+at.Format("2006-01")+".md", knownDecisions, at.Format("2006-01"), l)
		case x.Kind == kindProfile || x.Kind == kindPreference || x.Kind == kindLesson || x.Kind == kindSituation:
			l.Group = x.Kind
			if x.Kind == kindSituation {
				l.When = x.ValidTo.Local().Format("2006-01-02 15:04")
			}
			put("profil.md", knownProfile, "", l)
		case x.About == aliasOwner:
			l.Group = aliasOwner
			put("profil.md", knownProfile, "", l)
		case x.Kind == kindPerson || strings.HasPrefix(x.About, aliasPerson+":"):
			name := strings.TrimPrefix(x.About, aliasPerson+":")
			put("personnes/"+fileSlug(name)+".md", knownPeople, name, l)
		default:
			put("faits/"+fileSlug(x.About)+".md", knownFacts, x.About, l)
		}
	}
	rank := map[knownSection]int{}
	for i, s := range knownSections {
		rank[s] = i
	}
	sort.SliceStable(order, func(i, j int) bool {
		a, b := byPath[order[i]], byPath[order[j]]
		if a.Section != b.Section {
			return rank[a.Section] < rank[b.Section]
		}
		if a.Section == knownJournal || a.Section == knownDecisions {
			return a.Path > b.Path // the latest first
		}
		return a.Path < b.Path
	})
	out := make([]knownFile, 0, len(order))
	groups := map[string]int{kindProfile: 0, kindPreference: 1, aliasOwner: 2, kindLesson: 3, kindSituation: 4}
	for _, p := range order {
		f := *byPath[p]
		if f.Section == knownProfile {
			sort.SliceStable(f.Lines, func(i, j int) bool { return groups[f.Lines[i].Group] < groups[f.Lines[j].Group] })
		}
		if f.Section == knownJournal {
			sort.SliceStable(f.Lines, func(i, j int) bool { return f.Lines[i].When < f.Lines[j].When })
		}
		out = append(out, f)
	}
	return out
}

// fileSlug is a name or a subject as a file's name: lower case letters,
// digits and hyphens; none is "general".
func fileSlug(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range foldAccents(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			dash = false
		} else if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	out := strings.TrimSuffix(b.String(), "-")
	if len(out) > 80 {
		out = strings.TrimSuffix(out[:80], "-")
	}
	if out == "" {
		return "general"
	}
	return out
}

// heading is a file's title, in the user's language.
func (f knownFile) heading(lang string) string {
	switch {
	case f.Section == knownProfile:
		return tr(lang, "espace.known.profile")
	case f.Title != "":
		return f.Title
	case f.Section == knownPeople:
		return tr(lang, "espace.known.people.unnamed")
	}
	return tr(lang, "espace.known.facts.general")
}

// markdown is a file of the mirror as written, in the user's language.
func (f knownFile) markdown(lang string) string {
	var b strings.Builder
	title := f.heading(lang)
	fmt.Fprintf(&b, "# %s\n\n%s\n", title, tr(lang, "espace.known.note"))
	group := ""
	for i, l := range f.Lines {
		switch {
		case f.Section == knownProfile && (i == 0 || l.Group != group):
			group = l.Group
			fmt.Fprintf(&b, "\n## %s\n\n", tr(lang, "espace.known.group."+group))
		case i == 0:
			b.WriteString("\n")
		}
		b.WriteString("- ")
		switch {
		case l.Group == kindSituation:
			b.WriteString(fmt.Sprintf(tr(lang, "espace.known.until"), l.When) + " ")
		case l.When != "":
			b.WriteString(l.When + " ")
		}
		if l.External {
			fmt.Fprintf(&b, "(%s) ", tr(lang, "espace.known.external"))
		}
		fmt.Fprintf(&b, "%s ^m%d\n", l.Text, l.ID)
	}
	return b.String()
}

// memMirror writes the mirror into its folder; one sync at a time.
type memMirror struct {
	dir  string
	lang func() string
	mu   sync.Mutex
}

// memoirePath is the mirror's folder, next to core.db.
func (c *Config) memoirePath() string {
	return filepath.Join(filepath.Dir(c.DB), "memoire")
}

// mirrorSync writes the mirror again from memory.db, if there is one: a
// failure is logged, never the change's (the change is in memory.db).
// It is called once a change is committed, never inside a transaction:
// memory.db has one connection.
func (m *memStore) mirrorSync() {
	if m.mirror == nil {
		return
	}
	if err := m.mirror.sync(m); err != nil {
		log.Printf("memory: mirror: %v", err)
	}
}

// sync renders the memory and writes what changed: a file whose bytes are
// the same is not touched, a file no memory is in any more is removed,
// and so is an empty folder.
func (mm *memMirror) sync(m *memStore) error {
	mm.mu.Lock()
	defer mm.mu.Unlock()
	xs, err := m.knownMemories()
	if err != nil {
		return err
	}
	lang := "fr"
	if mm.lang != nil {
		lang = mm.lang()
	}
	want := map[string][]byte{}
	for _, f := range knownFiles(xs) {
		want[filepath.FromSlash(f.Path)] = []byte(f.markdown(lang))
	}
	if err := os.MkdirAll(mm.dir, 0o700); err != nil {
		return err
	}
	var errs []string
	for rel, body := range want {
		path := filepath.Join(mm.dir, rel)
		if old, err := os.ReadFile(path); err == nil && bytes.Equal(old, body) {
			continue
		}
		if err := writeMirrorFile(path, body); err != nil {
			errs = append(errs, err.Error())
		}
	}
	// What is no longer: the files no memory is in, then the empty folders.
	var dirs []string
	filepath.WalkDir(mm.dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || path == mm.dir {
			return nil
		}
		if d.IsDir() {
			dirs = append(dirs, path)
			return nil
		}
		rel, _ := filepath.Rel(mm.dir, path)
		if _, ok := want[rel]; !ok {
			if err := os.Remove(path); err != nil {
				errs = append(errs, err.Error())
			}
		}
		return nil
	})
	sort.Sort(sort.Reverse(sort.StringSlice(dirs)))
	for _, d := range dirs {
		os.Remove(d) // only when empty
	}
	if len(errs) > 0 {
		return fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return nil
}

// writeMirrorFile writes a file whole or not at all (a temporary file
// renamed), readable by the user alone.
func writeMirrorFile(path string, body []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".mirror-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// archive writes the mirror as a tar.gz, for the nightly backup.
func (mm *memMirror) archive(w io.Writer) error {
	mm.mu.Lock()
	defer mm.mu.Unlock()
	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)
	err := filepath.WalkDir(mm.dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(mm.dir, path)
		if err != nil {
			return err
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if err := tw.WriteHeader(&tar.Header{Name: "memoire/" + filepath.ToSlash(rel), Mode: 0o600, Size: int64(len(body)), ModTime: info.ModTime()}); err != nil {
			return err
		}
		_, err = tw.Write(body)
		return err
	})
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return gz.Close()
}
