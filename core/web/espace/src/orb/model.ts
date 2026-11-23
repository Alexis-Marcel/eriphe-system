import type { GraphNode, OrbClosed, OrbFeed, OrbWords } from "../data/api";
import type { Sent } from "../data/store";

// Eriphe's orb (web_orb.go): what it shows, and where, as pure functions
// tested on their own (model.test.ts); Orb.tsx draws it in the graph,
// Line.tsx on the phone.
//
// The orb only answers: it leaves the bar in reply to what the user said
// to it, never unasked. What Eriphe does or says on its own (an event's
// words, a subject that began to wait) is the graph's to show (a node's
// ring), not the orb's; eriphe-core sends it only what answers the user.
//
// One orb, one thing at a time, by what matters most:
//   1. work: Eriphe is at work on the user's message (or the user just
//      wrote): beside the subject it works on, else at the home; it shows
//      it works, and the first line of what it writes;
//   2. failed: it could not answer: where the turn belonged, with « Try
//      again », until the user writes again or closes it;
//   3. wait: its answer raised a card that waits for the user's yes in a
//      subject: beside it, pointing at the card, until it is answered or
//      closed;
//   4. answer: its answer to the user's last message: at the home, an
//      answer of more than a line, whole, held until the user moves on
//      (writes again, opens a subject, closes it); beside a subject, a few
//      lines (« Voir plus » unfolds all of it, which then stays until the
//      user moves on), until something else happens: newer
//      words replace them, or the user moves on (opens another subject,
//      writes, closes them); else they go after WORDS_MS of the page being
//      seen;
//   5. rest: in the bar.
// What the user closed, or saw for its while, is not shown again (Closed,
// kept by eriphe-core with the view).

export type Mode = "rest" | "work" | "failed" | "wait" | "answer";

export type Show = {
  mode: Mode;
  node: string | null; // beside this node; null: the home
  key: string; // what is shown, to remember it
  words?: OrbWords; // Eriphe's words
  draft?: string; // the first line of what it writes
  card?: number; // the card waiting
  pinned?: boolean; // stays until the user moves on
  full?: boolean; // its words unfolded whole (« Voir plus »): they stay until the user moves on
};

// WORDS_MS is how long Eriphe's words stay beside a subject when nothing
// else happens, counted while the page is seen (useOrb's clock).
export const WORDS_MS = 120_000;

// Closed is what the user closed or saw for its while: the home's answer
// (its seq), the words seen (their seq), the cards (their id), the
// failures (their seq). Seqs and ids only grow. eriphe-core keeps it with
// what is open (the view: data/keep.ts), on any of the user's screens.
export type Closed = OrbClosed;

export type Mind = {
  now: number; // the orb's clock: how long the page was seen

  sent: Sent | null;
  closed: Closed;
  met: (key: string) => number | undefined; // when it was first shown, on that clock
  nodes: Record<string, GraphNode>;
};

// wordsKey is the key of Eriphe's words, by their message alone.
const wordsKey = (seq: number) => `words:${seq}`;

export function orbShow(x: OrbFeed | null, m: Mind): Show {
  const known = (id?: string | null) => (id && m.nodes[id] ? id : null);
  const fresh = (key: string) => {
    const at = m.met(key);
    return at === undefined || m.now - at < WORDS_MS;
  };
  if (m.sent || x?.work) {
    const at = x?.work ? x.work.node : m.sent?.to;
    return { mode: "work", node: known(at), key: "work", draft: x?.work?.draft || undefined };
  }
  if (x?.failed && x.failed.seq > m.closed.failed) return { mode: "failed", node: known(x.failed.node), key: `failed:${x.failed.seq}`, pinned: true };
  const a = x?.answer;
  const answerKey = a ? wordsKey(a.seq) : "";
  const answerFresh = !!a && a.seq > m.closed.seen && fresh(answerKey);
  if (x?.wait && x.wait.card > m.closed.wait && known(x.wait.node)) {
    const words = a && answerFresh && known(a.node) === x.wait.node ? a : undefined;
    return { mode: "wait", node: x.wait.node, key: `wait:${x.wait.card}`, card: x.wait.card, words, pinned: true };
  }
  const home = !!a && !!a.whole && !a.node;
  if (a && home && a.seq > m.closed.answer) return { mode: "answer", node: null, key: answerKey, words: a, pinned: true };
  // Beside a subject, its answer, a few lines.
  if (a && !home && answerFresh) return { mode: "answer", node: known(a.node), key: answerKey, words: a };
  return { mode: "rest", node: null, key: "rest" };
}

// expires is how long the shown thing stays from when it was first shown;
// null: until the user moves on.
export function expires(s: Show): number | null {
  return s.pinned || s.full || s.mode === "work" || s.mode === "rest" ? null : WORDS_MS;
}

// words tells whether it shows Eriphe's words beside a subject: those
// that stay until something else happens (not the pinned ones).
export const words = (s: Show) => s.mode === "answer" && !s.pinned;

// replaced tells whether the words shown (was) are over now that
// something else shows (now): newer words, or Eriphe at work (the user
// wrote). A card or a failure only comes before them: they come back after.
export const replaced = (was: Show, now: Show) => words(was) && was.key !== now.key && (now.mode === "work" || words(now));

// leaves tells whether the bar talking to another page (talk) moves on
// from the words shown: words about another subject. Words about the
// subject just opened (by the user, or by Eriphe for what was asked at the
// home) stay beside its page.
export const leaves = (s: Show, talk: string | null) => words(s) && s.node !== talk;

// close is what the user closing it (or its while over) remembers.
export function close(c: Closed, s: Show): Closed {
  switch (s.mode) {
    case "failed":
      return { ...c, failed: Math.max(c.failed, Number(s.key.split(":")[1])) };
    case "wait":
      return { ...c, wait: Math.max(c.wait, s.card ?? 0) };
    case "answer":
      return s.pinned ? { ...c, answer: Math.max(c.answer, s.words!.seq) } : { ...c, seen: Math.max(c.seen, s.words!.seq) };
  }
  return c;
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
// NEAR is how much further than a side the orb may go around its subject
// to find a free spot: beyond, it would no longer be beside it.
export const NEAR = 120;

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
// is free, the free spot nearest around it; null when there is none: the
// orb never covers a title, a page or the bar, it stays in the bar.
export function placeBeside(target: Rect, words: [number, number], obstacles: Rect[], screen: Rect, prev?: Side | "home", anchor?: number): Placed | null {
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
  for (const side of order) {
    const b = at(side);
    if (cost(b.box, obstacles, screen) === 0) return { ...b.placed, side };
  }
  // No side free: the free spot nearest to it, around it (NEAR at most), its words
  // opening away from it.
  const tx = (target.l + target.r) / 2, ty = (target.t + target.b) / 2;
  const far = (target.r - target.l) / 2 + AWAY, deep = (target.b - target.t) / 2 + AWAY;
  for (let r = 0; r <= NEAR; r += 20) {
    for (let i = 0; i < 24; i++) {
      const a = (i / 24) * Math.PI * 2;
      const [x, y] = [tx + Math.cos(a) * (far + r), ty + Math.sin(a) * (deep + r)];
      const dir = Math.cos(a) < 0 ? -1 : 1;
      const b = bodyAt(dir === 1 ? x : x + ORB, y - ORB / 2, words, dir);
      if (hits(b.box, target, AWAY - CLEAR) || cost(b.box, obstacles, screen) > 0) continue;
      return { ...b.placed, side: "around" };
    }
  }
  return null;
}

// keepOnScreen keeps the orb's words whole on screen, whatever placed them
// and however wide they turned out (measured late, grown since): words
// that would leave the screen on their side open on the orb's other side
// when they fit there and cover nothing there (avoid: the subject, the
// other bodies, the bar), else slide in, a margin from the edge (screen is
// the screen less its margins); never higher or lower than it either.
export function keepOnScreen(p: Placed, screen: Rect, avoid: Rect[] = []): Placed {
  const w0 = p.words;
  if (!w0) return p;
  const w = w0.r - w0.l, h = w0.b - w0.t;
  const fits = (l: number) => l >= screen.l && l + w <= screen.r;
  const free = (l: number) => !avoid.some((o) => hits({ l, t: w0.t, r: l + w, b: w0.b }, o, 0));
  let l = w0.l, dir = p.dir;
  if (!fits(l)) {
    const other = dir === 1 ? p.orb[0] - ORB / 2 - GAP - w : p.orb[0] + ORB / 2 + GAP;
    if (fits(other) && free(other)) {
      l = other;
      dir = dir === 1 ? -1 : 1;
    } else {
      l = Math.max(screen.l, Math.min(l, screen.r - w));
    }
  }
  const t = Math.max(screen.t, Math.min(w0.t, screen.b - h));
  if (l === w0.l && t === w0.t) return p;
  return { ...p, words: { l, t, r: l + w, b: t + h }, dir };
}

// placeHome is where the orb goes at the home: the free spot nearest the
// middle of the screen's free band (the one it had, prev, while it stays
// free); null when there is none.
export function placeHome(words: [number, number], obstacles: Rect[], screen: Rect, prev?: [number, number]): Placed | null {
  const total = ORB + (words[0] > 0 ? GAP + words[0] : 0);
  const h = Math.max(ORB, words[1]);
  const at = (cx: number, cy: number) => bodyAt(cx - total / 2, cy - h / 2, words, 1);
  if (prev) {
    const b = at(prev[0], prev[1]);
    if (cost(b.box, obstacles, screen) === 0) return b.placed;
  }
  const cx = (screen.l + screen.r) / 2, cy = (screen.t + screen.b) / 2;
  const first = at(cx, cy);
  if (cost(first.box, obstacles, screen) === 0) return first.placed;
  for (let r = 40; r < 1600; r += 40) {
    for (let i = 0; i < 16; i++) {
      const a = (i / 16) * Math.PI * 2;
      const b = at(cx + Math.cos(a) * r * 1.4, cy + Math.sin(a) * r);
      if (cost(b.box, obstacles, screen) === 0) return b.placed;
    }
  }
  return null;
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
