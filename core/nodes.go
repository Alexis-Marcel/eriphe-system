package main

import (
	"context"
	"errors"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Alexis-Marcel/eriphe-system/core/nodestore"
)

// The graph's nodes (package nodestore): a git repository of markdown
// files, nodes/ next to core.db, and its index nodes.db beside it. A
// secret is refused by the memory's filter (scanSecrets); the nightly
// backup goes with the memory's, into its backup folder, at its hour.

// nodesPaths are the graph's repository and its index, next to core.db.
func (c *Config) nodesPaths() (dir, index string) {
	data := filepath.Dir(c.DB)
	return filepath.Join(data, "nodes"), filepath.Join(data, "nodes.db")
}

// refuseSecret is the store's filter: a secret is never written in a
// node nor in its trace.
func refuseSecret(text string) error {
	if why := scanSecrets(text); why != "" {
		return errors.New(why)
	}
	return nil
}

// openNodes opens the graph, and logs what its start found: the files
// changed or removed outside Eriphe, those that do not read as a node.
func openNodes(cfg *Config) (*nodestore.Store, error) {
	dir, index := cfg.nodesPaths()
	s, rep, err := nodestore.Open(dir, index, nodestore.Options{Refuse: refuseSecret})
	if err != nil {
		return nil, err
	}
	if rep.Rebuilt {
		log.Printf("nodes: index made from the files")
	}
	if len(rep.Changed) > 0 {
		log.Printf("nodes: changed outside Eriphe, committed as a version of their own: %s", strings.Join(rep.Changed, ", "))
	}
	if len(rep.Gone) > 0 {
		log.Printf("nodes: removed outside Eriphe: %s", strings.Join(rep.Gone, ", "))
	}
	for _, b := range rep.Broken {
		log.Printf("nodes: not a node, left alone: %s", b)
	}
	return s, nil
}

// runNodes backs the graph up once a night, at the memory's hour
// (extract_at), into the memory's backup folder (nodesNight).
func (c *core) runNodes(ctx context.Context) {
	if c.nodes == nil {
		return
	}
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	var done string
	for {
		done = c.nodesNight(done, time.Now())
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// nodesNight backs the graph up when the night's hour has passed and the
// day has not been backed up (done, or its copy is in the backup folder,
// for a start after the hour); it gives the day backed up.
func (c *core) nodesNight(done string, now time.Time) string {
	mc := c.conf().memoryConfig()
	today := now.Format("2006-01-02")
	at, err := time.ParseInLocation("15:04", mc.ExtractAt, time.Local)
	if err != nil {
		at, _ = time.ParseInLocation("15:04", "03:00", time.Local)
	}
	nightly := time.Date(now.Year(), now.Month(), now.Day(), at.Hour(), at.Minute(), 0, 0, time.Local)
	if now.Before(nightly) || done == today {
		return done
	}
	if _, err := os.Stat(filepath.Join(mc.BackupDir, "nodes-"+today+".db")); err == nil {
		return today
	}
	if err := c.nodes.Backup(mc.BackupDir, mc.BackupKeep); err != nil {
		log.Printf("nodes: backup: %v", err)
	}
	return today
}
