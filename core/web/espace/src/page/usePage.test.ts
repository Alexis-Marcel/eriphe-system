import { describe, expect, it } from "vitest";
import type { NodePage, ProposalView } from "../data/api";
import { concerns } from "./usePage";

const card = (ref: string): ProposalView => ({ ref, card: 1, state: "pending", text: ref, verb: "Faire" });

// A page as eriphe-core sends it: a card at its foot, one decided, a card
// block, a List whose row a card waits on.
const page = {
  node: { id: "n1" },
  cards: [card("card:1")],
  settled: [card("card:2")],
  parts: [{ block: { name: "c", card: "card:3" } }, { block: { name: "l", lists: [{ component: "x", source: "s", items: [{ n: 0, id: "m", card: "card:4" }] }] } }],
} as unknown as NodePage;

describe("a page reads itself again", () => {
  it("only when proposals it shows, or its node's cards, moved", () => {
    const moved = (refs: string[], more: { nodes?: string[]; all?: boolean } = {}) => ({ n: 1, refs, ...more });
    for (const ref of ["card:1", "card:2", "card:3", "card:4"]) expect(concerns(moved([ref]), "n1", page), ref).toBe(true);
    // a new card of its node, in no page yet
    expect(concerns(moved(["card:9"], { nodes: ["n1"] }), "n1", null)).toBe(true);
    // the stream fell behind: everything
    expect(concerns(moved([], { all: true }), "n1", page)).toBe(true);
    // another page's card: nothing
    expect(concerns(moved(["card:9"], { nodes: ["n2"] }), "n1", page)).toBe(false);
    expect(concerns(moved(["card:1"]), "n1", null)).toBe(false);
  });
});
