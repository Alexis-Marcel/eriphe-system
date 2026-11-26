import { createContext, useContext, useEffect, useState } from "react";
import { useWords } from "../i18n";
import { Icon as IconView } from "./icons";
import { answerOne, chosen, useMoved, useProposals, type ProposalView } from "./proposals";

// A card that waits on an item, on the item's line (item_cards.go): one
// Oui, a discreet Non on hover, what the Oui does in code's words in its
// tooltip (the card's sentence and where it acts: « Mettre 2 mails à la
// corbeille · compte perso »). One card may wait on several rows (the
// brain's call on two mails): each row shows it, with a quiet mark of how
// many share it; hovered, its sibling rows light up; a Oui on any of them
// answers that one card. Decided, a short word of where it stands, until
// the item leaves the list. The page draws it from what eriphe-core told
// of the row (its card's ref, code's), never from the page's words: it is
// answered even on a line someone else's words may have written.

// RowItem is the item a page's row references, as eriphe-core told of it
// (its line, its id, its card: page_items.go), and how many rows of the
// page share its card (shared): never the page's words, so that no author
// can write it.
export type RowInfo = { n: number; id: string; card?: string; shared?: number };
export const RowItem = createContext<RowInfo | undefined>(undefined);

// inlineTitle is what a row's Oui says on hover: the card's sentence,
// and where it acts.
export function inlineTitle(p: ProposalView): string {
  return p.context ? `${p.text} · ${p.context}` : p.text;
}

// siblings lights up, or not, the rows that carry the same card.
function siblings(ref: string, on: boolean) {
  for (const e of document.querySelectorAll<HTMLElement>(`[data-row-card="${CSS.escape(ref)}"]`)) e.closest("li")?.classList.toggle("a2-card-sibling", on);
}

// InlineYes draws a card on a row, or on a batch's line. where: the
// surface, component and row it is answered from; left: the items a
// batch's rows unticked, whose calls its Oui leaves out; marked: the line
// it sits on already carries the sign of someone else's words (an
// invitation), which it does not repeat.
// On a row that shares its card with others, ✕ refuses only that row's
// own calls (those on its item alone), and the card waits on with the
// others; a row with no call of its own refuses the card.
export function InlineYes({ refText, where, say, left, marked }: { refText: string; where: { surfaceId: string; componentId: string; at?: string }; say?: boolean; left?: ReadonlySet<string>; marked?: boolean }) {
  const { t } = useWords();
  const src = useProposals();
  const row = useContext(RowItem);
  const [p, setP] = useProposal(refText);
  const [busy, setBusy] = useState(false);
  const [failed, setFailed] = useState(false);
  useEffect(() => () => siblings(refText, false), [refText]);
  if (!refText || !p) return null;
  const shared = row?.card === refText && (row.shared ?? 0) > 1 ? row.shared! : 0;
  const mine = shared && row ? rowCalls(p, row.id) : [];
  const answer = async (yes: boolean) => {
    if (busy) return;
    setBusy(true);
    setFailed(false);
    const { ok, again } =
      !yes && mine.length
        ? { ok: await src.answer({ surfaceId: where.surfaceId, componentId: where.componentId, ref: p.ref, yes: false, left: mine }).catch(() => false), again: await src.read(refText).catch(() => null) }
        : await answerOne(src, refText, p, yes, where, yes ? left : undefined);
    if (!ok) setFailed(true);
    setP(again ?? p);
    setBusy(false);
  };
  if (p.state !== "pending") return <InlineState p={p} />;
  const going = chosen(p, left).only.length;
  const yesWord = p.acts?.[0] && (p.choices?.length ?? 0) > 1 ? p.acts[Math.max(1, Math.min(going, p.acts.length)) - 1] : p.verb;
  const title = inlineTitle(p);
  return (
    <span
      className={`a2-inline${shared ? " a2-inline-many" : ""}`}
      aria-label={title}
      data-row-card={refText}
      onMouseEnter={shared ? () => siblings(refText, true) : undefined}
      onMouseLeave={shared ? () => siblings(refText, false) : undefined}
      onFocus={shared ? () => siblings(refText, true) : undefined}
      onBlur={shared ? () => siblings(refText, false) : undefined}
    >
      {p.tainted && !marked && (
        <span className="a2-inline-ext" title={t("espace.a2.proposal.tainted")}>
          <IconView name="warning" />
          <span className="sr-only">{t("espace.a2.proposal.tainted")}</span>
        </span>
      )}
      {say && p.text !== yesWord && <span className="a2-inline-what">{p.text}</span>}
      {shared > 0 && (
        <span className="a2-inline-shared" title={t("espace.row.shared", shared)}>
          <IconView name="link" />
          <span aria-hidden="true">{shared}</span>
          <span className="sr-only">{t("espace.row.shared", shared)}</span>
        </span>
      )}
      <button type="button" className="a2-inline-no" disabled={busy} title={t(mine.length ? "espace.row.drop" : "espace.row.no")} aria-label={t(mine.length ? "espace.row.drop" : "espace.row.no")} onClick={() => answer(false)}>
        <IconView name="close" />
      </button>
      <button type="button" className={`btn a2-button a2-button-primary a2-inline-yes${busy ? " is-busy" : ""}`} disabled={busy || ((p.choices?.length ?? 0) > 0 && going === 0)} title={title} aria-busy={busy} onClick={() => answer(true)}>
        {yesWord}
      </button>
      {failed && (
        <span className="a2-proposal-failed" role="alert">
          {t("espace.a2.proposal.failed")}
        </span>
      )}
    </span>
  );
}

// InlineState is a decided card's short word on its row, until the item
// leaves the list.
function InlineState({ p }: { p: ProposalView }) {
  const { t } = useWords();
  const word = p.state === "approved" ? (p.exec === "running" ? "espace.row.running" : p.exec === "failed" ? "espace.row.failed" : "espace.row.done") : "espace.row.rejected";
  return (
    <span className={`a2-inline-state a2-inline-${p.state}${p.exec ? " a2-inline-" + p.exec : ""}`} role="status">
      <IconView name={p.state === "approved" && p.exec !== "failed" ? (p.exec === "running" ? "clock" : "check") : "close"} />
      {t(word)}
    </span>
  );
}

// rowCalls are the calls of a card that wait and act on this one item
// alone: what a row's ✕ may refuse without touching another row.
export function rowCalls(p: ProposalView, item: string): number[] {
  return (p.choices ?? []).filter((c) => c.state === "proposed" && c.items?.length === 1 && c.items[0] === item).map((c) => c.id);
}

// useProposal is a card's proposal, read and read again each time it
// moves; undefined while it is read, null when there is none.
function useProposal(refText: string): [ProposalView | null | undefined, (p: ProposalView | null) => void] {
  const src = useProposals();
  const [p, setP] = useState<ProposalView | null | undefined>(undefined);
  useEffect(() => {
    if (!refText) return;
    let live = true;
    src.read(refText).then(
      (v) => live && setP(v),
      () => live && setP(null),
    );
    return () => {
      live = false;
    };
  }, [src, refText]);
  const moved = useMoved(src, refText ? [refText] : []);
  useEffect(() => {
    if (!moved || !refText) return;
    let live = true;
    src.read(refText).then((v) => live && setP(v), () => {});
    return () => {
      live = false;
    };
  }, [src, refText, moved]);
  return [p, setP];
}
