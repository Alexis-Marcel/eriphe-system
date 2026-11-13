// @vitest-environment jsdom
// (The page imports the A2UI renderer, which needs a DOM to load.)
import { afterEach, describe, expect, it, vi } from "vitest";
import * as data from "./data";
import { closedIn, focusedIn, layoutLine, leftGrid, placedAt, unfocused } from "./Espace";
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
    expect(body.get("from")).toBe("workspace"); // typed in the workspace: Eriphe may answer on stage

    await data.send("bonjour", null);
    expect((fetch.mock.calls[1] as unknown as [string, RequestInit])[1].body).toEqual(new URLSearchParams({ text: "bonjour", from: "workspace" }));
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
    const now = { ...grid, focus: undefined, items: [grid.items[1]] };
    expect(leftGrid(["week", "pay", "gone", "shut", "seen"], now, new Set(["seen"]), new Set(["shut"]))).toEqual(["week", "gone"]);
  });

  it("does not take what a focus folds, or gives back, for tiles left aside", () => {
    const before = ["week", "pay", "news"];
    const around = { ...grid, focus: "pay", items: [grid.items[1]] };
    expect(leftGrid(before, around, new Set(), new Set())).toEqual([]);
    const back = { ...grid, focus: undefined, items: [grid.items[0]] };
    expect(leftGrid(["pay"], back, new Set(), new Set(), "pay")).toEqual([]);
    expect(leftGrid(["pay"], back, new Set(), new Set())).toEqual(["pay"]);
  });

  it("shows a move, a resize and a close at once", () => {
    const moved = placedAt(grid, "pay", { size: "l", col: 8, row: 5 });
    expect(moved.items[1]).toEqual({ id: "pay", title: "Paiement", size: "l", col: 8, row: 5, w: 4, h: 3, pinned: true });
    const closed = closedIn(grid, "week");
    expect(closed.items.map((i) => i.id)).toEqual(["pay"]);
    expect(closed.focus).toBeUndefined();
    expect(closed.chips).toEqual([{ id: "news", title: "Nouvelles" }, { id: "week", title: "La semaine", closed: true }]);
  });

  it("opens a surface at once, shows the brain's line about it, and goes back to the layout before", () => {
    const before: Layout = { ...grid, focus: undefined, eriphe: "Ta journée." };
    const open = focusedIn(before, "pay");
    expect(open.focus).toBe("pay");
    expect(layoutLine(open)).toBe("Ta journée.");
    // eriphe-core recomposes around it, then the brain's line comes.
    const around: Layout = { ...open, eriphe: "Autour du paiement.", items: [grid.items[1], { ...grid.items[0], related: true }] };
    expect(layoutLine(around)).toBe("Autour du paiement.");
    const said = { ...around, say: "Il attend depuis hier." };
    expect(layoutLine(said)).toBe("Il attend depuis hier.");
    // Another opened: its line is not the last one's.
    expect(focusedIn(said, "week").say).toBeUndefined();
    // Back: the layout before, kept by the page with nothing behind it.
    expect(unfocused(said, before)).toBe(before);
    // With eriphe-core behind, nothing open at once, nothing marked, no line, until it gives the one before back.
    const back = unfocused(said, null);
    expect(back.focus).toBeUndefined();
    expect(back.items.some((i) => i.related)).toBe(false);
    expect(layoutLine(back)).toBe("Autour du paiement.");
    expect(layoutLine({ ...back, say: "Il attend depuis hier." })).toBe("Autour du paiement.");
  });
});
