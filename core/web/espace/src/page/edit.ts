import type { NodeFile, Saved } from "../data/api";

// The .md tab's edit (#633), step by step, apart from its editor: the
// version it was made on (base), the text as the user has it, and where a
// save stands. A save names the base's hash: a newer version that came
// meanwhile is a conflict, shown with its text, and nothing is written;
// the user takes the newer one, or keeps theirs, to be saved over it.

export type Edit = {
  base: { version: number; hash: string; body: string };
  text: string;
  status: "idle" | "saving" | "saved" | "conflict" | "failed";
  newer?: { version: number; hash: string; body: string };
  why?: string;
};

export const fromFile = (f: NodeFile): Edit => ({ base: { version: f.version, hash: f.hash, body: f.body }, text: f.body, status: "idle" });

export const dirty = (e: Edit) => e.text !== e.base.body;

// typed is the user's text.
export const typed = (e: Edit, text: string): Edit =>
  text === e.text ? e : { ...e, text, status: e.status === "conflict" ? "conflict" : "idle", why: undefined };

// read is the file read again (a new version came): taken while the user
// changed nothing, else kept for the save to find.
export function read(e: Edit, f: NodeFile): Edit {
  if (f.version === e.base.version && f.hash === e.base.hash) return e;
  if (!dirty(e) && e.status !== "saving") return { ...fromFile(f), status: e.status === "saved" ? "saved" : "idle" };
  return e;
}

export const saving = (e: Edit): Edit => ({ ...e, status: "saving", why: undefined });

// saved is what the save came to. The text saved is the new base.
export function saved(e: Edit, text: string, r: Saved): Edit {
  if (r.ok) return { base: { version: r.version, hash: r.hash, body: text }, text: e.text, status: "saved" };
  if ("conflict" in r) return { ...e, status: "conflict", newer: r.conflict };
  return { ...e, status: "failed", why: r.why };
}

// takeNewer drops the user's text for the newer version's.
export const takeNewer = (e: Edit): Edit => (e.newer ? { base: e.newer, text: e.newer.body, status: "idle" } : e);

// keepMine keeps the user's text, made now on the newer version: the next
// save writes over it.
export const keepMine = (e: Edit): Edit => (e.newer ? { base: e.newer, text: e.text, status: "idle" } : e);
