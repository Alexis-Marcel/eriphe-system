import { useEffect, useState } from "react";
import { setTheme, step, store, useGraph } from "../data/store";
import { CAP, fmt, play, SCENARIOS, start, useGlance, weightOf, type Style } from "./glance";
import "./glance.css";

// Panel is the node-glance prototype's own panel (proto/node-glance, dev
// server only): the scenarios, the three tile styles, the theme, and
// which glances are past their threshold right now. Its words stay here:
// it is not part of the workspace.

const STYLES: { id: Style; label: string }[] = [
  { id: "A", label: "A chiffre" },
  { id: "B", label: "B courbe" },
  { id: "C", label: "C couleur" },
];

export function Panel() {
  useEffect(() => start(), []);
  const [folded, setFolded] = useState(false);
  const { style, scenario, tiles, over, live, seen } = useGlance();
  const nodes = useGraph((s) => s.nodes);
  const theme = useGraph((s) => s.theme);
  const dark = theme ? theme === "dark" : matchMedia("(prefers-color-scheme: dark)").matches;
  const hint = SCENARIOS.find((s) => s.id === scenario)?.hint;
  return (
    <aside className={`gl-panel${folded ? " folded" : ""}`} aria-label="Prototype : aperçu des nœuds">
      <header>
        <span className="micro">Prototype · aperçu</span>
        <button type="button" className="gl-fold" onClick={() => setFolded(!folded)} aria-expanded={!folded}>
          {folded ? "▸" : "▾"}
        </button>
      </header>
      {!folded && (
        <>
          <p className="gl-k">Scénario</p>
          <div className="gl-list">
            {SCENARIOS.map((s) => (
              <button key={s.id} type="button" data-scenario={s.id} className={s.id === scenario ? "on" : ""} onClick={() => play(s.id)}>
                {s.label}
              </button>
            ))}
          </div>
          {hint && <p className="gl-hint">{hint}</p>}
          <p className="gl-k">Style de tuile</p>
          <div className="gl-seg">
            {STYLES.map((s) => (
              <button key={s.id} type="button" data-style={s.id} className={s.id === style ? "on" : ""} onClick={() => useGlance.setState({ style: s.id })}>
                {s.label}
              </button>
            ))}
          </div>
          <p className="gl-k">Thème</p>
          <div className="gl-seg">
            <button type="button" data-theme-set="light" className={!dark ? "on" : ""} onClick={() => step(store, setTheme, "light")}>
              Clair
            </button>
            <button type="button" data-theme-set="dark" className={dark ? "on" : ""} onClick={() => step(store, setTheme, "dark")}>
              Sombre
            </button>
          </div>
          <p className="gl-k">
            Au-dessus du seuil · {tiles.length}/{CAP} tuiles
          </p>
          <ul className="gl-over">
            {over.length === 0 && Object.keys(seen).length === 0 && <li className="gl-none">aucune</li>}
            {Object.keys(seen).map((id) => (
              <li key={`seen-${id}`} className="out">
                <span>{nodes[id]?.title}</span>
                <span className="gl-num">vu</span>
                <em>cercle jusqu'à ce que la variation retombe sous le seuil puis le repasse</em>
              </li>
            ))}
            {over.map((id) => {
              const g = nodes[id]?.glance;
              if (!g) return null;
              const l = live[id];
              const f = fmt(g, l?.delta ?? 0);
              return (
                <li key={id} className={tiles.includes(id) ? "" : "out"}>
                  <span>{nodes[id].title}</span>
                  <span className="gl-num">
                    {f.arrow} {f.text} · ×{weightOf(g, l).toLocaleString("fr-FR", { maximumFractionDigits: 1 })}
                  </span>
                  <em>{tiles.includes(id) ? "tuile" : "cercle (plafond)"}</em>
                </li>
              );
            })}
          </ul>
        </>
      )}
    </aside>
  );
}
