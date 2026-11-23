import { useEffect, useState } from "react";
import type { GraphNode } from "./data/api";
import { applyNodes, closePage, openPage, step, store, talkTo, useGraph } from "./data/store";
import { ForcesPanel } from "./graph/Forces";
import { CENTRING, FORCES, forcesFrom, setCentring, setForces, type Forces } from "./graph/layout";
import "./dev.css";

// The dev server's ?physique page: the graph's four forces to tune by hand,
// like Obsidian's « Forces » panel, on the owner's graph (three subjects,
// each linked both ways to the view it follows, one alone) or on some
// twenty-five nodes in a few clusters. Only on the dev server (dev.tsx),
// never in the built app.

const at = (min: number) => new Date(Date.now() - min * 60_000).toISOString();
const row = (id: string, title: string, more: Partial<GraphNode> = {}): GraphNode => ({
  id, title, lifetime: "work", status: "idle", created: at(600), updated: at(30), version: 1, ...more,
});

// The owner's graph. The views take the sample's ids: their pages are the
// sample's views (data/sampleviews.ts).
function ownerGraph(): GraphNode[] {
  return [
    row("sj", "Suivre mes journées", { links: ["day"], created: at(700) }),
    row("day", "Ma journée", { links: ["sj"], lifetime: "standing", created: at(699) }),
    row("sm", "Suivre mes mails", { links: ["mails"], created: at(698) }),
    row("mails", "Mes mails", { links: ["sm"], lifetime: "standing", external: true, created: at(697) }),
    row("si", "Suivre mes investissements", { links: ["invest"], created: at(696) }),
    row("invest", "Mon portefeuille", { links: ["si"], lifetime: "standing", created: at(695) }),
    row("tools", "Outils à revoir", { created: at(694) }),
  ];
}

// Some twenty-five nodes: four subjects with their bubbles, a few text
// links across, three alone.
function scaleGraph(): GraphNode[] {
  const out: GraphNode[] = [];
  const clusters: [string, string, string[]][] = [
    ["voy", "Voyage d'été", ["Billets de train", "Logement", "Itinéraire", "Valise"]],
    ["mai", "Maison", ["Devis toiture", "Assurance habitation", "Jardin"]],
    ["sub", "Substack", ["Plan du 1er article", "Style des articles", "Relecture", "Calendrier de publication"]],
    ["pro", "Alertes de la prod", ["Disque plein", "Certificat à renouveler", "Sauvegardes"]],
  ];
  let m = 900;
  for (const [id, title, kids] of clusters) {
    out.push(row(id, title, { created: at(m--) }));
    kids.forEach((k, i) => out.push(row(`${id}${i}`, k, { parent: id, created: at(m--), status: i === 1 ? "running" : "idle" })));
  }
  out.push(row("sub0a", "Idées de titres", { parent: "sub0", created: at(m--) }));
  out.push(row("day", "Ma journée", { lifetime: "standing", links: ["voy0", "mai0"], created: at(m--) }));
  out.push(row("mails", "Mes mails", { lifetime: "standing", links: ["sub"], status: "waits", created: at(m--) }));
  out.push(row("invest", "Mon portefeuille", { lifetime: "standing", created: at(m--) }));
  out.push(row("fac", "Factures du mois", { links: ["mai1"], created: at(m--) }));
  out.push(row("tools", "Outils à revoir", { created: at(m--) }));
  out.push(row("lec", "Lectures", { created: at(m--) }));
  return out;
}

export const physicsGraph = ownerGraph;

const KEY = "eriphe.physique";

// The forces kept on this browser (the product will keep them with the
// user's settings), else the defaults.
export function keptForces(): void {
  let kept: unknown = null;
  try {
    kept = JSON.parse(localStorage.getItem(KEY) ?? "null");
  } catch {
    /* nothing kept */
  }
  setForces(forcesFrom(kept));
}

export function PhysicsPanel() {
  const nodes = useGraph((s) => s.nodes);
  const open = useGraph((s) => s.open);
  const talk = useGraph((s) => s.talk);
  const [centred, setCentred] = useState(CENTRING.focus);
  const [f, setF] = useState<Forces>({ ...FORCES });
  const [copied, setCopied] = useState(false);
  const [linkTo, setLinkTo] = useState("");
  const [from, setFrom] = useState("");
  const [to, setTo] = useState("");
  const [shut, setShut] = useState(false);
  const list = Object.values(nodes).sort((a, b) => a.created.localeCompare(b.created) || a.id.localeCompare(b.id));
  const first = list[0]?.id ?? "";

  useEffect(() => {
    localStorage.setItem(KEY, JSON.stringify(f));
  }, [f]);

  const tune = (next: Forces) => {
    setForces(next);
    setF({ ...FORCES });
  };
  const copy = () => {
    void navigator.clipboard?.writeText(JSON.stringify(FORCES, null, 2));
    setCopied(true);
    setTimeout(() => setCopied(false), 1200);
  };
  const load = (g: GraphNode[]) => step(store, applyNodes, { reset: true, nodes: g });
  const toggle = (id: string) => step(store, open.includes(id) ? closePage : openPage, id, true);
  const allOpen = list.length > 0 && list.every((x) => open.includes(x.id));
  const toggleAll = () => {
    if (allOpen) for (const x of list) step(store, closePage, x.id);
    else for (const x of list) step(store, openPage, x.id, true);
  };
  const add = () => {
    let n = list.length + 1;
    while (nodes[`n${n}`]) n++;
    step(store, applyNodes, { nodes: [row(`n${n}`, `Nouveau ${n}`, { links: linkTo ? [linkTo] : [], created: new Date().toISOString() })] });
  };
  const link = () => {
    const a = nodes[from || first], b = to || first;
    if (!a || !nodes[b] || a.id === b || (a.links ?? []).includes(b)) return;
    step(store, applyNodes, { nodes: [{ ...a, links: [...(a.links ?? []), b] }] });
  };
  const name = (x: GraphNode) => x.title ?? x.id;
  const centre = (on: boolean) => {
    setCentring(on);
    setCentred(on);
  };
  // the focus goes to the next open page, in the order they opened
  const nextFocus = () => {
    if (open.length < 2) return;
    step(store, talkTo, open[(open.indexOf(talk ?? "") + 1) % open.length]);
  };
  // something happens in « Ma journée »: it waits for the user (its halo), or no more
  const happen = () => {
    const d = nodes.day;
    if (d) step(store, applyNodes, { nodes: [{ ...d, status: d.status === "waits" ? "idle" : "waits", updated: new Date().toISOString() }] });
  };

  return (
    <aside className={`dev-phys${shut ? " shut" : ""}`} aria-label="Forces">
      <header>
        <b>?physique</b>
        <button type="button" onClick={() => setShut(!shut)} aria-expanded={!shut}>
          {shut ? "▸" : "▾"}
        </button>
      </header>
      {!shut && (
        <>
          <ForcesPanel value={f} onChange={tune} centred={centred} onCentred={centre} />
          <div className="dev-phys-row">
            <button type="button" onClick={copy}>
              {copied ? "Copié" : "Copier les valeurs"}
            </button>
          </div>

          <b className="dev-phys-sec">Graphe</b>
          <div className="dev-phys-row">
            <button type="button" onClick={() => load(ownerGraph())}>
              Mon graphe
            </button>
            <button type="button" onClick={() => load(scaleGraph())}>
              25 nœuds
            </button>
          </div>

          <b className="dev-phys-sec">Pages</b>
          <div className="dev-phys-pages">
            {list.map((x) => (
              <label key={x.id}>
                <input type="checkbox" checked={open.includes(x.id)} onChange={() => toggle(x.id)} />
                {name(x)}
              </label>
            ))}
          </div>
          <div className="dev-phys-row">
            <button type="button" onClick={toggleAll}>
              {allOpen ? "Tout fermer" : "Tout ouvrir"}
            </button>
            <button type="button" onClick={nextFocus} disabled={open.length < 2}>
              Focus suivant
            </button>
          </div>
          <p className="dev-phys-note">Focus : {talk && nodes[talk] ? name(nodes[talk]) : "aucun"}</p>
          <div className="dev-phys-row">
            <button type="button" onClick={happen} disabled={!nodes.day}>
              {nodes.day?.status === "waits" ? "Ma journée : plus rien" : "Déclencher un changement dans Ma journée"}
            </button>
          </div>

          <b className="dev-phys-sec">Ajouter un nœud</b>
          <div className="dev-phys-row">
            <select value={linkTo} onChange={(e) => setLinkTo(e.target.value)} aria-label="Lié à">
              <option value="">lié à rien</option>
              {list.map((x) => (
                <option key={x.id} value={x.id}>
                  lié à {name(x)}
                </option>
              ))}
            </select>
            <button type="button" onClick={add}>
              Ajouter
            </button>
          </div>

          <b className="dev-phys-sec">Ajouter un lien</b>
          <div className="dev-phys-row">
            <select value={from || first} onChange={(e) => setFrom(e.target.value)} aria-label="De">
              {list.map((x) => (
                <option key={x.id} value={x.id}>
                  {name(x)}
                </option>
              ))}
            </select>
            <select value={to || first} onChange={(e) => setTo(e.target.value)} aria-label="Vers">
              {list.map((x) => (
                <option key={x.id} value={x.id}>
                  {name(x)}
                </option>
              ))}
            </select>
            <button type="button" onClick={link}>
              Lier
            </button>
          </div>
        </>
      )}
    </aside>
  );
}
