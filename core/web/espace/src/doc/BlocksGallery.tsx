import { useMemo } from "react";
import { MotionConfig } from "motion/react";
import { sampleProposals } from "../a2ui/fixtures";
import { ProposalsContext } from "../a2ui/proposals";
import type { NodePage } from "../data/api";
import { samplePage, type Authored } from "../data/samplecore";
import { gallerySections, type GallerySection } from "../data/sampledays";
import "../data/sample"; // registers the sample views
import { useWords } from "../i18n";
import golden from "./page.golden.json";
import { Doc } from "./Doc";

// Prototype (#730), the dev server's ?blocks: each new or changed block in
// a page of its own, as a node's page draws it (Doc), with what it replaces
// and the JSON Eriphe writes for it. Made-up data (sampledays.ts).

function pageOf(s: GallerySection): NodePage {
  const base = golden as unknown as NodePage;
  const p = samplePage(s.id)!;
  const now = new Date().toISOString();
  return {
    ...base,
    node: { id: s.id, title: s.title, lifetime: "standing", status: "idle", created: now, updated: now, version: 1 },
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
        <main className="proto-blocks">
          <header className="proto-head">
            <h1>Blocs : prototype</h1>
            <p>Chaque bloc nouveau ou revu, dans une page comme Eriphe l'écrirait, avec ce qu'il remplace et le JSON du bloc. Les données sont inventées.</p>
          </header>
          {pages.map(({ s, page }) => (
            <section key={s.id} className="proto-sec" id={s.id}>
              <h2>{s.title}</h2>
              <p className="proto-replaces">{s.replaces}</p>
              <div className="proto-page">
                <Doc page={page} latest live={false} />
              </div>
              <details className="proto-json">
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
