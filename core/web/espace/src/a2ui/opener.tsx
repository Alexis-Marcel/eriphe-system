import { useEffect, useId, useState, type MouseEvent, type ReactNode } from "react";
import type { Opened, PickItem, PickList } from "../data/api";
import { useWords } from "../i18n";
import type { Picks } from "./picks";

// An item opened in place (item_open.go): a row of a List whose source
// says how one item is read in full opens below itself on a click, or on
// its disclosure button (the keyboard's way), and closes the same way or
// by its ✕. eriphe-core reads the item with the source's read tool, the
// first time it is opened (or asks the user first, inline, for a read
// that needs their yes), and the page keeps what it said for its session
// (picks.opened), drawn again as it was when the page is. What it said is
// shown as text, never as HTML: its common fields, its body, the whole
// under « Détails », marked « extérieur » when its server is not trusted,
// with the item's note (« Classé « À supprimer » : … »). A click on a box,
// a button or a link of the row does not open it.

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
      <div className="a2-openhead" onClick={onRow}>
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
          <OpenedView st={st} onYes={() => void read(true)} />
        </div>
      )}
    </div>
  );
}

function OpenedView({ st, onYes }: { st: OpenState; onYes: () => void }) {
  const { t } = useWords();
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
  const note = r.note;
  return (
    <div className="a2-openbody">
      {r.external && <span className="a2-text-mark">{t("espace.external.mark")}</span>}
      {note && (note.group || note.note) && (
        <p className="a2-opennote">
          {note.group ? t("espace.open.group", note.group) : t("espace.open.note")}
          {note.note ? ` : ${note.note}` : ""}
          {note.external && <span className="a2-text-mark">{t("espace.external.mark")}</span>}
        </p>
      )}
      {r.title && <p className="a2-opentitle">{r.title}</p>}
      {(r.from || r.date) && (
        <dl className="a2-openmeta">
          {r.from && (
            <>
              <dt>{t("espace.open.from")}</dt>
              <dd>{r.from}</dd>
            </>
          )}
          {r.date && (
            <>
              <dt>{t("espace.open.date")}</dt>
              <dd>{r.date}</dd>
            </>
          )}
        </dl>
      )}
      {r.body && <div className="a2-opentext">{r.body}</div>}
      {r.raw && (
        <details className="a2-openraw">
          <summary>{t("espace.open.details")}</summary>
          <pre>{r.raw}</pre>
        </details>
      )}
    </div>
  );
}
