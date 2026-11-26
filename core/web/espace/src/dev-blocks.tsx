import { useMemo } from "react";
import { MotionConfig } from "motion/react";
import { sampleProposals } from "./a2ui/fixtures";
import { ProposalsContext } from "./a2ui/proposals";
import type { NodePage } from "./data/api";
import { samplePage, type Authored } from "./data/samplecore";
import { gallerySections, type GallerySection } from "./data/sampledays";
import { useWords } from "./i18n";
import golden from "./doc/page.golden.json";
import { Doc } from "./doc/Doc";
import "./dev.css";

// The dev server's ?blocks: the blocks a node's page is made of, each in a
// page of its own as the page draws it (Doc), with what it is for and the
// JSON Eriphe writes for it. Made-up data (data/sampledays.ts); dev.tsx
// imports it, so none of it is in the built app.

function pageOf(s: GallerySection): NodePage {
  const base = golden as unknown as NodePage;
  const p = samplePage(s.id)!;
  const now = new Date().toISOString();
  return {
    ...base,
    node: { id: s.id, title: s.title, maintained: ["vitrine"], weight: 0.6, status: "idle", created: now, updated: now, version: 1 },
    body: p.body,
    parts: p.parts,
    cards: p.cards,
    settled: p.settled,
  };
}

// The block as its file holds it: one key a line, a component a line.
export function written(b: Authored): string {
  const keys: string[] = [`{"components": [\n${b.components.map((c) => "  " + JSON.stringify(c)).join(",\n")}\n]`];
  if (b.data) keys.push(` "data": ${JSON.stringify(b.data)}`);
  if (b.sources) keys.push(` "sources": ${JSON.stringify(b.sources)}`);
  return "```eriphe:block " + b.name + "\n" + keys.join(",\n") + "}\n```";
}

export function BlocksGallery() {
  const { lang } = useWords();
  const proposals = useMemo(() => sampleProposals(lang), [lang]);
  const pages = useMemo(() => gallerySections.map((s) => ({ s, page: pageOf(s) })), []);
  return (
    <ProposalsContext.Provider value={proposals}>
      <MotionConfig reducedMotion="user">
        <main className="dev-blocks">
          <header className="dev-blocks-head">
            <h1>Blocs</h1>
            <p>Chaque bloc dans une page, comme Eriphe l'écrit, avec ce à quoi il sert et son JSON. Les données sont inventées.</p>
          </header>
          {pages.map(({ s, page }) => (
            <section key={s.id} className="dev-blocks-sec" id={s.id}>
              <h2>{s.title}</h2>
              <p className="dev-blocks-for">{s.for}</p>
              <div className="dev-blocks-page">
                <Doc page={page} latest live={false} />
              </div>
              <details className="dev-blocks-json">
                <summary>Ce qu'Eriphe écrit</summary>
                <pre>{s.view.parts.filter((p): p is Authored => typeof p !== "string").map(written).join("\n\n")}</pre>
              </details>
            </section>
          ))}
        </main>
      </MotionConfig>
    </ProposalsContext.Provider>
  );
}
