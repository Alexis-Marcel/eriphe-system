// @vitest-environment jsdom
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterAll, beforeAll, describe, expect, it, vi } from "vitest";
import { setReads, type GraphNode } from "../data/api";
import { applyNodes, empty, openPage, step, store } from "../data/store";
import { makeT, WordsContext } from "../i18n";
import { OrbProvider } from "../orb/useOrb";
import strings from "../strings.json";
import { coreShape } from "./glance";
import { Graph } from "./Graph";

// The graph draws a bubble again only when what it shows changes: each
// frame of the simulation moves the bubbles on their elements, it does
// not draw them again. A bubble's drawing calls coreShape once: counted.
vi.mock("./glance", async (importOriginal) => {
  const m = await importOriginal<typeof import("./glance")>();
  return { ...m, coreShape: vi.fn(m.coreShape) };
});

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const node = (id: string, parent?: string, links?: string[]): GraphNode => ({
  id, title: `Sujet ${id}`, parent, links, lifetime: "work", status: "idle", created: `2026-11-18T10:0${id.length}:00Z`, updated: new Date().toISOString(), version: 1,
});

// The frames, played by hand.
let frames: FrameRequestCallback[] = [];
const frame = () =>
  act(() => {
    const now = frames;
    frames = [];
    for (const f of now) f(performance.now());
  });

beforeAll(() => {
  vi.stubGlobal("matchMedia", () => ({ matches: false, addEventListener() {}, removeEventListener() {} }));
  vi.stubGlobal("ResizeObserver", class { observe() {} disconnect() {} unobserve() {} });
  vi.stubGlobal("requestAnimationFrame", (f: FrameRequestCallback) => frames.push(f));
  vi.stubGlobal("cancelAnimationFrame", () => undefined);
  setReads({ page: async () => null, trace: async () => [], file: async () => null, open: async () => ({ error: "" }) });
});
afterAll(() => {
  vi.unstubAllGlobals();
  setReads(null);
});

describe("the graph's frames", () => {
  it("move the bubbles without drawing them again", async () => {
    store.setState(empty(), true);
    step(store, applyNodes, { reset: true, nodes: [node("a"), node("a1", "a"), node("a2", "a"), node("b", undefined, ["a"]), node("c"), node("cc", "c")] });
    const el = document.createElement("div");
    document.body.append(el);
    const root = createRoot(el);
    await act(async () =>
      root.render(
        <WordsContext.Provider value={{ t: makeT(strings.fr), lang: "fr" }}>
          <OrbProvider>
            <Graph live />
          </OrbProvider>
        </WordsContext.Provider>,
      ),
    );
    for (let i = 0; i < 5; i++) frame();
    const bubble = (id: string) => el.querySelector<HTMLElement>(`.gr-node[data-node="${id}"]`)!;
    expect(bubble("c").style.left).not.toBe("");

    // A page opens: the graph moves for a while; the bubbles are drawn
    // again once (dimmed, the one open hidden), then not at each frame.
    await act(async () => step(store, openPage, "a", false));
    frame();
    const drawn = vi.mocked(coreShape).mock.calls.length;
    const was = [bubble("c").style.left, bubble("c").style.top].join();
    for (let i = 0; i < 40; i++) frame();
    expect([bubble("c").style.left, bubble("c").style.top].join()).not.toBe(was);
    expect(vi.mocked(coreShape).mock.calls.length).toBe(drawn);
    // The links follow too, straight on their elements: one line per pair.
    expect(el.querySelectorAll("line.gr-link")).toHaveLength(4);
    expect(el.querySelector("path.gr-link")).toBeNull();
    for (const l of el.querySelectorAll("line.gr-link")) expect(l.getAttribute("x1")).not.toBeNull();

    act(() => root.unmount());
    el.remove();
    store.setState(empty(), true);
  });
});
