package main

import (
	"strings"

	"github.com/Alexis-Marcel/eriphe-system/core/nodefile"
	"github.com/Alexis-Marcel/eriphe-system/core/nodestore"
)

// A node's page is a Markdown document (nodefile/md.go). After every
// write of an author, code checks it (nodefile's Check) and says what it
// found in the write's result (problems): the write is kept, its author
// fixes it. A line edit (node_edit's old or move) names its line by what
// it holds, so it needs no version: it is made on the last one, and
// refused when its line is gone.

// writeOn is the version an author's write is made on: the one whose
// hash it read (expect), or the last one, whichever it is, for a line
// edit (nodestore's ChangeLast). Only code says last: an author's expect
// never does.
type writeOn struct {
	expect string
	last   bool
}

// onLast is a line edit's version.
var onLast = writeOn{last: true}

// pageProblems is what is wrong in the page a write made, as its author
// reads it: nil when nothing is. hasTool tells the tools the author has,
// for the page's do: links.
func pageProblems(d *nodestore.Doc, hasTool func(string) bool) []nodefile.Problem {
	if d == nil {
		return nil
	}
	p, err := d.Node.Page()
	if err != nil {
		return nil
	}
	if pr := p.Check(hasTool); len(pr) > 0 {
		return pr
	}
	return nil
}

// brainHasTool tells whether the brain has a tool (server/tool): one of
// a catalog server's.
func (c *core) brainHasTool(tool string) bool {
	server, name, _ := strings.Cut(tool, "/")
	if server == "" || name == "" {
		return false
	}
	_, ok := c.conf().Servers[server]
	return ok
}

// lentTool tells whether a task lent grant has a tool (server/tool).
func (c *core) lentTool(grant []string) func(string) bool {
	return func(tool string) bool {
		server, name, _ := strings.Cut(tool, "/")
		return server != "" && name != "" && !providedServer(server) && grantAllows(grant, server, name)
	}
}
