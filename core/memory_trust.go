package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"unicode"
)

// Who may write what in the memory is decided here, in code: a prompt
// injection read in a mail or a page must not become something Eriphe
// believes. The rules:
//
//   - external content and tasks only propose (status proposed), never a
//     profile or a lesson; I approve them in the workspace;
//   - a brain turn that called an external catalog tool is tainted: what it
//     remembers is only proposed, unless my own message of that turn asked
//     for it; nor may it allow a permission or lend a task tools that act
//     (taintedAllow, taintedResources);
//   - only my own words create or change lessons, preferences and the
//     profile (source_ref names my message, checked against its role);
//   - every write goes through the scanner: instructions to the model,
//     invisible characters, secrets and tokenised links are refused, softer
//     imperative wording is flagged and only proposed.

// scanResult is what the scanner found.
type scanResult struct {
	Reject string // why the text is refused
	Flags  []string
}

var (
	injectionRes = []*regexp.Regexp{
		regexp.MustCompile(`(?i)\b(ignore|disregard|forget|override)\b.{0,30}\b(previous|prior|above|earlier|all|any|your|the)\b.{0,20}\b(instructions?|prompts?|rules|directives|guidelines|messages?)`),
		regexp.MustCompile(`(?i)\b(ignore|oublie|oubliez|ignorez|ne tiens pas compte)\b.{0,30}\b(les|tes|toutes|vos|des)\b.{0,25}\b(instructions?|consignes?|règles|regles|messages? précédents?)`),
		regexp.MustCompile(`(?i)\b(system|developer)\s*(prompt|message|instructions?)\b`),
		regexp.MustCompile(`(?i)\b(prompt|message|consignes?)\s+(système|systeme)\b`),
		regexp.MustCompile(`(?i)\byou are now\b|\btu es (maintenant|désormais|desormais)\b|\bnew instructions\b|\bnouvelles? (consignes?|instructions?)\b`),
		regexp.MustCompile(`(?i)<\s*/?\s*(system|assistant|user|instructions?|tool_call|function_calls?)\s*>|\[/?INST\]|<\|im_start\|>|<\|endoftext\|>`),
		regexp.MustCompile(`(?i)\b(jailbreak|DAN mode|developer mode)\b`),
	}
	// Softer: wording that tells the assistant what to do. Fine in my own
	// lessons, suspicious anywhere else.
	imperativeRes = []*regexp.Regexp{
		regexp.MustCompile(`(?i)\b(always|never|whenever|from now on|you must|you should)\b`),
		regexp.MustCompile(`(?i)\b(toujours|jamais|dorénavant|dorenavant|désormais|desormais|à partir de maintenant|tu dois|il faut que tu|chaque fois que)\b`),
		regexp.MustCompile(`(?i)\b(call|run|execute|send|forward|transfer|exécute|execute|envoie|transfère|transfere|appelle)\b.{0,40}\b(tool|outil|run_task|answer_task|mail|e-mail|email|token|jeton|password|mot de passe)\b`),
	}
	secretRes = []*regexp.Regexp{
		regexp.MustCompile(`\b(sk|rk|pk)-(ant-|proj-|live-|test-)?[A-Za-z0-9_\-]{16,}`),
		regexp.MustCompile(`\b(ghp|gho|ghu|ghs|ghr)_[A-Za-z0-9]{20,}|\bgithub_pat_[A-Za-z0-9_]{20,}`),
		regexp.MustCompile(`\bxox[abposr]-[A-Za-z0-9\-]{10,}`),
		regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`),
		regexp.MustCompile(`\bAIza[0-9A-Za-z_\-]{30,}`),
		regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`),
		regexp.MustCompile(`\beyJ[A-Za-z0-9_\-]{8,}\.eyJ[A-Za-z0-9_\-]{8,}\.[A-Za-z0-9_\-]{8,}`),
		regexp.MustCompile(`(?i)\b(password|passwd|mot de passe|mdp|api[_ -]?key|secret|token|jeton)\s*[:=]\s*\S{6,}`),
	}
	longTokenRe = regexp.MustCompile(`[A-Za-z0-9+/_\-]{40,}`)
	urlRe2      = regexp.MustCompile(`(?i)\b[a-z][a-z0-9+.\-]*://[^\s<>"'\x60]+`)
	secretKeys  = regexp.MustCompile(`(?i)^(token|access_token|refresh_token|id_token|auth|key|api_key|apikey|secret|sig|signature|password|pass|pwd|code|session|sessionid|jwt|x-amz-signature|x-amz-credential)$`)
)

// invisible tells the characters that hide text from me: zero-width ones,
// bidirectional controls, tags, and other format characters.
func invisible(r rune) bool {
	switch {
	case r >= 0x200B && r <= 0x200F, r >= 0x202A && r <= 0x202E, r >= 0x2060 && r <= 0x2064,
		r >= 0x2066 && r <= 0x2069, r == 0xFEFF, r == 0x00AD, r == 0x180E, r >= 0xE0000 && r <= 0xE007F:
		return true
	case r == '\n' || r == '\t' || r == '\r':
		return false
	}
	return unicode.Is(unicode.Cf, r) || unicode.Is(unicode.Co, r) || (unicode.IsControl(r))
}

// scanMemoryText checks what is about to be written.
func scanMemoryText(parts ...string) scanResult {
	var res scanResult
	text := strings.Join(parts, "\n")
	for _, r := range text {
		if invisible(r) {
			res.Reject = fmt.Sprintf("invisible or control character U+%04X", r)
			return res
		}
	}
	for _, re := range injectionRes {
		if re.MatchString(text) {
			res.Reject = "it reads like instructions to an assistant, not like something to remember"
			return res
		}
	}
	for _, re := range secretRes {
		if re.MatchString(text) {
			res.Reject = "it looks like a credential: secrets belong in the vault, never in the memory"
			return res
		}
	}
	for _, m := range longTokenRe.FindAllString(text, -1) {
		if looksRandom(m) {
			res.Reject = "it holds a long random string, which looks like a credential"
			return res
		}
	}
	for _, raw := range urlRe2.FindAllString(text, -1) {
		u, err := url.Parse(strings.TrimRight(raw, ".,;:!?)"))
		if err != nil {
			res.Flags = append(res.Flags, "url")
			continue
		}
		if u.User != nil {
			res.Reject = "a link carries a user name or password"
			return res
		}
		for k := range u.Query() {
			if secretKeys.MatchString(k) {
				res.Reject = "a link carries a token (" + k + "=…): keep the link without it"
				return res
			}
		}
		if u.Scheme != "http" && u.Scheme != "https" && u.Scheme != "obsidian" {
			res.Flags = append(res.Flags, "url-scheme")
		}
	}
	for _, re := range imperativeRes {
		if re.MatchString(text) {
			res.Flags = append(res.Flags, "imperative")
			break
		}
	}
	return res
}

// looksRandom tells a credential-like string from a word or a path: digits,
// lower and upper case letters all present.
func looksRandom(s string) bool {
	var lower, upper, digit bool
	for _, r := range s {
		switch {
		case unicode.IsLower(r):
			lower = true
		case unicode.IsUpper(r):
			upper = true
		case unicode.IsDigit(r):
			digit = true
		}
	}
	return lower && upper && digit
}

// Taint: what the brain read in its current turn. A turn starts when
// eriphe-core delivers it a message of mine (tendBrain); a call the brain
// makes to an external catalog server, which eriphe-core proxies, taints it.
// A delivery of events only does not start a clean turn: what the brain
// read is still in its context, so the turn goes on and its taint with it,
// until I am back in the loop.

type brainTurn struct {
	mu       sync.Mutex
	from, to int // the seqs of the messages delivered in this turn
	eventSeq int // the brain task's last event when the turn started
	tainted  []string
	keep     func(brainTurnState) // stores it, so a restart of eriphe-core does not forget it
}

// brainTurnState is the turn as the store keeps it (kv brain_turn).
type brainTurnState struct {
	From     int      `json:"from"`
	To       int      `json:"to"`
	EventSeq int      `json:"event_seq"`
	Tainted  []string `json:"tainted,omitempty"`
}

// span is the seqs of the messages delivered in this turn; 0, 0 if none.
func (t *brainTurn) span() (int, int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.from, t.to
}

// taintUnknown taints a brain's turn that eriphe-core cannot tell about,
// started before it was kept: until my next message, it is as if it read
// something.
const taintUnknown = "eriphe-core.restarted"

// load restores the kept turn. Without one while a brain runs, the turn
// is tainted: what it read before the restart is not known.
func (t *brainTurn) load(raw string, brainRuns bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	var st brainTurnState
	if raw == "" || json.Unmarshal([]byte(raw), &st) != nil {
		if brainRuns {
			t.tainted = []string{taintUnknown}
		}
		return
	}
	t.from, t.to, t.eventSeq, t.tainted = st.From, st.To, st.EventSeq, st.Tainted
}

// save is called with t.mu held.
func (t *brainTurn) save() {
	if t.keep != nil {
		t.keep(brainTurnState{t.from, t.to, t.eventSeq, append([]string(nil), t.tainted...)})
	}
}

func (t *brainTurn) start(from, to, eventSeq int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.from, t.to, t.eventSeq, t.tainted = from, to, eventSeq, nil
	t.save()
}

// goOn extends the turn over a delivery of events only (or a resumed
// session): it keeps its taint, carried adds what the events showed so far
// (turnTaint, read before eventSeq moves on), and my messages of the turn
// stay its own for the memory.
func (t *brainTurn) goOn(from, to, eventSeq int, carried []string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.from == 0 {
		t.from = from
	}
	t.to, t.eventSeq = max(t.to, to), eventSeq
	for _, why := range carried {
		if !containsStr(t.tainted, why) {
			t.tainted = append(t.tainted, why)
		}
	}
	t.save()
}

func (t *brainTurn) taint(why string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !containsStr(t.tainted, why) {
		t.tainted = append(t.tainted, why)
		t.save()
	}
}

func (t *brainTurn) snapshot() (from, to, eventSeq int, tainted []string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.from, t.to, t.eventSeq, append([]string(nil), t.tainted...)
}

// brainCalledCatalog is the proxy's hook: the brain called a catalog tool.
func (c *core) brainCalledCatalog(taskID, server, tool string) {
	if taskID == "" || taskID != c.brainTaskID() {
		return
	}
	if containsStr(c.conf().memoryConfig().TrustedServers, server) {
		return
	}
	c.turn.taint(server + "." + tool)
}

// turnTaint is why the brain's current turn is tainted, if it is: the
// proxy's record, and the turn's own events for the MCP servers the brain
// reaches without eriphe-core (the deprecated brain.mcp_servers) and the
// web tools.
func (c *core) turnTaint() []string {
	return c.turnTaintBut(nil)
}

// turnTaintBut is turnTaint without the brain's page reads still waiting
// for their answer (pending: how many per URL): a read that has not run has
// read nothing yet, and reads asked together were chosen before any of them.
func (c *core) turnTaintBut(pending map[string]int) []string {
	pending = maps.Clone(pending)
	_, _, eventSeq, tainted := c.turn.snapshot()
	id := c.brainTaskID()
	if id == "" {
		return tainted
	}
	// Every event of the turn, page by page: a long turn has more than one.
	var events []EventRow
	for after := eventSeq; ; {
		page, err := c.store.eventsAfter(id, after, 500)
		if err != nil {
			return append(tainted, "events.unreadable") // not known: as if it read something
		}
		events = append(events, page...)
		if len(page) < 500 {
			break
		}
		after = page[len(page)-1].Seq
	}
	trusted := c.conf().memoryConfig().TrustedServers
	for _, e := range events {
		if e.Type != "tool_use" {
			continue
		}
		name, _ := e.Data["name"].(string)
		if name == "" {
			name, _ = e.Data["tool_name"].(string)
		}
		switch {
		case strings.HasPrefix(name, "mcp__eriphe-core__"):
		case strings.HasPrefix(name, "mcp__"):
			server, _, _ := strings.Cut(strings.TrimPrefix(name, "mcp__"), "__")
			if !containsStr(trusted, server) && !containsStr(tainted, name) {
				tainted = append(tainted, name)
			}
		case name == "WebFetch" || name == "WebSearch":
			if u := webURL(e.Data["input"]); name == "WebFetch" && pending[u] > 0 {
				pending[u]--
				continue
			}
			if !containsStr(tainted, name) {
				tainted = append(tainted, name)
			}
		}
	}
	return tainted
}

// Reading back is reading too. A memory of external trust (a task's
// journal, a proposal from a mail that I approved) holds what was read
// outside, summarised: the turn that reads it through recall or the
// commitments tool is tainted like one that read the mail itself. The
// context pack and the wake packet, which nobody asked for, name such a
// memory without quoting it. And what the brain says in a tainted turn
// may repeat what it read: its message is external for the extraction,
// so that it is not learnt back as Eriphe's own conclusion.

const taintExternalMemory = "memory.external"

// readMemories taints the brain's turn when it reads an external memory.
func (c *core) readMemories(xs []*Memory) {
	for _, x := range xs {
		if x.Trust == trustExternal {
			c.turn.taint(taintExternalMemory)
			return
		}
	}
}

// markBrainMessage records that a message of the brain was written in a
// tainted turn (taintKey): the extraction reads it as external content.
func (c *core) markBrainMessage(seq int) {
	if tainted := c.turnTaint(); len(tainted) > 0 {
		c.store.set(taintKey(seq), "brain:"+strings.Join(tainted, ","))
	}
}

// externalLine names an external memory in a packet without quoting it.
func externalLine(x *Memory) string {
	src := x.SourceRef
	if src == "" {
		src = x.SourceKind
	}
	return fmt.Sprintf("#%d (%s tiré d'un contenu extérieur, %s : recall le relit, ce qui souille le tour)", x.ID, x.Kind, src)
}

// The taint also limits what the brain does, not only what it remembers: a
// tainted turn may have been talked into anything by what it read. It may
// not allow a permission (the request becomes mine to answer) nor lend a
// task anything but believed reads. My own message of the same turn does
// not lift this.

// taintedAllow refuses the brain's allow of a permission during a tainted
// turn, and hands the request to me instead.
func (c *core) taintedAllow(taskID, pid string) error {
	tainted := c.turnTaint()
	if len(tainted) == 0 {
		return nil
	}
	why := c.conf().assistantName() + " a lu un contenu extérieur dans ce tour (" + strings.Join(tainted, ", ") + ")"
	c.store.markUserOnly(taskID, pid, why)
	return fmt.Errorf("not allowed: this turn read external content (%s), so allowing is %s's decision now: "+
		"the request is forwarded to %s, relay it in one line and do not retry", strings.Join(tainted, ", "), c.conf().User, c.conf().User)
}

// ownerMessage reads a message I wrote, from a source_ref "msg:N" (or "#N").
func (c *core) ownerMessage(ref string) (*Message, bool) {
	seq, ok := parseMsgRef(ref)
	if !ok {
		return nil, false
	}
	msgs, err := c.store.messagesAfter(seq-1, 1)
	if err != nil || len(msgs) == 0 || msgs[0].Seq != seq || msgs[0].Role != roleUser {
		return nil, false
	}
	return &msgs[0], true
}

func parseMsgRef(ref string) (int, bool) {
	ref = strings.TrimSpace(ref)
	s, ok := strings.CutPrefix(ref, "msg:")
	if !ok {
		s, ok = strings.CutPrefix(ref, "#")
	}
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(s)
	return n, err == nil && n > 0
}

// rememberRequest: wording of mine that asks Eriphe to keep something.
var rememberRequest = regexp.MustCompile(`(?i)\b(retiens|retenir|souviens|souvenir|rappelle[- ]toi|mémorise|memorise|mémoriser|note (que|bien|ça|ca)|garde en (tête|tete|mémoire|memoire)|n'oublie pas|oublie pas|remember|keep in mind|note that|don't forget|do not forget|mets? (le |la |ça |ca )?(dans|en) (ta )?mémoire|enregistre|ajoute à ta mémoire|à l'avenir|a l'avenir|la prochaine fois|désormais|dorénavant|corrige|mets à jour|oublie)\b`)

// writeDecision is how a brain write is recorded, or why it is refused.
type writeDecision struct {
	Status string
	Trust  string
	Source string // source_kind
	Ref    string
	Why    string // why it is only proposed
}

var errOwnerOnly = errors.New("only " + "the user's own words can create or change a lesson, a preference or the profile: cite the user's message with source_ref (msg:N, the #N of the message)")

// decideBrainWrite applies the rules to a brain's remember or update. kind
// is the memory's kind after the write.
func (c *core) decideBrainWrite(kind, ref string, scan scanResult) (writeDecision, error) {
	d := writeDecision{Status: memActive, Trust: trustFirstParty, Source: srcBrain, Ref: strings.TrimSpace(ref)}
	if scan.Reject != "" {
		return d, fmt.Errorf("refused: %s", scan.Reject)
	}
	from, to, _, _ := c.turn.snapshot()
	msg, owner := c.ownerMessage(d.Ref)
	if owner {
		d.Trust = trustOwner
		d.Ref = fmt.Sprintf("msg:%d", msg.Seq)
	}
	if ownerKind(kind) && !owner {
		return d, errOwnerOnly
	}
	// My message of this very turn asked for it.
	asked := owner && msg.Seq >= from && msg.Seq <= to && from > 0 && rememberRequest.MatchString(msg.Text)
	if tainted := c.turnTaint(); len(tainted) > 0 && !asked {
		d.Status, d.Trust = memProposed, trustExternal
		d.Why = "this turn read external content (" + strings.Join(tainted, ", ") + ")"
		if ownerKind(kind) && kind != kindPreference {
			return d, fmt.Errorf("refused: this turn read external content (%s), and a %s cannot be proposed from it", strings.Join(tainted, ", "), kind)
		}
		return d, nil
	}
	if containsStr(scan.Flags, "imperative") && !owner {
		d.Status = memProposed
		d.Why = "it reads like an instruction, and does not come from the user's own message"
	}
	return d, nil
}

// extractionDecision applies the rules to an extraction's operation: what
// it read decides its trust, and only an episode or something stated in my
// own words can be active at once; what the extraction deduced (basis
// inferred or ambiguous) is proposed.
func extractionDecision(kind string, refTrust string, scan scanResult, basis string) (writeDecision, error) {
	d := writeDecision{Status: memProposed, Trust: refTrust, Source: srcExtraction}
	if scan.Reject != "" {
		return d, fmt.Errorf("refused: %s", scan.Reject)
	}
	if kind == kindProfile || kind == kindLesson && refTrust != trustExternal {
		return d, fmt.Errorf("an extraction cannot write a %s", kind)
	}
	if kind == kindLesson {
		// A task's lesson (its receipt): only ever proposed, for the user to endorse.
		d.Why = "a lesson drawn from a task waits for the user's approval"
		return d, nil
	}
	switch {
	case containsStr(scan.Flags, "imperative") && refTrust != trustOwner:
		d.Why = "it reads like an instruction"
	case kind == kindPreference:
		d.Why = "a preference waits for the user's approval"
	case kind == kindEpisode:
		d.Status = memActive // the journal: its trust tells what it is made of
	case basis == basisInferred || basis == basisAmbiguous:
		d.Why = "it is " + basis + ", not said in so many words"
	case refTrust == trustOwner:
		d.Status, d.Trust = memActive, trustFirstParty
	default:
		d.Why = "taken from a task or an external content"
	}
	return d, nil
}
