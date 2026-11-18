import { describe, expect, it } from "vitest";
import type { GraphNode } from "./api";
import {
  applyDoc, applyExchange, applyMoved, applyNodes, applyNotes, applyProgress, applyShow, applySource, closePage, empty, home, NOTES_KEPT,
  openPage, pathOf, placeAt, restore, setTheme, SHOW_FRESH, sourceKey, subtree, talkTo, viewOf, type State,
} from "./store";

const node = (id: string, more: Partial<GraphNode> = {}): GraphNode => ({
  id, title: id.toUpperCase(), lifetime: "work", state: "idle", created: "2026-11-18T10:00:00Z", updated: "2026-11-18T10:00:00Z", version: 1, ...more,
});

function withGraph(): State {
  return applyNodes(empty(), { reset: true, nodes: [node("a"), node("a1", { parent: "a" }), node("a2", { parent: "a" }), node("b"), node("b1", { parent: "b" })] });
}

describe("the store", () => {
  it("takes the graph whole, then the rows that changed, and drops those that left", () => {
    let s = withGraph();
    expect(Object.keys(s.nodes)).toHaveLength(5);
    s = applyNodes(s, { nodes: [node("a1", { parent: "a", state: "running", line: "Trois articles lus" })] });
    expect(s.nodes.a1.line).toBe("Trois articles lus");
    s = applyNodes(s, { gone: ["b1"] });
    expect(s.nodes.b1).toBeUndefined();
    s = applyNodes(s, { reset: true, nodes: [node("c")] });
    expect(Object.keys(s.nodes)).toEqual(["c"]);
  });

  it("opens a page alone, or beside the others, and the console talks to it", () => {
    let s = withGraph();
    s = openPage(s, "a1");
    expect(s.open).toEqual(["a1"]);
    s = openPage(s, "b", true);
    expect(s.open).toEqual(["a1", "b"]);
    expect(s.talk).toBe("b");
    s = talkTo(s, "a1");
    expect(s.talk).toBe("a1");
    // closing one page closes that one only
    s = closePage(s, "b");
    expect(s.open).toEqual(["a1"]);
    expect(s.talk).toBe("a1");
    s = openPage(s, "a2");
    expect(s.open).toEqual(["a2"]);
    expect(home(s)).toMatchObject({ open: [], talk: null });
    // a node that is not there opens nothing
    expect(openPage(s, "zz")).toBe(s);
  });

  it("closes the page of a node that left the graph", () => {
    let s = openPage(openPage(withGraph(), "a1"), "b1", true);
    s = applyNodes(s, { gone: ["b1"] });
    expect(s.open).toEqual(["a1"]);
    expect(s.talk).toBe("a1");
  });

  it("opens the page Eriphe shows, once, beside what is open; an old one is not news", () => {
    const now = Date.parse("2026-11-18T12:00:00Z");
    let s = openPage(withGraph(), "a");
    const old = applyShow(s, { node: "b", seq: 4, at: new Date(now - SHOW_FRESH - 1000).toISOString() }, now);
    expect(old.open).toEqual(["a"]);
    expect(old.shown).toBe(4);
    s = applyShow(s, { node: "b", seq: 4, at: new Date(now - 1000).toISOString() }, now);
    expect(s.open).toEqual(["a", "b"]);
    expect(s.talk).toBe("b");
    s = closePage(s, "b");
    // the same show again (the stream connected again): nothing
    expect(applyShow(s, { node: "b", seq: 4, at: new Date(now).toISOString() }, now)).toBe(s);
  });

  it("keeps the exchange of the node the console talks to only", () => {
    let s = openPage(withGraph(), "a");
    expect(applyExchange(s, { node: "b", lines: [] }).exchange).toBeNull();
    s = applyExchange(s, { node: "a", lines: [{ seq: 3, role: "brain", text: "Oui.", time: "" }], suggestions: ["Vas-y", 3 as unknown as string] });
    expect(s.exchange?.lines).toHaveLength(1);
    expect(s.exchange?.suggestions).toEqual(["Vas-y"]);
    // talking to another node, it waits for that one's
    expect(talkTo(openPage(s, "b", true), "b").exchange).toBeNull();
  });

  it("counts what changed: versions, traces, progress, sources, proposals, notices", () => {
    let s = withGraph();
    s = applyDoc(applyDoc(s, { id: "a", version: 2 }), { id: "a", trace: true });
    expect(s.docs.a).toBe(1);
    expect(s.traces.a).toBe(1);
    s = applyProgress(s, { node: "a1", text: "1/2", at: "2026-11-18T10:00:00Z" });
    expect(s.progress.a1.text).toBe("1/2");
    s = applySource(s, { node: "a", block: "b", name: "n", value: { x: 1 } });
    expect(s.sources[sourceKey("a", "b", "n")]).toEqual({ x: 1 });
    s = applyMoved(s, { refs: ["card:1"] });
    expect(s.moved).toMatchObject({ n: 1, refs: ["card:1"] });
    expect(applyMoved(s, { refs: [] })).toBe(s);
    for (let i = 0; i < NOTES_KEPT + 2; i++) s = applyNotes(s, [{ id: i, body: { fr: "x" }, url: "/" }]);
    expect(s.notes.map((n) => n.id)).toEqual([2, 3, 4, 5]);
  });

  it("reads the graph's parents", () => {
    const s = withGraph();
    expect(pathOf(s.nodes, "a2")).toEqual(["a", "a2"]);
    expect(subtree(s.nodes, "a").sort()).toEqual(["a", "a1", "a2"]);
  });

  it("comes back as the user left it: its pages once their nodes come, the places, the theme", () => {
    let s = restore(empty(), { open: ["a1", "gone", "b"], talk: "a1", theme: "dark" }, { a: { x: 10, y: -4 } });
    expect(s).toMatchObject({ open: ["a1", "gone", "b"], talk: "a1", theme: "dark", places: { a: { x: 10, y: -4 } } });
    s = applyNodes(s, { reset: true, nodes: [node("a"), node("a1", { parent: "a" }), node("b")] });
    expect(s.open).toEqual(["a1", "b"]);
    expect(s.talk).toBe("a1");
    expect(viewOf(s)).toEqual({ open: ["a1", "b"], talk: "a1", theme: "dark" });
    // What eriphe-core kept that is not a view reads as nothing.
    expect(restore(empty(), { open: [3 as unknown as string], talk: "x", theme: "pink" as never }, undefined)).toMatchObject({ open: [], talk: null, theme: "", places: {} });
  });

  it("keeps where a bubble was dropped, forgets it, and the theme chosen", () => {
    let s = placeAt(empty(), "a", { x: 1, y: 2 });
    expect(s.places).toEqual({ a: { x: 1, y: 2 } });
    s = placeAt(s, "a", null);
    expect(s.places).toEqual({});
    const t = setTheme(s, "light");
    expect(t.theme).toBe("light");
    expect(setTheme(t, "light")).toBe(t);
  });
});
