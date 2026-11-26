import { useEffect, useState } from "react";
import type { Opened } from "../data/api";
import { useWords } from "../i18n";
import { stamp } from "./format";
import { Icon } from "./icons";
import { answerOne, useMoved, useProposals, type ProposalView } from "./proposals";

// An item a page references, opened in place below its line
// (page/items.tsx, item_open.go): eriphe-core reads it with its server's
// read tool, the first time it is opened (a read that needs the user's
// yes is not made: a line says to ask Eriphe, who proposes it on a card).
// Opening is never a yes.
// It reads as a letter: who and when, quietly, on one line; its subject;
// its text, shown as text, never as HTML. The card that waits on it
// (code's, never the page's words) comes next: what its Oui does, the
// very words or event it makes, and its Oui there. One « détails » at the
// foot holds the exact call and the item as read. Someone else's words
// are marked once, by the page's head (its ⚠): nothing here repeats it.

// OpenState is a row's: open or not, and what was read.
// pending is the read under way, kept with it: a row drawn again while it
// reads (the page read again) takes it up.
export type OpenState = { open: boolean; read?: Opened; loading?: boolean; pending?: Promise<Opened> };

// useCard is the card that waits on an opened item, read again whenever it
// moves (its Oui on the row, a channel, another page).
function useCard(card: string | undefined): [ProposalView | null, (p: ProposalView | null) => void] {
  const src = useProposals();
  const [p, setP] = useState<ProposalView | null>(null);
  const moved = useMoved(src, card ? [card] : []);
  useEffect(() => {
    if (!card) return setP(null);
    let live = true;
    src.read(card).then(
      (v) => live && setP(v),
      () => live && setP(null),
    );
    return () => {
      live = false;
    };
  }, [src, card, moved]);
  return [p, setP];
}

// A sender as a letter's head says it: "Sam <sam@example.org>" is Sam, the
// address beside, quieter; anything else as it is.
export function sender(from: string): { name: string; address?: string } {
  const m = /^\s*"?([^"<]*?)"?\s*<([^<>@\s]+@[^<>\s]+)>\s*$/.exec(from);
  return m && m[1] ? { name: m[1], address: m[2] } : { name: from };
}

// LONG is from what length a field of a card is content to read (a reply,
// a message) rather than a fact on a line.
const LONG = 90;

// OpenedView is an item opened, as read (st), below a page's line
// (page/items.tsx). marked: the page's head already says someone else's
// words; card: the card that waits on it, its Oui here too; named: the
// page's words for it, said with a warning when eriphe-core finds they do
// not say the item read (misleading).
export function OpenedView({ st, marked, card, where, named }: { st: OpenState; marked: boolean; card?: string; where: { surfaceId: string; componentId: string }; named?: string }) {
  const { t, lang } = useWords();
  const [p, setP] = useCard(card);
  const r = st.read;
  if (st.loading || !r) return <p className="a2-openwait" role="status">{t("espace.open.loading")}</p>;
  if (r.error) return <p className="a2-openfail" role="status">{r.error}</p>;
  const who = r.from ? sender(r.from) : null;
  const when = r.date ? stamp(lang, r.date) || r.date : "";
  const exact = (p?.fields ?? []).filter((f) => f.detail);
  // Someone else's words on a page not marked as such (the page's head says it
  // otherwise): a small amber sign, its sentence on hover.
  const taint = r.external && !marked && (
    <span className="a2-taint" role="img" title={t("espace.external.why")} aria-label={t("espace.external.why")}>
      <Icon name="warning" />
    </span>
  );
  return (
    <article className="a2-read">
      {(who || when || taint) && (
        <header className="a2-read-head">
          {taint}
          {who && (
            <span className="a2-read-from" title={r.from}>
              {who.name}
              {who.address && <span className="a2-read-addr">{who.address}</span>}
            </span>
          )}
          {when && (
            <time className="a2-read-date" dateTime={r.date} title={r.date}>
              {when}
            </time>
          )}
        </header>
      )}
      {r.misleading && named && (
        <p className="a2-read-lie" role="note">
          <Icon name="warning" /> {t("espace.open.misleading", named)}
        </p>
      )}
      {r.title && <h4 className="a2-read-title">{r.title}</h4>}
      {r.body && <div className="a2-read-text">{r.body}</div>}
      {p && card && <OnYes p={p} card={card} where={where} onAnswered={setP} />}
      {(exact.length > 0 || r.raw) && (
        <details className="a2-read-more">
          <summary>{t("espace.open.more")}</summary>
          {exact.length > 0 && p?.state === "pending" && (
            <section>
              <h5>{t("espace.open.call")}</h5>
              <pre>{exact.map((f) => `${f.name}\n${f.value}`).join("\n\n")}</pre>
            </section>
          )}
          {r.raw && (
            <section>
              <h5>{t("espace.open.raw")}</h5>
              <pre>{r.raw}</pre>
            </section>
          )}
        </details>
      )}
    </article>
  );
}

// OnYes is, under an opened item, the card that waits on it: what its Oui
// does in code's words, then what it makes, as it will be (a long field,
// the reply itself, in a calm box: text, never Markdown, whoever wrote
// it; a short one, a fact on a line), and its Oui there, the same as the
// row's (which keeps quiet while the item is open). Decided: where it
// stands, in a word.
function OnYes({ p, card, where, onAnswered }: { p: ProposalView; card: string; where: { surfaceId: string; componentId: string }; onAnswered: (p: ProposalView | null) => void }) {
  const { t } = useWords();
  const src = useProposals();
  const [busy, setBusy] = useState(false);
  const [failed, setFailed] = useState(false);
  if (p.state !== "pending") {
    const word = p.state === "approved" ? (p.exec === "running" ? "espace.row.running" : p.exec === "failed" ? "espace.row.failed" : "espace.row.done") : "espace.row.rejected";
    return (
      <p className={`a2-onyes-state a2-inline-${p.state}${p.exec ? " a2-inline-" + p.exec : ""}`} role="status">
        <Icon name={p.state === "approved" && p.exec !== "failed" ? (p.exec === "running" ? "clock" : "check") : "close"} />
        {t(word)} · {p.text}
      </p>
    );
  }
  const answer = async (yes: boolean) => {
    if (busy) return;
    setBusy(true);
    setFailed(false);
    const { ok, again } = await answerOne(src, card, p, yes, where);
    if (!ok) setFailed(true);
    onAnswered(again ?? p);
    setBusy(false);
  };
  const shown = (p.fields ?? []).filter((f) => !f.detail);
  const long = shown.filter((f) => f.value.length > LONG || f.value.includes("\n"));
  const short = shown.filter((f) => !long.includes(f));
  return (
    <section className={`a2-onyes${shown.length === 0 ? " bare" : ""}`} aria-label={t("espace.open.onyes")}>
      <p className="a2-onyes-what">
        <span className="a2-onyes-kicker">{t("espace.open.onyes")}</span>
        {p.text}
      </p>
      {short.length > 0 && (
        <dl className="a2-onyes-facts">
          {short.map((f) => (
            <div key={f.name}>
              <dt>{f.name}</dt>
              <dd>{f.value}</dd>
            </div>
          ))}
        </dl>
      )}
      {long.map((f) => (
        <figure key={f.name} className="a2-onyes-content">
          <figcaption>{f.name}</figcaption>
          <div className="a2-onyes-text">{f.value}</div>
        </figure>
      ))}
      <div className="a2-onyes-acts">
        {failed && (
          <span className="a2-proposal-failed" role="alert">
            {t("espace.a2.proposal.failed")}
          </span>
        )}
        <button type="button" className="a2-inline-no a2-onyes-no" disabled={busy} title={t("espace.row.no")} aria-label={t("espace.row.no")} onClick={() => answer(false)}>
          <Icon name="close" />
        </button>
        <button type="button" className={`btn a2-button a2-button-primary a2-inline-yes${busy ? " is-busy" : ""}`} disabled={busy} aria-busy={busy} onClick={() => answer(true)}>
          {p.verb}
        </button>
      </div>
    </section>
  );
}
