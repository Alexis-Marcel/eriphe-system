package main

import (
	"fmt"
	"regexp"
	"strings"
)

// Who may write an intention is decided here, in code: a prompt injection
// read in a mail or a page must not become something Eriphe owes or
// reminds. The rules:
//
//   - a brain turn that called an external catalog tool is tainted: an
//     intention it writes is only proposed, unless my own message of that
//     turn asked for it; I approve it in the workspace, or it expires
//     undecided;
//   - every write goes through the scanner (scan.go): instructions to the
//     model, invisible characters, secrets and tokenised links are
//     refused, softer imperative wording is flagged and only proposed.

// rememberRequest: wording of mine that asks Eriphe to keep something.
var rememberRequest = regexp.MustCompile(`(?i)\b(retiens|retenir|souviens|souvenir|rappelle[- ]toi|mémorise|memorise|mémoriser|note (que|bien|ça|ca)|garde en (tête|tete|mémoire|memoire)|n'oublie pas|oublie pas|remember|keep in mind|note that|don't forget|do not forget|mets? (le |la |ça |ca )?(dans|en) (ta )?mémoire|enregistre|ajoute à ta mémoire|à l'avenir|a l'avenir|la prochaine fois|désormais|dorénavant|corrige|mets à jour|oublie|forget)\b`)

// How a brain write is recorded (writeDecision.Status).
const (
	writeActive   = "active"
	writeProposed = "proposed" // waits for my approval
)

// writeDecision is how a brain write is recorded, or why it is refused.
type writeDecision struct {
	Status string // writeActive, or writeProposed
	Trust  string
	Source string // source_kind
	Ref    string
	Why    string // why it is only proposed
}

// decideBrainWrite applies the rules to a brain's intention: ref cites
// my message (msg:N) when it comes from my words.
func (c *core) decideBrainWrite(ref string, scan scanResult) (writeDecision, error) {
	d := writeDecision{Status: writeActive, Trust: trustFirstParty, Source: srcBrain, Ref: strings.TrimSpace(ref)}
	if scan.Reject != "" {
		return d, fmt.Errorf("refused: %s", scan.Reject)
	}
	msg, owner := c.ownerMessage(d.Ref)
	if owner {
		d.Trust = trustOwner
		d.Ref = fmt.Sprintf("msg:%d", msg.Seq)
	}
	asked := owner && c.askedThisTurn(msg)
	if tainted := c.turnTaint(); len(tainted) > 0 && !asked {
		d.Status, d.Trust = writeProposed, trustExternal
		d.Why = "this turn read external content (" + strings.Join(tainted, ", ") + ")"
		return d, nil
	}
	if containsStr(scan.Flags, "imperative") && !owner {
		d.Status = writeProposed
		d.Why = "it reads like an instruction, and does not come from the user's own message"
	}
	return d, nil
}

// askedThisTurn tells whether my message, of this very turn, asks Eriphe
// to keep, correct or forget something: that lifts a tainted turn's
// "only proposed" for an intention, never for acting.
func (c *core) askedThisTurn(msg *Message) bool {
	from, to, _, _ := c.turn.snapshot()
	return msg.Seq >= from && msg.Seq <= to && from > 0 && rememberRequest.MatchString(msg.Text)
}
