package main

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// A kept call read plainly (the brain's access card, calls and runcalls
// cards): what its yes does to which items, in code's words, never the
// model's; its exact call (tool and every parameter, raw) under the
// card's one Détails, and the yes runs that, as it is shown. Nothing here
// knows a server: mail, calendar or any other.
//
//   - The sentence says what happens to what: as the tool declares it
//     (its _meta "eriphe/says", call_says.go), else by its own title (its
//     annotations', or its own), else by its name; and what it acts on,
//     how many and of what kind (the kind as the tool declares it, else
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
	// No item known: a list its name says is of ids (idArg).
	for _, k := range slices.Sorted(maps.Keys(input)) {
		if v, ok := input[k].([]any); ok && len(v) > 0 && idArg(k, nil, nil) {
			return k, len(v)
		}
	}
	return "", 0
}

// countItems says n items, of no kind: « 3 éléments ».
func countItems(n int, lang string) string {
	if n == 1 {
		return tr(lang, "card.noun.item.one")
	}
	return fmt.Sprintf(tr(lang, "card.noun.item.many"), n)
}

// callPhrase says what a call does to what, in a sentence, for a tool
// that does not say it itself (call_says.go): the action (its title, else
// its name), the items (how many), and its other short parameters. title
// is the tool's own (its annotations' or its own), "" for none; count,
// when not 0, how many it says in the place of the input's (calls that do
// the same to other items, together).
func callPhrase(tool, title string, input map[string]any, labels []itemLabel, lang string, count int) string {
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
		s := buttonText(title, 80)
		if s == "" {
			s = upperFirst(fmt.Sprintf(tr(lang, "access.call.other"), strings.ReplaceAll(short, "_", " ")))
		}
		if one != "" {
			s += " · " + one
		}
		return s
	}
	what := countItems(n, lang)
	var s string
	switch t := buttonText(title, 60); {
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
	if _, t := c.catalogTool(callTool(tool)); t != nil {
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
	if s := c.toolSaysOf(name); s != nil {
		// The tool says its call itself (call_says.go).
		_, n := callItems(in, labels)
		if count > 0 {
			n = count
		}
		return s.phrase(in, s.itemWords(labels, n, lang), s.countWords(n, lang), lang, c.userZone())
	}
	return callPhrase(name, c.toolTitle(name), in, labels, lang, count)
}

// saysItem tells whether a kept call's sentence names its one item
// itself (a tool that says its call, call_says.go): no line below says
// it again.
func (c *core) saysItem(a accessQuestion) bool {
	name, _, _ := strings.Cut(a.Call, " ")
	if len(c.labelsOf(a)) != 1 || c.callCount(a) > 1 {
		return false
	}
	s := c.toolSaysOf(name)
	return s != nil && strings.Contains(inLang(s.Text, "fr")+inLang(s.Text, "en"), "{item}")
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

// callDetail is a kept call's exact call, one click further than its
// Détails (Raw): its tool, and every parameter of its input, raw, but
// those shown (callShown, by name).
func callDetail(a accessQuestion, shown []string) cardField {
	var input map[string]any
	json.Unmarshal([]byte(a.Input), &input)
	var lines []string
	for _, f := range callFields(input) {
		if !slices.Contains(shown, f.Name) {
			lines = append(lines, f.Name+" : "+f.Value)
		}
	}
	return cardField{Name: callTool(a.Call), Value: truncate(strings.Join(lines, "\n"), cardValueMax), Detail: true, Raw: true}
}

// callDetails are a kept call's Détails: its parameters by their meaning
// (each named by its tool's input schema, else in the user's words for a
// common name, else by its own; its value in words: the tool's declared
// words, a date as one), never an id (its item is said by its label on
// the card), nor the account (said quietly beside it), nor what is shown
// (callShown); then its exact call (callDetail), one click further.
func (c *core) callDetails(a accessQuestion, shown []string, lang string) []cardField {
	var input map[string]any
	json.Unmarshal([]byte(a.Input), &input)
	tool, _, _ := strings.Cut(a.Call, " ")
	props, _ := c.inputSchema(tool)["properties"].(map[string]any)
	says := c.toolSaysOf(tool)
	if says == nil {
		says = &toolSays{}
	}
	ids := map[string]bool{}
	for _, l := range c.labelsOf(a) {
		ids[l.ID] = true
	}
	var out []cardField
	for _, k := range slices.Sorted(maps.Keys(input)) {
		v := input[k]
		if slices.Contains(shown, k) || k == "account" || idArg(k, v, ids) {
			continue
		}
		w := says.argWords(k, v, lang, c.userZone())
		if _, isBool := v.(bool); isBool && w == "" {
			w = argValue(v, lang)
		}
		if w == "" {
			continue
		}
		out = append(out, cardField{Name: paramName(k, props, lang), Value: w, Detail: true})
	}
	return append(out, callDetail(a, shown))
}

// idArg tells an argument that names items by their id: by its name (id,
// ids, …_id, …_ids), or its value one of the items' ids.
func idArg(k string, v any, ids map[string]bool) bool {
	k = strings.ToLower(k)
	if k == "id" || k == "ids" || strings.HasSuffix(k, "_id") || strings.HasSuffix(k, "_ids") {
		return true
	}
	switch x := v.(type) {
	case string:
		return ids[x]
	case []any:
		return slices.ContainsFunc(x, func(e any) bool { return ids[valueText(e)] })
	}
	return false
}

// paramName names a call's parameter for the user: its input schema's
// title, else the user's words for a common name, else its own.
func paramName(k string, props map[string]any, lang string) string {
	p, _ := props[k].(map[string]any)
	name, _ := p["title"].(string)
	if name = truncate(oneLine(name), 60); name != "" {
		return name
	}
	if w := tr(lang, "card.param."+strings.ToLower(k)); w != "card.param."+strings.ToLower(k) {
		return w
	}
	return k
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
		fs = append(fs, cardField{Name: paramName(k, props, lang), Value: s})
		names = append(names, k)
	}
	return fs, names
}

// callFieldsOf are a kept call's fields as its card shows them: what was
// read to say it, its long texts to read, then its exact call under the
// one Détails.
func (c *core) callFieldsOf(a accessQuestion, lang string) []cardField {
	shown, names := c.callShown(a, lang)
	return append(append(callRead(a), shown...), c.callDetails(a, names, lang)...)
}

// callsWords are a card's words for the calls it keeps: one sentence for
// one call, its items below; for several, each call is a line, with its
// items.
func (c *core) callsWords(asks []accessQuestion, lang string) (sentence string, lines []string) {
	if len(asks) == 1 {
		a := asks[0]
		if c.saysItem(a) {
			return c.callText(a, lang), nil
		}
		return c.callText(a, lang), labelLines(c.labelsOf(a), c.callCount(a), lang)
	}
	var said []string
	for _, a := range asks {
		s := c.callText(a, lang)
		said = append(said, s)
		if items := labelLines(c.labelsOf(a), c.callCount(a), lang); len(items) > 0 && !c.saysItem(a) {
			s = fmt.Sprintf(tr(lang, "card.call.with"), s, strings.Join(items, ", "))
		}
		lines = append(lines, s)
	}
	return strings.Join(said, " · "), lines
}

// catalogTool is a catalog tool in the pool, by its server/tool name;
// nil when it is not there.
func (c *core) catalogTool(name string) (*upstream, *mcp.Tool) {
	server, tool, ok := strings.Cut(name, "/")
	if !ok || c.pool == nil {
		return nil, nil
	}
	u := c.pool.get(server)
	if u == nil {
		return nil, nil
	}
	return u, u.toolByClaudeName(tool)
}

// annotationTitle is a tool's title as its annotations say it.
func annotationTitle(t *mcp.Tool) string {
	if t.Annotations != nil {
		return t.Annotations.Title
	}
	return ""
}

// buttonText is a text of a tool's for a card's words: one line, no
// placeholder of ours, n runes at most.
func buttonText(s string, n int) string {
	s = strings.NewReplacer("{n}", "", "%", "％").Replace(s)
	return truncate(oneLine(s), n)
}

// argsText says a call's args in a line: a single text by its
// value, else each as « name : value ».
func argsText(args map[string]any, lang string) string {
	if len(args) == 0 {
		return ""
	}
	keys := slices.Sorted(maps.Keys(args))
	if len(keys) == 1 {
		if s, ok := args[keys[0]].(string); ok {
			return buttonText(s, 40)
		}
	}
	return strings.Join(argLines(args, lang, 3), ", ")
}

// argLines are a call's args, « name : value » each, most of
// them (0: all).
func argLines(args map[string]any, lang string, most int) []string {
	var out []string
	for _, k := range slices.Sorted(maps.Keys(args)) {
		if most > 0 && len(out) == most {
			out = append(out, "…")
			break
		}
		out = append(out, buttonText(k, 30)+" : "+buttonText(argValue(args[k], lang), 60))
	}
	return out
}

// argValue is an arg's value in words.
func argValue(v any, lang string) string {
	switch x := v.(type) {
	case bool:
		return tr(lang, map[bool]string{true: "act.yes", false: "act.no"}[x])
	case string:
		return x
	}
	raw, _ := json.Marshal(v)
	return string(raw)
}

// doneText says in a line what calls of a tool did: as the tool declares
// it (call_says.go), else how many items they took.
func (c *core) doneText(call string, n int, lang string) string {
	if s := c.toolSaysOf(call); s != nil {
		if w := counted(s.Done, n, lang); w != "" {
			return w
		}
	}
	if n == 1 {
		return tr(lang, "act.done.one")
	}
	return fmt.Sprintf(tr(lang, "act.done.many"), n)
}
