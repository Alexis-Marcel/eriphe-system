import { describe, expect, it } from "vitest";
import type { GraphNode } from "../data/api";
import { listOrder } from "./model";

const node = (id: string, created: string, parent?: string): GraphNode => ({
  id, title: id, parent, lifetime: "work", state: "idle", created: `2026-11-18T10:${created}:00Z`, updated: "2026-11-18T10:00:00Z", version: 1,
});

describe("the list, on a small screen", () => {
  it("reads each subject, then its bubbles under it, in the order they were made", () => {
    const nodes = Object.fromEntries(
      [node("b", "02"), node("a", "01"), node("a2", "05", "a"), node("a1", "04", "a"), node("a11", "06", "a1"), node("b1", "03", "b"), node("lost", "07", "gone")].map((x) => [x.id, x]),
    );
    expect(listOrder(nodes).map((x) => x.id)).toEqual(["a", "a1", "a11", "a2", "b", "b1", "lost"]);
  });
});
