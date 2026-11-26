import { createContext, useContext, useEffect, useState } from "react";
import { answerProposal, proposal, type Answer, type Moved, type ProposalView } from "../data/api";
import { store } from "../data/store";

// Where the Proposal and Agenda components read proposals, send an answer and
// learn that one moved (made or decided anywhere: its card, a channel, a
// plain "oui", another page): eriphe-core by default (data/api.ts: the
// proposal's card answered, the stream's "proposals" events in the store);
// the sample and the tests give their own.
export type Proposals = {
  read(ref: string): Promise<ProposalView | null>;
  answer(a: Answer): Promise<boolean>;
  // follow calls back when proposals moved; it gives a function to stop.
  follow(on: (m: Moved) => void): () => void;
};

export function followProposals(on: (m: Moved) => void): () => void {
  return store.subscribe((s, before) => {
    if (s.moved.n !== before.moved.n) on(s.moved);
  });
}

export const coreProposals: Proposals = { read: proposal, answer: answerProposal, follow: followProposals };

export const ProposalsContext = createContext<Proposals>(coreProposals);
export const useProposals = () => useContext(ProposalsContext);

// HomeNode is the node whose page shows the cards (page/Cards.tsx): a card
// of that node needs not say where it comes from. Code's, never a block's.
export const HomeNode = createContext<string | undefined>(undefined);

// useMoved is a count that grows each time one of these proposals moved
// (any, when refs is null): a component reads again when it changes.
export function useMoved(src: Proposals, refs: string[] | null): number {
  const [n, setN] = useState(0);
  const key = refs?.join(" ") ?? null;
  useEffect(() => {
    const mine = key === null ? null : new Set(key.split(" "));
    return src.follow((m) => {
      if (m.all || mine === null || m.refs.some((r) => mine.has(r))) setN((x) => x + 1);
    });
  }, [src, key]);
  return n;
}

// chosen splits a card's calls by the items the user left (a batch's rows
// unticked): a call all of whose items are left goes to left, any other
// to only.
export function chosen(p: ProposalView, leftItems?: ReadonlySet<string>): { only: number[]; left: number[] } {
  const out = { only: [] as number[], left: [] as number[] };
  for (const c of p.choices ?? []) {
    const items = c.items ?? [];
    if (leftItems && items.length > 0 && items.every((id) => leftItems.has(id))) out.left.push(c.id);
    else out.only.push(c.id);
  }
  return out;
}

// answerOne answers a card that waits on items (its row's Oui, a batch's,
// or the same Oui in the item opened): its calls chosen (all but those of
// the items left), then read again. where names the surface and
// component it is answered from, and the row (at) when bound inside a
// list.
export async function answerOne(src: Proposals, ref: string, p: ProposalView, yes: boolean, where: { surfaceId: string; componentId: string; at?: string }, leftItems?: ReadonlySet<string>): Promise<{ ok: boolean; again: ProposalView | null }> {
  const choices = p.choices ?? [];
  const only = yes && choices.length > 0 ? chosen(p, leftItems) : {};
  const ok = await src.answer({ surfaceId: where.surfaceId, componentId: where.componentId, ref: p.ref, yes, ...(where.at ? { at: where.at } : {}), ...only }).catch(() => false);
  const again = await src.read(ref).catch(() => null);
  return { ok, again };
}

export type { Answer, Moved, ProposalView };
