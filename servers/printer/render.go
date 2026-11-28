package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	_ "image/jpeg" // decoded for printers without image/jpeg
	_ "image/png"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// renderer turns a PDF into pages at a resolution, in gray (ch 1) or
// sRGB (ch 3), calling each page in turn so that one at a time is held.
type renderer interface {
	render(ctx context.Context, pdf []byte, dpi, ch int, each func(*page) error) error
}

// maxPages is the most pages one document is printed with.
const maxPages = 200

// pdftoppm renders with poppler's pdftoppm, in a folder of its own under
// the temporary directory, removed after.
type pdftoppm struct{ bin string }

func (r pdftoppm) render(ctx context.Context, pdf []byte, dpi, ch int, each func(*page) error) error {
	if _, err := exec.LookPath(r.bin); err != nil {
		return errors.New("this server cannot read a PDF: pdftoppm is not in its image")
	}
	dir, err := os.MkdirTemp("", "print-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	in := filepath.Join(dir, "in.pdf")
	if err := os.WriteFile(in, pdf, 0o600); err != nil {
		return err
	}
	args := []string{"-r", strconv.Itoa(dpi), "-l", strconv.Itoa(maxPages)}
	if ch == 1 {
		args = append(args, "-gray")
	}
	args = append(args, in, filepath.Join(dir, "p"))
	cmd := exec.CommandContext(ctx, r.bin, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("the PDF could not be read: %s", strings.TrimSpace(cmp(stderr.String(), err.Error())))
	}
	files, _ := filepath.Glob(filepath.Join(dir, "p-*.p?m"))
	sort.Slice(files, func(i, j int) bool { return pageNumber(files[i]) < pageNumber(files[j]) })
	if len(files) == 0 {
		return errors.New("the PDF has no page")
	}
	for _, f := range files {
		p, err := readPNM(f)
		os.Remove(f)
		if err != nil {
			return err
		}
		if err := each(p); err != nil {
			return err
		}
	}
	return nil
}

func cmp(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func pageNumber(f string) int {
	base := strings.TrimSuffix(filepath.Base(f), filepath.Ext(f))
	n, _ := strconv.Atoi(base[strings.LastIndexByte(base, '-')+1:])
	return n
}

// readPNM reads a binary PGM (P5) or PPM (P6) of 8 bits, as pdftoppm
// writes them.
func readPNM(path string) (*page, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	br := bufio.NewReader(f)
	var head [4]string // magic, width, height, maximum value
	for i := range head {
		tok, err := pnmToken(br)
		if err != nil {
			return nil, fmt.Errorf("a rendered page: %w", err)
		}
		head[i] = tok
	}
	magic := head[0]
	w, _ := strconv.Atoi(head[1])
	h, _ := strconv.Atoi(head[2])
	maxv, _ := strconv.Atoi(head[3])
	ch := 1
	switch magic {
	case "P5":
	case "P6":
		ch = 3
	default:
		return nil, fmt.Errorf("a rendered page of kind %q", magic)
	}
	if maxv != 255 || w <= 0 || h <= 0 || w > 20000 || h > 20000 {
		return nil, errors.New("a rendered page of an unexpected size")
	}
	p := &page{w: w, h: h, ch: ch, pix: make([]byte, w*h*ch)}
	if _, err := io.ReadFull(br, p.pix); err != nil {
		return nil, fmt.Errorf("a rendered page: %w", err)
	}
	return p, nil
}

// pnmToken reads one word of a PNM header, past whitespace and # comments,
// and the one whitespace after it.
func pnmToken(br *bufio.Reader) (string, error) {
	var tok []byte
	for {
		c, err := br.ReadByte()
		if err != nil {
			return "", err
		}
		switch {
		case c == '#' && len(tok) == 0:
			if _, err := br.ReadString('\n'); err != nil {
				return "", err
			}
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			if len(tok) > 0 {
				return string(tok), nil
			}
		default:
			if len(tok) > 20 {
				return "", errors.New("not a PNM header")
			}
			tok = append(tok, c)
		}
	}
}

// decodeImage reads a JPEG or a PNG as one page.
func decodeImage(data []byte) (*page, error) {
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("the image could not be read: %w", err)
	}
	b := img.Bounds()
	if b.Dx() > 20000 || b.Dy() > 20000 {
		return nil, errors.New("the image is too large")
	}
	p := &page{w: b.Dx(), h: b.Dy(), ch: 3, pix: make([]byte, b.Dx()*b.Dy()*3)}
	for y := 0; y < p.h; y++ {
		for x := 0; x < p.w; x++ {
			c := color.NRGBAModel.Convert(img.At(b.Min.X+x, b.Min.Y+y)).(color.NRGBA)
			// on white paper: blend what is transparent with white
			a := int(c.A)
			o := (y*p.w + x) * 3
			p.pix[o] = byte((int(c.R)*a + 255*(255-a)) / 255)
			p.pix[o+1] = byte((int(c.G)*a + 255*(255-a)) / 255)
			p.pix[o+2] = byte((int(c.B)*a + 255*(255-a)) / 255)
		}
	}
	return p, nil
}
