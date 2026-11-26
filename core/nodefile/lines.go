package nodefile

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// Line operations: a page is written by several authors (a skill, code
// on an event, the user's gestures), each changing a line, never the
// whole page. An operation names its line by what it holds, not only by
// its number, so it needs no version: if the line is still there it
// applies, otherwise it is refused (ErrLineGone) and its author reads the
// page again.

// ErrLineGone: the line an operation names is not in the page (any
// more): read it again.
var ErrLineGone = errors.New("the line is not in the page any more: read it again")

// ErrLineTwice: the line an operation names is there more than once:
// name more of it.
var ErrLineTwice = errors.New("the line is there more than once: give more of it")

// LineRef names a line of a body: by its text, and by its number (from
// 1) when known. Text is the whole line, or a piece of it found in that
// line only; a callout's > and the indent may be left out.
type LineRef struct {
	Line int
	Text string
}

// ReplaceOnce replaces old, a piece of body found once, by new.
func ReplaceOnce(body, old, new string) (string, error) {
	switch strings.Count(body, old) {
	case 0:
		return "", ErrLineGone
	case 1:
		return strings.Replace(body, old, new, 1), nil
	}
	return "", ErrLineTwice
}

// lineQuoteRe cuts a line into its callout's > (with the space after
// each) and the rest.
var lineQuoteRe = regexp.MustCompile(`^((?:[ \t]*>[ ]?)*)(.*)$`)

// lineCore is a line as a LineRef's text is matched: its > and indent off,
// trimmed.
func lineCore(t string) string {
	return strings.TrimSpace(lineQuoteRe.FindStringSubmatch(t)[2])
}

// splitBody cuts a body into its lines, and gives back how to join them.
func splitBody(body string) ([]string, func([]string) string) {
	eol := "\n"
	if strings.Contains(body, "\r\n") {
		eol = "\r\n"
	}
	end := strings.HasSuffix(body, "\n")
	lines := strings.Split(strings.TrimSuffix(strings.TrimSuffix(body, "\n"), "\r"), "\n")
	for i := range lines {
		lines[i] = strings.TrimSuffix(lines[i], "\r")
	}
	return lines, func(ls []string) string {
		s := strings.Join(ls, eol)
		if end {
			s += eol
		}
		return s
	}
}

// locate finds the line r names (its index) among the lines of body
// that are role's (a task, an item, a heading), outside code and the
// references' targets: the line at r.Line when it still holds r's text,
// else the only line that holds it whole, else the only line that holds
// it in part.
func locate(body string, lines []string, r LineRef, role func(string) bool) (int, error) {
	want := lineCore(r.Text)
	if want == "" {
		return 0, errors.New("a line is named by its text")
	}
	info := ReadPage(body).info
	cand := func(i int) bool {
		return i < len(info) && !info[i].code && !info[i].def && role(lines[i])
	}
	if r.Line >= 1 && r.Line <= len(lines) && cand(r.Line-1) && lineCore(lines[r.Line-1]) == want {
		return r.Line - 1, nil
	}
	for _, part := range []bool{false, true} {
		at := -1
		for i, l := range lines {
			if !cand(i) {
				continue
			}
			c := lineCore(l)
			if c == want || (part && strings.Contains(c, want)) {
				if at >= 0 {
					return 0, ErrLineTwice
				}
				at = i
			}
		}
		if at >= 0 {
			return at, nil
		}
	}
	return 0, ErrLineGone
}

// FindLine is the number (from 1) of the line r names in body, any line
// outside code and the references' targets: the line at r.Line when it
// still holds r's text, else the only one that holds it.
func FindLine(body string, r LineRef) (int, error) {
	lines, _ := splitBody(body)
	i, err := locate(body, lines, r, func(string) bool { return true })
	if err != nil {
		return 0, err
	}
	return i + 1, nil
}

// taskBoxRe is a task's line: its prefix up to the box, its state, the
// rest.
var taskBoxRe = regexp.MustCompile(`^((?:[ \t]*>[ ]?)*[ \t]*(?:[-*+]|\d+[.)])[ \t]+)\[([ xX/-])\](.*)$`)

// The roles of the lines an operation names.
var (
	isTask  = taskBoxRe.MatchString
	isItem  = itemLineRe.MatchString
	isPlace = func(l string) bool { return isItem(l) || pageHeadRe.MatchString(strings.TrimSpace(l)) }
)

// SetTask writes a task's state (TaskOpen, TaskDone, TaskDoing,
// TaskDropped) into its line, every other byte kept.
func SetTask(body string, task LineRef, state string) (string, error) {
	if state != TaskOpen && state != TaskDone && state != TaskDoing && state != TaskDropped {
		return "", fmt.Errorf("a task's state is %q, %q, %q or %q", TaskOpen, TaskDone, TaskDoing, TaskDropped)
	}
	lines, join := splitBody(body)
	i, err := locate(body, lines, task, isTask)
	if err != nil {
		return "", fmt.Errorf("no task (- [ ] …) holds it: %w", err)
	}
	m := taskBoxRe.FindStringSubmatch(lines[i])
	lines[i] = m[1] + "[" + state + "]" + m[3]
	return join(lines), nil
}

// itemLineRe is an item's line: its > prefix, its indent, the rest.
var itemLineRe = regexp.MustCompile(`^((?:[ \t]*>[ ]?)*)([ \t]*)((?:[-*+]|\d+[.)])[ \t]+.*)$`)

// quoteDepth is how many > a line's prefix holds.
func quoteDepth(prefix string) int { return strings.Count(prefix, ">") }

// itemSpan is the item at line i and its detail: its lines [i, end).
// prefix and ind are its > prefix and its indent.
func itemSpan(lines []string, i int) (end int, prefix, ind string, ok bool) {
	m := itemLineRe.FindStringSubmatch(lines[i])
	if m == nil {
		return 0, "", "", false
	}
	prefix, ind = m[1], m[2]
	depth, base := quoteDepth(prefix), indent(ind)
	inner := func(t string) (string, bool) {
		q := lineQuoteRe.FindStringSubmatch(t)
		if quoteDepth(q[1]) != depth {
			return "", false
		}
		return q[2], true
	}
	end = i + 1
	for j := i + 1; j < len(lines); j++ {
		rest, same := inner(lines[j])
		if !same {
			break
		}
		if blank(rest) {
			continue // kept only if an indented line follows
		}
		if indent(rest) <= base {
			break
		}
		end = j + 1
	}
	return end, prefix, ind, true
}

// MoveItem moves an item with its detail after another line: after a
// heading (and the do: line right under it), it becomes the section's
// first item; after another item, it goes after that item's detail, at
// its indent and in its callout.
func MoveItem(body string, item, after LineRef) (string, error) {
	lines, join := splitBody(body)
	i, err := locate(body, lines, item, isItem)
	if err != nil {
		return "", fmt.Errorf("move: no item of a list holds it: %w", err)
	}
	end, _, ind, _ := itemSpan(lines, i)
	t, err := locate(body, lines, after, isPlace)
	if err != nil {
		return "", fmt.Errorf("after: no heading nor item holds it: %w", err)
	}
	if t >= i && t < end {
		return "", errors.New("an item is not moved into itself")
	}
	moved := make([]string, end-i)
	copy(moved, lines[i:end])

	// Where it goes, and at what prefix and indent.
	var at int
	toPrefix, toInd := "", ""
	if tEnd, tp, ti, isItem := itemSpan(lines, t); isItem {
		at, toPrefix, toInd = tEnd, tp, ti
	} else if pageHeadRe.MatchString(strings.TrimSpace(lines[t])) {
		at = t + 1
		for k := at; k < len(lines); k++ {
			if blank(lines[k]) {
				continue
			}
			if toks := inlineTokens(lines[k]); at == t+1 && onlyLinks(lines[k], toks) && toks[0].kind == LinkDo {
				at = k + 1 // under the heading's do: line
				continue
			}
			if m := itemLineRe.FindStringSubmatch(lines[k]); m != nil && m[1] == "" && m[2] == "" {
				at = k // the section's list: first in it
			}
			break
		}
	} else {
		return "", fmt.Errorf("after: line %d is neither a heading nor an item", t+1)
	}
	base := indent(ind)
	for k, l := range moved {
		q := lineQuoteRe.FindStringSubmatch(l)
		rest := dedent(q[2], base)
		if blank(rest) {
			moved[k] = strings.TrimRight(toPrefix, " ")
			continue
		}
		moved[k] = toPrefix + toInd + rest
	}

	// Out of its place, then in its new one.
	out := make([]string, 0, len(lines))
	out = append(out, lines[:i]...)
	out = append(out, lines[end:]...)
	if at > i {
		at -= end - i
	}
	// Of two blank lines it leaves together, or one it leaves last, one
	// goes.
	if i > 0 && blank(out[i-1]) && (i == len(out) || blank(out[i])) {
		out = append(out[:i-1], out[i:]...)
		if at >= i {
			at--
		}
	}
	// After a heading, a text that is not a list keeps a blank line from it.
	if at < len(out) && !blank(out[at]) && !itemLineRe.MatchString(out[at]) && !strings.HasPrefix(strings.TrimSpace(out[at]), ">") {
		moved = append(moved, "")
	}
	out = append(out[:at], append(moved, out[at:]...)...)
	return join(out), nil
}
