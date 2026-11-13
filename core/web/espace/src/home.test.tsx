// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import * as data from "./data";
import { Espace } from "./Espace";
import { FailSafe } from "./Fallback";
import { WordsContext, makeT } from "./i18n";

// The workspace is Eriphe's home page: when it cannot open, the user gets
// the conversation; on a phone it is one column, a surface open has the
// screen.

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let el: HTMLDivElement;
let root: Root;

beforeEach(() => {
  el = document.createElement("div");
  document.body.append(el);
  root = createRoot(el);
});

afterEach(async () => {
  await act(async () => root.unmount());
  el.remove();
  vi.unstubAllGlobals();
});

describe("the fallback to the conversation", () => {
  it("fails a boot it cannot read, never runs on made-up data", async () => {
    vi.stubGlobal("location", { assign: vi.fn(), search: "" });
    vi.stubGlobal("fetch", vi.fn(async () => new Response("down", { status: 502 })));
    await expect(data.boot("/espace/boot")).rejects.toThrow();
    vi.stubGlobal("fetch", vi.fn(async () => Promise.reject(new TypeError("offline"))));
    await expect(data.boot("/espace/boot")).rejects.toThrow();
    // The dev server names no boot: its fixtures, said to be made up.
    const b = await data.boot(undefined);
    expect(b.live).toBe(false);
  });

  it("sends a signed-out boot to the sign-in", async () => {
    const assign = vi.fn();
    vi.stubGlobal("location", { assign, search: "" });
    vi.stubGlobal("fetch", vi.fn(async () => new Response(null, { status: 401 })));
    await expect(data.boot("/espace/boot")).rejects.toThrow();
    expect(assign).toHaveBeenCalledWith("/login");
  });

  it("offers the conversation when the stream has been down too long", () => {
    const now = 100_000;
    expect(data.streamDown({ open: true, since: 0 }, now)).toBe(false);
    expect(data.streamDown({ open: false, since: now - 5_000 }, now)).toBe(false);
    expect(data.streamDown({ open: false, since: now - data.STREAM_GRACE - 1 }, now)).toBe(true);
  });

  it("follows the stream's health", () => {
    type ES = { onopen?: () => void; onerror?: () => void; readyState: number; addEventListener(): void; removeEventListener(): void; close(): void };
    const all: ES[] = [];
    vi.stubGlobal(
      "EventSource",
      class {
        readyState = 0;
        constructor() {
          all.push(this as unknown as ES);
        }
        addEventListener() {}
        removeEventListener() {}
        close() {}
      },
    );
    const stop = data.stream({ lang: "fr", name: "Eriphe", csrf: "", strings: {}, live: true }, { applyMessages() {}, surfaces: () => [], onAction: () => () => {}, onError: () => () => {} } as never);
    const es = all[all.length - 1];
    es.onopen!();
    expect(data.streamHealth().open).toBe(true);
    const before = Date.now();
    es.onerror!(); // the browser tries again by itself
    const h = data.streamHealth();
    expect(h.open).toBe(false);
    expect(h.since).toBeGreaterThanOrEqual(before);
    es.onopen!();
    expect(data.streamHealth().open).toBe(true);
    stop();
  });

  it("shows the way to the conversation when the page throws while it draws", async () => {
    vi.spyOn(console, "error").mockImplementation(() => {});
    const Broken = () => {
      throw new Error("broken");
    };
    await act(async () =>
      root.render(
        <WordsContext.Provider value={{ t: makeT({ "espace.convo": "Ouvrir la conversation" }), lang: "fr" }}>
          <FailSafe csrf="tok">
            <Broken />
          </FailSafe>
        </WordsContext.Provider>,
      ),
    );
    const a = el.querySelector<HTMLAnchorElement>(".espace-fallback a")!;
    expect(a.getAttribute("href")).toBe("/conversation");
    expect(a.textContent).toBe("Ouvrir la conversation");
    const form = el.querySelector<HTMLFormElement>(".espace-fallback form")!;
    expect(form.getAttribute("action")).toBe("/settings/home");
    expect(form.querySelector<HTMLInputElement>('[name="csrf"]')!.value).toBe("tok");
    expect(form.querySelector<HTMLInputElement>('[name="home"]')!.value).toBe("conversation");
    expect(el.querySelector(".espace-loading")!.classList.contains("failed")).toBe(true);
    vi.restoreAllMocks();
  });
});

describe("the workspace on a phone", () => {
  const phone = (matches: boolean) =>
    vi.stubGlobal("matchMedia", (q: string) => ({ matches, media: q, addEventListener() {}, removeEventListener() {} }));
  const draw = () =>
    act(async () =>
      root.render(
        <WordsContext.Provider value={{ t: makeT({}), lang: "fr" }}>
          <Espace boot={{ lang: "fr", name: "Eriphe", csrf: "", strings: {}, live: false }} />
        </WordsContext.Provider>,
      ),
    );

  it("is one column, and a surface open has the screen, with the way back", async () => {
    phone(true);
    await draw();
    const page = el.querySelector<HTMLElement>(".espace")!;
    expect(page.classList.contains("narrow")).toBe(true);
    expect(page.classList.contains("focused")).toBe(false);
    expect(el.querySelector("main.canvas")!.classList.contains("column")).toBe(true);
    // No widening of a panel on a column; the conversation is a link away.
    expect(el.querySelector('.eriphe-links a[href="/conversation"]')).not.toBeNull();
    expect(el.querySelector(".eriphe-links button")).toBeNull();

    const tiles = el.querySelectorAll("main.canvas article").length;
    const first = el.querySelector<HTMLElement>("main.canvas article")!;
    await act(async () => first.click());
    expect(page.classList.contains("focused")).toBe(true);
    expect(el.querySelector("main.canvas")!.classList.contains("full")).toBe(true);
    expect(el.querySelectorAll("main.canvas article")).toHaveLength(1);
    // The line to write to Eriphe about it stays, and the way back.
    expect(el.querySelector(".eriphe-input input")).not.toBeNull();
    const back = el.querySelector<HTMLButtonElement>(".eriphe-actions button")!;
    await act(async () => back.click());
    expect(page.classList.contains("focused")).toBe(false);
    expect(el.querySelectorAll("main.canvas article")).toHaveLength(tiles);
    expect(el.querySelector("main.canvas")!.classList.contains("full")).toBe(false);
  });

  it("is the grid on a wide screen", async () => {
    phone(false);
    await draw();
    expect(el.querySelector(".espace")!.classList.contains("narrow")).toBe(false);
    expect(el.querySelector("main.canvas")!.classList.contains("column")).toBe(false);
  });
});
