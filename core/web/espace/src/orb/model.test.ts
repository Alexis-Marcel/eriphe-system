import { describe, expect, it } from "vitest";
import type { GraphNode, OrbFeed } from "../data/api";
import { close, expires, glide, GAP, homePoint, keepOnScreen, leaves, NEAR, ORB, orbShow, placeBeside as besideOrNone, placeHome as homeOrNone, replaced, WORDS_MS, type Closed, type Mind, type Rect } from "./model";

// Where it goes when there is a free spot (the cases below have one).
const placeBeside = (...a: Parameters<typeof besideOrNone>) => besideOrNone(...a)!;
const placeHome = (...a: Parameters<typeof homeOrNone>) => homeOrNone(...a)!;

const node = (id: string, more: Partial<GraphNode> = {}): GraphNode => ({
  id, title: id.toUpperCase(), lifetime: "work", status: "idle", created: "2026-11-18T10:00:00Z", updated: "2026-11-18T10:00:00Z", version: 1, ...more,
});
const nodes = { sub: node("sub"), mail: node("mail"), day: node("day") };
const none: Closed = { answer: 0, seen: 0, failed: 0 };
const mind = (more: Partial<Mind> = {}): Mind => ({ now: 1_000_000, sent: null, closed: none, met: () => undefined, nodes, ...more });

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
    expect(orbShow(x, mind({ met: () => 0, now: 10 * WORDS_MS })).mode).toBe("answer");
    // closed: gone, for good
    const c = close(none, s);
    expect(c.answer).toBe(5);
    expect(orbShow(x, mind({ closed: c })).mode).toBe("rest");
  });

  it("goes beside the subject its answer belongs to, until something else happens", () => {
    const x: OrbFeed = { asked: 4, answer: { seq: 5, node: "sub", text: "J'ai ouvert le sujet.\nLe plan est écrit.", more: true } };
    const s = orbShow(x, mind());
    expect(s).toMatchObject({ mode: "answer", node: "sub" });
    expect(s.pinned).toBeFalsy();
    expect(expires(s)).toBe(WORDS_MS);
    // long past the old 15 s, still there
    expect(orbShow(x, mind({ met: () => mind().now - 60_000 })).mode).toBe("answer");
    // two minutes of the page seen: gone
    expect(orbShow(x, mind({ met: () => mind().now - WORDS_MS + 10 })).mode).toBe("answer");
    expect(orbShow(x, mind({ met: () => mind().now - WORDS_MS })).mode).toBe("rest");
    // closed, it is seen: a reload does not bring it back
    expect(orbShow(x, mind({ closed: close(none, s) })).mode).toBe("rest");
  });

  it("lets a newer answer replace the words beside a subject", () => {
    const answer: OrbFeed = { asked: 4, answer: { seq: 5, node: "sub", text: "Fait." } };
    const a = orbShow(answer, mind());
    const again = orbShow({ asked: 7, answer: { seq: 8, node: "mail", text: "Envoyé." } }, mind());
    expect(again).toMatchObject({ mode: "answer", node: "mail" });
    expect(replaced(a, again)).toBe(true);
    // the same answer again: not replaced
    expect(replaced(a, orbShow(answer, mind()))).toBe(false);
    // the user writing replaces them too
    expect(replaced(a, orbShow(answer, mind({ sent: { text: "Et ?", after: 4, to: "sub" } })))).toBe(true);
    // a failure only comes before them; pinned things are not words
    expect(replaced(a, orbShow({ ...answer, failed: { seq: 2, node: "mail" } }, mind()))).toBe(false);
    expect(replaced(orbShow({ answer: { seq: 5, text: "a\n\nb", whole: true } }, mind()), again)).toBe(false);
  });

  it("moves on from the words when the user opens another subject", () => {
    const s = orbShow({ asked: 4, answer: { seq: 5, node: "sub", text: "Fait." } }, mind());
    expect(leaves(s, "mail")).toBe(true);
    expect(leaves(s, null)).toBe(true);
    // the subject they are about, opened (« Voir plus », Eriphe's show): they stay beside its page
    expect(leaves(s, "sub")).toBe(false);
    // the pinned ones are not words that leave
    expect(leaves(orbShow({ failed: { seq: 3, node: "mail" } }, mind()), "sub")).toBe(false);
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

  it("says the answer of a turn that raised a card as any other, wherever the card waits", () => {
    // the answer belongs to the subject asked in, the card waits in another node's page (a task's)
    const x = { asked: 8, answer: { seq: 9, node: "mail", text: "J'ai lancé le tri : dis oui sur sa carte." }, wait: { card: 3, node: "sub" } } as OrbFeed;
    const s = orbShow(x, mind());
    expect(s).toMatchObject({ mode: "answer", node: "mail" });
    expect(s.words?.text).toBe("J'ai lancé le tri : dis oui sur sa carte.");
    // a feed with nothing but a card to point at: the orb rests, the graph shows what waits
    expect(orbShow({ asked: 8, wait: { card: 3, node: "mail" } } as OrbFeed, mind()).mode).toBe("rest");
  });

  it("only answers: a subject that begins to wait for the user, unasked, leaves it in the bar", () => {
    // what the orb is told carries nothing unasked: a node that waits is
    // the graph's (its ring), whatever its nodes say
    const waiting = { ...nodes, mail: node("mail", { status: "waits" }) };
    expect(orbShow({ asked: 3 }, mind({ nodes: waiting }))).toMatchObject({ mode: "rest", node: null });
    expect(orbShow(null, mind({ nodes: waiting })).mode).toBe("rest");
    // the answer it had stays where it was
    const x: OrbFeed = { asked: 4, answer: { seq: 5, node: "sub", text: "Fait." } };
    expect(orbShow(x, mind({ nodes: waiting }))).toMatchObject({ mode: "answer", node: "sub" });
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

  it("covers nothing even when the screen is full: then it has no place out there", () => {
    const full: Rect = { l: 0, t: 0, r: 1440, b: 900 }; // one page over all
    const sub: Rect = { l: 600, t: 300, r: 700, b: 400 };
    expect(besideOrNone(sub, words, [sub, full], screen)).toBeNull();
    expect(homeOrNone(words, [full], screen)).toBeNull();
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
    // Two pages side by side, the screen's edge at the left, a bubble
    // right under the first's middle: no side of it is free, but a little
    // aside, under it, there is room.
    const mail: Rect = { l: 76, t: 104, r: 696, b: 516 };
    const prod: Rect = { l: 860, t: 0, r: 1364, b: 630 };
    const under: Rect = { l: 350, t: 530, r: 430, b: 610 };
    const all = [mail, prod, under, bar];
    const p = besideOrNone(mail, [360, 58], all, screen)!;
    expect(p.side).toBe("around");
    for (const o of all) expect(overlaps(box(p), o)).toBe(false);
    expect(box(p).t - mail.b).toBeLessThan(NEAR + 40);
  });

  it("stays out of the space when the nearest free spot is no longer beside it", () => {
    const mail: Rect = { l: 76, t: 104, r: 696, b: 516 };
    const prod: Rect = { l: 744, t: 0, r: 1364, b: 630 };
    const band: Rect = { l: 0, t: 520, r: 1440, b: 760 }; // bubbles all along under it
    expect(besideOrNone(mail, [360, 58], [mail, prod, band, bar], screen)).toBeNull();
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

describe("its words stay whole on screen", () => {
  const screen: Rect = { l: 8, t: 60, r: 1192, b: 792 };
  const inside = (w: Rect) => w.l >= screen.l && w.r <= screen.r && w.t >= screen.t && w.b <= screen.b;

  it("leaves words that fit as they are", () => {
    const p = { orb: [600, 300] as [number, number], words: { l: 623, t: 290, r: 923, b: 330 }, side: "right" as const, dir: 1 as const };
    expect(keepOnScreen(p, screen)).toBe(p);
  });

  it("opens them on the orb's other side when they would leave the screen on theirs", () => {
    // Opened to the left of an orb near the left edge: cut off.
    const p = { orb: [60, 300] as [number, number], words: { l: 60 - ORB / 2 - GAP - 300, t: 290, r: 60 - ORB / 2 - GAP, b: 330 }, side: "left" as const, dir: -1 as const };
    const q = keepOnScreen(p, screen);
    expect(q.dir).toBe(1);
    expect(q.words!.l).toBe(60 + ORB / 2 + GAP);
    expect(q.words!.r - q.words!.l).toBe(300);
    expect(inside(q.words!)).toBe(true);
  });

  it("never opens them on its subject: slid in instead", () => {
    // Left of a page, its words open left past the edge; its other side is the page.
    const page: Rect = { l: 60 + ORB / 2 + 4, t: 200, r: 900, b: 600 };
    const p = { orb: [60, 300] as [number, number], words: { l: 60 - ORB / 2 - GAP - 300, t: 290, r: 60 - ORB / 2 - GAP, b: 330 }, side: "left" as const, dir: -1 as const };
    const q = keepOnScreen(p, screen, [page]);
    expect(q.dir).toBe(-1);
    expect(q.words).toEqual({ l: 8, t: 290, r: 308, b: 330 });
  });

  it("slides them in when neither side holds them, and never above the top or below the bottom", () => {
    // Wider than either side of an orb in the middle of a narrow screen.
    const narrow: Rect = { l: 8, t: 60, r: 392, b: 792 };
    const p = { orb: [200, 40] as [number, number], words: { l: 223, t: 20, r: 583, b: 80 }, side: "above" as const, dir: 1 as const };
    const q = keepOnScreen(p, narrow);
    expect(q.words).toEqual({ l: 32, t: 60, r: 392, b: 120 });
    const low = keepOnScreen({ ...p, orb: [200, 780], words: { l: 223, t: 770, r: 300, b: 830 } }, narrow);
    expect(low.words!.b).toBe(792);
  });
});
