package main

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/Alexis-Marcel/eriphe-system/core/nodefile"
	"github.com/Alexis-Marcel/eriphe-system/core/nodestore"
)

// The graph's nodes (package nodestore): a git repository of markdown
// files, nodes/ next to core.db, and its index nodes.db beside it. A
// secret is refused by the memory's filter (scanSecrets); the nightly
// backup goes with the memory's, into its backup folder, at its hour.

// nodeByCode is the author of code's own writes of a node.
const nodeByCode = "eriphe"

// nodeRef is how a record names a node.
func nodeRef(id string) string { return "node:" + id }

// nodesPaths are the graph's repository and its index, next to core.db.
func (c *Config) nodesPaths() (dir, index string) {
	data := filepath.Dir(c.DB)
	return filepath.Join(data, "nodes"), filepath.Join(data, "nodes.db")
}

// refuseSecret is the store's filter: a secret is never written in a
// node nor in its trace. A reference's item id names an item, as a
// link's path does: it is not read as a long random string
// (withoutItemIDs); a credential's known shape in it still is.
func refuseSecret(text string) error {
	if why := scanSecretsNamed(text, withoutItemIDs(text)); why != "" {
		return errors.New(why)
	}
	return nil
}

// itemSchemeRe is a reference's scheme that names a tool's server (mail,
// calendar), as the page format writes it.
var itemSchemeRe = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)

// withoutItemIDs is text with the id of each reference to an item left
// out: a line the page reader takes for a reference's target
// (`[m1]: mail:<id>`, outside code), its scheme a server's name and its id
// the one token a tool gave, not a link (a link's path is left out by
// withoutLinkPaths, its query and fragment still seen). The rest of the
// line stays.
func withoutItemIDs(text string) string {
	p := nodefile.ReadPage(text)
	if len(p.Refs) == 0 {
		return text
	}
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	for _, d := range p.Refs {
		if d.ID == "" || !itemSchemeRe.MatchString(d.Scheme) || strings.HasPrefix(d.ID, "/") || d.Line > len(lines) {
			continue
		}
		l := lines[d.Line-1]
		at := strings.Index(l, "]:") + 2 // the target follows its label
		lines[d.Line-1] = l[:at] + strings.Replace(l[at:], d.Target, d.Scheme+":", 1)
	}
	return strings.Join(lines, "\n")
}

// openNodes opens the graph, and logs what its start found: the files
// changed or removed outside Eriphe, those that do not read as a node, those
// converted to today's format (with what core.db, db, kept of their
// blocks). Once every node reads (none broken), what core.db kept of the
// blocks is dropped: nothing reads it again.
func openNodes(cfg *Config, db *sql.DB) (*nodestore.Store, error) {
	dir, index := cfg.nodesPaths()
	var snaps nodefile.Snapshots
	if db != nil {
		snaps = blockSnapshots(db)
	}
	s, rep, err := nodestore.Open(dir, index, nodestore.Options{Refuse: refuseSecret, Snapshots: snaps})
	if err != nil {
		return nil, err
	}
	if db != nil && len(rep.Broken) == 0 {
		if err := dropBlockTables(db); err != nil {
			log.Printf("nodes: %v", err)
		}
	}
	switch {
	case rep.OldSchema != 0:
		log.Printf("nodes: index at schema %d, an older one: made again from the files", rep.OldSchema)
	case rep.Rebuilt:
		log.Printf("nodes: index made from the files")
	}
	if len(rep.Changed) > 0 {
		log.Printf("nodes: changed outside Eriphe, committed as a version of their own: %s", strings.Join(rep.Changed, ", "))
	}
	if len(rep.Converted) > 0 {
		log.Printf("nodes: converted to today's file format, the old one kept in the history: %s", strings.Join(rep.Converted, ", "))
	}
	if len(rep.Gone) > 0 {
		log.Printf("nodes: removed outside Eriphe: %s", strings.Join(rep.Gone, ", "))
	}
	for _, b := range rep.Broken {
		log.Printf("nodes: not a node, left alone: %s", b)
	}
	return s, nil
}

// BackupConfig is the nightly backup's hour, folder and days kept; it
// works without one. Its keys are read in the "memory" object of core.json
// (MemoryConfig).
type BackupConfig struct {
	ExtractAt  string `json:"extract_at,omitempty"`  // local time of the nightly run, 03:00 by default
	BackupDir  string `json:"backup_dir,omitempty"`  // nightly copies; memory-backups next to eriphe-core's database by default
	BackupKeep int    `json:"backup_keep,omitempty"` // nightly copies kept, 14 by default
}

func (c *Config) backupConfig() BackupConfig {
	var b BackupConfig
	if c.Memory != nil {
		b = c.Memory.BackupConfig
	}
	if b.ExtractAt == "" {
		b.ExtractAt = "03:00"
	}
	if b.BackupDir == "" {
		b.BackupDir = filepath.Join(filepath.Dir(c.DB), "memory-backups")
	}
	if b.BackupKeep <= 0 {
		b.BackupKeep = 14
	}
	return b
}

// nightly is the night's hour on now's day; one that does not parse is
// 03:00.
func (b BackupConfig) nightly(now time.Time) time.Time {
	at, err := time.ParseInLocation("15:04", b.ExtractAt, time.Local)
	if err != nil {
		at, _ = time.ParseInLocation("15:04", "03:00", time.Local)
	}
	return time.Date(now.Year(), now.Month(), now.Day(), at.Hour(), at.Minute(), 0, 0, time.Local)
}

// runNodes writes the lines that follow the event journal, and the
// glances of the nodes changed (node_listen.go), one at a time; and backs
// the graph up once a night, at the backup's hour (extract_at), into its
// folder (nodesNight).
func (c *core) runNodes(ctx context.Context) {
	if c.nodes == nil {
		return
	}
	c.subscribeEvents(c.queueHeard)
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	var done string
	for {
		done = c.nodesNight(done, time.Now())
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		case e := <-c.listen.events:
			c.heard(e)
		case <-c.listen.kick:
			for _, id := range c.changedDocs() {
				c.noteGlance(id, time.Now())
			}
		}
	}
}

// nodesNight backs the graph up when the night's hour has passed and the
// day has not been backed up (done, or its copy is in the backup folder,
// for a start after the hour); it gives the day backed up. A backup that
// failed gives done as it was: the next tick tries again. Before it, the
// values written since the nodes' last versions make one version, author
// readings (nodestore's values.go): at most one a day; after it, the
// repository is packed when its loose objects are many.
func (c *core) nodesNight(done string, now time.Time) string {
	mc := c.conf().backupConfig()
	today := now.Format("2006-01-02")
	if now.Before(mc.nightly(now)) || done == today {
		return done
	}
	if _, err := os.Stat(filepath.Join(mc.BackupDir, "nodes-"+today+".db")); err == nil {
		return today
	}
	if ids, err := c.nodes.CommitReadings(); err != nil {
		log.Printf("nodes: readings: %v", err)
	} else if len(ids) > 0 {
		log.Printf("nodes: the values of %d nodes in a version of the day", len(ids))
	}
	if err := c.nodes.Backup(mc.BackupDir, mc.BackupKeep); err != nil {
		log.Printf("nodes: backup: %v", err)
		return done
	}
	if packed, err := c.nodes.Pack(); err != nil {
		log.Printf("nodes: pack: %v", err)
	} else if packed {
		log.Printf("nodes: repository packed")
	}
	return today
}
