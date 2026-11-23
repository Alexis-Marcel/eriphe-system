package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/Alexis-Marcel/eriphe-system/core/nodestore"
)

// The photo of a shirt the user sends is kept with the shirt's node
// (#748): node_set with the attachment's id as a property's value keeps
// the file in the node (a commit in the graph's repository), the property
// names it, the page shows it, served by eriphe-core, and a Table of a
// node_find source shows it beside the node found.
func TestAttachmentToNodeMedia(t *testing.T) {
	tw, _ := graphWeb(t)
	c := tw.c
	photo := pngBytes(t)
	a := attach(t, c, "chemise.png", photo)
	id := mustBrainNodes(t, c, "node_open", map[string]any{"title": "Chemise bleue", "aside": true})["id"].(string)
	out := mustBrainNodes(t, c, "node_set", map[string]any{"id": id, "props": map[string]any{"tags": []any{"vêtement"}, "couleur": "bleu", "photo": a.ID}})
	src, _ := out["media"].(map[string]any)["photo"].(string)
	name, ok := strings.CutPrefix(src, mediaScheme)
	if !ok || !nodestore.ValidFileName(name) || !strings.HasSuffix(name, ".png") {
		t.Fatalf("node_set: %s", fmtJSON(out))
	}
	if len(c.turnTaint()) != 0 {
		t.Fatal("the user's own photo tainted the turn")
	}

	// The node's file (a commit: nodestore's TestNodeFiles); the property
	// names it.
	if b, err := c.nodes.File(id, name); err != nil || !bytes.Equal(b, photo) {
		t.Fatalf("not in the node's files: %v", err)
	}
	d, _ := c.nodes.Read(id, 0)
	if got := fmtJSON(d.Node.Meta.Props()); !strings.Contains(got, src) {
		t.Fatalf("property: %s", got)
	}

	// The page shows it, as eriphe-core serves it.
	p, err := tw.w.nodePageOf(id, 0, "fr")
	if err != nil {
		t.Fatal(err)
	}
	var shown *pageProp
	for i := range p.Props {
		if p.Props[i].Key == "photo" {
			shown = &p.Props[i]
		}
	}
	if shown == nil || len(shown.Media) != 1 || !shown.Media[0].Image || !strings.HasPrefix(shown.Media[0].Src, mediaPath+"?") {
		t.Fatalf("page props: %s", fmtJSON(p.Props))
	}
	code, body, h := tw.do("GET", shown.Media[0].Src, nil, nil)
	if code != http.StatusOK || body != string(photo) || h.Get("Content-Type") != "image/png" || h.Get("Content-Disposition") != "inline" {
		t.Fatalf("served %d %v", code, h)
	}

	// A page of node_find by tag: each node with its photo, signed for
	// that node.
	block := "```eriphe:block garde\n" + `{"components": {"component": "Table", "columns": [{"key": "title", "label": "Vêtement"}, {"key": "photo", "label": "Photo"}], "rows": {"@path": "/garde/nodes"}},
 "sources": {"garde": {"tool": "eriphe-core/node_find", "args": {"tags": ["vêtement"], "columns": ["photo"]}}}}` + "\n```\n"
	page := mustBrainNodes(t, c, "node_open", map[string]any{"title": "Garde-robe", "aside": true, "body": block})["id"].(string)
	c.live.wg.Wait()
	gp, err := tw.w.nodePageOf(page, 0, "fr")
	if err != nil {
		t.Fatal(err)
	}
	var rows []any
	for _, part := range gp.Parts {
		if part.Block != nil {
			rows, _ = part.Block.Data["garde"].(map[string]any)["nodes"].([]any)
		}
	}
	if len(rows) != 1 {
		t.Fatalf("rows: %s", fmtJSON(gp.Parts))
	}
	thumb, _ := rows[0].(map[string]any)["photo"].(string)
	if !strings.HasPrefix(thumb, mediaPath+"?") {
		t.Fatalf("thumbnail: %q", thumb)
	}
	if code, body, _ := tw.do("GET", thumb, nil, nil); code != http.StatusOK || body != string(photo) {
		t.Fatalf("thumbnail served %d", code)
	}
	// The brain's node_find still gives the property as it is written.
	found := mustBrainNodes(t, c, "node_find", map[string]any{"tags": []any{"vêtement"}, "columns": []any{"photo"}})
	if raw, _ := json.Marshal(found["nodes"]); !strings.Contains(string(raw), `"photo":"`+src+`"`) {
		t.Fatalf("node_find: %s", raw)
	}
}

// A recipe's PDF is kept too, and served as a download; what a node
// cannot keep is refused with why, and nothing is kept.
func TestAttachmentToNodeLimits(t *testing.T) {
	tw, _ := graphWeb(t)
	c := tw.c
	id := mustBrainNodes(t, c, "node_open", map[string]any{"title": "Tarte aux pommes", "aside": true})["id"].(string)
	pdf := attach(t, c, "recette.pdf", []byte("%PDF-1.4\n1 0 obj<<>>endobj\ntrailer<<>>\n%%EOF"))
	out := mustBrainNodes(t, c, "node_set", map[string]any{"id": id, "props": map[string]any{"recette": []any{pdf.ID}}})
	kept, _ := out["media"].(map[string]any)["recette"].([]any)
	if len(kept) != 1 || !strings.HasSuffix(kept[0].(string), ".pdf") {
		t.Fatalf("pdf: %s", fmtJSON(out))
	}
	p, _ := tw.w.nodePageOf(id, 0, "fr")
	if len(p.Props) != 1 || len(p.Props[0].Media) != 1 || p.Props[0].Media[0].Image {
		t.Fatalf("page: %s", fmtJSON(p.Props))
	}
	if code, _, h := tw.do("GET", p.Props[0].Media[0].Src, nil, nil); code != http.StatusOK || h.Get("Content-Type") != "application/pdf" || h.Get("Content-Disposition") != "attachment" {
		t.Fatalf("pdf served %d %v", code, h)
	}

	text := attach(t, c, "notes.txt", []byte("2 œufs"))
	big := attach(t, c, "grande.png", append(pngBytes(t), make([]byte, nodestore.MaxFile)...))
	photo := attach(t, c, "tarte.png", pngBytes(t))
	for _, x := range []struct {
		props map[string]any
		want  string
	}{
		{map[string]any{"photo": text.ID}, "an image or a PDF only"},
		{map[string]any{"photo": big.ID}, "at most"},
		{map[string]any{"photo": "att_00000000000000000000000000000000"}, "no such attachment"},
		{map[string]any{"photo": photo.ID, "bad:key": "x"}, "property's name"}, // refused before the file is kept
	} {
		if _, msg := brainNodes(t, c, "node_set", map[string]any{"id": id, "props": x.props}); !strings.Contains(msg, x.want) {
			t.Errorf("%v: %q, want %q", x.props, msg, x.want)
		}
	}
	if names, _ := c.nodes.Files(id); len(names) != 1 {
		t.Fatalf("files kept by refused writes: %v", names)
	}
	// Full: the files a node keeps at most.
	for len(mustFiles(t, c, id)) < nodestore.MaxFiles {
		if _, err := c.nodes.PutFile(id, byBrain, "png", pngBytes(t)); err != nil {
			t.Fatal(err)
		}
	}
	if _, msg := brainNodes(t, c, "node_set", map[string]any{"id": id, "props": map[string]any{"photo": photo.ID}}); !strings.Contains(msg, "files at most") {
		t.Errorf("past the files a node keeps: %q", msg)
	}
	// From a channel the user did not trust: the turn is tainted, the node
	// external.
	other := mustBrainNodes(t, c, "node_open", map[string]any{"title": "Autre", "aside": true})["id"].(string)
	stored, _ := c.store.attachment(photo.ID)
	c.store.setMsgTaint(stored.Seq, channelTaint("Salon"))
	mustBrainNodes(t, c, "node_set", map[string]any{"id": other, "props": map[string]any{"photo": photo.ID}})
	if sum, _ := c.nodes.Get(other); !sum.External {
		t.Fatal("a photo from an untrusted channel left the node trusted")
	}
}

func mustFiles(t *testing.T, c *core, id string) []string {
	t.Helper()
	names, err := c.nodes.Files(id)
	if err != nil {
		t.Fatal(err)
	}
	return names
}
