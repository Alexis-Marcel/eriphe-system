import type { Message } from "../a2ui/adapter";
import { CATALOG } from "../a2ui/catalog";
import type { NodePage, PageBlock } from "../data/api";

// A node's page as the page draws it (web_graph.go's nodePage): its text
// as Markdown, its blocks as A2UI surfaces, one per block, through the
// renderer the catalog's components use (a2ui/). A block's surface is
// named by its node and its name; its data is the block's, with each
// source's last value at /<name> and the user's marks at /marks, as
// eriphe-core sends it. A block someone else's words may have written
// (external) is marked so: there, a Proposal can only ask.

export type Piece = { kind: "text"; text: string } | { kind: "block"; surface: string; block: PageBlock } | { kind: "broken"; name: string; error: string };

// surfaceOf names a block's surface: its node and its name, which both
// keep to [A-Za-z0-9_-].
export const surfaceOf = (node: string, block: string) => `${node}--${block}`;

// pieces is the page, piece by piece, in its order.
export function pieces(page: NodePage): Piece[] {
  const out: Piece[] = [];
  for (const p of page.parts ?? []) {
    if (p.block) {
      const b = p.block;
      if (b.error || !Array.isArray(b.components)) out.push({ kind: "broken", name: b.name, error: b.error ?? "" });
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
