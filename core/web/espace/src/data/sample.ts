import golden from "../doc/page.golden.json";
import type { GraphNode, NodeFile, NodePage, TraceEntry } from "./api";
import { samplePage, setViews } from "./samplecore";
import { sampleDays } from "./sampledays";
import { sampleViews } from "./sampleviews";

// The dev server's made-up graph, when no eriphe-core is behind: a few
// subjects and their bubbles, each page the shape eriphe-core sends
// (page.golden.json); the views (« Mes mails », « Alertes de la prod »)
// written as Eriphe writes them and drawn by a make-believe core
// (samplecore.ts, sampleviews.ts). No real name, address or tool in it.

setViews([...sampleViews, ...sampleDays]);

const at = (min: number) => new Date(Date.now() - min * 60_000).toISOString();

const row = (id: string, title: string, more: Partial<GraphNode> = {}): GraphNode => ({
  id, title, lifetime: "work", status: "idle", created: at(600), updated: at(30), version: 1, ...more,
});

const nodes: GraphNode[] = [
  row("sub", "Substack"),
  row("plan", "Plan du 1er article", { parent: "sub", version: 2, status: "waits" }),
  row("style", "Style des articles", { parent: "sub", status: "running", line: "3 articles lus" }),
  row("day", "Ma journée", { lifetime: "standing", created: at(700) }),
  row("mails", "Mes mails", { lifetime: "standing", created: at(650), links: ["sub"], status: "waits", external: true, version: 9 }),
  row("prod", "Alertes de la prod", { lifetime: "standing", created: at(640), status: "waits", version: 4 }),
  row("ext", "Virement urgent à valider", { parent: "mails", external: true, titleExternal: true }),
];

// What each view was decided to be, what it reads, the skills that work in it.
const skill = (name: string) => [{ name, what: "note chaque élément, prépare ce qu'il propose, écrit le bilan" }];
const ALIVE: Record<string, { decided: string; sources: { name: string; what: string }[]; skills: { name: string; what: string }[] }> = {
  mails: {
    decided: "Les deux comptes, perso et travail. Ne jamais rater Sam, l'école, la banque. Préparer les réponses en brouillon, jeter les pubs et les newsletters sur mon oui.",
    sources: [{ name: "mails", what: "lit mail/search_messages toutes les 5 min" }],
    skills: skill("trier-mes-mails"),
  },
  prod: {
    decided: "Suivre la prod : ce qui casse d'abord, ce qui est résolu en lot.",
    sources: [{ name: "prod", what: "lit alerts/list_alerts toutes les 5 min" }],
    skills: skill("suivre-la-prod"),
  },
  day: {
    decided: "Ma journée et la semaine qui vient : les invitations à répondre, les trous où caser ce qui traîne.",
    sources: [{ name: "agenda", what: "lit calendar/list_events toutes les 15 min" }],
    skills: skill("ma-journee"),
  },
  invest: {
    decided: "Le PEA et l'assurance-vie : la valeur, ce qui bouge, l'écart à la cible. Rien n'est acheté ni vendu sans mon oui.",
    sources: [{ name: "pea", what: "lit bourse/portefeuille toutes les 60 min" }],
    skills: skill("suivre-mes-placements"),
  },
};

const page = (id: string): NodePage | null => {
  const n = nodes.find((x) => x.id === id);
  if (!n) return null;
  const base = golden as unknown as NodePage;
  const versions = Array.from({ length: n.version }, (_, i) => ({ n: i + 1, author: i ? "user" : "brain", at: at(60 - i * 10) }));
  const view = samplePage(id);
  if (view)
    return {
      ...base,
      node: n,
      version: n.version,
      versions,
      parts: view.parts,
      cards: view.cards,
      settled: view.settled,
      alive: { ...(ALIVE[id] ?? ALIVE.prod), updated: at(4) },
    };
  const block = base.parts.find((p) => p.block)!.block!;
  return {
    ...base,
    node: n,
    version: n.version,
    versions,
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
  file: (id: string): NodeFile => ({ md: `---\nid: ${id}\n---\n\nUn exemple.\n`, body: "Un exemple.\n", version: 1, hash: "0", last: 1 }),
};
