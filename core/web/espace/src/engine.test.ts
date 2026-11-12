import { describe, expect, it } from "vitest";
import { COLS, DIMS, ROWS, pack, place, related, type Layout } from "./engine";
import type { Composition, Item, Obj, Ref, Rule } from "./types";

const mail = (id: string, from = "Sam <sam@example.org>"): Obj => ({
  ref: `mail:${id}`, type: "mail", from, subject: `Subject ${id}`, at: "2026-11-12T09:00", excerpt: "…",
});
const storeOf = (...objs: Obj[]) => new Map<Ref, Obj>(objs.map((o) => [o.ref, o]));
const comp = (items: Item[], chips?: Ref[]): Composition => ({ mode: "side", eriphe: "", items, chips });

// Every module is inside the grid, and no two share a cell.
function checkNoOverflow(l: Layout) {
  const cells = new Set<string>();
  for (const p of l.placed) {
    const { w, h } = p;
    expect(w).toBeGreaterThanOrEqual(DIMS[p.size].w);
    expect(h).toBe(DIMS[p.size].h);
    expect(p.col).toBeGreaterThanOrEqual(0);
    expect(p.row).toBeGreaterThanOrEqual(0);
    expect(p.col + w).toBeLessThanOrEqual(COLS);
    expect(p.row + h).toBeLessThanOrEqual(ROWS);
    for (let r = p.row; r < p.row + h; r++)
      for (let c = p.col; c < p.col + w; c++) {
        const k = `${c},${r}`;
        expect(cells.has(k), `cell ${k} taken twice`).toBe(false);
        cells.add(k);
      }
  }
}

describe("pack", () => {
  it("places top left first", () => {
    expect(pack(["xl", "m"])).toEqual([{ size: "xl", col: 0, row: 0 }, { size: "m", col: 6, row: 0 }]);
  });
  it("shrinks what does not fit, then leaves it out", () => {
    const spots = pack(["xl", "xl", "xl", "s"]);
    // Two xl fill 12 x 5; a third shrinks to l (4 x 3) in the 3 rows left, the s fits beside it.
    expect(spots[0]?.size).toBe("xl");
    expect(spots[1]?.size).toBe("xl");
    expect(spots[2]?.size).toBe("l");
    expect(spots[3]?.size).toBe("s");
    const full = pack(new Array(40).fill("s"));
    expect(full.filter(Boolean)).toHaveLength(32); // 12 x 8 cells, 3 x 1 each
    expect(full.slice(32).every((s) => s === null)).toBe(true);
  });
});

describe("place", () => {
  it("sizes by weight", () => {
    const objs = [mail("a"), mail("b"), mail("c"), mail("d")];
    const l = place(comp([5, 4, 3, 2].map((w, i) => ({ ref: objs[i].ref, weight: w, why: "" }))), storeOf(...objs));
    // The l and m grow one step when there is room, never into xl.
    expect(l.placed.map((p) => p.size)).toEqual(["xl", "l", "l", "s"]);
    checkNoOverflow(l);
  });

  it("keeps one xl at most", () => {
    const objs = [mail("a"), mail("b")];
    const l = place(comp(objs.map((o) => ({ ref: o.ref, weight: 5, why: "" }))), storeOf(...objs));
    expect(l.placed.map((p) => p.size)).toEqual(["xl", "l"]);
  });

  it("never overflows, and turns what does not fit into chips", () => {
    const objs = Array.from({ length: 30 }, (_, i) => mail(String(i)));
    const items = objs.map((o, i) => ({ ref: o.ref, weight: (i % 5) + 1, why: `why ${i}` }));
    const l = place(comp(items), storeOf(...objs));
    checkNoOverflow(l);
    expect(l.placed.length + l.chips.length).toBe(30);
    expect(l.chips.length).toBeGreaterThan(0);
    expect(l.chips.every((c) => c.from === "overflow")).toBe(true);
    // The heaviest go first; what is left out is the lightest.
    const minPlaced = Math.min(...l.placed.map((p) => p.weight));
    const maxChip = Math.max(...l.chips.map((c) => items.find((i) => i.ref === c.obj.ref)!.weight));
    expect(maxChip).toBeLessThanOrEqual(minPlaced);
  });

  it("shrinks before it chips", () => {
    const objs = Array.from({ length: 8 }, (_, i) => mail(String(i)));
    const l = place(comp(objs.map((o) => ({ ref: o.ref, weight: 4, why: "" }))), storeOf(...objs));
    checkNoOverflow(l);
    expect(l.chips).toHaveLength(0);
    expect(l.placed.some((p) => p.size !== "l")).toBe(true);
  });

  it("drops unknown refs", () => {
    const a = mail("a");
    const l = place(comp([{ ref: a.ref, weight: 3, why: "" }, { ref: "mail:invented", weight: 5, why: "" }], ["card:nowhere"]), storeOf(a));
    expect(l.placed.map((p) => p.obj.ref)).toEqual([a.ref]);
    expect(l.chips).toHaveLength(0);
  });

  it("keeps the composition's chips as chips", () => {
    const a = mail("a"), b = mail("b");
    const l = place(comp([{ ref: a.ref, weight: 3, why: "" }], [b.ref]), storeOf(a, b));
    expect(l.placed.map((p) => p.obj.ref)).toEqual([a.ref]);
    expect(l.chips).toEqual([{ obj: b, why: "", from: "composition" }]);
  });

  it("applies the user's rules", () => {
    const a = mail("a", "Sam <sam@example.org>"), b = mail("b", "News <news@example.net>");
    const items = [{ ref: a.ref, weight: 3, why: "" }, { ref: b.ref, weight: 3, why: "" }];
    const never: Rule[] = [{ id: "1", key: "mail:example.net", delta: -5, at: "" }];
    expect(place(comp(items), storeOf(a, b), never).placed.map((p) => p.obj.ref)).toEqual([a.ref]);
    const more: Rule[] = [{ id: "2", key: "mail:example.net", delta: 2, at: "" }];
    expect(place(comp(items), storeOf(a, b), more).placed[0]).toMatchObject({ obj: b, size: "xl" });
  });

  it("puts the focus in xl and pulls its relations in", () => {
    const m = mail("a");
    const card: Obj = { ref: "card:1", type: "card", text: "Add to the agenda", tool: "calendar.create_event",
      input: { start: "2026-11-19T15:00", end: "2026-11-19T16:00", title: "Visit" }, about: m.ref, since: "2026-11-12T09:00", state: "pending" };
    const day: Obj = { ref: "agenda:2026-11-19", type: "agenda", date: "2026-11-19", events: [] };
    const other = mail("b");
    const store = storeOf(m, card, day, other);
    expect(related(card, [...store.values()]).sort()).toEqual([day.ref, m.ref].sort());
    const l = place(comp([{ ref: m.ref, weight: 3, why: "" }, { ref: other.ref, weight: 4, why: "" }], [card.ref]), store, [], card.ref);
    const by = Object.fromEntries(l.placed.map((p) => [p.obj.ref, p]));
    expect(by[card.ref]).toMatchObject({ size: "xl", focus: true });
    expect(by[m.ref].related).toBe(true);
    expect(by[day.ref].related).toBe(true);
    expect(by[other.ref].size).toBe("s");
    expect(l.chips).toHaveLength(0);
    checkNoOverflow(l);
  });
});
