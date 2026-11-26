import { Component, useCallback, useMemo, useState, type ReactNode } from "react";
import { MotionConfig } from "motion/react";
import { ProposalsContext } from "../a2ui/proposals";
import demenagement from "./demenagement-A.md?raw";
import { fakeProposals } from "./fake";
import type { Problem } from "./parse";
import { MdPage } from "./render";
import { demenagementTitle, samples } from "./samples";
import "../dev.css";

// PROTOTYPE, the dev server's ?markdown: a node's page written as plain
// Markdown (Obsidian's conventions), drawn by Eriphe's components. Left, the
// sample pages; for each, Rendu (Eriphe's drawing), Fichier .md (the file
// itself, editable: a tick writes into it), Aujourd'hui (the same page as
// today's JSON block). « Coller une page »: any Markdown, drawn live.

const PASTE = "coller";
const DEM = "demenagement";
const size = (s: string) => `${s.length.toLocaleString("fr-FR")} caractères · ≈ ${Math.round(s.length / 4).toLocaleString("fr-FR")} tokens`;

export function DevMarkdown() {
  const proposals = useMemo(() => fakeProposals(), []);
  const pages = useMemo(() => [...samples.map((s) => ({ id: s.id, title: s.title })), { id: DEM, title: demenagementTitle }], []);
  const [files, setFiles] = useState<Record<string, string>>(() => ({ ...Object.fromEntries(samples.map((s) => [s.id, s.md])), [DEM]: demenagement, [PASTE]: "" }));
  const [sel, setSel] = useState(() => new URLSearchParams(location.search).get("page") ?? "mails");
  const [tab, setTab] = useState<"rendu" | "md" | "today">("rendu");
  const [problems, setProblems] = useState<Problem[]>([]);
  const byTitle = useCallback((title: string) => pages.find((p) => p.title.toLowerCase() === title.toLowerCase()), [pages]);
  const setter = useCallback((id: string) => (f: (s: string) => string) => setFiles((x) => ({ ...x, [id]: f(x[id]) })), []);
  const openNode = useCallback(
    (title: string) => {
      const p = byTitle(title);
      if (p) {
        setSel(p.id);
        setTab("rendu");
      }
    },
    [byTitle],
  );
  const embed = useCallback(
    (title: string) => {
      const p = byTitle(title);
      return p ? { md: files[p.id], setMd: setter(p.id) } : undefined;
    },
    [byTitle, files, setter],
  );
  const sample = samples.find((s) => s.id === sel);
  const title = sel === PASTE ? "Coller une page" : (pages.find((p) => p.id === sel)?.title ?? "");
  const md = files[sel] ?? "";
  const onProblems = useCallback((p: Problem[]) => setProblems(p), []);
  return (
    <ProposalsContext.Provider value={proposals}>
      <MotionConfig reducedMotion="user">
        <div className="dev-md">
          <nav className="dev-md-nav">
            <h1>Pages en Markdown</h1>
            <p>Prototype : le fichier est le contenu, Eriphe le décore. Données inventées.</p>
            {pages.map((p) => (
              <button key={p.id} type="button" className={sel === p.id ? "on" : ""} onClick={() => (setSel(p.id), setTab("rendu"))}>
                {p.title}
                <small>{files[p.id].length.toLocaleString("fr-FR")} car.</small>
              </button>
            ))}
            <button type="button" className={`dev-md-paste${sel === PASTE ? " on" : ""}`} onClick={() => (setSel(PASTE), setTab("rendu"))}>
              Coller une page
            </button>
          </nav>
          <main className="dev-md-main">
            <header className="dev-md-head">
              <h2>{title}</h2>
              {sel !== PASTE && (
                <div className="dev-md-tabs" role="tablist">
                  {(
                    [
                      ["rendu", "Rendu"],
                      ["md", "Fichier .md"],
                      ...(sample ? [["today", "Aujourd'hui"]] : []),
                    ] as [typeof tab, string][]
                  ).map(([k, l]) => (
                    <button key={k} type="button" role="tab" aria-selected={tab === k} className={tab === k ? "on" : ""} onClick={() => setTab(k)}>
                      {l}
                    </button>
                  ))}
                </div>
              )}
              <p className="dev-md-size">
                Markdown : {size(md)}
                {sample && <> · Aujourd'hui (JSON) : {size(sample.today)}</>}
              </p>
            </header>
            {sel === PASTE && (
              <textarea className="dev-md-src dev-md-pastebox" value={md} placeholder="Colle ici une page en Markdown : elle se dessine dessous." onChange={(e) => setFiles((x) => ({ ...x, [PASTE]: e.target.value }))} spellCheck={false} />
            )}
            {(tab === "rendu" || sel === PASTE) && (
              <>
                <div className="dev-md-page dev-blocks-page">
                  <Boundary key={sel + md.length}>
                    <MdPage key={sel} md={md} setMd={setter(sel)} page={sel} openNode={openNode} embed={embed} onProblems={onProblems} />
                  </Boundary>
                </div>
                {problems.length > 0 && (
                  <details className="dev-md-problems" open={sel === PASTE || sel === DEM}>
                    <summary>Ce que le code dirait à l'auteur ({problems.length})</summary>
                    <ul>
                      {problems.map((p, i) => (
                        <li key={i}>
                          {p.ln >= 0 && <code>l. {p.ln + 1}</code>} {p.what}
                        </li>
                      ))}
                    </ul>
                  </details>
                )}
              </>
            )}
            {tab === "md" && sel !== PASTE && (
              <>
                <p className="dev-md-hint">Exactement ce que lisent Obsidian et un LLM. Modifiable : le rendu suit ; une coche dans le rendu s'écrit ici.</p>
                <textarea className="dev-md-src" value={md} onChange={(e) => setFiles((x) => ({ ...x, [sel]: e.target.value }))} spellCheck={false} />
              </>
            )}
            {tab === "today" && sample && (
              <>
                <p className="dev-md-hint">{sample.todayNote}</p>
                <pre className="dev-md-src">{sample.today}</pre>
              </>
            )}
          </main>
        </div>
      </MotionConfig>
    </ProposalsContext.Provider>
  );
}

// Boundary: a page the parser or a component chokes on says so, visibly.
class Boundary extends Component<{ children: ReactNode }, { error: string }> {
  state = { error: "" };
  static getDerivedStateFromError(e: unknown) {
    return { error: e instanceof Error ? e.message : String(e) };
  }
  render() {
    return this.state.error ? <p className="md-lie">Le rendu a échoué : {this.state.error}</p> : this.props.children;
  }
}
