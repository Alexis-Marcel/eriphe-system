import { useEffect, useMemo, useState, useSyncExternalStore } from "react";
import { useAdapter } from "../a2ui/adapter";
import { Markdown, type Wiki } from "../a2ui/markdown";
import { PicksContext, type Picks } from "../a2ui/picks";
import { SurfaceView } from "../a2ui/Surfaces";
import { act, ask, mark, openItem, type NodePage, type PickItem } from "../data/api";
import { sampleAct, sampleOpen } from "../data/samplecore";
import { sourceKey, store, useGraph } from "../data/store";
import { useWords } from "../i18n";
import { Cards } from "../page/Cards";
import { blockOf, messages, pickLists, pieces, sourceMessage, surfaceOf, toGesture, type Piece } from "./blocks";

// Doc draws a node's page: its text as Markdown, by the page's own
// renderer, which makes no HTML of it; its blocks through the A2UI
// renderer, a surface each, in an adapter of the page's own; a card block
// as the card it places, read from eriphe-core (Cards), or « not found ». While the
// last version shows, a source's new value goes straight into its block
// (the stream's "source"); an older version shows as it was. A block's
// gestures (a button's ask, a Mark) go to eriphe-core, which reads them
// again in the block it keeps (web_gestures.go); on an older version, or
// from a block someone else's words may have written, nothing is sent.
// A [[link]] of its text opens the node eriphe-core resolved it to
// (page.refs) beside the page (onOpen), named as the graph names it now.
// A List that filters a source's items shows the rows eriphe-core kept
// (list_sections.go). On the last version, live, a List of a source's items an action acts on
// lets the user pick rows and run the action (a2ui/picks.tsx): its rows
// follow the source's, the click goes to eriphe-core.
export function Doc({ page, latest, live, onOpen }: { page: NodePage; latest: boolean; live: boolean; onOpen?: (id: string) => void }) {
  const { t, lang } = useWords();
  const nodes = useGraph((s) => s.nodes);
  const wiki = useMemo<Wiki>(() => {
    const external = t("espace.external");
    return {
      find: (name) => {
        const r = page.refs?.[name];
        if (!r) return undefined;
        const n = nodes[r.id];
        const title = (n ? n.title : r.title) || external;
        const marked = n ? n.titleExternal : r.titleExternal;
        return { id: r.id, title, named: true, external: Boolean(marked), archived: r.archived };
      },
      open: (id) => onOpen?.(id),
      none: t("espace.wiki.none"),
      archived: (title) => t("espace.wiki.archived", title),
      external: t("espace.title.external"),
    };
  }, [page, nodes, onOpen, t]);
  const adapter = useAdapter(lang);
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
      if (g?.kind === "mark") void mark(id, page.version, g.block, g.list, g.item, g.mark).catch(() => undefined);
    });
  }, [adapter, latest, live, page]);

  // The rows of the lists to pick in, as the stream sends them after the
  // page was read; the page's own until then.
  const [rows, setRows] = useState<Record<string, PickItem[]>>({});
  useEffect(() => {
    setRows({});
    if (!latest) return;
    const id = page.node.id;
    return store.subscribe((s, before) => {
      if (s.rows === before.rows) return;
      const mine: Record<string, PickItem[]> = {};
      for (const [k, v] of Object.entries(s.rows)) if (before.rows[k] !== v && k.startsWith(id + "\u0000")) mine[k] = v;
      if (Object.keys(mine).length) setRows((r) => ({ ...r, ...mine }));
    });
  }, [latest, page]);
  const [kept] = useState<Picks["kept"]>(() => new Map());
  const [opened] = useState<NonNullable<Picks["opened"]>>(() => new Map());
  const picks = useMemo<Picks>(() => {
    const lists = pickLists(page, (block, component) => (latest ? rows[sourceKey(page.node.id, block, component)] : undefined));
    return {
      list: (surface, component) => lists.get(surface)?.find((l) => l.component === component),
      // Only on the last version, live: an older one shows its rows.
      // On the dev server, the sample's make-believe core (samplecore.ts).
      run: !latest
        ? undefined
        : live
          ? (surface, list, action, ids) => act(page.node.id, surface.slice(page.node.id.length + 2), list.component, action.name, page.version, ids)
          : (surface, list, action, ids) => sampleAct(page.node.id, surface.slice(page.node.id.length + 2), list.component, action.name, ids),
      kept,
      open: !latest
        ? undefined
        : live
          ? (surface, list, item, yes, tool) => openItem(page.node.id, surface.slice(page.node.id.length + 2), list.source, item, yes, tool)
          : (_surface, _list, item) => sampleOpen(page.node.id, item),
      opened,
      marked: (surface) => page.parts.some((p) => !!p.block?.external && surfaceOf(page.node.id, p.block.name) === surface),
    };
  }, [latest, live, page, rows, kept, opened]);

  const surfaces = useSyncExternalStore(adapter.subscribe, adapter.surfaces);
  if (ps.length === 0) return <p className="pg-empty">{t("espace.page.empty")}</p>;
  return (
    <PicksContext.Provider value={picks}>
      <div className="pg-doc">
        {ps.map((p: Piece, i) => {
          if (p.kind === "text") return <Markdown key={i} text={p.text} newTab={t("espace.a2.newtab")} wiki={wiki} />;
          if (p.kind === "broken") return <p key={i} className="pg-broken" role="note">{t("espace.page.broken", p.name)}</p>;
          if (p.kind === "card")
            return p.found ? (
              <div key={p.surface} className="pg-card" data-card={p.ref}>
                <Cards surface={p.surface} refs={[p.ref]} home={page.node.id} />
              </div>
            ) : (
              <p key={p.surface} className="pg-card-missing" role="note">
                {t("espace.card.missing")}
              </p>
            );
          const s = surfaces.find((x) => x.id === p.surface);
          return (
            <section key={p.surface} className={`a2-block${p.block.external ? " ext" : ""}`} data-surface={p.surface}>
              {/* Someone else's words: the page's head says it once, in
                  words; the block only says where, quietly (a thin edge, a
                  small dot on it whose words say why). */}
              {p.block.external && <span className="a2-block-mark" role="img" title={t("espace.external.why")} aria-label={t("espace.external.why")} />}
              {s && <SurfaceView surface={s} />}
            </section>
          );
        })}
      </div>
    </PicksContext.Provider>
  );
}
