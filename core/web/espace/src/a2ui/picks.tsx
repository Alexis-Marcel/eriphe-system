import { createContext, useContext, useEffect, useMemo, useState, type ReactNode } from "react";
import type { PickAction, PickItem, PickList } from "../data/api";
import { useWords } from "../i18n";

// The Lists of a source's items eriphe-core tells the page of
// (list_actions.go, list_sections.go): a List that filters the source's
// items (a section: « À supprimer ») shows only the rows code kept, in the
// source's order; a List some action acts on lets the user pick rows in
// it. That one gets, per row, a discreet box (seen on hover, once
// something is picked, and always on a touch screen), « tout
// sélectionner », a button per action on all its rows (« Supprimer les
// 12 »), and, once something is picked, a small bar with the actions over
// the picked. Nothing else shows before.
//
// The buttons' words are code's, eriphe-core's (one, many and all with
// {n}): the author's label is only a small hint beside them, and
// eriphe-core sends none for a block someone else's words may have
// written. A tool that throws away asks once more, with its exact call:
// the tool, its fixed args and the very items it will take. The click is
// the user's own, on the ids the page showed, frozen when the button is
// pressed: eriphe-core reads the action again in the block, checks each
// id against the source and the List's filter, never adds one, and runs
// it (web_list_actions.go); the page says in a line what was done.

// Picks is what a page gives its Lists: the list a List component of a
// surface is, if eriphe-core told of it, and how to run an action on it
// (none on an older version, or offline). kept holds what is picked in
// each list, the confirm open and the line of the last click, by surface and List: the page
// read again (a new version, a source's snapshot) draws its blocks again,
// and the user's pick and confirm stay.
export type Picks = {
  list(surface: string, component: string): PickList | undefined;
  run?: (surface: string, list: PickList, action: PickAction, ids: string[]) => Promise<{ ok: boolean; text: string }>;
  kept: Map<string, Kept>;
};

type Kept = { picked: string[]; result: Result | null; asking?: Asking | null };

export const PicksContext = createContext<Picks | null>(null);

// useList is what eriphe-core told of a List: its rows (a filtered one
// shows only those) and, when the user may run its actions, picks.
export function useList(surface: string | undefined, component: string): { list: PickList; picks: Picks; pickable: boolean } | undefined {
  const picks = useContext(PicksContext);
  const list = picks && surface ? picks.list(surface, component) : undefined;
  return picks && list ? { list, picks, pickable: !!picks.run && list.actions.length > 0 } : undefined;
}

// buttonText is what an action's button says for n items: code's words.
export function buttonText(a: PickAction, n: number): string {
  return n === 1 ? a.one : a.many.replace("{n}", String(n));
}

// allText is what an action's button says for all the n rows of a list.
export function allText(a: PickAction, n: number): string {
  return n === 1 ? a.one : (a.all ?? a.many).replace("{n}", String(n));
}

// itemLine is an item as the confirm names it: its title · its detail.
export function itemLine(it: PickItem): string {
  const title = it.title || it.id;
  return it.detail ? `${title} · ${it.detail}` : title;
}

// MAX_IDS is the most items one click acts on (actionsMaxIDs): a list of
// more has no button for all its rows.
export const MAX_IDS = 200;

type Result = { ok: boolean; text: string };

// A row of a List as drawn: its place in the source's list, and what it
// shows.
export type Row = { n: number; node: ReactNode };

// Asking: an action that throws away, on the very items it will take.
type Asking = { action: PickAction; items: PickItem[]; label: string };

// Pickable draws a List's rows with their boxes, the buttons on all its
// rows, and the bar.
export function Pickable({ surface, list, picks, rows, direction }: { surface: string; list: PickList; picks: Picks; rows: Row[]; direction: string }) {
  const { t } = useWords();
  const key = `${surface}\u0000${list.component}`;
  const [picked, setPicked] = useState<string[]>(() => picks.kept.get(key)?.picked ?? []);
  const [asking, setAsking] = useState<Asking | null>(() => picks.kept.get(key)?.asking ?? null);
  const [running, setRunning] = useState(false);
  const [result, setResult] = useState<Result | null>(() => picks.kept.get(key)?.result ?? null);
  useEffect(() => void picks.kept.set(key, { picked, result, asking }), [picks.kept, key, picked, result, asking]);
  const byN = useMemo(() => new Map(list.items.map((it) => [it.n, it])), [list.items]);
  const ids = useMemo(() => new Set(list.items.map((it) => it.id)), [list.items]);

  // The list read again: what left it is no longer picked.
  useEffect(() => {
    setPicked((p) => {
      const kept = p.filter((id) => ids.has(id));
      return kept.length === p.length ? p : kept;
    });
  }, [ids]);

  const toggle = (id: string) => {
    setResult(null);
    setAsking(null);
    setPicked((p) => (p.includes(id) ? p.filter((x) => x !== id) : [...p, id]));
  };
  const all = picked.length > 0 && picked.length === ids.size;
  const pickAll = () => {
    setResult(null);
    setAsking(null);
    setPicked(all ? [] : list.items.map((it) => it.id));
  };
  const run = async (a: PickAction, items: PickItem[]) => {
    setAsking(null);
    setRunning(true);
    const ran = items.map((it) => it.id);
    const r = await (picks.run?.(surface, list, a, ran) ?? Promise.resolve({ ok: false, text: t("espace.act.error") })).catch(() => ({ ok: false, text: t("espace.act.error") }));
    const left = r.ok ? picked.filter((id) => !ran.includes(id)) : picked;
    // Kept at once: the page may have drawn the list again meanwhile.
    picks.kept.set(key, { picked: left, result: r, asking: null });
    setRunning(false);
    setResult(r);
    if (r.ok) setPicked(left);
  };
  // The items an action takes, frozen when its button is pressed: the
  // confirm names them, the click sends them, whatever the list shows
  // meanwhile.
  const press = (a: PickAction, items: PickItem[], label: string) => {
    setResult(null);
    if (a.confirm) setAsking({ action: a, items, label });
    else void run(a, items);
  };
  const chosen = list.items.filter((it) => picked.includes(it.id));
  const whole = list.items.length > 0 && list.items.length <= MAX_IDS;

  return (
    <div className={`a2-picks${picked.length ? " picking" : ""}`}>
      {picked.length > 0 && (
        <div className="a2-pickbar" role="toolbar" aria-label={t("espace.act.bar")}>
          <span className="a2-pickcount">{t("espace.act.picked", picked.length)}</span>
          {list.actions.map((a) => (
            <button key={a.name} type="button" className="a2-pickact" disabled={running} onClick={() => press(a, chosen, buttonText(a, chosen.length))} data-action={a.name}>
              {buttonText(a, picked.length)}
              {a.hint && (
                <small className={`a2-pickhint${a.hintExternal ? " ext" : ""}`} title={a.hintExternal ? t("espace.words.external") : undefined}>
                  {a.hint}
                  {a.hintExternal && <span aria-label={t("espace.words.external")}> ⚠</span>}
                </small>
              )}
            </button>
          ))}
        </div>
      )}
      {asking && (
        <div className="a2-pickask" role="alertdialog" aria-label={asking.label}>
          <p>{t("espace.act.ask")}</p>
          <dl>
            <dt>{asking.action.tool}</dt>
            {(asking.action.args ?? []).map((l) => (
              <dd key={l}>{l}</dd>
            ))}
          </dl>
          <ul>
            {asking.items.map((it) => (
              <li key={it.id}>{itemLine(it)}</li>
            ))}
          </ul>
          <span className="a2-pickask-btns">
            <button type="button" className="btn" onClick={() => setAsking(null)}>
              {t("espace.act.cancel")}
            </button>
            <button type="button" className="btn primary" onClick={() => void run(asking.action, asking.items)}>
              {t("espace.act.confirm")}
            </button>
          </span>
        </div>
      )}
      {running && (
        <p className="a2-pickdone" role="status">
          {t("espace.act.running")}
        </p>
      )}
      {!running && result && (
        <p className={`a2-pickdone${result.ok ? "" : " failed"}`} role="status">
          {result.text}
        </p>
      )}
      <div className="a2-pickhead">
        <label className="a2-pickall">
          <input type="checkbox" className="a2-pick" checked={all} onChange={pickAll} />
          <span>{all ? t("espace.act.clear") : t("espace.act.all")}</span>
        </label>
        {whole &&
          list.actions.map((a) => (
            <button
              key={a.name}
              type="button"
              className="a2-pickwhole"
              disabled={running}
              onClick={() => press(a, list.items.slice(), allText(a, list.items.length))}
              data-action={a.name}
            >
              {allText(a, list.items.length)}
            </button>
          ))}
      </div>
      <ul className={`a2-list a2-list-${direction} a2-pickable`}>
        {rows.map((row) => {
          const it = byN.get(row.n);
          const on = !!it && picked.includes(it.id);
          return (
            <li key={row.n} className={on ? "picked" : undefined}>
              {it && (
                <input type="checkbox" className="a2-pick" checked={on} onChange={() => toggle(it.id)} aria-label={t("espace.act.pick", it.title || it.id)} />
              )}
              <div className="a2-pickrow">{row.node}</div>
            </li>
          );
        })}
      </ul>
    </div>
  );
}
