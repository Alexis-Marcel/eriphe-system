import { describe, expect, it } from "vitest";
import type { GraphNode } from "../data/api";
import { radiusOf } from "./model";

const node = (id: string, weight: number, parent?: string): GraphNode => ({
  id, title: id, parent, weight, status: "idle", created: "", updated: "", version: 1,
});

// A bubble's size is the weight eriphe-core computed (node_weight.go),
// never one of its own: the same weight, the same size; a subject bigger
// than its bubbles.
describe("a bubble's size", () => {
  it("follows the core's weight", () => {
    const nodes = { a: node("a", 1), b: node("b", 0.5, "a"), c: node("c", 1.2, "a") };
    expect(radiusOf(nodes.a, nodes)).toBe(38);
    expect(radiusOf(nodes.b, nodes)).toBe(15.5);
    expect(radiusOf(nodes.c, nodes)).toBeGreaterThan(radiusOf(nodes.b, nodes));
    expect(radiusOf({ ...nodes.b, status: "waits" }, nodes)).toBe(15.5); // its status is in its weight already
  });
});
