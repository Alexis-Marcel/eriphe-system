package nodefile

import (
	"fmt"
	"maps"
	"slices"
	"strings"
)

// The authors' API: what the brain, a task or the user writes. Code's
// fields (a block's snapshot, marks and external, the node's external)
// are never theirs: left out, they are kept as the version written over
// has them; written alike, they are accepted; written otherwise, the
// write is refused. A new block has none. A block may be written in its
// short form (one component, its root): it is completed, and written in
// its full form.

// ParseAuthor reads an author's write of a whole node over the version
// it replaces (prev; nil for a new node), with code's fields kept from
// prev, and checks it.
func ParseAuthor(raw []byte, prev *Node) (*Node, error) {
	n, err := parse(raw, true)
	if err != nil {
		return nil, err
	}
	ext := prev != nil && prev.Meta.External
	if n.hasExt && n.Meta.External != ext {
		return nil, fmt.Errorf("front matter: external is %w: leave it out", ErrCodeField)
	}
	n.Meta.External = ext
	for _, b := range n.Blocks() {
		var pb *Block
		if prev != nil {
			pb = prev.Block(b.Name)
		}
		if err := b.keepCode(pb); err != nil {
			return nil, err
		}
	}
	return n, n.Validate()
}

// AuthorBody replaces the node's body (its text and blocks) with an
// author's, code's fields of its blocks kept from those so named.
func (n *Node) AuthorBody(body string) error {
	segs, err := parseBody([]byte(body), true)
	if err != nil {
		return err
	}
	for _, s := range segs {
		if s.block == nil {
			continue
		}
		if err := s.block.keepCode(n.Block(s.block.Name)); err != nil {
			return err
		}
		if err := s.block.Validate(); err != nil {
			return err
		}
	}
	n.segs = segs
	return nil
}

// PutAuthorBlock writes an author's block (its JSON): in place of the
// one so named, else before the block named before, else last. Code's
// fields are kept from the block it replaces.
func (n *Node) PutAuthorBlock(name string, raw []byte, before string) error {
	if !BlockRe.MatchString(name) {
		return blockErr(name, "a block's name is lowercase letters, digits and _, 32 at most")
	}
	if before == name {
		return blockErr(name, "before names another block: the one to place this one before")
	}
	b, err := decodeBlock(name, raw, true)
	if err != nil {
		return err
	}
	if err := b.keepCode(n.Block(name)); err != nil {
		return err
	}
	if err := b.Validate(); err != nil {
		return err
	}
	at := func(name string) int {
		return slices.IndexFunc(n.segs, func(s segment) bool { return s.block != nil && s.block.Name == name })
	}
	if before != "" && at(before) < 0 {
		return blockErr(name, "before: no block "+before+" in the node (its blocks: "+n.blockList()+")")
	}
	if i := at(name); i >= 0 {
		if before == "" {
			n.segs[i] = segment{block: b}
			return nil
		}
		n.segs = slices.Delete(n.segs, i, i+1)
	}
	if before != "" {
		n.segs = slices.Insert(n.segs, at(before), segment{block: b})
		return nil
	}
	// Last, after a blank line.
	if k := len(n.segs) - 1; k >= 0 && n.segs[k].block == nil && !strings.HasSuffix(n.segs[k].text, "\n\n") {
		if !strings.HasSuffix(n.segs[k].text, "\n") {
			n.segs[k].text += "\n"
		}
		n.segs[k].text += "\n"
	} else if k >= 0 {
		n.segs = append(n.segs, segment{text: "\n"})
	}
	n.segs = append(n.segs, segment{block: b})
	return nil
}

// blockList names the node's blocks in an error.
func (n *Node) blockList() string {
	var names []string
	for _, b := range n.Blocks() {
		names = append(names, b.Name)
	}
	if len(names) == 0 {
		return "none yet"
	}
	return strings.Join(names, ", ")
}

// keepCode refuses an author's block that changes code's fields, and
// keeps them from the block it replaces (prev; nil for a new block).
func (b *Block) keepCode(prev *Block) error {
	var keep Block
	if prev != nil {
		keep = *prev
	}
	var problems []string
	if b.has.snapshot && !sameField(b.Snapshot, keep.Snapshot) {
		problems = append(problems, "snapshot is "+ErrCodeField.Error()+": leave it out")
	}
	if b.has.marks && !sameField(b.Marks, keep.Marks) {
		problems = append(problems, "marks are "+ErrCodeField.Error()+", the user's: leave them out")
	}
	if b.has.external && b.External != keep.External {
		problems = append(problems, "external is "+ErrCodeField.Error()+": leave it out")
	}
	if len(problems) > 0 {
		return &BlockError{Name: b.Name, Problems: problems, Err: ErrCodeField}
	}
	b.Snapshot, b.External = keep.Snapshot, keep.External
	b.Marks = nil
	for list, items := range keep.Marks {
		if b.Marks == nil {
			b.Marks = Marks{}
		}
		b.Marks[list] = maps.Clone(items)
	}
	b.has = has{completed: b.has.completed}
	return nil
}
