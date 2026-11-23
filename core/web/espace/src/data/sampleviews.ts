import type { Authored, SampleView } from "./samplecore";
import { mailView, todo } from "./samplemails";

// The dev server's views, written as Eriphe writes them (eriphe:block
// JSON, the catalog's generic pieces only: no component, rule or word of a
// service): « Mes mails », and « Alertes de la prod » to show the very
// same pieces serve another matter. Made-up data: Sam, example.org.

const ago = (min: number) => new Date(Date.now() - min * 60_000).toISOString();

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
      items: ["a1"],
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
};
prodView.parts[0] = prodView.bilan!(prodView);

export const sampleViews: SampleView[] = [mailView, prodView];
