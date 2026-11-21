import { createContext, useContext, useEffect, useState } from "react";
import { answerProposal, dayProposals, proposal, type Answer, type Moved, type ProposalView } from "../data/api";
import { store } from "../data/store";

// Where the Proposal and Day components read proposals, send an answer and
// learn that one moved (made or decided anywhere: its card, a channel, a
// plain "oui", another page): eriphe-core by default (data/api.ts: the
// proposal's card answered, the stream's "proposals" events in the store);
// the sample and the tests give their own.
export type Proposals = {
  read(ref: string): Promise<ProposalView | null>;
  answer(a: Answer): Promise<boolean>;
  // day: the proposals that wait and take place on a day (YYYY-MM-DD).
  day(date: string): Promise<ProposalView[]>;
  // follow calls back when proposals moved; it gives a function to stop.
  follow(on: (m: Moved) => void): () => void;
};

export function followProposals(on: (m: Moved) => void): () => void {
  return store.subscribe((s, before) => {
    if (s.moved.n !== before.moved.n) on(s.moved);
  });
}

export const coreProposals: Proposals = { read: proposal, answer: answerProposal, day: dayProposals, follow: followProposals };

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

// answerOne answers a card that waits on one item (its row's Oui, or the
// same Oui in the item opened): its calls all chosen, then read again.
// where names the surface and component it is answered from, and the row
// (at) when bound inside a list.
export async function answerOne(src: Proposals, ref: string, p: ProposalView, yes: boolean, where: { surfaceId: string; componentId: string; at?: string }): Promise<{ ok: boolean; again: ProposalView | null }> {
  const choices = p.choices ?? [];
  const only = yes && choices.length > 0 ? { only: choices.map((c) => c.id), left: [] } : {};
  const ok = await src.answer({ surfaceId: where.surfaceId, componentId: where.componentId, ref: p.ref, yes, ...(where.at ? { at: where.at } : {}), ...only }).catch(() => false);
  const again = await src.read(ref).catch(() => null);
  return { ok, again };
}

export type { Answer, Moved, ProposalView };
