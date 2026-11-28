package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"
)

// A small IPP/1.1 and 2.0 client (RFC 8010, RFC 8011): enough to ask a
// printer what it is and to send it a job. No dependency: the encoding is
// a few tags, and the printer's answers are read leniently (collections,
// which this server does not need, are skipped).

// Operations.
const (
	opPrintJob             = 0x0002
	opGetJobAttributes     = 0x0009
	opGetPrinterAttributes = 0x000B
)

// Delimiter tags.
const (
	tagOperation   = 0x01
	tagJob         = 0x02
	tagEnd         = 0x03
	tagPrinter     = 0x04
	tagUnsupported = 0x05
)

// Value tags.
const (
	tagInteger         = 0x21
	tagBoolean         = 0x22
	tagEnum            = 0x23
	tagResolution      = 0x32
	tagRange           = 0x33
	tagBegCollection   = 0x34
	tagTextLang        = 0x35
	tagNameLang        = 0x36
	tagEndCollection   = 0x37
	tagText            = 0x41
	tagName            = 0x42
	tagKeyword         = 0x44
	tagURI             = 0x45
	tagCharset         = 0x47
	tagLanguage        = 0x48
	tagMimeType        = 0x49
	tagMemberAttrName  = 0x4A
	maxIPPMessageBytes = 8 << 20 // a printer's answer, at most
)

// attr is one attribute to send: a tag and its values (strings for the
// string tags, int for integer and enum, bool for boolean).
type attr struct {
	tag    byte
	name   string
	values []any
}

// request is an IPP request: its operation attributes, then its job
// attributes, then the document.
type request struct {
	op        uint16
	operation []attr
	job       []attr
}

var requestID atomic.Uint32

// encode writes the request's message, without the document.
func (r request) encode() ([]byte, error) {
	var b bytes.Buffer
	b.Write([]byte{2, 0}) // IPP 2.0
	binary.Write(&b, binary.BigEndian, r.op)
	binary.Write(&b, binary.BigEndian, requestID.Add(1))
	for _, g := range []struct {
		tag   byte
		attrs []attr
	}{{tagOperation, r.operation}, {tagJob, r.job}} {
		if len(g.attrs) == 0 {
			continue
		}
		b.WriteByte(g.tag)
		for _, a := range g.attrs {
			for i, v := range a.values {
				name := a.name
				if i > 0 {
					name = "" // an additional value
				}
				if err := writeValue(&b, a.tag, name, v); err != nil {
					return nil, fmt.Errorf("%s: %w", a.name, err)
				}
			}
		}
	}
	b.WriteByte(tagEnd)
	return b.Bytes(), nil
}

func writeValue(b *bytes.Buffer, tag byte, name string, v any) error {
	var val []byte
	switch x := v.(type) {
	case string:
		val = []byte(x)
	case int:
		val = binary.BigEndian.AppendUint32(nil, uint32(int32(x)))
	case bool:
		val = []byte{0}
		if x {
			val[0] = 1
		}
	case resolution:
		val = binary.BigEndian.AppendUint32(nil, uint32(x.X))
		val = binary.BigEndian.AppendUint32(val, uint32(x.Y))
		val = append(val, 3) // dots per inch
	case [2]int:
		val = binary.BigEndian.AppendUint32(nil, uint32(int32(x[0])))
		val = binary.BigEndian.AppendUint32(val, uint32(int32(x[1])))
	default:
		return fmt.Errorf("value of type %T", v)
	}
	if len(name) > 0xFFFF || len(val) > 0xFFFF {
		return errors.New("too long")
	}
	b.WriteByte(tag)
	binary.Write(b, binary.BigEndian, uint16(len(name)))
	b.WriteString(name)
	binary.Write(b, binary.BigEndian, uint16(len(val)))
	b.Write(val)
	return nil
}

// resolution is a value of tag resolution, in dots per inch.
type resolution struct{ X, Y int }

// response is a printer's answer: its status and its attributes by name,
// the printer's and the job's alike (this client asks for one at a time).
type response struct {
	status uint16 // the operation, in a request
	attrs  map[string][]any
	size   int // the message's length: what follows is a document
}

func (r response) ok() bool { return r.status < 0x0100 }

func (r response) strings(name string) []string {
	var out []string
	for _, v := range r.attrs[name] {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func (r response) string(name string) string {
	if s := r.strings(name); len(s) > 0 {
		return s[0]
	}
	return ""
}

func (r response) ints(name string) []int {
	var out []int
	for _, v := range r.attrs[name] {
		if n, ok := v.(int); ok {
			out = append(out, n)
		}
	}
	return out
}

func (r response) int(name string) (int, bool) {
	if n := r.ints(name); len(n) > 0 {
		return n[0], true
	}
	return 0, false
}

func (r response) bool(name string) bool {
	for _, v := range r.attrs[name] {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	return false
}

func (r response) resolutions(name string) []resolution {
	var out []resolution
	for _, v := range r.attrs[name] {
		if x, ok := v.(resolution); ok {
			out = append(out, x)
		}
	}
	return out
}

// rangeOf is the upper bound of a rangeOfInteger, 0 when absent.
func (r response) rangeMax(name string) int {
	for _, v := range r.attrs[name] {
		if x, ok := v.([2]int); ok {
			return x[1]
		}
	}
	return 0
}

// decode reads an IPP response. Values inside collections are skipped.
func decode(data []byte) (response, error) {
	if len(data) < 8 {
		return response{}, errors.New("not an IPP answer: too short")
	}
	out := response{status: binary.BigEndian.Uint16(data[2:4]), attrs: map[string][]any{}}
	p := 8
	var last string
	depth := 0
	for p < len(data) {
		tag := data[p]
		p++
		if tag == tagEnd {
			out.size = p
			return out, nil
		}
		if tag < 0x10 { // the next group
			last = ""
			continue
		}
		if p+2 > len(data) {
			return out, errors.New("truncated IPP answer")
		}
		nl := int(binary.BigEndian.Uint16(data[p:]))
		p += 2
		if p+nl+2 > len(data) {
			return out, errors.New("truncated IPP answer")
		}
		name := string(data[p : p+nl])
		p += nl
		vl := int(binary.BigEndian.Uint16(data[p:]))
		p += 2
		if p+vl > len(data) {
			return out, errors.New("truncated IPP answer")
		}
		val := data[p : p+vl]
		p += vl
		switch tag {
		case tagBegCollection:
			if depth == 0 && name != "" {
				last = name
			}
			depth++
			continue
		case tagEndCollection:
			if depth > 0 {
				depth--
			}
			continue
		}
		if depth > 0 {
			continue // a member of a collection
		}
		if name != "" {
			last = name
		}
		if last == "" {
			continue
		}
		if v, ok := decodeValue(tag, val); ok {
			out.attrs[last] = append(out.attrs[last], v)
		}
	}
	return out, errors.New("IPP answer without its end")
}

func decodeValue(tag byte, val []byte) (any, bool) {
	switch {
	case tag == tagInteger || tag == tagEnum:
		if len(val) != 4 {
			return nil, false
		}
		return int(int32(binary.BigEndian.Uint32(val))), true
	case tag == tagBoolean:
		return len(val) == 1 && val[0] != 0, len(val) == 1
	case tag == tagResolution:
		if len(val) != 9 {
			return nil, false
		}
		x, y := int(int32(binary.BigEndian.Uint32(val))), int(int32(binary.BigEndian.Uint32(val[4:])))
		if val[8] == 4 { // dots per centimeter
			x, y = x*254/100, y*254/100
		}
		return resolution{x, y}, true
	case tag == tagRange:
		if len(val) != 8 {
			return nil, false
		}
		return [2]int{int(int32(binary.BigEndian.Uint32(val))), int(int32(binary.BigEndian.Uint32(val[4:])))}, true
	case tag == tagTextLang || tag == tagNameLang:
		// language length, language, text length, text
		if len(val) < 2 {
			return nil, false
		}
		ll := int(binary.BigEndian.Uint16(val))
		if len(val) < 2+ll+2 {
			return nil, false
		}
		tl := int(binary.BigEndian.Uint16(val[2+ll:]))
		if len(val) < 4+ll+tl {
			return nil, false
		}
		return string(val[4+ll : 4+ll+tl]), true
	case tag >= 0x40 && tag <= 0x5F:
		return string(val), true
	}
	return nil, false
}

// ippClient talks to one printer.
type ippClient struct {
	uri      string // ipp:// or ipps://, as the printer's attributes name it
	endpoint string // the same over http:// or https://
	http     *http.Client
}

// newIPPClient checks a printer's address: ipp:// (HTTP, port 631 by
// default) or ipps:// (TLS). Printers on a home network carry a
// certificate they made themselves: it is not checked.
func newIPPClient(raw string, timeout time.Duration) (*ippClient, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("%q is not a printer's address: ipp://<address>/ipp/print", raw)
	}
	switch u.Scheme {
	case "ipp", "ipps":
	default:
		return nil, fmt.Errorf("%q: the address starts with ipp:// or ipps://", raw)
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("%q: an address without user, query or fragment", raw)
	}
	if u.Path == "" {
		u.Path = "/ipp/print"
	}
	endpoint := *u
	endpoint.Scheme = "http"
	if u.Scheme == "ipps" {
		endpoint.Scheme = "https"
	}
	if u.Port() == "" {
		endpoint.Host = u.Host + ":631"
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.Proxy = nil
	tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} // a printer's own certificate
	c := &ippClient{uri: u.String(), endpoint: endpoint.String(), http: &http.Client{Transport: tr, Timeout: timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	return c, nil
}

// do sends a request and its document (nil: none).
func (c *ippClient) do(ctx context.Context, r request, doc []byte) (response, error) {
	head, err := r.encode()
	if err != nil {
		return response{}, err
	}
	body := io.MultiReader(bytes.NewReader(head), bytes.NewReader(doc))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, body)
	if err != nil {
		return response{}, err
	}
	req.ContentLength = int64(len(head) + len(doc))
	req.Header.Set("Content-Type", "application/ipp")
	res, err := c.http.Do(req)
	if err != nil {
		return response{}, fmt.Errorf("the printer does not answer: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return response{}, fmt.Errorf("the printer answered HTTP %d", res.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(bufio.NewReader(res.Body), maxIPPMessageBytes))
	if err != nil {
		return response{}, fmt.Errorf("reading the printer's answer: %w", err)
	}
	out, err := decode(data)
	if err != nil {
		return out, err
	}
	if !out.ok() {
		msg := out.string("status-message")
		if msg == "" {
			msg = statusName(out.status)
		}
		return out, fmt.Errorf("the printer refused: %s", msg)
	}
	return out, nil
}

// operation is the attributes every request starts with.
func (c *ippClient) operation(extra ...attr) []attr {
	return append([]attr{
		{tagCharset, "attributes-charset", []any{"utf-8"}},
		{tagLanguage, "attributes-natural-language", []any{"en"}},
		{tagURI, "printer-uri", []any{c.uri}},
		{tagName, "requesting-user-name", []any{"eriphe"}},
	}, extra...)
}

func statusName(s uint16) string {
	switch s {
	case 0x0400:
		return "bad request"
	case 0x0401:
		return "forbidden"
	case 0x0402, 0x0403:
		return "it asks for a sign-in, which this server does not do"
	case 0x0406:
		return "not found: check the address's path (often /ipp/print)"
	case 0x040A:
		return "document format not supported"
	case 0x040B:
		return "attributes or values not supported"
	case 0x0500:
		return "internal error"
	case 0x0503:
		return "version not supported"
	case 0x0507:
		return "the printer is busy"
	case 0x0508:
		return "the job was canceled"
	}
	return fmt.Sprintf("status 0x%04x", s)
}
