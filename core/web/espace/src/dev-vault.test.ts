import { describe, expect, it } from "vitest";
import { isVault, vaultGraph, type VaultNote } from "./dev-vault";

// ?leger's real shape: a vault's export as a graph. A tiny made-up vault.

const notes: VaultNote[] = [
  { title: "Alpha", folder: "Cours", links: ["Beta", "Nowhere", "Alpha"], mtime: "2026-11-20T10:00:00Z" },
  { title: "Beta", folder: "Cours", links: ["Alpha"], mtime: "2026-09-01T10:00:00Z" },
  { title: "Gamma", folder: "Projets/Jardin", links: ["Beta", "Beta"], mtime: "2026-06-01T10:00:00Z" },
  { title: "Beta", folder: "Projets/Jardin", links: [], mtime: "not a date" },
  { title: "Delta", folder: "", links: [], mtime: "2026-11-25T10:00:00Z" },
];

describe("a vault's graph", () => {
  const g = vaultGraph(notes);
  const by = new Map(g.map((x) => [x.id, x]));

  it("has a root per folder, titled by it, its dates its notes' first and last", () => {
    const roots = g.filter((x) => x.id.startsWith("vf"));
    expect(roots.map((x) => x.title)).toEqual(["Cours", "Projets/Jardin"]);
    expect(roots.every((x) => !x.parent && x.status === "idle")).toBe(true);
    expect(roots[0].created).toBe("2026-09-01T10:00:00.000Z");
    expect(roots[0].updated).toBe("2026-11-20T10:00:00.000Z");
  });

  it("makes each note a child of its folder's root, changed when its file was, idle", () => {
    const notesOut = g.filter((x) => x.id.startsWith("vn"));
    expect(notesOut).toHaveLength(notes.length);
    expect(by.get("vn0")!.parent).toBe("vf0");
    expect(by.get("vn2")!.parent).toBe("vf1");
    expect(by.get("vn4")!.parent).toBeUndefined(); // no folder: alone
    expect(by.get("vn1")!.updated).toBe("2026-09-01T10:00:00.000Z");
    expect(by.get("vn3")!.updated).toBe("1970-01-01T00:00:00.000Z"); // unreadable: old
    expect(notesOut.every((x) => x.status === "idle")).toBe(true);
  });

  it("links by title, once, never to itself or to a title no note has; a title twice, the first", () => {
    expect(by.get("vn0")!.links).toEqual(["vn1"]);
    expect(by.get("vn2")!.links).toEqual(["vn1"]);
    expect(by.get("vn3")!.links).toBeUndefined();
  });

  it("names nodes by position, never by title", () => {
    for (const x of g) expect(x.id).toMatch(/^v[fn]\d+$/);
  });

  it("marks notes as waiting or running on demand, never a root, the same for a seed", () => {
    const m = vaultGraph(notes, { waits: 2, runs: 1, seed: 3 });
    expect(m.filter((x) => x.status === "waits")).toHaveLength(2);
    expect(m.filter((x) => x.status === "running")).toHaveLength(1);
    expect(m.filter((x) => x.id.startsWith("vf")).every((x) => x.status === "idle")).toBe(true);
    expect(vaultGraph(notes, { waits: 2, runs: 1, seed: 3 })).toEqual(m);
  });

  it("tells a vault's export from anything else", () => {
    expect(isVault(notes)).toBe(true);
    expect(isVault({})).toBe(false);
    expect(isVault([{ title: "x" }])).toBe(false);
  });
});
