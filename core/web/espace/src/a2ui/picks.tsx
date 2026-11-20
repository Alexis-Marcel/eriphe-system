import { createContext, useContext, useEffect, useMemo, useState, type ReactNode } from "react";
import type { PickAction, PickItem, PickList } from "../data/api";
import { useWords } from "../i18n";

// Picking items in a list, and running on them the actions their block
// declares (list_actions.go). A List bound to a source's items that some
// action acts on gets, per row, a discreet box (seen on hover, once
// something is picked, and always on a touch screen), « tout
// sélectionner », and, once something is picked, a small bar with the
// actions over the list. Nothing shows before.
//
// The buttons' words are code's, eriphe-core's (one, many with {n}): the
// author's label is only a small hint beside them, and eriphe-core sends
// none for a block someone else's words may have written. A tool that
// throws away asks once more, with its exact call: the tool, its fixed
// args and the items picked. The click is the user's own: eriphe-core
// reads the action again in the block, checks the ids against the source
// and runs it (web_list_actions.go); the page says in a line what was
// done.

// Picks is what a page gives its Lists: the list a List component of a
// surface picks in, if any, and how to run an action on it. kept holds
// what is picked in each list and the line of the last click, by surface
// and List: the page read again (a new version, a source's snapshot)
// draws its blocks again, and the user's pick stays.
export type Picks = {
  list(surface: string, component: string): PickList | undefined;
  run(surface: string, list: PickList, action: PickAction, ids: string[]): Promise<{ ok: boolean; text: string }>;
  kept: Map<string, Kept>;
};

type Kept = { picked: string[]; result: Result | null };

export const PicksContext = createContext<Picks | null>(null);

// usePicks is what a List picks in, and how to run; undefined when it is
// not a list to pick in (or not on the last version, or offline).
export function usePicks(surface: string | undefined, component: string): { list: PickList; picks: Picks } | undefined {
  const picks = useContext(PicksContext);
  const list = picks && surface ? picks.list(surface, component) : undefined;
  return picks && list && list.actions.length > 0 ? { list, picks } : undefined;
}

// buttonText is what an action's button says for n items: code's words.
export function buttonText(a: PickAction, n: number): string {
  return n === 1 ? a.one : a.many.replace("{n}", String(n));
}

// itemLine is an item as the confirm names it: its title · its detail.
export function itemLine(it: PickItem): string {
  const title = it.title || it.id;
  return it.detail ? `${title} · ${it.detail}` : title;
}

type Result = { ok: boolean; text: string };

// PickList draws a List's rows (children, in the source's order: row i is
// the item whose n is i) with their boxes, and the bar.
export function Pickable({ surface, list, picks, rows, direction }: { surface: string; list: PickList; picks: Picks; rows: ReactNode[]; direction: string }) {
  const { t } = useWords();
  const key = `${surface}\u0000${list.component}`;
  const [picked, setPicked] = useState<string[]>(() => picks.kept.get(key)?.picked ?? []);
  const [asking, setAsking] = useState<PickAction | null>(null);
  const [running, setRunning] = useState(false);
  const [result, setResult] = useState<Result | null>(() => picks.kept.get(key)?.result ?? null);
  useEffect(() => void picks.kept.set(key, { picked, result }), [picks.kept, key, picked, result]);
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
  const run = async (a: PickAction) => {
    setAsking(null);
    setRunning(true);
    const r = await picks.run(surface, list, a, picked).catch(() => ({ ok: false, text: t("espace.act.error") }));
    // Kept at once: the page may have drawn the list again meanwhile.
    picks.kept.set(key, { picked: r.ok ? [] : picked, result: r });
    setRunning(false);
    setResult(r);
    if (r.ok) setPicked([]);
  };
  const press = (a: PickAction) => (a.confirm ? setAsking(a) : void run(a));
  const chosen = list.items.filter((it) => picked.includes(it.id));

  return (
    <div className={`a2-picks${picked.length ? " picking" : ""}`}>
      {picked.length > 0 && (
        <div className="a2-pickbar" role="toolbar" aria-label={t("espace.act.bar")}>
          <span className="a2-pickcount">{t("espace.act.picked", picked.length)}</span>
          {list.actions.map((a) => (
            <button key={a.name} type="button" className="a2-pickact" disabled={running} onClick={() => press(a)} data-action={a.name}>
              {buttonText(a, picked.length)}
              {a.hint && <small className="a2-pickhint">{a.hint}</small>}
            </button>
          ))}
        </div>
      )}
      {asking && (
        <div className="a2-pickask" role="alertdialog" aria-label={buttonText(asking, picked.length)}>
          <p>{t("espace.act.ask")}</p>
          <dl>
            <dt>{asking.tool}</dt>
            {(asking.args ?? []).map((l) => (
              <dd key={l}>{l}</dd>
            ))}
          </dl>
          <ul>
            {chosen.map((it) => (
              <li key={it.id}>{itemLine(it)}</li>
            ))}
          </ul>
          <span className="a2-pickask-btns">
            <button type="button" className="btn" onClick={() => setAsking(null)}>
              {t("espace.act.cancel")}
            </button>
            <button type="button" className="btn primary" onClick={() => void run(asking)}>
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
      <label className="a2-pickall">
        <input type="checkbox" className="a2-pick" checked={all} onChange={pickAll} />
        <span>{all ? t("espace.act.clear") : t("espace.act.all")}</span>
      </label>
      <ul className={`a2-list a2-list-${direction} a2-pickable`}>
        {rows.map((row, i) => {
          const it = byN.get(i);
          const on = !!it && picked.includes(it.id);
          return (
            <li key={i} className={on ? "picked" : undefined}>
              {it && (
                <input type="checkbox" className="a2-pick" checked={on} onChange={() => toggle(it.id)} aria-label={t("espace.act.pick", it.title || it.id)} />
              )}
              <div className="a2-pickrow">{row}</div>
            </li>
          );
        })}
      </ul>
    </div>
  );
}
