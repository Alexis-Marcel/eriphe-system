// @vitest-environment jsdom
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import type { Glance, GraphNode } from "../data/api";
import { CAP, coreShape, pickTiles, sparkPath, TILE, variation } from "./glance";
import { TileFace } from "./Tile";

const glance = (delta: number, threshold = 2, o: Partial<Glance> = {}): Glance => ({
  label: "PEA",
  unit: "percent",
  value: 100 + delta,
  ref: 100,
  delta,
  threshold,
  series: [100, 100 + delta / 2, 100 + delta],
  shown: Math.abs(delta) >= threshold,
  ...o,
});
const node = (id: string, g?: Glance, o: Partial<GraphNode> = {}): GraphNode => ({
  id,
  title: id,
  lifetime: "standing",
  state: "idle",
  created: "",
  updated: "",
  version: 1,
  glance: g,
  ...o,
});

describe("which nodes are tiles", () => {
  it("are those whose glance is shown and not seen, the strongest first, at most three", () => {
    const nodes = [
      node("calm", glance(1)),
      node("pea", glance(3)), // 1.5
      node("prod", glance(40, 10)), // 4
      node("conso", glance(-5, 2)), // 2.5
      node("tools", glance(2.6, 2)), // 1.3
      node("none"),
    ];
    expect(CAP).toBe(3);
    expect(pickTiles(nodes, [], [])).toEqual(["prod", "conso", "pea"]);
    // opened: no longer a tile, the fourth takes its place
    expect(pickTiles(nodes, ["prod"], [])).toEqual(["conso", "pea", "tools"]);
  });

  it("never makes a tile of a node that waits, nor of one seen", () => {
    const nodes = [node("mails", glance(5), { waits: true }), node("asks", glance(5), { state: "waiting" }), node("seen", glance(5, 2, { seen: true })), node("pea", glance(3))];
    expect(pickTiles(nodes, [], [])).toEqual(["pea"]);
  });

  it("keeps a tile against a newcomer not clearly stronger", () => {
    const nodes = [node("a", glance(3)), node("b", glance(3.2)), node("c", glance(3.4)), node("d", glance(3.6))];
    expect(pickTiles(nodes, [], [])).toEqual(["d", "c", "b"]);
    // a was a tile (1.5, ×1.2 = 1.8): b (1.6) is not a fifth stronger, a stays
    expect(pickTiles(nodes, [], ["a", "c", "d"])).toEqual(["d", "c", "a"]);
    // twice stronger, a newcomer wins
    const strong = [...nodes, node("e", glance(8))];
    expect(pickTiles(strong, [], ["a", "c", "d"])[0]).toBe("e");
    expect(pickTiles(strong, [], ["a", "c", "d"])).toHaveLength(3);
  });
});

describe("the morph", () => {
  it("takes the core from its circle to the tile's rounded rectangle, and back; at once with less motion", () => {
    expect(coreShape(TILE, 35, false).to).toEqual({ width: 148, height: 60, borderRadius: 14 });
    expect(coreShape(undefined, 35, false).to).toEqual({ width: 70, height: 70, borderRadius: 35 });
    expect(coreShape(TILE, 35, false).move).toMatchObject({ type: "spring" });
    expect(coreShape(TILE, 35, true).move).toEqual({ duration: 0 });
  });
});

describe("a tile's words", () => {
  it("writes the variation in the page's language, with its arrow", () => {
    expect(variation(glance(3.24), "fr")).toEqual({ up: true, arrow: "▲", text: "3,2 %" });
    expect(variation(glance(-12.4), "fr")).toEqual({ up: false, arrow: "▼", text: "12 %" });
    expect(variation(glance(-3.24), "en")).toEqual({ up: false, arrow: "▼", text: "3.2%" });
    expect(variation(glance(38, 10, { unit: "amount" }), "fr").text).toBe("38");
    expect(variation(glance(2.5, 1, { unit: "amount" }), "fr").text).toBe("2,5");
  });

  it("draws its recent values on a scale that holds the reference", () => {
    expect(sparkPath(glance(3, 2, { series: [103] }), 124, 18)).toBeNull();
    const s = sparkPath(glance(3, 2, { series: [101, 103] }), 124, 18)!;
    // the reference (100) is the bottom: 101 sits a third up, 103 at the top
    expect(s.line).toBe("M0.0,11.3L124.0,2.0");
    expect(s.last).toEqual([124, 2]);
  });

  it("shows the label, the variation and the line, in neutral greys", () => {
    const el = document.createElement("div");
    el.innerHTML = renderToStaticMarkup(<TileFace g={glance(3.2)} lang="fr" />);
    expect(el.querySelector(".gl-label")?.textContent).toBe("PEA");
    expect(el.querySelector(".gl-delta")?.textContent).toBe("▲3,2 %");
    expect(el.querySelector(".gl-spark path.gl-line")).not.toBeNull();
    expect(el.querySelector(".good, .bad")).toBeNull();
    expect(TILE).toEqual([148, 60]);
  });
});
