package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Alexis-Marcel/eriphe-system/core/nodestore"
)

// capturedPNG is the start of a PNG: enough for its type to be sniffed.
var capturedPNG = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR")

// postTaskMedia posts an image for a task, as its device relays it.
func postTaskMedia(t *testing.T, c *core, task, node, secret string, body []byte) (int, string) {
	t.Helper()
	req := httptest.NewRequest("POST", "/v1/device/tasks/"+task+"/media?node="+node, bytes.NewReader(body))
	req.SetPathValue("id", task)
	req.Header.Set("Authorization", "Bearer "+secret)
	req.Header.Set("Eriphe-Device", "pc")
	rec := httptest.NewRecorder()
	c.handleTaskMedia(rec, req)
	return rec.Code, rec.Body.String()
}

// A task's capture is kept in its own node's files, never another's; a
// block of that node shows it through eriphe-core, signed for that node
// only, even when the block is external: it carries nothing out.
func TestTaskMediaInItsNode(t *testing.T) {
	tw, _ := graphWeb(t)
	c := tw.c
	c.store.registerDevice("pc", hashSecret("device-secret"))
	other := mustNode(t, c, nodestore.New{Title: "Autre"})
	mine := mustNode(t, c, nodestore.New{Title: "Prod"})
	c.store.insertTask(&TaskRow{ID: "t-cap", Kind: "worker", Runtime: "claude-code", State: stateRunning, Device: "pc"})
	c.store.insertTask(&TaskRow{ID: "t-none", Kind: "worker", Runtime: "claude-code", State: stateRunning, Device: "pc"})
	if _, err := c.store.db.Exec(`INSERT INTO task_nodes (task, node) VALUES ('t-cap', ?)`, mine); err != nil {
		t.Fatal(err)
	}

	for _, x := range []struct {
		task, node, secret string
		body               []byte
		want               int
	}{
		{"t-cap", mine, "wrong", capturedPNG, http.StatusUnauthorized},
		{"t-nope", mine, "device-secret", capturedPNG, http.StatusNotFound},
		{"t-none", mine, "device-secret", capturedPNG, http.StatusForbidden}, // no node of its own
		{"t-cap", other, "device-secret", capturedPNG, http.StatusForbidden}, // not its node
		{"t-cap", mine, "device-secret", []byte("<svg onload=x>"), http.StatusUnsupportedMediaType},
		{"t-cap", mine, "device-secret", make([]byte, nodestore.MaxFile+1), http.StatusRequestEntityTooLarge},
	} {
		if code, body := postTaskMedia(t, c, x.task, x.node, x.secret, x.body); code != x.want {
			t.Errorf("%s → %s: %d %s, want %d", x.task, x.node, code, body, x.want)
		}
	}
	code, body := postTaskMedia(t, c, "t-cap", mine, "device-secret", capturedPNG)
	if code != http.StatusOK {
		t.Fatalf("kept: %d %s", code, body)
	}
	var got struct{ Src, Type string }
	json.Unmarshal([]byte(body), &got)
	name, ok := strings.CutPrefix(got.Src, mediaScheme)
	if !ok || !nodestore.ValidFileName(name) || got.Type != "image/png" {
		t.Fatalf("answered %s", body)
	}
	if b, err := c.nodes.File(mine, name); err != nil || !bytes.Equal(b, capturedPNG) {
		t.Fatalf("not in its node: %v", err)
	}

	// The task writes it in a block of its node, external: it shows.
	block := "```eriphe:block shot\n" + `{"components": [{"id": "root", "component": "Image", "url": "` + got.Src + `", "alt": "La prod"}]}` + "\n```\n"
	d, _ := c.nodes.Read(mine, 0)
	if _, err := c.nodes.Write(mine, block, d.Version.Hash, "task:t-cap"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.nodes.Create(nodestore.New{Title: "Copie", External: true, Body: block, Author: byBrain}); err != nil {
		t.Fatal(err)
	}
	p, err := tw.w.nodePageOf(mine, 0, "fr")
	if err != nil || len(p.Parts) != 1 || p.Parts[0].Block == nil || p.Parts[0].Block.Error != "" {
		t.Fatalf("page %+v %v", p, err)
	}
	addr := imageURL(t, p.Parts[0].Block.Components)
	code, img, h := tw.do("GET", addr, nil, nil)
	if code != http.StatusOK || img != string(capturedPNG) || h.Get("Content-Type") != "image/png" || h.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("served %d %q %v", code, img, h)
	}
	// Another node naming the same file shows nothing of it: it is
	// signed for its own node, where there is no such file.
	sums, _ := c.nodes.Find("Copie", false, 1)
	cp, _ := tw.w.nodePageOf(sums[0].ID, 0, "fr")
	if code, _, _ := tw.do("GET", imageURL(t, cp.Parts[0].Block.Components), nil, nil); code != http.StatusNotFound {
		t.Errorf("another node's file served: %d", code)
	}
	// A signature is for its address only.
	if code, _, _ := tw.do("GET", "/espace/media?u=media:"+other+"/"+name+"&s=x", nil, nil); code != http.StatusForbidden {
		t.Errorf("unsigned: %d", code)
	}
}

// imageURL is the url of the Image among a block's components.
func imageURL(t *testing.T, comps any) string {
	t.Helper()
	raw, _ := json.Marshal(comps)
	var list []map[string]any
	json.Unmarshal(raw, &list)
	for _, c := range list {
		if u, ok := c["url"].(string); ok && c["component"] == "Image" && strings.HasPrefix(u, mediaPath+"?") {
			return u
		}
	}
	t.Fatalf("no image in %s", raw)
	return ""
}
