import { useEffect, useMemo, useState, useSyncExternalStore } from "react";
import { createAdapter, type Adapter } from "../a2ui/adapter";
import { Markdown } from "../a2ui/markdown";
import { SurfaceView } from "../a2ui/Surfaces";
import { ask, mark, type NodePage } from "../data/api";
import { sourceKey, store } from "../data/store";
import { useWords } from "../i18n";
import { Cards } from "../page/Cards";
import { blockOf, messages, pieces, sourceMessage, toGesture, type Piece } from "./blocks";

// Doc draws a node's page: its text as Markdown, by the page's own
// renderer, which makes no HTML of it; its blocks through the A2UI
// renderer, a surface each, in an adapter of the page's own; a card block
// as the card it places, read from eriphe-core (Cards), or « not found ». While the
// last version shows, a source's new value goes straight into its block
// (the stream's "source"); an older version shows as it was. A block's
// gestures (a button's ask, a Mark) go to eriphe-core, which reads them
// again in the block it keeps (web_gestures.go); on an older version, or
// from a block someone else's words may have written, nothing is sent.
export function Doc({ page, latest, live }: { page: NodePage; latest: boolean; live: boolean }) {
  const { t, lang } = useWords();
  const [adapter] = useState<Adapter>(() => createAdapter(lang));
  useEffect(() => () => adapter.dispose(), [adapter]);
  const ps = useMemo(() => pieces(page), [page]);

  // The page read again (a new version, another one asked): its blocks
  // made again from scratch.
  useEffect(() => {
    adapter.applyMessages(adapter.surfaces().map((s) => ({ version: "1.0" as const, deleteSurface: { surfaceId: s.id } })));
    adapter.applyMessages(messages(page));
  }, [adapter, page]);

  useEffect(() => {
    if (!latest) return;
    const id = page.node.id;
    const names = ps.flatMap((p) => (p.kind === "block" ? [p.block.name] : []));
    return store.subscribe((s, before) => {
      if (s.sources === before.sources) return;
      for (const [k, v] of Object.entries(s.sources)) {
        if (before.sources[k] === v) continue;
        const [node, block, name] = k.split("\u0000");
        if (node === id && names.includes(block) && k === sourceKey(node, block, name)) adapter.applyMessages([sourceMessage(node, block, name, v)]);
      }
    });
  }, [adapter, latest, page, ps]);

  useEffect(() => {
    if (!latest || !live) return;
    const id = page.node.id;
    return adapter.onAction((m) => {
      const g = toGesture(m.action, id, (name) => blockOf(page, name));
      if (g?.kind === "ask") void ask(id, g.block, g.component).catch(() => undefined);
      if (g?.kind === "mark") void mark(id, g.block, g.list, g.item, g.mark).catch(() => undefined);
    });
  }, [adapter, latest, live, page]);

  const surfaces = useSyncExternalStore(adapter.subscribe, adapter.surfaces);
  if (ps.length === 0) return <p className="pg-empty">{t("espace.page.empty")}</p>;
  return (
    <div className="pg-doc">
      {ps.map((p: Piece, i) => {
        if (p.kind === "text") return <Markdown key={i} text={p.text} newTab={t("espace.a2.newtab")} />;
        if (p.kind === "broken") return <p key={i} className="pg-broken" role="note">{t("espace.page.broken", p.name)}</p>;
        if (p.kind === "card")
          return p.found ? (
            <div key={p.surface} className="pg-card" data-card={p.ref}>
              <Cards surface={p.surface} refs={[p.ref]} />
            </div>
          ) : (
            <p key={p.surface} className="pg-card-missing" role="note">
              {t("espace.card.missing")}
            </p>
          );
        const s = surfaces.find((x) => x.id === p.surface);
        return (
          <section key={p.surface} className="a2-block" data-surface={p.surface}>
            {p.block.external && <span className="a2-text-mark">{t("espace.external.mark")}</span>}
            {s && <SurfaceView surface={s} />}
          </section>
        );
      })}
    </div>
  );
}
