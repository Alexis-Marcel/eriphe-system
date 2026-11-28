package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// fakePrinter is an IPP printer on httptest: it answers
// Get-Printer-Attributes with its attributes and keeps each Print-Job.
type fakePrinter struct {
	t     *testing.T
	srv   *httptest.Server
	attrs []attr // its printer attributes
	mu    sync.Mutex
	jobs  []fakeJob
	path  string
}

type fakeJob struct {
	attrs response // the request's attributes, operation and job alike
	doc   []byte
}

func newFakePrinter(t *testing.T, attrs ...attr) *fakePrinter {
	f := &fakePrinter{t: t, attrs: attrs}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

// url is the printer's ipp:// address.
func (f *fakePrinter) url() string {
	return "ipp://" + strings.TrimPrefix(f.srv.URL, "http://") + "/ipp/print"
}

func (f *fakePrinter) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.path = r.URL.Path
	f.mu.Unlock()
	if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/ipp" {
		http.Error(w, "not IPP", http.StatusBadRequest)
		return
	}
	body, _ := io.ReadAll(r.Body)
	req, err := decode(body)
	if err != nil {
		f.t.Errorf("fake printer: %v", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	op := req.status
	if req.string("printer-uri") == "" || req.string("attributes-charset") != "utf-8" {
		f.t.Errorf("fake printer: a request without its operation attributes: %v", req.attrs)
	}
	id := binary.BigEndian.Uint32(body[4:8])
	var group byte
	var out []attr
	switch op {
	case opGetPrinterAttributes:
		group, out = tagPrinter, f.attrs
	case opPrintJob:
		f.mu.Lock()
		f.jobs = append(f.jobs, fakeJob{attrs: req, doc: bytes.Clone(body[req.size:])})
		n := len(f.jobs)
		f.mu.Unlock()
		group, out = tagJob, []attr{{tagInteger, "job-id", []any{100 + n}}, {tagEnum, "job-state", []any{3}}}
	default:
		f.t.Errorf("fake printer: operation 0x%04x", op)
	}
	var b bytes.Buffer
	b.Write([]byte{2, 0, 0, 0})
	binary.Write(&b, binary.BigEndian, id)
	b.WriteByte(tagOperation)
	writeValue(&b, tagCharset, "attributes-charset", "utf-8")
	writeValue(&b, tagLanguage, "attributes-natural-language", "en")
	b.WriteByte(group)
	for _, a := range out {
		for i, v := range a.values {
			name := a.name
			if i > 0 {
				name = ""
			}
			writeValue(&b, a.tag, name, v)
		}
	}
	b.WriteByte(tagEnd)
	w.Header().Set("Content-Type", "application/ipp")
	w.Write(b.Bytes())
}

func (f *fakePrinter) job(i int) fakeJob {
	f.mu.Lock()
	defer f.mu.Unlock()
	if i >= len(f.jobs) {
		f.t.Fatalf("fake printer: %d jobs, wanted job %d", len(f.jobs), i)
	}
	return f.jobs[i]
}

func (f *fakePrinter) jobCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.jobs)
}

// A Brother laser as AirPrint describes it: Apple raster, gray, duplex.
func laserAttrs() []attr {
	return []attr{
		{tagText, "printer-make-and-model", []any{"Brother DCP-L2530DW series"}},
		{tagEnum, "printer-state", []any{3}},
		{tagKeyword, "printer-state-reasons", []any{"none"}},
		{tagMimeType, "document-format-supported", []any{"application/octet-stream", "image/urf", "image/pwg-raster"}},
		{tagBoolean, "color-supported", []any{false}},
		{tagKeyword, "sides-supported", []any{"one-sided", "two-sided-long-edge", "two-sided-short-edge"}},
		{tagKeyword, "media-default", []any{"iso_a4_210x297mm"}},
		{tagKeyword, "media-ready", []any{"iso_a4_210x297mm"}},
		{tagName, "marker-names", []any{"Black Toner"}},
		{tagInteger, "marker-levels", []any{40}},
		{tagKeyword, "urf-supported", []any{"V1.4", "CP1", "DM3", "IS1", "MT1-3", "PQ4", "RS300-600", "W8", "OB10"}},
		{tagKeyword, "pwg-raster-document-type-supported", []any{"black_1", "sgray_8"}},
		{tagKeyword, "pwg-raster-document-sheet-back", []any{"rotated"}},
	}
}

// fakeRenderer draws pages of its own: page i is white with a black
// square in its top left corner, i+1 pixels wide, at a tenth of the size
// it would be at dpi.
type fakeRenderer struct {
	pages int
	asked []int // dpi, ch of each call
}

func (r *fakeRenderer) render(_ context.Context, pdf []byte, dpi, ch int, each func(*page) error) error {
	r.asked = append(r.asked, dpi, ch)
	for i := 0; i < r.pages; i++ {
		p := newPage(8*dpi/10, 11*dpi/10, ch)
		for y := 0; y <= i; y++ {
			for x := 0; x <= i; x++ {
				for c := 0; c < ch; c++ {
					p.pix[(y*p.w+x)*ch+c] = 0
				}
			}
		}
		if err := each(p); err != nil {
			return err
		}
	}
	return nil
}

// decodeLines reads a page's compressed lines back, w×h pixels of ch
// bytes each, and what follows them.
func decodeLines(t *testing.T, data []byte, w, h, ch int) (*page, []byte) {
	t.Helper()
	p := &page{w: w, h: h, ch: ch, pix: make([]byte, w*h*ch)}
	i := 0
	for y := 0; y < h; {
		rep := int(data[i]) + 1
		i++
		row := make([]byte, 0, w*ch)
		for len(row) < w*ch {
			c := int(data[i])
			i++
			if c <= 127 {
				for k := 0; k <= c; k++ {
					row = append(row, data[i:i+ch]...)
				}
				i += ch
			} else {
				n := 257 - c
				row = append(row, data[i:i+n*ch]...)
				i += n * ch
			}
		}
		if len(row) != w*ch {
			t.Fatalf("row %d: %d bytes, wanted %d", y, len(row), w*ch)
		}
		for k := 0; k < rep; k++ {
			copy(p.row(y), row)
			y++
		}
	}
	return p, data[i:]
}
