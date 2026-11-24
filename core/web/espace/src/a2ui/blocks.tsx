import { useContext, useEffect, useId, useState, type ReactNode } from "react";
import { createComponentImplementation } from "@a2ui/react";
import { CommonSchemas } from "@a2ui/web_core/v1_0";
import { z } from "zod";
import type { ListRow, SourceList } from "../data/api";
import { useWords, type Lang } from "../i18n";
import { num, number, stamp } from "./format";
import { InlineYes } from "./inline";
import { useProposals } from "./proposals";
import { ListsContext, openable, useList, type Lists } from "./lists";
import { viaCore } from "./media";
import { cleanTrend, Spark } from "./spark";

// The blocks a model writes as one component (core/a2ui/catalog_v1.json):
// Sections, Figures, Gallery and Asks. The model says what; the page
// supplies the rest: groups, counts, units, layout on a phone (container
// queries in blocks.css), and the empty and reading states. A row's card,
// whether it opens, which item it is: what eriphe-core told of the
// source's rows (lists.tsx), never the block's data. Motion is CSS only,
// none under prefers-reduced-motion.

const str = (v: unknown) => (typeof v === "string" ? v : typeof v === "number" ? String(v) : "");
const TextOrNumber = z.union([z.string(), z.number(), CommonSchemas.DataBinding, CommonSchemas.FunctionCall]);
const listOr = <T extends z.ZodTypeAny>(item: T) => z.union([z.array(item), CommonSchemas.DataBinding]);
const Units = z.record(z.string(), z.string()).optional();
type Row = Record<string, unknown>;
const rowsOf = (v: unknown): Row[] | undefined => (Array.isArray(v) ? v.filter((r): r is Row => !!r && typeof r === "object" && !Array.isArray(r)) : undefined);

// A sender as people read it: "Sam <sam@example.org>" is Sam.
const nameOf = (from: string) => from.split(" <")[0].replace(/^"|"$/g, "").trim() || from;
function initials(name: string): string {
  const words = nameOf(name).split(/[\s.\-_]+/).filter((w) => /\p{L}/u.test(w));
  return (words.length > 1 ? words[0][0] + words[words.length - 1][0] : (words[0] ?? "?").slice(0, 1)).toUpperCase();
}
// A calm hue from a name, the same every time (5 tints).
function hue(name: string): number {
  let h = 0;
  for (const c of name) h = (h * 31 + c.charCodeAt(0)) % 5;
  return h;
}

// --- Units: one key, many looks -----------------------------------------

// shown writes a value with its unit: « € » an amount, « % » a share,
// « ±% » a signed change in ink, « ★ » a rating out of 5, any other word
// after the number. A value that is no number is written as it is.
export function shown(lang: Lang, v: unknown, unit: string | undefined, stars: (n: string) => string): ReactNode {
  const n = num(v);
  if (n === undefined || typeof v === "boolean") return Array.isArray(v) ? v.map(str).filter(Boolean).join(", ") : str(v);
  if (unit === "★") {
    const on = Math.max(0, Math.min(5, Math.round(n)));
    return (
      <span className="a2-stars" role="img" aria-label={stars(number(lang, n))}>
        {[1, 2, 3, 4, 5].map((i) => (
          <i key={i} className={i <= on ? "on" : undefined} />
        ))}
      </span>
    );
  }
  if (unit === "±%") return `${n > 0 ? "+" : n < 0 ? "−" : ""}${number(lang, Math.abs(n), 1)} %`;
  if (unit === "%" || unit === "€") return `${number(lang, n)} ${unit}`;
  return unit ? `${number(lang, n)} ${unit}` : number(lang, n);
}

// A block's own title, under the page's.
export function BlockTitle({ text, aside }: { text: string; aside?: ReactNode }) {
  if (!text && !aside) return null;
  return (
    <header className="a2-head">
      {text && <h3 className="a2-head-title">{text}</h3>}
      {aside}
    </header>
  );
}

// Reading: a skeleton shaped like the block, never written by a model.
function Reading({ kind }: { kind: "rows" | "tiles" }) {
  const { t } = useWords();
  return (
    <div className={`a2-sk a2-sk-${kind}`} role="status" aria-busy="true" aria-label={t("espace.blocks.reading")}>
      {[0, 1, 2, 3].map((i) => (
        <span key={i} />
      ))}
    </div>
  );
}

function Empty() {
  const { t } = useWords();
  return (
    <div className="a2-none">
      <span className="a2-none-ring" aria-hidden="true" />
      <p>{t("espace.blocks.empty")}</p>
    </div>
  );
}

// --- Figures ------------------------------------------------------------

const Figure = z.object({
  label: CommonSchemas.DynamicString,
  value: TextOrNumber.optional(),
  unit: CommonSchemas.DynamicString.optional(),
  delta: CommonSchemas.DynamicNumber.optional(),
  deltaLabel: CommonSchemas.DynamicString.optional(),
  trend: listOr(z.number()).optional(),
});

const Figures = createComponentImplementation(
  { name: "Figures", schema: z.object({ items: listOr(Figure), title: CommonSchemas.DynamicString.optional() }) },
  ({ props }) => {
    const { t, lang } = useWords();
    const items = (Array.isArray(props.items) ? props.items : []).filter((f): f is z.infer<typeof Figure> => !!f && typeof f === "object");
    return (
      <div className="a2-figs-block">
        <BlockTitle text={str(props.title)} />
        <div className={`a2-figs n${Math.min(items.length, 4)}`}>
          {items.map((f, i) => {
            const n = num(f.value);
            const value = n !== undefined && typeof f.value === "number" ? number(lang, n) : str(f.value);
            const unit = str(f.unit);
            // Its change in ink, and one sign of which way: ▲ up, ▼ down.
            const delta = num(f.delta);
            const size = delta === undefined ? "" : `${number(lang, Math.abs(delta))}${unit ? ` ${unit}` : ""}`;
            const said = delta === undefined ? "" : t(delta > 0 ? "espace.a2.delta.up" : delta < 0 ? "espace.a2.delta.down" : "espace.a2.delta.flat", size);
            const trend = cleanTrend(f.trend);
            return (
              <div className="a2-fig" key={i}>
                <span className="a2-fig-label">{str(f.label)}</span>
                {/* keyed by its value: a figure that moved comes in again, a
                    faint flash (blocks.css) */}
                <span className="a2-fig-value" key={value}>
                  {value || "—"}
                  {unit && <small> {unit}</small>}
                </span>
                {(delta !== undefined || f.deltaLabel) && (
                  <span className="a2-fig-delta">
                    {delta !== undefined && delta !== 0 && <b aria-hidden="true">{delta > 0 ? "▲" : "▼"} {size}</b>}
                    {said && <span className="sr-only">{said}</span>}
                    {f.deltaLabel ? ` ${str(f.deltaLabel)}` : ""}
                  </span>
                )}
                {trend.length > 1 && <Spark values={trend} />}
              </div>
            );
          })}
        </div>
      </div>
    );
  },
);

// --- Sections -----------------------------------------------------------

// A row of Sections: what eriphe-core told of the item (its id, title,
// detail, card), and what the item's note says (code's fields, written
// from item_note: _note, _state, _group).
type SecRow = { n: number; id: string; title: string; detail: string; note: string; state: string; group: string; card?: string; when: string; told?: ListRow };
type Group = { name: string; kind: "open" | "batch" | "folded"; rows: SecRow[] };

const ROWS_SHOWN = 6;

// rowsFrom are the rows of Sections: those eriphe-core told of, in the
// source's order, each with its item's note; without them (an older
// version), the data's items, with no card.
export function rowsFrom(data: Row[], told: SourceList | undefined): SecRow[] {
  const note = (r: Row | undefined) => ({ note: str(r?._note), state: str(r?._state), group: str(r?._group), when: str(r?.date) });
  if (told)
    return told.items.map((it) => ({ n: it.n, id: it.id, title: it.title || it.id, detail: it.detail ?? "", card: it.card, told: it, ...note(data[it.n]) }));
  return data.map((r, n) => ({ n, id: str(r.id) || String(n), title: str(r.title ?? r.subject ?? r.name), detail: str(r.from ?? r.detail), ...note(r) }));
}

// groupsOf sorts the rows into groups by code's fields: done rows leave;
// a group where something is to do (act, look) or a card waits opens
// first; a group whose rows all share one card is a batch, folded with
// its yes; the others fold. rest names the rows with no group.
export function groupsOf(rows: SecRow[], rest: string): Group[] {
  const by = new Map<string, SecRow[]>();
  for (const r of rows) {
    if (r.state === "done") continue;
    const g = r.group || rest;
    by.set(g, [...(by.get(g) ?? []), r]);
  }
  const rank = { open: 0, batch: 1, folded: 2 };
  return [...by]
    .map(([name, rs]): Group => {
      const card = rs[0].card;
      const batch = rs.length > 1 && !!card && rs.every((r) => r.card === card);
      const open = rs.some((r) => r.state === "act" || r.state === "look" || !!r.card);
      return { name, rows: rs, kind: batch ? "batch" : open ? "open" : "folded" };
    })
    .sort((a, b) => rank[a.kind] - rank[b.kind]);
}

function SecLine({ r, rich }: { r: SecRow; rich: boolean }) {
  const { lang } = useWords();
  const who = nameOf(r.detail);
  const when = stamp(lang, r.when);
  // Rich: Eriphe's note first, then who and what; folded: what, then who
  // and the note, quieter.
  const head = rich && r.note ? r.note : r.title;
  const sub = rich && r.note ? [who, r.title] : [who, r.note];
  return (
    <div className={`a2-sec-row${rich ? " rich" : ""}`}>
      {rich && (
        <span className={`a2-sec-av h${hue(who || r.title)}`} aria-hidden="true">
          {initials(who || r.title)}
        </span>
      )}
      <div className="a2-sec-main">
        <span className="a2-sec-title">{head}</span>
        {sub.some(Boolean) && (
          <span className="a2-sec-sub">
            {sub[0] && <b>{sub[0]}</b>}
            {sub[0] && sub[1] ? " · " : ""}
            {sub[1]}
          </span>
        )}
      </div>
      {when && <time className="a2-sec-when">{when}</time>}
    </div>
  );
}

type Told = { surface: string; list: SourceList; lists: Lists } | undefined;

function SecRows({ rows, rich, told, ticks }: { rows: SecRow[]; rich: boolean; told: Told; ticks?: { left: ReadonlySet<string>; toggle: (id: string) => void } }) {
  const { t } = useWords();
  const [all, setAll] = useState(false);
  const shownRows = all ? rows : rows.slice(0, ROWS_SHOWN);
  return (
    <ul className="a2-sec-rows">
      {shownRows.map((r) => {
        const line = <SecLine r={r} rich={rich} />;
        const node = told ? openable(told.surface, told.list, told.lists, line, r.told, !!ticks) : line;
        if (!ticks) return <li key={r.id}>{node}</li>;
        const on = !ticks.left.has(r.id);
        return (
          <li key={r.id} className={`a2-sec-tick${on ? "" : " left"}`}>
            <input type="checkbox" className="a2-tick" checked={on} onChange={() => ticks.toggle(r.id)} aria-label={t("espace.batch.keep", r.title)} />
            {node}
          </li>
        );
      })}
      {rows.length > ROWS_SHOWN && (
        <li className="a2-sec-more-li">
          <button type="button" className="linkish a2-sec-more" aria-expanded={all} onClick={() => setAll(!all)}>
            {all ? t("espace.blocks.less") : t("espace.blocks.more", rows.length - ROWS_SHOWN)}
          </button>
        </li>
      )}
    </ul>
  );
}

// A folded group, or a batch: one line, its count; a batch's line carries
// the yes of the card its rows share, and opened, when its card holds a
// call for each row (its choices), each row has its box, ticked: the rows
// unticked leave their calls out (InlineYes's left). One call over all the
// rows has no box: it cannot leave one out.
function SecFold({ g, told }: { g: Group; told: Told }) {
  const { t } = useWords();
  const id = useId();
  const src = useProposals();
  const [open, setOpen] = useState(false);
  const [left, setLeft] = useState<ReadonlySet<string>>(() => new Set());
  const [several, setSeveral] = useState(false);
  const batch = g.kind === "batch" && told ? g.rows[0].card : undefined;
  useEffect(() => {
    if (!batch) return;
    let live = true;
    src.read(batch).then(
      (p) => live && setSeveral((p?.choices?.length ?? 0) > 1),
      () => {},
    );
    return () => {
      live = false;
    };
  }, [src, batch]);
  const toggle = (item: string) =>
    setLeft((was) => {
      const next = new Set(was);
      if (next.has(item)) next.delete(item);
      else next.add(item);
      return next;
    });
  return (
    <div className={`a2-sec-fold${batch ? " batch" : ""}${open ? " open" : ""}`}>
      <div className="a2-sec-fold-line">
        <button type="button" className="a2-sec-fold-head" aria-expanded={open} aria-controls={id} title={t(open ? "espace.a2.fold.close" : "espace.a2.fold.open")} onClick={() => setOpen(!open)}>
          <span className="a2-sec-chev" aria-hidden="true" />
          <span className="a2-sec-fold-name">{g.name}</span>
          <span className="a2-count">{g.rows.length}</span>
          {left.size > 0 && <span className="a2-batch-kept">{t("espace.batch.kept", left.size)}</span>}
        </button>
        {batch && told && (
          <span className="a2-sec-fold-card">
            <InlineYes refText={batch} say left={left} where={{ surfaceId: told.surface, componentId: told.list.component }} />
          </span>
        )}
      </div>
      {open && (
        <div id={id} className="a2-sec-fold-body">
          <SecRows rows={g.rows} rich={false} told={told} ticks={batch && several ? { left, toggle } : undefined} />
        </div>
      )}
    </div>
  );
}

const Sections = createComponentImplementation(
  { name: "Sections", schema: z.object({ items: z.union([z.string(), CommonSchemas.DataBinding]), title: CommonSchemas.DynamicString.optional() }) },
  ({ props, context }) => {
    const { t } = useWords();
    const surface = context.dataContext.surface?.id;
    const told = useList(surface, context.componentModel.id);
    const data = rowsOf(props.items);
    const title = str(props.title);
    if (!data) return <Reading kind="rows" />;
    const groups = groupsOf(rowsFrom(data, told?.list), t("espace.blocks.rest"));
    const at: Told = told && surface ? { surface, list: told.list, lists: told.lists } : undefined;
    if (!groups.length)
      return (
        <div className="a2-secs">
          <BlockTitle text={title} />
          <Empty />
        </div>
      );
    const open = groups.filter((g) => g.kind === "open");
    return (
      <div className="a2-secs">
        <BlockTitle text={title} />
        {!open.length && <p className="a2-secs-clear">{t("espace.blocks.clear")}</p>}
        {open.map((g) => (
          <section className="a2-sec" key={g.name}>
            <h4 className="a2-sec-head">
              {g.name} <span className="a2-count">{g.rows.length}</span>
            </h4>
            <SecRows rows={g.rows} rich told={at} />
          </section>
        ))}
        {groups
          .filter((g) => g.kind !== "open")
          .map((g) => (
            <SecFold key={g.name} g={g} told={at} />
          ))}
      </div>
    );
  },
);

// --- Gallery ------------------------------------------------------------

const TILES_SHOWN = 8;
const IMAGE_FIELDS = ["photo", "image"]; // core/a2ui/paths.go's ImageFields

const imageOf = (r: Row) => str(r[IMAGE_FIELDS.find((k) => typeof r[k] === "string") ?? ""]);
const titleOf = (r: Row) => str(r.title ?? r.name ?? r.label);

const Gallery = createComponentImplementation(
  {
    name: "Gallery",
    schema: z.object({
      items: z.union([z.string(), z.array(z.record(z.string(), z.unknown())), CommonSchemas.DataBinding]),
      title: CommonSchemas.DynamicString.optional(),
      by: z.string().optional(),
      units: Units,
    }),
  },
  ({ props }) => {
    const { t, lang } = useWords();
    const lists = useContext(ListsContext);
    const [cat, setCat] = useState<string | null>(null);
    const [all, setAll] = useState(false);
    const rows = rowsOf(props.items);
    if (!rows) return <Reading kind="tiles" />;
    const title = str(props.title);
    if (!rows.length)
      return (
        <div className="a2-gal-block">
          <BlockTitle text={title} />
          <Empty />
        </div>
      );
    const by = props.by ?? "";
    const units: Record<string, string> = props.units ?? {};
    const cats = by ? [...new Set(rows.map((r) => str(r[by])).filter(Boolean))] : [];
    const picked = cat === null ? rows : rows.filter((r) => str(r[by]) === cat);
    const tiles = all ? picked : picked.slice(0, TILES_SHOWN);
    const stars = (n: string) => t("espace.blocks.stars", n);
    return (
      <div className="a2-gal-block">
        <BlockTitle text={title} aside={<span className="a2-count">{rows.length}</span>} />
        {cats.length > 1 && (
          <div className="a2-chips" role="group" aria-label={by}>
            {[null, ...cats].map((c) => (
              <button key={c ?? ""} type="button" className={`a2-chip${cat === c ? " on" : ""}`} aria-pressed={cat === c} onClick={() => setCat(c)}>
                {c ?? t("espace.blocks.all")}
                <small>{c === null ? rows.length : rows.filter((r) => str(r[by]) === c).length}</small>
              </button>
            ))}
          </div>
        )}
        <ul className={`a2-gal${rows.length <= 4 && !by ? " few" : ""}`}>
          {tiles.map((r, i) => {
            const src = imageOf(r);
            const name = titleOf(r);
            // A node of the graph (a node_find source's row) opens its page.
            const node = typeof r.id === "string" ? lists?.node?.(r.id) : undefined;
            const facts = Object.entries(units).filter(([k]) => r[k] !== undefined && r[k] !== null && r[k] !== "");
            const tile = (
              <figure className="a2-tile">
                <span className="a2-tile-img">{viaCore(src) ? <img src={src} alt={name} loading="lazy" decoding="async" /> : null}</span>
                <figcaption>
                  <span className="a2-tile-title">{name}</span>
                  {facts.length > 0 && (
                    <span className="a2-tile-facts">
                      {facts.map(([k, u]) => (
                        <span key={k}>{shown(lang, r[k], u, stars)}</span>
                      ))}
                    </span>
                  )}
                </figcaption>
              </figure>
            );
            return (
              <li key={str(r.id) || i}>
                {node && lists?.openNode ? (
                  <button type="button" className="a2-tile-open" onClick={() => lists.openNode!(node.id)}>
                    {tile}
                  </button>
                ) : (
                  tile
                )}
              </li>
            );
          })}
        </ul>
        {picked.length > TILES_SHOWN && (
          <button type="button" className="linkish a2-sec-more" aria-expanded={all} onClick={() => setAll(!all)}>
            {all ? t("espace.blocks.less") : t("espace.blocks.others", picked.length - TILES_SHOWN)}
          </button>
        )}
      </div>
    );
  },
);

// --- Asks ---------------------------------------------------------------

const Ask = z.union([z.string(), z.object({ text: z.string(), until: z.record(z.string(), z.string()).optional() })]);

// Asks are a node's likely answers, as buttons: a click asks Eriphe its
// words, which eriphe-core reads again in the stored block, by the
// component and the ask's place (« root#1 »: web_gestures.go); none acts.
// The first is the default. An ask whose until holds comes empty: code
// hid it (setup_buttons.go), and the others keep their place.
const Asks = createComponentImplementation({ name: "Asks", schema: z.object({ items: z.array(Ask) }) }, ({ props, context }) => {
  const lists = useContext(ListsContext);
  const surface = context.dataContext.surface?.id ?? "";
  const id = context.componentModel.id;
  const [sent, setSent] = useState(-1);
  const items = (props.items ?? []).map((it) => (typeof it === "string" ? it : str(it?.text)).trim());
  const first = items.findIndex(Boolean);
  if (first < 0) return null;
  return (
    <div className="a2-asks">
      {items.map((text, i) =>
        text ? (
          <button
            key={i}
            type="button"
            className={`btn a2-button a2-ask a2-button-${i === first ? "primary" : "default"}${sent === i ? " sent" : ""}`}
            onClick={() => {
              setSent(i);
              lists?.ask?.(surface, `${id}#${i}`);
            }}
          >
            {text}
          </button>
        ) : null,
      )}
    </div>
  );
});

export const blocks = [Sections, Figures, Gallery, Asks];
