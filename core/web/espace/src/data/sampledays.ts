import type { Authored, SampleView } from "./samplecore";

// The dev server's « Ma journée », « Investissements » and blocks gallery
// (?blocks, dev-blocks.tsx), written as Eriphe writes them, on made-up
// data (Sam, Léa, Alex, example.org) dated from today, so the agenda's
// line is where the clock is. No real name, address or tool in it.

const p2 = (n: number) => String(n).padStart(2, "0");
export function dayOf(offset: number): string {
  const d = new Date();
  d.setDate(d.getDate() + offset);
  return `${d.getFullYear()}-${p2(d.getMonth() + 1)}-${p2(d.getDate())}`;
}
const at = (offset: number, hm: string) => `${dayOf(offset)}T${hm}:00+01:00`;

// --- Ma journée -----------------------------------------------------------

type Ev = { id: string; title: string; start: string; end?: string; place?: string; allDay?: boolean; status?: string; organizer?: string };
const events: Ev[] = [
  { id: "e1", title: "Footing avec Léa", start: at(0, "07:45"), end: at(0, "08:30"), place: "Parc", status: "confirmed" },
  { id: "e2", title: "Point d'équipe", start: at(0, "09:30"), end: at(0, "10:00"), place: "Visio", status: "confirmed" },
  { id: "e3", title: "Atelier devis terrasse", start: at(0, "10:00"), end: at(0, "11:30"), place: "Chez Sam", status: "confirmed" },
  { id: "e4", title: "Pique-nique au parc", start: at(0, "12:30"), end: at(0, "14:00"), place: "Parc de la Tête d'Or", status: "needsAction", organizer: "Léa" },
  { id: "e5", title: "Déjeuner avec Alex", start: at(0, "13:00"), end: at(0, "14:00"), status: "tentative" },
  { id: "e6", title: "Appel avec Alex", start: at(0, "17:30"), end: at(0, "18:00"), status: "cancelled" },
  { id: "e7", title: "Tennis", start: at(0, "18:30"), end: at(0, "19:30"), place: "Club", status: "confirmed" },
  { id: "e8", title: "Anniversaire de Léa", start: dayOf(1), allDay: true },
  { id: "e9", title: "Dentiste", start: at(1, "09:30"), end: at(1, "10:15"), place: "Cabinet dentaire", status: "confirmed" },
  { id: "e10", title: "Appel avec Sam", start: at(1, "14:00"), end: at(1, "14:30"), status: "tentative" },
  { id: "e11", title: "Dîner du club", start: at(1, "20:00"), end: at(1, "22:30"), place: "Le Bistrot", status: "NEEDS-ACTION", organizer: "Club de tennis" },
  { id: "e12", title: "Réunion de chantier", start: at(2, "08:30"), end: at(2, "09:30"), place: "Terrasse", status: "confirmed" },
  { id: "e13", title: "Courses", start: at(2, "18:00"), end: at(2, "18:45") },
  { id: "e14", title: "Train pour Lyon", start: at(4, "07:12"), end: at(4, "09:05"), status: "confirmed" },
  { id: "e15", title: "Concert", start: at(4, "20:30"), end: at(4, "23:00"), status: "declined" },
  { id: "e16", title: "Marché", start: at(5, "09:00"), end: at(5, "10:00") },
  { id: "e17", title: "Brunch chez Sam", start: at(6, "11:00"), end: at(6, "13:00"), status: "needs-action", organizer: "Sam" },
];

const calendar = { agenda: { tool: "calendar/list_events", args: { days: 7 }, items: { path: "events", id: "id", title: "title" } } };

const agendaBlock: Authored = {
  name: "agenda",
  sources: calendar,
  components: [{ id: "root", component: "Agenda", days: 7, events: { "@path": "/agenda/events" } }],
};

const workBlock: Authored = {
  name: "en_cours",
  components: [
    { id: "root", component: "Column", children: ["h", "article", "terrasse"] },
    { id: "h", component: "Text", variant: "subtitle", text: "En cours" },
    { id: "article", component: "Progress", label: "Article « Le bureau en graphe »", value: 3, current: 1, total: 5, step: "Relecture par Léa" },
    { id: "terrasse", component: "Progress", label: "Terrasse", value: 1, total: 4, step: "Attente du bon signé de Sam" },
  ],
};

const nightBlock: Authored = {
  name: "cette_nuit",
  components: [
    { id: "root", component: "Column", children: ["h", "steps"] },
    { id: "h", component: "Text", variant: "subtitle", text: "Pendant la nuit" },
    { id: "steps", component: "Timeline", items: { "@path": "/night" } },
  ],
  data: {
    night: [
      { label: "Sauvegarde du NAS faite", state: "done", date: at(0, "02:10"), detail: "412 Go, 18 min" },
      { label: "38 mails triés, prêts à jeter sur ton oui", state: "done", date: at(0, "06:30") },
      { label: "Relevé de la banque : la synchronisation a échoué", state: "failed", date: at(0, "06:45"), detail: "Elle sera retentée à 12 h" },
      { label: "Ta journée écrite", state: "done", date: at(0, "07:00") },
    ],
  },
};

const dayCards: SampleView["cards"] = [
  {
    ref: "calls:61",
    card: 61,
    items: ["e4"],
    kind: "calls",
    state: "pending",
    node: "day",
    text: "Accepter l'invitation de Léa : « Pique-nique au parc », 12 h 30",
    fields: [{ name: "calendar/respond_event", value: "event : e4\nresponse : accepted", detail: true }],
  },
  {
    ref: "calls:62",
    card: 62,
    items: ["e11"],
    kind: "calls",
    state: "pending",
    node: "day",
    text: "Accepter l'invitation du club : « Dîner du club », demain 20 h",
    fields: [{ name: "calendar/respond_event", value: "event : e11\nresponse : accepted", detail: true }],
  },
  {
    ref: "calls:63",
    card: 63,
    items: ["e17"],
    kind: "calls",
    state: "pending",
    node: "day",
    text: "Accepter l'invitation de Sam : « Brunch chez Sam »",
    fields: [{ name: "calendar/respond_event", value: "event : e17\nresponse : accepted", detail: true }],
  },
  {
    ref: "intention:64",
    card: 64,
    state: "pending",
    node: "day",
    text: "Une heure pour relire le devis de Sam",
    why: "L'appel avec Alex est annulé : le créneau est libre.",
    when: [{ start: at(0, "16:00"), end: at(0, "17:00") }],
  },
];

const dayView: SampleView = {
  id: "day",
  parts: [
    "Une matinée pleine jusqu'à 11 h 30, puis Léa t'invite au pique-nique de 12 h 30 : il chevauche le déjeuner avec Alex, encore incertain. L'appel de 17 h 30 est annulé, j'y ai mis une heure pour relire le devis de Sam.\n\n",
    agendaBlock,
    workBlock,
    nightBlock,
  ],
  values: { agenda: { events } },
  notes: {},
  bodies: {},
  cards: dayCards,
};

// --- Investissements --------------------------------------------------------

// The PEA's value over the last 30 days, ending today.
const pea = [46120, 46380, 46210, 46540, 46900, 46710, 46650, 47020, 47310, 47180, 46890, 46970, 47250, 47480, 47390, 47610, 47820, 47560, 47400, 47690, 47930, 48010, 47780, 47660, 47890, 48120, 47990, 48060, 48190, 48230];
const peaSeries = pea.map((y, i) => ({ x: dayOf(i - pea.length + 1), y }));

const investFigures: Authored = {
  name: "chiffres",
  components: [
    { id: "root", component: "Row", children: ["total", "pea", "av", "livret"] },
    { id: "total", component: "Metric", label: "Patrimoine placé", value: 82170, unit: "€", delta: 968, deltaLabel: "cette semaine", trend: pea.map((v) => v + 33940) },
    { id: "pea", component: "Metric", label: "PEA", value: 48230, unit: "€", delta: 852, deltaLabel: "cette semaine", trend: pea },
    { id: "av", component: "Metric", label: "Assurance-vie", value: 21940, unit: "€", delta: 88, deltaLabel: "cette semaine", trend: [21700, 21720, 21760, 21750, 21800, 21830, 21810, 21860, 21900, 21940] },
    { id: "livret", component: "Metric", label: "Livret A", value: 12000, unit: "€", delta: 0, deltaLabel: "3 % par an" },
  ],
};

const investChart: Authored = {
  name: "courbe",
  components: [
    { id: "root", component: "Column", children: ["h", "chart"] },
    { id: "h", component: "Text", variant: "subtitle", text: "Le PEA sur 30 jours" },
    { id: "chart", component: "Chart", kind: "line", unit: "€", series: [{ name: "PEA", points: peaSeries }] },
  ],
};

const investTable: Authored = {
  name: "lignes",
  components: [
    { id: "root", component: "Column", children: ["h", "table", "alloc"] },
    { id: "h", component: "Text", variant: "subtitle", text: "Les lignes du PEA" },
    {
      id: "table",
      component: "Table",
      columns: [
        { key: "nom", label: "Ligne" },
        { key: "valeur", label: "Valeur (€)", kind: "number" },
        { key: "semaine", label: "Semaine", kind: "change" },
        { key: "an", label: "Depuis janvier", kind: "change" },
        { key: "part", label: "Part (%)", kind: "number" },
      ],
      rows: { "@path": "/lignes" },
    },
    {
      id: "alloc",
      component: "Chart",
      kind: "stack",
      unit: "%",
      series: [
        { name: "Actions", points: [{ x: "Aujourd'hui", y: 64 }, { x: "Cible", y: 60 }] },
        { name: "Obligations", points: [{ x: "Aujourd'hui", y: 26 }, { x: "Cible", y: 30 }] },
        { name: "Liquidités", points: [{ x: "Aujourd'hui", y: 10 }, { x: "Cible", y: 10 }] },
      ],
    },
  ],
  data: {
    lignes: [
      { nom: "ETF Monde", valeur: 24860, semaine: 2.6, an: 14.2, part: 51.5 },
      { nom: "ETF Europe", valeur: 6040, semaine: 1.1, an: 7.9, part: 12.5 },
      { nom: "ETF Obligations", valeur: 12510, semaine: -0.3, an: 2.1, part: 25.9 },
      { nom: "Liquidités", valeur: 4820, semaine: 0, an: 0, part: 10.1 },
    ],
  },
};

const investQuote: Authored = {
  name: "a_lire",
  external: true,
  components: [
    { id: "root", component: "Column", children: ["h", "q"] },
    { id: "h", component: "Text", variant: "subtitle", text: "À lire" },
    {
      id: "q",
      component: "Quote",
      text: "La banque centrale laisse ses taux inchangés et dit vouloir attendre le printemps avant toute baisse. Les marchés obligataires ont peu réagi.",
      from: "La Lettre Éco",
      date: at(0, "07:40"),
      url: "https://journal.example.org/eco/taux-inchanges",
    },
  ],
};

const investView: SampleView = {
  id: "invest",
  parts: [
    "Le PEA a pris 1,8 % cette semaine, porté par l'ETF Monde. Les actions pèsent 64 % pour une cible de 60 % : je propose de rééquilibrer 400 € vers les obligations.\n\n",
    investFigures,
    investChart,
    investTable,
    investQuote,
  ],
  values: {},
  notes: {},
  bodies: {},
  cards: [
    {
      ref: "calls:71",
      card: 71,
      kind: "calls",
      state: "pending",
      node: "invest",
      text: "Rééquilibrer le PEA : vendre 400 € d'ETF Monde, acheter 400 € d'ETF Obligations",
      why: "Les actions dépassent la cible de 4 points depuis trois semaines.",
      fields: [
        { name: "Ordre 1", value: "Vendre ETF Monde, 400 € au marché" },
        { name: "Ordre 2", value: "Acheter ETF Obligations, 400 € au marché" },
      ],
    },
  ],
};

// --- The blocks gallery (?blocks) ---------------------------------------------

// A section of the gallery: what the block is, what it is for, and the
// view that shows it (one block, as Eriphe writes it).
export type GallerySection = { id: string; title: string; for: string; view: SampleView };

const section = (id: string, title: string, what: string, block: Authored, more: Partial<SampleView> = {}): GallerySection => ({
  id,
  title,
  for: what,
  view: { id, parts: [block], values: {}, notes: {}, bodies: {}, cards: [], ...more },
});

export const gallerySections: GallerySection[] = [
  section(
    "g-agenda-week",
    "Agenda : la semaine",
    "Ce qui vient d'un calendrier. Aujourd'hui heure par heure avec la ligne de maintenant, demain en colonne, les jours suivants en petites colonnes (une liste quand la page est étroite). Une invitation sans réponse et un créneau proposé sont des fantômes violets qui portent le Oui de leur carte ; une invitation porte le signe ambre de ce qui vient d'un autre.",
    agendaBlock,
    { values: { agenda: { events } }, cards: dayCards.map((c) => ({ ...c, node: "g-agenda-week" })) },
  ),
  section(
    "g-agenda-day",
    "Agenda : une journée",
    "Le même bloc avec days: 1 (ou absent) : la journée seule, heure par heure. Un statut se dit en mots (peut-être, annulé, refusé, à répondre), jamais comme la source l'écrit.",
    { name: "jour", sources: calendar, components: [{ id: "root", component: "Agenda", events: { "@path": "/agenda/events" } }] },
    { values: { agenda: { events } }, cards: dayCards.map((c) => ({ ...c, node: "g-agenda-day" })) },
  ),
  section("g-figures", "Rangée de chiffres", "Quelques chiffres côte à côte : une bande, des filets entre eux, l'écart signé à l'encre (▲ ▼), la petite courbe de leurs dernières valeurs (trend).", investFigures),
  section(
    "g-person",
    "Personne",
    "Qui attend quoi : le nom, ce qu'elle est, une phrase, quelques faits en lignes. Un téléphone et une adresse mail s'ouvrent d'un clic (tel:, mailto:), rien d'autre.",
    {
      name: "qui",
      components: [
        { id: "root", component: "Grid", size: "large", children: ["sam", "lea"] },
        {
          id: "sam",
          component: "Person",
          name: "Sam Martin",
          detail: "Artisan, la terrasse",
          note: "Attend ta réponse sur le devis depuis mardi.",
          facts: [
            { label: "Téléphone", value: "06 00 00 00 00" },
            { label: "Mail", value: "sam@example.org" },
            { label: "Prochain rendez-vous", value: "Mercredi, réunion de chantier" },
          ],
        },
        { id: "lea", component: "Person", name: "Léa", detail: "Amie", note: "T'invite au pique-nique de midi ; son anniversaire est demain." },
      ],
    },
  ),
  section("g-quote", "Citation", "Les mots d'un autre, tels qu'il les a écrits : qui, quand, et la page d'où ils viennent, en clair.", {
    name: "cite",
    external: true,
    components: [
      {
        id: "root",
        component: "Quote",
        text: "Bonjour,\nVoici le devis pour la terrasse : 4 200 € posé, début possible lundi 7 décembre. Tu peux me dire avant vendredi si ça te va ?",
        from: "Sam <sam@example.org>",
        date: at(-1, "18:12"),
      },
    ],
  }),
  section("g-night", "Ce qui s'est passé", "Timeline : quand toutes les étapes ont une date, la date passe en marge. Le point dit l'état ; le texte reste à l'encre.", nightBlock),
  section("g-work", "Avancée d'un travail", "Progress : le mot, « 3 sur 5 », le nom de l'étape en cours, et la barre de ses étapes.", workBlock),
  section("g-table", "Tableau avec écarts", "Table : une colonne change écrit l'écart signé (+ −), à l'encre, aligné à droite.", investTable),
];

export const sampleDays: SampleView[] = [dayView, investView, ...gallerySections.map((s) => s.view)];
