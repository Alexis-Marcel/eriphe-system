import { useEffect, useId, useRef, useState, type MouseEvent, type ReactNode } from "react";
import type { Opened, PickItem, PickList } from "../data/api";
import { useWords } from "../i18n";
import { stamp } from "./format";
import { Icon } from "./icons";
import type { Picks } from "./picks";
import { answerOne, useMoved, useProposals, type ProposalView } from "./proposals";

// An item opened in place (item_open.go): a row of a List whose source
// says how one item is read in full opens below itself on a click, or on
// its disclosure button (the keyboard's way), and closes the same way or
// by its ✕. eriphe-core reads the item with the source's read tool, the
// first time it is opened (or asks the user first, inline, for a read
// that needs their yes), and the page keeps what it said for its session
// (picks.opened), drawn again as it was when the page is.
// It reads as a letter: who and when, quietly, on one line; its subject;
// its text, shown as text, never as HTML. The item's note only when the
// row does not already say it. The card that waits on it (item.card,
// code's list, never the block's data) comes next: what its Oui does,
// the very words or event it makes, and its Oui there. One « détails »
// at the foot holds the exact call and the item as read. Someone else's
// words are marked once, by the page's head and the block's edge: nothing
// here repeats it. A click on a box, a button or a link of the row does
// not open it.

// OpenState is a row's: open or not, and what was read.
// pending is the read under way, kept with it: a row drawn again while it
// reads (the page read again) takes it up.
export type OpenState = { open: boolean; read?: Opened; loading?: boolean; pending?: Promise<Opened> };

const INTERACTIVE = "button, a, input, label, select, textarea, [role=button]";

export function Opener({ surface, list, picks, item, children }: { surface: string; list: PickList; picks: Picks; item: PickItem; children: ReactNode }) {
  const { t } = useWords();
  const key = `${surface}\u0000${list.component}\u0000${item.id}`;
  const [st, setSt] = useState<OpenState>(() => picks.opened?.get(key) ?? { open: false });
  useEffect(() => void picks.opened?.set(key, st), [picks.opened, key, st]);
  const panel = useId();
  const head = useRef<HTMLDivElement>(null);

  // A read under way, from this row or the one drawn before it: its
  // answer, when it comes, is the row's, kept at once (the page may have
  // drawn the list again meanwhile).
  const pending = st.loading ? st.pending : undefined;
  useEffect(() => {
    if (!pending) return;
    let here = true;
    void pending.then((r) => {
      const next = { open: true, read: r, loading: false };
      picks.opened?.set(key, next);
      if (here) setSt(next);
    });
    return () => {
      here = false;
    };
  }, [pending, picks.opened, key]);

  const read = (yes: boolean) => {
    const p = (picks.open?.(surface, list, item.id, yes, yes ? st.read?.ask?.tool : undefined) ?? Promise.resolve<Opened>({ error: t("espace.open.error") })).catch(() => ({
      error: t("espace.open.error"),
    }));
    const next = { ...st, open: true, loading: true, pending: p };
    picks.opened?.set(key, next);
    setSt(next);
  };
  const toggle = () => {
    if (st.open) setSt((s) => ({ ...s, open: false }));
    else if (st.read && !st.read.error) setSt((s) => ({ ...s, open: true }));
    else void read(false);
  };
  const onRow = (e: MouseEvent) => {
    if ((e.target as Element).closest(INTERACTIVE)) return; // its own gestures
    toggle();
  };

  return (
    <div className={`a2-openrow${st.open ? " open" : ""}`}>
      <div className="a2-openhead" ref={head} onClick={onRow}>
        <div className="a2-openmain">{children}</div>
        <button
          type="button"
          className="a2-openbtn"
          aria-expanded={st.open}
          aria-controls={panel}
          aria-label={st.open ? t("espace.open.close") : t("espace.open.show", item.title || item.id)}
          onClick={toggle}
        >
          {st.open ? "▾" : "▸"}
        </button>
      </div>
      {st.open && (
        <div id={panel} className="a2-opened" role="region" aria-label={item.title || item.id}>
          <button type="button" className="a2-openclose" aria-label={t("espace.open.close")} onClick={() => setSt((s) => ({ ...s, open: false }))}>
            ✕
          </button>
          <OpenedView st={st} said={head.current?.textContent ?? ""} card={item.card} where={{ surfaceId: surface, componentId: list.component }} onYes={() => void read(true)} />
        </div>
      )}
    </div>
  );
}

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

function OpenedView({ st, said, card, where, onYes }: { st: OpenState; said: string; card?: string; where: { surfaceId: string; componentId: string }; onYes: () => void }) {
  const { t, lang } = useWords();
  const [p, setP] = useCard(card);
  const r = st.read;
  if (st.loading || !r) return <p className="a2-openwait" role="status">{t("espace.open.loading")}</p>;
  if (r.error) return <p className="a2-openfail" role="status">{r.error}</p>;
  if (r.ask)
    return (
      <div className="a2-openask">
        <p>{t("espace.open.ask")}</p>
        <dl>
          <dt>{r.ask.tool}</dt>
          {r.ask.args.map((l) => (
            <dd key={l}>{l}</dd>
          ))}
        </dl>
        <button type="button" className="btn primary" onClick={onYes}>
          {t("espace.open.yes")}
        </button>
      </div>
    );
  // The note, only when the row does not already say it.
  const note = r.note && (r.note.group || r.note.note) && !(r.note.note && said.includes(r.note.note)) ? r.note : undefined;
  const who = r.from ? sender(r.from) : null;
  const when = r.date ? stamp(lang, r.date) || r.date : "";
  const exact = (p?.fields ?? []).filter((f) => f.detail);
  return (
    <article className="a2-read">
      {(who || when) && (
        <header className="a2-read-head">
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
      {r.title && <h4 className="a2-read-title">{r.title}</h4>}
      {note && (
        <p className="a2-read-note">
          {note.group ? t("espace.open.group", note.group) : t("espace.open.note")}
          {note.note ? ` : ${note.note}` : ""}
        </p>
      )}
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
          {t("espace.a2.yes")}
        </button>
      </div>
    </section>
  );
}
