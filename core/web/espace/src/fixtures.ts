import type { Lang } from "./i18n";
import type { Composition, Obj } from "./types";

// Made-up data, shown until eriphe-core serves a composition (#386) and the
// objects (#384): the page says it is an example. Neutral names and
// addresses only (example.org, "Sam").

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
  const at = (h: string) => `${today}T${h}`;

  const objects: Obj[] = [
    {
      ref: "mail:m1", type: "mail", from: "Sam <sam@example.org>", at: at("08:12"), source: "mail",
      subject: L("Visite de l'atelier", "Visit of the workshop"),
      excerpt: L(
        "Bonjour, je passe dans le quartier après-demain : est-ce que 15 h te va pour la visite de l'atelier ? Dis-moi si un autre moment est mieux.",
        "Hello, I am around the day after tomorrow: does 3 pm suit you for the visit of the workshop? Tell me if another time is better.",
      ),
    },
    {
      ref: "card:101", type: "card", tool: "calendar.create_event", about: "mail:m1", since: at("08:14"), state: "pending",
      text: L("Ajouter la visite de Sam à l'agenda", "Add Sam's visit to the agenda"),
      input: { start: `${day}T15:00`, end: `${day}T16:00`, title: L("Visite de l'atelier", "Workshop visit"), location: "Atelier" },
    },
    {
      ref: `agenda:${day}`, type: "agenda", date: day,
      events: [
        { start: "10:00", end: "11:00", title: L("Revue de projet", "Project review") },
        { start: "15:30", end: "16:30", title: L("Appel avec l'équipe", "Call with the team") },
      ],
    },
    {
      ref: "session:s1", type: "session", project: "site", title: L("Refaire la page d'accueil", "Redo the home page"),
      goal: L("Une page d'accueil plus claire", "A clearer home page"),
      steps: [
        { title: L("Lire la page actuelle", "Read the current page"), status: "done", at: "07:40" },
        { title: L("Proposer deux versions", "Propose two versions"), status: "done", at: "07:58" },
        { title: L("Choisir la version", "Pick the version"), status: "waiting", note: L("attend ton choix", "waits for your choice") },
        { title: L("Publier", "Publish"), status: "waiting" },
      ],
      finding: L("Deux versions sont prêtes : l'une garde la structure, l'autre la simplifie.", "Two versions are ready: one keeps the structure, the other simplifies it."),
    },
    {
      ref: "choice:c1", type: "choice", session: "session:s1",
      question: L("Quelle version de la page d'accueil publier ?", "Which version of the home page to publish?"),
      options: [L("Garder la structure", "Keep the structure"), L("La version simplifiée", "The simpler version")],
      advice: L("La version simplifiée charge deux fois plus vite.", "The simpler version loads twice as fast."),
    },
    {
      ref: "alert:a1", type: "alert", monitor: "backup", severity: L("avertissement", "warning"), at: at("03:10"), count: 2,
      title: L("La sauvegarde de la nuit a échoué", "Last night's backup failed"),
      note: L("Le disque de destination est plein à 97 %.", "The target disk is 97% full."),
    },
    {
      ref: "project:atelier", type: "project", name: "Atelier", due: day, late: false, state: L("en cours", "in progress"),
      next: L("Préparer la visite", "Prepare the visit"),
    },
    {
      ref: "event:e1", type: "event", title: L("Dentiste", "Dentist"), start: `${today}T17:30`, end: `${today}T18:00`, source: "agenda",
    },
    {
      ref: "mail:m2", type: "mail", from: "News <news@example.net>", at: at("06:00"), source: "mail",
      subject: L("La lettre de la semaine", "This week's letter"),
      excerpt: L("Les nouveautés de la semaine.", "This week's news."),
    },
    {
      ref: "mail:m3", type: "mail", from: "Facturation <billing@example.com>", at: at("06:30"), source: "mail",
      subject: L("Votre facture est disponible", "Your invoice is available"),
      excerpt: L("La facture du mois est en pièce jointe.", "This month's invoice is attached."),
    },
    { ref: "group:g1", type: "group", title: L("Le courrier du matin", "This morning's mail"), refs: ["mail:m2", "mail:m3"] },
    {
      ref: "card:102", type: "card", tool: "github.merge_pull_request", since: at("07:20"), state: "pending", input: {},
      text: L("Fusionner la correction du formulaire", "Merge the form fix"),
    },
  ];

  const comp: Composition = {
    mode: "side",
    ambient: "decision",
    eriphe: L(
      "Bonjour. Sam propose une visite après-demain à 15 h, qui chevauche ton appel. La page d'accueil attend ton choix, et la sauvegarde de la nuit a échoué.",
      "Hello. Sam suggests a visit the day after tomorrow at 3 pm, which overlaps your call. The home page waits for your choice, and last night's backup failed.",
    ),
    items: [
      { ref: "card:101", weight: 5, why: L("Sam attend une réponse", "Sam waits for an answer"), say: L("Sam propose 15 h, mais ton appel commence à 15 h 30 : je peux proposer 14 h à la place.", "Sam suggests 3 pm, but your call starts at 3:30: I can suggest 2 pm instead.") },
      { ref: "choice:c1", weight: 4, why: L("La session est en pause", "The session is paused") },
      { ref: "mail:m1", weight: 3, why: L("Une question de Sam", "A question from Sam") },
      { ref: "alert:a1", weight: 3, why: L("Deux échecs de suite", "Two failures in a row") },
      { ref: "session:s1", weight: 2, why: L("En cours depuis ce matin", "Running since this morning") },
      { ref: "project:atelier", weight: 2, why: L("Échéance après-demain", "Due the day after tomorrow") },
      { ref: "event:e1", weight: 2, why: L("Ce soir", "Tonight") },
      { ref: "group:g1", weight: 1, why: L("Rien d'urgent", "Nothing urgent") },
      { ref: "mail:invented", weight: 4, why: "A ref the composer made up: dropped by code" },
    ],
    chips: ["card:102"],
  };
  return { comp, objects };
}
