import { describe, expect, it } from "vitest";
import type { BarLine, GraphNode } from "./api";
import {
  applyDoc, applyExchange, applyMoved, applyNodes, applyNotes, applyProgress, applyShow, applySource, closePage, empty, NOTES_KEPT,
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
    // closing the last one, the console talks to the home
    expect(closePage(s, "a2")).toMatchObject({ open: [], talk: null });
    // a node that is not there opens nothing
    expect(openPage(s, "zz")).toBe(s);
  });

  it("closes the page of a node that left the graph", () => {
    let s = openPage(openPage(withGraph(), "a1"), "b1", true);
    s = applyNodes(s, { gone: ["b1"] });
    expect(s.open).toEqual(["a1"]);
    expect(s.talk).toBe("a1");
  });

  it("opens the page Eriphe shows, once, beside what is open, the console where it was; an old one is not news", () => {
    const now = Date.parse("2026-11-18T12:00:00Z");
    let s = openPage(withGraph(), "a");
    const old = applyShow(s, { node: "b", seq: 4, at: new Date(now - SHOW_FRESH - 1000).toISOString() }, now);
    expect(old.open).toEqual(["a"]);
    expect(old.shown).toBe(4);
    s = applyShow(s, { node: "b", seq: 4, at: new Date(now - 1000).toISOString() }, now);
    expect(s.open).toEqual(["a", "b"]);
    expect(s.talk).toBe("a");
    // from the home, the console stays the home's, whatever comes or goes
    let h = applyShow(withGraph(), { node: "b", seq: 5, at: new Date(now).toISOString() }, now);
    expect(h).toMatchObject({ open: ["b"], talk: null });
    h = applyNodes(h, { nodes: [node("c")] });
    expect(h.talk).toBeNull();
    h = applyShow(h, { node: "a", seq: 6, at: new Date(now).toISOString() }, now);
    expect(closePage(h, "b")).toMatchObject({ open: ["a"], talk: null });
    s = closePage(s, "b");
    // the same show again (the stream connected again): nothing
    expect(applyShow(s, { node: "b", seq: 4, at: new Date(now).toISOString() }, now)).toBe(s);
  });

  it("moves the console into a subject opened from the home, never from a page", () => {
    const now = Date.parse("2026-11-18T12:00:00Z");
    const at = new Date(now).toISOString();
    const h = applyShow(withGraph(), { node: "b", seq: 1, at, talk: true }, now);
    expect(h).toMatchObject({ open: ["b"], talk: "b" });
    const s = applyShow(openPage(withGraph(), "a"), { node: "b", seq: 1, at, talk: true }, now);
    expect(s).toMatchObject({ open: ["a", "b"], talk: "a" });
  });

  it("keeps the cards no node holds with the exchange", () => {
    const s = applyExchange(withGraph(), { cards: ["view:1", 2 as unknown as string] });
    expect(s.exchange?.cards).toEqual(["view:1"]);
  });

  it("keeps the exchange of the node the console talks to only", () => {
    let s = openPage(withGraph(), "a");
    expect(applyExchange(s, { node: "b" }).exchange).toBeNull();
    s = applyExchange(s, { node: "a", asked: { seq: 2, text: "Et alors ?" }, said: { seq: 3, text: "Oui." } });
    expect(s.exchange?.said?.text).toBe("Oui.");
    expect(s.exchange?.asked?.seq).toBe(2);
    // a line that is not one is dropped, never drawn
    s = applyExchange(s, { node: "a", said: { seq: "x", text: 1 } as unknown as BarLine });
    expect(s.exchange?.said).toBeUndefined();
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
    // the rows of its Lists, by the List's component
    s = applySource(s, { node: "a", block: "b", name: "n", value: {}, lists: [{ component: "drop", items: [{ n: 2, id: "m3" }] }] });
    expect(s.rows[sourceKey("a", "b", "drop")]).toEqual([{ n: 2, id: "m3" }]);
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
    let s = restore(empty(), { open: ["a1", "gone", "b"], talk: "a1", shown: 4, theme: "dark" }, { a: { x: 10, y: -4 } });
    expect(s).toMatchObject({ open: ["a1", "gone", "b"], talk: "a1", shown: 4, theme: "dark", places: { a: { x: 10, y: -4 } } });
    s = applyNodes(s, { reset: true, nodes: [node("a"), node("a1", { parent: "a" }), node("b")] });
    expect(s.open).toEqual(["a1", "b"]);
    expect(s.talk).toBe("a1");
    expect(viewOf(s)).toEqual({ open: ["a1", "b"], talk: "a1", shown: 4, theme: "dark" });
    // A show the user already took, given again as the stream opens, opens nothing.
    expect(applyShow(s, { node: "a", seq: 4, at: new Date().toISOString() }).open).toEqual(["a1", "b"]);
    // What eriphe-core kept that is not a view reads as nothing.
    expect(restore(empty(), { open: [3 as unknown as string], talk: "x", theme: "pink" as never }, undefined)).toMatchObject({ open: [], talk: null, theme: "", places: {} });
    expect(restore(empty(), { open: ["a"] }, undefined).talk).toBeNull();
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
