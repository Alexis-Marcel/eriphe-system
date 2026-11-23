import type { Action, Message } from "../a2ui/adapter";
import { CATALOG } from "../a2ui/catalog";
import { stillAsks, type ListRow, type NodePage, type PageBlock, type SourceList } from "../data/api";

// A node's page as the page draws it (web_graph.go's nodePage): its text
// as Markdown, its blocks as A2UI surfaces, one per block, through the
// renderer the catalog's components use (a2ui/). A block's surface is
// named by its node and its name; its data is the block's, with each
// source's last value at /<name>, as eriphe-core sends it. A block
// someone else's words may have written (external) is marked so; its
// button still asks, and eriphe-core marks what it says as external too.

export type Piece =
  | { kind: "text"; text: string }
  | { kind: "block"; surface: string; block: PageBlock }
  | { kind: "card"; surface: string; name: string; ref: string; found: boolean }
  | { kind: "broken"; name: string; error: string };

// surfaceOf names a block's surface: its node and its name, which both
// keep to [A-Za-z0-9_-].
export const surfaceOf = (node: string, block: string) => `${node}--${block}`;

// pieces is the page, piece by piece, in its order.
export function pieces(page: NodePage): Piece[] {
  const out: Piece[] = [];
  for (const p of page.parts ?? []) {
    if (p.block) {
      const b = p.block;
      if (b.error) out.push({ kind: "broken", name: b.name, error: b.error });
      // A card block: the card itself is eriphe-core's (proposal), drawn
      // from its records; none when it is not one of the node's. Decided,
      // it leaves the body (the ⋯ menu lists it).
      else if (b.card && b.proposal && b.proposal.ref === b.card && !stillAsks(b.proposal)) continue;
      else if (b.card) out.push({ kind: "card", surface: surfaceOf(page.node.id, b.name), name: b.name, ref: b.card, found: !!b.proposal && b.proposal.ref === b.card });
      else if (!Array.isArray(b.components)) out.push({ kind: "broken", name: b.name, error: "" });
      else out.push({ kind: "block", surface: surfaceOf(page.node.id, b.name), block: b });
    } else if (typeof p.text === "string" && p.text.trim()) out.push({ kind: "text", text: p.text });
  }
  return out;
}

// messages is what makes the page's blocks in the renderer: a surface each
// (the page read again, a new version, makes them again from scratch).
export function messages(page: NodePage): Message[] {
  const out: Message[] = [];
  for (const p of pieces(page)) {
    if (p.kind !== "block") continue;
    const meta = p.block.external ? { extensions: { eriphe: { tainted: true, author: "node" } } } : { extensions: { eriphe: { author: "node" } } };
    out.push({
      version: "1.0",
      createSurface: { surfaceId: p.surface, catalogId: CATALOG.id, components: p.block.components ?? [], dataModel: p.block.data ?? {}, metadata: meta },
    });
  }
  return out;
}

// sourceMessage puts a source's new value where the block reads it.
export function sourceMessage(node: string, block: string, name: string, value: unknown): Message {
  return { version: "1.0", updateDataModel: { surfaceId: surfaceOf(node, block), path: `/${name.replace(/~/g, "~0").replace(/\//g, "~1")}`, value } };
}

// sourceLists are the Lists bound to a source's items the page must know
// (those that filter them, open their rows, a batch folds, or on whose
// rows a card waits), by their block's surface (list_rows.go,
// list_sections.go): none from a block that is not drawn. Each one's rows
// are its latest (rows, by the List's component, as the stream sent
// them), else the page's.
export function sourceLists(page: NodePage, rows: (block: string, component: string) => ListRow[] | undefined): Map<string, SourceList[]> {
  const out = new Map<string, SourceList[]>();
  for (const p of pieces(page)) {
    if (p.kind !== "block" || !Array.isArray(p.block.lists) || !p.block.lists.length) continue;
    out.set(
      p.surface,
      p.block.lists.map((l) => ({ ...l, items: rows(p.block.name, l.component) ?? l.items ?? [] })),
    );
  }
  return out;
}

// blockOf is a block of the page, by its name.
export function blockOf(page: NodePage, name: string): PageBlock | undefined {
  return page.parts.find((p) => p.block?.name === name)?.block;
}

// A gesture on a block, as eriphe-core takes it: a button's ask (the
// button only: eriphe-core reads its words in the block). It is the only
// one: nothing in a block acts.
export type Gesture = { kind: "ask"; block: string; component: string };

// toGesture reads an action of one of the node's blocks: none from
// another surface or from a block that is not there. An external block's
// ask goes too: eriphe-core carries its taint to the turn it starts.
export function toGesture(a: Action, node: string, block: (name: string) => PageBlock | undefined): Gesture | null {
  const prefix = surfaceOf(node, "");
  if (!a.surfaceId.startsWith(prefix)) return null;
  const name = a.surfaceId.slice(prefix.length);
  const b = block(name);
  if (!b) return null;
  if (a.name === "ask") return { kind: "ask", block: name, component: a.sourceComponentId };
  return null;
}
