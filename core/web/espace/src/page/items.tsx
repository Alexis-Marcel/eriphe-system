import { useEffect, useMemo, useState } from "react";
import { Icon } from "../a2ui/icons";
import { InlineYes, RowItem } from "../a2ui/inline";
import { OpenedView, sender, type OpenState } from "../a2ui/opener";
import { openItem, type NodePage, type PageItem } from "../data/api";
import { useWords } from "../i18n";
import type { Slots } from "./Body";
import { usePageActs } from "./acts";
import { Cards } from "./Cards";
import type { MdItem, PageLink } from "./read";

// What eriphe-core places on a page's lines (page_items.go), drawn where
// it says, never where the page's own words would put it:
//
//   - A card that waits on an item the page references ([text][m1], with
//     [m1]: mail:<id>) is on the row of the item that writes the
//     reference (its Oui, its ✕, its words in code's; a card several rows
//     share shows on each, with how many: a Oui on any answers it).
//   - A card: link places one of the node's own cards where it is
//     written: alone on its line, the whole card; in a sentence, its Oui.
//   - A reference whose words do not say the item its server read carries
//     a warning and what the item is, as read (« Les clés pour samedi » ·
//     Sam); one whose item could not be read, a quiet « non vérifié ».
//   - A row that references an item its server opens opens it below, on
//     a click, read by eriphe-core from the page as stored (only on the
//     last version); the page keeps what it read for its session.

// usePageSlots are the slots (page/Body.tsx) of a node's page.
export function usePageSlots(page: NodePage, latest: boolean, live = true): Slots {
  const [opened] = useState(() => new Map<string, OpenState>());
  // The do: links of its headings, and the boxes of their rows (acts.tsx).
  // Only on the last version of a node that takes gestures.
  const acts = usePageActs(page, latest && live);
  return useMemo<Slots>(() => {
    const id = page.node.id;
    const items = page.items ?? [];
    const placed = new Set(page.placed ?? []);
    const marked = !!page.node.external;
    // How many rows carry each card: a card several rows share says it.
    const rows = new Map<string, Set<number>>();
    for (const it of items) if (it.card && it.row) rows.set(it.card, (rows.get(it.card) ?? new Set()).add(it.row));
    const onRow = (line: number) => items.filter((it) => it.row === line);
    return {
      item: (it: MdItem) => {
        const mine = onRow(it.line).filter((x) => x.card);
        const ticks = acts.item(it);
        if (!mine.length) return ticks;
        const seen = new Set<string>();
        return (
          <span className="md-cards">
            {ticks}
            {mine.map((x) => {
              if (seen.has(x.card!)) return null;
              seen.add(x.card!);
              return (
                <RowItem.Provider key={x.card} value={{ n: it.line, id: x.id, card: x.card, shared: rows.get(x.card!)?.size ?? 1 }}>
                  <InlineYes refText={x.card!} say where={{ surfaceId: `${id}--page`, componentId: `line-${it.line}` }} marked={marked} />
                </RowItem.Provider>
              );
            })}
          </span>
        );
      },
      link: (l: PageLink) => {
        if (l.kind === "do") return acts.link(l);
        if (l.kind !== "card" || !placed.has(l.target)) return undefined;
        if (l.alone)
          return (
            <div className="md-card" data-card={l.target}>
              <Cards surface={`${id}--line-${l.line}`} refs={[l.target]} home={id} />
            </div>
          );
        return <InlineYes refText={l.target} say where={{ surfaceId: `${id}--page`, componentId: `line-${l.line}` }} marked={marked} />;
      },
      ref: (label: string, ln: number) => {
        const x = items.find((it) => it.line === ln && it.label === label && it.card) ?? acts.ref(label, ln);
        if (x?.misleading) return <Misleading x={x} />;
        if (x?.unread) return <Unread />;
        return null;
      },
      opens: latest
        ? (it: MdItem) => {
            const mine = onRow(it.line).filter((x) => x.open);
            const labels = [...new Map(mine.map((x) => [x.label, x])).values()];
            if (!labels.length) return undefined;
            return (
              <>
                {labels.map((x) => (
                  <RefOpened key={x.label} id={id} x={x} card={onRow(it.line).find((y) => y.label === x.label && y.card)?.card} marked={marked} cache={opened} />
                ))}
              </>
            );
          }
        : undefined,
    };
  }, [page, latest, opened, acts]);
}

// Misleading is the warning beside a reference whose words do not say the
// item its server read, and what the item is, as read: its title, and who
// it is from (a name, its address in the tooltip).
function Misleading({ x }: { x: Pick<PageItem, "title" | "detail"> }) {
  const { t } = useWords();
  const what = t("espace.md.ref.misleading", [x.title, x.detail].filter(Boolean).join(" · "));
  const who = x.detail ? sender(x.detail).name : "";
  return (
    <span className="md-ref-lie" title={what}>
      <Icon name="warning" />
      <span className="sr-only">{what}</span>
      <span className="md-ref-real" aria-hidden="true">
        {x.title ? `« ${x.title} »` : ""}
        {x.title && who ? " · " : ""}
        {who}
      </span>
    </span>
  );
}

// Unread is the quiet sign of a reference whose item eriphe-core could not
// read: its words are not checked.
function Unread() {
  const { t } = useWords();
  return (
    <span className="md-ref-unread" title={t("espace.md.ref.unread.why")}>
      {t("espace.md.ref.unread")}
    </span>
  );
}

// RefOpened is the item a reference names, opened below its row: read
// once by eriphe-core, kept for the page's session (cache).
function RefOpened({ id, x, card, marked, cache }: { id: string; x: PageItem; card?: string; marked: boolean; cache: Map<string, OpenState> }) {
  const { t } = useWords();
  const key = `${id}\u0000${x.label}`;
  const [st, setSt] = useState<OpenState>(() => cache.get(key) ?? { open: true, loading: true });
  useEffect(() => {
    if (st.read) return;
    let live = true;
    const pending =
      cache.get(key)?.pending ??
      openItem(id, { ref: x.label }).catch(() => ({
        error: t("espace.open.error"),
      }));
    cache.set(key, { open: true, loading: true, pending });
    void pending.then((read) => {
      const next = { open: true, read };
      if (read.error) cache.delete(key);
      else cache.set(key, next);
      if (live) setSt(next);
    });
    return () => {
      live = false;
    };
  }, [cache, key, id, x.label, st.read, t]);
  return (
    <div className="a2-opened md-ref-opened" role="region" aria-label={x.text}>
      <OpenedView st={st} said="" marked={marked} card={card} where={{ surfaceId: `${id}--page`, componentId: `open-${x.label}` }} named={x.text} />
    </div>
  );
}
