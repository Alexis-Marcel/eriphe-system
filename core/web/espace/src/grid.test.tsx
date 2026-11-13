// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { createAdapter, type Adapter, type Message } from "./a2ui/adapter";
import { CATALOG } from "./a2ui/catalog";
import { GRID_LINES } from "./a2ui/code";
import { Grid, stacked, type Spot } from "./Grid";
import type { Layout } from "./types";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let el: HTMLDivElement;
let root: Root;
let adapter: Adapter;
let opened: string[];

const text = (id: string, words: string): Message => ({
  version: "1.0",
  createSurface: { surfaceId: id, catalogId: CATALOG.id, components: [{ id: "root", component: "Text", text: words }] },
});

const layout: Layout = {
  eriphe: "Le paiement attend ton accord.",
  by: "composer",
  ambient: "urgent",
  items: [
    { id: "pay", title: "Paiement", size: "xl", col: 0, row: 0, w: 6, h: 5, why: "Attend ton accord" },
    { id: "week", title: "La semaine", size: "m", col: 6, row: 0, w: 6, h: 2 },
    { id: "ghost", title: "Pas encore là", size: "s", col: 6, row: 2, w: 3, h: 1 },
  ],
  chips: [{ id: "news", title: "Nouvelles", why: "Rien de pressé" }],
};

const render = (l: Layout) => act(async () => root.render(<Grid adapter={adapter} layout={l} onOpen={(id) => opened.push(id)} />));
const tile = (id: string) => el.querySelector<HTMLElement>(`article[data-surface="${id}"]`);

beforeEach(async () => {
  el = document.createElement("div");
  document.body.append(el);
  adapter = createAdapter();
  opened = [];
  root = createRoot(el);
  await act(async () => adapter.applyMessages([text("pay", "240 €"), text("week", "Trois réunions"), text("news", "Une lettre"), text("late", "Sans place")]));
});

afterEach(async () => {
  await act(async () => root.unmount());
  adapter.dispose();
  el.remove();
});

describe("the grid of surfaces", () => {
  it("puts each tile where eriphe-core says, by class only", async () => {
    const styles = document.querySelectorAll("style").length;
    await render(layout);
    expect(tile("pay")?.className).toContain("size-xl at-c0 at-r0 w-6 h-5");
    expect(tile("week")?.className).toContain("size-m at-c6 at-r0 w-6 h-2");
    expect(tile("pay")?.querySelector(".tile-title")?.textContent).toBe("Paiement");
    expect(tile("pay")?.querySelector(".a2-text")?.textContent).toBe("240 €");
    expect(tile("pay")?.querySelector(".why")?.textContent).toBe("Attend ton accord");
    // A tile whose surface has not come yet is not drawn.
    expect(tile("ghost")).toBeNull();
    // No style attribute, no <style>: the CSP stays strict.
    expect(document.querySelectorAll("[style]")).toHaveLength(0);
    expect(document.querySelectorAll("style")).toHaveLength(styles);
  });

  it("shows a surface's one-line summary on a small tile and a chip, its content when larger", async () => {
    const line = (id: string, summary: string): Message => ({
      version: "1.0",
      createSurface: {
        surfaceId: id,
        catalogId: CATALOG.id,
        components: [{ id: "root", component: "Text", text: "La frise entière" }],
        metadata: { extensions: { eriphe: { title: "PR 12", summary } } },
      },
    });
    await act(async () =>
      adapter.applyMessages([line("ghost", "Fusion ✓ · Déploiement à toi"), { version: "1.0", deleteSurface: { surfaceId: "news" } }, line("news", "Recherche …")]),
    );
    await render(layout);
    expect(tile("ghost")?.querySelector(".tile-line")?.textContent).toBe("Fusion ✓ · Déploiement à toi");
    expect(tile("ghost")?.querySelector(".a2-text")).toBeNull();
    expect(el.querySelector('.chip[data-surface="news"] .chip-from')?.textContent).toBe("Recherche …");
    // Larger, or open: the surface itself.
    await render({ ...layout, items: layout.items.map((i) => (i.id === "ghost" ? { ...i, size: "m" as const, h: 2 } : i)) });
    expect(tile("ghost")?.querySelector(".tile-line")).toBeNull();
    expect(tile("ghost")?.querySelector(".a2-text")?.textContent).toBe("La frise entière");
    await render({ ...layout, focus: "ghost" });
    expect(tile("ghost")?.querySelector(".tile-line")).toBeNull();
  });

  it("folds what the layout folds, and what it does not name yet, into chips", async () => {
    await render(layout);
    const chips = [...el.querySelectorAll<HTMLElement>(".chips .chip")].map((c) => c.dataset.surface);
    expect(chips).toEqual(["news", "late"]);
    expect(el.querySelector('.chip[data-surface="news"] .chip-from')?.textContent).toBe("Rien de pressé");
    expect(el.querySelectorAll("article")).toHaveLength(2);
  });

  it("opens a chip or a tile, not the one already open", async () => {
    await render({ ...layout, focus: "week" });
    await act(async () => el.querySelector<HTMLButtonElement>('.chip[data-surface="news"] button')!.click());
    await act(async () => tile("pay")!.click());
    await act(async () => tile("week")!.click());
    expect(opened).toEqual(["news", "pay"]);
    expect(tile("week")?.classList.contains("focus")).toBe(true);
  });

  it("marks what relates to the surface open, and says so when it has no why", async () => {
    await render({ ...layout, focus: "pay", items: [{ ...layout.items[0], related: true }, { ...layout.items[1], related: true }] });
    expect(tile("week")?.classList.contains("related")).toBe(true);
    expect(tile("week")?.querySelector(".why")?.textContent).toBe("espace.related");
    expect(tile("pay")?.classList.contains("related")).toBe(false);
    expect(tile("pay")?.querySelector(".why")?.textContent).toBe("Attend ton accord");
    await render(layout);
    expect(tile("week")?.classList.contains("related")).toBe(false);
    expect(tile("week")?.querySelector(".why")).toBeNull();
  });

  it("closes a tile with its cross, without opening it", async () => {
    const closed: string[] = [];
    await act(async () => root.render(<Grid adapter={adapter} layout={layout} onOpen={(id) => opened.push(id)} onClose={(id) => closed.push(id)} />));
    const cross = tile("week")!.querySelector<HTMLButtonElement>("button.tile-close")!;
    expect(cross.getAttribute("aria-label")).toBe("espace.tile.close");
    await act(async () => cross.click());
    expect(closed).toEqual(["week"]);
    expect(opened).toEqual([]);
  });

  it("shows a closed surface as a chip marked closed", async () => {
    await render({ ...layout, chips: [...layout.chips, { id: "late", title: "Sans place", closed: true }] });
    const chip = el.querySelector<HTMLElement>('.chip[data-surface="late"]')!;
    expect(chip.classList.contains("closed")).toBe(true);
    expect(chip.querySelector(".chip-from")?.textContent).toBe("espace.chip.closed");
  });

  const placing = async () => {
    const placed: [string, string, Spot][] = [];
    await act(async () => root.render(<Grid adapter={adapter} layout={layout} onOpen={(id) => opened.push(id)} onPlace={(id, what, to) => placed.push([id, what, to])} />));
    return placed;
  };
  const key = (b: HTMLElement, k: string) => act(async () => { b.dispatchEvent(new KeyboardEvent("keydown", { key: k, bubbles: true })); });

  it("moves a tile with the arrow keys on its grip, kept on the grid", async () => {
    const placed = await placing();
    const grip = tile("week")!.querySelector<HTMLButtonElement>("button.tile-move")!;
    await key(grip, "ArrowDown");
    await key(grip, "ArrowRight"); // week is m (3 wide) at col 6: col 7 fits, as 9 does
    await key(grip, "ArrowUp"); // row 0: nowhere to go
    await key(grip, "Enter");
    expect(placed).toEqual([
      ["week", "moved", { size: "m", col: 6, row: 1 }],
      ["week", "moved", { size: "m", col: 7, row: 0 }],
    ]);
    expect(opened).toEqual([]);
  });

  it("changes a tile's size with its button: a click, + and −", async () => {
    const placed = await placing();
    const size = tile("pay")!.querySelector<HTMLButtonElement>("button.tile-size")!;
    await act(async () => size.click()); // xl wraps to s
    await key(size, "-"); // xl to l
    await key(size, "+"); // xl is the biggest
    expect(placed).toEqual([
      ["pay", "resized", { size: "s", col: 0, row: 0 }],
      ["pay", "resized", { size: "l", col: 0, row: 0 }],
    ]);
    const week = tile("week")!.querySelector<HTMLButtonElement>("button.tile-size")!;
    await key(week, "ArrowUp"); // m to l at col 6, row 0
    expect(placed[2]).toEqual(["week", "resized", { size: "l", col: 6, row: 0 }]);
    expect(opened).toEqual([]);
  });

  it("drags a tile by its grip to a cell, a ghost showing where, by class only", async () => {
    const placed = await placing();
    const canvas = el.querySelector<HTMLElement>("main.canvas")!;
    canvas.getBoundingClientRect = () => ({ left: 0, top: 0, width: 1200, height: 800, right: 1200, bottom: 800, x: 0, y: 0, toJSON() {} });
    const grip = tile("week")!.querySelector<HTMLButtonElement>("button.tile-move")!;
    // Cells are 100 × 100: grabbed in cell (8, 0), week's top left is (6, 0).
    await act(async () => { grip.dispatchEvent(new MouseEvent("pointerdown", { bubbles: true, button: 0, clientX: 850, clientY: 50 })); });
    await act(async () => { window.dispatchEvent(new MouseEvent("pointermove", { clientX: 450, clientY: 450 })); });
    const ghost = el.querySelector<HTMLElement>(".tile-ghost")!;
    expect(ghost.className).toBe("tile-ghost at-c2 at-r4 w-3 h-2");
    expect(tile("week")!.classList.contains("dragging")).toBe(true);
    expect(document.querySelectorAll("[style]")).toHaveLength(0);
    await act(async () => { window.dispatchEvent(new MouseEvent("pointermove", { clientX: 1190, clientY: 790 })); });
    expect(el.querySelector(".tile-ghost")!.className).toBe("tile-ghost at-c9 at-r6 w-3 h-2");
    await act(async () => { window.dispatchEvent(new MouseEvent("pointerup", {})); });
    expect(el.querySelector(".tile-ghost")).toBeNull();
    expect(placed).toEqual([["week", "moved", { size: "m", col: 9, row: 6 }]]);
    expect(opened).toEqual([]);
  });

  it("moves a tile when the layout changes", async () => {
    await render(layout);
    await render({ ...layout, items: [{ ...layout.items[1], size: "xl", col: 0, row: 0, w: 12, h: 5 }], chips: [{ id: "pay", title: "Paiement" }] });
    expect(tile("week")?.className).toContain("size-xl at-c0 at-r0 w-12 h-5");
    expect([...el.querySelectorAll<HTMLElement>(".chip")].map((c) => c.dataset.surface)).toEqual(["pay", "news", "late"]);
  });
});

// On a phone: one column, in the composer's order, the chips after; a
// surface open has the screen to itself.
describe("the workspace on a phone", () => {
  const phone = (l: Layout) => act(async () => root.render(<Grid adapter={adapter} layout={l} narrow onOpen={(id) => opened.push(id)} />));
  // The composer's order is the items', not their place: week comes first
  // though it is on the right.
  const ordered: Layout = { ...layout, items: [layout.items[1], layout.items[0]] };

  it("stacks the tiles in the composer's order, the chips last", () => {
    const s = stacked(ordered, new Set(["pay", "week", "news", "late"]));
    expect(s.tiles.map((t) => t.id)).toEqual(["week", "pay"]);
    expect(s.chips.map((c) => c.id)).toEqual(["news", "late"]);
    expect(s.full).toBe(false);
  });

  it("draws one column, then the chips, with no grid tools", async () => {
    await phone(ordered);
    const canvas = el.querySelector<HTMLElement>("main.canvas")!;
    expect(canvas.classList.contains("column")).toBe(true);
    expect([...canvas.querySelectorAll<HTMLElement>("article")].map((a) => a.dataset.surface)).toEqual(["week", "pay"]);
    // The chips come after the tiles in the page.
    const strip = el.querySelector<HTMLElement>(".chips")!;
    expect(strip.classList.contains("stacked")).toBe(true);
    expect(canvas.compareDocumentPosition(strip) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect([...strip.querySelectorAll<HTMLElement>(".chip")].map((c) => c.dataset.surface)).toEqual(["news", "late"]);
    // No move nor size on a column; the cross stays.
    expect(el.querySelector("button.tile-move")).toBeNull();
    expect(el.querySelector("button.tile-size")).toBeNull();
    expect(tile("pay")!.querySelector("button.tile-close")).not.toBeNull();
    expect(document.querySelectorAll("[style]")).toHaveLength(0);
    await act(async () => tile("pay")!.click());
    expect(opened).toEqual(["pay"]);
  });

  it("gives the screen to the surface open, alone", async () => {
    const focus: Layout = { ...ordered, focus: "pay", items: [layout.items[0], { ...layout.items[1], related: true }] };
    expect(stacked(focus, new Set(["pay", "week", "news"]))).toMatchObject({ tiles: [{ id: "pay" }], chips: [], full: true });
    await phone(focus);
    expect(el.querySelector<HTMLElement>("main.canvas")!.className).toBe("canvas column full");
    expect([...el.querySelectorAll<HTMLElement>("article")].map((a) => a.dataset.surface)).toEqual(["pay"]);
    expect(tile("pay")!.classList.contains("focus")).toBe(true);
    expect(el.querySelectorAll(".chips .chip")).toHaveLength(0);
    // Back on the overview: the column again.
    await phone(ordered);
    expect([...el.querySelectorAll<HTMLElement>("article")].map((a) => a.dataset.surface)).toEqual(["week", "pay"]);
  });

  it("keeps the column while a chip opened is not placed yet", async () => {
    const waiting: Layout = { ...ordered, focus: "news" };
    expect(stacked(waiting, new Set(["pay", "week", "news"]))).toMatchObject({ full: false });
    await phone(waiting);
    expect(el.querySelector<HTMLElement>("main.canvas")!.className).toBe("canvas column");
    expect([...el.querySelectorAll<HTMLElement>("article")].map((a) => a.dataset.surface)).toEqual(["week", "pay"]);
  });

  it("keeps the wide grid's order and tools off a phone", async () => {
    await render(ordered);
    const strip = el.querySelector<HTMLElement>(".chips")!;
    const canvas = el.querySelector<HTMLElement>("main.canvas")!;
    expect(canvas.classList.contains("column")).toBe(false);
    expect(strip.compareDocumentPosition(canvas) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(el.querySelector("button.tile-move")).not.toBeNull();
  });
});

// jsdom lays nothing out: the heights a browser would give, by class.
function sizes(px: { canvas: number; tile: number; body: number; fit: (fit: HTMLElement) => number }) {
  const of = (el: HTMLElement, what: "client" | "offset") => {
    if (el.matches("main.canvas")) return px.canvas;
    if (el.matches("article.tile")) return px.tile;
    if (el.matches(".mod-body")) return px.body;
    if (el.matches(".mod-fit") && what === "offset") return px.fit(el);
    return 0;
  };
  vi.spyOn(HTMLElement.prototype, "clientHeight", "get").mockImplementation(function (this: HTMLElement) {
    return of(this, "client");
  });
  vi.spyOn(HTMLElement.prototype, "offsetHeight", "get").mockImplementation(function (this: HTMLElement) {
    return of(this, "offset");
  });
}

describe("a tile and its content", () => {
  afterEach(() => vi.restoreAllMocks());
  const two = (id: string, a: string, b: string): Message => ({
    version: "1.0",
    createSurface: {
      surfaceId: id,
      catalogId: CATALOG.id,
      components: [
        { id: "root", component: "Column", children: ["a", "b"] },
        { id: "a", component: "Text", text: a },
        { id: "b", component: "Text", text: b },
      ],
    },
  });

  const pairs: Layout = {
    ...layout,
    items: [
      { id: "pair", title: "Deux mots", size: "s", col: 0, row: 0, w: 3, h: 1 },
      { id: "more", title: "Encore", size: "s", col: 3, row: 0, w: 3, h: 1 },
    ],
  };

  it("says how many rows each tile's content needs, but the open one's, and never scrolls a tile on the grid", async () => {
    await act(async () => adapter.applyMessages([two("pair", "Un", "Deux"), two("more", "Trois", "Quatre")]));
    // A canvas of 8 rows of 90 px, 0 px apart; a tile 90 px high, its body 60.
    sizes({ canvas: 720, tile: 90, body: 60, fit: () => 150 });
    const needs: [string, number][] = [];
    await act(async () =>
      root.render(<Grid adapter={adapter} layout={{ ...pairs, focus: "more" }} onOpen={() => {}} onNeed={(id, rows) => needs.push([id, rows])} />),
    );
    expect(needs).toContainEqual(["pair", 2]);
    expect(needs.some(([id]) => id === "more")).toBe(false);
    // The content sits whole in .mod-fit, inside a body that does not scroll (espace.css).
    expect(tile("pair")!.querySelector(".mod-body > .mod-fit .a2-text")).not.toBeNull();
  });

  it("shows only whole blocks when it has too little room, and says the rest is there", async () => {
    await act(async () => adapter.applyMessages([two("pair", "Un", "Deux"), two("more", "Trois", "Quatre")]));
    // Each text shown is 40 px; the body holds 60.
    const shown = (fit: HTMLElement) => 40 * Array.from(fit.querySelectorAll(".a2-text")).filter((b) => !b.closest(".fit-out")).length;
    sizes({ canvas: 720, tile: 90, body: 60, fit: shown });
    await render(pairs);
    // (In a browser a ResizeObserver measures again once the surface drew; here, a layout that changed.)
    await render({ ...pairs, items: pairs.items.map((i) => ({ ...i, why: "Deux" })) });
    const pay = tile("pair")!;
    expect(pay.classList.contains("cut")).toBe(true);
    expect(Array.from(pay.querySelectorAll(".a2-text")).map((b) => [b.textContent, b.classList.contains("fit-out")])).toEqual([
      ["Un", false],
      ["Deux", true],
    ]);
    expect(pay.querySelector(".tile-more")?.textContent).toBe("espace.tile.more");
    // Given the room, all of it again.
    sizes({ canvas: 720, tile: 200, body: 170, fit: shown });
    await render({ ...pairs, items: pairs.items.map((i) => ({ ...i, h: 2 })) });
    expect(pay.classList.contains("cut")).toBe(false);
    expect(pay.querySelectorAll(".fit-out")).toHaveLength(0);
  });

  it("shows a long code's first lines whole on the grid, and all of it open, where it scrolls", async () => {
    const lines = Array.from({ length: GRID_LINES + 20 }, (_, i) => `ligne ${i + 1}`);
    const code = (id: string): Message => ({
      version: "1.0",
      createSurface: { surfaceId: id, catalogId: CATALOG.id, components: [{ id: "root", component: "Code", language: "text", text: lines.join("\n") }] },
    });
    await act(async () => adapter.applyMessages([code("pair"), code("more")]));
    // Each line shown is 20 px; the body holds 60.
    const shown = (fit: HTMLElement) => 20 * Array.from(fit.querySelectorAll(".a2-code-line")).filter((b) => !b.closest(".fit-out")).length;
    sizes({ canvas: 720, tile: 90, body: 60, fit: shown });
    await render({ ...pairs, focus: "more" });
    await render({ ...pairs, focus: "more", items: pairs.items.map((i) => ({ ...i, why: "Du code" })) });
    // On the grid: GRID_LINES drawn at most, the rest counted; the last hidden whole, the tile says so.
    const grid = tile("pair")!;
    expect(grid.querySelectorAll(".a2-code-line")).toHaveLength(GRID_LINES);
    expect(grid.querySelector(".a2-code-rest")?.textContent).toBe("espace.a2.code.rest");
    expect(grid.classList.contains("cut")).toBe(true);
    expect(Array.from(grid.querySelectorAll(".a2-code-line:not(.fit-out)")).map((l) => l.textContent)).toEqual(["ligne 1", "ligne 2", "ligne 3"]);
    expect(grid.querySelector(".mod-body > .mod-fit .a2-code-body")).not.toBeNull();
    // Open: every line, none hidden, nothing counted; its body is the one that scrolls (espace.css, .mod.focus).
    const open = tile("more")!;
    expect(open.classList.contains("focus")).toBe(true);
    expect(open.querySelectorAll(".a2-code-line")).toHaveLength(lines.length);
    expect(open.querySelectorAll(".fit-out")).toHaveLength(0);
    expect(open.querySelector(".a2-code-rest")).toBeNull();
    expect(open.classList.contains("cut")).toBe(false);
  });
});
