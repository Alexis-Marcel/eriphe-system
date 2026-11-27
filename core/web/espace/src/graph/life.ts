// A big graph's sizes by life (lod.ts, from LOD.from nodes): a node's
// radius, its weight's (model.ts, radiusOf), times how alive it is now,
// worked out by code from what the node already says, never chosen by a
// model: what waits for the user the biggest, then what runs, what a skill
// keeps up, what changed lately (fading over about a week), what many
// nodes link to (by the log of its links); idle for weeks, smaller. And a
// node asleep (idle for days, nothing waiting, running or kept up, no
// figure to show) is a small pale dot, a bubble only very close.

export const DAY = 86_400_000;

// What the life of a node reads.
export type LifeOf = {
  status: "idle" | "running" | "waits" | "done";
  updated: string;
  maintained?: readonly string[];
  tile?: boolean; // its glance shown and not seen (glance.ts): a figure to look at
};

// The factors, each times the others; the whole held within MIN and MAX.
export const LIFE = {
  waits: 1.45,
  running: 1.25,
  maintained: 1.15,
  fresh: 0.3, // up to this much more when just changed, fading over FRESH days
  freshDays: 7,
  hub: 0.25, // up to this much more for the most linked (HUB links and more)
  hubLinks: 40,
  idle: 0.75, // down to this after weeks idle (from a week, over IDLE days)
  idleDays: 21,
  min: 0.6,
  max: 1.8,
};
// A node asleep is this much of its radius (its dot).
export const SLEEP_R = 0.5;

const age = (x: LifeOf, now: number) => {
  const t = Date.parse(x.updated);
  return t === t ? Math.max(0, (now - t) / DAY) : Infinity;
};

// alive tells whether something keeps a node awake whatever its age.
const alive = (x: LifeOf) => x.status === "waits" || x.status === "running" || !!x.maintained?.length || !!x.tile;

// lifeFactor is how much bigger (or smaller) a node is for its life, at
// now, its degree its links (parent and children included); strength 0
// gives 1, 1 the factors as they are, between and past them their power.
export function lifeFactor(x: LifeOf, degree: number, now: number, strength = 1): number {
  if (strength <= 0) return 1;
  const days = age(x, now);
  let f = 1;
  if (x.status === "waits") f *= LIFE.waits;
  else if (x.status === "running") f *= LIFE.running;
  if (x.maintained?.length) f *= LIFE.maintained;
  if (days !== Infinity) f *= 1 + LIFE.fresh * Math.exp(-days / LIFE.freshDays);
  f *= 1 + LIFE.hub * Math.min(1, Math.log1p(Math.max(0, degree)) / Math.log1p(LIFE.hubLinks));
  if (!alive(x)) {
    // idle: from a week on, down to LIFE.idle
    const idle = Math.max(0, days - 7);
    f *= LIFE.idle + (1 - LIFE.idle) * Math.exp(-idle / (LIFE.idleDays / 2));
  }
  f = Math.pow(f, strength);
  return Math.min(LIFE.max, Math.max(LIFE.min, f));
}

// asleep tells whether a node sleeps: nothing changed for days days, and
// nothing keeps it awake (it waits, runs, is kept up, shows a figure).
export function asleep(x: LifeOf, now: number, days: number): boolean {
  return !alive(x) && age(x, now) >= days;
}

// degrees is each node's links, its parent and children counting.
export function degrees(nodes: readonly { id: string; parent?: string; links?: readonly string[] }[]): Map<string, number> {
  const ids = new Set(nodes.map((x) => x.id));
  const d = new Map<string, number>();
  const seen = new Set<string>();
  const add = (a: string, b: string) => {
    if (a === b || !ids.has(a) || !ids.has(b)) return;
    const k = a < b ? `${a}\n${b}` : `${b}\n${a}`;
    if (seen.has(k)) return;
    seen.add(k);
    d.set(a, (d.get(a) ?? 0) + 1);
    d.set(b, (d.get(b) ?? 0) + 1);
  };
  for (const x of nodes) {
    if (x.parent) add(x.parent, x.id);
    for (const l of x.links ?? []) add(x.id, l);
  }
  return d;
}

// lifeRadius is a node's radius in a big graph: its radius r times its
// life (when on), a node asleep SLEEP_R of it (when on), rounded to half a
// unit, so that a little change of age does not move the layout.
export function lifeRadius(r: number, x: LifeOf, degree: number, now: number, o: { life: boolean; lifeStrength: number; sleep: boolean; sleepDays: number }): { r: number; sleeps: boolean } {
  const sleeps = o.sleep && asleep(x, now, o.sleepDays);
  let f = o.life ? lifeFactor(x, degree, now, o.lifeStrength) : 1;
  if (sleeps) f = Math.min(f, 1) * SLEEP_R;
  return { r: Math.round(r * f * 2) / 2, sleeps };
}
