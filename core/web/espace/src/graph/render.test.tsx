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
import { CULL_FROM } from "./cull";
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
  id, title: `Sujet ${id}`, parent, links, weight: 0.6, status: "idle", created: `2026-11-18T10:0${id.length}:00Z`, updated: new Date().toISOString(), version: 1,
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
    expect(bubble("c").style.transform).toMatch(/^translate\(/);
    expect(bubble("c").style.left).toBe("");

    // A page opens: the graph moves for a while; the bubbles are drawn
    // again once (the one open hidden), then not at each frame.
    await act(async () => step(store, openPage, "a", false));
    frame();
    const drawn = vi.mocked(coreShape).mock.calls.length;
    const was = bubble("c").style.transform;
    for (let i = 0; i < 40; i++) frame();
    expect(bubble("c").style.transform).not.toBe(was);
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

// A big graph draws only the bubbles the camera sees, and their links:
// the camera gone far away, none; back, they are drawn again where they are.
describe("a big graph", () => {
  it("draws only what the camera sees", async () => {
    store.setState(empty(), true);
    const many = Array.from({ length: CULL_FROM + 50 }, (_, i) => node(`n${i}`, i % 10 ? `n${i - (i % 10)}` : undefined));
    step(store, applyNodes, { reset: true, nodes: many });
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
    const drawn = () => el.querySelectorAll(".gr-node").length;
    expect(drawn()).toBeGreaterThan(0);
    expect(el.querySelectorAll("line.gr-link").length).toBeGreaterThan(0);

    // The wheel takes the camera far away: nothing is drawn.
    const wheel = (dx: number) => act(() => void el.querySelector(".gr-viewport")!.dispatchEvent(new WheelEvent("wheel", { deltaX: dx, bubbles: true, cancelable: true })));
    wheel(1e6);
    frame();
    expect(drawn()).toBe(0);
    expect(el.querySelectorAll("line.gr-link")).toHaveLength(0);
    // Back: drawn again, each where the simulation has it.
    wheel(-1e6);
    frame();
    expect(drawn()).toBeGreaterThan(0);
    for (const b of el.querySelectorAll<HTMLElement>(".gr-node")) expect(b.style.transform).toMatch(/^translate\(/);

    act(() => root.unmount());
    el.remove();
    store.setState(empty(), true);
  });
});

// A satellite orbits a bubble only while a skill maintains its node, as
// eriphe-core says it (maintained, computed from its skills); its core's
// words name them.
describe("the satellite", () => {
  it("shows only on a node a skill maintains", async () => {
    store.setState(empty(), true);
    step(store, applyNodes, { reset: true, nodes: [{ ...node("m"), maintained: ["tri-mails"] }, node("w"), { ...node("e"), maintained: [] }] });
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
    frame();
    const bubble = (id: string) => el.querySelector<HTMLElement>(`.gr-node[data-node="${id}"]`)!;
    expect(bubble("m").querySelector(".gr-sat")).not.toBeNull();
    expect(bubble("m").classList.contains("maintained")).toBe(true);
    expect(bubble("m").querySelector(".gr-core")?.getAttribute("title")).toBe("Entretenu par tri-mails");
    for (const id of ["w", "e"]) {
      expect(bubble(id).querySelector(".gr-sat")).toBeNull();
      expect(bubble(id).classList.contains("maintained")).toBe(false);
    }
    // Its skill gone, the satellite goes.
    await act(async () => step(store, applyNodes, { nodes: [{ ...node("m"), version: 2 }] }));
    frame();
    expect(bubble("m").querySelector(".gr-sat")).toBeNull();

    act(() => root.unmount());
    el.remove();
    store.setState(empty(), true);
  });
});
