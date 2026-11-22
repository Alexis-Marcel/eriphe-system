import { createContext, useContext, type ReactNode } from "react";
import type { ListRow, Opened, SourceList } from "../data/api";
import { useWords } from "../i18n";
import { InlineYes, RowItem } from "./inline";
import { Opener, type OpenState } from "./opener";

// The Lists of a source's items eriphe-core tells the page of (list_rows.go,
// list_sections.go): a List that filters the source's items (a section:
// « À supprimer ») shows only the rows code kept, in the source's order;
// a row a card waits on carries that card's Oui, drawn by the page from
// what eriphe-core told of the row, never from the block's data; a row
// whose source opens its items opens in place (opener.tsx). Nothing in a
// block acts: no box, no action bar, no mark. A yes is a card's.

// Lists is what a page gives its Lists: the list a List component of a
// surface is, if eriphe-core told of it, and how to read an item in full
// (none on an older version), what the page read, by surface, List and
// item, and whether a surface is a block marked as someone else's words
// (its edge says it): an item opened in it says nothing more.
export type Lists = {
  list(surface: string, component: string): SourceList | undefined;
  open?: (surface: string, list: SourceList, item: string) => Promise<Opened>;
  opened?: Map<string, OpenState>;
  marked?: (surface: string) => boolean;
};

export const ListsContext = createContext<Lists | null>(null);

// A batch (a Fold that names a List: « 38 mails inutiles »): the Fold's
// line carries the Oui of the card its rows share; opened, the List shows
// each row with its box, all ticked, and the user unticks what they keep
// (left): the Oui then leaves out the calls of the rows left.
export type Batch = { list: string; left: ReadonlySet<string>; toggle: (id: string) => void };
export const BatchContext = createContext<Batch | null>(null);

// useList is what eriphe-core told of a List: its rows (a filtered one
// shows only those).
export function useList(surface: string | undefined, component: string): { list: SourceList; lists: Lists } | undefined {
  const lists = useContext(ListsContext);
  const list = lists && surface ? lists.list(surface, component) : undefined;
  return lists && list ? { list, lists } : undefined;
}

// sharedCard is the card all of a List's rows share, if they do: a batch's.
export function sharedCard(items: ListRow[]): string | undefined {
  const first = items[0]?.card;
  return first && items.every((it) => it.card === first) ? first : undefined;
}

// openable is a row as a List draws it: with the Oui of the card that
// waits on it, beside it (a card on a row is always answered on that row),
// and opening in place when its source opens its items and the page may
// read them (opener.tsx). The row also tells what it draws which item it
// is, as eriphe-core listed it (RowItem), and how many rows of the List
// share its card.
export function openable(surface: string, list: SourceList, lists: Lists, node: ReactNode, item: ListRow | undefined, quiet = false): ReactNode {
  if (!item) return node;
  const shared = item.card ? list.items.filter((it) => it.card === item.card).length : 0;
  const row =
    item.card && !quiet ? (
      <div className="a2-row a2-align-center a2-rowcard">
        {node}
        <InlineYes refText={item.card} say where={{ surfaceId: surface, componentId: list.component }} />
      </div>
    ) : (
      node
    );
  const inner = !list.open || !lists.open ? row : (
    <Opener surface={surface} list={list} lists={lists} item={item}>
      {row}
    </Opener>
  );
  return <RowItem.Provider value={{ ...item, shared }}>{inner}</RowItem.Provider>;
}

export { RowItem };

// A row of a List as drawn: its place in the source's list, and what it
// shows.
export type Row = { n: number; node: ReactNode };

// SourceRows draws a List's rows: a batch's with their boxes (BatchRows),
// any other with each row's card.
export function SourceRows({ surface, list, lists, rows, direction }: { surface: string; list: SourceList; lists: Lists; rows: Row[]; direction: string }) {
  const batch = useContext(BatchContext);
  const byN = new Map(list.items.map((it) => [it.n, it]));
  if (batch && batch.list === list.component) return <BatchRows surface={surface} list={list} lists={lists} rows={rows} direction={direction} batch={batch} />;
  return (
    <ul className={`a2-list a2-list-${direction}`}>
      {rows.map((r) => (
        <li key={r.n}>{openable(surface, list, lists, r.node, byN.get(r.n))}</li>
      ))}
    </ul>
  );
}

// BatchRows are a batch's rows: each ticked unless the user left it. The
// card's Oui is on the batch's line, not on each row.
function BatchRows({ surface, list, lists, rows, direction, batch }: { surface: string; list: SourceList; lists: Lists; rows: Row[]; direction: string; batch: Batch }) {
  const { t } = useWords();
  const byN = new Map(list.items.map((it) => [it.n, it]));
  const card = sharedCard(list.items);
  return (
    <ul className={`a2-list a2-list-${direction} a2-batchrows`}>
      {rows.map((row) => {
        const it = byN.get(row.n);
        const on = !!it && !batch.left.has(it.id);
        return (
          <li key={row.n} className={on ? "ticked" : "left"}>
            {it && card && <input type="checkbox" className="a2-tick" checked={on} onChange={() => batch.toggle(it.id)} aria-label={t("espace.batch.keep", it.title || it.id)} />}
            <div className="a2-tickrow">{openable(surface, list, lists, row.node, it, !!card)}</div>
          </li>
        );
      })}
    </ul>
  );
}
