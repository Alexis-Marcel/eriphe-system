import { describe, expect, it } from "vitest";
import type { GraphNode, OrbFeed } from "../data/api";
import { ANSWER_MS, close, expires, glide, homePoint, ORB, orbShow, placeBeside, placeHome, SAID_MS, type Closed, type Mind, type Rect } from "./model";

const node = (id: string, more: Partial<GraphNode> = {}): GraphNode => ({
  id, title: id.toUpperCase(), lifetime: "work", state: "idle", created: "2026-11-18T10:00:00Z", updated: "2026-11-18T10:00:00Z", version: 1, ...more,
});
const nodes = { sub: node("sub"), mail: node("mail"), day: node("day") };
const none: Closed = { answer: 0, seen: 0, wait: 0, failed: 0 };
const mind = (more: Partial<Mind> = {}): Mind => ({ now: 1_000_000, sent: null, closed: none, met: () => undefined, nodes, call: null, ...more });

describe("what the orb shows", () => {
  it("rests in the bar with nothing to say", () => {
    expect(orbShow(null, mind()).mode).toBe("rest");
    expect(orbShow({ asked: 3 }, mind()).mode).toBe("rest");
  });

  it("answers without a subject at the home, held until closed", () => {
    const x: OrbFeed = { asked: 4, answer: { seq: 5, text: "Oui, **Seerr**.\n\nChaque recherche te demande ton accord.", whole: true } };
    const s = orbShow(x, mind());
    expect(s).toMatchObject({ mode: "answer", node: null, pinned: true });
    expect(expires(s)).toBeNull();
    // long after, still there
    expect(orbShow(x, mind({ met: () => 0, now: 10 * ANSWER_MS })).mode).toBe("answer");
    // closed: gone, for good
    const c = close(none, s);
    expect(c.answer).toBe(5);
    expect(orbShow(x, mind({ closed: c })).mode).toBe("rest");
  });

  it("goes beside the subject its answer belongs to, for a while", () => {
    const x: OrbFeed = { asked: 4, answer: { seq: 5, node: "sub", text: "J'ai ouvert le sujet.\nLe plan est écrit.", more: true } };
    const s = orbShow(x, mind());
    expect(s).toMatchObject({ mode: "answer", node: "sub" });
    expect(s.pinned).toBeFalsy();
    expect(expires(s)).toBe(ANSWER_MS);
    expect(orbShow(x, mind({ met: () => mind().now - ANSWER_MS + 10 })).mode).toBe("answer");
    expect(orbShow(x, mind({ met: () => mind().now - ANSWER_MS })).mode).toBe("rest");
    // its while over, it is seen: a reload does not bring it back
    expect(orbShow(x, mind({ closed: close(none, s) })).mode).toBe("rest");
  });

  it("speaks at the home when its subject is not in the graph", () => {
    expect(orbShow({ answer: { seq: 5, node: "gone", text: "C'est fait." } }, mind())).toMatchObject({ mode: "answer", node: null });
  });

  it("works beside the subject it works on, at once when the user writes", () => {
    expect(orbShow({ work: { node: "sub", draft: "Je regarde" } }, mind())).toMatchObject({ mode: "work", node: "sub", draft: "Je regarde" });
    expect(orbShow({ work: {} }, mind())).toMatchObject({ mode: "work", node: null });
    // sent, not yet landed: working where the user wrote
    const s = orbShow({ asked: 2, answer: { seq: 3, text: "x\n\ny", whole: true } }, mind({ sent: { text: "Et alors ?", after: 2, to: "mail" } }));
    expect(s).toMatchObject({ mode: "work", node: "mail" });
    expect(expires(s)).toBeNull();
  });

  it("says it could not answer, where it was, until closed or asked again", () => {
    const x: OrbFeed = { asked: 6, failed: { seq: 7, node: "sub" } };
    const s = orbShow(x, mind());
    expect(s).toMatchObject({ mode: "failed", node: "sub", pinned: true });
    expect(orbShow(x, mind({ closed: close(none, s) })).mode).toBe("rest");
  });

  it("points at the card that waits for the user's yes, with its answer when it is about it", () => {
    const x: OrbFeed = { asked: 8, answer: { seq: 9, node: "mail", text: "Tu me donnes ton accord ?" }, wait: { card: 3, node: "mail" } };
    const s = orbShow(x, mind());
    expect(s).toMatchObject({ mode: "wait", node: "mail", card: 3, pinned: true });
    expect(s.words?.text).toBe("Tu me donnes ton accord ?");
    expect(orbShow({ wait: { card: 3, node: "mail" } }, mind()).words).toBeUndefined();
    // closed: the next thing shows, here the answer
    expect(orbShow(x, mind({ closed: close(none, s) }))).toMatchObject({ mode: "answer", node: "mail" });
    // a card in a node the graph does not have: nothing to point at
    expect(orbShow({ wait: { card: 4, node: "gone" } }, mind()).mode).toBe("rest");
  });

  it("announces an event's words for a while, behind what comes first, never in its place", () => {
    const home: OrbFeed = { asked: 4, answer: { seq: 5, text: "Oui.\n\nEt plus.", whole: true }, said: { seq: 6, node: "day", text: "Un outil de plus : films." } };
    // the pinned answer first: the words wait
    expect(orbShow(home, mind()).mode).toBe("answer");
    // closed, they show, beside their subject, for a while
    const c = close(none, orbShow(home, mind()));
    const s = orbShow(home, mind({ closed: c }));
    expect(s).toMatchObject({ mode: "said", node: "day" });
    expect(s.words?.text).toBe("Un outil de plus : films.");
    expect(expires(s)).toBe(SAID_MS);
    expect(orbShow(home, mind({ closed: c, met: () => mind().now - SAID_MS })).mode).toBe("rest");
    // words older than the answer are not news
    expect(orbShow({ answer: { seq: 9, node: "sub", text: "Fait." }, said: { seq: 8, text: "avant" } }, mind({ closed: { ...none, seen: 9 } })).mode).toBe("rest");
  });

  it("says a subject began to wait for the user, unasked", () => {
    const s = orbShow({}, mind({ call: { id: "mail", n: 1 } }));
    expect(s).toMatchObject({ mode: "said", node: "mail", call: "mail" });
    expect(orbShow({}, mind({ call: { id: "mail", n: 1 }, met: () => mind().now - SAID_MS })).mode).toBe("rest");
    // the user's work comes first
    expect(orbShow({ work: { node: "sub" } }, mind({ call: { id: "mail", n: 1 } })).mode).toBe("work");
  });
});

describe("where the orb goes", () => {
  const screen: Rect = { l: 8, t: 60, r: 1432, b: 892 };
  const bar: Rect = { l: 410, t: 820, r: 1030, b: 880 };
  const words: [number, number] = [240, 44];
  const box = (p: { orb: [number, number]; words: Rect | null }): Rect => {
    const o = { l: p.orb[0] - ORB / 2, t: p.orb[1] - ORB / 2, r: p.orb[0] + ORB / 2, b: p.orb[1] + ORB / 2 };
    return p.words ? { l: Math.min(o.l, p.words.l), t: Math.min(o.t, p.words.t), r: Math.max(o.r, p.words.r), b: Math.max(o.b, p.words.b) } : o;
  };
  const overlaps = (a: Rect, b: Rect) => a.l < b.r && a.r > b.l && a.t < b.b && a.b > b.t;

  it("takes the free side beside its subject, its words away from it", () => {
    const sub: Rect = { l: 600, t: 300, r: 700, b: 400 }; // a bubble and its title under it
    const p = placeBeside(sub, words, [sub, bar], screen);
    expect(p.side).toBe("right");
    expect(p.dir).toBe(1);
    expect(p.orb[0]).toBeGreaterThan(sub.r);
    expect(overlaps(box(p), sub)).toBe(false);
    // a page on its right: it goes left, its words opening to the left
    const page: Rect = { l: 720, t: 200, r: 1300, b: 700 };
    const q = placeBeside(sub, words, [sub, page, bar], screen);
    expect(q.side).toBe("left");
    expect(q.dir).toBe(-1);
    for (const o of [sub, page, bar]) expect(overlaps(box(q), o)).toBe(false);
  });

  it("never covers a title, a page or the bar when a side is free", () => {
    const sub: Rect = { l: 600, t: 600, r: 700, b: 760 };
    const left: Rect = { l: 250, t: 600, r: 580, b: 800 }; // pages on both sides, the bar under it
    const right: Rect = { l: 720, t: 600, r: 1100, b: 800 };
    const title: Rect = { l: 560, t: 470, r: 900, b: 520 }; // a title above
    const p = placeBeside(sub, words, [sub, left, right, title, bar], screen);
    for (const o of [sub, left, right, title, bar]) expect(overlaps(box(p), o)).toBe(false);
    expect(box(p).t).toBeGreaterThanOrEqual(screen.t);
  });

  it("stays where it was put at the home, frame after frame", () => {
    for (const w of [[240, 44], [360, 57], [0, 0]] as [number, number][]) {
      const first = placeHome(w, [bar], screen);
      let p = first;
      for (let i = 0; i < 50; i++) p = placeHome(w, [bar], screen, homePoint(p, w));
      expect(p.orb).toEqual(first.orb);
    }
  });

  it("finds the free spot nearest around it when no side is free", () => {
    // Two pages side by side, the screen's edge at the left: no side of
    // the first is free, but further down, among the bubbles, there is room.
    const mail: Rect = { l: 76, t: 104, r: 696, b: 516 };
    const prod: Rect = { l: 744, t: 0, r: 1364, b: 630 };
    const bubbles: Rect[] = [{ l: 269, t: 552, r: 339, b: 621 }, { l: 198, t: 693, r: 267, b: 810 }, { l: 366, t: 683, r: 442, b: 800 }];
    const all = [mail, prod, ...bubbles, bar];
    const p = placeBeside(mail, [360, 58], all, screen);
    expect(p.side).toBe("around");
    for (const o of all) expect(overlaps(box(p), o)).toBe(false);
    expect(box(p).t - mail.b).toBeLessThan(200);
  });

  it("keeps its side while it stays free, so that it does not hop", () => {
    const sub: Rect = { l: 600, t: 300, r: 700, b: 400 };
    expect(placeBeside(sub, words, [sub], screen, "below").side).toBe("below");
    const under: Rect = { l: 500, t: 410, r: 900, b: 600 };
    expect(placeBeside(sub, words, [sub, under], screen, "below").side).not.toBe("below");
  });

  it("stays on the screen when its subject is at the edge", () => {
    const sub: Rect = { l: 1300, t: 70, r: 1430, b: 150 };
    const p = placeBeside(sub, words, [sub], screen);
    const b = box(p);
    expect(b.r).toBeLessThanOrEqual(screen.r);
    expect(b.t).toBeGreaterThanOrEqual(screen.t);
    expect(overlaps(b, sub)).toBe(false);
  });

  it("points at a card's height in an open page", () => {
    const page: Rect = { l: 400, t: 100, r: 1000, b: 700 };
    const p = placeBeside(page, words, [page, bar], screen, undefined, 520);
    expect(p.orb[1]).toBe(520);
    expect(p.orb[0]).toBeGreaterThan(page.r);
  });

  it("finds a free spot near the middle at the home, clear of bubbles and the bar", () => {
    const mid: Rect = { l: 600, t: 380, r: 840, b: 560 }; // a subject right in the middle
    const p = placeHome(words, [mid, bar], screen);
    for (const o of [mid, bar]) expect(overlaps(box(p), o)).toBe(false);
    const c = [(screen.l + screen.r) / 2, (screen.t + screen.b) / 2];
    expect(Math.hypot(p.orb[0] - c[0], p.orb[1] - c[1])).toBeLessThan(500);
    // it keeps that spot while it stays free
    const again = placeHome(words, [mid, bar], screen, [p.orb[0] + 130, p.orb[1]]);
    expect(Math.abs(again.orb[0] - p.orb[0])).toBeLessThan(140);
  });
});

describe("how the orb moves", () => {
  it("glides a share of the way each frame", () => {
    const a = glide([0, 0], [100, 0], false);
    expect(a[0]).toBeGreaterThan(0);
    expect(a[0]).toBeLessThan(100);
    let p: [number, number] = [0, 0];
    for (let i = 0; i < 200; i++) p = glide(p, [100, 50], false);
    expect(p).toEqual([100, 50]);
  });

  it("goes at once when the user asks for less motion", () => {
    expect(glide([0, 0], [300, 120], true)).toEqual([300, 120]);
  });
});
