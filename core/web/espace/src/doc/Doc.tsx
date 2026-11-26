import { useMemo } from "react";
import { type Wiki } from "../a2ui/markdown";
import { askLine, tick, type NodePage } from "../data/api";
import { useGraph } from "../data/store";
import { useWords } from "../i18n";
import { PageBody, type BodyProps } from "../page/Body";
import { usePageSlots } from "../page/items";
import { Props } from "./Props";

// Doc draws a node's page: its body as a Markdown page (page/Body.tsx),
// by the page's own renderer, which makes no HTML of it; on the last
// version of a node on the graph, a task's box ticks (eriphe-core writes
// its line, on the node's last version) and an ask: link asks
// (eriphe-core reads its words again in the page it keeps,
// web_gestures.go); on an older version nothing is sent.
// A [[link]] of its text opens the node eriphe-core resolved it to
// (page.refs) beside the page (onOpen), named as the graph names it now.
// Its lines carry what eriphe-core places there (page/items.tsx): the
// cards on the rows of the items it references, a card: link's card, an
// item opened below its row.
export function Doc({ page, latest, live, onOpen }: { page: NodePage; latest: boolean; live: boolean; onOpen?: (id: string) => void }) {
  const { t } = useWords();
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
  const id = page.node.id;
  const acts = latest && live;
  const onTick = useMemo<BodyProps["tick"]>(() => (acts ? (it, line, to) => tick(id, it.line, line, to).catch(() => false) : undefined), [acts, id]);
  const onAsk = useMemo<BodyProps["ask"]>(() => (acts ? (line, n) => void askLine(id, line, n).catch(() => undefined) : undefined), [acts, id]);
  // What eriphe-core places on its lines: cards on the rows of the items
  // it references, cards placed by card: links, the original of an item
  // opened below its row (page/items.tsx).
  const slots = usePageSlots(page, latest, live);
  if (!page.body.trim())
    return (
      <>
        <Props page={page} />
        <p className="pg-empty">{t("espace.page.empty")}</p>
      </>
    );
  return (
    <>
      <Props page={page} />
      <PageBody body={page.body} wiki={wiki} tick={onTick} ask={onAsk} slots={slots} />
    </>
  );
}
