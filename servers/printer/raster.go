package main

import (
	"bytes"
	"encoding/binary"
	"io"
	"strconv"
	"strings"
)

// Raster documents: what a printer without a PDF interpreter prints.
// Two formats share one line compression: Apple raster (image/urf, every
// AirPrint printer) and PWG raster (image/pwg-raster, IPP Everywhere).
// Pages are 8-bit gray or 8-bit sRGB, white is all ones.

// page is one page's pixels: 1 channel (gray) or 3 (sRGB), rows top down.
type page struct {
	w, h, ch int
	pix      []byte
}

func newPage(w, h, ch int) *page {
	p := &page{w: w, h: h, ch: ch, pix: make([]byte, w*h*ch)}
	for i := range p.pix {
		p.pix[i] = 0xFF
	}
	return p
}

func (p *page) row(y int) []byte { return p.pix[y*p.w*p.ch : (y+1)*p.w*p.ch] }

// fit draws src on a blank page of w×h, scaled to fit (keeping its
// proportions) and centered; a landscape src on a portrait page is turned
// a quarter first. src is converted to ch channels.
func fit(src *page, w, h, ch int) *page {
	if (src.w > src.h) != (w > h) && src.w != src.h {
		src = src.rotate90()
	}
	dst := newPage(w, h, ch)
	scale := min(float64(w)/float64(src.w), float64(h)/float64(src.h))
	sw, sh := max(1, int(float64(src.w)*scale)), max(1, int(float64(src.h)*scale))
	ox, oy := (w-sw)/2, (h-sh)/2
	px := make([]byte, 3)
	for y := 0; y < sh; y++ {
		sy := min(src.h-1, int(float64(y)/scale))
		for x := 0; x < sw; x++ {
			sx := min(src.w-1, int(float64(x)/scale))
			src.at(sx, sy, px)
			o := ((oy+y)*w + ox + x) * ch
			if ch == 1 {
				dst.pix[o] = gray(px)
			} else {
				copy(dst.pix[o:o+3], px)
			}
		}
	}
	return dst
}

// at puts the pixel at x, y in px as sRGB.
func (p *page) at(x, y int, px []byte) {
	o := (y*p.w + x) * p.ch
	if p.ch == 1 {
		px[0], px[1], px[2] = p.pix[o], p.pix[o], p.pix[o]
		return
	}
	copy(px, p.pix[o:o+3])
}

func gray(px []byte) byte {
	return byte((299*int(px[0]) + 587*int(px[1]) + 114*int(px[2]) + 500) / 1000)
}

// rotate90 is the page turned a quarter clockwise.
func (p *page) rotate90() *page {
	out := &page{w: p.h, h: p.w, ch: p.ch, pix: make([]byte, len(p.pix))}
	for y := 0; y < p.h; y++ {
		for x := 0; x < p.w; x++ {
			s := (y*p.w + x) * p.ch
			d := (x*out.w + (out.w - 1 - y)) * p.ch
			copy(out.pix[d:d+p.ch], p.pix[s:s+p.ch])
		}
	}
	return out
}

// transform flips the page in place: across (left-right), along
// (top-bottom), both is a half turn. A printer that prints the back of a
// sheet another way asks for it (sheet-back).
func (p *page) transform(across, along bool) {
	if along {
		for y := 0; y < p.h/2; y++ {
			a, b := p.row(y), p.row(p.h-1-y)
			for i := range a {
				a[i], b[i] = b[i], a[i]
			}
		}
	}
	if across {
		for y := 0; y < p.h; y++ {
			r := p.row(y)
			for l, rr := 0, p.w-1; l < rr; l, rr = l+1, rr-1 {
				for c := 0; c < p.ch; c++ {
					r[l*p.ch+c], r[rr*p.ch+c] = r[rr*p.ch+c], r[l*p.ch+c]
				}
			}
		}
	}
}

// backTransform is how the back of a sheet is flipped, for a printer's
// sheet-back (normal, flipped, rotated, manual-tumble) and the sides
// asked, as PWG 5102.4 and CUPS read them.
func backTransform(sheetBack string, shortEdge bool) (across, along bool) {
	switch sheetBack {
	case "flipped":
		if shortEdge {
			return true, false
		}
		return false, true
	case "rotated":
		if shortEdge {
			return false, false
		}
		return true, true
	case "manual-tumble":
		if shortEdge {
			return true, true
		}
	}
	return false, false
}

// rasterJob is how the pages are laid out for the printer.
type rasterJob struct {
	dpi       int
	duplex    bool
	shortEdge bool   // duplex turned on the short edge (tumble)
	quality   int    // 3 draft, 4 normal, 5 high
	pageSize  [2]int // in points, for PWG
	sizeName  string // the PWG media name, for PWG
}

// writeLines compresses a page's rows: each group of identical rows is
// a repeat count (rows - 1), then the row as runs of repeated pixels
// (count - 1, 0 to 127, then the pixel) or of literal pixels (257 - count,
// 129 to 255, then the pixels).
func writeLines(w io.Writer, p *page) error {
	var b bytes.Buffer
	bpp := p.ch
	for y := 0; y < p.h; {
		r := p.row(y)
		n := 1
		for y+n < p.h && n < 256 && bytes.Equal(p.row(y+n), r) {
			n++
		}
		b.WriteByte(byte(n - 1))
		for x := 0; x < p.w; {
			px := r[x*bpp : (x+1)*bpp]
			run := 1
			for x+run < p.w && run < 128 && bytes.Equal(r[(x+run)*bpp:(x+run+1)*bpp], px) {
				run++
			}
			if run > 1 {
				b.WriteByte(byte(run - 1))
				b.Write(px)
				x += run
				continue
			}
			// literal pixels, up to the next repeat
			lit := 1
			for x+lit < p.w && lit < 128 {
				next := r[(x+lit)*bpp : (x+lit+1)*bpp]
				if x+lit+1 < p.w && bytes.Equal(next, r[(x+lit+1)*bpp:(x+lit+2)*bpp]) {
					break
				}
				lit++
			}
			b.WriteByte(byte(257 - lit))
			b.Write(r[x*bpp : (x+lit)*bpp])
			x += lit
		}
		y += n
		if b.Len() > 1<<16 {
			if _, err := w.Write(b.Bytes()); err != nil {
				return err
			}
			b.Reset()
		}
	}
	_, err := w.Write(b.Bytes())
	return err
}

// encoded is a page whose lines are compressed (writeLines): what a job
// keeps of each page while the next is drawn.
type encoded struct {
	w, h, ch int
	lines    []byte
}

func encode(p *page) encoded {
	var b bytes.Buffer
	writeLines(&b, p) // a bytes.Buffer does not fail
	return encoded{w: p.w, h: p.h, ch: p.ch, lines: b.Bytes()}
}

// writeURF writes pages as Apple raster: "UNIRAST", the page count, then
// each page's 32-byte header and lines.
func writeURF(w io.Writer, pages []encoded, j rasterJob) error {
	head := make([]byte, 12)
	copy(head, "UNIRAST\x00")
	binary.BigEndian.PutUint32(head[8:], uint32(len(pages)))
	if _, err := w.Write(head); err != nil {
		return err
	}
	for _, p := range pages {
		h := make([]byte, 32)
		h[0] = byte(8 * p.ch) // bits per pixel
		if p.ch == 3 {
			h[1] = 1 // sRGB; 0 is sGray
		}
		h[2] = 1 // one-sided
		if j.duplex {
			h[2] = 3 // long edge
			if j.shortEdge {
				h[2] = 2
			}
		}
		h[3] = byte(j.quality)
		binary.BigEndian.PutUint32(h[12:], uint32(p.w))
		binary.BigEndian.PutUint32(h[16:], uint32(p.h))
		binary.BigEndian.PutUint32(h[20:], uint32(j.dpi))
		if _, err := w.Write(h); err != nil {
			return err
		}
		if _, err := w.Write(p.lines); err != nil {
			return err
		}
	}
	return nil
}

// PWG raster's page header: 1796 bytes, big-endian (PWG 5102.4).
const pwgHeaderSize = 1796

// writePWG writes pages as PWG raster: "RaS2", then each page's header
// and lines.
func writePWG(w io.Writer, pages []encoded, j rasterJob) error {
	if _, err := io.WriteString(w, "RaS2"); err != nil {
		return err
	}
	for _, p := range pages {
		h := make([]byte, pwgHeaderSize)
		put := func(off int, v uint32) { binary.BigEndian.PutUint32(h[off:], v) }
		copy(h[0:64], "PwgRaster")
		if j.duplex {
			put(272, 1)
			if j.shortEdge {
				put(368, 1) // Tumble
			}
		}
		put(276, uint32(j.dpi))
		put(280, uint32(j.dpi))
		put(352, uint32(j.pageSize[0]))
		put(356, uint32(j.pageSize[1]))
		put(372, uint32(p.w))
		put(376, uint32(p.h))
		put(384, 8)              // bits per color
		put(388, uint32(8*p.ch)) // bits per pixel
		put(392, uint32(p.w*p.ch))
		put(396, 0) // chunky
		if p.ch == 1 {
			put(400, 18) // sGray
		} else {
			put(400, 19) // sRGB
		}
		put(420, uint32(p.ch))
		put(452, uint32(len(pages)))
		put(456, 1) // cross-feed transform
		put(460, 1) // feed transform
		put(472, uint32(p.w))
		put(476, uint32(p.h))
		if p.ch == 3 {
			put(480, 0x00FFFFFF) // alternate primary: white
		}
		put(484, uint32(j.quality))
		copy(h[1732:1796], j.sizeName)
		if _, err := w.Write(h); err != nil {
			return err
		}
		if _, err := w.Write(p.lines); err != nil {
			return err
		}
	}
	return nil
}

// mediaSize reads a PWG self-describing media name (iso_a4_210x297mm,
// na_letter_8.5x11in) as its width and height in points; A4 when it says
// none.
func mediaSize(name string) (w, h int) {
	if i := strings.LastIndexByte(name, '_'); i >= 0 {
		name = name[i+1:]
	}
	per := 0.0
	switch {
	case strings.HasSuffix(name, "mm"):
		per = 72 / 25.4
	case strings.HasSuffix(name, "in"):
		per = 72
	}
	a, b, ok := strings.Cut(strings.TrimRight(name, "mni"), "x")
	x, errA := strconv.ParseFloat(a, 64)
	y, errB := strconv.ParseFloat(b, 64)
	if !ok || per == 0 || errA != nil || errB != nil || x <= 0 || y <= 0 {
		return 595, 842
	}
	return int(x*per + 0.5), int(y*per + 0.5)
}
