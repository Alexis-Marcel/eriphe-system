import { AnimatePresence, LayoutGroup, MotionConfig, motion } from "motion/react";
import { StrictMode, useState } from "react";
import { createRoot } from "react-dom/client";
import { WordsContext, makeT } from "../i18n";
import strings from "../strings.json";
import { AfterPage, type Sim, type State } from "./After";
import { BeforePage } from "./Before";
import { scenarioOf, scenarios } from "./scenarios";
import { pct, pretty, sizeOf, tokens } from "./size";
import "../fonts.css";
import "../espace.css";
import "./proto.css";

// Prototype « belles vues » (PROTO.md). Two layers:
//  - the shell (no ?frame): a bar to pick scenario, variant, theme, width,
//    motion and state, the page in an iframe of that width (so the real
//    renderer's media queries fire), the JSON of both sides and its size;
//  - the frame (?frame): the page alone, as a node's page, with a small
//    dock to replay the opening and simulate what happens (?shot hides it).

const q = new URLSearchParams(location.search);
const param = (k: string, d: string) => q.get(k) ?? d;
const theme = param("theme", "light");
document.documentElement.dataset.theme = theme;

type Variant = "avant" | "habille" | "apres" | "cote";
const VARIANTS: [Variant, string][] = [
  ["avant", "Aujourd'hui"],
  ["habille", "Habillé (même JSON)"],
  ["apres", "Nouveaux blocs"],
  ["cote", "Côte à côte"],
];
const STATES: [State, string][] = [
  ["live", "Données"],
  ["loading", "Chargement"],
  ["empty", "Vide"],
  ["error", "Erreur de source"],
];

// --- the frame: one page ---------------------------------------------------------

function Frame() {
  const s = scenarioOf(q.get("s"));
  const v = param("v", "apres") as Variant;
  const state = param("state", "live") as State;
  const shot = q.has("shot");
  const [open, setOpen] = useState(!q.has("closed"));
  const [replay, setReplay] = useState(0);
  const [sim, setSim] = useState<Sim>({ bump: 0, arrived: false, step: 0, first: false });
  const after = v === "apres";
  return (
    <MotionConfig reducedMotion={param("motion", "1") === "0" ? "always" : "user"}>
      <LayoutGroup>
        <main className={`bv-stage ${shot ? "shot" : ""}`}>
          {!open && (
            <div className="bv-graph">
              {scenarios.map((x) => (
                <motion.button key={x.id} layoutId={x.id === s.id ? "node" : undefined} className={`bv-node${x.id === s.id ? " me" : ""}`} onClick={() => x.id === s.id && setOpen(true)} transition={{ duration: 0.24, ease: [0.2, 0, 0, 1] }}>
                  <span>{x.title}</span>
                </motion.button>
              ))}
            </div>
          )}
          <AnimatePresence>
            {open && (
              <motion.article key="page" layoutId="node" className="bv-page" transition={{ duration: 0.24, ease: [0.2, 0, 0, 1] }}>
                <motion.header className="bv-page-head" initial={{ opacity: 0 }} animate={{ opacity: 1 }} transition={{ duration: 0.12 }}>
                  <h1>
                    {s.standing && <span className="bv-standing" aria-label="vue permanente" />}
                    {s.title}
                  </h1>
                  <p>{s.line}</p>
                </motion.header>
                <div className="bv-page-body">
                  {after ? <AfterPage s={s} state={state} sim={sim} replay={replay} /> : <BeforePage key={replay} s={s} state={state} polish={v === "habille"} />}
                </div>
              </motion.article>
            )}
          </AnimatePresence>
        </main>
      </LayoutGroup>
      {!shot && (
        <nav className="bv-dock" aria-label="Simuler">
          <button onClick={() => (open ? setOpen(false) : setOpen(true))}>{open ? "Fermer vers le nœud" : "Ouvrir depuis le nœud"}</button>
          <button onClick={() => (setReplay((r) => r + 1), setSim({ bump: 0, arrived: false, step: 0, first: false }))}>Rejouer</button>
          {after && s.id === "mails" && <button onClick={() => setSim((x) => ({ ...x, arrived: true }))}>Un mail arrive</button>}
          {after && s.id === "invest" && <button onClick={() => setSim((x) => ({ ...x, bump: x.bump + 1 }))}>Le marché bouge</button>}
          {after && s.id === "projet" && <button onClick={() => setSim((x) => ({ ...x, step: x.step + 1 }))}>Étape suivante</button>}
          {after && s.id === "premiere" && <button onClick={() => setSim((x) => ({ ...x, first: true }))}>Premiers articles</button>}
        </nav>
      )}
    </MotionConfig>
  );
}

// --- the shell ---------------------------------------------------------------------

const url = (over: Record<string, string | null>) => {
  const p = new URLSearchParams(location.search);
  for (const [k, v] of Object.entries(over)) v === null ? p.delete(k) : p.set(k, v);
  return `?${p}`;
};

function Seg<T extends string>({ k, value, opts }: { k: string; value: T; opts: [T, string][] }) {
  return (
    <span className="sh-seg">
      {opts.map(([v, l]) => (
        <a key={v} href={url({ [k]: v })} className={v === value ? "on" : ""}>
          {l}
        </a>
      ))}
    </span>
  );
}

function Sizes() {
  const rows = scenarios.map(sizeOf);
  const sum = (k: keyof ReturnType<typeof sizeOf>) => rows.reduce((n, r) => n + (r[k] as number), 0);
  return (
    <div className="sh-sizes">
      <table>
        <thead>
          <tr>
            <th>Page</th>
            <th>Blocs aujourd'hui</th>
            <th>Blocs prototype</th>
            <th>Écart</th>
            <th>Composants</th>
            <th>Corps entier (texte compris)</th>
          </tr>
        </thead>
        <tbody>
          {rows.map((r) => (
            <tr key={r.id}>
              <td>
                <a href={url({ s: r.id })}>{r.title}</a>
              </td>
              <td>
                {r.before} car. <small>≈ {tokens(r.before)} tk</small>
              </td>
              <td>
                {r.after} car. <small>≈ {tokens(r.after)} tk</small>
              </td>
              <td>
                <b>{pct(r.before, r.after)}</b>
              </td>
              <td>
                {r.beforeComps} → {r.afterComps}
              </td>
              <td>
                {r.beforeBody} → {r.afterBody} ({pct(r.beforeBody, r.afterBody)})
              </td>
            </tr>
          ))}
          <tr className="sum">
            <td>Total</td>
            <td>{sum("before")} car.</td>
            <td>{sum("after")} car.</td>
            <td>
              <b>{pct(sum("before"), sum("after"))}</b>
            </td>
            <td>
              {sum("beforeComps")} → {sum("afterComps")}
            </td>
            <td>
              {sum("beforeBody")} → {sum("afterBody")} ({pct(sum("beforeBody"), sum("afterBody"))})
            </td>
          </tr>
        </tbody>
      </table>
    </div>
  );
}

function Shell() {
  const sid = param("s", "mails");
  const s = scenarioOf(sid);
  const v = param("v", "apres") as Variant;
  const w = param("w", "desktop");
  const [showJson, setShowJson] = useState(q.has("json"));
  const frame = (variant: Variant) => url({ frame: "1", v: variant });
  const size = sizeOf(s);
  const frames: Variant[] = v === "cote" ? ["avant", "apres"] : [v];
  return (
    <div className="sh">
      <header className="sh-bar">
        <b>Belles vues</b>
        <span className="sh-seg">
          {scenarios.map((x) => (
            <a key={x.id} href={url({ s: x.id })} className={x.id === sid ? "on" : ""}>
              {x.title}
            </a>
          ))}
          <a href={url({ s: "tailles" })} className={sid === "tailles" ? "on" : ""}>
            Tailles
          </a>
        </span>
        <Seg k="v" value={v} opts={VARIANTS} />
        <Seg k="theme" value={theme} opts={[["light", "Clair"], ["dark", "Sombre"]]} />
        <Seg k="w" value={w} opts={[["phone", "Téléphone"], ["desktop", "Bureau"]]} />
        <Seg k="motion" value={param("motion", "1")} opts={[["1", "Mouvement"], ["0", "Réduit"]]} />
        <Seg k="state" value={param("state", "live") as State} opts={STATES} />
        <button className="sh-json" onClick={() => setShowJson(!showJson)}>
          JSON · {size.before} → {size.after} car. ({pct(size.before, size.after)})
        </button>
      </header>
      {sid === "tailles" ? (
        <Sizes />
      ) : (
        <div className={`sh-main ${showJson ? "with-json" : ""}`}>
          <div className={`sh-frames ${w}`}>
            {frames.map((f) => (
              <figure key={f}>
                {frames.length > 1 && <figcaption>{VARIANTS.find(([k]) => k === f)?.[1]}</figcaption>}
                <iframe title={f} src={frame(f)} className={w} />
              </figure>
            ))}
          </div>
          {showJson && (
            <aside className="sh-code">
              <section>
                <h2>
                  Aujourd'hui · {size.before} car. · {size.beforeComps} composants
                </h2>
                <pre>{s.before.map((p) => (typeof p === "string" ? "" : pretty(p))).filter(Boolean).join("\n\n")}</pre>
              </section>
              <section>
                <h2>
                  Prototype · {size.after} car. · {size.afterComps} composants
                </h2>
                <pre>{s.after.map((p) => (typeof p === "string" ? "" : pretty(p))).filter(Boolean).join("\n\n")}</pre>
              </section>
              <p className="sh-note">Compté : les blocs tels qu'ils s'écrivent dans le fichier du nœud (barrières comprises, JSON compact). Le texte est le même des deux côtés. Corps entier : voir « Tailles ».</p>
            </aside>
          )}
        </div>
      )}
    </div>
  );
}

const t = makeT(strings.fr);
createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <WordsContext.Provider value={{ t, lang: "fr" }}>{q.has("frame") ? <Frame /> : <Shell />}</WordsContext.Provider>
  </StrictMode>,
);
