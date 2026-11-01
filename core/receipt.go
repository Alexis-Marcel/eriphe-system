package main

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// A task's receipt: what its agent says it did, in a fixed shape, and what
// eriphe-core checked of it. The agent ends each turn with a fenced JSON
// block (receiptInstruction): every harness (Claude Code, Codex, OpenCode,
// any ACP agent) hands back its final text, while a tool call would need an
// extra tool allowed in each one's permission model. eriphe-core reads the
// block, never trusts it: what is "verified" is what a check run by the node
// saw pass, a file said changed is compared with the repository's diff, and
// a claim that nothing confirms stays "not verified". Code says what is
// verified, the model says what it did, the card shows the difference.

// receiptStatuses are the statuses an agent may give.
var receiptStatuses = map[string]bool{"done": true, "partial": true, "blocked": true}

// receipt is the block as the agent wrote it.
type receipt struct {
	Status        string   `json:"status"`
	Summary       string   `json:"summary,omitempty"`
	Changed       []string `json:"changed,omitempty"`
	Verified      []string `json:"verified,omitempty"`
	NotVerified   []string `json:"not_verified,omitempty"`
	Decisions     []string `json:"decisions,omitempty"`
	Risks         []string `json:"risks,omitempty"`
	NeedsApproval []string `json:"needs_approval,omitempty"`
	Lessons       []string `json:"lessons,omitempty"`
}

// receiptInstruction is the end of a worker's standing instruction.
const receiptInstruction = "End every turn with your receipt, a fenced ```json block as the very last thing of your message:\n" +
	"```json\n" +
	`{"receipt": {"status": "done | partial | blocked", "summary": "one sentence", "changed": ["files, branches, PRs"], ` +
	`"verified": ["what you checked and how"], "not_verified": ["what you did not check"], "decisions": ["choices you made"], ` +
	`"risks": ["what could go wrong"], "needs_approval": ["what waits for the user"], "lessons": ["what to do differently next time"]}}` + "\n" +
	"```\n" +
	"status is done when the work is finished, partial when some is left, blocked when you need an answer (ask it above the block). " +
	"Leave out the empty lists. Eriphe compares the receipt with its own checks and the repository's diff: claim nothing you did not do."

// The limits of what is kept of a receipt.
const (
	receiptItems   = 20
	receiptItemLen = 300
)

var (
	receiptFenceRe = regexp.MustCompile("(?s)```[ \\t]*(?:json|JSON)?[ \\t]*\\r?\\n(.*?)\\r?\\n?```")
	receiptRe      = regexp.MustCompile(`(?s)<receipt>\s*(.*?)\s*</receipt>`)
)

// parseReceipt finds the agent's receipt in its text: the last fenced block
// (or <receipt> tag) that reads as one. ok is false when there is none.
func parseReceipt(text string) (r *receipt, ok bool) {
	var blocks []string
	for _, m := range receiptFenceRe.FindAllStringSubmatch(text, -1) {
		blocks = append(blocks, m[1])
	}
	for _, m := range receiptRe.FindAllStringSubmatch(text, -1) {
		blocks = append(blocks, m[1])
	}
	if t := strings.TrimSpace(text); strings.HasPrefix(t, "{") {
		blocks = append(blocks, t) // the whole message is the block
	}
	for i := len(blocks) - 1; i >= 0; i-- {
		if r := decodeReceipt(blocks[i]); r != nil {
			return r, true
		}
	}
	return nil, false
}

// decodeReceipt reads one block: {"receipt": {...}} or the object itself.
// Lists given as one string are taken as one item; a status not known
// makes it no receipt.
func decodeReceipt(raw string) *receipt {
	var outer map[string]json.RawMessage
	if json.Unmarshal([]byte(strings.TrimSpace(raw)), &outer) != nil {
		return nil
	}
	if inner, ok := outer["receipt"]; ok {
		outer = nil
		if json.Unmarshal(inner, &outer) != nil {
			return nil
		}
	}
	str := func(k string) string {
		var s string
		json.Unmarshal(outer[k], &s)
		return truncate(strings.TrimSpace(s), receiptItemLen*2)
	}
	list := func(k string) []string {
		var l []any
		if json.Unmarshal(outer[k], &l) != nil {
			var s string
			if json.Unmarshal(outer[k], &s) != nil || strings.TrimSpace(s) == "" {
				return nil
			}
			l = []any{s}
		}
		var out []string
		for _, v := range l {
			s := strings.TrimSpace(fmt.Sprint(v))
			if s == "" || v == nil {
				continue
			}
			if len(out) == receiptItems {
				break
			}
			out = append(out, truncate(s, receiptItemLen))
		}
		return out
	}
	r := &receipt{Status: strings.ToLower(str("status")), Summary: str("summary"), Changed: list("changed"),
		Verified: list("verified"), NotVerified: list("not_verified"), Decisions: list("decisions"), Risks: list("risks"),
		NeedsApproval: list("needs_approval"), Lessons: list("lessons")}
	if !receiptStatuses[r.Status] {
		return nil
	}
	return r
}

// stripReceipt removes the receipt from the agent's text, which is shown
// without it.
func stripReceipt(text string) string {
	out := text
	for _, m := range receiptFenceRe.FindAllStringSubmatchIndex(text, -1) {
		if decodeReceipt(text[m[2]:m[3]]) != nil {
			out = strings.Replace(out, text[m[0]:m[1]], "", 1)
		}
	}
	for _, m := range receiptRe.FindAllStringSubmatchIndex(text, -1) {
		if decodeReceipt(text[m[2]:m[3]]) != nil {
			out = strings.Replace(out, text[m[0]:m[1]], "", 1)
		}
	}
	if t := strings.TrimSpace(out); strings.HasPrefix(t, "{") && decodeReceipt(t) != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// checkResult is a check the node ran in the task's container when a turn
// ended (its "check" event).
type checkResult struct {
	Name     string `json:"name"`
	OK       bool   `json:"ok"`
	Run      string `json:"run,omitempty"`    // a command, checked by its exit code
	Exists   string `json:"exists,omitempty"` // a path that must be there
	ExitCode int    `json:"exit_code,omitempty"`
	Output   string `json:"output,omitempty"` // the end of what it printed
	Seq      int    `json:"seq"`
}

// checkedReceipt is a receipt as eriphe-core read it against what it saw.
type checkedReceipt struct {
	// Status is the agent's, or "missing" when it gave none.
	Status  string   `json:"status"`
	Summary string   `json:"summary,omitempty"`
	Changed []string `json:"changed,omitempty"`
	// Verified is what a check run by the node saw pass.
	Verified []string `json:"verified,omitempty"`
	// Unchecked is what the agent says it verified and no check covered.
	Unchecked []string `json:"unchecked,omitempty"`
	// NotVerified is what the agent did not verify, and the failed checks.
	NotVerified   []string      `json:"not_verified,omitempty"`
	Decisions     []string      `json:"decisions,omitempty"`
	Risks         []string      `json:"risks,omitempty"`
	NeedsApproval []string      `json:"needs_approval,omitempty"`
	Lessons       []string      `json:"lessons,omitempty"`
	Checks        []checkResult `json:"checks,omitempty"`
	// Diff is the files the repository's diff shows, when the node gave it.
	Diff []string `json:"diff,omitempty"`
	// Mismatches are where the receipt and what eriphe-core saw differ.
	Mismatches []mismatch `json:"mismatches,omitempty"`
	// Unsupported: it says done while a check failed, or names changes the
	// diff does not have.
	Unsupported bool `json:"unsupported,omitempty"`
	// Turn is the seq of the turn_end it is from.
	Turn int `json:"turn"`
}

// missing tells a receipt the agent did not give.
func (r *checkedReceipt) missing() bool { return r == nil || r.Status == "missing" }

// Given tells the templates that the agent gave a receipt.
func (r *checkedReceipt) Given() bool { return !r.missing() }

// Open counts what is not verified: said so, or only claimed.
func (r *checkedReceipt) Open() int { return len(r.NotVerified) + len(r.Unchecked) }

// The kinds of mismatch between a receipt and what eriphe-core saw.
const (
	mmVerifiedFailed = "verified_failed" // Item said verified, Check failed
	mmDoneFailed     = "done_failed"     // done, but Check failed
	mmNotInDiff      = "not_in_diff"     // Item said changed, not in the diff
	mmUnreported     = "unreported"      // N files of the diff not in the receipt
)

// mismatch is one place where the receipt and what eriphe-core saw differ.
type mismatch struct {
	Kind  string `json:"kind"`
	Item  string `json:"item,omitempty"`
	Check string `json:"check,omitempty"`
	N     int    `json:"n,omitempty"`
}

// Text words it in lang (web_text_tasks.go).
func (m mismatch) Text(lang string) string {
	f := tr(lang, "receipt.mm."+m.Kind)
	switch m.Kind {
	case mmVerifiedFailed:
		return fmt.Sprintf(f, m.Item, m.Check)
	case mmDoneFailed:
		return fmt.Sprintf(f, m.Check)
	case mmNotInDiff:
		return fmt.Sprintf(f, m.Item)
	case mmUnreported:
		return fmt.Sprintf(f, m.N)
	}
	return m.Kind
}

func mismatchTexts(ms []mismatch, lang string) []string {
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = m.Text(lang)
	}
	return out
}

// failedChecks are the checks whose last run failed.
func (r *checkedReceipt) failedChecks() []checkResult {
	var out []checkResult
	for _, ch := range r.Checks {
		if !ch.OK {
			out = append(out, ch)
		}
	}
	return out
}

// lastTurn returns the text of the task's last finished turn, and the seq
// of its turn_end; 0 when no turn has ended.
func lastTurn(events []EventRow) (string, int) {
	end := -1
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Type == "turn_end" {
			end = i
			break
		}
	}
	if end < 0 {
		return "", 0
	}
	start := 0
	for i := end - 1; i >= 0; i-- {
		if events[i].Type == "turn_end" {
			start = i + 1
			break
		}
	}
	var parts []string
	for _, e := range events[start:end] {
		if e.Type == "text" && !flagged(e.Data["error"]) {
			if s, _ := e.Data["text"].(string); s != "" {
				parts = append(parts, s)
			}
		}
	}
	if len(parts) == 0 {
		if s, _ := events[end].Data["result"].(string); s != "" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, "\n\n"), events[end].Seq
}

// lastChecks returns the last result of each check, in the order first run.
func lastChecks(events []EventRow) []checkResult {
	var order []string
	by := map[string]checkResult{}
	for _, e := range events {
		if e.Type != "check" {
			continue
		}
		raw, _ := json.Marshal(e.Data)
		var ch checkResult
		if json.Unmarshal(raw, &ch) != nil || ch.Name == "" {
			continue
		}
		ch.Seq = e.Seq
		if _, ok := by[ch.Name]; !ok {
			order = append(order, ch.Name)
		}
		by[ch.Name] = ch
	}
	out := make([]checkResult, 0, len(order))
	for _, n := range order {
		out = append(out, by[n])
	}
	return out
}

// lastDiff returns the files of the last "diff" event, and whether there is one.
func lastDiff(events []EventRow) ([]string, bool) {
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Type != "diff" {
			continue
		}
		var files []string
		if l, ok := events[i].Data["files"].([]any); ok {
			for _, f := range l {
				if s, _ := f.(string); s != "" {
					files = append(files, s)
				}
			}
		}
		return files, true
	}
	return nil, false
}

// crossCheck reads the task's last receipt against its checks and its
// diff. It is nil before the first turn ends.
func crossCheck(events []EventRow) *checkedReceipt {
	text, turn := lastTurn(events)
	if turn == 0 {
		return nil
	}
	cr := &checkedReceipt{Status: "missing", Turn: turn, Checks: lastChecks(events)}
	diff, hasDiff := lastDiff(events)
	cr.Diff = diff
	r, ok := parseReceipt(text)
	if ok {
		cr.Status, cr.Summary, cr.Changed = r.Status, r.Summary, r.Changed
		cr.Decisions, cr.Risks, cr.NeedsApproval, cr.Lessons = r.Decisions, r.Risks, r.NeedsApproval, r.Lessons
		cr.NotVerified = append(cr.NotVerified, r.NotVerified...)
	}
	// What the code saw: the checks.
	for _, ch := range cr.Checks {
		if ch.OK {
			cr.Verified = append(cr.Verified, ch.Name)
		} else {
			cr.NotVerified = append(cr.NotVerified, ch.Name)
		}
	}
	if !ok {
		return cr
	}
	// What the agent says it verified: confirmed by a check that passed,
	// contradicted by one that failed, or only claimed.
	for _, v := range r.Verified {
		ch, found := matchCheck(v, cr.Checks)
		switch {
		case found && ch.OK:
		case found:
			cr.Mismatches = append(cr.Mismatches, mismatch{Kind: mmVerifiedFailed, Item: v, Check: ch.Name})
			cr.Unsupported = true
		default:
			cr.Unchecked = append(cr.Unchecked, v)
		}
	}
	if r.Status == "done" {
		for _, ch := range cr.failedChecks() {
			cr.Mismatches = append(cr.Mismatches, mismatch{Kind: mmDoneFailed, Check: ch.Name})
			cr.Unsupported = true
		}
	}
	if hasDiff {
		for _, c := range r.Changed {
			if looksLikePath(c) && !inDiff(c, diff) {
				cr.Mismatches = append(cr.Mismatches, mismatch{Kind: mmNotInDiff, Item: c})
				cr.Unsupported = true
			}
		}
		if n := unreported(r.Changed, diff); n > 0 {
			cr.Mismatches = append(cr.Mismatches, mismatch{Kind: mmUnreported, N: n})
		}
		if r.Status == "done" && len(r.Changed) > 0 && len(diff) == 0 && anyPath(r.Changed) {
			cr.Unsupported = true
		}
	}
	return cr
}

// matchCheck finds the check an item of "verified" speaks of: its name or
// its command, in the item's words.
func matchCheck(item string, checks []checkResult) (checkResult, bool) {
	l := strings.ToLower(item)
	for _, ch := range checks {
		if strings.Contains(l, strings.ToLower(ch.Name)) || ch.Run != "" && strings.Contains(l, strings.ToLower(ch.Run)) {
			return ch, true
		}
	}
	return checkResult{}, false
}

// looksLikePath tells a file of the repository from a branch, a PR or a sentence.
func looksLikePath(s string) bool {
	s = strings.TrimSpace(strings.Trim(s, "`"))
	if s == "" || strings.ContainsAny(s, " \t#:") || strings.HasPrefix(s, "http") {
		return false
	}
	base := s[strings.LastIndex(s, "/")+1:]
	return strings.Contains(base, ".") && !strings.HasPrefix(base, ".") || strings.Contains(s, "/") && strings.Contains(base, ".")
}

func anyPath(items []string) bool {
	for _, s := range items {
		if looksLikePath(s) {
			return true
		}
	}
	return false
}

// inDiff tells whether a path is in the diff, or a folder of one of its files.
func inDiff(p string, diff []string) bool {
	p = strings.TrimPrefix(strings.TrimPrefix(strings.Trim(strings.TrimSpace(p), "`"), "./"), "/work/repo/")
	for _, f := range diff {
		if f == p || strings.HasSuffix(f, "/"+p) || strings.HasPrefix(f, strings.TrimSuffix(p, "/")+"/") {
			return true
		}
	}
	return false
}

// unreported counts the files of the diff that the receipt does not name.
func unreported(changed, diff []string) int {
	n := 0
	for _, f := range diff {
		named := false
		for _, c := range changed {
			if inDiff(c, []string{f}) || strings.Contains(c, f) {
				named = true
				break
			}
		}
		if !named {
			n++
		}
	}
	return n
}

// brainText is the receipt as the brain reads it, in French like its other
// events: shorter than the agent's message, and checked.
func (r *checkedReceipt) brainText() string {
	var b strings.Builder
	line := func(label string, items []string) {
		if len(items) > 0 {
			fmt.Fprintf(&b, "\n- %s : %s", label, strings.Join(items, " ; "))
		}
	}
	fmt.Fprintf(&b, "Reçu (recoupé par eriphe-core) : statut %s", r.Status)
	if r.Summary != "" {
		fmt.Fprintf(&b, "\n- résumé : %s", r.Summary)
	}
	line("changé", r.Changed)
	line("vérifié par eriphe-core", r.Verified)
	line("affirmé sans contrôle", r.Unchecked)
	line("non vérifié", r.NotVerified)
	for _, ch := range r.failedChecks() {
		fmt.Fprintf(&b, "\n- contrôle %s échoué (code %d) : %s", ch.Name, ch.ExitCode, truncate(oneLine(ch.Output), 300))
	}
	line("décisions", r.Decisions)
	line("risques", r.Risks)
	line("attend l'accord", r.NeedsApproval)
	line("leçons proposées", r.Lessons)
	line("écarts", mismatchTexts(r.Mismatches, "fr"))
	return b.String()
}

// memoryText is the receipt as the night's extraction reads it
// (memory_extract.go): the task's own words, external, with what
// eriphe-core verified marked as such.
func (r *checkedReceipt) memoryText() string {
	var b strings.Builder
	fmt.Fprintf(&b, "status %s", r.Status)
	if r.Summary != "" {
		fmt.Fprintf(&b, "; summary: %s", r.Summary)
	}
	for _, f := range []struct {
		label string
		items []string
	}{{"changed", r.Changed}, {"verified by eriphe-core's checks", r.Verified}, {"claimed, not checked", r.Unchecked},
		{"not verified", r.NotVerified}, {"decisions", r.Decisions}, {"waits for approval", r.NeedsApproval},
		{"risks", r.Risks}, {"lessons it proposes", r.Lessons}, {"mismatches eriphe-core found", mismatchTexts(r.Mismatches, "en")}} {
		if len(f.items) > 0 {
			fmt.Fprintf(&b, "; %s: %s", f.label, strings.Join(f.items, " | "))
		}
	}
	return b.String()
}
