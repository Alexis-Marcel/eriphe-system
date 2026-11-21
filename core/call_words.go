package main

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"unicode/utf8"
)

// A kept call read plainly (the brain's access card, calls and runcalls
// cards): what its yes does to which items, in code's words, never the
// model's; its exact call (tool and every parameter, raw) under the
// card's one Détails, and the yes runs that, as it is shown. Nothing here
// knows a server: mail, calendar or any other.
//
//   - The sentence says what happens to what (callPhrase): the action, by
//     a verb of ours for the usual tool names (act.verb.*: trash, delete,
//     archive, move…; a move to archive or inbox is said as such), else
//     the tool's own title (annotations' title, or its own), else its
//     name; and what it acts on, how many and of what kind (callNoun: a
//     mail, an event, a file… by the server or the source, else
//     « élément »). The account it names is said apart, quietly
//     (accountWords: « compte Gmail »).
//   - The items are those of a node's sources its input names by their
//     id (a string, or a list of them), with their title and detail (a
//     mail's subject and sender) as the source lists them, kept with the
//     call when it is kept (the item may leave the list after); at most
//     callLinesMax named, then « et N autres »; an id no source lists is
//     counted, never guessed. Never the words a model wrote of them (an
//     item's note). A title read from a server the user does not trust
//     keeps its quiet mark (⚠): someone else's words.
//   - A call kept after reading external content stays « extérieur »:
//     the badge says it.

// itemLabel is an item a call acts on, as its node's source lists it.
type itemLabel struct {
	ID     string `json:"id"`
	Title  string `json:"title,omitempty"`
	Detail string `json:"detail,omitempty"`
	Source string `json:"source,omitempty"` // the node's source that lists it
	// External: read from a server the user does not trust, its words
	// someone else's: shown marked
	External bool `json:"external,omitempty"`
}

// callLabelsMax is the most items one call keeps by name; callLinesMax
// the most a card names, the others counted.
const (
	callLabelsMax = 20
	callLinesMax  = 5
)

// callItemLabels are the items a call's input names by their id, among
// those the node's sources of the call's own server list.
func (c *core) callItemLabels(node, tool string, input map[string]any) []itemLabel {
	server, _, ok := splitToolName(tool)
	if node == "" || c.nodes == nil || !ok {
		return nil
	}
	values := map[string]bool{}
	inputValues(input, values)
	var out []itemLabel
	seen := map[string]bool{}
	for _, r := range c.nodeReads(node) {
		if r.Source.Items == nil || r.Source.Monitor != "" || c.sourceServer(r.Source) != server {
			continue
		}
		ext := !c.trustsServer(server)
		for _, it := range sourceItems(r.Name, r.Source, r.Value, 0) {
			id, _ := it["id"].(string)
			if !values[id] || seen[id] {
				continue
			}
			seen[id] = true
			l := itemLabel{ID: id, Source: r.Name, External: ext}
			if t, _ := it["title"].(string); t != id {
				l.Title = t
			}
			l.Detail, _ = it["detail"].(string)
			out = append(out, l)
			if len(out) == callLabelsMax {
				return out
			}
		}
	}
	return out
}

// callRows is the node whose sources list the items a call names, and
// those items: prefer's first (the node the turn is about), else any
// node's, not archived, whose sources last read one of them; "" when
// none lists any.
func (c *core) callRows(prefer, tool string, input map[string]any) (string, []itemLabel) {
	if c.nodes == nil {
		return "", nil
	}
	if ls := c.callItemLabels(prefer, tool, input); len(ls) > 0 {
		return prefer, ls
	}
	values := map[string]bool{}
	inputValues(input, values)
	var nodes []string
	for v := range values {
		if utf8.RuneCountInString(v) < 2 {
			continue // too short to tell an id
		}
		raw, _ := json.Marshal(v)
		rows, err := c.store.db.Query(`SELECT DISTINCT node FROM node_reads WHERE instr(value, ?) > 0`, string(raw))
		if err != nil {
			continue
		}
		for rows.Next() {
			var n string
			if rows.Scan(&n) == nil && n != prefer && !slices.Contains(nodes, n) {
				nodes = append(nodes, n)
			}
		}
		rows.Close()
	}
	slices.Sort(nodes)
	for _, n := range nodes {
		if sum, err := c.nodes.Get(n); err != nil || !sum.Archived.IsZero() {
			continue
		}
		if ls := c.callItemLabels(n, tool, input); len(ls) > 0 {
			return n, ls
		}
	}
	return "", nil
}

// labelsOf are the items a kept call acts on: those kept with it, else
// those its node lists now.
func (c *core) labelsOf(a accessQuestion) []itemLabel {
	if len(a.Labels) > 0 || a.Node == "" {
		return a.Labels
	}
	name, _, _ := strings.Cut(a.Call, " ")
	var input map[string]any
	json.Unmarshal([]byte(a.Input), &input)
	return c.callItemLabels(a.Node, name, input)
}

// labelLine says an item in a line: its title quoted, and its detail;
// marked when its words are someone else's.
func labelLine(l itemLabel, lang string) string {
	title := truncate(oneLine(l.Title), 80)
	detail := truncate(oneLine(l.Detail), 60)
	s := ""
	switch {
	case title != "" && detail != "":
		s = fmt.Sprintf(tr(lang, "card.call.item"), title, detail)
	case title != "":
		s = fmt.Sprintf(tr(lang, "card.call.itemt"), title)
	default:
		return ""
	}
	if l.External {
		s += tr(lang, "card.call.ext")
	}
	return s
}

// labelLines are the items a call acts on, each in a line, at most
// callLinesMax; then « et N autres » for the rest of n, how many it
// acts on.
func labelLines(ls []itemLabel, n int, lang string) []string {
	var out []string
	for _, l := range ls {
		if len(out) == callLinesMax {
			break
		}
		if s := labelLine(l, lang); s != "" {
			out = append(out, s)
		}
	}
	if len(out) > 0 && n > len(out) {
		out = append(out, fmt.Sprintf(tr(lang, "card.call.more"), n-len(out)))
	}
	return out
}

// callItems is how a call's input names its items: the parameter that
// holds them (a list of ids, or one id) and how many; labels tell which
// values are items. "" and 0 when it names none.
func callItems(input map[string]any, labels []itemLabel) (key string, n int) {
	ids := map[string]bool{}
	for _, l := range labels {
		ids[l.ID] = true
	}
	for _, k := range slices.Sorted(maps.Keys(input)) {
		switch v := input[k].(type) {
		case []any:
			if len(v) > 0 && slices.ContainsFunc(v, func(x any) bool { return ids[valueText(x)] }) {
				return k, len(v)
			}
		case string:
			if ids[v] {
				key, n = k, 1 // a list naming them wins
			}
		}
	}
	if key != "" {
		return key, n
	}
	// No item known: a list of ids by its usual name.
	for _, k := range []string{"ids", "message_ids", "item_ids", "event_ids", "file_ids"} {
		if v, ok := input[k].([]any); ok && len(v) > 0 {
			return k, len(v)
		}
	}
	return "", 0
}

// nounOf is the kind of item a call acts on, a key of the card.noun.*
// words: by its server's name, else its items' source's name, when either
// is a usual word; "" when neither says.
func nounOf(server string, labels []itemLabel) string {
	names := []string{strings.ToLower(server)}
	for _, l := range labels {
		names = append(names, strings.ToLower(l.Source))
	}
	for _, n := range names {
		if k, ok := nounWords[n]; ok {
			return k
		}
	}
	return ""
}

// nounWords are the usual names of a server or a source, by the kind of
// item they list.
var nounWords = map[string]string{
	"mail": "mail", "mails": "mail", "email": "mail", "emails": "mail", "inbox": "mail",
	"calendar": "event", "agenda": "event", "events": "event", "calendars": "event",
	"files": "file", "file": "file", "documents": "file", "folders": "file",
	"messages": "message", "chat": "message",
	"issues": "issue", "pulls": "pull", "pull_requests": "pull",
	"contacts": "contact", "tasks": "task", "todos": "task", "notes": "note", "photos": "photo",
}

// countNoun says n items of a kind: « 2 mails », « 1 événement », else
// « 3 éléments ».
func countNoun(noun string, n int, lang string) string {
	key := "card.noun." + cmpOr(noun, "item")
	if n == 1 {
		return tr(lang, key+".one")
	}
	return fmt.Sprintf(tr(lang, key+".many"), n)
}

// destVerbs are the verbs of a move whose destination is a usual place,
// by the place: archive, the inbox, the trash.
var destVerbs = map[string]string{"archive": "archive", "archives": "archive", "inbox": "inbox", "trash": "trash", "bin": "trash", "corbeille": "trash"}

// destArgs are the usual names of a move's destination.
var destArgs = []string{"to", "folder", "destination", "mailbox", "target"}

// callPhrase says what a call does to what, in a sentence: the action
// (a verb of ours for its name, else its title, else its name), the items
// (how many, of what kind), and its other short parameters. title is the
// tool's own (its annotations' or its own), "" for none; noun the kind of
// its items (nounOf); count, when not 0, how many it says in the place
// of the input's (calls that do the same to other items, together).
func callPhrase(tool, title string, input map[string]any, labels []itemLabel, noun, lang string, count int) string {
	short := tool
	if _, t, ok := splitToolName(tool); ok {
		short = t
	} else if _, t, ok := strings.Cut(tool, "/"); ok {
		short = t
	}
	key, n := callItems(input, labels)
	if count > 0 && n > 0 {
		n = count
	}
	rest := maps.Clone(input)
	delete(rest, key)
	delete(rest, "account")
	// One item it is bound to (an event made from a mail, item_note's
	// call), or the only one it names: said by itself.
	one := ""
	if len(labels) == 1 && n <= 1 {
		one = labelLine(labels[0], lang)
	}
	if n == 0 {
		// Nothing it acts on by id: the action alone (a mail to send).
		s := tr(lang, "access.call."+short)
		switch t := buttonText(title, 80); {
		case s != "access.call."+short:
			s = upperFirst(s)
		case t != "":
			s = t
		default:
			s = upperFirst(fmt.Sprintf(tr(lang, "access.call.other"), strings.ReplaceAll(short, "_", " ")))
		}
		if one != "" {
			s += " · " + one
		}
		return s
	}
	verb := tr(lang, "act.verb."+short)
	known := verb != "act.verb."+short
	for _, d := range destArgs {
		to, ok := rest[d].(string)
		if !ok {
			continue
		}
		if v, ok := destVerbs[strings.ToLower(strings.TrimSpace(to))]; ok && (short == "move" || short == "label" || !known) {
			verb, known = tr(lang, "act.verb."+v), true
			delete(rest, d)
		}
		break
	}
	what := countNoun(noun, n, lang)
	var s string
	switch t := buttonText(title, 60); {
	case known:
		s = fmt.Sprintf(verb, what)
	case t != "" && one != "":
		s = t + " · " + one
	case t != "":
		s = t + " · " + what
	default:
		s = fmt.Sprintf(tr(lang, "act.verb.other"), buttonText(short, 40), what)
	}
	// Its other parameters, short ones: a folder, a flag. A long text is
	// shown apart (callShown); all are under Détails.
	for k, v := range rest {
		if str, ok := v.(string); ok && (utf8.RuneCountInString(str) > 40 || tr(lang, "card.param."+strings.ToLower(k)) != "card.param."+strings.ToLower(k)) {
			delete(rest, k) // a text to read, not a setting
		}
		if _, ok := v.([]any); ok {
			delete(rest, k)
		}
		if _, ok := v.(map[string]any); ok {
			delete(rest, k)
		}
	}
	if a := argsText(rest, lang); a != "" && len(rest) <= 2 {
		s += " → " + a
	}
	return s
}

// toolTitle is a catalog tool's own title: its annotations' or its own;
// "" when it has none or is not there.
func (c *core) toolTitle(tool string) string {
	if _, t := c.actionTool(callTool(tool)); t != nil {
		return cmpOr(t.Title, annotationTitle(t))
	}
	return ""
}

// callText is a kept call's sentence (callPhrase), in lang.
func (c *core) callText(a accessQuestion, lang string) string { return c.callTextN(a, 0, lang) }

// callTextN is callText for count items (0: as many as it acts on).
func (c *core) callTextN(a accessQuestion, count int, lang string) string {
	name, _, _ := strings.Cut(a.Call, " ")
	var in map[string]any
	json.Unmarshal([]byte(a.Input), &in)
	labels := c.labelsOf(a)
	server, _, _ := splitToolName(name)
	return callPhrase(name, c.toolTitle(name), in, labels, nounOf(server, labels), lang, count)
}

// callCount is how many items a kept call acts on (callItems), 0 for none.
func (c *core) callCount(a accessQuestion) int {
	var in map[string]any
	json.Unmarshal([]byte(a.Input), &in)
	_, n := callItems(in, c.labelsOf(a))
	return n
}

// catalogAsk tells whether an access card keeps a call of a catalog
// tool (not a page read nor a search, which say their own words).
func catalogAsk(a accessQuestion) bool {
	return a.Call != "" && len(a.Refs) == 0 && !strings.HasPrefix(a.Call, webSearchTool+" ") && !strings.HasPrefix(a.Call, webFetchTool+" ")
}

// callAccountWords is where a kept call acts, as the user knows it: the
// account it names (accountWords), a folder by its name, else its tool
// by its title; "" for none.
func (c *core) callAccountWords(a accessQuestion, lang string) string {
	account := a.Need.Account
	if account == "" {
		// A server that keeps no accounts of ours: the one its call names.
		var in map[string]any
		json.Unmarshal([]byte(a.Input), &in)
		account, _ = in["account"].(string)
		account = strings.TrimSpace(account)
	}
	switch {
	case a.Need.Server == "":
		return ""
	case a.Need.Server == foldersServer:
		return a.Need.TargetIn(lang)
	case account != "":
		return c.accountWords(a.Need.Server, account, lang)
	}
	return truncate(oneLine(cmpOr(c.conf().Servers[a.Need.Server].Title, a.Need.Server)), 60)
}

// accountWords say an account of a server quietly: « compte Gmail » by
// its provider when that names it alone among the server's accounts,
// else by its name (an address).
func (c *core) accountWords(server, account, lang string) string {
	label := account
	if svc, cfg, ok := accountsOf(c.conf().Servers[server]); ok && svc.Account != nil {
		byName := func(a svcAccount) string {
			if ch := svc.Account.choiceOf(a); ch.ID != "" && !ch.Custom {
				if w := tr(lang, svc.Account.Words+"."+ch.ID); w != svc.Account.Words+"."+ch.ID {
					return w
				}
			}
			return ""
		}
		if i := cfg.index(account); i >= 0 {
			if p := byName(cfg.Accounts[i]); p != "" {
				same := 0
				for _, a := range cfg.Accounts {
					if byName(a) == p {
						same++
					}
				}
				if same == 1 {
					label = p
				}
			}
		}
	}
	return fmt.Sprintf(tr(lang, "card.call.account"), truncate(oneLine(label), 60))
}

// upperFirst is s with its first letter a capital.
func upperFirst(s string) string {
	for i, r := range s {
		return strings.ToUpper(string(r)) + s[i+len(string(r)):]
	}
	return s
}

// callTool is a call's tool as its card's Détails name it: server/tool.
func callTool(call string) string {
	name, _, _ := strings.Cut(call, " ")
	if server, tool, ok := splitToolName(name); ok {
		return server + "/" + tool
	}
	return name
}

// callDetail is a kept call's exact call, under the card's one Détails:
// its tool, and every parameter of its input, raw, but those shown
// (callShown, by name).
func callDetail(a accessQuestion, shown []string) cardField {
	var input map[string]any
	json.Unmarshal([]byte(a.Input), &input)
	var lines []string
	for _, f := range callFields(input) {
		if !slices.Contains(shown, f.Name) {
			lines = append(lines, f.Name+" : "+f.Value)
		}
	}
	return cardField{Name: callTool(a.Call), Value: truncate(strings.Join(lines, "\n"), cardValueMax), Detail: true}
}

// callRead are what a card read to say a call (a pull request's title,
// a file's diff: card_context.go), kept with it: shown on the card, not
// under Détails. Its parameters are not among them.
func callRead(a accessQuestion) []cardField {
	var input map[string]any
	json.Unmarshal([]byte(a.Input), &input)
	var out []cardField
	for _, f := range a.fields() {
		if _, isParam := input[f.Name]; !isParam {
			out = append(out, f)
		}
	}
	return out
}

// callLongMin is from how many characters a text argument is content to
// read (a reply, a message: or one of several lines), shown on its card
// rather than under its Détails; callShownMax is the most it shows so,
// whole: a longer one stays under Détails.
const (
	callLongMin  = 90
	callShownMax = 4 * cardValueMax
)

// callShown are a kept call's long text arguments, lifted out of its
// Détails as fields to read (the very reply its Oui sends), as kept,
// whole; each named by its tool's input schema (the parameter's title),
// else in the user's words for a common name (body, text, message…),
// else by its own. names are their parameters, left out of the Détails.
// Whoever wrote them, the page shows them as text.
func (c *core) callShown(a accessQuestion, lang string) (fs []cardField, names []string) {
	var input map[string]any
	if json.Unmarshal([]byte(a.Input), &input) != nil {
		return nil, nil
	}
	tool, _, _ := strings.Cut(a.Call, " ")
	props, _ := c.inputSchema(tool)["properties"].(map[string]any)
	for _, k := range slices.Sorted(maps.Keys(input)) {
		s, ok := input[k].(string)
		n := utf8.RuneCountInString(s)
		if !ok || n > callShownMax || n <= callLongMin && !strings.Contains(strings.TrimSpace(s), "\n") {
			continue
		}
		p, _ := props[k].(map[string]any)
		name, _ := p["title"].(string)
		if name = truncate(oneLine(name), 60); name == "" {
			name = k
			if w := tr(lang, "card.param."+strings.ToLower(k)); w != "card.param."+strings.ToLower(k) {
				name = w
			}
		}
		fs = append(fs, cardField{Name: name, Value: s})
		names = append(names, k)
	}
	return fs, names
}

// callFieldsOf are a kept call's fields as its card shows them: what was
// read to say it, its long texts to read, then its exact call under the
// one Détails.
func (c *core) callFieldsOf(a accessQuestion, lang string) []cardField {
	shown, names := c.callShown(a, lang)
	return append(append(callRead(a), shown...), callDetail(a, names))
}

// callsWords are a card's words for the calls it keeps: one sentence for
// one call, its items below; for several, each call is a line, with its
// items.
func (c *core) callsWords(asks []accessQuestion, lang string) (sentence string, lines []string) {
	if len(asks) == 1 {
		a := asks[0]
		return c.callText(a, lang), labelLines(c.labelsOf(a), c.callCount(a), lang)
	}
	var said []string
	for _, a := range asks {
		s := c.callText(a, lang)
		said = append(said, s)
		if items := labelLines(c.labelsOf(a), c.callCount(a), lang); len(items) > 0 {
			s = fmt.Sprintf(tr(lang, "card.call.with"), s, strings.Join(items, ", "))
		}
		lines = append(lines, s)
	}
	return strings.Join(said, " · "), lines
}
