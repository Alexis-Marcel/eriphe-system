// @vitest-environment jsdom
import { describe, expect, it } from "vitest";
import { INTENT_FOR, shownLayout, type Intent } from "./Espace";
import { fitted, foldOut, focusedIn, needRows, unfocused } from "./place";
import type { Layout, Tile } from "./types";

const t = (id: string, size: Tile["size"], col: number, row: number, w: number, h: number, more: Partial<Tile> = {}): Tile => ({ id, title: id, size, col, row, w, h, ...more });

const base: Layout = {
  eriphe: "Ta journée.",
  by: "composer",
  ambient: "decision",
  items: [
    t("alert", "m", 0, 0, 3, 2, { urgent: true }),
    t("card", "s", 3, 0, 3, 1),
    t("today", "l", 6, 0, 6, 3),
    t("note", "s", 0, 2, 3, 1),
    t("week", "m", 3, 1, 3, 2),
  ],
  chips: [{ id: "news", title: "Nouvelles" }],
};

// Two tiles never share a cell, and every one is on the grid.
function check(l: Layout) {
  const seen = new Set<string>();
  for (const i of l.items) {
    expect(i.col >= 0 && i.row >= 0 && i.col + i.w <= 12 && i.row + i.h <= 8, `${i.id} on the grid`).toBe(true);
    for (let r = i.row; r < i.row + i.h; r++)
      for (let c = i.col; c < i.col + i.w; c++) {
        expect(seen.has(`${c},${r}`), `${i.id} at ${c},${r}`).toBe(false);
        seen.add(`${c},${r}`);
      }
  }
}

describe("a tile fits its content", () => {
  it("leaves core's layout as it is when every tile holds its content", () => {
    expect(fitted(base, {})).toBe(base);
    expect(fitted(base, { card: 1, today: 2, week: 2 })).toBe(base);
  });

  it("grows a tile under itself, where the grid has room, keeping core's order", () => {
    const l = fitted(base, { note: 3 });
    check(l);
    expect(l.items.map((i) => i.id)).toEqual(base.items.map((i) => i.id));
    expect(l.items.find((i) => i.id === "note")).toMatchObject({ col: 0, row: 2, w: 3, h: 3 });
    expect(l.items.find((i) => i.id === "today")).toEqual(base.items[2]);
  });

  it("moves a tile whose place an earlier one took, and folds it first when nothing is left", () => {
    // card grows down into week's place: week goes to the first free place.
    const l = fitted(base, { card: 3 });
    check(l);
    expect(l.items.find((i) => i.id === "card")).toMatchObject({ col: 3, row: 0, h: 3 });
    const week = l.items.find((i) => i.id === "week")!;
    expect(week.h).toBe(2);
    expect([week.col, week.row]).not.toEqual([3, 1]);

    // A grid full of tall tiles: what no longer fits is folded, first of the chips.
    const full = fitted(base, { alert: 8, card: 8, today: 8, note: 8, week: 8 });
    check(full);
    expect(full.items.map((i) => i.id)).toEqual(["alert", "card", "today"]);
    expect(full.chips.map((c) => c.id)).toEqual(["note", "week", "news"]);
  });

  it("never grows the surface open, nor a tile the user placed", () => {
    const l = { ...base, focus: "today", items: base.items.map((i) => (i.id === "card" ? { ...i, pinned: true } : i)) };
    expect(fitted(l, { today: 6, card: 4 })).toBe(l);
  });

  it("keeps the cells of the surface open from a tile that grows before it", () => {
    // alert comes first in core's order and would grow down into week's cells.
    const l = fitted({ ...base, focus: "week" }, { alert: 6 });
    check(l);
    expect(l.items.find((i) => i.id === "week")).toEqual(base.items[4]);
    expect(l.items.find((i) => i.id === "alert")!.h).toBeGreaterThan(2);
  });

  it("gives a moved tile as many rows as there are, not only its own", () => {
    // card takes week's place; week needs 6 rows: 6 nowhere, 5 under note.
    const l = fitted(base, { card: 3, week: 6 });
    check(l);
    const week = l.items.find((i) => i.id === "week")!;
    expect(week).toMatchObject({ col: 0, row: 3, h: 5 });
  });

  it("counts a tile's rows from its chrome and its content", () => {
    // cells of 90 px, 12 px apart: 2 rows hold 192 px (and a pixel of rounding).
    expect(needRows(90, 50, 40, 90, 12)).toBe(1);
    expect(needRows(90, 50, 150, 90, 12)).toBe(2);
    expect(needRows(90, 50, 155, 90, 12)).toBe(3);
    expect(needRows(90, 50, 40, 0, 12)).toBe(0);
  });
});

describe("a tile with too little room", () => {
  const content = () => {
    const el = document.createElement("div");
    el.innerHTML = `<div class="a2-column"><p class="a2-text">Un</p><p class="a2-text">Deux</p><ul class="a2-list"><li>Trois</li><li>Quatre</li></ul></div>`;
    return el;
  };
  // Each block shown is 20 px high.
  const height = (el: HTMLElement) => 20 * Array.from(el.querySelectorAll("p, li")).filter((b) => !b.closest(".fit-out")).length;

  it("hides its last blocks whole until it holds, the first one always kept", () => {
    const el = content();
    expect(foldOut(el, 45, height)).toBe(true);
    expect(Array.from(el.querySelectorAll(".fit-out")).map((b) => b.textContent)).toEqual(["Trois", "Quatre"]);
    const tiny = content();
    foldOut(tiny, 5, height);
    expect(Array.from(tiny.querySelectorAll("p, li")).filter((b) => !b.closest(".fit-out")).map((b) => b.textContent)).toEqual(["Un"]);
    expect(foldOut(content(), 100, height)).toBe(false);
  });
});

describe("a surface opened", () => {
  it("puts it in front at once by core's stage rule, the one on stage before reduced next to it", () => {
    const l = focusedIn(base, "card");
    check(l);
    expect(l.focus).toBe("card");
    expect(l.say).toBeUndefined();
    // The urgent first, then it, l at least.
    expect(l.items.map((i) => i.id).slice(0, 2)).toEqual(["alert", "card"]);
    expect(l.items.find((i) => i.id === "card")).toMatchObject({ size: "l", col: 3, row: 0 });

    // Another one opened: the open one stays a tile, m at most, right after it.
    const open = { ...l, items: l.items.map((i) => (i.id === "card" ? { ...i, size: "xl" as const, w: 6, h: 5 } : i)) };
    const next = focusedIn(open, "week");
    check(next);
    expect(next.focus).toBe("week");
    expect(next.previous).toBe("card");
    expect(next.items.map((i) => i.id).slice(0, 3)).toEqual(["alert", "week", "card"]);
    expect(next.items.find((i) => i.id === "card")).toMatchObject({ size: "m" });
  });

  it("keeps a placed tile at its place", () => {
    const l = focusedIn({ ...base, items: base.items.map((i) => (i.id === "today" ? { ...i, pinned: true } : i)) }, "card");
    check(l);
    expect(l.items.find((i) => i.id === "today")).toMatchObject({ col: 6, row: 0 });
  });

  it("comes out of the strip when it was a chip", () => {
    const l = focusedIn(base, "news");
    check(l);
    expect(l.focus).toBe("news");
    expect(l.chips.some((c) => c.id === "news")).toBe(false);
  });

  it("goes back to the layout before it", () => {
    const open = focusedIn(base, "card");
    expect(unfocused(open, base)).toBe(base);
    expect(unfocused(open, null).focus).toBeUndefined();
  });

  it("is kept over core's layouts until core agrees, or for a while", () => {
    const open: Intent = { open: "card", at: 1000 };
    // Core's layout from before the click: the page still shows the click.
    expect(shownLayout(base, open, 1100)?.focus).toBe("card");
    // Core agrees: its layout is shown as it is.
    expect(shownLayout({ ...base, focus: "card" }, open, 1200)).toBeNull();
    // Core never did: after a while, its layout.
    expect(shownLayout(base, open, 1000 + INTENT_FOR + 1)).toBeNull();
    const back: Intent = { back: base, at: 1000 };
    expect(shownLayout({ ...base, focus: "card" }, back, 1100)).toBe(base);
    expect(shownLayout(base, back, 1100)).toBeNull();
  });
});
