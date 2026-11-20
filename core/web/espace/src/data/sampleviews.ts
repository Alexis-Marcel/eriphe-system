import type { Authored, SampleView } from "./samplecore";

// The dev server's views, written as Eriphe writes them (eriphe:block
// JSON, the catalog's generic pieces only: no component, rule or word of a
// service): « Mes mails », and « Alertes de la prod » to show the very
// same pieces serve another matter. Made-up data: Sam, example.org.

const ago = (min: number) => new Date(Date.now() - min * 60_000).toISOString();

// --- Mes mails ------------------------------------------------------------

// A section of things that ask the user: one line per item, what Eriphe
// proposes (its note) and its one Oui (a card that waits on it).
const todo = (prefix: string, source: string, path: string, group: string, empty: string) => [
  { id: `${prefix}_h`, component: "Text", variant: "subtitle", text: group },
  {
    id: prefix,
    component: "List",
    children: { path: `/${source}/${path}[_group=${group}][_state!=done]`, componentId: `${prefix}_row` },
    emptyText: empty,
  },
  { id: `${prefix}_row`, component: "Row", align: "center", children: [`${prefix}_item`, `${prefix}_yes`] },
  { id: `${prefix}_yes`, component: "Proposal", inline: true, proposalId: { "@path": "_card" } },
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
  actions: {
    jeter: { label: "Jeter", tool: "mail/trash", args: { account: { "@item": "account" } }, items: "ids", source: "mails", list: "jeter" },
  },
  components: [
    { id: "root", component: "Column", children: ["todo_h", "todo", "trash", "info"] },
    ...todo("todo", "mails", "messages", "À traiter", "Rien ne t'attend."),
    { id: "todo_item", component: "Item", title: { "@path": "_note" }, detail: { "@path": "from" }, note: { "@path": "subject" } },
    { id: "trash", component: "Fold", list: "jeter", label: "{n} mails inutiles à jeter", child: "jeter" },
    { id: "jeter", component: "List", children: { path: "/mails/messages[_group=À jeter]", componentId: "jeter_row" } },
    { id: "jeter_row", component: "Item", level: "low", title: { "@path": "subject" }, detail: { "@path": "from" }, note: { "@path": "_note" } },
    { id: "info", component: "Fold", list: "infos", label: "Les {n} mails pour info", child: "infos" },
    { id: "infos", component: "List", children: { path: "/mails/messages[_group=Pour info]", componentId: "info_row" } },
    { id: "info_row", component: "Item", level: "low", title: { "@path": "subject" }, detail: { "@path": "from" }, note: { "@path": "_note" } },
  ],
};

const decidedBlock: Authored = {
  name: "decide",
  components: [
    { id: "root", component: "Fold", label: "Ce qu'on a décidé", child: "md" },
    {
      id: "md",
      component: "Markdown",
      text: "- Les deux comptes, perso et travail.\n- Ne jamais rater Sam, l'école, la banque.\n- Me prévenir au brief du matin ; tout de suite pour l'école.\n- Préparer les réponses en brouillon, jeter les pubs et les newsletters sur mon oui.",
    },
  ],
};

type Mail = { id: string; account: string; from: string; subject: string; date: string; snippet: string };
const mails: Mail[] = [];
const notes: SampleView["notes"] = {};
const bodies: SampleView["bodies"] = {};
const mail = (m: Omit<Mail, "date"> & { min: number }, group: string, note: string, body: string) => {
  const { min, ...rest } = m;
  mails.push({ ...rest, date: ago(min) });
  notes[m.id] = { group, note, state: group === "À traiter" ? "act" : group === "Pour info" ? "info" : "none" };
  bodies[m.id] = { title: m.subject, from: m.from, date: ago(min), body, raw: JSON.stringify({ id: m.id, account: m.account, folder: "INBOX" }, null, 2), external: true };
};

mail(
  { id: "m1", account: "perso", from: "Sam <sam@example.org>", subject: "Devis terrasse", snippet: "Tu peux me dire avant vendredi ?", min: 95 },
  "À traiter",
  "Répondre à Sam — brouillon prêt, avant vendredi",
  "Bonjour,\n\nVoici le devis pour la terrasse : 4 200 € posé, début possible lundi 7 décembre.\nTu peux me dire avant vendredi si ça te va ?\n\nSam",
);
mail(
  { id: "m2", account: "perso", from: "Cabinet dentaire <rdv@cabinet.example.org>", subject: "Votre rendez-vous de jeudi", snippet: "Jeudi 26 novembre à 10 h 30", min: 240 },
  "À traiter",
  "Ajouter le rendez-vous de jeudi 10 h 30",
  "Bonjour,\n\nNous vous confirmons votre rendez-vous le jeudi 26 novembre à 10 h 30.\nMerci de prévenir 48 h à l'avance en cas d'empêchement.",
);
mail(
  { id: "m3", account: "travail", from: "Alex <alex@travail.example.org>", subject: "Relecture du plan", snippet: "Tu as cinq minutes pour relire ?", min: 380 },
  "À traiter",
  "Répondre à Alex — brouillon prêt",
  "Salut,\n\nTu aurais cinq minutes pour relire le plan avant la réunion de mardi ?\n\nAlex",
);
mail(
  { id: "m4", account: "perso", from: "Agence <bail@agence.example.org>", subject: "Avenant au bail", snippet: "Veuillez trouver l'avenant…", min: 1300 },
  "À traiter",
  "À lire : l'avenant au bail, 3 pages, rien à signer",
  "Madame, Monsieur,\n\nVeuillez trouver ci-joint l'avenant au bail (révision annuelle du loyer, +1,2 %).\nAucune signature n'est demandée.",
);

const news = ["La Boutique", "Mode & Co", "Le Journal du Jardin", "Voyages Express", "TechHebdo", "Recettes du Jour", "Sport Plus", "Meubles Design"];
const pubs = ["-40 % ce week-end seulement", "Votre code promo vous attend", "Nouvelle collection d'hiver", "Livraison offerte jusqu'à dimanche", "Dernière chance !", "Vous avez gagné un bon d'achat"];
for (let i = 0; i < 36; i++) {
  const isNews = i % 2 === 0;
  const who = news[i % news.length];
  const domain = who.toLowerCase().normalize("NFD").replace(/[^a-z]/g, "");
  mail(
    {
      id: `t${i}`,
      account: i % 4 === 3 ? "travail" : "perso",
      from: `${who} <news@${domain}.example.org>`,
      subject: isNews ? `La lettre de la semaine n° ${120 + i}` : pubs[i % pubs.length],
      snippet: "",
      min: 60 + i * 47,
    },
    "À jeter",
    isNews ? "Newsletter jamais ouverte" : "Publicité",
    "…",
  );
}
mail(
  { id: "p1", account: "perso", from: "Livraison <suivi@colis-express.example.net>", subject: "Votre colis est bloqué", snippet: "", min: 150 },
  "À jeter",
  "Hameçonnage : faux transporteur, lien piégé",
  "Votre colis est bloqué, payez 1,99 € ici…",
);
mail(
  { id: "p2", account: "travail", from: "Support <securite@banque-en-ligne.example.net>", subject: "Votre compte sera suspendu", snippet: "", min: 520 },
  "À jeter",
  "Hameçonnage : se fait passer pour la banque",
  "Confirmez vos identifiants sous 24 h…",
);
mail(
  { id: "i1", account: "perso", from: "Énergie <factures@energie.example.org>", subject: "Votre facture de novembre", snippet: "", min: 600 },
  "Pour info",
  "62 €, prélevés le 5 décembre",
  "Montant : 62,00 €. Prélèvement le 5 décembre.",
);
mail(
  { id: "i2", account: "perso", from: "Colis <suivi@transport.example.org>", subject: "Votre colis a été livré", snippet: "", min: 900 },
  "Pour info",
  "Livré hier, en boîte aux lettres",
  "Votre colis a été livré.",
);
mail(
  { id: "i3", account: "perso", from: "Club de tennis <club@tennis.example.org>", subject: "Entraînement de samedi", snippet: "", min: 1100 },
  "Pour info",
  "Avancé à 9 h ce samedi",
  "L'entraînement de samedi est avancé à 9 h.",
);
mail(
  { id: "i4", account: "travail", from: "RH <rh@travail.example.org>", subject: "Fermeture du 24 décembre", snippet: "", min: 1500 },
  "Pour info",
  "Bureau fermé le 24 décembre",
  "Le bureau sera fermé le 24 décembre.",
);

// The replies the skill drafted: what the cards send on Oui.
const sam = "Bonjour Sam,\n\nMerci pour le devis. 4 200 € posé me va, et lundi 7 décembre aussi.\nJe te confirme par écrit, peux-tu m'envoyer le bon à signer ?\n\nBonne soirée";
const alex = "Salut Alex,\n\nOui, je le relis demain matin et je te fais mes retours avant midi.\n\nÀ mardi";

const mailCards: SampleView["cards"] = [
  {
    ref: "calls:41",
    card: 41,
    item: "m1",
    kind: "calls",
    state: "pending",
    tainted: true,
    node: "mails",
    text: "Envoyer la réponse à Sam · compte perso",
    // What a calls card would read once it shows what it sends (NOTES: a
    // long argument out of Détails, named by the tool's schema).
    fields: [
      { name: "Ta réponse, prête", value: sam },
      { name: "mail/send_message", value: `account : perso\nreply_to : m1\nbody : ${sam.slice(0, 60)}…`, detail: true },
    ],
  },
  {
    ref: "calls:42",
    card: 42,
    item: "m2",
    kind: "calls",
    state: "pending",
    tainted: true,
    node: "mails",
    text: "Créer l'événement « Dentiste » jeudi 26 novembre, 10 h 30 – 11 h 15",
    fields: [
      { name: "Agenda", value: "Perso" },
      { name: "Lieu", value: "Cabinet dentaire" },
      { name: "calendar/create_event", value: "title : Dentiste\nstart : 2026-11-26T10:30:00+01:00\nend : 2026-11-26T11:15:00+01:00", detail: true },
    ],
  },
  {
    ref: "calls:43",
    card: 43,
    item: "m3",
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
  // already decided: the menu's « Déjà décidé », never the page's foot
  { ref: "runcalls:30", card: 30, kind: "runcalls", state: "approved", exec: "done", node: "mails", text: "Archiver 12 mails" },
  { ref: "calls:31", card: 31, kind: "calls", state: "approved", exec: "done", node: "mails", text: "Mettre 5 mails à la corbeille" },
  { ref: "calls:33", card: 33, kind: "calls", state: "rejected", node: "mails", text: "Marquer 1 mail comme lu" },
  { ref: "skill:1", card: 1, kind: "skill", state: "approved", node: "mails", text: "Approuver le skill « Trier mes mails »" },
];

// What the view's skill writes on top: what asks the user, in a sentence;
// then what is only to know, in a few lines. Its next run writes it again.
const mailBilan = (v: SampleView) => {
  const open = (id: string) => v.notes[id]?.state !== "done" && ((v.values.mails.messages as Mail[]) ?? []).some((m) => m.id === id);
  const answers = ["m1", "m3"].filter(open).length;
  const rdv = open("m2");
  const phish = ["p1", "p2"].filter(open).length;
  const parts: string[] = [];
  if (answers) parts.push(answers === 2 ? "2 réponses attendues, dont Sam avant vendredi." : open("m1") ? "1 réponse attendue : Sam, avant vendredi." : "1 réponse attendue : Alex.");
  if (rdv) parts.push("1 rendez-vous à confirmer.");
  if (phish) parts.push(`${phish === 2 ? "2 tentatives" : "1 tentative"} d'hameçonnage dans le lot à jeter.`);
  if (!parts.length) parts.push("Rien ne t'attend dans tes mails.");
  return `${parts.join(" ")}\n\n**Pour info** · La facture d'électricité de novembre est arrivée (62 €, prélevés le 5 décembre). Ton colis a été livré hier. Le club avance l'entraînement de samedi à 9 h.\n\n`;
};

const mailView: SampleView = {
  id: "mails",
  parts: ["", mailBlock, decidedBlock],
  values: { mails: { messages: mails } },
  notes,
  bodies,
  cards: mailCards,
  bilan: mailBilan,
  words: {
    "mail/trash": {
      one: "Mettre 1 élément à la corbeille",
      many: "Mettre les {n} à la corbeille",
      all: "Tout mettre à la corbeille",
      done: (n) => (n === 1 ? "1 mis à la corbeille" : `${n} mis à la corbeille`),
      confirm: true,
    },
  },
};
mailView.parts[0] = mailBilan(mailView);

// --- Alertes de la prod: the same pieces, another matter -------------------

const prodBlock: Authored = {
  name: "alertes",
  sources: {
    alertes: {
      tool: "alerts/list_alerts",
      args: { since: "24h" },
      items: { path: "alerts", id: "id", title: "title", detail: "service", open: { tool: "alerts/get_alert", id: "id" } },
    },
  },
  actions: {
    acquitter: { label: "Acquitter", tool: "alerts/ack", args: { project: { "@item": "project" } }, items: "ids", source: "alertes", list: "resolues" },
  },
  components: [
    { id: "root", component: "Column", children: ["todo_h", "todo", "fold"] },
    ...todo("todo", "alertes", "alerts", "À traiter", "Rien ne brûle."),
    { id: "todo_item", component: "Item", title: { "@path": "_note" }, detail: { "@path": "service" }, note: { "@path": "title" } },
    { id: "fold", component: "Fold", list: "resolues", label: "{n} alertes résolues d'elles-mêmes", child: "resolues" },
    { id: "resolues", component: "List", children: { path: "/alertes/alerts[_group=Résolues]", componentId: "res_row" } },
    { id: "res_row", component: "Item", level: "low", title: { "@path": "title" }, detail: { "@path": "service" }, note: { "@path": "_note" } },
  ],
};

type Alert = { id: string; project: string; service: string; title: string };
const alerts: Alert[] = [
  { id: "a1", project: "api", service: "db-1", title: "Disque à 91 %" },
  { id: "a2", project: "web", service: "front", title: "Temps de réponse p95 > 800 ms" },
];
const prodNotes: SampleView["notes"] = {
  a1: { group: "À traiter", note: "Agrandir le volume de db-1 de 20 Go — prêt", state: "act" },
  a2: { group: "À traiter", note: "À regarder : lent depuis le déploiement de 14 h", state: "look" },
};
const flaps = ["Redémarrage du worker", "CPU > 85 % pendant 3 min", "Certificat renouvelé", "Latence DNS", "File d'attente > 1000", "Mémoire > 90 %", "Sonde HTTP en échec"];
for (let i = 0; i < 14; i++) {
  const id = `r${i}`;
  alerts.push({ id, project: i % 3 ? "api" : "web", service: i % 3 ? `worker-${(i % 4) + 1}` : "front", title: flaps[i % flaps.length] });
  prodNotes[id] = { group: "Résolues", note: `Revenu à la normale en ${2 + (i % 5)} min`, state: "none" };
}
const prodBodies: SampleView["bodies"] = Object.fromEntries(alerts.map((a) => [a.id, { title: a.title, from: a.service, date: ago(30), body: `Projet ${a.project}, service ${a.service}.`, raw: JSON.stringify(a, null, 2) }]));

const prodView: SampleView = {
  id: "prod",
  parts: ["", prodBlock],
  values: { alertes: { alerts } },
  notes: prodNotes,
  bodies: prodBodies,
  cards: [
    {
      ref: "calls:51",
      card: 51,
      item: "a1",
      kind: "calls",
      state: "pending",
      node: "prod",
      text: "Agrandir le volume « db-1-data » de 20 Go (100 → 120 Go)",
      fields: [{ name: "infra/resize_volume", value: '{"volume": "db-1-data", "size_gb": 120}', detail: true }],
    },
  ],
  bilan: (v) => {
    const open = v.notes.a1?.state !== "done";
    const left = ((v.values.alertes.alerts as Alert[]) ?? []).filter((a) => a.id.startsWith("r")).length;
    return `${open ? "1 alerte demande ton oui : le disque de db-1 est à 91 %, il sera plein vers dimanche. " : "Le disque de db-1 est agrandi. "}Le front est lent depuis le déploiement de 14 h, à regarder.${left ? ` ${left} alertes se sont résolues seules.` : ""}\n\n`;
  },
  words: {
    "alerts/ack": {
      one: "Acquitter 1 élément",
      many: "Acquitter les {n}",
      all: "Tout acquitter",
      done: (n) => (n === 1 ? "1 acquittée" : `${n} acquittées`),
    },
  },
};
prodView.parts[0] = prodView.bilan!(prodView);

export const sampleViews: SampleView[] = [mailView, prodView];
