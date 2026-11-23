package main

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/Alexis-Marcel/eriphe-system/core/nodefile"
	"github.com/Alexis-Marcel/eriphe-system/core/nodestore"
)

// A card block places one of the node's cards in its document: it holds
// the card's ref alone, and the page draws the card from the records,
// pending, decided or gone, placed in a tainted turn too. A card that is
// not the node's, or a block marked external by hand, shows no card.
func TestCardBlock(t *testing.T) {
	tw, _ := graphWeb(t)
	c, w := tw.c, tw.w
	subject := mustNode(t, c, nodestore.New{Title: "Garder un œil sur mes mails", Body: "## Ce qu'on a décidé\n\nToutes les boîtes.\n"})
	other := mustNode(t, c, nodestore.New{Title: "Garage"})
	propose := func(title, node string) string {
		t.Helper()
		out, err := c.proposeView(proposeViewIn{Title: title, Body: title + ".\n"}, nil)
		if err != nil {
			t.Fatal(err)
		}
		ref, _ := out["card"].(string)
		if ref == "" || !strings.Contains(fmtJSON(out), `{\"card\": \"`+ref+`\"}`) {
			t.Fatalf("the tool gives no card to place: %v", out)
		}
		c.cardRaisedFor(nodeRef(node), ref)
		return ref
	}
	mine := propose("Mes mails", subject)
	theirs := propose("Le garage", other)
	write := func(name, ref string, tainted bool) error {
		_, err := c.authorWrite(subject, hashOf(t, c, subject), byBrain, tainted, c.viewSourceOK(""), func(n *nodefile.Node) error {
			return n.PutAuthorBlock(name, []byte(`{"card": "`+ref+`"}`), "")
		})
		return err
	}
	if err := write("autre", theirs, false); err == nil || !strings.Contains(err.Error(), "not one of this node's cards") {
		t.Errorf("another node's card placed: %v", err)
	}
	if err := write("inconnue", "view:999", false); err == nil {
		t.Error("an unknown card placed")
	}
	// Placed in a turn that read someone else's words, the block is a ref
	// alone: it is not external, and its card shows where it stands.
	if err := write("vue", mine, true); err != nil {
		t.Fatalf("a card placed in a tainted turn: %v", err)
	}
	if d, _ := c.nodes.Read(subject, 0); d.Node.Block("vue").External || !d.Node.Meta.External {
		t.Errorf("tainted: the card block external %v, the node external %v", d.Node.Block("vue").External, d.Node.Meta.External)
	}
	if p, _ := w.nodePageOf(subject, 0, "fr"); !slices.ContainsFunc(p.Parts, func(part pagePart) bool {
		return part.Block != nil && part.Block.Name == "vue" && part.Block.Proposal != nil && part.Block.Proposal.Ref == mine
	}) {
		t.Error("the card placed in a tainted turn is not drawn where it is placed")
	}
	if err := write("vue", mine, false); err != nil {
		t.Fatal(err)
	}
	// Opened elsewhere, a node has no card of its own yet: none placed.
	if _, msg := brainNodes(t, c, "node_open", map[string]any{"title": "Ailleurs", "body": "```eriphe:block vue\n{\"card\": \"" + mine + "\"}\n```\n"}); msg == "" {
		t.Error("a new node placed another node's card")
	}

	card := func() *pageBlock {
		t.Helper()
		p, err := w.nodePageOf(subject, 0, "fr")
		if err != nil {
			t.Fatal(err)
		}
		for _, part := range p.Parts {
			if part.Block != nil && part.Block.Name == "vue" {
				for _, v := range p.Cards {
					if v.Ref == mine && part.Block.Proposal != nil {
						t.Errorf("the card placed shows at the foot too")
					}
				}
				return part.Block
			}
		}
		t.Fatalf("no card block: %+v", p.Parts)
		return nil
	}
	b := card()
	if b.Card != mine || b.Proposal == nil || b.Proposal.Ref != mine || b.Proposal.State != cardPending || b.Proposal.Text == "" || len(b.Components) != 0 {
		t.Fatalf("the card block %+v", b)
	}

	// Written past the guard (a file edited by hand): another node's card,
	// or one unknown, shows as not found, never with its Oui.
	for _, ref := range []string{theirs, "view:999"} {
		if _, err := c.nodes.Change(subject, hashOf(t, c, subject), byUser, func(n *nodefile.Node) error {
			return n.PutAuthorBlock("autre", []byte(`{"card": "`+ref+`"}`), "")
		}); err != nil {
			t.Fatal(err)
		}
		p, _ := w.nodePageOf(subject, 0, "fr")
		for _, part := range p.Parts {
			if part.Block != nil && part.Block.Name == "autre" && (part.Block.Card != ref || part.Block.Proposal != nil) {
				t.Errorf("%s: %+v", ref, part.Block)
			}
		}
	}
	// External, the block places nothing: the card waits at the foot.
	if _, err := c.nodes.Change(subject, hashOf(t, c, subject), nodeByCode, func(n *nodefile.Node) error { return n.SetBlockExternal("vue", true) }); err != nil {
		t.Fatal(err)
	}
	if p, _ := w.nodePageOf(subject, 0, "fr"); len(p.Cards) != 1 || p.Cards[0].Ref != mine {
		t.Errorf("an external card block hides its card: %+v", p.Cards)
	}
	if b := card(); b.Proposal != nil {
		t.Errorf("an external card block shows its card: %+v", b.Proposal)
	}
	if _, err := c.nodes.Change(subject, hashOf(t, c, subject), nodeByCode, func(n *nodefile.Node) error { return n.SetBlockExternal("vue", false) }); err != nil {
		t.Fatal(err)
	}

	// Archived with its node, the card is gone, and the block says so.
	if err := c.archiveNode(context.Background(), subject, byUser); err != nil {
		t.Fatal(err)
	}
	if b := card(); b.Proposal == nil || b.Proposal.State != cardGone {
		t.Fatalf("after the archive: %+v", b.Proposal)
	}
}

// hashOf is a node's last version's hash, for a write made on it.
func hashOf(t *testing.T, c *core, id string) string {
	t.Helper()
	d, err := c.nodes.Read(id, 0)
	if err != nil {
		t.Fatal(err)
	}
	return d.Version.Hash
}
