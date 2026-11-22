import { create } from "zustand";
import type { Glance, GraphNode } from "../data/api";
import { closePage, openPage, step, store } from "../data/store";

// The node-glance prototype (proto/node-glance), with no eriphe-core
// behind: a node's glance is its reduced form, a small tile in place of
// its bubble, when what it watches moved past what its skill declared
// (Glance, api.ts). The figures here are made up and tick in the page,
// as a source's live values would come from eriphe-core's stream, with
// no brain woken.
//
// The rules the prototype shows:
// - a node is a tile while its variation is past its threshold, and the
//   user has not seen it (opened its subject) since it crossed;
// - seen, it is a circle again until the variation falls back under the
//   threshold and crosses it once more (re-armed);
// - a node that waits for the user stays a circle: a request wins over
//   information (the violet ring says it);
// - at most CAP tiles: the most important win (variation / threshold),
//   the others stay circles.

export const CAP = 3;

export type Style = "A" | "B" | "C";

// Live is a glance's figures as they tick: the variation shown (in the
// glance's unit) and the recent values, for the sparkline.
export type Live = { delta: number; trend?: number; series: number[] };

export type Scenario = "calme" | "arrive" | "retombe" | "quatre" | "vu";

export const SCENARIOS: { id: Scenario; label: string; hint: string }[] = [
  { id: "calme", label: "Calme", hint: "Tout est sous les seuils : que des cercles." },
  { id: "arrive", label: "Variation arrive", hint: "Le PEA monte et passe le seuil (2 %) : le cercle devient une tuile, la valeur continue de bouger." },
  { id: "retombe", label: "Variation retombe", hint: "Le PEA redescend sous le seuil : la tuile redevient un cercle." },
  { id: "quatre", label: "Trois alertes + une quatrième", hint: "Quatre variations passent leur seuil : les trois plus fortes (variation / seuil) sont des tuiles, la quatrième reste un cercle." },
  { id: "vu", label: "Vu", hint: "Le sujet Investissements s'ouvre (vu), puis se ferme : il est redevenu un cercle, tant que la variation ne retombe pas puis ne repasse pas le seuil." },
];

// The tile's size for each style, fixed (the figures use tabular digits):
// the layout gives it room once, not at each tick.
export const TILE: Record<Style, [number, number]> = { A: [148, 40], B: [148, 60], C: [148, 60] };

type Target = Record<string, number>;

// Each scenario's variations, where each glance goes (in its unit); the
// variation glides there and wanders a little around it.
const calm: Target = { invest: 0.6, prod: 6, energy: 4, tools: 1 };
const TARGETS: Record<Scenario, { from: Target; to: Target }> = {
  calme: { from: calm, to: calm },
  arrive: { from: calm, to: { ...calm, invest: 3.2 } },
  retombe: { from: { ...calm, invest: 3.4 }, to: { ...calm, invest: 0.9 } },
  quatre: { from: { invest: 3.2, prod: 64, energy: 21, tools: 4 }, to: { invest: 3.3, prod: 64, energy: 21, tools: 4 } },
  vu: { from: { ...calm, invest: 3.2 }, to: { ...calm, invest: 3.3 } },
};

type State = {
  style: Style;
  scenario: Scenario;
  live: Record<string, Live>;
  // seen: the glances seen since they last crossed their threshold
  seen: Record<string, boolean>;
  // tiles: the nodes drawn as tiles now, the most important first
  tiles: string[];
  // over: those past their threshold, unseen, not waiting: tiles but for the cap
  over: string[];
};

export const useGlance = create<State>(() => ({ style: "C", scenario: "calme", live: {}, seen: {}, tiles: [], over: [] }));

const glances = (): [GraphNode, Glance][] =>
  Object.values(store.getState().nodes).flatMap((x) => (x.glance ? [[x, x.glance] as [GraphNode, Glance]] : []));

// weightOf is how important a glance is now: how far past its threshold.
export const weightOf = (g: Glance, l: Live | undefined) => (l ? Math.abs(l.delta) / g.threshold : 0);

// pick is the rule: the nodes past their threshold, unseen, not waiting,
// the most important first; the first CAP are tiles. A tile keeps its
// place against a newcomer that is not clearly stronger (a fifth more),
// so two near figures do not swap at each tick.
export function pick(s: Pick<State, "live" | "seen" | "tiles">, list: [GraphNode, Glance][]): { tiles: string[]; over: string[] } {
  const over = list
    .filter(([x, g]) => !x.waits && x.state !== "waiting" && !s.seen[x.id] && weightOf(g, s.live[x.id]) >= 1)
    .map(([x, g]) => ({ id: x.id, w: weightOf(g, s.live[x.id]) * (s.tiles.includes(x.id) ? 1.2 : 1) }))
    .sort((a, b) => b.w - a.w || a.id.localeCompare(b.id))
    .map((o) => o.id);
  return { tiles: over.slice(0, CAP), over };
}

// base: each glance's made-up figure, for its sparkline.
export const BASE: Record<string, number> = { invest: 100, prod: 1.2, energy: 11, tools: 6 };

function seriesFor(id: string, delta: number, unit: Glance["unit"]): number[] {
  const b = BASE[id] ?? 10;
  const out: number[] = [];
  for (let i = 0; i < 24; i++) {
    const f = i / 23;
    // the day so far: flat a while, then the move, with a little wiggle
    const ramp = Math.min(1, Math.max(0, (f - 0.35) / 0.55)) ** 1.4;
    const end = unit === "pct" ? (b * delta) / 100 : delta;
    const wiggle = (Math.sin(i * 1.7 + id.length) + Math.sin(i * 0.9 + id.charCodeAt(0))) * 0.05 * (Math.abs(end) || b * 0.01);
    out.push(b + end * ramp + wiggle);
  }
  return out;
}

let timer: ReturnType<typeof setInterval> | null = null;
let script: ReturnType<typeof setTimeout>[] = [];
let tick = 0;

// play starts a scenario: what is open closes, nothing is seen, each
// glance starts at its scenario's figure, then glides toward its target.
export function play(sc: Scenario) {
  script.forEach(clearTimeout);
  script = [];
  for (const id of store.getState().open) step(store, closePage, id);
  const t = TARGETS[sc];
  const live: Record<string, Live> = {};
  for (const [x, g] of glances()) {
    const d = t.from[x.id] ?? 0;
    live[x.id] = { delta: d, series: seriesFor(x.id, d, g.unit) };
  }
  useGlance.setState({ scenario: sc, live, seen: {}, tiles: [], over: [] });
  refresh();
  // « vu »: the user opens the subject, then closes it a moment later
  if (sc === "vu") {
    script.push(setTimeout(() => step(store, openPage, "invest", false), 2200));
    script.push(setTimeout(() => step(store, closePage, "invest"), 7000));
  }
}

function refresh() {
  const s = useGlance.getState();
  const { tiles, over } = pick(s, glances());
  if (tiles.join() !== s.tiles.join() || over.join() !== s.over.join()) useGlance.setState({ tiles, over });
}

// step1 moves each figure a tick: toward its target, a little noise, the
// sparkline taking the new value. A glance seen is re-armed once its
// variation falls back under its threshold.
function step1() {
  tick++;
  const s = useGlance.getState();
  const t = TARGETS[s.scenario].to;
  const live = { ...s.live };
  const seen = { ...s.seen };
  for (const [x, g] of glances()) {
    const l = live[x.id];
    if (!l) {
      // a glance the graph had not yet when the scenario began
      const d0 = TARGETS[s.scenario].from[x.id] ?? 0;
      live[x.id] = { delta: d0, series: seriesFor(x.id, d0, g.unit) };
      continue;
    }
    const goal = t[x.id] ?? 0;
    const noise = (Math.sin(tick * 1.3 + x.id.length * 2.1) + Math.sin(tick * 0.71 + x.id.charCodeAt(0))) * g.threshold * 0.05;
    // the trend glides to the goal; the figure shown wanders around it
    const trend = (l.trend ?? l.delta) + (goal - (l.trend ?? l.delta)) * 0.16;
    let d = trend + noise;
    if (g.unit === "count") d = Math.round(l.delta + (goal - l.delta) * 0.35);
    const b = BASE[x.id] ?? 10;
    const v = g.unit === "pct" ? b * (1 + d / 100) : b + d;
    // the sparkline is the day so far: a new point every few ticks, the last one live
    const series = tick % 4 === 0 ? [...l.series.slice(1), v] : [...l.series.slice(0, -1), v];
    live[x.id] = { delta: d, trend, series };
    if (seen[x.id] && Math.abs(d) < g.threshold) delete seen[x.id];
  }
  useGlance.setState({ live, seen });
  refresh();
}

// start runs the prototype: the ticker, and « seen » when a subject opens.
export function start(): () => void {
  play("calme");
  timer = setInterval(step1, 900);
  let open = store.getState().open;
  const off = store.subscribe((st) => {
    if (st.open === open) return;
    const born = st.open.filter((id) => !open.includes(id));
    open = st.open;
    if (!born.length) return;
    const s = useGlance.getState();
    const seen = { ...s.seen };
    for (const id of born) if (s.over.includes(id)) seen[id] = true;
    useGlance.setState({ seen });
    refresh();
  });
  return () => {
    if (timer) clearInterval(timer);
    script.forEach(clearTimeout);
    off();
  };
}

// fmt writes a variation the French way: « ▲ 3,2 % », « ▼ 12 % », « ▲ 4 ».
export function fmt(g: Glance, d: number): { arrow: string; text: string } {
  const arrow = d >= 0 ? "▲" : "▼";
  const a = Math.abs(d);
  if (g.unit === "count") return { arrow, text: String(Math.round(a)) };
  const n = a.toLocaleString("fr-FR", { minimumFractionDigits: a < 10 ? 1 : 0, maximumFractionDigits: a < 10 ? 1 : 0 });
  return { arrow, text: `${n} %` };
}
