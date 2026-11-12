import type { Lang } from "./i18n";
import type { Composition, Obj } from "./types";

// Made-up data, in the shape eriphe-core gives (objects.go), shown when it
// serves neither a composition (#386) nor an attention queue (the dev
// server, with no eriphe-core behind): the page says it is an example.
// Neutral names and addresses only (example.org, "Sam").

const iso = (d: Date) => {
  const p = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}`;
};

export function fixtures(lang: Lang, now = new Date()): { comp: Composition; objects: Obj[] } {
  const L = (fr: string, en: string) => (lang === "en" ? en : fr);
  const today = iso(now);
  const later = new Date(now);
  later.setDate(later.getDate() + 2);
  const day = iso(later);
  const at = (d: string, h: string) => `${d}T${h}:00+01:00`;
  const event = (id: string, d: string, start: string, end: string, title: string, location?: string) => ({
    id, account: "agenda", calendar: L("Perso", "Personal"), title, start: at(d, start), end: at(d, end), location,
  });
  const visit = event("e2", day, "15:00", "16:00", L("Visite de l'atelier", "Workshop visit"), L("Atelier", "Workshop"));
  const call = event("e3", day, "15:30", "16:30", L("Appel avec l'équipe", "Call with the team"));
  const mail = (id: string, from: string, subject: string, body: string, h: string, related?: string[]): Obj => ({
    ref: `mail:${id}`, kind: "mail", title: subject, related,
    mail: { id, account: "mail", folder: "INBOX", from, subject, date: at(today, h), unread: true, body },
  });

  const objects: Obj[] = [
    mail("m1", "Sam <sam@example.org>", L("Visite de l'atelier", "Visit of the workshop"),
      L(
        "Bonjour, je passe dans le quartier après-demain : est-ce que 15 h te va pour la visite de l'atelier ? Dis-moi si un autre moment est mieux.",
        "Hello, I am around the day after tomorrow: does 3 pm suit you for the visit of the workshop? Tell me if another time is better.",
      ), "08:12", [`agenda:${day}`]),
    {
      ref: "card:101", kind: "card", title: L("Ajouter la visite de Sam à l'agenda", "Add Sam's visit to the agenda"),
      related: ["mail:m1", `agenda:${day}`],
      card: {
        id: 101, kind: "intention", state: "pending", created: at(today, "08:14"), proposal: "intention:7",
        text: L("Ajouter la visite de Sam à l'agenda", "Add Sam's visit to the agenda"),
        why: L("Sam propose 15 h après-demain.", "Sam suggests 3 pm the day after tomorrow."),
        fields: [{ name: L("Quand", "When"), value: L("après-demain, 15 h – 16 h", "the day after tomorrow, 3–4 pm") }],
      },
    },
    { ref: `agenda:${day}`, kind: "agenda", title: day, agenda: { date: day, events: [event("e1", day, "10:00", "11:00", L("Revue de projet", "Project review")), visit, call] } },
    {
      ref: "session:4", kind: "session", title: L("Refaire la page d'accueil", "Redo the home page"), related: ["task:t-41"],
      session: {
        id: 4, title: L("Refaire la page d'accueil", "Redo the home page"), goal: L("Une page d'accueil plus claire", "A clearer home page"),
        state: "running", created: at(today, "07:30"),
        summary: L("Deux versions sont prêtes : l'une garde la structure, l'autre la simplifie.", "Two versions are ready: one keeps the structure, the other simplifies it."),
        steps: [
          { key: "read", title: L("Lire la page actuelle", "Read the current page"), kind: "task", status: "accepted", task: "t-40" },
          { key: "draft", title: L("Proposer deux versions", "Propose two versions"), kind: "task", status: "accepted", task: "t-41" },
          { key: "pick", title: L("Choisir la version", "Pick the version"), kind: "choice", status: "running" },
          { key: "publish", title: L("Publier", "Publish"), kind: "task", status: "waiting" },
        ],
      },
    },
    {
      ref: "card:102", kind: "card", title: L("Quelle version de la page d'accueil publier ?", "Which version of the home page to publish?"),
      related: ["session:4"],
      card: {
        id: 102, kind: "session", state: "pending", created: at(today, "07:58"), proposal: "session:4",
        text: L("Quelle version de la page d'accueil publier ? Oui pour la version simplifiée.", "Which version of the home page to publish? Yes for the simpler one."),
        why: L("La version simplifiée charge deux fois plus vite.", "The simpler version loads twice as fast."),
      },
    },
    {
      ref: "task:t-41", kind: "task", title: L("Proposer deux versions de la page", "Propose two versions of the page"), related: ["session:4"],
      task: { id: "t-41", title: L("Proposer deux versions de la page", "Propose two versions of the page"), agent: "sonnet", state: "idle", node: "pc", created: at(today, "07:41"), updated: at(today, "07:58") },
    },
    {
      ref: "project:atelier", kind: "project", title: "Atelier",
      project: { name: "Atelier", state: L("en cours", "in progress"), text: L("Préparer la visite, puis l'inventaire.", "Prepare the visit, then the inventory."), updated: at(today, "07:00") },
    },
    { ref: "event:e9", kind: "event", title: L("Dentiste", "Dentist"), related: [`agenda:${today}`], event: event("e9", today, "17:30", "18:00", L("Dentiste", "Dentist")) },
    mail("m2", "News <news@example.net>", L("La lettre de la semaine", "This week's letter"), L("Les nouveautés de la semaine.", "This week's news."), "06:00"),
    mail("m3", "Billing <billing@example.com>", L("Votre facture est disponible", "Your invoice is available"), L("La facture du mois est en pièce jointe.", "This month's invoice is attached."), "06:30"),
    { ref: "group:1", kind: "group", title: L("Le courrier du matin", "This morning's mail"), group: { refs: ["mail:m2", "mail:m3"] } },
    {
      ref: "card:103", kind: "card", title: L("Retenir : la sauvegarde se fait la nuit", "Remember: backups run at night"),
      card: { id: 103, kind: "memory", state: "pending", created: at(today, "07:20"), proposal: "memory:12", text: L("Retenir : la sauvegarde se fait la nuit", "Remember: backups run at night") },
    },
  ];

  const comp: Composition = {
    mode: "side",
    ambient: "decision",
    eriphe: L(
      "Bonjour. Sam propose une visite après-demain à 15 h, qui chevauche ton appel. La page d'accueil attend ton choix.",
      "Hello. Sam suggests a visit the day after tomorrow at 3 pm, which overlaps your call. The home page waits for your choice.",
    ),
    items: [
      { ref: "card:101", weight: 5, why: L("Sam attend une réponse", "Sam waits for an answer"), say: L("Sam propose 15 h, mais ton appel commence à 15 h 30 : je peux proposer 14 h à la place.", "Sam suggests 3 pm, but your call starts at 3:30: I can suggest 2 pm instead.") },
      { ref: "card:102", weight: 4, why: L("La session est en pause", "The session is paused") },
      { ref: "mail:m1", weight: 3, why: L("Une question de Sam", "A question from Sam") },
      { ref: "session:4", weight: 3, why: L("En cours depuis ce matin", "Running since this morning") },
      { ref: "project:atelier", weight: 2, why: L("La visite approche", "The visit is near") },
      { ref: "event:e9", weight: 2, why: L("Ce soir", "Tonight") },
      { ref: "group:1", weight: 1, why: L("Rien d'urgent", "Nothing urgent") },
      { ref: "mail:invented", weight: 4, why: "A ref no object has: dropped by code" },
    ],
    chips: ["card:103"],
  };
  return { comp, objects };
}
