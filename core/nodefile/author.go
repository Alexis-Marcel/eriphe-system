package nodefile

import (
	"bytes"
	"errors"
	"strings"
	"time"
)

// The authors' API: what the brain, a task or the user writes. Code's
// fields (the node's external, title_trusted and opened_by, a callout's
// external mark) are never theirs: left out, they are kept as the version
// written over has them; written alike, they are accepted; written
// otherwise, the write is refused.

// AuthorBody replaces the node's body with an author's. A front-matter
// block at its top becomes the node's properties (frontblock.go). A
// heading that only repeats the node's title at its top goes: the page
// shows the title above the body already. The external marks of its
// callouts are code's (listen.go).
func (n *Node) AuthorBody(body string) error {
	body = dropTitleHeading(n.foldFront(body), n.Meta.Title)
	if err := keepMarks(ReadPage(n.body), ReadPage(body)); err != nil {
		return err
	}
	n.body = body
	return nil
}

// CodeBody replaces the node's body with code's, its marks as written. No
// author writes this way.
func (n *Node) CodeBody(body string) error {
	n.body = body
	return nil
}

// ErrCodeField is why an author's write that changes a code's field is
// refused.
var ErrCodeField = errors.New("written by code only")

// AuthorBytes is the node as its authors wrote it: its front matter and
// body in their canonical form, without code's fields (the node's
// external, title_trusted and opened_by, done, links and format; the
// values and dates of the on: lines and the callouts' external marks).
// Two versions alike there differ only by what code wrote: an author's
// write made on one applies on the other.
func (n *Node) AuthorBytes() ([]byte, error) {
	m := n.Meta
	m.Format, m.External, m.TitleTrusted, m.Done, m.Links, m.OpenedBy = 0, false, false, time.Time{}, nil, nil
	front, err := encodeMeta(m)
	if err != nil {
		return nil, err
	}
	var b bytes.Buffer
	b.Write(front)
	b.WriteString(withoutValues(n.body)) // the values on: lines follow are code's (listen.go)
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
// a body, and the blank lines after it.
func dropTitleHeading(body, title string) string {
	if strings.TrimSpace(title) == "" {
		return body
	}
	text := strings.TrimLeft(body, " \t\r\n")
	line, rest, _ := strings.Cut(text, "\n")
	head := strings.TrimSpace(line)
	if !strings.HasPrefix(head, "#") {
		return body
	}
	head = strings.TrimSpace(strings.Trim(strings.TrimLeft(head, "#"), " #*_"))
	if !strings.EqualFold(head, strings.TrimSpace(title)) {
		return body
	}
	return strings.TrimLeft(rest, " \t\r\n")
}
