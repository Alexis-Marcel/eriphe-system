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
import { LOD } from "./lod";
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
  // jsdom paints nothing: a canvas has no context (paint.ts copes).
  HTMLCanvasElement.prototype.getContext = (() => null) as never;
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

// A graph of bubbles alone (below LOD.from nodes) has its sizes by life
// and its nodes asleep too: one idle for weeks is a smaller, pale bubble,
// still a button the keyboard reaches; a root never sleeps; a link takes
// its ends' life, those of a node open lit.
describe("a small graph's life", () => {
  it("draws a node asleep as a smaller, pale bubble, a root awake, its links faint", async () => {
    store.setState(empty(), true);
    const old = new Date(Date.now() - 90 * 86_400_000).toISOString();
    const aged = (x: GraphNode): GraphNode => ({ ...x, updated: old });
    step(store, applyNodes, { reset: true, nodes: [aged(node("r")), aged(node("r1", "r")), node("r2", "r"), aged(node("x", undefined, ["r1"])), node("y", undefined, ["r2"])] });
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
    const size = (id: string) => parseFloat(bubble(id).querySelector<HTMLElement>(".gr-core")!.style.width);
    expect(el.querySelector("canvas.gr-canvas")).toBeNull();
    // r1 asleep: smaller than r2, its sister awake, and pale; still a button
    expect(bubble("r1").classList.contains("sleeps")).toBe(true);
    expect(bubble("r2").classList.contains("sleeps")).toBe(false);
    expect(size("r1")).toBeLessThan(size("r2"));
    expect(bubble("r1").querySelector<HTMLButtonElement>("button.gr-core")!.tabIndex).not.toBe(-1);
    // the root, as old, awake, its size whole
    expect(bubble("r").classList.contains("sleeps")).toBe(false);
    // the links: r–r1 and x–r1 hardly there (the root takes r1's life),
    // r–r2 and y–r2 drawn
    const lines = (sel: string) => el.querySelectorAll(`line.gr-link${sel}`).length;
    expect(lines("")).toBe(4);
    expect(lines('[data-life="2"]')).toBe(2);
    expect(lines(":not([data-life])")).toBe(2);
    expect(lines("[data-lit]")).toBe(0);
    // r1 open: its two links lit
    await act(async () => step(store, openPage, "r1", false));
    frame();
    expect(lines("[data-lit]")).toBe(2);

    act(() => root.unmount());
    el.remove();
    store.setState(empty(), true);
  });
});

// A big graph paints its nodes on a canvas and draws as bubbles only
// those big on the screen, and no line of its links in the page: framed
// whole, a few; zoomed in, those the camera sees, the cap at most; the
// camera gone far away, none; back, drawn again where they are.
describe("a big graph", () => {
  it("draws as bubbles only the nodes big on the screen", async () => {
    store.setState(empty(), true);
    const many = Array.from({ length: LOD.from + 50 }, (_, i) => node(`n${i}`, i % 10 ? `n${i - (i % 10)}` : undefined));
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
    for (let i = 0; i < 30; i++) frame();
    const drawn = () => el.querySelectorAll(".gr-node").length;
    expect(el.querySelector("canvas.gr-canvas")).not.toBeNull();
    expect(el.querySelectorAll("line.gr-link")).toHaveLength(0);
    expect(drawn()).toBeLessThanOrEqual(LOD.cap);

    const vp = el.querySelector(".gr-viewport")!;
    const wheel = (init: WheelEventInit) => act(() => void vp.dispatchEvent(new WheelEvent("wheel", { bubbles: true, cancelable: true, clientX: innerWidth / 2, clientY: innerHeight / 2, ...init })));
    // Zoomed in: the bubbles the camera sees, as many as the budget lets
    // in a frame, then the rest.
    wheel({ deltaY: -1000, ctrlKey: true });
    for (let i = 0; i < 10; i++) frame();
    const zoomed = drawn();
    expect(zoomed).toBeGreaterThan(0);
    expect(zoomed).toBeLessThanOrEqual(LOD.cap);
    // The wheel takes the camera far away: nothing is drawn.
    wheel({ deltaX: 1e6 });
    frame();
    expect(drawn()).toBe(0);
    // Back: drawn again, each where the simulation has it.
    wheel({ deltaX: -1e6 });
    for (let i = 0; i < 10; i++) frame();
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

// A bubble draws only the elements that show something: a node with
// nothing running and no code has no ring and no face; a root has its
// orbit and its code, a done node its ✓, each one element. Its motion is
// CSS's: a hover draws no bubble again.
describe("a light bubble", () => {
  const mount = async () => {
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
    const done = () => {
      act(() => root.unmount());
      el.remove();
      store.setState(empty(), true);
    };
    return { bubble, done };
  };
  // (born or not: a node seen by an earlier test is not born again)
  const shape = (b: HTMLElement) => [...b.querySelectorAll("*")].map((e) => e.tagName.toLowerCase() + [...e.classList].filter((c) => c !== "fresh").map((c) => `.${c}`).join(""));

  it("draws only what shows", async () => {
    store.setState(empty(), true);
    step(store, applyNodes, { reset: true, nodes: [node("a"), node("a1", "a"), { ...node("a2", "a"), status: "done" }, { ...node("a3", "a"), status: "running" }] });
    const { bubble, done } = await mount();
    expect(shape(bubble("a1"))).toEqual(["div.gr-bub", "button.gr-core", "span.gr-label", "b"]);
    expect(shape(bubble("a"))).toEqual(["div.gr-bub", "svg.gr-ring", "circle.gr-orbit", "button.gr-core", "span.gr-code.gl-in", "span.gr-label", "b"]);
    expect(shape(bubble("a2"))).toEqual(["div.gr-bub", "button.gr-core", "span.gr-glyph.gl-in", "span.gr-label", "b"]);
    expect(bubble("a2").querySelector(".gr-glyph")?.textContent).toBe("✓");
    expect(shape(bubble("a3"))).toEqual(["div.gr-bub", "svg.gr-ring", "circle.gr-ring-run", "button.gr-core", "span.gr-label", "b"]);
    // the core's shape, written on it (CSS morphs it)
    const core = bubble("a1").querySelector<HTMLElement>(".gr-core")!;
    expect(core.style.width).toBe(core.style.height);
    expect(core.style.borderRadius).toBe(`${parseFloat(core.style.width) / 2}px`);

    // A hover fades the others by their classes: no bubble drawn again.
    const drawn = vi.mocked(coreShape).mock.calls.length;
    expect(drawn).toBeGreaterThan(0);
    act(() => void core.dispatchEvent(new MouseEvent("pointerover", { bubbles: true })));
    expect(document.querySelector(".gr-viewport")?.classList.contains("hovering")).toBe(true);
    act(() => void core.dispatchEvent(new MouseEvent("pointerout", { bubbles: true, relatedTarget: document.body })));
    expect(vi.mocked(coreShape).mock.calls.length).toBe(drawn);
    done();
  });

  it("cross-fades its face as it becomes a tile and back; a ✓ come later shows at once", async () => {
    store.setState(empty(), true);
    step(store, applyNodes, { reset: true, nodes: [node("a"), node("a1", "a")] });
    const { bubble, done } = await mount();
    const faces = () => [...bubble("a").querySelectorAll(".gl-in")].map((e) => e.className);
    // born with its code: no fade
    expect(faces()).toEqual(["gr-code gl-in"]);
    vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout"] });
    try {
      const glance = { label: "Conso", unit: "percent" as const, value: 2.6, ref: 2, delta: 30, threshold: 10, series: [2, 2.6], shown: true };
      await act(async () => step(store, applyNodes, { nodes: [{ ...node("a"), version: 2, glance }] }));
      expect(bubble("a").classList.contains("tiled")).toBe(true);
      expect(faces()).toEqual(["gr-code gl-in out", "gl-in in tile"]);
      act(() => void vi.advanceTimersByTime(100));
      expect(faces()).toEqual(["gl-in in tile"]);
      act(() => void vi.advanceTimersByTime(300));
      expect(faces()).toEqual(["gl-in"]);
      // and back
      await act(async () => step(store, applyNodes, { nodes: [{ ...node("a"), version: 3 }] }));
      expect(faces()).toEqual(["gl-in out", "gr-code gl-in in code"]);
      act(() => void vi.advanceTimersByTime(400));
      expect(faces()).toEqual(["gr-code gl-in"]);
      // a child that was a tile, done later: its ✓ at once
      await act(async () => step(store, applyNodes, { nodes: [{ ...node("a1", "a"), version: 2, glance }] }));
      await act(async () => step(store, applyNodes, { nodes: [{ ...node("a1", "a"), version: 3 }] }));
      act(() => void vi.advanceTimersByTime(400));
      await act(async () => step(store, applyNodes, { nodes: [{ ...node("a1", "a"), version: 4, status: "done" }] }));
      expect([...bubble("a1").querySelectorAll(".gl-in")].map((e) => e.className)).toEqual(["gr-glyph gl-in"]);
    } finally {
      vi.useRealTimers();
      done();
    }
  });
});
