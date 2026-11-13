// @vitest-environment jsdom
// (The page imports the A2UI renderer, which needs a DOM to load.)
import { afterEach, describe, expect, it, vi } from "vitest";
import * as data from "./data";
import { closedIn, leftGrid, leftUnopened, placedAt, tourOf } from "./Espace";
import { place } from "./engine";
import type { Layout, Obj } from "./types";

// The page keeps no rule: what the user says goes to Eriphe with the module
// open, what they do goes to eriphe-core as gestures, and a walkthrough
// Eriphe starts arrives on the event stream.
describe("workspace", () => {
  afterEach(() => vi.unstubAllGlobals());

  const start = async () => {
    const fetch = vi.fn(async () => new Response(null, { status: 204 }));
    vi.stubGlobal("fetch", fetch);
    vi.stubGlobal("location", { assign: vi.fn() });
    await data.load({ lang: "fr", name: "Eriphe", csrf: "tok", strings: {}, live: false });
    return fetch;
  };

  it("sends a message with the ref of the module open", async () => {
    const fetch = await start();
    expect(await data.send("montre-m'en moins", "mail:m-1")).toBe(true);
    const [url, init] = fetch.mock.calls[0] as unknown as [string, RequestInit];
    expect(url).toBe("/messages");
    expect((init.headers as Record<string, string>)["X-CSRF-Token"]).toBe("tok");
    const body = init.body as URLSearchParams;
    expect(body.get("text")).toBe("montre-m'en moins");
    expect(body.get("about")).toBe("mail:m-1");

    await data.send("bonjour", null);
    expect((fetch.mock.calls[1] as unknown as [string, RequestInit])[1].body).toEqual(new URLSearchParams({ text: "bonjour" }));
  });

  it("posts a gesture", async () => {
    const fetch = await start();
    expect(await data.gesture("opened", "mail:m-1", "Les nouveautés")).toBe(true);
    const [url, init] = fetch.mock.calls[0] as unknown as [string, RequestInit];
    expect(url).toBe("/api/espace/gesture");
    expect(init.method).toBe("POST");
    expect((init.headers as Record<string, string>)["X-CSRF-Token"]).toBe("tok");
    expect(JSON.parse(init.body as string)).toEqual({ what: "opened", ref: "mail:m-1", title: "Les nouveautés" });
  });

  it("posts a close, a move and a resize of a surface, with its place", async () => {
    const fetch = await start();
    await data.gesture("closed", "surface:week", "");
    await data.gesture("moved", "surface:week", "", { size: "l", col: 8, row: 5 });
    await data.gesture("resized", "surface:week", "", { size: "xl", col: 6, row: 3 });
    const bodies = fetch.mock.calls.map((c) => JSON.parse((c as unknown as [string, RequestInit])[1].body as string));
    expect(bodies).toEqual([
      { what: "closed", ref: "surface:week", title: "" },
      { what: "moved", ref: "surface:week", title: "", size: "l", col: 8, row: 5 },
      { what: "resized", ref: "surface:week", title: "", size: "xl", col: 6, row: 3 },
    ]);
  });

  const grid: Layout = {
    eriphe: "", by: "composer", ambient: "calm", focus: "week",
    items: [
      { id: "week", title: "La semaine", size: "m", col: 0, row: 0, w: 6, h: 2 },
      { id: "pay", title: "Paiement", size: "s", col: 6, row: 0, w: 3, h: 1 },
    ],
    chips: [{ id: "news", title: "Nouvelles" }],
  };

  it("tells which tiles left the grid unopened, not those opened or closed", () => {
    const now = { ...grid, items: [grid.items[1]] };
    expect(leftGrid(["week", "pay", "gone", "shut", "seen"], now, new Set(["seen"]), new Set(["shut"]))).toEqual(["week", "gone"]);
  });

  it("shows a move, a resize and a close at once", () => {
    const moved = placedAt(grid, "pay", { size: "l", col: 8, row: 5 });
    expect(moved.items[1]).toEqual({ id: "pay", title: "Paiement", size: "l", col: 8, row: 5, w: 4, h: 3, pinned: true });
    const closed = closedIn(grid, "week");
    expect(closed.items.map((i) => i.id)).toEqual(["pay"]);
    expect(closed.focus).toBeUndefined();
    expect(closed.chips).toEqual([{ id: "news", title: "Nouvelles" }, { id: "week", title: "La semaine", closed: true }]);
  });

  it("hands a walkthrough from the event stream", () => {
    const handlers: Record<string, (e: MessageEvent) => void> = {};
    class FakeSource {
      addEventListener(name: string, fn: (e: MessageEvent) => void) { handlers[name] = fn; }
      removeEventListener() {}
      close() {}
    }
    vi.stubGlobal("EventSource", FakeSource);
    const walks: data.Walk[] = [];
    const stop = data.follow(() => {}, (w) => walks.push(w));
    handlers.walkthrough({ data: JSON.stringify({ id: 3, refs: ["mail:m-1", "agenda:2026-11-12"] }) } as MessageEvent);
    handlers.walkthrough({ data: "not json" } as MessageEvent);
    expect(walks).toEqual([{ id: 3, refs: ["mail:m-1", "agenda:2026-11-12"] }]);
    stop();
  });

  it("tells what left the screen unopened", () => {
    const comp = { mode: "side" as const, eriphe: "", items: [{ ref: "mail:2", weight: 3, why: "" }], chips: ["mail:3"] };
    expect(leftUnopened(["mail:1", "mail:2", "mail:3", "mail:4"], comp, new Set(["mail:4"]))).toEqual(["mail:1"]);
  });

  it("walks through what matters on the screen when Eriphe names nothing", () => {
    const o = (id: string): Obj => ({ ref: `task:${id}`, kind: "task", title: id });
    const store = new Map([o("a"), o("b"), o("c")].map((x) => [x.ref, x]));
    const { placed } = place({ mode: "side", eriphe: "", items: [{ ref: "task:a", weight: 4, why: "" }, { ref: "task:b", weight: 1, why: "" }, { ref: "task:c", weight: 2, why: "" }] }, store);
    expect(tourOf(placed)).toEqual(["task:a", "task:c"]);
  });
});
