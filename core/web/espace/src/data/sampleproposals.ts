import type { Moved, Proposals, ProposalView } from "../a2ui/proposals";
import type { Lang } from "../i18n";
import type { OrbFeed } from "./api";
import { sampleCards } from "./samplecore";

// The dev server's proposals and orb (dev.tsx). Made-up words, no real
// name, address or tool in them.

// Made-up proposals for the dev server: a few of each kind, and a dated
// one an agenda shows as a ghost. Answering one changes nothing but its state on the
// page, and says it moved, as eriphe-core's stream would.
export function sampleProposals(lang: Lang): Proposals {
  const en = lang === "en";
  const views: ProposalView[] = [
    {
      ref: "rule:7",
      card: 7, verb: "Faire",
      state: "pending",
      text: en ? "Stop telling me about invoices before the brief?" : "Ne plus te prévenir des factures avant le brief ?",
      why: en ? "You opened none of the last five." : "Tu n'as ouvert aucune des cinq dernières.",
    },
    ...queueViews(lang),
    {
      ref: "access:5",
      card: 5, verb: "Faire",
      state: "approved",
      exec: "done",
      kind: "access",
      text: en ? "Merge the branch into main" : "Fusionner la branche dans main",
    },
    {
      ref: "calls:1",
      card: 9, verb: "Faire",
      state: "pending",
      kind: "calls",
      text: en ? "Move funds · 1 item" : "Move funds · 1 élément",
      context: en ? "My accounts" : "Mes comptes",
      from: en ? "My accounts · an item of the node" : "Mes comptes · un élément de la bulle",
      fields: [{ name: "bank/move_funds", value: "to : Courant", detail: true }],
    },
    {
      ref: "intention:3",
      card: 8, verb: "Faire",
      state: "pending",
      text: en ? "An hour to read the quote again" : "Une heure pour relire le devis",
      when: [{ start: "2026-11-12T14:30:00+01:00", end: "2026-11-12T15:30:00+01:00" }],
    },
  ];
  const states = new Map<string, string>();
  const followers = new Set<(m: Moved) => void>();
  const read = (ref: string) => {
    const v = views.find((v) => v.ref === ref);
    return v ? { ...v, state: states.get(ref) ?? v.state } : null;
  };
  // The dev server's views keep their own cards (samplecore.ts).
  const viewCards = sampleCards();
  return {
    read: async (ref) => viewCards.read(ref) ?? read(ref),
    answer: async ({ ref, yes, only }) => {
      if (viewCards.read(ref)) return viewCards.answer(ref, yes, only);
      if (read(ref)?.state !== "pending") return false;
      states.set(ref, yes ? "approved" : "rejected");
      for (const f of followers) f({ refs: [ref] });
      return true;
    },
    day: async (date) => [...views.map((v) => read(v.ref)!).filter((v) => v.state === "pending" && v.when?.some((w) => w.start.startsWith(date))), ...viewCards.day(date)],
    follow: (on) => {
      followers.add(on);
      const off = viewCards.follow(on);
      return () => {
        followers.delete(on);
        off();
      };
    },
  };
}

// Made-up cards of the dev workspace, some long, some drawn from external
// content.
const queue = {
  fr: [
    { text: "Répondre à Sam que le devis de la terrasse convient, et proposer de commencer les travaux lundi 16 novembre à 8 h", why: "Sam attend une réponse depuis mardi.", tainted: true },
    { text: "Garder en mémoire que l'école ferme à 16 h 30 le vendredi" },
    { text: "Classer la facture d'électricité d'octobre dans Factures / 2026", tainted: true, why: "Elle est arrivée ce matin, avec une pièce jointe." },
    { text: "Rappeler à Sam de renvoyer le formulaire d'inscription au club avant la fin de la semaine prochaine" },
  ],
  en: [
    { text: "Tell Sam the terrace quote is fine, and offer to start the work on Monday 16 November at 8 am", why: "Sam has been waiting for an answer since Tuesday.", tainted: true },
    { text: "Remember that the school closes at 4:30 pm on Fridays" },
    { text: "File the October electricity bill in Bills / 2026", tainted: true, why: "It came this morning, with an attachment." },
    { text: "Remind Sam to send the club's sign-up form back before the end of next week" },
  ],
};

export function queueViews(lang: Lang): ProposalView[] {
  return queue[lang].map((q, i) => ({ ref: `card:${20 + i}`, card: 20 + i, state: "pending", verb: lang === "fr" ? "Garder" : "Keep", ...q }));
}

// sampleOrb is the dev server's orb: Eriphe's answer beside a subject of
// the made-up graph, a few lines, the rest in its page.
export function sampleOrb(lang: Lang): OrbFeed {
  const en = lang === "en";
  return {
    asked: 11,
    answer: { seq: 12, node: "sub", text: en ? "All right: a contact form rather than a mailto link." : "D'accord : un formulaire de contact plutôt qu'un lien mailto.", more: true },
  };
}
