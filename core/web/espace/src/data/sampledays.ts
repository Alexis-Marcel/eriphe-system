import type { Authored, SampleView } from "./samplecore";

// Prototype (#730): « Ma journée » and « Investissements », written as
// Eriphe would write them with the new blocks, on made-up data (Sam, Léa,
// example.org) dated from today, so the agenda's line is where the clock
// is. No real name, address or tool in it.

const p2 = (n: number) => String(n).padStart(2, "0");
function dayOf(offset: number): string {
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

const agendaBlock: Authored = {
  name: "agenda",
  sources: {
    agenda: { tool: "calendar/list_events", args: { days: 7 }, items: { path: "events", id: "id", title: "title" } },
  },
  components: [{ id: "root", component: "Agenda", days: 7, events: { "@path": "/agenda/events" } }],
};

const dayView: SampleView = {
  id: "day",
  parts: [
    "Une matinée pleine jusqu'à 11 h 30, puis Léa t'invite au pique-nique de 12 h 30 : il chevauche le déjeuner avec Alex, encore incertain. L'appel de 17 h 30 est annulé, j'y ai mis une heure pour relire le devis de Sam.\n\n",
    agendaBlock,
  ],
  values: { agenda: { events } },
  notes: {},
  bodies: {},
  cards: [
    {
      ref: "calls:61",
      card: 61,
      items: ["e4"],
      kind: "calls",
      state: "pending",
      node: "day",
      tainted: true,
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
      tainted: true,
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
  ],
};

export const sampleDays: SampleView[] = [dayView];
