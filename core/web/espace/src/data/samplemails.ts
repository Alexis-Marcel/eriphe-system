import type { OrbFeed, Tick } from "./api";
import { movedCards, pushSources, type Authored, type SampleCard, type SampleView } from "./samplecore";

// « Mes mails », the dev server's view, for the prototype « Eriphe coche,
// tu cliques » (proto/eriphe-coche, PROTO.md). Two accounts (perso,
// travail), a few mails that ask the user (each with its card, as today),
// and the inbox. Eriphe proposes what to do with inbox mails by ticking
// them, with a reason; the page's bar does them (a2ui/ticks.ts).
// ?avant shows the same moments as main does them today: a calls card per
// account and action, its Oui on every row it acts on (call_fold.go).
// Made-up data only: Sam, Léa, Alex, example.org.

const ago = (min: number) => new Date(Date.now() - min * 60_000).toISOString();
export const avant = typeof location !== "undefined" && new URLSearchParams(location.search).has("avant");

// A section of things that ask the user: one line per item, what Eriphe
// proposes (its note); the page draws the Oui of the card that waits on
// it beside it (code's, never the block's).
export const todo = (prefix: string, source: string, path: string, group: string, empty: string) => [
  { id: `${prefix}_h`, component: "Text", variant: "subtitle", text: group },
  {
    id: prefix,
    component: "List",
    children: { path: `/${source}/${path}[_group=${group}][_state!=done]`, componentId: `${prefix}_item` },
    emptyText: empty,
  },
];

const mailBlock: Authored = {
  name: "mails",
  external: true,
  sources: {
    mails: {
      tool: "mail/search_messages",
      args: { query: "in:inbox", max: 100 },
      items: { path: "messages", id: "id", title: "subject", detail: "from", open: { tool: "mail/read_message", id: "id" } },
    },
  },
  components: [
    { id: "root", component: "Column", children: ["todo_h", "todo", "inbox_h", "inbox"] },
    ...todo("todo", "mails", "messages", "À traiter", "Rien ne t'attend."),
    { id: "todo_item", component: "Item", title: { "@path": "_note" }, detail: { "@path": "from" }, note: { "@path": "subject" } },
    { id: "inbox_h", component: "Text", variant: "subtitle", text: "Boîte de réception" },
    { id: "inbox", component: "List", children: { path: "/mails/messages[_group=Boîte][_state!=done]", componentId: "inbox_row" }, emptyText: "Boîte vide." },
    { id: "inbox_row", component: "Item", level: "low", title: { "@path": "subject" }, detail: { "@path": "from" }, note: { "@path": "account" } },
  ],
};

type Mail = { id: string; account: string; from: string; subject: string; date: string; snippet: string };
const mails: Mail[] = [];
const notes: SampleView["notes"] = {};
const bodies: SampleView["bodies"] = {};
// what the user asked Eriphe in scenario A, ticked once asked; what the
// triage skill ticked on its own (the CI failures a later build replaced)
const asked: Record<string, Tick> = {};
const ticks: Record<string, Tick> = {};

type Spec = { id: string; account: string; from: string; subject: string; min: number; body?: string };
const add = (m: Spec, group: string, note = "") => {
  const { min, body, ...rest } = m;
  mails.push({ ...rest, snippet: "", date: ago(min) });
  notes[m.id] = { group, note, state: group === "À traiter" ? "act" : "none" };
  bodies[m.id] = { title: m.subject, from: m.from, date: ago(min), body: body ?? "…", raw: JSON.stringify({ id: m.id, account: m.account, folder: "INBOX" }, null, 2), external: true };
};
const trash = (reason: string, account: string): Tick => ({ action: "corbeille", reason, account });
const archive = (reason: string, account: string): Tick => ({ action: "archiver", reason, account });

// --- À traiter: what asks the user, each with its card ---------------------

add({ id: "m1", account: "perso", from: "Sam <sam@example.org>", subject: "Devis terrasse", min: 95, body: "Bonjour,\n\nVoici le devis pour la terrasse : 4 200 € posé, début possible lundi 7 décembre.\nTu peux me dire avant vendredi si ça te va ?\n\nSam" }, "À traiter", "Répondre à Sam — brouillon prêt, avant vendredi");
add({ id: "m2", account: "perso", from: "Cabinet dentaire <rdv@cabinet.example.org>", subject: "Votre rendez-vous de jeudi", min: 240, body: "Nous vous confirmons votre rendez-vous le jeudi 26 novembre à 10 h 30." }, "À traiter", "Ajouter le rendez-vous de jeudi 10 h 30");
add({ id: "m3", account: "travail", from: "Alex <alex@travail.example.org>", subject: "Relecture du plan", min: 380, body: "Salut,\n\nTu aurais cinq minutes pour relire le plan avant la réunion de mardi ?\n\nAlex" }, "À traiter", "Répondre à Alex — brouillon prêt");
add({ id: "m4", account: "perso", from: "Agence <bail@agence.example.org>", subject: "Avenant au bail", min: 1300 }, "À traiter", "À lire : l'avenant au bail, 3 pages, rien à signer");

// --- The inbox -------------------------------------------------------------

const inbox: (Spec & { ask?: Tick; skill?: Tick })[] = [
  { id: "c3", account: "travail", from: "CI <ci@forge.example.org>", subject: "Réussi : build #813 sur main", min: 150 },
  { id: "c1", account: "travail", from: "CI <ci@forge.example.org>", subject: "Échec : build #812 sur main", min: 200, skill: trash("échec CI remplacé", "travail") },
  { id: "l1", account: "perso", from: "Léa <lea@example.org>", subject: "Photos du week-end", min: 260 },
  { id: "c2", account: "travail", from: "CI <ci@forge.example.org>", subject: "Échec : build #809 sur main", min: 330, skill: trash("échec CI remplacé", "travail") },
  { id: "e1", account: "perso", from: "Énergie <factures@energie.example.org>", subject: "Votre facture de novembre", min: 600 },
  { id: "b1", account: "perso", from: "Backblaze <billing@backblaze.example.org>", subject: "Votre reçu B2 — novembre", min: 700, ask: trash("Backblaze", "perso") },
  { id: "w1", account: "travail", from: "AWS <no-reply@aws.example.org>", subject: "Alerte de facturation : seuil de 10 $ atteint", min: 900, ask: trash("AWS", "travail") },
  { id: "k1", account: "perso", from: "Colis <suivi@transport.example.org>", subject: "Votre colis a été livré", min: 960 },
  { id: "f1", account: "perso", from: "Box internet <factures@box.example.org>", subject: "Votre facture de novembre est réglée", min: 1000, ask: archive("facture payée", "perso") },
  { id: "t1", account: "perso", from: "Club de tennis <club@tennis.example.org>", subject: "Entraînement de samedi", min: 1100 },
  { id: "w2", account: "travail", from: "AWS <no-reply@aws.example.org>", subject: "Votre facture AWS de novembre", min: 1400, ask: trash("AWS", "travail") },
  { id: "h1", account: "travail", from: "RH <rh@travail.example.org>", subject: "Fermeture du 24 décembre", min: 1500 },
  { id: "f2", account: "perso", from: "Assurance <contrat@assurance.example.org>", subject: "Échéance de novembre prélevée", min: 1700, ask: archive("facture payée", "perso") },
  { id: "b2", account: "perso", from: "Backblaze <billing@backblaze.example.org>", subject: "Rapport de sauvegarde hebdomadaire", min: 2000, ask: trash("Backblaze", "perso") },
  { id: "f3", account: "travail", from: "Comptabilité <compta@travail.example.org>", subject: "Note de frais n° 42 remboursée", min: 2300, ask: archive("remboursée", "travail") },
  { id: "w3", account: "travail", from: "AWS <no-reply@aws.example.org>", subject: "Nouveautés de la semaine", min: 2600, ask: trash("AWS", "travail") },
  { id: "s3", account: "perso", from: "SNCF <voyages@sncf.example.org>", subject: "Votre avis compte : notez votre voyage", min: 3000, ask: trash("SNCF · enquête", "perso") },
  { id: "f4", account: "perso", from: "Mobile <factures@mobile.example.org>", subject: "Facture de novembre — payée", min: 3400, ask: archive("facture payée", "perso") },
  { id: "b3", account: "perso", from: "Backblaze <billing@backblaze.example.org>", subject: "Votre reçu B2 — octobre", min: 4300, ask: trash("Backblaze", "perso") },
  { id: "f5", account: "perso", from: "Énergie <factures@energie.example.org>", subject: "Facture d'octobre — payée", min: 5200, ask: archive("facture payée", "perso") },
  { id: "s2", account: "perso", from: "SNCF <voyages@sncf.example.org>", subject: "Échange confirmé : Lyon → Paris", min: 11000, ask: trash("SNCF · échange passé", "perso") },
  { id: "s1", account: "perso", from: "SNCF <voyages@sncf.example.org>", subject: "Votre e-billet Paris → Lyon du 14 novembre", min: 12000, ask: trash("SNCF · trajet passé", "perso") },
];
// the newsletters and ads, between them (9 perso, 3 travail)
const senders = ["La Boutique", "Mode & Co", "Le Journal du Jardin", "Voyages Express", "TechHebdo", "Recettes du Jour", "Sport Plus", "Meubles Design", "Club Lecture", "Bricolage Pro", "Ciné Hebdo", "Vins & Terroirs"];
const pubs = ["-40 % ce week-end seulement", "Votre code promo vous attend", "Nouvelle collection d'hiver", "Livraison offerte jusqu'à dimanche", "Dernière chance !", "Vous avez gagné un bon d'achat"];
senders.forEach((who, i) => {
  const news = i % 2 === 0;
  const account = i % 4 === 3 ? "travail" : "perso";
  const domain = who.toLowerCase().normalize("NFD").replace(/[^a-z]/g, "");
  inbox.push({ id: `p${i}`, account, from: `${who} <news@${domain}.example.org>`, subject: news ? `La lettre de la semaine n° ${120 + i}` : pubs[i % pubs.length], min: 120 + i * 410, ask: trash(news ? "newsletter" : "pub", account) });
});
inbox.sort((a, b) => a.min - b.min);
for (const m of inbox) {
  add(m, "Boîte");
  if (m.ask) asked[m.id] = m.ask;
  if (m.skill) ticks[m.id] = m.skill;
}

// --- Cards: what is not a row of a list (send a mail, add an event) ---------

const sam = "Bonjour Sam,\n\nMerci pour le devis. 4 200 € posé me va, et lundi 7 décembre aussi.\nPeux-tu m'envoyer le bon à signer ?\n\nBonne soirée";
const alex = "Salut Alex,\n\nOui, je le relis demain matin et je te fais mes retours avant midi.\n\nÀ mardi";
const cards: SampleCard[] = [
  {
    ref: "calls:41",
    card: 41,
    items: ["m1"],
    kind: "calls",
    state: "pending",
    tainted: true,
    node: "mails",
    text: "Envoyer la réponse à Sam · compte perso",
    fields: [
      { name: "Ta réponse, prête", value: sam },
      { name: "mail/send_message", value: `account : perso\nreply_to : m1\nbody : ${sam.slice(0, 60)}…`, detail: true },
    ],
  },
  {
    ref: "calls:42",
    card: 42,
    items: ["m2"],
    kind: "calls",
    state: "pending",
    tainted: true,
    node: "mails",
    text: "Créer l'événement « Dentiste » jeudi 26 novembre, 10 h 30 – 11 h 15",
    fields: [{ name: "calendar/create_event", value: "title : Dentiste\nstart : 2026-11-26T10:30:00+01:00\nend : 2026-11-26T11:15:00+01:00", detail: true }],
  },
  {
    ref: "calls:43",
    card: 43,
    items: ["m3"],
    kind: "calls",
    state: "pending",
    tainted: true,
    node: "mails",
    text: "Envoyer la réponse à Alex · compte travail",
    fields: [
      { name: "Ta réponse, prête", value: alex },
      { name: "mail/send_message", value: `account : travail\nreply_to : m3\nbody : ${alex.slice(0, 60)}…`, detail: true },
    ],
  },
  { ref: "runcalls:30", card: 30, kind: "runcalls", state: "approved", exec: "done", node: "mails", text: "Archiver 12 mails" },
];

// --- ?avant: the same ticks as main's cards ---------------------------------

const verb = (action: string, n: number) => (action === "archiver" ? `Archiver ${n === 1 ? "1 mail" : `${n} mails`}` : `Mettre ${n === 1 ? "1 mail" : `${n} mails`} à la corbeille`);
const subjectOf = (id: string) => mails.find((m) => m.id === id)!;
let nextCard = 60;
// asCards makes main's calls cards of ticks: one card per action and
// account (another account is another argument: another card).
function asCards(ts: Record<string, Tick>) {
  const groups = new Map<string, string[]>();
  for (const [id, t] of Object.entries(ts)) groups.set(`${t.action}\u0000${t.account}`, [...(groups.get(`${t.action}\u0000${t.account}`) ?? []), id]);
  for (const [key, ids] of groups) {
    const [action, account] = key.split("\u0000");
    const card = nextCard++;
    cards.push({
      ref: `calls:${card}`,
      card,
      items: ids,
      kind: "calls",
      state: "pending",
      tainted: true,
      node: "mails",
      text: `${verb(action, ids.length)} · compte ${account}`,
      choices: ids.map((id, i) => ({ id: card * 100 + i, text: `« ${subjectOf(id).subject} » · ${subjectOf(id).from}`, n: 1, state: "proposed", items: [id] })),
      acts: ids.map((_, i) => verb(action, i + 1)),
    });
  }
}

export const mailView: SampleView = {
  id: "mails",
  parts: [""],
  values: { mails: { messages: mails } },
  notes,
  bodies,
  cards,
  ticks: avant ? {} : ticks,
  actions: { corbeille: { label: "Corbeille", done: "{n} à la corbeille" }, archiver: { label: "Archiver", done: "{n} archivé(s)" } },
  bilan: (v) => {
    const open = (id: string) => v.notes[id]?.state !== "done";
    const answers = ["m1", "m3"].filter(open).length;
    const left = mails.filter((m) => v.notes[m.id]?.group === "Boîte" && open(m.id)).length;
    const parts: string[] = [];
    if (answers) parts.push(answers === 2 ? "2 réponses attendues, dont Sam avant vendredi." : "1 réponse attendue.");
    if (open("m2")) parts.push("1 rendez-vous à confirmer.");
    parts.push(`${left} mails dans la boîte de réception, sur tes deux comptes.`);
    return `${parts.join(" ")}\n\n`;
  },
};
mailView.parts = [mailView.bilan!(mailView), mailBlock];
if (avant) asCards(ticks); // the triage skill's older card, apart

// --- The scenarios (the dev panel, ?coche=a, ?coche=ab) --------------------

let askedDone = false;
// cocheAsk is scenario A: the user said « supprime Backblaze, AWS, SNCF,
// les newsletters et les pubs, archive les factures déjà payées ». Eriphe
// ticks the rows and answers in one line; ?avant, it makes its cards and
// the orb points at one.
export function cocheAsk(): OrbFeed | null {
  if (askedDone) return null;
  askedDone = true;
  const n = Object.values({ ...mailView.ticks, ...asked }).filter((t) => t.action === "corbeille").length;
  const a = Object.values(asked).filter((t) => t.action === "archiver").length;
  if (avant) {
    const first = nextCard;
    asCards(asked);
    pushSources(mailView);
    return { asked: 21, answer: { seq: 22, node: "mails", text: `J'ai préparé ${nextCard - first} propositions dans « Mes mails » : ${n} mails à la corbeille, ${a} à archiver, par compte.` }, wait: { card: first, node: "mails" } };
  }
  Object.assign(mailView.ticks!, asked);
  pushSources(mailView);
  return { asked: 21, answer: { seq: 22, node: "mails", text: `C'est coché dans « Mes mails » : ${n} à la corbeille, ${a} factures payées à archiver. Décoche ce que tu gardes.` } };
}

let arrived = 0;
// cocheArrive is scenario B: a new mail comes while the page is open; the
// triage skill ticks it with its reason. Nothing else moves: no card, no
// words in the orb (the node's halo only). ?avant, its call folds into
// the oldest untouched card of the same account and action.
export function cocheArrive() {
  const id = `n${arrived++}`;
  const offers = ["Soldes d'hiver : -50 % sur tout", "Votre panier vous attend", "Offre réservée aux membres"];
  mails.unshift({ id, account: "perso", from: "Mode & Co <news@modeco.example.org>", subject: offers[(arrived - 1) % offers.length], date: ago(0), snippet: "" });
  notes[id] = { group: "Boîte", note: "", state: "none" };
  bodies[id] = { title: offers[0], from: "Mode & Co", date: ago(0), body: "…", external: true };
  if (avant) {
    const c = cards.find((x) => x.state === "pending" && x.text.includes("à la corbeille") && x.text.endsWith("compte perso"));
    if (c) {
      c.items = [id, ...(c.items ?? [])];
      c.choices = [{ id: c.card! * 100 + 90 + arrived, text: `« ${mails[0].subject} » · ${mails[0].from}`, n: 1, state: "proposed", items: [id] }, ...(c.choices ?? [])];
      c.acts = c.choices.map((_, i) => verb("corbeille", i + 1));
      c.text = `${verb("corbeille", c.items.length)} · compte perso`;
      movedCards([c.ref]);
    } else asCards({ [id]: { ...trash("pub", "perso") } });
  } else mailView.ticks![id] = { ...trash("pub", "perso"), at: new Date().toISOString() };
  pushSources(mailView);
}
