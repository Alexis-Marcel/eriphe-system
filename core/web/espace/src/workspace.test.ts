// @vitest-environment jsdom
// (The page imports the A2UI renderer, which needs a DOM to load.)
import { afterEach, describe, expect, it, vi } from "vitest";
import * as data from "./data";
import { closedIn, leftGrid, placedAt } from "./Espace";
import type { Layout } from "./types";

// The page keeps no rule: what the user says goes to Eriphe with the
// surface open, what they do goes to eriphe-core as gestures.
describe("workspace", () => {
  afterEach(() => vi.unstubAllGlobals());

  // The boot gives the CSRF token every request carries.
  const start = async () => {
    vi.stubGlobal("location", { assign: vi.fn(), search: "" });
    vi.stubGlobal("fetch", vi.fn(async () => Response.json({ lang: "fr", name: "Eriphe", csrf: "tok", strings: {} })));
    await data.boot("/espace/boot");
    const fetch = vi.fn(async () => new Response(null, { status: 204 }));
    vi.stubGlobal("fetch", fetch);
    return fetch;
  };

  it("sends a message with the surface open", async () => {
    const fetch = await start();
    expect(await data.send("montre-m'en moins", "surface:week")).toBe(true);
    const [url, init] = fetch.mock.calls[0] as unknown as [string, RequestInit];
    expect(url).toBe("/messages");
    expect((init.headers as Record<string, string>)["X-CSRF-Token"]).toBe("tok");
    const body = init.body as URLSearchParams;
    expect(body.get("text")).toBe("montre-m'en moins");
    expect(body.get("about")).toBe("surface:week");

    await data.send("bonjour", null);
    expect((fetch.mock.calls[1] as unknown as [string, RequestInit])[1].body).toEqual(new URLSearchParams({ text: "bonjour" }));
  });

  it("posts a close, a move and a resize of a surface, with its place", async () => {
    const fetch = await start();
    await data.gesture("ignored", "surface:pay");
    await data.gesture("closed", "surface:week");
    await data.gesture("moved", "surface:week", { size: "l", col: 8, row: 5 });
    await data.gesture("resized", "surface:week", { size: "xl", col: 6, row: 3 });
    const bodies = fetch.mock.calls.map((c) => JSON.parse((c as unknown as [string, RequestInit])[1].body as string));
    expect(bodies).toEqual([
      { what: "ignored", ref: "surface:pay" },
      { what: "closed", ref: "surface:week" },
      { what: "moved", ref: "surface:week", size: "l", col: 8, row: 5 },
      { what: "resized", ref: "surface:week", size: "xl", col: 6, row: 3 },
    ]);
    expect((fetch.mock.calls[0] as unknown as [string, RequestInit])[1].headers).toMatchObject({ "X-CSRF-Token": "tok" });
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
});
