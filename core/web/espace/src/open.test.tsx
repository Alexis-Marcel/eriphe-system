// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { CATALOG } from "./a2ui/catalog";
import { Espace } from "./Espace";
import { WordsContext, makeT } from "./i18n";
import type { Layout } from "./types";

// A click on the workspace answers at once: the surface opened is in
// front before eriphe-core's layout comes, going back too; what core
// refused is undone, and said.

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

class FakeSource {
  static all: FakeSource[] = [];
  listeners = new Map<string, Set<EventListener>>();
  closed = false;
  constructor(public url: string) {
    FakeSource.all.push(this);
  }
  addEventListener(type: string, h: EventListener) {
    if (!this.listeners.has(type)) this.listeners.set(type, new Set());
    this.listeners.get(type)!.add(h);
  }
  removeEventListener(type: string, h: EventListener) {
    this.listeners.get(type)?.delete(h);
  }
  close() {
    this.closed = true;
  }
  static emit(type: string, payload: unknown) {
    const es = FakeSource.all.filter((s) => !s.closed).pop()!;
    const e = new MessageEvent(type, { data: JSON.stringify(payload) });
    for (const h of es.listeners.get(type) ?? []) h(e);
  }
}

let el: HTMLDivElement;
let root: Root;
let actions: { body: unknown; done: (ok: boolean) => void }[];

const text = (id: string, words: string) => ({
  version: "1.0",
  createSurface: { surfaceId: id, catalogId: CATALOG.id, components: [{ id: "root", component: "Text", text: words }] },
});

const grid: Layout = {
  ambient: "calm",
  items: [
    { id: "today", title: "Aujourd'hui", size: "l", col: 0, row: 0, w: 6, h: 3 },
    { id: "pay", title: "Paiement", size: "s", col: 6, row: 0, w: 3, h: 1 },
  ],
  chips: [],
};

beforeEach(() => {
  el = document.createElement("div");
  document.body.append(el);
  root = createRoot(el);
  FakeSource.all = [];
  actions = [];
  vi.stubGlobal("EventSource", FakeSource);
  vi.stubGlobal("matchMedia", (q: string) => ({ matches: false, media: q, addEventListener() {}, removeEventListener() {} }));
  // An action waits until the test answers it; the rest is done at once.
  vi.stubGlobal("fetch", (url: string, init?: RequestInit) => {
    if (url !== "/api/espace/action") return Promise.resolve(new Response(null, { status: 204 }));
    return new Promise<Response>((resolve) =>
      actions.push({ body: JSON.parse(init!.body as string), done: (ok) => resolve(new Response(null, { status: ok ? 204 : 500 })) }),
    );
  });
});

afterEach(async () => {
  await act(async () => root.unmount());
  el.remove();
  vi.unstubAllGlobals();
});

const words = { "espace.settings": "Réglages", "espace.error": "Ça n'a pas marché.", "espace.back": "Revenir" };

async function draw() {
  await act(async () =>
    root.render(
      <WordsContext.Provider value={{ t: makeT(words), lang: "fr" }}>
        <Espace boot={{ lang: "fr", name: "Eriphe", csrf: "tok", strings: words, live: true }} />
      </WordsContext.Provider>,
    ),
  );
  await act(async () => {
    FakeSource.emit("a2ui", { seq: 1, reset: true, messages: [text("today", "Trois réunions"), text("pay", "240 €")] });
    FakeSource.emit("layout", grid);
  });
}

const tile = (id: string) => el.querySelector<HTMLElement>(`article[data-surface="${id}"]`)!;

describe("a link to a surface", () => {
  it("opens it once it is on the workspace (/?open=<id>, a card's thread), then leaves the address", async () => {
    history.replaceState(null, "", "/?open=pay");
    await draw();
    expect(actions).toHaveLength(1);
    expect(actions[0].body).toMatchObject({ action: { name: "open", surfaceId: "pay" } });
    expect(tile("pay").classList.contains("focus")).toBe(true);
    expect(location.search).toBe("");
    // Not twice.
    await act(async () => FakeSource.emit("layout", grid));
    expect(actions).toHaveLength(1);
  });

  it("ignores an id that is no surface's id", async () => {
    history.replaceState(null, "", "/?open=" + encodeURIComponent("<x>"));
    await draw();
    expect(actions).toHaveLength(0);
    history.replaceState(null, "", "/");
  });
});

describe("a click on the workspace", () => {
  it("opens a surface in front at once, whatever core's layout says meanwhile", async () => {
    await draw();
    await act(async () => tile("pay").click());
    // At once, before core answered: it in front, l at least, as core places it.
    expect(actions).toHaveLength(1);
    // The workspace re-laid out: it in the centre, the other in the column at the side.
    expect(tile("pay").className).toMatch(/size-l centre focus/);
    expect(tile("today").classList.contains("side-item")).toBe(true);
    expect(el.querySelector(".canvas.focus-mode > aside.side")?.contains(tile("today"))).toBe(true);
    // A layout core sent before it read the click does not undo it.
    await act(async () => FakeSource.emit("layout", grid));
    expect(tile("pay").classList.contains("focus")).toBe(true);
    // Core's layout around it: the same re-layout, its places on the grid aside.
    await act(async () => actions[0].done(true));
    const around: Layout = { ...grid, focus: "pay", items: [{ ...grid.items[1], size: "xl", col: 0, row: 0, w: 6, h: 5 }, { ...grid.items[0], size: "m", col: 6, row: 0, w: 3, h: 2 }] };
    await act(async () => FakeSource.emit("layout", around));
    expect(tile("pay").classList.contains("centre")).toBe(true);
    expect(tile("today").classList.contains("side-item")).toBe(true);

    // Back: the layout before it, at once.
    await act(async () => el.querySelector<HTMLButtonElement>(".eriphe-actions button")!.click());
    expect(tile("pay").classList.contains("focus")).toBe(false);
    expect(tile("today").className).toContain("size-l at-c0 at-r0 w-6 h-3");
  });

  it("brings what needs the user to the column while a surface is open, never to the centre", async () => {
    await draw();
    await act(async () => tile("today").click());
    await act(async () => actions[0].done(true));
    const around: Layout = { ...grid, focus: "today", items: [{ ...grid.items[0], size: "xl", w: 6, h: 5 }, { ...grid.items[1], col: 6 }] };
    await act(async () => FakeSource.emit("layout", around));
    expect(el.querySelector(".side-new")).toBeNull();
    // A card a task waits on arrives: urgent, placed first by core; the focus stays.
    const card = text("q-card-7", "Envoyer le récapitulatif à Sam ?");
    Object.assign(card.createSurface, { metadata: { extensions: { eriphe: { title: "À décider", weight: 80, updated: "2026-11-15T16:00:00Z" } } } });
    await act(async () => {
      FakeSource.emit("a2ui", { seq: 2, messages: [card] });
      FakeSource.emit("layout", { ...around, items: [{ id: "q-card-7", title: "À décider", size: "l", col: 0, row: 0, w: 4, h: 3, urgent: true }, ...around.items] });
    });
    expect(tile("today").classList.contains("centre")).toBe(true);
    const arrived = tile("q-card-7");
    expect(arrived.classList.contains("side-item")).toBe(true);
    expect(arrived.className).toContain("needs");
    expect(arrived.className).toContain("fresh");
    // First of the column, whole: its content answers there.
    expect(el.querySelector("aside.side > article")).toBe(arrived);
    expect(arrived.querySelector(".side-body .a2-text")?.textContent).toBe("Envoyer le récapitulatif à Sam ?");
    // A click opens it instead, by core, at once.
    await act(async () => arrived.querySelector<HTMLButtonElement>("button.side-open")!.click());
    expect(actions[1].body).toMatchObject({ action: { name: "open", surfaceId: "q-card-7" } });
    expect(tile("q-card-7").classList.contains("centre")).toBe(true);
    expect(tile("today").classList.contains("side-item")).toBe(true);
    // Esc gives the overview again.
    await act(async () => window.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape" })));
    expect(el.querySelector(".canvas.focus-mode")).toBeNull();
  });

  it("goes back to core's layout, and says so, when core refused", async () => {
    await draw();
    await act(async () => tile("pay").click());
    expect(tile("pay").classList.contains("focus")).toBe(true);
    await act(async () => actions[0].done(false));
    await act(async () => new Promise((r) => setTimeout(r, 0)));
    expect(tile("pay").classList.contains("focus")).toBe(false);
    expect(el.querySelector(".toast")?.textContent).toBe("Ça n'a pas marché.");
  });

  it("finds the settings from the workspace", async () => {
    await draw();
    const a = el.querySelector<HTMLAnchorElement>(".eriphe-panel a.eriphe-settings")!;
    expect(a.getAttribute("href")).toBe("/settings");
    expect(a.getAttribute("aria-label")).toBe("Réglages");
  });
});
