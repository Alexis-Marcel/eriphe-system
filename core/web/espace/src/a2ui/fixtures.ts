import type { Earlier, Exchange, ExchangeLine } from "../data";
import type { Lang } from "../i18n";
import type { Layout } from "../types";
import type { Message } from "./adapter";
import { CATALOG } from "./catalog";
import gallery from "./gallery.json";
import threads from "./threads.json";
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

// The threads as eriphe-core draws them (threads.json, written from the
// code by ERIPHE_UPDATE_THREADS=1 go test -run TestThreadSurfacesFixture):
// a work thread, one that lists items, one ended.
export function threadFixtures(lang: Lang): Message[] {
  return threads[lang] as unknown as Message[];
}

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
      ref: "access:5",
      card: 5,
      state: "approved",
      exec: "done",
      kind: "access",
      text: en ? "Merge the branch into main" : "Fusionner la branche dans main",
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

const proposalTitle = { fr: "Proposition", en: "Proposal" };

// sampleLines is the dev server's conversation (web_espace_exchange.go),
// oldest first: a few exchanges, the last one about the terrace.
function sampleLines(lang: Lang, now: number): ExchangeLine[] {
  const en = lang === "en";
  const at = (min: number) => new Date(now - min * 60_000).toISOString();
  return [
    { seq: 2, role: "user", text: en ? "use the club's colours on the home page" : "mets les couleurs du club sur l'accueil", time: at(26 * 60) },
    { seq: 3, role: "brain", text: en ? "Done: the home page uses **the club's green**; the assistant will show you before publishing." : "C'est fait : l'accueil prend **le vert du club** ; l'assistant te le montre avant de publier.", time: at(26 * 60 - 1) },
    { seq: 5, role: "user", text: en ? "what's on tomorrow?" : "j'ai quoi demain ?", time: at(40) },
    { seq: 6, role: "brain", text: en ? "A call at 10 and the garage at 5 pm." : "Un appel à 10 h et le garage à 17 h.", time: at(39) },
    { seq: 8, role: "user", text: en ? "is the backup running again?" : "la sauvegarde repart ?", time: at(12) },
    { seq: 9, role: "brain", text: en ? "Not yet: the NAS disk is still full. Free some room and I'll start it again." : "Pas encore : le disque du NAS est toujours plein. Libère de la place et je la relance.", time: at(11) },
    { seq: 11, role: "user", text: en ? "add a contact form after all" : "ajoute plutôt un formulaire de contact finalement", time: at(3) },
    { seq: 12, role: "brain", text: en ? "All right: a contact form rather than a mailto link. I restarted the assistant; it shows you the page before publishing." : "D'accord : un formulaire de contact plutôt qu'un lien mailto. J'ai relancé l'assistant ; il te montre la page avant de publier.", time: at(2) },
  ];
}

// sampleExchange is the dev server's last exchange.
export function sampleExchange(lang: Lang, now = Date.now()): Exchange {
  return { lines: sampleLines(lang, now).slice(-2), earlier: true };
}

// sampleEarlier is the dev server's lines before a line, two at a time, as
// GET /api/espace/earlier gives them.
export function sampleEarlier(lang: Lang, before: number, now = Date.now()): Earlier {
  const all = sampleLines(lang, now).filter((l) => l.seq < before);
  return { lines: all.slice(-2), more: all.length > 2 };
}

// workspaceFixtures is the dev server's workspace: the made-up surfaces
// (the to-do list, the queue's proposals and alert, three of the
// gallery's) and where eriphe-core would lay them out on the 12 × 8 grid.
export function workspaceFixtures(lang: Lang): { messages: Message[]; layout: Layout; ids: string[]; exchange: Exchange } {
  // Each with its state as eriphe-core computes it (tileStateOf): a card waits for the user.
  const meta = (state: string) => ({ extensions: { eriphe: { state } } });
  const card = (id: string, root: Record<string, unknown>, state = "yours"): Message => ({
    version: "1.0",
    createSurface: { surfaceId: id, catalogId: CATALOG.id, components: [{ id: "root", ...root }], metadata: meta(state) },
  } as Message);
  const pick = (id: string) => galleryFixtures(lang).find((m) => "createSurface" in m && m.createSurface.surfaceId === id)!;
  const q = queueViews(lang);
  const messages: Message[] = [
    ...surfaceFixtures(lang),
    card("q-alert", { component: "Alert", level: "warning", text: alert[lang].text }, "urgent"),
    ...[0, 2].map((i) => card(`q-card-${i}`, { component: "Proposal", proposalId: q[i].ref })),
    // Cards that ask the same, one surface: a List of Proposals (workspace_queue.go).
    {
      version: "1.0",
      createSurface: {
        surfaceId: "q-group",
        catalogId: CATALOG.id,
        metadata: meta("yours"),
        components: [
          { id: "root", component: "List", children: ["g1", "g3"] },
          { id: "g1", component: "Proposal", proposalId: q[1].ref },
          { id: "g3", component: "Proposal", proposalId: q[3].ref },
        ],
      },
    } as Message,
    pick("gallery-day"),
    pick("gallery-figures"),
    pick("gallery-project"),
    ...threadFixtures(lang),
  ];
  const t = proposalTitle[lang];
  const en = lang === "en";
  // In eriphe-core's order (workspace_layout.go): what waits for the user,
  // then the urgent, the new, the quiet, what ended.
  const layout: Layout = {
    ambient: "decision",
    items: [
      { id: "thread-101", title: en ? "The club's site" : "Le site du club", size: "l", col: 0, row: 0, w: 4, h: 3 },
      { id: "q-card-0", title: t, size: "m", col: 4, row: 0, w: 3, h: 2 },
      { id: "q-group", title: en ? "Reminders to keep" : "Rappels à garder", size: "m", col: 7, row: 0, w: 5, h: 2 },
      { id: "q-card-2", title: t, size: "s", col: 4, row: 2, w: 3, h: 1 },
      { id: "q-alert", title: alert[lang].title, size: "s", col: 7, row: 2, w: 5, h: 1, urgent: true },
      { id: "thread-102", title: en ? "My accounts" : "Mes comptes", size: "l", col: 0, row: 3, w: 4, h: 3 },
      { id: SAMPLE_SURFACE, title: words[lang].title, size: "l", col: 4, row: 3, w: 4, h: 3 },
      { id: "thread-103", title: en ? "The terrace" : "La terrasse", size: "m", col: 8, row: 3, w: 4, h: 2 },
    ],
    chips: [
      { id: "gallery-day", title: en ? "Today" : "Aujourd'hui" },
      { id: "gallery-figures", title: en ? "Home" : "Maison" },
      { id: "gallery-project", title: en ? "The terrace" : "La terrasse", why: en ? "Nothing urgent" : "Rien de pressé" },
    ],
  };
  return { messages, layout, ids: messages.flatMap((m) => ("createSurface" in m ? [m.createSurface.surfaceId] : [])), exchange: sampleExchange(lang) };
}
