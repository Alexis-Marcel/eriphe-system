// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { createAdapter, type Adapter, type Message } from "./a2ui/adapter";
import { CATALOG } from "./a2ui/catalog";
import { Grid } from "./Grid";
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

  it("moves a tile when the layout changes", async () => {
    await render(layout);
    await render({ ...layout, items: [{ ...layout.items[1], size: "xl", col: 0, row: 0, w: 12, h: 5 }], chips: [{ id: "pay", title: "Paiement" }] });
    expect(tile("week")?.className).toContain("size-xl at-c0 at-r0 w-12 h-5");
    expect([...el.querySelectorAll<HTMLElement>(".chip")].map((c) => c.dataset.surface)).toEqual(["pay", "news", "late"]);
  });
});
