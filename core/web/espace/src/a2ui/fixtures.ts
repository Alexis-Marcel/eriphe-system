import type { Earlier, Exchange, ExchangeLine } from "../data/api";
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
      text: en ? "use “move funds”" : "utiliser « move funds »",
      from: en ? "My accounts · an item of the node" : "Mes comptes · un élément de la bulle",
      yes: en ? "On Yes, this call runs once, exactly as shown." : "Sur Oui, cet appel part une seule fois, tel qu'il est montré.",
      fields: [{ name: en ? "use “move funds”" : "utiliser « move funds »", value: "to : Courant" }],
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

// sampleLines is the dev server's conversation (web_console.go),
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

// sampleExchange is the dev server's last exchange, with the replies
// Eriphe suggests under its answer.
export function sampleExchange(lang: Lang, now = Date.now()): Exchange {
  const suggestions = lang === "en" ? ["Show me the page", "Keep the mailto link"] : ["Montre-moi la page", "Garde le lien mailto"];
  return { lines: sampleLines(lang, now).slice(-2), earlier: true, suggestions };
}

// sampleEarlier is the dev server's lines before a line, two at a time, as
// GET /api/earlier gives them.
export function sampleEarlier(lang: Lang, before: number, now = Date.now()): Earlier {
  const all = sampleLines(lang, now).filter((l) => l.seq < before);
  return { lines: all.slice(-2), more: all.length > 2 };
}
