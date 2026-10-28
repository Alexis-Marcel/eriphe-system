package main

import (
	"fmt"
	"html/template"
	"strings"

	qrcode "github.com/skip2/go-qrcode"
)

// qrSVG draws a QR code of text as an inline SVG, in Go: no image service
// sees the address. The encoder is the one Tailscale's own CLI uses (already
// in the dependency graph). Dark modules on white, whatever the theme, with
// the 4-module quiet zone the readers expect.
func qrSVG(text, label string) (template.HTML, error) {
	q, err := qrcode.New(text, qrcode.Medium)
	if err != nil {
		return "", err
	}
	bits := q.Bitmap() // with its quiet zone
	n := len(bits)
	var path strings.Builder
	for y, row := range bits {
		for x := 0; x < len(row); {
			if !row[x] {
				x++
				continue
			}
			start := x
			for x < len(row) && row[x] {
				x++
			}
			fmt.Fprintf(&path, "M%d %dh%dv1h-%dz", start, y, x-start, x-start)
		}
	}
	svg := fmt.Sprintf(`<svg class="qr" xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" shape-rendering="crispEdges" role="img" aria-label="%s">`+
		`<rect width="%d" height="%d" fill="#fff"/><path fill="#000" d="%s"/></svg>`,
		n, n, template.HTMLEscapeString(label), n, n, path.String())
	return template.HTML(svg), nil
}
