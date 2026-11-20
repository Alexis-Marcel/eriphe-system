import type { GraphNode, OrbFeed, OrbWords } from "../data/api";
import type { Sent } from "../data/store";

// Eriphe's orb (web_orb.go): what it shows, and where, as pure functions
// tested on their own (model.test.ts); Orb.tsx draws it in the graph,
// Line.tsx on the phone.
//
// One orb, one thing at a time, by what matters most:
//   1. work: Eriphe is at work (or the user just wrote): beside the
//      subject it works on, else at the home; it shows it works, and the
//      first line of what it writes;
//   2. failed: it could not answer: where the turn belonged, with « Try
//      again », until the user writes again or closes it;
//   3. wait: a card waits for the user's yes in a subject: beside it,
//      pointing at the card, until it is answered or closed;
//   4. answer: its answer to the user's last message: at the home, an
//      answer of more than a line, whole, held until the user moves on
//      (writes again, opens a subject, closes it); beside a subject, a few
//      lines for a while (its page says the rest);
//   5. said: its later words that answer no message of the user's (an
//      event's), for a while: they wait behind what comes first, never
//      replace it; then a subject that began to wait for the user unasked
//      (a monitor's mail, tools to review): « X t'attend »;
//   6. rest: in the bar.
// What it showed for its while, or the user closed, is not shown again
// (Closed, kept in the browser).

export type Mode = "rest" | "work" | "failed" | "wait" | "answer" | "said";

export type Show = {
  mode: Mode;
  node: string | null; // beside this node; null: the home
  key: string; // what is shown, to remember it
  words?: OrbWords; // Eriphe's words
  draft?: string; // the first line of what it writes
  card?: number; // the card waiting
  call?: string; // a node that began to wait: say it
  pinned?: boolean; // stays until the user moves on
};

// ANSWER_MS and SAID_MS are how long Eriphe's words stay beside a subject:
// an answer, an event's words.
export const ANSWER_MS = 15_000;
export const SAID_MS = 9_000;

// Closed is what the user closed or saw for its while: the home's answer
// (its seq), the words seen (their seq), the cards (their id), the
// failures (their seq). Seqs and ids only grow.
export type Closed = { answer: number; seen: number; wait: number; failed: number };

export type Mind = {
  now: number;
  sent: Sent | null;
  closed: Closed;
  met: (key: string) => number | undefined; // when it was first shown
  nodes: Record<string, GraphNode>;
  call: { id: string; n: number } | null; // a node that began to wait for the user, unasked
};

export function orbShow(x: OrbFeed | null, m: Mind): Show {
  const known = (id?: string | null) => (id && m.nodes[id] ? id : null);
  const fresh = (key: string, ms: number) => {
    const at = m.met(key);
    return at === undefined || m.now - at < ms;
  };
  if (m.sent || x?.work) {
    const at = x?.work ? x.work.node : m.sent?.to;
    return { mode: "work", node: known(at), key: "work", draft: x?.work?.draft || undefined };
  }
  if (x?.failed && x.failed.seq > m.closed.failed) return { mode: "failed", node: known(x.failed.node), key: `failed:${x.failed.seq}`, pinned: true };
  const a = x?.answer;
  const answerKey = a ? `answer:${a.seq}` : "";
  const answerFresh = !!a && a.seq > m.closed.seen && fresh(answerKey, ANSWER_MS);
  if (x?.wait && x.wait.card > m.closed.wait && known(x.wait.node)) {
    const words = a && answerFresh && known(a.node) === x.wait.node ? a : undefined;
    return { mode: "wait", node: x.wait.node, key: `wait:${x.wait.card}`, card: x.wait.card, words, pinned: true };
  }
  if (a && a.whole && !a.node) {
    if (a.seq > m.closed.answer) return { mode: "answer", node: null, key: answerKey, words: a, pinned: true };
  } else if (a && answerFresh) {
    return { mode: "answer", node: known(a.node), key: answerKey, words: a };
  }
  const s = x?.said;
  if (s && s.seq > m.closed.seen && (!a || s.seq > a.seq)) {
    const key = `said:${s.seq}`;
    if (fresh(key, SAID_MS)) return { mode: "said", node: known(s.node), key, words: s };
  }
  if (m.call && known(m.call.id)) {
    const key = `call:${m.call.id}:${m.call.n}`;
    if (fresh(key, SAID_MS)) return { mode: "said", node: m.call.id, key, call: m.call.id };
  }
  return { mode: "rest", node: null, key: "rest" };
}

// expires is how long the shown thing stays from when it was first shown;
// null: until the user moves on.
export function expires(s: Show): number | null {
  if (s.pinned || s.mode === "work" || s.mode === "rest") return null;
  return s.mode === "answer" ? ANSWER_MS : SAID_MS;
}

// close is what the user closing it (or its while over) remembers.
export function close(c: Closed, s: Show): Closed {
  switch (s.mode) {
    case "failed":
      return { ...c, failed: Math.max(c.failed, Number(s.key.split(":")[1])) };
    case "wait":
      return { ...c, wait: Math.max(c.wait, s.card ?? 0) };
    case "answer":
      return s.pinned ? { ...c, answer: Math.max(c.answer, s.words!.seq) } : { ...c, seen: Math.max(c.seen, s.words!.seq) };
    case "said":
      return s.words ? { ...c, seen: Math.max(c.seen, s.words.seq) } : c;
  }
  return c;
}

// The closed things, kept in the browser so that a reload does not bring
// them back.
const CLOSED = "eriphe.espace.orb.closed";

export function loadClosed(): Closed {
  const none = { answer: 0, seen: 0, wait: 0, failed: 0 };
  try {
    const v = JSON.parse(localStorage.getItem(CLOSED) ?? "{}");
    return { ...none, ...Object.fromEntries(Object.entries(v).filter(([k, n]) => k in none && typeof n === "number")) };
  } catch {
    return none;
  }
}

export function saveClosed(c: Closed): void {
  try {
    localStorage.setItem(CLOSED, JSON.stringify(c));
  } catch {
    // no storage: closed for this page only
  }
}

/* ---------- where it goes ---------- */

// A rectangle on screen.
export type Rect = { l: number; t: number; r: number; b: number };

// ORB is the orb's diameter beside a subject; GAP, from the orb to its
// words; AWAY, from the subject; CLEAR, the room kept around what it must
// not cover.
export const ORB = 26;
export const GAP = 10;
export const AWAY = 14;
export const CLEAR = 6;

export type Side = "right" | "left" | "below" | "below-left" | "above" | "above-left" | "around";
type Beside = Exclude<Side, "around">;
const SIDES: Beside[] = ["right", "left", "below", "below-left", "above", "above-left"];

// Placed is where the orb is drawn: its centre, its words' box (none when
// it says nothing), the side taken; dir: its words open to the right (1)
// or to the left (-1), away from the subject.
export type Placed = { orb: [number, number]; words: Rect | null; side: Side | "home"; dir: 1 | -1 };

const hits = (a: Rect, b: Rect, pad = CLEAR) => a.l < b.r + pad && a.r > b.l - pad && a.t < b.b + pad && a.b > b.t - pad;
const area = (a: Rect, b: Rect) => Math.max(0, Math.min(a.r, b.r) - Math.max(a.l, b.l)) * Math.max(0, Math.min(a.b, b.b) - Math.max(a.t, b.t));
const inside = (a: Rect, s: Rect) => a.l >= s.l && a.r <= s.r && a.t >= s.t && a.b <= s.b;

// bodyAt is the orb and its words, the orb's corner at (x, y): its words
// to its right (dir 1) or its left (-1), their top a little above the
// orb's.
function bodyAt(x: number, y: number, [w, h]: [number, number], dir: 1 | -1): { box: Rect; placed: Placed } {
  const total = ORB + (w > 0 ? GAP + w : 0);
  const l = dir === 1 ? x : x - total;
  const orbX = dir === 1 ? l + ORB / 2 : l + total - ORB / 2;
  const orbY = y + ORB / 2;
  const top = Math.min(y, orbY - Math.min(h / 2, ORB / 2 + 6));
  const box = { l, t: top, r: l + total, b: Math.max(y + ORB, top + h) };
  const words = w > 0 ? { l: dir === 1 ? orbX + ORB / 2 + GAP : orbX - ORB / 2 - GAP - w, t: top, r: dir === 1 ? orbX + ORB / 2 + GAP + w : orbX - ORB / 2 - GAP, b: top + h } : null;
  return { box, placed: { orb: [orbX, orbY], words, side: "home", dir } };
}

// cost is how bad a box is: what it covers (most of all, the bar, pages
// and titles: the obstacles), off the screen.
function cost(box: Rect, obstacles: Rect[], screen: Rect): number {
  let c = 0;
  for (const o of obstacles) if (hits(box, o)) c += 1000 + area(box, o);
  if (!inside(box, screen)) c += 100_000;
  return c;
}

// placeBeside is where the orb goes beside a subject (target, on screen;
// anchor: the height it points at, its middle by default): the first side
// where it covers nothing and stays on screen, the side it had (prev)
// first, so that it does not hop while things move around it; when none
// is free, the free spot nearest around it; when there is none, the side
// that covers least, kept on the screen.
export function placeBeside(target: Rect, words: [number, number], obstacles: Rect[], screen: Rect, prev?: Side | "home", anchor?: number): Placed {
  const ay = Math.max(target.t, Math.min(target.b, anchor ?? target.t + Math.min(target.b - target.t, 2 * ORB) / 2));
  const cx = (target.l + target.r) / 2;
  const h = Math.max(ORB, words[1]);
  const at = (side: Beside) => {
    switch (side) {
      case "right":
        return bodyAt(target.r + AWAY, ay - ORB / 2, words, 1);
      case "left":
        return bodyAt(target.l - AWAY, ay - ORB / 2, words, -1);
      case "below":
        return bodyAt(cx - ORB / 2, target.b + AWAY, words, 1);
      case "below-left":
        return bodyAt(cx + ORB / 2, target.b + AWAY, words, -1);
      case "above":
        return bodyAt(cx - ORB / 2, target.t - AWAY - h, words, 1);
      case "above-left":
        return bodyAt(cx + ORB / 2, target.t - AWAY - h, words, -1);
    }
  };
  const order = prev && prev !== "home" && prev !== "around" ? [prev, ...SIDES.filter((s) => s !== prev)] : SIDES;
  let best: { box: Rect; placed: Placed; side: Beside } | null = null;
  let low = Infinity;
  for (const side of order) {
    const b = at(side);
    const c = cost(b.box, obstacles, screen);
    if (c === 0) return { ...b.placed, side };
    if (c < low) {
      low = c;
      best = { ...b, side };
    }
  }
  // No side free: the free spot nearest to it, around it, its words
  // opening away from it.
  const tx = (target.l + target.r) / 2, ty = (target.t + target.b) / 2;
  const far = (target.r - target.l) / 2 + AWAY, deep = (target.b - target.t) / 2 + AWAY;
  for (let r = 0; r < 600; r += 30) {
    for (let i = 0; i < 24; i++) {
      const a = (i / 24) * Math.PI * 2;
      const [x, y] = [tx + Math.cos(a) * (far + r), ty + Math.sin(a) * (deep + r)];
      const dir = Math.cos(a) < 0 ? -1 : 1;
      const b = bodyAt(dir === 1 ? x : x + ORB, y - ORB / 2, words, dir);
      if (hits(b.box, target, AWAY - CLEAR) || cost(b.box, obstacles, screen) > 0) continue;
      return { ...b.placed, side: "around" };
    }
  }
  // Nowhere free: the least bad, brought back on the screen.
  const { box, placed, side } = best!;
  const dx = Math.max(screen.l - box.l, Math.min(0, screen.r - box.r)), dy = Math.max(screen.t - box.t, Math.min(0, screen.b - box.b));
  return shift({ ...placed, side }, dx, dy);
}

const shift = (p: Placed, dx: number, dy: number): Placed => ({
  ...p,
  orb: [p.orb[0] + dx, p.orb[1] + dy],
  words: p.words && { l: p.words.l + dx, t: p.words.t + dy, r: p.words.r + dx, b: p.words.b + dy },
});

// placeHome is where the orb goes at the home: the free spot nearest the
// middle of the screen's free band (the one it had, prev, while it stays
// free).
export function placeHome(words: [number, number], obstacles: Rect[], screen: Rect, prev?: [number, number]): Placed {
  const total = ORB + (words[0] > 0 ? GAP + words[0] : 0);
  const h = Math.max(ORB, words[1]);
  const at = (cx: number, cy: number) => bodyAt(cx - total / 2, cy - h / 2, words, 1);
  if (prev) {
    const b = at(prev[0], prev[1]);
    if (cost(b.box, obstacles, screen) === 0) return b.placed;
  }
  const cx = (screen.l + screen.r) / 2, cy = (screen.t + screen.b) / 2;
  let best = at(cx, cy), low = cost(best.box, obstacles, screen);
  for (let r = 40; r < 1600 && low > 0; r += 40) {
    for (let i = 0; i < 16; i++) {
      const a = (i / 16) * Math.PI * 2;
      const b = at(cx + Math.cos(a) * r * 1.4, cy + Math.sin(a) * r);
      const c = cost(b.box, obstacles, screen);
      if (c < low) {
        low = c;
        best = b;
        if (c === 0) break;
      }
    }
  }
  return best.placed;
}

// homePoint is the point placeHome placed it from, to keep it there
// (placeHome's prev): placeHome(prev) gives back the same place.
export const homePoint = (p: Placed, words: [number, number]): [number, number] => {
  const total = ORB + (words[0] > 0 ? GAP + words[0] : 0);
  const l = p.dir === 1 ? p.orb[0] - ORB / 2 : p.orb[0] + ORB / 2 - total;
  return [l + total / 2, p.orb[1] - ORB / 2 + Math.max(ORB, words[1]) / 2];
};

// glide is the orb's next point on its way to `to`: a share of the way
// each frame; at once when the user asks for less motion.
export function glide(from: [number, number], to: [number, number], reduced: boolean): [number, number] {
  if (reduced) return to;
  const x = from[0] + (to[0] - from[0]) * 0.16, y = from[1] + (to[1] - from[1]) * 0.16;
  return Math.hypot(to[0] - x, to[1] - y) < 0.5 ? to : [x, y];
}
