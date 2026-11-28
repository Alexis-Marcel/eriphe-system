package main

import (
	"context"
	"log"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const instructions = "Printers of the local network, reached over IPP. printer_status says how a printer is (paper, ink or toner, stopped or not); print prints one of a node's files, a PDF or an image."

// titles are the tools' titles, as people read them.
var titles = map[string]string{
	"printer_status": "Printer status",
	"print":          "Print",
	"test_account":   "Test a printer",
}

// says is how eriphe-core says a call on a card (core/README.md, Cards).
var says = map[string]map[string]any{
	"print": {
		"text": map[string]string{"fr": "Imprimer[ « {title} »][ en {copies} exemplaires]", "en": "Print[ “{title}”][, {copies} copies]"},
		"done": map[string][]string{"fr": {"Imprimé", "Imprimés"}, "en": {"Printed", "Printed"}},
	},
}

func metaOf(name string) mcp.Meta {
	m := mcp.Meta{}
	if s, ok := says[name]; ok {
		m["eriphe/says"] = s
	}
	if name == "print" {
		// document is one of a node's files: eriphe-core reads it and
		// sends it as a data: URI (servers/README.md, A node's file).
		m["eriphe/files"] = []string{"document"}
	}
	if len(m) == 0 {
		return nil
	}
	return m
}

func ptr[T any](v T) *T { return &v }

type statusIn struct {
	Account string `json:"account,omitempty" jsonschema:"the printer, by its name; may be left out with one printer"`
}

type printIn struct {
	Account  string `json:"account,omitempty" jsonschema:"the printer, by its name; may be left out with one printer"`
	Document string `json:"document" jsonschema:"the file to print: one of a node's files, media:<node id>/<name> (a PDF, a JPEG or a PNG)"`
	Title    string `json:"title,omitempty" jsonschema:"what the job is called on the printer and on the card: the document's title"`
	Copies   int    `json:"copies,omitempty" jsonschema:"how many copies, 1 to 50; default 1"`
	Sides    string `json:"sides,omitempty" jsonschema:"one-sided (default), two-sided-long-edge (a book) or two-sided-short-edge (a calendar)"`
	Color    *bool  `json:"color,omitempty" jsonschema:"false prints in black and white on a color printer; left out: in color when the printer can"`
}

func newServer(ps *Printers, version string) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "eriphe-printer", Version: version}, &mcp.ServerOptions{Instructions: instructions})
	tool(s, "printer_status", "How a printer is: idle, printing or stopped, why (no paper, toner low…), its ink or toner levels, whether it prints in color and on both sides.",
		&mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: ptr(false)},
		func(ctx context.Context, in statusIn) (Status, error) { return ps.Status(ctx, in.Account) })
	tool(s, "print", "Print a document on a printer: one of a node's files, a PDF or an image. The printer must be on; it says when it lacks paper.",
		// Paper comes out of the house's printer: what it does leaves
		// Eriphe, as a message does.
		&mcp.ToolAnnotations{DestructiveHint: ptr(false), OpenWorldHint: ptr(true)},
		func(ctx context.Context, in printIn) (Job, error) {
			return ps.Print(ctx, PrintIn{Account: in.Account, Document: in.Document, Title: in.Title,
				Copies: in.Copies, Sides: strings.TrimSpace(in.Sides), Color: in.Color})
		})
	addTestAccount(s, ps)
	return s
}

// tool registers a typed tool and logs each call: name, duration, error.
// Arguments are not logged: a document is in them.
func tool[In, Out any](s *mcp.Server, name, desc string, ann *mcp.ToolAnnotations, fn func(context.Context, In) (Out, error)) {
	mcp.AddTool(s, &mcp.Tool{Name: name, Title: titles[name], Description: desc, Annotations: ann, Meta: metaOf(name)},
		func(ctx context.Context, _ *mcp.CallToolRequest, in In) (*mcp.CallToolResult, Out, error) {
			start := time.Now()
			out, err := fn(ctx, in)
			if err != nil {
				log.Printf("tool=%s ms=%d error=%q", name, time.Since(start).Milliseconds(), err.Error())
			} else {
				log.Printf("tool=%s ms=%d", name, time.Since(start).Milliseconds())
			}
			return nil, out, err
		})
}

// Setup tools (servers/README.md): marked eriphe/setup, kept for
// eriphe-core's workspace.

var setupMeta = mcp.Meta{"eriphe/setup": true}

type testAccountIn struct {
	Account string `json:"account,omitempty" jsonschema:"the printer to try, by its name; may be left out with one printer"`
}

// testAccountOut says whether the printer answers; Detail is what it is,
// in its own words (its model), for the workspace to show.
type testAccountOut struct {
	OK     bool   `json:"ok"`
	Error  string `json:"error,omitempty"`
	Detail string `json:"detail,omitempty"`
}

func addTestAccount(s *mcp.Server, ps *Printers) {
	mcp.AddTool(s, &mcp.Tool{Name: "test_account", Title: titles["test_account"], Meta: setupMeta,
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: ptr(false)},
		Description: "Setup only, for the workspace: ask a printer what it is, and say whether it answers."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in testAccountIn) (*mcp.CallToolResult, testAccountOut, error) {
			start := time.Now()
			st, err := ps.Status(ctx, in.Account)
			log.Printf("tool=test_account ms=%d ok=%t", time.Since(start).Milliseconds(), err == nil)
			if err != nil {
				return nil, testAccountOut{Error: err.Error()}, nil
			}
			return nil, testAccountOut{OK: true, Detail: st.Model}, nil
		})
}
