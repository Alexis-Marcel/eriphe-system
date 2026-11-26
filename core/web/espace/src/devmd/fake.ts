import type { Moved, Proposals, ProposalView } from "../a2ui/proposals";
import { cards } from "./samples";

// PROTOTYPE: the cards code holds, made up. A card already proposed
// (card:…) is read from samples.ts; a list's card (do:…) is composed by the
// page from the things its lines reference, and registered here. A Oui
// runs « for a moment », then is done.

export type FakeProposals = Proposals & { register(p: ProposalView): void; peek(ref: string): ProposalView | undefined };

export function fakeProposals(): FakeProposals {
  const views = new Map<string, ProposalView>(Object.entries(cards).map(([k, v]) => [k, { ...v }]));
  const followers = new Set<(m: Moved) => void>();
  const moved = (ref: string) => {
    for (const f of followers) f({ refs: [ref] } as Moved);
  };
  // A card: ref no sample knows (a pasted page) is made up on the spot.
  const read = (ref: string): ProposalView | null => {
    if (!views.has(ref) && /^[a-z]+:\d+$/.test(ref)) views.set(ref, { ref, card: Number(ref.split(":")[1]), kind: "calls", state: "pending", verb: "Faire", text: `Proposition ${ref} (inconnue du prototype : carte fictive)` });
    const v = views.get(ref);
    return v ? { ...v } : null;
  };
  return {
    register(p) {
      const was = views.get(p.ref);
      views.set(p.ref, was && was.state !== "pending" ? { ...p, state: was.state, exec: was.exec, choices: was.choices } : p);
    },
    peek: (ref) => views.get(ref),
    read: async (ref) => read(ref),
    answer: async ({ ref, yes, only }) => {
      const v = views.get(ref);
      if (!v || v.state !== "pending") return false;
      const kept = only ? (v.choices ?? []).map((c) => ({ ...c, state: only.includes(c.id) ? "approved" : "rejected" })) : v.choices;
      views.set(ref, { ...v, state: yes ? "approved" : "rejected", exec: yes ? "running" : undefined, choices: kept });
      moved(ref);
      if (yes)
        setTimeout(() => {
          views.set(ref, { ...views.get(ref)!, exec: "done" });
          moved(ref);
        }, 900);
      return true;
    },
    day: async (date) => [...views.values()].filter((v) => v.state === "pending" && v.when?.some((w) => w.start.startsWith(date))),
    follow: (on) => {
      followers.add(on);
      return () => void followers.delete(on);
    },
  };
}
