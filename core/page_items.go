package main

import (
	"cmp"
	"context"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Alexis-Marcel/eriphe-system/core/nodefile"
)

// A page's items (docs/node-format.md, « References »): what a node's
// page references, [text][m1] with its target [m1]: mail:<id> at the
// bottom, is an item of a catalog server when the target's scheme is that
// server's name, its id copied as the server's tool gave it. Nothing here
// knows a server or a tool.
//
//   - A call kept for the user's yes (demand.go) whose server is the
//     reference's, and whose input names the reference's id (a string, or
//     in a list of them), acts on that item: kept with the call (its
//     labels), it waits on the line that references it, on the item's row
//     the reader sees (nodefile's Row: the outermost item holding the
//     line). A reference to another server's id is not its item. Calls
//     that do the same thing to other items of the node fold into one
//     card (call_fold.go), the same rule as before.
//   - A card's words come from the item as its server reads it (its
//     declared read, item_open.go), at the moment the call is kept: a free
//     read, bounded in time; never from the link's words. An item no free
//     read names is counted, never named.
//   - The link's words that do not say the item read are misleading: the
//     page shows them with a warning (the prototype's case: « Pub Maison
//     Lune » pointing at a mail of Sam's).
//   - A card no line carries (its reference in a paragraph, or the page
//     no longer references it) waits at the foot of the page.
//
// A block's source (until node format 3 converts it) still lists its items
// the old way (call_words.go, item_cards.go).

// refItem is an item a node's page references: its reference's label,
// the catalog server that holds it, its id as the page writes it.
type refItem struct {
	Label  string
	Server string
	ID     string
}

// refItems are a page's references to a catalog server's item, each label
// once (its first target).
func (c *core) refItems(p *nodefile.Page) []refItem {
	servers := c.conf().Servers
	var out []refItem
	seen := map[string]bool{}
	for _, d := range p.Refs {
		if seen[d.Label] {
			continue
		}
		seen[d.Label] = true
		if d.Scheme == "" || d.ID == "" {
			continue
		}
		_, catalog := servers[d.Scheme]
		if _, provided := providedServers[d.Scheme]; !catalog && !provided {
			continue // a catalog server's, or one Eriphe provides (tool_review.go)
		}
		out = append(out, refItem{Label: d.Label, Server: d.Scheme, ID: d.ID})
	}
	return out
}

// refItemNamed is the item a page's reference names, by its label.
func (c *core) refItemNamed(p *nodefile.Page, label string) (refItem, bool) {
	label = nodefile.RefLabel(label)
	for _, r := range c.refItems(p) {
		if r.Label == label {
			return r, true
		}
	}
	return refItem{}, false
}

// refItemLabels are the items of node's page a call's input names by their
// id, among those of the call's own server, each read by its server
// (itemWords) for the card's words.
func (c *core) refItemLabels(ctx context.Context, node, tool string, input map[string]any) []itemLabel {
	server, _, ok := splitToolName(tool)
	if node == "" || c.nodes == nil || !ok {
		return nil
	}
	d, err := c.nodes.Read(node, 0)
	if err != nil {
		return nil
	}
	p, err := d.Node.Page()
	if err != nil {
		return nil
	}
	values := map[string]bool{}
	inputValues(input, values)
	var out []itemLabel
	seen := map[string]bool{}
	for _, r := range c.refItems(p) {
		if r.Server != server || !values[r.ID] || seen[r.ID] {
			continue
		}
		seen[r.ID] = true
		out = append(out, itemLabel{ID: r.ID, Server: server, External: !c.trustsServer(server)})
		if len(out) == callLabelsMax {
			break
		}
	}
	if len(out) > 0 {
		c.itemWords(ctx, server, out)
	}
	return out
}

// itemWords reads each item by the tool its server declares (a free read
// only, as a click opens it), for its title and detail: what the card
// says of it. One that is not read is left unnamed.
func (c *core) itemWords(ctx context.Context, server string, ls []itemLabel) {
	o := c.serverOpener(server)
	if o == nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, cardReadTimeout)
	defer cancel()
	for i := range ls {
		out, status, _ := c.readItem(ctx, o, ls[i].ID, c.userLang())
		if status != 200 {
			if ctx.Err() != nil {
				return
			}
			continue
		}
		if out.Title != ls[i].ID {
			ls[i].Title = truncate(oneLine(out.Title), 200)
		}
		ls[i].Detail = truncate(oneLine(cmp.Or(out.From, out.Date)), 200)
	}
}

// misleading tells the words of a reference that do not say the item it
// names, as its server read it: none of their words (of three letters or
// more, case and accents aside) is in its title or detail. Words too short
// to tell, or an item not read, say nothing.
func misleading(words string, l itemLabel) bool {
	said := foldAccents(l.Title + " " + l.Detail)
	if strings.TrimSpace(said) == "" {
		return false
	}
	n := 0
	for _, w := range strings.FieldsFunc(foldAccents(words), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		if utf8.RuneCountInString(w) < 3 {
			continue
		}
		if strings.Contains(said, w) {
			return false
		}
		n++
	}
	return n > 0
}

// pageMisleads tells whether one of the page's references by label says
// something the item read does not.
func pageMisleads(p *nodefile.Page, label string, l itemLabel) bool {
	for _, u := range p.Uses {
		if u.Label == label && misleading(u.Text, l) {
			return true
		}
	}
	return false
}

// itemWait is a card that waits on an item of a node, and the item as it
// was kept with the call.
type itemWait struct {
	card string
	item itemLabel
}

// itemWaits are the cards on a node's items that still ask something
// (callsAsk), each item once with its newest card: a card whose calls act
// on several items (call_fold.go) is on each of them.
func (c *core) itemWaits(node string) []itemWait {
	var out []itemWait
	if node == "" {
		return out
	}
	asks := c.asksWhere(`node = ? AND batch != 0 AND (item != '' OR labels != '')
		AND (state = ? OR state = ? AND decided >= ?) ORDER BY batch DESC, id`, node, startProposed, startApproved, time.Now().Add(-itemCardsKept).Unix())
	asking := map[int64]bool{}
	seen := map[string]bool{}
	for _, a := range asks {
		yes, ok := asking[a.Batch]
		if !ok {
			yes = c.callsAsk(a.Batch)
			asking[a.Batch] = yes
		}
		if !yes {
			continue
		}
		for _, l := range askItems(a) {
			key := l.Server + "\x00" + l.ID
			if !seen[key] {
				seen[key] = true
				out = append(out, itemWait{card: proposalRef(cardCalls, a.Batch), item: l})
			}
		}
	}
	return out
}

// pageItem is a reference of a node's page to a catalog server's item, on
// the line that writes it: what the page draws there.
type pageItem struct {
	Line  int    `json:"line"`  // the line of the reference ([text][m1])
	Label string `json:"label"` // its label, lowercased
	Text  string `json:"text"`  // the reference's words, as the page writes them
	// Row is the item's row the reader sees holding the line (nodefile's
	// Row), which carries its card and opens it; 0 for none (a paragraph's,
	// a table's): its card waits at the foot.
	Row int    `json:"row,omitempty"`
	ID  string `json:"id"` // the item's id, as the page writes it: what a card's choices name
	// Open: its server reads one item by id (item_open.go): a click on its
	// row opens it below.
	Open bool `json:"open,omitempty"`
	// Card is the card that waits on the item; Title and Detail the item
	// as its server read it when the card's call was kept (External: its
	// words someone else's); Misleading when the reference's words do not
	// say it; Unread when it could not be read (no free read, a failure):
	// the reference's words are not checked, which the page says.
	Card       string `json:"card,omitempty"`
	Title      string `json:"title,omitempty"`
	Detail     string `json:"detail,omitempty"`
	External   bool   `json:"external,omitempty"`
	Misleading bool   `json:"misleading,omitempty"`
	Unread     bool   `json:"unread,omitempty"`
}

// pageItems are the references of node id's page p to catalog servers'
// items, each where it is written. Nothing is read from a server: an
// item's words are those kept with the card that waits on it.
func (c *core) pageItems(id string, p *nodefile.Page) []pageItem {
	out := []pageItem{}
	refs := c.refItems(p)
	if len(refs) == 0 {
		return out
	}
	byLabel := map[string]refItem{}
	for _, r := range refs {
		byLabel[r.Label] = r
	}
	waits := map[string]itemWait{}
	for _, w := range c.itemWaits(id) {
		if w.item.Server != "" {
			waits[w.item.Server+"\x00"+w.item.ID] = w
		}
	}
	opens := map[string]bool{}
	for _, u := range p.Uses {
		r, ok := byLabel[u.Label]
		if !ok {
			continue
		}
		open, known := opens[r.Server]
		if !known {
			open = c.serverOpener(r.Server) != nil
			opens[r.Server] = open
		}
		it := pageItem{Line: u.Line, Label: u.Label, Text: u.Text, Row: p.Row(u.Item), ID: r.ID, Open: open}
		if w, ok := waits[r.Server+"\x00"+r.ID]; ok {
			it.Card, it.Title, it.Detail, it.External = w.card, w.item.Title, w.item.Detail, w.item.External
			it.Misleading = misleading(u.Text, w.item)
			it.Unread = w.item.Title == "" && w.item.Detail == ""
		}
		out = append(out, it)
	}
	return out
}

// pagePlaced are the cards node id's page carries on its lines: those on
// an item's row, and those a card: link places (one of the node's own
// cards, never another's: a page forges nothing; not in a folded callout
// or a chart, where no reader sees it). Their place is there, not the
// foot of the page. A card's raiser makes it the node's by writing its
// card: line (cardLinesHome).
func (c *core) pagePlaced(id string, p *nodefile.Page, items []pageItem) []string {
	out := []string{}
	add := func(ref string) {
		if !slices.Contains(out, ref) {
			out = append(out, ref)
		}
	}
	for _, it := range items {
		if it.Card != "" && it.Row != 0 {
			add(it.Card)
		}
	}
	for _, l := range p.Links {
		if l.Kind == nodefile.LinkCard && l.Target != "" && !p.Unseen(l.Line) && c.nodeHasCard(id, l.Target) {
			add(l.Target)
		}
	}
	return out
}

// cardLinesHome makes node id the home of the cards its page places with
// a card: line written by the card's raiser (cardRaiser; the line's
// author, lineAuthor): a card waits where its author chose to write it,
// in that node only (cardHome), most often the node where its subject is
// discussed. Never a card someone else raised: a page does not pull
// another's card out of its node. Not from a folded callout or a chart.
// Run after every author write.
func (c *core) cardLinesHome(id, body string) {
	p := nodefile.ReadPage(body)
	lines := strings.Split(body, "\n")
	for _, l := range p.Links {
		if l.Kind != nodefile.LinkCard || l.Target == "" || p.Unseen(l.Line) || l.Line > len(lines) {
			continue
		}
		if slices.Equal(c.cardNodes(l.Target), []string{id}) {
			continue // home already
		}
		raiser := c.cardRaiser(l.Target)
		if raiser == "" {
			continue
		}
		text := strings.TrimSpace(lines[l.Line-1])
		if c.lineAuthor(id, "\x00\x00"+text, cardLineIn(text)) == raiser {
			c.cardHome(id, l.Target)
		}
	}
}

// cardLineIn tells whether a body holds the line text (trimmed).
func cardLineIn(text string) func(string) bool {
	return func(body string) bool {
		for _, l := range strings.Split(body, "\n") {
			if strings.TrimSpace(l) == text {
				return true
			}
		}
		return false
	}
}
