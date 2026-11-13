import type { Lang } from "../i18n";
import type { Layout } from "../types";
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

// sampleLayout is where eriphe-core would lay the made-up surface out: the
// layout the event stream's "layout" carries (workspace_layout.go).
export function sampleLayout(lang: Lang): Layout {
  return {
    eriphe: "",
    by: "fallback",
    ambient: "calm",
    items: [{ id: SAMPLE_SURFACE, title: words[lang].title, size: "l", col: 0, row: 0, w: 12, h: 3 }],
    chips: [],
  };
}

// The gallery: surfaces that use every component of the catalog, for the
// tests and to look at the components on the dev server (?gallery). A Go
// test checks that eriphe-core accepts them (web_espace_gallery_test.go).
export function galleryFixtures(lang: Lang): Message[] {
  return gallery[lang] as unknown as Message[];
}

export const gallerySurfaces = (lang: Lang) => galleryFixtures(lang).map((m) => ("createSurface" in m ? m.createSurface.surfaceId : ""));

// Made-up proposals for the gallery: its Proposal's, and a dated one its
// Day shows as a ghost. Answering one changes nothing but its state on the
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
  return {
    read: async (ref) => read(ref),
    answer: async ({ ref, yes }) => {
      if (read(ref)?.state !== "pending") return false;
      states.set(ref, yes ? "approved" : "rejected");
      for (const f of followers) f({ refs: [ref] });
      return true;
    },
    day: async (date) => views.map((v) => read(v.ref)!).filter((v) => v.state === "pending" && v.when?.some((w) => w.start.startsWith(date))),
    follow: (on) => {
      followers.add(on);
      return () => followers.delete(on);
    },
  };
}
