import { stillAsks, type Moved, type Opened, type PageItem, type ProposalView } from "./api";

// A small make-believe eriphe-core for the dev server (sample.ts): a few
// pages as their author writes them (Markdown, page/read.ts), the items
// their references name, and what code does with them, done here the way
// eriphe-core does it in Go:
//   - each reference to an item, on its row, with the card that waits on
//     it (page_items.go);
//   - an item opened in place (item_open.go);
//   - a card answered: its items settle, and the page's skill writes the
//     page again without them.
// Nothing leaves the page. No real name, address or tool in it.

// An item a page references: what its server reads of it.
export type SampleItem = Opened & { id: string };
export type SampleView = {
  id: string;
  // the node's body, as its file holds it
  body: (v: SampleView) => string;
  // the items its references name, by label
  items: Record<string, SampleItem>;
  // the items done (a card on them answered yes): the page leaves them out
  done: Set<string>;
  // the cards: those on items (items), the others the node's
  cards: SampleCard[];
};
export type SampleCard = Omit<ProposalView, "verb"> & { verb?: string; items?: string[]; settled?: boolean };

// asView is a sample card as eriphe-core sends it: its yes says its verb,
// the first word of its sentence as eriphe-core says it (card_verb.go).
function asView(c: SampleCard): ProposalView {
  const first = c.text.split(" ")[0] ?? "";
  return { ...c, verb: c.verb ?? (/^\p{Lu}[\p{L}'-]*$/u.test(first) ? first : "Faire") };
}

const followers = new Set<(m: Moved) => void>();
const bumpers = new Set<(id: string) => void>();
let views: SampleView[] = [];

export function setViews(vs: SampleView[]) {
  views = vs;
}

export const viewOf = (id: string) => views.find((v) => v.id === id);

// onBump calls back when a view changed (eriphe-core's "doc" event).
export function onBump(f: (id: string) => void): () => void {
  bumpers.add(f);
  return () => bumpers.delete(f);
}
const bump = (id: string) => bumpers.forEach((f) => f(id));
const moved = (refs: string[]) => followers.forEach((f) => f({ refs }));

// itemsOf are the page's references to items, each on its line, with the
// card that waits on it, as page_items.go tells them.
function itemsOf(v: SampleView, body: string): PageItem[] {
  const out: PageItem[] = [];
  body.split("\n").forEach((l, i) => {
    for (const m of l.matchAll(/\[([^\]]+)\]\[([^\]]+)\]/g)) {
      const it = v.items[m[2].toLowerCase()];
      if (!it) continue;
      const card = v.cards.find((c) => c.items?.includes(it.id) && !c.settled);
      const row = /^\s*[-*] /.test(l) ? i + 1 : undefined;
      out.push({ line: i + 1, label: m[2], text: m[1], row, id: it.id, open: true, card: card?.ref, title: it.title, detail: it.from, external: it.external });
    }
  });
  return out;
}

export function samplePage(id: string): { body: string; items: PageItem[]; cards: ProposalView[]; settled: ProposalView[] } | null {
  const v = viewOf(id);
  if (!v) return null;
  const body = v.body(v);
  // the node's own cards, as eriphe-core splits them: those that still ask
  // at the foot, the decided ones for the « ⋯ » menu; those on items show
  // on their rows
  const own = v.cards.filter((c) => !c.items);
  return {
    body,
    items: itemsOf(v, body),
    cards: own.filter(stillAsks).map(asView),
    settled: own.filter((c) => !stillAsks(c)).map(asView),
  };
}

export async function sampleOpen(id: string, ref: string): Promise<Opened> {
  await new Promise((r) => setTimeout(r, 250));
  const it = viewOf(id)?.items[ref.toLowerCase()];
  if (!it) return { error: "Introuvable." };
  const { id: _, ...read } = it;
  return read;
}

// The views' cards as proposals: answered, a card on items runs (then is
// done), and its items settle: the page's skill writes the page again
// without them (those of the calls left, unticked, stay).
export function sampleCards(): {
  read(ref: string): ProposalView | null;
  answer(ref: string, yes: boolean, only?: number[]): boolean;
  follow(on: (m: Moved) => void): () => void;
} {
  const find = (ref: string) => {
    for (const v of views) {
      const c = v.cards.find((x) => x.ref === ref);
      if (c) return { v, c };
    }
    return null;
  };
  return {
    read: (ref) => {
      const f = find(ref);
      return f ? asView(f.c) : null;
    },
    answer: (ref, yes, only) => {
      const f = find(ref);
      if (!f || f.c.state !== "pending") return false;
      f.c.state = yes ? "approved" : "rejected";
      if (yes) f.c.exec = "running";
      const going = only && f.c.choices ? f.c.choices.filter((ch) => only.includes(ch.id)).flatMap((ch) => ch.items ?? []) : (f.c.items ?? []);
      moved([ref]);
      setTimeout(() => {
        if (yes) f.c.exec = "done";
        f.c.settled = true;
        if (yes) for (const it of going) f.v.done.add(it);
        moved([ref]);
        bump(f.v.id);
      }, 900);
      return true;
    },
    follow: (on) => {
      followers.add(on);
      return () => followers.delete(on);
    },
  };
}
