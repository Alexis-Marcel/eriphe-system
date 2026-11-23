import type { Authored, Note, SampleCard } from "../data/samplecore";
import { dayOf, investChart, investFigures, investQuote, investTable, pea } from "../data/sampledays";
import { mailBlock, samQuote, weekFigures } from "../data/sampleviews";

// Prototype « belles vues »: six pages from real use, each written twice
// as Eriphe would write its node's body: `before` in today's catalog (flat
// components, ids, children by id), `after` with the prototype's
// higher-level blocks (one component a block, keyed props with defaults).
// The text (the bilan, the decisions) is the same on both sides: only the
// blocks differ. Made-up data only: Sam, Léa, Alex, example.org.

// A source as the block writes it: a tool with its items, or node_find.
export type Source = { tool: string; args?: Record<string, unknown>; every?: number; items?: { path: string; id: string; title: string; detail?: string; open?: { tool: string; id: string } } };
// An « after » block: one component, no id, no children by id.
export type After = { name: string; external?: boolean; sources?: Record<string, Source>; components: Record<string, unknown> };
export type Before = Omit<Authored, "sources"> & { sources?: Record<string, Source> };

export type Scenario = {
  id: string;
  title: string;
  line: string; // what the page head says under the title
  standing: boolean;
  values: Record<string, unknown>; // the sources' last values, by name
  notes: Record<string, Note>;
  cards: SampleCard[];
  before: (string | Before)[];
  after: (string | After)[];
};

const ago = (min: number) => new Date(Date.now() - min * 60_000).toISOString();
const media = (name: string) => `/espace/media?url=${name}`;

// --- Mes mails: 124 mails, 4 to handle --------------------------------------

type Mail = { id: string; account: string; from: string; subject: string; date: string; snippet: string };
const mails: Mail[] = [];
const mailNotes: Record<string, Note> = {};
const mail = (id: string, account: string, from: string, subject: string, min: number, group: string, note: string, snippet = "") => {
  mails.push({ id, account, from, subject, date: ago(min), snippet });
  mailNotes[id] = { group, note, state: group === "À traiter" ? "act" : group === "Pour info" ? "info" : "none" };
};
mail("m1", "perso", "Sam <sam@example.org>", "Devis terrasse", 95, "À traiter", "Répondre à Sam — brouillon prêt, avant vendredi", "Tu peux me dire avant vendredi ?");
mail("m2", "perso", "Cabinet dentaire <rdv@cabinet.example.org>", "Votre rendez-vous de jeudi", 240, "À traiter", "Ajouter le rendez-vous de jeudi 10 h 30");
mail("m3", "travail", "Alex <alex@travail.example.org>", "Relecture du plan", 380, "À traiter", "Répondre à Alex — brouillon prêt");
mail("m4", "perso", "Agence <bail@agence.example.org>", "Avenant au bail", 1300, "À traiter", "À lire : l'avenant au bail, 3 pages, rien à signer");
const senders = ["La Boutique", "Mode & Co", "Le Journal du Jardin", "Voyages Express", "TechHebdo", "Recettes du Jour", "Sport Plus", "Meubles Design", "Cinéma Club", "Bricolage Pro", "Vins & Caves", "Librairie du Coin"];
const pubs = ["-40 % ce week-end seulement", "Votre code promo vous attend", "Nouvelle collection d'hiver", "Livraison offerte jusqu'à dimanche", "Dernière chance !", "Vous avez gagné un bon d'achat"];
for (let i = 0; i < 100; i++) {
  const who = senders[i % senders.length];
  const domain = who.toLowerCase().normalize("NFD").replace(/[^a-z]/g, "");
  const isNews = i % 2 === 0;
  mail(`t${i}`, i % 4 === 3 ? "travail" : "perso", `${who} <news@${domain}.example.org>`, isNews ? `La lettre de la semaine n° ${120 + i}` : pubs[i % pubs.length], 60 + i * 23, "À jeter", isNews ? "Newsletter jamais ouverte" : "Publicité");
}
mail("p1", "perso", "Livraison <suivi@colis-express.example.net>", "Votre colis est bloqué", 150, "À jeter", "Hameçonnage : faux transporteur, lien piégé");
mail("p2", "travail", "Support <securite@banque-en-ligne.example.net>", "Votre compte sera suspendu", 520, "À jeter", "Hameçonnage : se fait passer pour la banque");
const infos: [string, string, string][] = [
  ["Énergie <factures@energie.example.org>", "Votre facture de novembre", "62 €, prélevés le 5 décembre"],
  ["Colis <suivi@transport.example.org>", "Votre colis a été livré", "Livré hier, en boîte aux lettres"],
  ["Club de tennis <club@tennis.example.org>", "Entraînement de samedi", "Avancé à 9 h ce samedi"],
  ["RH <rh@travail.example.org>", "Fermeture du 24 décembre", "Bureau fermé le 24 décembre"],
  ["Banque <info@banque.example.org>", "Votre relevé de novembre", "Relevé disponible, rien d'inhabituel"],
  ["Léa <lea@example.org>", "Photos du week-end", "Léa partage 24 photos du week-end"],
  ["Mairie <contact@mairie.example.org>", "Collecte des encombrants", "Collecte mardi 1er décembre"],
  ["CI <ci@travail.example.org>", "Build réussi sur main", "Le build de main est repassé au vert"],
  ["Assurance <contrat@assurance.example.org>", "Votre attestation 2027", "Attestation jointe, à garder"],
  ["Mutuelle <remb@mutuelle.example.org>", "Remboursement effectué", "23,50 € remboursés"],
  ["École <info@ecole.example.org>", "Sortie du vendredi", "Sortie au musée vendredi, rien à signer"],
  ["Opérateur <factures@telecom.example.org>", "Votre facture mobile", "19,99 €, prélevés le 8 décembre"],
  ["Bibliothèque <prets@biblio.example.org>", "Rappel de retour", "2 livres à rendre avant le 3 décembre"],
  ["Covoiturage <noreply@covoit.example.org>", "Trajet confirmé", "Trajet Lyon samedi 7 h, confirmé"],
  ["Syndic <syndic@immeuble.example.org>", "Compte rendu de l'AG", "Ravalement voté pour le printemps"],
  ["Sam <sam@example.org>", "Photos de la terrasse", "Sam a envoyé les photos du chantier voisin"],
  ["Alex <alex@travail.example.org>", "Ordre du jour de mardi", "Ordre du jour de la réunion de mardi"],
  ["Pharmacie <rappel@pharmacie.example.org>", "Votre commande est prête", "Commande prête au comptoir"],
];
infos.forEach(([from, subject, note], i) => mail(`i${i}`, i % 3 === 2 ? "travail" : "perso", from, subject, 600 + i * 90, "Pour info", note));

const junkIds = mails.filter((m) => mailNotes[m.id].group === "À jeter").map((m) => m.id);
const mailCards: SampleCard[] = [
  { ref: "calls:141", card: 141, items: ["m1"], kind: "calls", state: "pending", tainted: true, node: "p-mails", text: "Envoyer la réponse à Sam · compte perso" },
  { ref: "calls:142", card: 142, items: ["m2"], kind: "calls", state: "pending", tainted: true, node: "p-mails", text: "Créer l'événement « Dentiste » jeudi 26 novembre, 10 h 30" },
  { ref: "calls:143", card: 143, items: ["m3"], kind: "calls", state: "pending", tainted: true, node: "p-mails", text: "Envoyer la réponse à Alex · compte travail" },
  {
    ref: "calls:144",
    card: 144,
    items: junkIds,
    kind: "calls",
    state: "pending",
    tainted: true,
    node: "p-mails",
    text: `Mettre ${junkIds.length} mails à la corbeille`,
    choices: junkIds.map((id, i) => ({ id: 1440 + i, text: id, n: 1, state: "proposed", items: [id] })),
  },
];

const mailBilan =
  "2 réponses attendues, dont Sam avant vendredi. 1 rendez-vous à confirmer. 2 tentatives d'hameçonnage dans le lot à jeter.\n\n**Pour info** · La facture d'électricité de novembre est arrivée (62 €, prélevés le 5 décembre). Ton colis a été livré hier. Le club avance l'entraînement de samedi à 9 h.\n\n";

const mailSource: Source = {
  tool: "mail/search_messages",
  args: { query: "in:inbox", max: 150 },
  items: { path: "messages", id: "id", title: "subject", detail: "from", open: { tool: "mail/read_message", id: "id" } },
};
const samQuoteRoot = samQuote.components[0] as Record<string, unknown>;
const weekMetrics = weekFigures.components.filter((c) => c.component === "Metric").map(({ id: _id, component: _c, ...m }) => m);

const mailsScenario: Scenario = {
  id: "mails",
  title: "Mes mails",
  line: "124 mails · relu il y a 4 min",
  standing: true,
  values: { mails: { messages: mails } },
  notes: mailNotes,
  cards: mailCards,
  before: [mailBilan, samQuote, { ...mailBlock, sources: { mails: mailSource } }, weekFigures],
  after: [
    mailBilan,
    { name: "sam", external: true, components: (({ id: _id, ...q }) => q)(samQuoteRoot) },
    { name: "mails", external: true, sources: { mails: mailSource }, components: { component: "Sections", items: "mails" } },
    { name: "semaine", components: { component: "Figures", title: "Cette semaine", items: weekMetrics } },
  ],
};

// --- Investissements ---------------------------------------------------------

const comp = (b: Authored, id: string) => b.components.find((c) => c.id === id) as Record<string, unknown>;
const strip = ({ id: _id, component: _c, ...rest }: Record<string, unknown>) => rest;
const investBilan =
  "Le PEA a pris 1,8 % cette semaine, porté par l'ETF Monde. Les actions pèsent 64 % pour une cible de 60 % : je propose de rééquilibrer 400 € vers les obligations.\n\n";
const lines = (investTable.data!.lignes as { nom: string; valeur: number; semaine: number; an: number; part: number }[]).map((l) => ({
  Ligne: l.nom,
  Valeur: l.valeur,
  Semaine: l.semaine,
  "Depuis janvier": l.an,
  Part: l.part,
}));

const investScenario: Scenario = {
  id: "invest",
  title: "Investissements",
  line: "PEA et assurance-vie · relu il y a 12 min",
  standing: true,
  values: {},
  notes: {},
  cards: [
    {
      ref: "calls:171",
      card: 171,
      kind: "calls",
      state: "pending",
      node: "p-invest",
      text: "Rééquilibrer le PEA : vendre 400 € d'ETF Monde, acheter 400 € d'ETF Obligations",
      why: "Les actions dépassent la cible de 4 points depuis trois semaines.",
    },
  ],
  before: [investBilan, investFigures, investChart, investTable, investQuote],
  after: [
    investBilan,
    { name: "chiffres", components: { component: "Figures", items: investFigures.components.filter((c) => c.component === "Metric").map(strip) } },
    { name: "courbe", components: { component: "Chart", title: "Le PEA sur 30 jours", unit: "€", series: { PEA: pea }, end: dayOf(0) } },
    {
      name: "repartition",
      components: {
        component: "Chart",
        kind: "stack",
        title: "Répartition et cible",
        unit: "%",
        series: { Actions: { "Aujourd'hui": 64, Cible: 60 }, Obligations: { "Aujourd'hui": 26, Cible: 30 }, Liquidités: { "Aujourd'hui": 10, Cible: 10 } },
      },
    },
    { name: "lignes", components: { component: "Table", title: "Les lignes du PEA", rows: lines, units: { Valeur: "€", Semaine: "±%", "Depuis janvier": "±%", Part: "%" } } },
    { name: "a_lire", external: true, components: { component: "Quote", ...strip(comp(investQuote, "q")) } },
  ],
};

// --- Cuisine: rated recipes, the fridge ---------------------------------------

type Recipe = { id: string; title: string; note: number; temps: number; manque: number; photo: string };
const recipes: Recipe[] = [
  ["Omelette aux épinards", 4, 15, 0, "vert"],
  ["Pâtes carbonara", 5, 20, 0, "jaune"],
  ["Quiche lorraine", 4, 55, 0, "jaune"],
  ["Risotto aux champignons", 5, 40, 1, "beige"],
  ["Curry de lentilles", 4, 35, 2, "orange"],
  ["Soupe de potiron", 3, 30, 1, "orange"],
  ["Gratin dauphinois", 5, 75, 1, "creme"],
  ["Salade niçoise", 3, 20, 3, "vert"],
  ["Poulet rôti au citron", 5, 90, 2, "camel"],
  ["Tarte aux pommes", 4, 60, 1, "camel"],
  ["Chili sin carne", 4, 45, 3, "rouge"],
  ["Croque-monsieur", 3, 15, 1, "beige"],
  ["Pancakes du dimanche", 4, 25, 0, "camel"],
  ["Ratatouille", 4, 60, 4, "rouge"],
].map(([title, note, temps, manque, color], i) => ({ id: `r${i}`, title: title as string, note: note as number, temps: temps as number, manque: manque as number, photo: media(`recette-${color}.svg`) }));
const nodeOf = (r: Recipe) => ({ id: r.id, title: r.title, status: "idle", changed: ago(60 * 24 * (3 + Number(r.id.slice(1)))), note: r.note, temps: r.temps, photo: r.photo });

const recipeBilan =
  "Avec ce qu'il reste au frigo, quatre recettes sont possibles ce soir, la plus rapide en 15 min. Les œufs sont à finir avant jeudi.\n\n## Le frigo\n\n- Œufs (6)\n- Crème fraîche\n- Épinards\n- Parmesan\n- Lardons\n- Citrons (2)\n- Beurre\n- Yaourts (4)\n- Lait\n\n";
const ceSoirSrc: Source = { tool: "eriphe-core/node_find", args: { tags: ["cuisine/recette"], where: [{ key: "manque", op: "=", value: 0 }], sort: "temps", columns: ["note", "temps", "photo"] } };
const recettesSrc: Source = { tool: "eriphe-core/node_find", args: { tags: ["cuisine/recette"], sort: "-note", columns: ["note", "temps"] } };
const askButtons = (labels: string[]): Before => ({
  name: "reponses",
  components: [
    { id: "root", component: "Row", children: labels.map((_, i) => `b${i}`) },
    ...labels.map((l, i) => ({ id: `b${i}`, component: "Button", ...(i === 0 ? { variant: "primary" } : {}), label: l, action: { event: { name: "ask", context: { text: l } } } })),
  ],
});

const recipesScenario: Scenario = {
  id: "cuisine",
  title: "Cuisine",
  line: "14 recettes · frigo noté hier soir",
  standing: true,
  values: {
    ce_soir: { nodes: recipes.filter((r) => !r.manque).sort((a, b) => a.temps - b.temps).map(nodeOf) },
    recettes: { nodes: [...recipes].sort((a, b) => b.note - a.note).map(nodeOf) },
  },
  notes: {},
  cards: [],
  before: [
    recipeBilan,
    {
      name: "ce_soir",
      sources: { ce_soir: ceSoirSrc },
      components: [
        { id: "root", component: "Column", children: ["h", "g"] },
        { id: "h", component: "Text", variant: "subtitle", text: "Possible ce soir" },
        { id: "g", component: "Grid", size: "small", children: { path: "/ce_soir/nodes", componentId: "c" } },
        { id: "c", component: "Card", child: "cc" },
        { id: "cc", component: "Column", children: ["img", "name", "meta"] },
        { id: "img", component: "Image", url: { "@path": "photo" }, alt: { "@path": "title" }, fit: "cover" },
        { id: "name", component: "Text", text: { "@path": "title" } },
        { id: "meta", component: "Text", variant: "caption", text: { "@call": "formatString", args: { value: "${temps} min · ${note}/5" } } },
      ],
    },
    {
      name: "recettes",
      sources: { recettes: recettesSrc },
      components: [
        { id: "root", component: "Column", children: ["h", "t"] },
        { id: "h", component: "Text", variant: "subtitle", text: "Toutes mes recettes" },
        {
          id: "t",
          component: "Table",
          columns: [
            { key: "title", label: "Recette" },
            { key: "note", label: "Note (/5)", kind: "number" },
            { key: "temps", label: "Temps (min)", kind: "number" },
          ],
          rows: { "@path": "/recettes/nodes" },
        },
      ],
    },
    askButtons(["Fais la liste de courses", "Propose le menu de la semaine"]),
  ],
  after: [
    recipeBilan,
    { name: "ce_soir", sources: { ce_soir: ceSoirSrc }, components: { component: "Gallery", title: "Possible ce soir", items: "ce_soir", units: { temps: "min", note: "★" } } },
    { name: "recettes", sources: { recettes: recettesSrc }, components: { component: "Table", title: "Toutes mes recettes", rows: "recettes", units: { note: "★", temps: "min" } } },
    { name: "reponses", components: { component: "Asks", items: ["Fais la liste de courses", "Propose le menu de la semaine"] } },
  ],
};

// --- Garde-robe: pieces with photos -------------------------------------------

type Piece = { id: string; title: string; categorie: string; photo: string; portee: number };
const pieces: Piece[] = [
  ["Chemise oxford bleue", "Hauts", "haut-bleu", 14],
  ["T-shirt blanc", "Hauts", "haut-blanc", 22],
  ["Pull col rond marine", "Hauts", "pull-marine", 9],
  ["Pull camionneur gris", "Hauts", "pull-gris", 0],
  ["Sweat vert sauge", "Hauts", "pull-vert", 3],
  ["Chemise lin beige", "Hauts", "haut-beige", 0],
  ["Jean brut", "Bas", "bas-marine", 31],
  ["Chino beige", "Bas", "bas-beige", 8],
  ["Pantalon de laine gris", "Bas", "bas-gris", 0],
  ["Jean noir", "Bas", "bas-noir", 12],
  ["Manteau camel", "Vestes", "veste-camel", 6],
  ["Veste en jean", "Vestes", "veste-bleu", 4],
  ["Doudoune noire", "Vestes", "veste-noir", 11],
  ["Blazer marine", "Vestes", "veste-marine", 0],
  ["Baskets blanches", "Chaussures", "chaussure-blanc", 27],
  ["Boots marron", "Chaussures", "chaussure-brun", 10],
  ["Derbies noires", "Chaussures", "chaussure-noir", 0],
  ["Robe chemise rouge", "Robes", "robe-rouge", 2],
].map(([title, categorie, pic, portee], i) => ({ id: `w${i}`, title: title as string, categorie: categorie as string, photo: media(`${pic}.svg`), portee: portee as number }));
const piecesSrc: Source = { tool: "eriphe-core/node_find", args: { tags: ["garde-robe"], sort: "categorie", columns: ["categorie", "photo", "portee"] } };
const outfit = [pieces[2], pieces[7], pieces[10], pieces[15]].map((p) => ({ title: p.title, photo: p.photo }));

const wardrobeBilan =
  "Demain 8° et pluie l'après-midi : je propose le pull marine, le chino beige, le manteau camel et les boots. Cinq pièces n'ont pas été portées cette saison.\n\n";
const wardrobeScenario: Scenario = {
  id: "garde-robe",
  title: "Garde-robe",
  line: "18 pièces · météo de demain lue à 7 h",
  standing: true,
  values: { pieces: { nodes: pieces.map((p) => ({ id: p.id, title: p.title, status: "idle", changed: ago(4000), categorie: p.categorie, photo: p.photo, portee: p.portee })) } },
  notes: {},
  cards: [],
  before: [
    wardrobeBilan,
    {
      name: "tenue",
      components: [
        { id: "root", component: "Column", children: ["h", "g"] },
        { id: "h", component: "Text", variant: "subtitle", text: "La tenue de demain" },
        { id: "g", component: "Grid", size: "small", children: { path: "/tenue", componentId: "c" } },
        { id: "c", component: "Card", child: "cc" },
        { id: "cc", component: "Column", children: ["img", "t"] },
        { id: "img", component: "Image", url: { "@path": "photo" }, alt: { "@path": "title" }, fit: "cover" },
        { id: "t", component: "Text", variant: "caption", text: { "@path": "title" } },
      ],
      data: { tenue: outfit },
    },
    {
      name: "chiffres",
      components: [
        { id: "root", component: "Row", children: ["n", "jamais", "plus"] },
        { id: "n", component: "Metric", label: "Pièces", value: 18 },
        { id: "jamais", component: "Metric", label: "Jamais portées cette saison", value: 5 },
        { id: "plus", component: "Metric", label: "La plus portée", value: "Jean brut", deltaLabel: "31 fois" },
      ],
    },
    {
      name: "pieces",
      sources: { pieces: piecesSrc },
      components: [
        { id: "root", component: "Column", children: ["h", "g"] },
        { id: "h", component: "Text", variant: "subtitle", text: "Toutes mes pièces" },
        { id: "g", component: "Grid", size: "small", children: { path: "/pieces/nodes", componentId: "c" } },
        { id: "c", component: "Card", child: "cc" },
        { id: "cc", component: "Column", children: ["img", "name", "meta"] },
        { id: "img", component: "Image", url: { "@path": "photo" }, alt: { "@path": "title" }, fit: "cover" },
        { id: "name", component: "Text", text: { "@path": "title" } },
        { id: "meta", component: "Text", variant: "caption", text: { "@call": "formatString", args: { value: "${categorie} · portée ${portee} fois" } } },
      ],
    },
  ],
  after: [
    wardrobeBilan,
    { name: "tenue", components: { component: "Gallery", title: "La tenue de demain", items: outfit } },
    {
      name: "chiffres",
      components: { component: "Figures", items: [{ label: "Pièces", value: 18 }, { label: "Jamais portées cette saison", value: 5 }, { label: "La plus portée", value: "Jean brut", deltaLabel: "31 fois" }] },
    },
    { name: "pieces", sources: { pieces: piecesSrc }, components: { component: "Gallery", title: "Toutes mes pièces", items: "pieces", by: "categorie", units: { portee: "fois" } } },
  ],
};

// --- Projet: a subject, its steps, its notes ------------------------------------

const steps = [
  { label: "Choisir le bois", state: "done", date: "2026-11-02", detail: "Pin traité classe 4" },
  { label: "Devis de Sam signé", state: "done", date: "2026-11-18" },
  { label: "Verser l'acompte de 30 %", state: "current", date: "2026-11-27", detail: "1 260 €, avant vendredi" },
  { label: "Commande du bois", state: "pending", date: "2026-11-30" },
  { label: "Début du chantier", state: "pending", date: "2026-12-07" },
  { label: "Réception des travaux", state: "pending", date: "2026-12-18" },
];
const projectBilan =
  "Le devis de Sam est signé : il attend l'acompte de 30 % (1 260 €) avant vendredi pour commander le bois. Le chantier commence lundi 7 décembre.\n\n## Ce qu'on a décidé\n\n- Pin traité classe 4, pas de composite\n- Pose par Sam, 4 200 € posé\n- Début lundi 7 décembre\n\n## Ensuite\n\n";
const projectNotes =
  "## Notes\n\nLa mairie n'exige pas de déclaration sous 20 m² : la terrasse fait 18 m². Voir [[Devis de Sam]] et [[Choix du bois]]. Léa prête sa scie circulaire pour les finitions.\n\n";
const samPerson = {
  name: "Sam Martin",
  detail: "Artisan, la terrasse",
  note: "Attend l'acompte pour commander le bois.",
  facts: [
    { label: "Téléphone", value: "06 00 00 00 00" },
    { label: "Mail", value: "sam@example.org" },
  ],
};
const projectScenario: Scenario = {
  id: "projet",
  title: "Terrasse",
  line: "Sujet · changé il y a 2 h",
  standing: false,
  values: {},
  notes: {},
  cards: [],
  before: [
    projectBilan,
    {
      name: "etapes",
      components: [
        { id: "root", component: "Column", children: ["p", "t"] },
        { id: "p", component: "Progress", label: "En cours", value: 2, current: 1, total: 6, step: "Verser l'acompte de 30 %" },
        { id: "t", component: "Timeline", items: steps },
      ],
    },
    { name: "sam", components: [{ id: "root", component: "Person", ...samPerson }] },
    projectNotes,
    askButtons(["Prépare le virement de l'acompte", "Relance Sam pour la date"]),
  ],
  after: [
    projectBilan,
    { name: "etapes", components: { component: "Timeline", items: steps } },
    { name: "sam", components: { component: "Person", ...samPerson } },
    projectNotes,
    { name: "reponses", components: { component: "Asks", items: ["Prépare le virement de l'acompte", "Relance Sam pour la date"] } },
  ],
};

// --- Première fois: a view just made, nothing read yet ---------------------------

const readsSource: Source = {
  tool: "lectures/list_articles",
  args: { unread: true },
  every: 60,
  items: { path: "articles", id: "id", title: "title", detail: "site", open: { tool: "lectures/read_article", id: "id" } },
};
const firstBilan = "Je relis tes flux toutes les heures et je te garde ici ce qui vaut la peine d'être lu, le plus court d'abord.\n\n";
export const articles = [
  { id: "a1", title: "Pourquoi les graphes de notes vieillissent mal", site: "blog.example.org" },
  { id: "a2", title: "Dix minutes pour un meilleur café filtre", site: "cafe.example.org" },
  { id: "a3", title: "Le retour des trains de nuit", site: "journal.example.org" },
];
export const articleNotes: Record<string, Note> = {
  a1: { group: "À lire", note: "8 min · sur tes notes, très proche de ton article", state: "look" },
  a2: { group: "À lire", note: "3 min · la recette en 5 étapes", state: "look" },
  a3: { group: "Pour info", note: "Paris–Nice relancé en avril", state: "info" },
};
const firstScenario: Scenario = {
  id: "premiere",
  title: "Mes lectures",
  line: "Vue créée il y a 1 min",
  standing: true,
  values: { lectures: { articles: [] } },
  notes: {},
  cards: [],
  before: [
    firstBilan,
    {
      name: "lectures",
      sources: { lectures: readsSource },
      components: [
        { id: "root", component: "Column", children: ["h", "l", "info"] },
        { id: "h", component: "Text", variant: "subtitle", text: "À lire" },
        { id: "l", component: "List", children: { path: "/lectures/articles[_group=À lire][_state!=done]", componentId: "row" }, emptyText: "Rien à lire pour l'instant : je relis tes flux toutes les heures." },
        { id: "row", component: "Item", title: { "@path": "title" }, detail: { "@path": "site" }, note: { "@path": "_note" } },
        { id: "info", component: "Fold", label: "Les {n} articles pour info", child: "infos" },
        { id: "infos", component: "List", children: { path: "/lectures/articles[_group=Pour info]", componentId: "inforow" } },
        { id: "inforow", component: "Item", level: "low", title: { "@path": "title" }, detail: { "@path": "site" }, note: { "@path": "_note" } },
      ],
    },
  ],
  after: [firstBilan, { name: "lectures", sources: { lectures: readsSource }, components: { component: "Sections", items: "lectures" } }],
};

export const scenarios: Scenario[] = [mailsScenario, investScenario, recipesScenario, wardrobeScenario, projectScenario, firstScenario];
export const scenarioOf = (id: string | null) => scenarios.find((s) => s.id === id) ?? scenarios[0];
