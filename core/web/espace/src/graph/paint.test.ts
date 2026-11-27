// @vitest-environment jsdom
import { beforeAll, describe, expect, it } from "vitest";
import { FADE, Painter, scene, type SceneNode, type State } from "./paint";

// The canvas paints a disc for each node on screen but those drawn as
// bubbles (once their fade is over) and those covered by their page; a
// node that waits gets its violet ring; the labels, the biggest first.

beforeAll(() => {
  HTMLCanvasElement.prototype.getContext = (() => null) as never;
});

// A context that counts what is painted.
function counting() {
  const n = { arc: 0, rect: 0, text: [] as string[], strokes: [] as string[] };
  const ctx = new Proxy(
    { arc: () => n.arc++, rect: () => n.rect++, fillText: (t: string) => n.text.push(t), stroke() { n.strokes.push(String((ctx as { strokeStyle?: string }).strokeStyle)); } } as Record<string, unknown>,
    {
      get: (o, k) => (k in o ? o[k as string] : () => undefined),
      set: (o, k, v) => ((o[k as string] = v), true),
    },
  );
  return { n, ctx: ctx as unknown as CanvasRenderingContext2D };
}

const node = (id: string, r: number, x: Partial<SceneNode> = {}): SceneNode => ({ id, r, depth: 1, light: 1, status: "idle", fam: 0, title: `t ${id}`, code: "", ...x });
const view = { x: 0, y: 0, k: 1, w: 1000, h: 600 };
const none: State = { mounted: new Map(), swapped: new Map(), open: new Set(), near: null, nearLinks: null };

describe("the canvas", () => {
  const s = scene([node("a", 30, { depth: 0 }), node("b", 10), node("c", 10, { status: "waits" }), node("far", 10)], [["a", "b"]]);
  const xy = Float32Array.from([100, 100, 200, 100, 300, 100, 5000, 100]);

  it("paints a disc for each node on screen, the labels of the biggest first", () => {
    const { n, ctx } = counting();
    const p = new Painter(document.createElement("canvas"), ctx);
    p.resize(1000, 600);
    p.paint(s, xy, view, none, 0);
    // a, b, c as discs, and c's ring; far is off screen
    expect(n.arc).toBe(4);
    expect(n.text[0]).toBe("t a");
  });

  it("leaves a node to its bubble once faded, and to its page", () => {
    const { n, ctx } = counting();
    const p = new Painter(document.createElement("canvas"), ctx);
    p.resize(1000, 600);
    const fading = p.paint(s, xy, view, { ...none, mounted: new Map([["b", 1]]), swapped: new Map([["b", 1000]]), open: new Set(["a"]) }, 1000 + FADE / 2);
    expect(fading).toBe(true);
    expect(n.arc).toBe(3); // b fading, c and its ring
    n.arc = 0;
    expect(p.paint(s, xy, view, { ...none, mounted: new Map([["b", 1]]), swapped: new Map([["b", 1000]]), open: new Set(["a"]) }, 1000 + FADE)).toBe(false);
    expect(n.arc).toBe(2); // c and its ring
    expect(n.text).not.toContain("t a");
  });
});
