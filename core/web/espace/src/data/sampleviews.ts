import type { SampleItem, SampleView } from "./samplecore";

// The dev server's views, written as Eriphe writes them: Markdown pages
// (docs/node-format.md), the items they reference read by a make-believe
// server. « Mes mails », « Alertes de la prod », « Ma journée » and
// « Investissements » show the very same pieces serve other matters.
// Made-up data: Sam, example.org.

const ago = (min: number) => new Date(Date.now() - min * 60_000).toISOString();

// --- Mes mails ------------------------------------------------------------

type Mail = { label: string; id: string; from: string; subject: string; body: string; min: number };
const mails: Mail[] = [
  { label: "m1", id: "aWQtMQ", from: "Sam <sam@example.org>", subject: "Devis terrasse", body: "Voici le devis pour la terrasse : 4 200 € posé, début possible lundi 7 décembre. Tu peux me dire avant vendredi si ça te va ?", min: 95 },
  { label: "m2", id: "aWQtMg", from: "Cabinet dentaire <rdv@cabinet.example.org>", subject: "Votre rendez-vous de jeudi", body: "Jeudi 26 novembre à 10 h 30. Merci de confirmer.", min: 240 },
  { label: "m3", id: "aWQtMw", from: "Alex <alex@travail.example.org>", subject: "Relecture du plan", body: "Tu as cinq minutes pour relire le plan avant mardi ?", min: 380 },
  { label: "p1", id: "cC0x", from: "Livraison <suivi@colis-express.example.net>", subject: "Votre colis est bloqué", body: "Payez 1,99 € pour débloquer votre colis.", min: 150 },
  { label: "p2", id: "cC0y", from: "Support <securite@banque-en-ligne.example.net>", subject: "Votre compte sera suspendu", body: "Confirmez vos identifiants sous 24 h.", min: 520 },
  { label: "i1", id: "aS0x", from: "Énergie <factures@energie.example.org>", subject: "Votre facture de novembre", body: "62 €, prélevés le 5 décembre.", min: 600 },
  { label: "i2", id: "aS0y", from: "Club de tennis <club@tennis.example.org>", subject: "Entraînement de samedi", body: "L'entraînement de samedi est avancé à 9 h.", min: 1100 },
];

const mailItems: Record<string, SampleItem> = Object.fromEntries(
  mails.map((m) => [m.label, { id: m.id, title: m.subject, from: m.from, date: ago(m.min), body: m.body, external: true, raw: JSON.stringify({ id: m.id, subject: m.subject, from: m.from }) }]),
);

const sam = "Bonjour Sam,\n\nMerci pour le devis. 4 200 € posé me va, et lundi 7 décembre aussi.\nPeux-tu m'envoyer le bon à signer ?\n\nBonne soirée";

// row writes a mail's line, while it is not done.
const row = (v: SampleView, label: string, note: string) => {
  const m = mails.find((x) => x.label === label)!;
  return v.done.has(m.id) ? "" : `- [${m.subject}][${label}] · ${m.from.split(" <")[0]} : ${note}\n`;
};

const mailsView: SampleView = {
  id: "mails",
  items: mailItems,
  done: new Set(),
  body: (v) => {
    const todo = row(v, "m1", "il attend ta réponse avant vendredi") + row(v, "m2", "un rendez-vous à confirmer") + row(v, "m3", "une relecture avant mardi");
    const junk = row(v, "p1", "hameçonnage") + row(v, "p2", "hameçonnage");
    return [
      "Écrit aujourd'hui à 9 h.\n\nSam attend ta réponse avant vendredi.\n\n",
      `## À traiter\n${todo || "Rien à traiter.\n"}\n`,
      junk ? `## À jeter\n[Jeter ces mails](do:mail/trash)\n${junk}\n` : "",
      `## Pour info\n${row(v, "i1", "62 €, prélevés le 5 décembre")}${row(v, "i2", "avancé à 9 h")}\n`,
      "[Montre la semaine](ask:) · [Prépare aussi la réponse à Alex](ask:)\n\n",
      mails.map((m) => `[${m.label}]: mail:${m.id}`).join("\n") + "\n",
    ].join("");
  },
  cards: [
    {
      ref: "calls:41",
      card: 41,
      items: ["aWQtMQ"],
      kind: "calls",
      state: "pending",
      tainted: true,
      node: "mails",
      text: "Envoyer la réponse à Sam · compte perso",
      fields: [
        { name: "Ta réponse, prête", value: sam },
        { name: "mail/send_message", value: `account : perso\nreply_to : aWQtMQ\nbody : ${sam.slice(0, 60)}…`, detail: true },
      ],
    },
    {
      ref: "calls:42",
      card: 42,
      items: ["aWQtMg"],
      kind: "calls",
      state: "pending",
      tainted: true,
      node: "mails",
      text: "Créer l'événement « Dentiste » jeudi 26 novembre, 10 h 30 – 11 h 15",
      fields: [
        { name: "Agenda", value: "Perso" },
        { name: "calendar/create_event", value: "title : Dentiste\nstart : 2026-11-26T10:30:00+01:00\nend : 2026-11-26T11:15:00+01:00", detail: true },
      ],
    },
    // already decided: the menu's « Déjà décidé », never the page's foot
    { ref: "calls:30", card: 30, kind: "calls", state: "approved", exec: "done", node: "mails", text: "Archiver 12 mails" },
    { ref: "calls:33", card: 33, kind: "calls", state: "rejected", node: "mails", text: "Marquer 1 mail comme lu" },
  ],
};

// --- Alertes de la prod ----------------------------------------------------

const prodView: SampleView = {
  id: "prod",
  items: {
    a1: { id: "alert-1", title: "Erreurs 5xx sur l'API", from: "monitoring", date: ago(12), body: "42 erreurs par minute depuis 10 h 02.", raw: '{"id":"alert-1","severity":"critical"}' },
    a2: { id: "alert-2", title: "Disque à 91 %", from: "monitoring", date: ago(80), body: "Le volume des journaux est presque plein.", raw: '{"id":"alert-2","severity":"warning"}' },
  },
  done: new Set(),
  body: () =>
    [
      "Ce qui casse d'abord, ce qui est résolu en lot.\n\n",
      "> [!figures]\n> - Erreurs 5xx par minute : [42](on:monitor.changed/prod-erreurs)\n> - Alertes ouvertes : 2\n\n",
      "## À regarder\n- [Erreurs 5xx sur l'API][a1] : depuis 10 h 02\n- [Disque à 91 %][a2] : les journaux\n\n",
      "[Préviens-moi si ça dépasse 100](ask:)\n\n",
      "[a1]: alerts:alert-1\n[a2]: alerts:alert-2\n",
    ].join(""),
  cards: [],
};

// --- Ma journée ------------------------------------------------------------

const today = new Date().toISOString().slice(0, 10);
const dayView: SampleView = {
  id: "day",
  items: {},
  done: new Set(),
  body: () =>
    [
      "Ta journée, tenue à jour.\n\n",
      `> [!agenda]\n> - ${today} 11:00 Point produit\n> - ${today} 14:30 Dentiste\n\n`,
      "## À faire\n- [ ] Rappeler Sam pour le devis\n- [x] Relire le plan\n\n",
      "[Ajoute aussi mes mails à traiter](ask:) · [Fais-moi un point chaque matin à 8 h](ask:)\n",
    ].join(""),
  cards: [
    { ref: "intention:3", card: 8, state: "pending", node: "day", text: "Une heure pour relire le devis", when: [{ start: `${today}T16:00:00+01:00`, end: `${today}T17:00:00+01:00` }] },
  ],
};

// --- Investissements ---------------------------------------------------------

const investView: SampleView = {
  id: "invest",
  items: {},
  done: new Set(),
  body: () =>
    [
      "Le PEA et l'assurance-vie : la valeur, ce qui bouge, l'écart à la cible.\n\n",
      "> [!figures|glance 3%]\n> - PEA : [18 385 €](on:monitor.changed/pea)\n> - Assurance-vie : 9 200 €\n\n",
      "> [!chart|line] Valeur du PEA\n> | Mois | PEA (€) |\n> |---|---|\n> | sept. | 17 600 |\n> | oct. | 18 020 |\n> | nov. | 18 385 |\n\n",
      "Rien n'est acheté ni vendu sans ton oui.\n",
    ].join(""),
  cards: [],
};

export const sampleViews: SampleView[] = [mailsView, prodView, dayView, investView];
