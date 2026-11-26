package nodefile

import (
	"regexp"
	"strings"
	"unicode"

	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

// Links between nodes are written in their text, as in Obsidian: [[Title]]
// names another node by its title, [[Title|shown]] shows other words for
// it. Never inside code: a fenced block or a code span is literal, and
// \[[ is only brackets. The workspace's Markdown reader
// (web/espace/src/a2ui/markdown.tsx) reads them the same way; both are
// held to testdata/wikilinks.json.

// WikiLink is a [[name]] or [[name|shown]] of a node's text.
type WikiLink struct {
	Name  string `json:"name"`            // the title it names, as written
	Shown string `json:"shown,omitempty"` // the words shown instead, if any
}

// WikiLinks are the links of the node's text, in order, each name once
// (by LinkKey).
func (n *Node) WikiLinks() []WikiLink {
	var out []WikiLink
	seen := map[string]bool{}
	for _, l := range WikiLinks(n.body) {
		if k := LinkKey(l.Name); !seen[k] {
			seen[k] = true
			out = append(out, l)
		}
	}
	return out
}

// LinkKey is what a link's name and a title are matched by: lowercase,
// without diacritics, a typographic apostrophe as a straight one, its
// spaces one each.
func LinkKey(s string) string {
	t := transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)
	folded, _, err := transform.String(t, strings.ToLower(s))
	if err != nil {
		folded = strings.ToLower(s)
	}
	return strings.Join(strings.Fields(apostrophes.Replace(folded)), " ")
}

var apostrophes = strings.NewReplacer("\u2019", "'", "\u2018", "'", "\u02bc", "'")

// WikiLinks are the links of a piece of markdown text, in order, every
// one: its paragraphs, headings, list items, quotes and table cells read
// as the Markdown reader reads them, its fenced code left out.
func WikiLinks(text string) []WikiLink {
	var out []WikiLink
	for _, chunk := range inlineChunks(text) {
		out = append(out, chunkLinks(chunk)...)
	}
	return out
}

var (
	fenceRe   = regexp.MustCompile("^(`{3,})[^`]*$|^(~{3,}).*$")
	headingRe = regexp.MustCompile(`^#{1,6}\s+(.*)$`)
	hashesRe  = regexp.MustCompile(`^#{1,6}\s`)
	ruleRe    = regexp.MustCompile(`^(-{3,}|\*{3,}|_{3,})$`)
	itemRe    = regexp.MustCompile(`^(?:[-*+]|\d+[.)])\s+(.*)$`)
	quoteRe   = regexp.MustCompile(`^>\s?(.*)$`)
	alignRe   = regexp.MustCompile(`^:?-+:?$`)
	delimRe   = regexp.MustCompile(`^[\s|:-]+$`)
)

func startsBlock(line string) bool {
	return fenceRe.MatchString(line) || hashesRe.MatchString(line) || strings.HasPrefix(line, ">") || ruleRe.MatchString(line)
}

// inlineChunks cuts text into the pieces the Markdown reader reads inline
// one by one (markdown.tsx, blocks): a paragraph's lines together, a
// heading, a list item, a quote's lines together, a table's cells.
func inlineChunks(text string) []string {
	var out []string
	var para []string
	quote := -1 // the last chunk, when it is a quote: the next quote line joins it
	flush := func() {
		if len(para) > 0 {
			out = append(out, strings.Join(para, "\n"))
			quote = -1
		}
		para = nil
	}
	lines := strings.Split(strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n"), "\n")
	fence := ""
	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if fence != "" {
			if n := runLen(line, 0, fence[0]); n >= len(fence) && strings.TrimSpace(line[n:]) == "" {
				fence = ""
			}
			continue
		}
		if m := fenceRe.FindStringSubmatch(line); m != nil {
			flush()
			quote = -1
			fence = m[1] + m[2]
			continue
		}
		if line == "" {
			flush()
			continue
		}
		if strings.Contains(line, "|") && i+1 < len(lines) {
			if align := delimiterRow(strings.TrimSpace(lines[i+1])); align > 0 && len(TableCells(line)) == align {
				flush()
				quote = -1
				out = append(out, fit(TableCells(line), align)...)
				for i += 2; i < len(lines) && strings.TrimSpace(lines[i]) != "" && !startsBlock(strings.TrimSpace(lines[i])); i++ {
					out = append(out, fit(TableCells(lines[i]), align)...)
				}
				i--
				continue
			}
		}
		if m := headingRe.FindStringSubmatch(line); m != nil {
			flush()
			quote = -1
			out = append(out, m[1])
			continue
		}
		if ruleRe.MatchString(line) {
			flush()
			continue
		}
		if m := itemRe.FindStringSubmatch(line); m != nil {
			flush()
			quote = -1
			out = append(out, m[1])
			continue
		}
		if m := quoteRe.FindStringSubmatch(line); m != nil {
			flush()
			if quote >= 0 {
				out[quote] += "\n" + m[1]
			} else {
				out = append(out, m[1])
				quote = len(out) - 1
			}
			continue
		}
		para = append(para, line)
	}
	flush()
	return out
}

func fit(cells []string, width int) []string {
	if len(cells) > width {
		return cells[:width]
	}
	return cells
}

// delimiterRow is the number of columns of a table's second row; 0 when
// the line is not one.
func delimiterRow(line string) int {
	if !strings.Contains(line, "-") || !delimRe.MatchString(line) {
		return 0
	}
	cs := TableCells(line)
	if len(cs) == 0 {
		return 0
	}
	for _, c := range cs {
		if !alignRe.MatchString(c) {
			return 0
		}
	}
	if len(cs) == 1 && !strings.Contains(line, "|") {
		return 0
	}
	return len(cs)
}

// TableCells splits a table row on its pipes, as GFM does (a pipe splits
// even inside a code span, an escaped one, \|, stays a |), but for a pipe
// inside a [[name|shown]] link, which splits nothing.
func TableCells(line string) []string {
	s := strings.TrimSpace(line)
	s = strings.TrimPrefix(s, "|")
	var out []string
	var cell strings.Builder
	ended := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		ended = false
		switch {
		case c == '\\' && i+1 < len(s) && s[i+1] == '|':
			cell.WriteByte('|')
			i++
		case c == '\\' && i+1 < len(s):
			cell.WriteByte(c)
			cell.WriteByte(s[i+1])
			i++
		case c == '[' && i+1 < len(s) && s[i+1] == '[' && wikiEnd(s, i) > 0:
			e := wikiEnd(s, i)
			cell.WriteString(s[i:e])
			i = e - 1
		case c == '|':
			out = append(out, strings.TrimSpace(cell.String()))
			cell.Reset()
			ended = true
		default:
			cell.WriteByte(c)
		}
	}
	if !ended || strings.TrimSpace(cell.String()) != "" {
		out = append(out, strings.TrimSpace(cell.String()))
	}
	return out
}

// wikiEnd is where the [[...]] that opens at i ends (just past its ]]),
// or -1: it closes on the same line, holds no bracket, and names
// something.
func wikiEnd(s string, i int) int {
	for j := i + 2; j+1 < len(s); j++ {
		switch s[j] {
		case '\n', '[':
			return -1
		case ']':
			if s[j+1] != ']' {
				return -1
			}
			if wikiParse(s[i+2:j]) == nil {
				return -1
			}
			return j + 2
		}
	}
	return -1
}

// wikiParse reads a link's inside, name|shown; nil when it names nothing.
func wikiParse(inner string) *WikiLink {
	inner = strings.ReplaceAll(inner, `\|`, "|")
	name, shown, _ := strings.Cut(inner, "|")
	name = strings.TrimSpace(name)
	if name == "" {
		return nil
	}
	return &WikiLink{Name: name, Shown: strings.TrimSpace(shown)}
}

func runLen(s string, i int, ch byte) int {
	j := i
	for j < len(s) && s[j] == ch {
		j++
	}
	return j - i
}

// codeEnd is where the code span opened by the n backticks at i ends, or
// -1 when no run of exactly n closes it.
func codeEnd(s string, i, n int) int {
	for j := i + n; j < len(s); {
		if s[j] != '`' {
			j++
			continue
		}
		m := runLen(s, j, '`')
		if m == n {
			return j + m
		}
		j += m
	}
	return -1
}

// chunkLinks are the links of a piece read inline: escapes and code
// spans are skipped as the Markdown reader skips them.
func chunkLinks(s string) []WikiLink {
	var out []WikiLink
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == '\\' && i+1 < len(s) && isEscapable(s[i+1]):
			i += 2
		case c == '`':
			n := runLen(s, i, '`')
			if e := codeEnd(s, i, n); e >= 0 {
				i = e
			} else {
				i += n
			}
		case c == '[' && i+1 < len(s) && s[i+1] == '[':
			if e := wikiEnd(s, i); e > 0 {
				out = append(out, *wikiParse(s[i+2 : e-2]))
				i = e
			} else {
				i++
			}
		default:
			i++
		}
	}
	return out
}

// isEscapable is an ASCII punctuation character, which a backslash
// escapes.
func isEscapable(c byte) bool {
	return c >= '!' && c <= '/' || c >= ':' && c <= '@' || c >= '[' && c <= '`' || c >= '{' && c <= '~'
}
