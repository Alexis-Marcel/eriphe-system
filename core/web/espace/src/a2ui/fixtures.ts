import type { OrbFeed } from "../data/api";
import { sampleCards } from "../data/samplecore";
import type { Lang } from "../i18n";
import type { Message } from "./adapter";
import { CATALOG } from "./catalog";
import gallery from "./gallery.json";
import type { Moved, Proposals, ProposalView } from "./proposals";

// Made-up A2UI messages, in the shape eriphe-core sends, for the dev server
// and the tests, when no eriphe-core is behind. No real name, address or
// tool in them.

const words = {
  fr: {
    title: "À faire aujourd'hui",
    items: [{ label: "Relire le devis de Sam" }, { label: "Préparer la réunion de 14 h" }, { label: "Répondre à l'école" }],
    empty: "Rien à faire",
    ask: "Résume ma journée",
    askText: "Résume ma journée en trois lignes",
  },
  en: {
    title: "To do today",
    items: [{ label: "Read Sam's quote" }, { label: "Prepare the 2 pm meeting" }, { label: "Answer the school" }],
    empty: "Nothing to do",
    ask: "Sum up my day",
    askText: "Sum up my day in three lines",
  },
};

export const SAMPLE_SURFACE = "sample-today";

export function surfaceFixtures(lang: Lang): Message[] {
  const w = words[lang];
  return [
    {
      version: "1.0",
      createSurface: {
        surfaceId: SAMPLE_SURFACE,
        catalogId: CATALOG.id,
        components: [
          { id: "root", component: "Column", children: ["title", "items", "actions"] },
          { id: "title", component: "Text", variant: "title", text: { "@path": "/title" } },
          { id: "items", component: "List", children: { componentId: "item", path: "/items" }, emptyText: w.empty },
          { id: "item", component: "Text", text: { "@path": "label" } },
          { id: "actions", component: "Row", justify: "end", children: ["ask"] },
          {
            id: "ask",
            component: "Button",
            label: w.ask,
            variant: "quiet",
            action: { event: { name: "ask", context: { text: w.askText } } },
          },
        ],
        dataModel: { title: w.title, items: w.items },
      },
    },
  ];
}

// The gallery: surfaces that use every component of the catalog, for the
// tests and to look at the components on the dev server (?gallery). A Go
// test checks that eriphe-core accepts them (espace_gallery_test.go).
export function galleryFixtures(lang: Lang): Message[] {
  return gallery[lang] as unknown as Message[];
}


// Made-up proposals for the gallery: its Proposal's, and a dated one its
// Agenda shows as a ghost. Answering one changes nothing but its state on the
// page, and says it moved, as eriphe-core's stream would.
export function sampleProposals(lang: Lang): Proposals {
  const en = lang === "en";
  const views: ProposalView[] = [
    {
      ref: "rule:7",
      card: 7,
      state: "pending",
      text: en ? "Stop telling me about invoices before the brief?" : "Ne plus te prévenir des factures avant le brief ?",
      why: en ? "You opened none of the last five." : "Tu n'as ouvert aucune des cinq dernières.",
    },
    ...queueViews(lang),
    {
      ref: "access:5",
      card: 5,
      state: "approved",
      exec: "done",
      kind: "access",
      text: en ? "Merge the branch into main" : "Fusionner la branche dans main",
    },
    {
      ref: "calls:1",
      card: 9,
      state: "pending",
      kind: "calls",
      text: en ? "Move funds · 1 item" : "Move funds · 1 élément",
      context: en ? "My accounts" : "Mes comptes",
      from: en ? "My accounts · an item of the node" : "Mes comptes · un élément de la bulle",
      fields: [{ name: "bank/move_funds", value: "to : Courant", detail: true }],
    },
    {
      ref: "intention:3",
      card: 8,
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
  return queue[lang].map((q, i) => ({ ref: `card:${20 + i}`, card: 20 + i, state: "pending", ...q }));
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
