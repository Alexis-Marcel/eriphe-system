package main

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// Who may write what in the memory is decided here, in code: a prompt
// injection read in a mail or a page must not become something Eriphe
// believes. The rules:
//
//   - external content, and a task that read it, only propose (status
//     proposed), never a profile, a preference or a lesson; I approve them
//     in the workspace, or they expire undecided; the nightly extraction
//     leaves such a task out altogether;
//   - a brain turn that called an external catalog tool is tainted: what it
//     remembers is only proposed, unless my own message of that turn asked
//     for it; nor may it lend a task tools that act (taintedResources);
//   - only my own words create or change lessons, preferences and the
//     profile (source_ref names my message, checked against its role);
//   - every write goes through the scanner: instructions to the model,
//     invisible characters, secrets and tokenised links are refused, softer
//     imperative wording is flagged and only proposed.

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

// externalLine names an external memory in a packet without quoting it.
func externalLine(x *Memory) string {
	src := x.SourceRef
	if src == "" {
		src = x.SourceKind
	}
	return fmt.Sprintf("#%d (%s tiré d'un contenu extérieur, %s : recall le relit, ce qui souille le tour)", x.ID, x.Kind, src)
}

// rememberRequest: wording of mine that asks Eriphe to keep something.
var rememberRequest = regexp.MustCompile(`(?i)\b(retiens|retenir|souviens|souvenir|rappelle[- ]toi|mémorise|memorise|mémoriser|note (que|bien|ça|ca)|garde en (tête|tete|mémoire|memoire)|n'oublie pas|oublie pas|remember|keep in mind|note that|don't forget|do not forget|mets? (le |la |ça |ca )?(dans|en) (ta )?mémoire|enregistre|ajoute à ta mémoire|à l'avenir|a l'avenir|la prochaine fois|désormais|dorénavant|corrige|mets à jour|oublie|forget)\b`)

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
	msg, owner := c.ownerMessage(d.Ref)
	if owner {
		d.Trust = trustOwner
		d.Ref = fmt.Sprintf("msg:%d", msg.Seq)
	}
	if ownerKind(kind) && !owner {
		return d, errOwnerOnly
	}
	asked := owner && c.askedThisTurn(msg)
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

// askedThisTurn tells whether my message, of this very turn, asks Eriphe
// to keep, correct or forget something: that lifts a tainted turn's
// "only proposed" for the memory, never for acting.
func (c *core) askedThisTurn(msg *Message) bool {
	from, to, _, _ := c.turn.snapshot()
	return msg.Seq >= from && msg.Seq <= to && from > 0 && rememberRequest.MatchString(msg.Text)
}
