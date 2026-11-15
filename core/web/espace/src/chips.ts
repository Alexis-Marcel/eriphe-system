import type { FoldedChip } from "./types";

// A chip as the strip shows it: the folded surfaces it stands for (one,
// or several that say the same), the first opened by a click.
export type ShownChip = FoldedChip & { ids: string[] };

// surfaceTitle is the title eriphe-core gives a surface (its metadata).
export function surfaceTitle(s: unknown): string | undefined {
  const t = (s as { metadata?: { extensions?: { eriphe?: { title?: unknown } } } } | undefined)?.metadata?.extensions?.eriphe?.title;
  return typeof t === "string" && t.trim() ? t.trim() : undefined;
}

// surfaceSummary is a surface's one-line summary, when eriphe-core gives
// one (a thread's: « Fusion ✓ · Déploiement à toi »): what a small tile and
// a chip show of it.
export function surfaceSummary(s: unknown): string | undefined {
  const t = (s as { metadata?: { extensions?: { eriphe?: { summary?: unknown } } } } | undefined)?.metadata?.extensions?.eriphe?.summary;
  return typeof t === "string" && t.trim() ? t.trim() : undefined;
}

// surfaceWeight is a surface's weight (1 to 100) as eriphe-core gives it;
// 80 and up is urgent (weightUrgent, workspace_layout.go): it needs the
// user. 0 when it has none.
export function surfaceWeight(s: unknown): number {
  const w = (s as { metadata?: { extensions?: { eriphe?: { weight?: unknown } } } } | undefined)?.metadata?.extensions?.eriphe?.weight;
  return typeof w === "number" ? w : 0;
}

// URGENT is the weight from which a surface needs the user (weightUrgent).
export const URGENT = 80;

// surfaceUpdated is when eriphe-core last wrote a surface again as a whole
// (its metadata's updated): a new item in a group of the queue, a thread's
// line that changed. "" when it has none.
export function surfaceUpdated(s: unknown): string {
  const u = (s as { metadata?: { extensions?: { eriphe?: { updated?: unknown } } } } | undefined)?.metadata?.extensions?.eriphe?.updated;
  return typeof u === "string" ? u : "";
}

// shownChips are the chips the strip shows. Each has words, never a raw
// id: the layout's title, else its surface's own, else untitled. Chips
// with the same title (and both closed, or both not) are one, with their
// count; untitled ones stay apart.
export function shownChips(chips: FoldedChip[], titleOf: (id: string) => string | undefined, untitled: string): ShownChip[] {
  const out: ShownChip[] = [];
  const same = new Map<string, ShownChip>();
  for (const c of chips) {
    const named = (c.title && c.title !== c.id ? c.title : undefined) ?? titleOf(c.id);
    const chip = { ...c, title: named ?? untitled, ids: [c.id] };
    if (named === undefined) {
      out.push(chip); // untitled: not the same as another untitled
      continue;
    }
    const key = `${c.closed ? "closed" : "open"}\u0000${named}`;
    const first = same.get(key);
    if (first) {
      first.ids.push(c.id);
      continue;
    }
    same.set(key, chip);
    out.push(chip);
  }
  return out;
}

// chipLabel is what a chip says: its title, and how many it stands for
// when several.
export const chipLabel = (c: ShownChip) => (c.ids.length > 1 ? `${c.title} · ${c.ids.length}` : c.title);
