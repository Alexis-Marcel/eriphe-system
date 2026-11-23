package nodefile

import (
	"bytes"

	"slices"
	"strings"
	"time"
)

// The authors' API: what the brain, a task or the user writes. Code's
// fields (a block's external, the node's external,
// title_trusted and opened_by) are never theirs: left out, they are kept as the version written over
// has them; written alike, they are accepted; written otherwise, the
// write is refused. A new block has none. A block may be written in its
// short form (one component, its root): it is completed, and written in
// its full form.

// AuthorBody replaces the node's body (its text and blocks) with an
// author's, code's fields of its blocks kept from those so named. A
// heading that only repeats the node's title at its top goes: the page
// shows the title above the body already.
func (n *Node) AuthorBody(body string) error {
	segs, err := parseBody([]byte(body), true)
	if err != nil {
		return err
	}
	segs = dropTitleHeading(segs, n.Meta.Title)
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
	if k := len(n.segs) - 1; k >= 0 && n.segs[k].block == nil && inFence(n.segs[k].text) {
		return blockErr(name, "the node's text ends inside an open code fence: close it first, or the block would read as text")
	}
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
	if b.has.external && b.External != keep.External {
		problems = append(problems, "external is "+ErrCodeField.Error()+": leave it out")
	}
	if len(problems) > 0 {
		return &BlockError{Name: b.Name, Problems: problems, Err: ErrCodeField}
	}
	b.External = keep.External
	b.has = has{completed: b.has.completed, card: b.has.card}
	return nil
}

// AuthorText is the body as its author writes it (AuthorBody takes it
// back): its text and blocks, without code's field of the blocks
// (external).
func (n *Node) AuthorText() (string, error) {
	var b bytes.Buffer
	for _, s := range n.segs {
		if s.block == nil {
			b.WriteString(s.text)
			continue
		}
		bare := *s.block
		bare.External = false
		out, err := bare.canonical()
		if err != nil {
			return "", err
		}
		b.Write(out)
	}
	return b.String(), nil
}

// AuthorBytes is the node as its authors wrote it: its front matter and
// body in their canonical form, without code's fields (a block's
// external, the node's external, title_trusted and
// opened_by, done, links and format). Two versions alike
// there differ only by what code wrote: an author's write made on one
// applies on the other.
func (n *Node) AuthorBytes() ([]byte, error) {
	m := n.Meta
	m.Format, m.External, m.TitleTrusted, m.Done, m.Links, m.OpenedBy = 0, false, false, time.Time{}, nil, nil
	front, err := encodeMeta(m)
	if err != nil {
		return nil, err
	}
	var b bytes.Buffer
	b.Write(front)
	for _, s := range n.segs {
		if s.block == nil {
			b.WriteString(s.text)
			continue
		}
		bare := *s.block
		bare.External = false
		out, err := bare.canonical()
		if err != nil {
			return nil, err
		}
		b.Write(out)
	}
	return b.Bytes(), nil
}

// VouchTitle says the node's writer stands for the title it writes now:
// the user, or an author not tainted. The store then trusts the title of
// an external node (Meta.TitleTrusted); without it, a title written on an
// external node may be someone else's words. Only a writer that wrote the
// title vouches for it.
func (n *Node) VouchTitle() { n.vouched = true }

// TitleVouched tells whether the node's writer vouched for its title.
func (n *Node) TitleVouched() bool { return n.vouched }

// dropTitleHeading takes away a heading that repeats title, at the top of
// a body's first text, and the blank lines after it.
func dropTitleHeading(segs []segment, title string) []segment {
	if len(segs) == 0 || segs[0].block != nil || strings.TrimSpace(title) == "" {
		return segs
	}
	text := strings.TrimLeft(segs[0].text, " \t\r\n")
	line, rest, _ := strings.Cut(text, "\n")
	head := strings.TrimSpace(line)
	if !strings.HasPrefix(head, "#") {
		return segs
	}
	head = strings.TrimSpace(strings.Trim(strings.TrimLeft(head, "#"), " #*_"))
	if !strings.EqualFold(head, strings.TrimSpace(title)) {
		return segs
	}
	rest = strings.TrimLeft(rest, " \t\r\n")
	if rest == "" {
		return slices.Clone(segs[1:])
	}
	out := slices.Clone(segs)
	out[0].text = rest
	return out
}
