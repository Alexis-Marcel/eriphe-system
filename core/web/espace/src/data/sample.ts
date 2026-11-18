import golden from "../doc/page.golden.json";
import type { GraphNode, NodeFile, NodePage, TraceEntry } from "./api";

// The dev server's made-up graph, when no eriphe-core is behind: a few
// subjects and their bubbles, each page the shape eriphe-core sends
// (page.golden.json). No real name, address or tool in it.

const at = (min: number) => new Date(Date.now() - min * 60_000).toISOString();

const row = (id: string, title: string, more: Partial<GraphNode> = {}): GraphNode => ({
  id, title, lifetime: "work", state: "idle", created: at(600), updated: at(30), version: 1, ...more,
});

const nodes: GraphNode[] = [
  row("sub", "Substack"),
  row("plan", "Plan du 1er article", { parent: "sub", version: 2, waits: true }),
  row("style", "Style des articles", { parent: "sub", state: "running", line: "3 articles lus" }),
  row("day", "Ma journée", { lifetime: "standing", created: at(700) }),
  row("mails", "Mails", { lifetime: "standing", created: at(650), links: ["sub"] }),
  row("ext", "", { parent: "mails", external: true }),
];

const page = (id: string): NodePage | null => {
  const n = nodes.find((x) => x.id === id);
  if (!n) return null;
  const base = golden as unknown as NodePage;
  const block = base.parts.find((p) => p.block)!.block!;
  return {
    ...base,
    node: n,
    version: n.version,
    versions: Array.from({ length: n.version }, (_, i) => ({ n: i + 1, author: i ? "user" : "brain", at: at(60 - i * 10) })),
    parts: [{ text: `# ${n.title || "…"}\n\nUn exemple : ces données sont fictives.\n\n` }, { block: { ...block, external: n.external } }],
    cards: [],
  };
};

export const sample = {
  nodes,
  page: (id: string, _version = 0): NodePage | null => page(id),
  trace: (_id: string): TraceEntry[] => [
    { at: at(50), author: "brain", text: "Task lancée." },
    { at: at(20), author: "task:t1", text: "Trois articles lus." },
  ],
  file: (id: string): NodeFile => ({ md: `---\nid: ${id}\n---\n\nUn exemple.\n`, version: 1, hash: "0", last: 1 }),
};
