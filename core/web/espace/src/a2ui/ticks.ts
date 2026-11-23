import { useSyncExternalStore } from "react";
import type { ListRow, SourceList, TickAction } from "../data/api";

// Prototype « Eriphe coche, tu cliques » (proto/eriphe-coche, PROTO.md).
//
// When Eriphe proposes an action on items of a view, no card waits: the
// rows come pre-ticked (ListRow.tick: the action and a short reason, code's)
// and the page's bar shows one button per action, with its count and
// accounts. The user unticks what stays and clicks: one click per action,
// one call per account behind it.
//
// What the user changed (rows unticked, an action changed on a row) is
// kept here, by node and item, outside the page's surfaces: a page read
// again rebuilds them, and the choices must not go with them.

export type Call = { account: string; ids: string[] };
// A runner does an action's calls (eriphe-core would run one call per
// account) and says how many items went; the dev server's core gives it.
export type Runner = (node: string, action: string, calls: Call[]) => Promise<number>;

type NodeTicks = { left: ReadonlySet<string>; moved: Readonly<Record<string, string>>; result?: { text: string; n: number }; busy?: string };
type State = Readonly<Record<string, NodeTicks>>;

let state: State = {};
const subs = new Set<() => void>();
const set = (node: string, f: (t: NodeTicks) => NodeTicks) => {
  state = { ...state, [node]: f(state[node] ?? { left: new Set(), moved: {} }) };
  subs.forEach((s) => s());
};
const subscribe = (f: () => void) => {
  subs.add(f);
  return () => subs.delete(f);
};
const EMPTY: NodeTicks = { left: new Set(), moved: {} };

let runner: Runner | null = null;
export function setTickRunner(r: Runner | null) {
  runner = r;
}

// resetTicks forgets what the user changed (the dev panel's « recommencer »).
export function resetTicks() {
  state = {};
  subs.forEach((s) => s());
}

export function useTicks(node: string | undefined): NodeTicks {
  return useSyncExternalStore(subscribe, () => (node ? (state[node] ?? EMPTY) : EMPTY));
}

export function toggleTick(node: string, id: string) {
  set(node, (t) => {
    const left = new Set(t.left);
    if (left.has(id)) left.delete(id);
    else left.add(id);
    return { ...t, left };
  });
}

// changeAction moves a row to the List's next action (corbeille → archiver).
export function changeAction(node: string, id: string, actions: string[], now: string) {
  const next = actions[(actions.indexOf(now) + 1) % actions.length];
  set(node, (t) => ({ ...t, moved: { ...t.moved, [id]: next } }));
}

// A ticked row as the bar counts it.
export type Ticked = { id: string; action: string; account: string; left: boolean };

// tickedRows are the rows of a page's Lists Eriphe ticked, once each, with
// the action the user left on them; and the actions the Lists name.
export function tickedRows(lists: SourceList[], t: NodeTicks): { rows: Ticked[]; actions: Record<string, TickAction> } {
  const seen = new Set<string>();
  const rows: Ticked[] = [];
  const actions: Record<string, TickAction> = {};
  for (const l of lists) {
    Object.assign(actions, l.actions ?? {});
    for (const it of l.items) {
      if (!it.tick || seen.has(it.id)) continue;
      seen.add(it.id);
      rows.push({ id: it.id, action: t.moved[it.id] ?? it.tick.action, account: it.tick.account ?? "", left: t.left.has(it.id) });
    }
  }
  return { rows, actions };
}

export const actionOf = (it: ListRow, t: NodeTicks) => t.moved[it.id] ?? it.tick?.action ?? "";

// runAction runs one action over its ticked rows: one call per account.
export async function runAction(node: string, action: string, rows: Ticked[], done: TickAction | undefined) {
  if (!runner || state[node]?.busy) return;
  const going = rows.filter((r) => r.action === action && !r.left);
  if (!going.length) return;
  const byAccount = new Map<string, string[]>();
  for (const r of going) byAccount.set(r.account, [...(byAccount.get(r.account) ?? []), r.id]);
  const calls = [...byAccount].map(([account, ids]) => ({ account, ids }));
  set(node, (t) => ({ ...t, busy: action }));
  const n = await runner(node, action, calls).catch(() => -1);
  set(node, (t) => {
    const moved = { ...t.moved };
    for (const r of going) delete moved[r.id];
    return { ...t, moved, busy: undefined, result: n < 0 ? { text: "", n: -1 } : { text: (done?.done ?? "{n}").replace("{n}", String(n)), n } };
  });
}
