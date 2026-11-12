import type { Lang } from "../i18n";
import type { Message } from "./adapter";
import { CATALOG } from "./catalog";
import gallery from "./gallery.json";
import type { Proposals, ProposalView } from "./proposals";

// Made-up A2UI messages, in the shape eriphe-core sends, for the dev server
// and the tests until core streams surfaces (#419). No real name, address or
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
// test checks that eriphe-core accepts them (web_espace_gallery_test.go).
export function galleryFixtures(lang: Lang): Message[] {
  return gallery[lang] as unknown as Message[];
}

export const gallerySurfaces = (lang: Lang) => galleryFixtures(lang).map((m) => ("createSurface" in m ? m.createSurface.surfaceId : ""));

// A made-up proposal for the gallery's Proposal: answering it changes
// nothing but its state on the page.
export function sampleProposals(lang: Lang): Proposals {
  const view: ProposalView = {
    ref: "rule:7",
    card: 7,
    state: "pending",
    text: lang === "en" ? "Stop telling me about invoices before the brief?" : "Ne plus te prévenir des factures avant le brief ?",
    why: lang === "en" ? "You opened none of the last five." : "Tu n'as ouvert aucune des cinq dernières.",
  };
  const states = new Map<string, string>();
  return {
    read: async (ref) => (ref === view.ref ? { ...view, state: states.get(ref) ?? view.state } : null),
    answer: async (card, yes) => {
      if (card !== view.card) return false;
      states.set(view.ref, yes ? "approved" : "rejected");
      return true;
    },
  };
}
