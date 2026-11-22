// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { GraphNode } from "./api";

const sent: unknown[] = [];
vi.mock("./api", () => ({ putView: (v: unknown) => (sent.push(v), Promise.resolve()) }));

const { keep, KEEP_AFTER } = await import("./keep");
const { applyNodes, closePage, makeStore, openPage } = await import("./store");

const node = (id: string, more: Partial<GraphNode> = {}): GraphNode => ({
  id, title: id, lifetime: "standing", status: "idle", created: "", updated: "", version: 1, ...more,
});
const glance = { label: "Conso", unit: "percent" as const, value: 2.6, ref: 2, delta: 30, threshold: 10, series: [2, 2.6], shown: true };

describe("what is kept of the workspace", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    sent.length = 0;
  });
  afterEach(() => vi.useRealTimers());

  it("sends the view a moment after it changes, and at once when a tile's page opens", () => {
    const store = makeStore();
    store.setState(applyNodes(store.getState(), { reset: true, nodes: [node("plain"), node("tile", { glance }), node("seen", { glance: { ...glance, seen: true } })] }));
    const stop = keep(store);
    store.setState(openPage(store.getState(), "plain", false));
    expect(sent).toHaveLength(0);
    vi.advanceTimersByTime(KEEP_AFTER);
    expect(sent).toHaveLength(1);
    store.setState(openPage(store.getState(), "seen", false));
    expect(sent).toHaveLength(1);
    // the tile's page: at once, though it closes right after
    store.setState(openPage(store.getState(), "tile", false));
    expect(sent).toHaveLength(2);
    expect((sent[1] as { open: string[] }).open).toContain("tile");
    store.setState(closePage(store.getState(), "tile"));
    vi.advanceTimersByTime(KEEP_AFTER);
    expect((sent[sent.length - 1] as { open: string[] }).open).not.toContain("tile");
    stop();
  });
});
