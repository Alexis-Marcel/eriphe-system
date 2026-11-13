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
    ...queueViews(lang),
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

// The made-up attention queue of the dev workspace: proposals as
// eriphe-core's queue shows them, one surface each (workspace_queue.go),
// some long, some drawn from external content.
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

function queueViews(lang: Lang): ProposalView[] {
  return queue[lang].map((q, i) => ({ ref: `card:${20 + i}`, card: 20 + i, state: "pending", ...q }));
}

const alert = {
  fr: { title: "Attend une réponse", text: "La sauvegarde de la nuit ne s'est pas terminée : le disque du NAS était plein à 3 h 12." },
  en: { title: "Waiting for an answer", text: "Last night's backup did not finish: the NAS disk was full at 3:12 am." },
};

const eriphe = {
  fr: "Bonjour. Quatre propositions attendent ton accord, et la sauvegarde de cette nuit a échoué.",
  en: "Hello. Four proposals wait for your yes, and last night's backup failed.",
};

const proposalTitle = { fr: "Proposition", en: "Proposal" };

// workspaceFixtures is the dev server's workspace: the made-up surfaces
// (the to-do list, the queue's proposals and alert, three of the
// gallery's) and where eriphe-core would lay them out on the 12 × 8 grid.
export function workspaceFixtures(lang: Lang): { messages: Message[]; layout: Layout; ids: string[] } {
  const card = (id: string, root: Record<string, unknown>): Message => ({
    version: "1.0",
    createSurface: { surfaceId: id, catalogId: CATALOG.id, components: [{ id: "root", ...root }] },
  } as Message);
  const pick = (id: string) => galleryFixtures(lang).find((m) => "createSurface" in m && m.createSurface.surfaceId === id)!;
  const q = queueViews(lang);
  const messages: Message[] = [
    ...surfaceFixtures(lang),
    card("q-alert", { component: "Alert", level: "warning", text: alert[lang].text }),
    ...q.map((v, i) => card(`q-card-${i}`, { component: "Proposal", proposalId: v.ref })),
    pick("gallery-day"),
    pick("gallery-figures"),
    pick("gallery-project"),
  ];
  const t = proposalTitle[lang];
  const en = lang === "en";
  const layout: Layout = {
    eriphe: eriphe[lang],
    by: "fallback",
    ambient: "decision",
    items: [
      { id: "q-alert", title: alert[lang].title, size: "m", col: 0, row: 0, w: 3, h: 2, urgent: true },
      { id: "q-card-0", title: t, size: "m", col: 3, row: 0, w: 3, h: 2 },
      { id: SAMPLE_SURFACE, title: words[lang].title, size: "l", col: 6, row: 0, w: 6, h: 3 },
      { id: "q-card-1", title: t, size: "s", col: 0, row: 2, w: 3, h: 1 },
      { id: "q-card-2", title: t, size: "s", col: 3, row: 2, w: 3, h: 1 },
      { id: "gallery-day", title: en ? "Today" : "Aujourd'hui", size: "l", col: 0, row: 3, w: 4, h: 3 },
      { id: "gallery-figures", title: en ? "Home" : "Maison", size: "l", col: 4, row: 3, w: 4, h: 3 },
      { id: "q-card-3", title: t, size: "s", col: 8, row: 3, w: 4, h: 1 },
    ],
    chips: [{ id: "gallery-project", title: en ? "The terrace" : "La terrasse", why: en ? "Nothing urgent" : "Rien de pressé" }],
  };
  return { messages, layout, ids: messages.flatMap((m) => ("createSurface" in m ? [m.createSurface.surfaceId] : [])) };
}
