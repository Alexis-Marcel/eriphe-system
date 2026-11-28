package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// a tiny PDF: its magic is what DetectContentType reads.
var pdfDoc = []byte("%PDF-1.4\n1 0 obj << >> endobj\ntrailer << >>\n%%EOF\n")

func dataURI(kind string, b []byte) string {
	return "data:" + kind + ";base64," + base64.StdEncoding.EncodeToString(b)
}

func printers(t *testing.T, r renderer, ps ...*fakePrinter) *Printers {
	t.Helper()
	var c Config
	for i, p := range ps {
		c.Accounts = append(c.Accounts, PrinterConfig{Name: []string{"bureau", "salon"}[i], URL: p.url()})
	}
	return NewPrinters(c, r)
}

func TestEncodeRequest(t *testing.T) {
	requestID.Store(6)
	got, err := request{op: opGetPrinterAttributes, operation: []attr{
		{tagCharset, "attributes-charset", []any{"utf-8"}},
		{tagKeyword, "requested-attributes", []any{"a", "bc"}},
	}, job: []attr{{tagInteger, "copies", []any{2}}, {tagBoolean, "x", []any{true}}}}.encode()
	if err != nil {
		t.Fatal(err)
	}
	want := []byte{2, 0, 0, 0x0B, 0, 0, 0, 7,
		0x01,
		0x47, 0, 18, 'a', 't', 't', 'r', 'i', 'b', 'u', 't', 'e', 's', '-', 'c', 'h', 'a', 'r', 's', 'e', 't', 0, 5, 'u', 't', 'f', '-', '8',
		0x44, 0, 20, 'r', 'e', 'q', 'u', 'e', 's', 't', 'e', 'd', '-', 'a', 't', 't', 'r', 'i', 'b', 'u', 't', 'e', 's', 0, 1, 'a',
		0x44, 0, 0, 0, 2, 'b', 'c', // an additional value: no name
		0x02,
		0x21, 0, 6, 'c', 'o', 'p', 'i', 'e', 's', 0, 4, 0, 0, 0, 2,
		0x22, 0, 1, 'x', 0, 1, 1,
		0x03}
	if !bytes.Equal(got, want) {
		t.Fatalf("encoded\n%v\nwant\n%v", got, want)
	}
}

func TestDecodeResponse(t *testing.T) {
	var b bytes.Buffer
	b.Write([]byte{2, 0, 0, 0, 0, 0, 0, 1, tagPrinter})
	writeValue(&b, tagText, "printer-info", "Office")
	// textWithLanguage: language, then text
	b.Write([]byte{tagTextLang, 0, 22})
	b.WriteString("printer-make-and-model")
	b.Write([]byte{0, 2 + 2 + 2 + 5, 0, 2, 'f', 'r', 0, 5, 'M', 'o', 'd', 'e', 'l'})
	// a collection, skipped, then an attribute after it
	writeValue(&b, tagBegCollection, "media-col-default", "")
	writeValue(&b, tagMemberAttrName, "", "media-size")
	writeValue(&b, tagBegCollection, "", "")
	writeValue(&b, tagMemberAttrName, "", "x-dimension")
	writeValue(&b, tagInteger, "", 21000)
	writeValue(&b, tagEndCollection, "", "")
	writeValue(&b, tagEndCollection, "", "")
	writeValue(&b, tagKeyword, "sides-supported", "one-sided")
	writeValue(&b, tagKeyword, "", "two-sided-long-edge")
	// resolution in dots per centimeter, range
	b.Write([]byte{tagResolution, 0, 3, 'r', 'e', 's', 0, 9, 0, 0, 0, 118, 0, 0, 0, 118, 4})
	b.Write([]byte{tagRange, 0, 6, 'c', 'o', 'p', 'i', 'e', 's', 0, 8, 0, 0, 0, 1, 0, 0, 0, 99})
	b.WriteByte(tagEnd)
	b.WriteString("document")
	r, err := decode(b.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if got := r.string("printer-make-and-model"); got != "Model" {
		t.Errorf("make and model %q", got)
	}
	if got := r.strings("sides-supported"); len(got) != 2 || got[1] != "two-sided-long-edge" {
		t.Errorf("sides %v", got)
	}
	if _, ok := r.attrs["media-col-default"]; ok || len(r.attrs["x-dimension"]) > 0 {
		t.Errorf("a collection's members were read: %v", r.attrs)
	}
	if got := r.resolutions("res"); len(got) != 1 || got[0].X != 299 {
		t.Errorf("resolution %v", got)
	}
	if r.rangeMax("copies") != 99 {
		t.Errorf("copies range %v", r.attrs["copies"])
	}
	if string(b.Bytes()[r.size:]) != "document" {
		t.Errorf("size %d does not end the message", r.size)
	}
	if _, err := decode(b.Bytes()[:20]); err == nil {
		t.Error("a truncated answer was read")
	}
}

func TestAddresses(t *testing.T) {
	for raw, want := range map[string]string{
		"ipp://192.0.2.10/ipp/print":  "http://192.0.2.10:631/ipp/print",
		"ipp://192.0.2.10":            "http://192.0.2.10:631/ipp/print",
		"ipps://192.0.2.10:443/ipp/x": "https://192.0.2.10:443/ipp/x",
	} {
		c, err := newIPPClient(raw, 0)
		if err != nil || c.endpoint != want {
			t.Errorf("%s: %v %v, want %s", raw, c, err, want)
		}
	}
	for _, raw := range []string{"http://192.0.2.10/", "192.0.2.10", "ipp://user@192.0.2.10/", "ipp:///x"} {
		if _, err := newIPPClient(raw, 0); err == nil {
			t.Errorf("%s was taken", raw)
		}
	}
}

func TestStatus(t *testing.T) {
	f := newFakePrinter(t, laserAttrs()...)
	st, err := printers(t, nil, f).Status(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if st.Model != "Brother DCP-L2530DW series" || st.State != "idle" || len(st.Reasons) != 0 || st.Color || !st.Duplex {
		t.Errorf("status %+v", st)
	}
	if len(st.Markers) != 1 || st.Markers[0].Level != 40 || st.Paper[0] != "iso_a4_210x297mm" {
		t.Errorf("markers %+v paper %v", st.Markers, st.Paper)
	}
	if f.path != "/ipp/print" {
		t.Errorf("asked at %q", f.path)
	}
}

func TestPrintPDFAsIs(t *testing.T) {
	f := newFakePrinter(t,
		attr{tagEnum, "printer-state", []any{3}},
		attr{tagMimeType, "document-format-supported", []any{"application/pdf", "image/urf"}},
		attr{tagBoolean, "color-supported", []any{true}},
		attr{tagKeyword, "sides-supported", []any{"one-sided", "two-sided-long-edge"}},
		attr{tagRange, "copies-supported", []any{[2]int{1, 99}}},
	)
	r := &fakeRenderer{}
	job, err := printers(t, r, f).Print(context.Background(), PrintIn{Document: dataURI("application/pdf", pdfDoc),
		Title: "Recette", Copies: 2, Sides: "two-sided-long-edge", Color: ptr(false)})
	if err != nil {
		t.Fatal(err)
	}
	if job.ID != 101 || job.State != "pending" || job.Format != "application/pdf" || job.Printer != "bureau" {
		t.Errorf("job %+v", job)
	}
	j := f.job(0)
	if !bytes.Equal(j.doc, pdfDoc) {
		t.Errorf("document %q", j.doc)
	}
	for k, want := range map[string]string{"job-name": "Recette", "document-format": "application/pdf",
		"sides": "two-sided-long-edge", "print-color-mode": "monochrome", "requesting-user-name": "eriphe"} {
		if got := j.attrs.string(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	if n, _ := j.attrs.int("copies"); n != 2 {
		t.Errorf("copies %d", n)
	}
	if len(r.asked) > 0 {
		t.Error("a PDF the printer reads was drawn")
	}
}

func TestPrintPDFAsURF(t *testing.T) {
	f := newFakePrinter(t, laserAttrs()...)
	f.attrs[3] = attr{tagMimeType, "document-format-supported", []any{"image/urf"}}
	r := &fakeRenderer{pages: 3}
	job, err := printers(t, r, f).Print(context.Background(), PrintIn{Document: dataURI("application/pdf", pdfDoc), Sides: "two-sided-long-edge"})
	if err != nil {
		t.Fatal(err)
	}
	if job.Format != "image/urf" || job.Pages != 3 {
		t.Errorf("job %+v", job)
	}
	if len(r.asked) != 2 || r.asked[0] != 600 || r.asked[1] != 1 {
		t.Errorf("drawn at %v, want 600 dpi in gray", r.asked)
	}
	doc := f.job(0).doc
	if string(doc[:8]) != "UNIRAST\x00" || binary.BigEndian.Uint32(doc[8:]) != 3 {
		t.Fatalf("URF head %q", doc[:12])
	}
	rest := doc[12:]
	w, h := 595*600/72, 842*600/72
	for i := 0; i < 3; i++ {
		ph := rest[:32]
		if ph[0] != 8 || ph[1] != 0 || ph[2] != 3 || ph[3] != 4 ||
			binary.BigEndian.Uint32(ph[12:]) != uint32(w) || binary.BigEndian.Uint32(ph[16:]) != uint32(h) ||
			binary.BigEndian.Uint32(ph[20:]) != 600 {
			t.Fatalf("page %d header %v", i, ph)
		}
		var p *page
		p, rest = decodeLines(t, rest[32:], w, h, 1)
		// the drawn page (480×660 at 600 dpi) is scaled to fit A4 and
		// centered; its square is in its top left corner, or bottom
		// right on a back page (DM3: rotated, a half turn).
		scale := min(float64(w)/480, float64(h)/660)
		ox, oy := (w-int(480*scale))/2, (h-int(660*scale))/2
		x, y := ox+1, oy+1
		if i == 1 {
			x, y = w-1-ox-1, h-1-oy-1
		}
		if p.pix[y*w+x] != 0 {
			t.Errorf("page %d: no ink at %d,%d", i, x, y)
		}
		if p.pix[(h/2)*w+w/2] != 0xFF {
			t.Errorf("page %d: ink in its middle", i)
		}
	}
	if len(rest) != 0 {
		t.Errorf("%d bytes after the last page", len(rest))
	}
}

func TestPrintImageAsPWG(t *testing.T) {
	f := newFakePrinter(t, laserAttrs()...)
	f.attrs[3] = attr{tagMimeType, "document-format-supported", []any{"image/pwg-raster"}}
	f.attrs = append(f.attrs, attr{tagResolution, "pwg-raster-document-resolution-supported", []any{resolution{300, 300}}})
	img := image.NewNRGBA(image.Rect(0, 0, 40, 20)) // landscape, turned a quarter
	for i := range img.Pix {
		img.Pix[i] = 0xFF
	}
	img.Set(0, 0, color.Black)
	var b bytes.Buffer
	png.Encode(&b, img)
	job, err := printers(t, &fakeRenderer{}, f).Print(context.Background(), PrintIn{Document: dataURI("image/png", b.Bytes()), Copies: 2})
	if err != nil {
		t.Fatal(err)
	}
	if job.Format != "image/pwg-raster" || job.Pages != 1 {
		t.Errorf("job %+v", job)
	}
	doc := f.job(0).doc
	if string(doc[:4]) != "RaS2" {
		t.Fatalf("PWG head %q", doc[:4])
	}
	w, h := 595*300/72, 842*300/72
	rest := doc[4:]
	for i := 0; i < 2; i++ { // copies-supported says none: the page is repeated
		hd := rest[:pwgHeaderSize]
		u := func(off int) uint32 { return binary.BigEndian.Uint32(hd[off:]) }
		if string(bytes.TrimRight(hd[:64], "\x00")) != "PwgRaster" || u(276) != 300 || u(372) != uint32(w) ||
			u(376) != uint32(h) || u(388) != 8 || u(392) != uint32(w) || u(400) != 18 || u(452) != 2 || u(352) != 595 ||
			string(bytes.TrimRight(hd[1732:], "\x00")) != "iso_a4_210x297mm" {
			t.Fatalf("page %d header: res %d w %d h %d bpp %d space %d total %d", i, u(276), u(372), u(376), u(388), u(400), u(452))
		}
		var p *page
		p, rest = decodeLines(t, rest[pwgHeaderSize:], w, h, 1)
		// turned clockwise: the top left black pixel is now top right
		scale := min(float64(w)/20, float64(h)/40)
		ox, oy := (w-int(20*scale))/2, (h-int(40*scale))/2
		if p.pix[(oy+2)*w+(w-ox-3)] != 0 || p.pix[(oy+2)*w+ox+2] != 0xFF {
			t.Errorf("copy %d: the image was not turned", i)
		}
	}
	if n, ok := f.job(0).attrs.int("copies"); ok {
		t.Errorf("copies %d sent with the pages repeated", n)
	}
}

func TestPrintRefusals(t *testing.T) {
	stopped := newFakePrinter(t, append(laserAttrs(), attr{tagText, "printer-state-message", []any{"Plus de papier"}})...)
	stopped.attrs[1] = attr{tagEnum, "printer-state", []any{5}}
	oneSided := newFakePrinter(t, laserAttrs()...)
	oneSided.attrs[5] = attr{tagKeyword, "sides-supported", []any{"one-sided"}}
	noRaster := newFakePrinter(t, laserAttrs()...)
	noRaster.attrs[3] = attr{tagMimeType, "document-format-supported", []any{"application/postscript"}}
	ok := newFakePrinter(t, laserAttrs()...)
	ctx := context.Background()
	pdf := dataURI("application/pdf", pdfDoc)
	for _, c := range []struct {
		name string
		ps   *Printers
		in   PrintIn
		want string
	}{
		{"stopped", printers(t, nil, stopped), PrintIn{Document: pdf}, "Plus de papier"},
		{"sides", printers(t, nil, oneSided), PrintIn{Document: pdf, Sides: "two-sided-long-edge"}, "does not print two-sided-long-edge"},
		{"formats", printers(t, nil, noRaster), PrintIn{Document: pdf}, "reads none"},
		{"not data", printers(t, nil, ok), PrintIn{Document: "media:n_1/med_1.pdf"}, "one of a node's files"},
		{"not base64", printers(t, nil, ok), PrintIn{Document: "data:application/pdf,%25PDF"}, "base64"},
		{"html", printers(t, nil, ok), PrintIn{Document: dataURI("application/pdf", []byte("<html><script>"))}, "not text/html"},
		{"copies", printers(t, nil, ok), PrintIn{Document: pdf, Copies: 51}, "50 at most"},
		{"which", printers(t, nil, ok, ok), PrintIn{Document: pdf}, "give account"},
		{"unknown", printers(t, nil, ok), PrintIn{Document: pdf, Account: "cave"}, `no printer named "cave"`},
		{"none", NewPrinters(Config{}, nil), PrintIn{Document: pdf}, "no printer is set up"},
	} {
		_, err := c.ps.Print(ctx, c.in)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want %q", c.name, err, c.want)
		}
	}
	if stopped.jobCount()+oneSided.jobCount()+noRaster.jobCount()+ok.jobCount() != 0 {
		t.Error("a refused document reached a printer")
	}
}

func TestConfig(t *testing.T) {
	c, err := LoadConfig(`{"accounts":[{"name":"Bureau","url":"ipp://192.0.2.10/ipp/print"}],"timeout_seconds":30}`, "")
	if err != nil || len(c.Accounts) != 1 || c.TimeoutSeconds != 30 {
		t.Fatalf("%+v %v", c, err)
	}
	if _, err := LoadConfig(`{"accounts":[{"name":"a","url":"x"},{"name":"A","url":"y"}]}`, ""); err == nil {
		t.Error("two printers of one name were taken")
	}
	if c, err := LoadConfig("", ""); err != nil || len(c.Accounts) != 0 {
		t.Errorf("no configuration: %+v %v", c, err)
	}
}

func TestHelpers(t *testing.T) {
	for name, want := range map[string][2]int{"iso_a4_210x297mm": {595, 842}, "na_letter_8.5x11in": {612, 792}, "": {595, 842}, "custom_x": {595, 842}} {
		if w, h := mediaSize(name); w != want[0] || h != want[1] {
			t.Errorf("%q: %dx%d", name, w, h)
		}
	}
	for _, c := range []struct {
		all  []int
		want int
	}{{[]int{300, 600}, 600}, {[]int{360, 720}, 360}, {[]int{150}, 150}, {nil, 0}} {
		if got := pickResolution(c.all); got != c.want {
			t.Errorf("%v: %d", c.all, got)
		}
	}
	if urfResolution([]string{"W8", "RS300-600"}) != 600 || urfSheetBack([]string{"DM2"}) != "flipped" || urfSheetBack(nil) != "normal" {
		t.Error("urf-supported")
	}
	if a, b := backTransform("rotated", false); !a || !b {
		t.Error("rotated long edge is a half turn")
	}
	if a, b := backTransform("flipped", true); !a || b {
		t.Error("flipped short edge is across")
	}
	p := &page{w: 2, h: 2, ch: 1, pix: []byte{1, 2, 3, 4}}
	p.transform(true, true)
	if !bytes.Equal(p.pix, []byte{4, 3, 2, 1}) {
		t.Errorf("half turn %v", p.pix)
	}
	q := (&page{w: 2, h: 1, ch: 1, pix: []byte{1, 2}}).rotate90()
	if q.w != 1 || q.h != 2 || !bytes.Equal(q.pix, []byte{1, 2}) {
		t.Errorf("quarter turn %+v", q)
	}
}

func TestLinesRoundTrip(t *testing.T) {
	p := newPage(300, 5, 3)
	for x := 0; x < 300; x++ {
		if x%3 == 0 || x > 250 {
			p.pix[(2*300+x)*3] = byte(x)
		}
	}
	var b bytes.Buffer
	writeLines(&b, p)
	got, rest := decodeLines(t, b.Bytes(), 300, 5, 3)
	if !bytes.Equal(got.pix, p.pix) || len(rest) != 0 {
		t.Error("lines do not read back")
	}
}

func TestReadPNM(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "p-1.pgm")
	os.WriteFile(f, append([]byte("P5\n# pdftoppm\n3 2\n255\n"), 0, 1, 2, 3, 4, 5), 0o600)
	p, err := readPNM(f)
	if err != nil || p.w != 3 || p.h != 2 || p.ch != 1 || p.pix[5] != 5 {
		t.Fatalf("%+v %v", p, err)
	}
	if pageNumber(filepath.Join(dir, "p-12.ppm")) != 12 {
		t.Error("page number")
	}
}

// TestPdftoppm draws a real PDF when pdftoppm is installed (the image
// has it; a developer's machine may not).
func TestPdftoppm(t *testing.T) {
	if _, err := exec.LookPath("pdftoppm"); err != nil {
		t.Skip("pdftoppm is not installed")
	}
	pdf := "%PDF-1.4\n1 0 obj<</Type/Catalog/Pages 2 0 R>>endobj\n2 0 obj<</Type/Pages/Kids[3 0 R]/Count 1>>endobj\n" +
		"3 0 obj<</Type/Page/Parent 2 0 R/MediaBox[0 0 72 144]>>endobj\ntrailer<</Root 1 0 R>>\n%%EOF\n"
	var got []*page
	err := pdftoppm{bin: "pdftoppm"}.render(context.Background(), []byte(pdf), 100, 1, func(p *page) error { got = append(got, p); return nil })
	if err != nil || len(got) != 1 || got[0].w != 100 || got[0].h != 200 {
		t.Fatalf("%v %v", got, err)
	}
}

func TestTools(t *testing.T) {
	f := newFakePrinter(t, laserAttrs()...)
	server := newServer(printers(t, nil, f), "test")
	ct, st := mcp.NewInMemoryTransports()
	ctx := context.Background()
	go server.Run(ctx, st)
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "t"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	list, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]*mcp.Tool{}
	for _, tl := range list.Tools {
		byName[tl.Name] = tl
	}
	pr := byName["print"]
	if pr == nil || pr.Annotations.ReadOnlyHint || *pr.Annotations.OpenWorldHint != true {
		t.Fatalf("print %+v", pr)
	}
	files, _ := json.Marshal(pr.Meta["eriphe/files"])
	if string(files) != `["document"]` || pr.Meta["eriphe/says"] == nil {
		t.Errorf("print's _meta %v", pr.Meta)
	}
	if s := byName["printer_status"]; s == nil || !s.Annotations.ReadOnlyHint {
		t.Errorf("printer_status %+v", s)
	}
	if s := byName["test_account"]; s == nil || s.Meta["eriphe/setup"] != true {
		t.Errorf("test_account %+v", s)
	}
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "test_account", Arguments: map[string]any{}})
	if err != nil || res.IsError {
		t.Fatalf("%v %v", res, err)
	}
	raw, _ := json.Marshal(res.StructuredContent)
	if string(raw) != `{"detail":"Brother DCP-L2530DW series","ok":true}` {
		t.Errorf("test_account %s", raw)
	}
}
