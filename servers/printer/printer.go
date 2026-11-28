package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"slices"
	"strings"
	"time"
)

// Config is the server's configuration (servers/README.md), read as JSON
// from TOOL_CONFIG or the file TOOL_CONFIG_FILE names: its accounts are
// its printers.
type Config struct {
	Accounts []PrinterConfig `json:"accounts"`
	// TimeoutSeconds bounds one exchange with a printer (default 120: a
	// long document takes time to send).
	TimeoutSeconds int `json:"timeout_seconds,omitempty"`
}

// PrinterConfig is one printer: its name and its address.
type PrinterConfig struct {
	Name string `json:"name"`
	URL  string `json:"url"` // ipp://192.0.2.10/ipp/print
}

// LoadConfig reads the configuration inline or from a file.
func LoadConfig(inline, file string) (Config, error) {
	var c Config
	raw := []byte(inline)
	if inline == "" && file != "" {
		b, err := os.ReadFile(file)
		if err != nil {
			return c, err
		}
		raw = b
	}
	if len(bytes.TrimSpace(raw)) > 0 {
		if err := json.Unmarshal(raw, &c); err != nil {
			return c, fmt.Errorf("configuration: %w", err)
		}
	}
	seen := map[string]bool{}
	for _, p := range c.Accounts {
		key := strings.ToLower(strings.TrimSpace(p.Name))
		if key == "" {
			return c, errors.New("configuration: a printer without a name")
		}
		if seen[key] {
			return c, fmt.Errorf("configuration: two printers named %q", p.Name)
		}
		seen[key] = true
	}
	return c, nil
}

// Printers are the configured printers.
type Printers struct {
	list     []PrinterConfig
	timeout  time.Duration
	renderer renderer
}

func NewPrinters(c Config, r renderer) *Printers {
	t := time.Duration(c.TimeoutSeconds) * time.Second
	if t <= 0 {
		t = 120 * time.Second
	}
	return &Printers{list: c.Accounts, timeout: t, renderer: r}
}

// client is the printer named (any one when it is the only one).
func (ps *Printers) client(name string) (*ippClient, PrinterConfig, error) {
	var p PrinterConfig
	switch {
	case len(ps.list) == 0:
		return nil, p, errors.New("no printer is set up: add its address in Eriphe's settings")
	case name == "" && len(ps.list) == 1:
		p = ps.list[0]
	case name == "":
		return nil, p, fmt.Errorf("several printers: give account, one of %s", ps.names())
	default:
		i := slices.IndexFunc(ps.list, func(x PrinterConfig) bool { return strings.EqualFold(x.Name, name) })
		if i < 0 {
			return nil, p, fmt.Errorf("no printer named %q: %s", name, ps.names())
		}
		p = ps.list[i]
	}
	c, err := newIPPClient(p.URL, ps.timeout)
	return c, p, err
}

func (ps *Printers) names() string {
	var n []string
	for _, p := range ps.list {
		n = append(n, p.Name)
	}
	return strings.Join(n, ", ")
}

// printerAttrs are what this server asks a printer.
var printerAttrs = []any{
	"printer-make-and-model", "printer-info", "printer-state", "printer-state-reasons", "printer-state-message",
	"document-format-supported", "color-supported", "sides-supported", "copies-supported",
	"media-default", "media-ready", "marker-names", "marker-levels", "marker-types",
	"pwg-raster-document-resolution-supported", "pwg-raster-document-type-supported", "pwg-raster-document-sheet-back",
	"urf-supported", "printer-resolution-default",
}

func (c *ippClient) attributes(ctx context.Context) (response, error) {
	return c.do(ctx, request{op: opGetPrinterAttributes, operation: c.operation(
		attr{tagKeyword, "requested-attributes", printerAttrs},
	)}, nil)
}

// Marker is an ink or toner, with its level in percent (-1: unknown).
type Marker struct {
	Name  string `json:"name"`
	Level int    `json:"level"`
}

// Status is what a printer says of itself.
type Status struct {
	Printer string   `json:"printer"`
	Model   string   `json:"model,omitempty"`
	State   string   `json:"state"`             // idle, printing or stopped
	Reasons []string `json:"reasons,omitempty"` // media-empty, toner-low…, none when all is well
	Message string   `json:"message,omitempty"` // the printer's own words
	Color   bool     `json:"color"`
	Duplex  bool     `json:"two_sided"`
	Paper   []string `json:"paper,omitempty"` // the paper loaded, as PWG media names
	Markers []Marker `json:"markers,omitempty"`
}

func statusOf(name string, a response) Status {
	s := Status{Printer: name, Model: cmp(a.string("printer-make-and-model"), a.string("printer-info")),
		Message: a.string("printer-state-message"), Color: a.bool("color-supported"), Paper: a.strings("media-ready")}
	switch n, _ := a.int("printer-state"); n {
	case 3:
		s.State = "idle"
	case 4:
		s.State = "printing"
	case 5:
		s.State = "stopped"
	default:
		s.State = "unknown"
	}
	for _, r := range a.strings("printer-state-reasons") {
		if r != "none" {
			s.Reasons = append(s.Reasons, r)
		}
	}
	for _, side := range a.strings("sides-supported") {
		if strings.HasPrefix(side, "two-sided") {
			s.Duplex = true
		}
	}
	levels := a.ints("marker-levels")
	for i, n := range a.strings("marker-names") {
		m := Marker{Name: n, Level: -1}
		if i < len(levels) && levels[i] >= 0 && levels[i] <= 100 {
			m.Level = levels[i]
		}
		s.Markers = append(s.Markers, m)
	}
	return s
}

// Status asks a printer how it is.
func (ps *Printers) Status(ctx context.Context, name string) (Status, error) {
	c, p, err := ps.client(name)
	if err != nil {
		return Status{}, err
	}
	a, err := c.attributes(ctx)
	if err != nil {
		return Status{}, err
	}
	return statusOf(p.Name, a), nil
}

// PrintIn is one document to print.
type PrintIn struct {
	Account  string
	Document string // a data: URI (a PDF, a JPEG or a PNG)
	Title    string
	Copies   int
	Sides    string // one-sided, two-sided-long-edge, two-sided-short-edge
	Color    *bool  // nil: the printer's own way
}

// Job is a job the printer took.
type Job struct {
	Printer string `json:"printer"`
	ID      int    `json:"job_id"`
	State   string `json:"state,omitempty"`
	Pages   int    `json:"pages,omitempty"` // when this server made the pages (a raster)
	Format  string `json:"format"`          // what was sent
}

// maxDocumentBytes is the most a document may weigh.
const maxDocumentBytes = 20 << 20

// documentOf reads a data: URI, by its bytes and not what it says.
func documentOf(uri string) ([]byte, string, error) {
	rest, ok := strings.CutPrefix(strings.TrimSpace(uri), "data:")
	if !ok {
		return nil, "", errors.New("document: one of a node's files (media:<node>/<name>), which Eriphe sends as a data: URI")
	}
	meta, payload, ok := strings.Cut(rest, ",")
	if !ok || !strings.HasSuffix(meta, ";base64") {
		return nil, "", errors.New("document: a base64 data: URI")
	}
	if base64.StdEncoding.DecodedLen(len(payload)) > maxDocumentBytes {
		return nil, "", fmt.Errorf("document: %d MiB at most", maxDocumentBytes>>20)
	}
	data, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return nil, "", errors.New("document: its base64 could not be read")
	}
	kind := http.DetectContentType(data)
	switch kind {
	case "application/pdf", "image/jpeg", "image/png":
		return data, kind, nil
	}
	return nil, "", fmt.Errorf("document: a PDF, a JPEG or a PNG, not %s", kind)
}

// Print sends one document to a printer: as it is when the printer reads
// its format, else as pages this server draws in a raster it reads.
func (ps *Printers) Print(ctx context.Context, in PrintIn) (Job, error) {
	data, kind, err := documentOf(in.Document)
	if err != nil {
		return Job{}, err
	}
	c, p, err := ps.client(in.Account)
	if err != nil {
		return Job{}, err
	}
	a, err := c.attributes(ctx)
	if err != nil {
		return Job{}, err
	}
	if n, _ := a.int("printer-state"); n == 5 {
		return Job{}, fmt.Errorf("the printer is stopped: %s", cmp(a.string("printer-state-message"), strings.Join(a.strings("printer-state-reasons"), ", ")))
	}
	copies := max(in.Copies, 1)
	if copies > 50 {
		return Job{}, errors.New("copies: 50 at most")
	}
	sides := cmp(in.Sides, "one-sided")
	if sides != "one-sided" && !slices.Contains(a.strings("sides-supported"), sides) {
		return Job{}, fmt.Errorf("this printer does not print %s: %s", sides, strings.Join(a.strings("sides-supported"), ", "))
	}
	color := a.bool("color-supported")
	if in.Color != nil {
		color = color && *in.Color
	}
	title := cmp(strings.TrimSpace(in.Title), "Eriphe")
	if len(title) > 200 {
		title = title[:200]
	}

	formats := a.strings("document-format-supported")
	job := Job{Printer: p.Name}
	doc := data
	switch {
	case slices.Contains(formats, kind):
		job.Format = kind
	case slices.Contains(formats, "image/pwg-raster"):
		job.Format = "image/pwg-raster"
	case slices.Contains(formats, "image/urf"):
		job.Format = "image/urf"
	default:
		return Job{}, fmt.Errorf("this printer reads none of what this server sends (%s): it reads %s", kind, strings.Join(formats, ", "))
	}
	rasterCopies := 1
	if job.Format != kind {
		if a.rangeMax("copies-supported") < copies {
			rasterCopies, copies = copies, 1 // the pages are repeated
		}
		doc, job.Pages, err = ps.raster(ctx, a, job.Format, data, kind, sides, color, rasterCopies)
		if err != nil {
			return Job{}, err
		}
	}

	jobAttrs := []attr{}
	if copies > 1 {
		jobAttrs = append(jobAttrs, attr{tagInteger, "copies", []any{copies}})
	}
	if slices.Contains(a.strings("sides-supported"), sides) {
		jobAttrs = append(jobAttrs, attr{tagKeyword, "sides", []any{sides}})
	}
	if a.bool("color-supported") {
		mode := "monochrome"
		if color {
			mode = "color"
		}
		jobAttrs = append(jobAttrs, attr{tagKeyword, "print-color-mode", []any{mode}})
	}
	res, err := c.do(ctx, request{op: opPrintJob, operation: c.operation(
		attr{tagName, "job-name", []any{title}},
		attr{tagMimeType, "document-format", []any{job.Format}},
	), job: jobAttrs}, doc)
	if err != nil {
		return Job{}, err
	}
	job.ID, _ = res.int("job-id")
	job.State = jobState(res)
	return job, nil
}

func jobState(r response) string {
	n, _ := r.int("job-state")
	switch n {
	case 3:
		return "pending"
	case 4:
		return "held"
	case 5:
		return "printing"
	case 6:
		return "stopped"
	case 7:
		return "canceled"
	case 8:
		return "aborted"
	case 9:
		return "completed"
	}
	return ""
}

// raster draws the document as pages of the printer's raster format.
func (ps *Printers) raster(ctx context.Context, a response, format string, data []byte, kind, sides string, color bool, copies int) ([]byte, int, error) {
	j := rasterJob{quality: 4, duplex: sides != "one-sided", shortEdge: sides == "two-sided-short-edge"}
	ch := 1
	var sheetBack string
	if format == "image/urf" {
		urf := a.strings("urf-supported")
		j.dpi = urfResolution(urf)
		if color && slices.Contains(urf, "SRGB24") {
			ch = 3
		} else if !slices.Contains(urf, "W8") && slices.Contains(urf, "SRGB24") {
			ch = 3 // a printer of sRGB only
		}
		sheetBack = urfSheetBack(urf)
	} else {
		types := a.strings("pwg-raster-document-type-supported")
		switch {
		case color && slices.Contains(types, "srgb_8"):
			ch = 3
		case slices.Contains(types, "sgray_8"):
		case slices.Contains(types, "srgb_8"):
			ch = 3
		default:
			return nil, 0, fmt.Errorf("this printer's raster is %s: this server draws sgray_8 or srgb_8", strings.Join(types, ", "))
		}
		j.dpi = pwgResolution(a.resolutions("pwg-raster-document-resolution-supported"))
		sheetBack = a.string("pwg-raster-document-sheet-back")
	}
	if j.dpi == 0 {
		return nil, 0, errors.New("this printer does not say at which resolution it prints a raster")
	}
	j.sizeName = cmp(a.string("media-default"), "iso_a4_210x297mm")
	pw, ph := mediaSize(j.sizeName)
	j.pageSize = [2]int{pw, ph}
	w, h := pw*j.dpi/72, ph*j.dpi/72

	// Each page is drawn, its back flipped when the printer asks, and
	// compressed before the next: one page at a time is held whole.
	across, along := backTransform(sheetBack, j.shortEdge)
	var pages []encoded
	add := func(p *page) error {
		if len(pages) >= maxPages {
			return fmt.Errorf("%d pages at most", maxPages)
		}
		f := fit(p, w, h, ch)
		if j.duplex && len(pages)%2 == 1 && (across || along) {
			f.transform(across, along)
		}
		pages = append(pages, encode(f))
		return nil
	}
	if kind == "application/pdf" {
		if err := ps.renderer.render(ctx, data, j.dpi, ch, add); err != nil {
			return nil, 0, err
		}
	} else {
		p, err := decodeImage(data)
		if err != nil {
			return nil, 0, err
		}
		if err := add(p); err != nil {
			return nil, 0, err
		}
	}
	n := len(pages)
	if j.duplex && n%2 == 1 && copies > 1 {
		pages = append(pages, encode(newPage(w, h, ch))) // each copy starts on its own sheet
	}
	one := pages
	for i := 1; i < copies; i++ {
		pages = append(pages, one...)
	}
	var out bytes.Buffer
	var err error
	if format == "image/urf" {
		err = writeURF(&out, pages, j)
	} else {
		err = writePWG(&out, pages, j)
	}
	return out.Bytes(), n, err
}

// urfResolution is the resolution of an urf-supported's RS: 600 when it
// offers it, else the lowest above 300, else its first.
func urfResolution(urf []string) int {
	for _, k := range urf {
		rs, ok := strings.CutPrefix(k, "RS")
		if !ok {
			continue
		}
		var all []int
		for _, s := range strings.Split(rs, "-") {
			var n int
			if _, err := fmt.Sscan(s, &n); err == nil && n > 0 {
				all = append(all, n)
			}
		}
		return pickResolution(all)
	}
	return 0
}

func pwgResolution(rs []resolution) int {
	var all []int
	for _, r := range rs {
		if r.X == r.Y {
			all = append(all, r.X)
		}
	}
	return pickResolution(all)
}

func pickResolution(all []int) int {
	if len(all) == 0 {
		return 0
	}
	if slices.Contains(all, 600) {
		return 600
	}
	best := 0
	for _, n := range all {
		if n >= 300 && (best == 0 || n < best) {
			best = n
		}
	}
	if best == 0 {
		return slices.Max(all)
	}
	return best
}

// urfSheetBack reads urf-supported's DM: how the back of a sheet prints.
func urfSheetBack(urf []string) string {
	for _, k := range urf {
		switch k {
		case "DM2":
			return "flipped"
		case "DM3":
			return "rotated"
		case "DM4":
			return "manual-tumble"
		}
	}
	return "normal"
}
