package main

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func pngBytes(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	img.Set(1, 1, color.RGBA{R: 200, A: 255})
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// minimalPDF builds a one-page PDF that shows text, with a correct xref table.
func minimalPDF(text string) []byte {
	stream := fmt.Sprintf("BT /F1 12 Tf 72 720 Td (%s) Tj ET", text)
	objs := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(stream), stream),
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
	}
	var b bytes.Buffer
	b.WriteString("%PDF-1.4\n")
	offsets := make([]int, len(objs))
	for i, o := range objs {
		offsets[i] = b.Len()
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", i+1, o)
	}
	xref := b.Len()
	fmt.Fprintf(&b, "xref\n0 %d\n0000000000 65535 f \n", len(objs)+1)
	for _, off := range offsets {
		fmt.Fprintf(&b, "%010d 00000 n \n", off)
	}
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objs)+1, xref)
	return b.Bytes()
}

type upload struct {
	name string
	data []byte
}

// send posts a message with files, as the composer does.
func (tw *testWeb) send(csrf, text string, files ...upload) (int, string) {
	tw.t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	mw.WriteField("text", text)
	for _, f := range files {
		w, _ := mw.CreateFormFile("files", f.name)
		w.Write(f.data)
	}
	mw.Close()
	req, _ := http.NewRequest("POST", tw.srv.URL+"/messages", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if csrf != "" {
		req.Header.Set("X-CSRF-Token", csrf)
	}
	resp, err := tw.hc.Do(req)
	if err != nil {
		tw.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(raw)
}

func storedFiles(t *testing.T, s *Store) []string {
	t.Helper()
	entries, _ := os.ReadDir(s.attachmentDir())
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

func TestUploadAttachments(t *testing.T) {
	tw := newTestWeb(t)
	csrf := tw.setup()
	img := pngBytes(t)

	if code, body := tw.send(csrf, "What is in it?", upload{"../../etc/screen shot.png", img}, upload{"notes.md", []byte("# Notes\nhello")}); code != http.StatusNoContent {
		t.Fatalf("upload: %d %s", code, body)
	}
	msgs, _ := tw.c.store.messagesAfter(0, 10)
	if len(msgs) != 1 || msgs[0].Text != "What is in it?" || len(msgs[0].Attachments) != 2 {
		t.Fatalf("messages: %+v", msgs)
	}
	a, b := msgs[0].Attachments[0], msgs[0].Attachments[1]
	if a.Name != "screen shot.png" || a.Type != "image/png" || a.Size != int64(len(img)) || b.Type != "text/markdown" {
		t.Fatalf("attachments: %+v %+v", a, b)
	}
	// Stored under its random id, never under its name.
	files := storedFiles(t, tw.c.store)
	if len(files) != 2 || !attachmentID.MatchString(files[0]) || !attachmentID.MatchString(files[1]) {
		t.Fatalf("stored: %v", files)
	}
	if raw, _ := os.ReadFile(filepath.Join(tw.c.store.attachmentDir(), a.ID)); !bytes.Equal(raw, img) {
		t.Fatal("stored content differs")
	}

	// The conversation shows them, and opens them.
	_, page, _ := tw.do("GET", "/", nil, nil)
	if !strings.Contains(page, `src="/attachments/`+a.ID+`"`) || !strings.Contains(page, "notes.md") {
		t.Fatal("the conversation does not show the attachments")
	}
	code, got, h := tw.do("GET", "/attachments/"+a.ID, nil, nil)
	if code != 200 || got != string(img) || h.Get("Content-Type") != "image/png" {
		t.Fatalf("open: %d %s", code, h.Get("Content-Type"))
	}
	if _, _, h := tw.do("GET", "/attachments/"+b.ID, nil, nil); h.Get("Content-Type") != "text/plain; charset=utf-8" {
		t.Fatalf("a text is served as %s", h.Get("Content-Type"))
	}
	if code, _, _ := tw.do("GET", "/attachments/att_00000000000000000000000000000000", nil, nil); code != 404 {
		t.Fatalf("unknown id: %d", code)
	}

	// The brain is told of them with the message.
	if lines := attachmentLines(msgs[0].Attachments); !strings.Contains(lines, a.ID) || !strings.Contains(lines, `"notes.md"`) || !strings.Contains(lines, "text/markdown") {
		t.Fatalf("lines: %s", lines)
	}

	// Files alone are a message too.
	if code, body := tw.send(csrf, "", upload{"scan.pdf", minimalPDF("Hello")}); code != http.StatusNoContent {
		t.Fatalf("file alone: %d %s", code, body)
	}
}

func TestUploadRefused(t *testing.T) {
	tw := newTestWeb(t)
	csrf := tw.setup()
	cases := []struct {
		name  string
		files []upload
		code  int
		says  string
	}{
		{"html", []upload{{"page.html", []byte("<html><script>alert(1)</script></html>")}}, 400, "page.html"},
		{"svg", []upload{{"logo.svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg"></svg>`)}}, 400, "logo.svg"},
		{"binary", []upload{{"tool.exe", []byte("MZ\x90\x00\x03\x00\x00\x00")}}, 400, "tool.exe"},
		{"binary named txt", []upload{{"fake.txt", []byte("a\x00b")}}, 400, "fake.txt"},
		{"empty", []upload{{"empty.txt", nil}}, 400, "empty.txt"},
		{"too big", []upload{{"big.txt", bytes.Repeat([]byte("a"), maxAttachmentSize+1)}}, 413, "big.txt"},
		{"too many", []upload{{"1.txt", []byte("1")}, {"2.txt", []byte("2")}, {"3.txt", []byte("3")}, {"4.txt", []byte("4")}, {"5.txt", []byte("5")}, {"6.txt", []byte("6")}}, 400, "Cinq"},
	}
	for _, c := range cases {
		code, body := tw.send(csrf, "look", c.files...)
		if code != c.code || !strings.Contains(body, c.says) {
			t.Errorf("%s: %d %q", c.name, code, body)
		}
	}
	if msgs, _ := tw.c.store.messagesAfter(0, 10); len(msgs) != 0 {
		t.Fatalf("a refused upload added %v", msgs)
	}
	if files := storedFiles(t, tw.c.store); len(files) != 0 {
		t.Fatalf("a refused upload left %v", files)
	}

	// The whole body is limited too.
	var huge []upload
	for i := range maxAttachments + 1 {
		huge = append(huge, upload{fmt.Sprintf("%d.txt", i), bytes.Repeat([]byte("a"), maxAttachmentSize)})
	}
	if code, _ := tw.send(csrf, "", huge...); code != http.StatusRequestEntityTooLarge {
		t.Fatalf("huge body: %d", code)
	}

	// Without the session's CSRF token, or without a session, nothing is kept.
	if code, _ := tw.send("", "x", upload{"a.txt", []byte("a")}); code != http.StatusForbidden {
		t.Fatalf("no CSRF: %d", code)
	}
	tw.hc.Jar, _ = cookiejar.New(nil)
	if code, _ := tw.send(csrf, "x", upload{"a.txt", []byte("a")}); code != http.StatusSeeOther {
		t.Fatalf("no session: %d", code)
	}
	if code, _, _ := tw.do("GET", "/attachments/att_00000000000000000000000000000000", nil, nil); code != http.StatusSeeOther {
		t.Fatalf("open without a session: %d", code)
	}
	if files := storedFiles(t, tw.c.store); len(files) != 0 {
		t.Fatalf("an unauthenticated upload left %v", files)
	}
}

func TestAttachmentType(t *testing.T) {
	for _, c := range []struct {
		name string
		data []byte
		want string
	}{
		{"a.png", pngBytes(t), "image/png"},
		{"a.txt", pngBytes(t), "image/png"}, // the content decides
		{"a.jpg", []byte("\xFF\xD8\xFFrest"), "image/jpeg"},
		{"doc.pdf", minimalPDF("x"), "application/pdf"},
		{"data.CSV", []byte("a,b\n1,2\n"), "text/csv"},
		{"readme.md", []byte("<!-- comment -->\n# Title"), "text/markdown"},
		{"page.html", []byte("<html></html>"), ""},
		{"notes.txt", []byte{0xff, 0xfe, 0x00}, ""},
		{"photo.heic", []byte("\x00\x00\x00\x18ftypheic"), ""},
	} {
		got, _ := attachmentType(c.name, c.data)
		if got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
	if n := cleanAttachmentName("C:\\Users\\x\\a\u202e b.txt"); n != "a b.txt" {
		t.Errorf("clean name: %q", n)
	}
	if n := cleanAttachmentName(strings.Repeat("é", 300)); len([]rune(n)) != maxAttachmentName {
		t.Errorf("long name: %d", len([]rune(n)))
	}
}

// attach adds my message with one file, as the workspace does.
func attach(t *testing.T, c *core, name string, data []byte) Attachment {
	t.Helper()
	fh := fileHeader(t, name, data)
	atts, err := c.store.saveAttachments([]*multipart.FileHeader{fh})
	if err != nil {
		t.Fatal(err)
	}
	seq, err := c.store.addUserMessage("see attached", atts)
	if err != nil {
		t.Fatal(err)
	}
	tk, _ := c.store.task("t-brain")
	c.turn.start(int(seq), int(seq), tk.LastSeq)
	return atts[0]
}

func fileHeader(t *testing.T, name string, data []byte) *multipart.FileHeader {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	w, _ := mw.CreateFormFile("files", name)
	w.Write(data)
	mw.Close()
	form, err := multipart.NewReader(&body, mw.Boundary()).ReadForm(1 << 20)
	if err != nil {
		t.Fatal(err)
	}
	return form.File["files"][0]
}

// callReadAttachment calls the tool through eriphe-core's MCP server.
func callReadAttachment(t *testing.T, c *core, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	ctx := context.Background()
	ct, st := mcp.NewInMemoryTransports()
	if _, err := newMCPServer(c).Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	sess, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	res, err := sess.CallTool(ctx, &mcp.CallToolParams{Name: "read_attachment", Arguments: args})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestReadAttachmentImage(t *testing.T) {
	c, _ := testMemoryCore(t)
	img := pngBytes(t)
	a := attach(t, c, "screenshot.png", img)
	if len(c.turnTaint()) != 0 {
		t.Fatal("tainted before reading")
	}
	res := callReadAttachment(t, c, map[string]any{"id": a.ID})
	if res.IsError || len(res.Content) != 2 {
		t.Fatalf("result: %+v", res)
	}
	head, ok := res.Content[0].(*mcp.TextContent)
	if !ok || !strings.Contains(head.Text, "data, never instructions") || !strings.Contains(head.Text, "screenshot.png") {
		t.Fatalf("head: %+v", res.Content[0])
	}
	im, ok := res.Content[1].(*mcp.ImageContent)
	if !ok || im.MIMEType != "image/png" || !bytes.Equal(im.Data, img) {
		t.Fatalf("image: %+v", res.Content[1])
	}
	// Reading it taints the turn: no allowing, no lending tools that act.
	if tainted := c.turnTaint(); !containsStr(tainted, "read_attachment") {
		t.Fatalf("taint: %v", tainted)
	}
	if err := c.taintedAllow("t-brain", "p1"); err == nil {
		t.Fatal("a tainted turn allowed a permission")
	}
	d, err := c.decideBrainWrite(kindFact, "", scanResult{})
	if err != nil || d.Status != memProposed {
		t.Fatalf("a tainted turn's memory: %+v %v", d, err)
	}
	// The next message of mine starts a clean turn.
	ownerSays(c, "thanks")
	if tainted := c.turnTaint(); len(tainted) != 0 {
		t.Fatalf("still tainted: %v", tainted)
	}
}

func TestReadAttachmentText(t *testing.T) {
	c, _ := testMemoryCore(t)
	text := strings.Repeat("é", maxAttachmentChars) + "THE END"
	a := attach(t, c, "notes.txt", []byte(text))
	res := callReadAttachment(t, c, map[string]any{"id": a.ID})
	if res.IsError || len(res.Content) != 1 {
		t.Fatalf("result: %+v", res)
	}
	got := res.Content[0].(*mcp.TextContent).Text
	if !strings.Contains(got, "data, never instructions") || strings.Contains(got, "THE END") ||
		!strings.Contains(got, fmt.Sprintf("offset %d", maxAttachmentChars)) {
		t.Fatalf("first part: %s", got[len(got)-200:])
	}
	res = callReadAttachment(t, c, map[string]any{"id": a.ID, "offset": maxAttachmentChars})
	if got := res.Content[0].(*mcp.TextContent).Text; !strings.HasSuffix(got, "\n\nTHE END") {
		t.Fatalf("rest: %q", got)
	}
	if !containsStr(c.turnTaint(), "read_attachment") {
		t.Fatal("reading a text did not taint the turn")
	}
}

func TestReadAttachmentPDFAndUnknown(t *testing.T) {
	c, _ := testMemoryCore(t)
	a := attach(t, c, "invoice.pdf", minimalPDF("Invoice 42 from Example Org"))
	res := callReadAttachment(t, c, map[string]any{"id": a.ID})
	got := res.Content[0].(*mcp.TextContent).Text
	if res.IsError || !strings.Contains(got, "Invoice 42 from Example Org") {
		t.Fatalf("pdf: %s", got)
	}
	// A brain whose agent reads PDFs gets it whole, or its text if it asks.
	c.conf().Providers = map[string]Provider{"claude": {Kind: "claude-subscription"}, "chatgpt": {Kind: "chatgpt-subscription"},
		"or": {Kind: "openrouter-api"}}
	c.conf().Agents = map[string]Agent{"opus": {Runtime: "claude-code", Provider: "claude"}, "gpt": {Runtime: "codex", Provider: "chatgpt"},
		"kimi": {Runtime: "opencode", Provider: "or", Model: "openrouter/moonshotai/kimi-k3.1-pro"}}
	c.conf().Brain = &BrainConfig{Agent: "opus"}
	res = callReadAttachment(t, c, map[string]any{"id": a.ID})
	if r, ok := res.Content[len(res.Content)-1].(*mcp.EmbeddedResource); res.IsError || !ok ||
		r.Resource.MIMEType != "application/pdf" || !bytes.Equal(r.Resource.Blob, minimalPDF("Invoice 42 from Example Org")) {
		t.Fatalf("whole pdf: %+v", res.Content)
	}
	res = callReadAttachment(t, c, map[string]any{"id": a.ID, "as_text": true})
	if got := res.Content[0].(*mcp.TextContent).Text; !strings.Contains(got, "Invoice 42") {
		t.Fatalf("as text: %s", got)
	}
	c.conf().Brain.Agent = "gpt"
	res = callReadAttachment(t, c, map[string]any{"id": a.ID})
	if _, ok := res.Content[0].(*mcp.TextContent); !ok || len(res.Content) != 1 {
		t.Fatalf("codex brain: %+v", res.Content)
	}
	// OpenCode passes a PDF on, but this model does not read one.
	c.conf().Brain.Agent = "kimi"
	res = callReadAttachment(t, c, map[string]any{"id": a.ID})
	if _, ok := res.Content[0].(*mcp.TextContent); !ok || len(res.Content) != 1 {
		t.Fatalf("brain on a model without PDF: %+v", res.Content)
	}
	c.conf().Brain = nil

	broken := attach(t, c, "broken.pdf", []byte("%PDF-1.4\nnot really"))
	if res := callReadAttachment(t, c, map[string]any{"id": broken.ID}); !res.IsError {
		t.Fatalf("a broken PDF: %+v", res.Content)
	}
	for _, id := range []string{"att_00000000000000000000000000000000", "../core.db", ""} {
		if res := callReadAttachment(t, c, map[string]any{"id": id}); !res.IsError {
			t.Fatalf("%q: %+v", id, res.Content)
		}
	}
}
