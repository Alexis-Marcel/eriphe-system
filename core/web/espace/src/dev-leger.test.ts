import { describe, expect, it } from "vitest";
import type { GraphNode } from "./data/api";
import { secondBrain } from "./dev-leger";
import { DAY } from "./graph/life";
import { pairs } from "./graph/layout";

// The ?leger page's second brain is shaped as one: about 1,100 notes and
// 4,000 links, a few hubs much linked, many leaves, some notes alone.
describe("the second brain preset", () => {
  const nodes = secondBrain();
  const ids = new Set(nodes.map((x) => x.id));
  const links = pairs(nodes.map((x) => ({ id: x.id, parent: x.parent, links: (x.links ?? []).filter((l) => ids.has(l)) })));

  it("has about 1,100 notes and 4,000 links", () => {
    expect(nodes).toHaveLength(1100);
    expect(links.length).toBeGreaterThan(3800);
    expect(links.length).toBeLessThanOrEqual(4000);
  });

  it("has hubs, leaves and notes alone", () => {
    const degree = new Map<string, number>();
    for (const [a, b] of links) {
      degree.set(a, (degree.get(a) ?? 0) + 1);
      degree.set(b, (degree.get(b) ?? 0) + 1);
    }
    const d = nodes.map((x) => degree.get(x.id) ?? 0).sort((a, b) => b - a);
    expect(d[0]).toBeGreaterThan(150);
    expect(d.filter((k) => k === 0).length).toBeGreaterThan(50);
    expect(d.filter((k) => k <= 2).length).toBeGreaterThan(300);
    expect(nodes.filter((x) => !x.parent).length).toBeGreaterThan(20);
  });

  it("lives as a real one: a handful waiting in a few families, some running or kept up, many asleep", () => {
    const now = Date.now();
    const age = (x: GraphNode) => (now - Date.parse(x.updated)) / DAY;
    const waiting = nodes.filter((x) => x.status === "waits");
    expect(waiting.length).toBeGreaterThanOrEqual(2);
    expect(waiting.length).toBeLessThanOrEqual(8);
    const top = (x: GraphNode): string => (x.parent ? top(nodes.find((y) => y.id === x.parent)!) : x.id);
    expect(new Set(waiting.map(top)).size).toBeLessThanOrEqual(3);
    expect(nodes.filter((x) => x.status === "running").length).toBeGreaterThan(5);
    expect(nodes.filter((x) => x.maintained?.length).length).toBeGreaterThan(5);
    expect(nodes.filter((x) => age(x) < 7).length).toBeGreaterThan(50);
    expect(nodes.filter((x) => age(x) > 21).length).toBeGreaterThan(nodes.length * 0.4);
    expect(nodes.filter((x) => age(x) > 120).length).toBeGreaterThan(50);
  });

  it("is the same each time", () => {
    expect(secondBrain()).toEqual(nodes);
  });
});
