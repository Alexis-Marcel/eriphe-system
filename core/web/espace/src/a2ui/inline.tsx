import { createContext, useContext, useEffect, useState } from "react";
import type { ListRow } from "../data/api";
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
// of the row (its card's ref, code's), never from a block's data: it is
// answered even in a block someone else's words may have written.

// RowItem is the item a row of a List of a source's items draws, as
// eriphe-core told of it (its id, its card), and how many rows of that
// List share its card (shared): out of the block's data, so that no
// author can write it.
export type RowInfo = ListRow & { shared?: number };
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
// batch's rows unticked, whose calls its Oui leaves out.
export function InlineYes({ refText, where, say, left }: { refText: string; where: { surfaceId: string; componentId: string; at?: string }; say?: boolean; left?: ReadonlySet<string> }) {
  const { t } = useWords();
  const src = useProposals();
  const row = useContext(RowItem);
  const [p, setP] = useState<ProposalView | null | undefined>(undefined);
  const [busy, setBusy] = useState(false);
  const [failed, setFailed] = useState(false);
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
  useEffect(() => () => siblings(refText, false), [refText]);
  if (!refText || !p) return null;
  const answer = async (yes: boolean) => {
    if (busy) return;
    setBusy(true);
    setFailed(false);
    const { ok, again } = await answerOne(src, refText, p, yes, where, yes ? left : undefined);
    if (!ok) setFailed(true);
    setP(again ?? p);
    setBusy(false);
  };
  if (p.state !== "pending") {
    const word = p.state === "approved" ? (p.exec === "running" ? "espace.row.running" : p.exec === "failed" ? "espace.row.failed" : "espace.row.done") : "espace.row.rejected";
    return (
      <span className={`a2-inline-state a2-inline-${p.state}${p.exec ? " a2-inline-" + p.exec : ""}`} role="status">
        <IconView name={p.state === "approved" && p.exec !== "failed" ? (p.exec === "running" ? "clock" : "check") : "close"} />
        {t(word)}
      </span>
    );
  }
  const going = chosen(p, left).only.length;
  const yesWord = p.acts?.[0] && (p.choices?.length ?? 0) > 1 ? p.acts[Math.max(1, Math.min(going, p.acts.length)) - 1] : t("espace.a2.yes");
  const shared = row?.card === refText && (row.shared ?? 0) > 1 ? row.shared! : 0;
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
      {p.tainted && (
        <span className="a2-inline-ext" title={t("espace.a2.proposal.tainted")}>
          <IconView name="warning" />
          <span className="sr-only">{t("espace.a2.proposal.tainted")}</span>
        </span>
      )}
      {say && <span className="a2-inline-what">{p.text}</span>}
      {shared > 0 && (
        <span className="a2-inline-shared" title={t("espace.row.shared", shared)}>
          <IconView name="link" />
          <span aria-hidden="true">{shared}</span>
          <span className="sr-only">{t("espace.row.shared", shared)}</span>
        </span>
      )}
      <button type="button" className="a2-inline-no" disabled={busy} title={t("espace.row.no")} aria-label={t("espace.row.no")} onClick={() => answer(false)}>
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
