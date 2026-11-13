import { useEffect, useId, useRef, useState, type KeyboardEvent, type ReactNode } from "react";
import { createComponentImplementation, type ReactComponentImplementation } from "@a2ui/react";
import { Catalog, CommonSchemas } from "@a2ui/web_core/v1_0";
import { z } from "zod";
import { useWords, type Lang } from "../i18n";
import { Chart as ChartView, cleanSeries } from "./chart";
import { Day as DayView, cleanEvents, type Ghost } from "./day";
import { functions, num, number, time, wall, when } from "./format";
import { Icon as IconView, ICON_NAMES } from "./icons";
import { Markdown as MarkdownView } from "./markdown";
import { useMoved, useProposals, type ProposalView } from "./proposals";

// The workspace's A2UI catalog, as the page renders it: shapes, never tools.
// eriphe-core owns the catalog (its JSON Schema, which validates what a
// model writes and tells it what it may use: core/a2ui/catalog_v1.json);
// the page implements each component with React, with exactly the
// catalog's properties, under the workspace's CSP: classes only, no style
// attribute, no <style>. A component's fixed words are the app's
// (web_text_espace.go), in the user's language.
//
// CATALOG is the one place that names the catalog: its id, which a
// surface's createSurface gives, and where eriphe-core serves its document.
export const CATALOG = {
  id: "urn:eriphe:a2ui:workspace:v1",
  url: "/espace/catalog/v1.json",
} as const;

const Justify = z.enum(["start", "center", "end", "spaceBetween"]).optional();
const Align = z.enum(["start", "center", "end", "stretch"]).optional();
const IconName = z.union([z.enum(ICON_NAMES as [string, ...string[]]), CommonSchemas.DataBinding]);
const TextOrNumber = z.union([z.string(), z.number(), CommonSchemas.DataBinding, CommonSchemas.FunctionCall]);
// A list given as is, or bound to the surface's data.
const listOr = <T extends z.ZodTypeAny>(item: T) => z.union([z.array(item), CommonSchemas.DataBinding]);

// A child list, once resolved: a component id, or the id and the data path
// of one instance of a template.
type Child = string | { id: string; basePath?: string };
type Build = (id: string, basePath?: string) => ReactNode;

function kids(children: unknown, build: Build): ReactNode[] {
  return (Array.isArray(children) ? (children as Child[]) : []).map((c) =>
    typeof c === "string" ? build(c) : build(c.id, c.basePath),
  );
}

const str = (v: unknown) => (typeof v === "string" ? v : typeof v === "number" ? String(v) : "");

// --- Layout -------------------------------------------------------------

const Text = createComponentImplementation(
  {
    name: "Text",
    schema: z.object({
      text: CommonSchemas.DynamicString,
      variant: z.enum(["title", "subtitle", "body", "caption"]).optional(),
    }),
  },
  ({ props }) => {
    const v = props.variant ?? "body";
    const cls = `a2-text a2-text-${v}`;
    // A title is a heading, under the tile's: assistive technology can
    // jump between them.
    if (v === "title") return <h3 className={cls}>{props.text}</h3>;
    if (v === "subtitle") return <h4 className={cls}>{props.text}</h4>;
    return <p className={cls}>{props.text}</p>;
  },
);

const box = (dir: "row" | "column") =>
  createComponentImplementation(
    {
      name: dir === "row" ? "Row" : "Column",
      schema: z.object({ children: CommonSchemas.ChildList, justify: Justify, align: Align }),
    },
    ({ props, buildChild }) => (
      <div className={`a2-${dir} a2-justify-${props.justify ?? "start"} a2-align-${props.align ?? "stretch"}`}>
        {kids(props.children, buildChild)}
      </div>
    ),
  );

const List = createComponentImplementation(
  {
    name: "List",
    schema: z.object({
      children: CommonSchemas.ChildList,
      direction: z.enum(["vertical", "horizontal"]).optional(),
      emptyText: CommonSchemas.DynamicString.optional(),
    }),
  },
  ({ props, buildChild }) => {
    const items = kids(props.children, buildChild);
    if (!items.length) return props.emptyText ? <p className="a2-empty">{props.emptyText}</p> : null;
    return (
      <ul className={`a2-list a2-list-${props.direction ?? "vertical"}`}>
        {items.map((item, i) => (
          <li key={i}>{item}</li>
        ))}
      </ul>
    );
  },
);

const Card = createComponentImplementation(
  {
    name: "Card",
    schema: z.object({ title: CommonSchemas.DynamicString.optional(), child: CommonSchemas.Child }),
  },
  ({ props, buildChild }) => {
    const id = useId();
    const title = str(props.title);
    return (
      <section className="a2-card" aria-labelledby={title ? id : undefined}>
        {title && (
          <h3 id={id} className="a2-card-title">
            {title}
          </h3>
        )}
        <div className="a2-card-body">{props.child ? buildChild(props.child) : null}</div>
      </section>
    );
  },
);

// Tabs follow the WAI-ARIA tabs pattern: one tab stop, arrows, Home, End;
// the panels not shown are hidden, not removed, so their data stays bound.
const Tabs = createComponentImplementation(
  {
    name: "Tabs",
    schema: z.object({
      tabs: z.array(z.object({ title: CommonSchemas.DynamicString, child: CommonSchemas.Child })).min(1),
    }),
  },
  ({ props, buildChild }) => {
    const base = useId();
    const tabs = props.tabs ?? [];
    const [sel, setSel] = useState(0);
    const refs = useRef<(HTMLButtonElement | null)[]>([]);
    const current = Math.min(sel, Math.max(0, tabs.length - 1));
    const go = (i: number) => {
      const n = (i + tabs.length) % tabs.length;
      setSel(n);
      refs.current[n]?.focus();
    };
    const onKey = (e: KeyboardEvent) => {
      const next = { ArrowRight: current + 1, ArrowLeft: current - 1, Home: 0, End: tabs.length - 1 }[e.key];
      if (next === undefined) return;
      e.preventDefault();
      go(next);
    };
    return (
      <div className="a2-tabs">
        <div className="a2-tablist" role="tablist" onKeyDown={onKey}>
          {tabs.map((tab, i) => (
            <button
              key={i}
              ref={(el) => {
                refs.current[i] = el;
              }}
              type="button"
              role="tab"
              id={`${base}-t${i}`}
              aria-controls={`${base}-p${i}`}
              aria-selected={i === current}
              tabIndex={i === current ? 0 : -1}
              className="a2-tab"
              onClick={() => setSel(i)}
            >
              {str(tab.title)}
            </button>
          ))}
        </div>
        {tabs.map((tab, i) => (
          <div key={i} role="tabpanel" id={`${base}-p${i}`} aria-labelledby={`${base}-t${i}`} hidden={i !== current} className="a2-tabpanel">
            {buildChild(tab.child)}
          </div>
        ))}
      </div>
    );
  },
);

const Divider = createComponentImplementation({ name: "Divider", schema: z.object({}) }, () => <hr className="a2-divider" />);

// --- Content ------------------------------------------------------------

const Markdown = createComponentImplementation(
  { name: "Markdown", schema: z.object({ text: CommonSchemas.DynamicString }) },
  ({ props }) => {
    const { t } = useWords();
    return <MarkdownView text={str(props.text)} newTab={t("espace.a2.newtab")} />;
  },
);

// An image is loaded only through eriphe-core (/espace/media, which fetches
// and checks it): a model's URL is never fetched by the page, so it cannot
// carry data out. Any other address shows what the image is, in words.
export function viaCore(url: string): boolean {
  try {
    const u = new URL(url, location.origin);
    return u.origin === location.origin && u.pathname === "/espace/media" && url.startsWith("/espace/media?");
  } catch {
    return false;
  }
}

const Image = createComponentImplementation(
  {
    name: "Image",
    schema: z.object({
      url: CommonSchemas.DynamicString,
      alt: CommonSchemas.DynamicString.optional(),
      fit: z.enum(["contain", "cover"]).optional(),
    }),
  },
  ({ props }) => {
    const { t } = useWords();
    const url = str(props.url), alt = str(props.alt);
    if (viaCore(url)) return <img className={`a2-image a2-fit-${props.fit ?? "contain"}`} src={url} alt={alt} loading="lazy" decoding="async" />;
    return (
      <div className="a2-image-off" role="img" aria-label={alt || t("espace.a2.image.off")}>
        <IconView name="document" />
        <span>{alt || t("espace.a2.image.off")}</span>
      </div>
    );
  },
);

const Icon = createComponentImplementation({ name: "Icon", schema: z.object({ name: IconName }) }, ({ props }) => <IconView name={props.name} />);

// --- Input --------------------------------------------------------------

const Button = createComponentImplementation(
  {
    name: "Button",
    schema: z.object({
      label: CommonSchemas.DynamicString,
      icon: IconName.optional(),
      variant: z.enum(["primary", "default", "quiet"]).optional(),
      action: CommonSchemas.Action,
    }),
  },
  ({ props }) => (
    <button type="button" className={`btn a2-button a2-button-${props.variant ?? "default"}`} onClick={() => props.action?.()}>
      {props.icon ? <IconView name={props.icon} /> : null}
      {props.label}
    </button>
  ),
);

// The fields write what the user does to the surface's data (the bound
// path), as A2UI does: an action sent later carries it.
type Setter<T> = { setValue?: (v: T) => void };

const Choice = createComponentImplementation(
  {
    name: "Choice",
    schema: z.object({
      label: CommonSchemas.DynamicString.optional(),
      options: z.array(z.object({ label: CommonSchemas.DynamicString, value: z.string() })).min(1),
      value: CommonSchemas.DynamicStringList,
      multiple: z.boolean().optional(),
    }),
  },
  ({ props }) => {
    const name = useId();
    const chosen = Array.isArray(props.value) ? (props.value as unknown[]).map(String) : [];
    const set = (props as Setter<string[]>).setValue;
    const pick = (v: string, on: boolean) =>
      set?.(props.multiple ? (on ? [...chosen.filter((x) => x !== v), v] : chosen.filter((x) => x !== v)) : [v]);
    const label = str(props.label);
    return (
      <fieldset className="a2-choice">
        {label && <legend className="a2-field-label">{label}</legend>}
        <div className="a2-options">
          {(props.options ?? []).map((o) => (
            <label key={o.value} className="a2-option">
              <input
                type={props.multiple ? "checkbox" : "radio"}
                name={name}
                value={o.value}
                checked={chosen.includes(o.value)}
                onChange={(e) => pick(o.value, e.target.checked)}
              />
              <span>{str(o.label)}</span>
            </label>
          ))}
        </div>
      </fieldset>
    );
  },
);

const TextField = createComponentImplementation(
  {
    name: "TextField",
    schema: z.object({
      label: CommonSchemas.DynamicString.optional(),
      value: CommonSchemas.DynamicString,
      placeholder: CommonSchemas.DynamicString.optional(),
      multiline: z.boolean().optional(),
    }),
  },
  ({ props }) => {
    const id = useId();
    const set = (props as Setter<string>).setValue;
    const common = {
      id,
      className: "a2-input",
      value: str(props.value),
      placeholder: str(props.placeholder) || undefined,
      "aria-label": props.label ? undefined : str(props.placeholder) || undefined,
      readOnly: !set,
    };
    return (
      <div className="a2-field">
        {props.label && <label className="a2-field-label" htmlFor={id}>{props.label}</label>}
        {props.multiline ? (
          <textarea {...common} rows={3} onChange={(e) => set?.(e.target.value)} />
        ) : (
          <input {...common} type="text" onChange={(e) => set?.(e.target.value)} />
        )}
      </div>
    );
  },
);

// What an <input> of each mode shows of an ISO 8601 value.
export function dateInput(mode: "date" | "time" | "dateTime", iso: string): string {
  const w = /^(\d{4}-\d{2}-\d{2})?(?:T?(\d{2}:\d{2}))?/.exec(iso);
  const d = w?.[1] ?? "", t = w?.[2] ?? "";
  if (mode === "date") return d;
  if (mode === "time") return t;
  return d && t ? `${d}T${t}` : d ? `${d}T00:00` : "";
}

const DateField = createComponentImplementation(
  {
    name: "DateField",
    schema: z.object({
      label: CommonSchemas.DynamicString.optional(),
      value: CommonSchemas.DynamicString,
      mode: z.enum(["date", "time", "dateTime"]).optional(),
    }),
  },
  ({ props }) => {
    const id = useId();
    const mode = props.mode ?? "date";
    const set = (props as Setter<string>).setValue;
    return (
      <div className="a2-field">
        {props.label && <label className="a2-field-label" htmlFor={id}>{props.label}</label>}
        <input
          id={id}
          className="a2-input"
          type={mode === "dateTime" ? "datetime-local" : mode}
          value={dateInput(mode, str(props.value))}
          readOnly={!set}
          onChange={(e) => set?.(e.target.value)}
        />
      </div>
    );
  },
);

// --- Data shapes --------------------------------------------------------

const Metric = createComponentImplementation(
  {
    name: "Metric",
    schema: z.object({
      label: CommonSchemas.DynamicString,
      value: TextOrNumber,
      unit: CommonSchemas.DynamicString.optional(),
      delta: CommonSchemas.DynamicNumber.optional(),
      deltaLabel: CommonSchemas.DynamicString.optional(),
      better: z.enum(["up", "down"]).optional(),
    }),
  },
  ({ props }) => {
    const { t, lang } = useWords();
    const n = typeof props.value === "number" ? props.value : undefined;
    const value = n !== undefined ? number(lang, n) : str(props.value);
    const unit = str(props.unit);
    const delta = num(props.delta);
    const better = props.better ?? "up";
    const mood = !delta ? "flat" : (delta > 0) === (better === "up") ? "good" : "bad";
    const size = delta === undefined ? "" : `${number(lang, Math.abs(delta))}${unit ? ` ${unit}` : ""}`;
    const deltaText = delta === undefined ? "" : `${delta > 0 ? "+" : delta < 0 ? "−" : "±"}${size}`;
    const said = delta === undefined ? "" : t(delta > 0 ? "espace.a2.delta.up" : delta < 0 ? "espace.a2.delta.down" : "espace.a2.delta.flat", size);
    return (
      <div className="a2-metric">
        <p className="a2-metric-label">{props.label}</p>
        <p className="a2-metric-value">
          {value}
          {unit && <span className="a2-metric-unit"> {unit}</span>}
        </p>
        {delta !== undefined && (
          <p className={`a2-delta a2-delta-${mood}`}>
            <IconView name={delta > 0 ? "trendUp" : delta < 0 ? "trendDown" : "check"} />
            <span aria-hidden="true">{deltaText}</span>
            <span className="sr-only">{said}</span>
            {props.deltaLabel && <span className="a2-delta-label"> {props.deltaLabel}</span>}
          </p>
        )}
      </div>
    );
  },
);

const Point = z.object({ x: z.union([z.string(), z.number()]), y: z.number() });
const Series = z.object({ name: z.string(), points: z.array(Point) });

const Chart = createComponentImplementation(
  {
    name: "Chart",
    schema: z.object({
      kind: z.enum(["line", "bar"]),
      series: listOr(Series),
      unit: CommonSchemas.DynamicString.optional(),
    }),
  },
  ({ props }) => {
    const { t, lang } = useWords();
    const series = cleanSeries(props.series);
    const names = series.map((s) => s.name).filter(Boolean).join(", ");
    const label = t(props.kind === "bar" ? "espace.a2.chart.bar" : "espace.a2.chart.line", names || "—");
    if (!series.some((s) => s.points.length)) return <p className="a2-empty">{t("espace.a2.chart.empty")}</p>;
    return <ChartView kind={props.kind} series={series} unit={str(props.unit) || undefined} lang={lang} label={label} />;
  },
);

const TableColumn = z.object({ key: z.string(), label: z.string(), kind: z.enum(["text", "number", "date"]).optional() });

function cell(lang: Lang, kind: string | undefined, v: unknown): string {
  if (v === null || v === undefined) return "";
  if (kind === "number") {
    const n = num(v);
    return n === undefined ? str(v) : number(lang, n);
  }
  if (kind === "date") {
    const w = wall(v);
    return w ? when(lang, w) : str(v);
  }
  return typeof v === "object" ? "" : String(v);
}

const Table = createComponentImplementation(
  {
    name: "Table",
    schema: z.object({
      columns: z.array(TableColumn).min(1),
      rows: listOr(z.record(z.string(), z.unknown())),
      emptyText: CommonSchemas.DynamicString.optional(),
    }),
  },
  ({ props }) => {
    const { t, lang } = useWords();
    const rows = (Array.isArray(props.rows) ? props.rows : []).filter((r): r is Record<string, unknown> => !!r && typeof r === "object");
    const cols = props.columns ?? [];
    if (!rows.length) return <p className="a2-empty">{str(props.emptyText) || t("espace.a2.table.empty")}</p>;
    return (
      <div className="a2-table-wrap">
        <table className="a2-table">
          <thead>
            <tr>
              {cols.map((c) => (
                <th key={c.key} scope="col" className={`a2-col-${c.kind ?? "text"}`}>{c.label}</th>
              ))}
            </tr>
          </thead>
          <tbody>
            {rows.map((r, i) => (
              <tr key={i}>
                {cols.map((c) => (
                  <td key={c.key} className={`a2-col-${c.kind ?? "text"}`}>{cell(lang, c.kind, r[c.key])}</td>
                ))}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    );
  },
);

const TimelineItem = z.object({
  label: z.string(),
  state: z.enum(["done", "current", "pending", "failed"]),
  date: z.string().optional(),
  detail: z.string().optional(),
});
const STATES = ["done", "current", "pending", "failed"];

const Timeline = createComponentImplementation(
  { name: "Timeline", schema: z.object({ items: listOr(TimelineItem) }) },
  ({ props }) => {
    const { t, lang } = useWords();
    const items = (Array.isArray(props.items) ? props.items : []).filter(
      (it): it is z.infer<typeof TimelineItem> => !!it && typeof it === "object" && typeof it.label === "string",
    );
    return (
      <ol className="a2-timeline">
        {items.map((it, i) => {
          const state = STATES.includes(it.state) ? it.state : "pending";
          const w = wall(it.date);
          return (
            <li key={i} className={`a2-step a2-step-${state}`} aria-current={state === "current" ? "step" : undefined}>
              <span className="a2-step-dot" aria-hidden="true">
                {state === "done" ? <IconView name="check" /> : state === "failed" ? <IconView name="close" /> : null}
              </span>
              <span className="a2-step-body">
                <span className="a2-step-label">{it.label}</span>
                <span className="sr-only">, {t(`espace.a2.step.${state}`)}</span>
                {w && <span className="a2-step-date"> {when(lang, w)}</span>}
                {it.detail && <span className="a2-step-detail">{it.detail}</span>}
              </span>
            </li>
          );
        })}
      </ol>
    );
  },
);

// A sender as people read it: "Sam <sam@example.org>" is Sam.
export const nameOf = (from: string) => from.split(" <")[0].replace(/^"|"$/g, "").trim() || from;

// When a message came: its time today, else its date (and time).
function sent(lang: Lang, iso: string, now = new Date()): string {
  const w = wall(iso);
  if (!w) return "";
  const today = [now.getFullYear(), now.getMonth() + 1, now.getDate()].map((n) => String(n).padStart(2, "0")).join("-");
  return w.time && iso.slice(0, 10) === today ? time(lang, w) : when(lang, w, now);
}

// Message has a short form (the excerpt) and a full one (the body), which
// the user opens in place.
const Message = createComponentImplementation(
  {
    name: "Message",
    schema: z.object({
      from: CommonSchemas.DynamicString,
      date: CommonSchemas.DynamicString.optional(),
      subject: CommonSchemas.DynamicString.optional(),
      excerpt: CommonSchemas.DynamicString.optional(),
      body: CommonSchemas.DynamicString.optional(),
    }),
  },
  ({ props }) => {
    const { t, lang } = useWords();
    const [open, setOpen] = useState(false);
    const id = useId();
    const from = nameOf(str(props.from));
    const date = str(props.date);
    const body = str(props.body), excerpt = str(props.excerpt);
    return (
      <article className={`a2-message${open ? " a2-message-open" : ""}`} aria-labelledby={`${id}-s`}>
        <span className="a2-avatar" aria-hidden="true">{from.slice(0, 1).toUpperCase()}</span>
        <div className="a2-message-main">
          <p className="a2-message-head">
            <span className="a2-message-from">{from}</span>
            {date && (
              <time className="a2-message-date" dateTime={date}>
                {sent(lang, date)}
              </time>
            )}
          </p>
          <p id={`${id}-s`} className="a2-message-subject">{str(props.subject) || from}</p>
          {open && body ? (
            <p id={`${id}-b`} className="a2-message-body">{body}</p>
          ) : (
            excerpt && <p className="a2-message-excerpt">{excerpt}</p>
          )}
          {body && (
            <button type="button" className="a2-more" aria-expanded={open} aria-controls={open ? `${id}-b` : undefined} onClick={() => setOpen(!open)}>
              {t(open ? "espace.a2.message.less" : "espace.a2.message.more")}
            </button>
          )}
        </div>
      </article>
    );
  },
);

const Day = createComponentImplementation(
  {
    name: "Day",
    schema: z.object({
      date: CommonSchemas.DynamicString,
      events: listOr(
        z.object({ title: z.string(), start: z.string(), end: z.string().optional(), place: z.string().optional(), allDay: z.boolean().optional() }),
      ),
      proposals: z.boolean().optional(),
    }),
  },
  ({ props }) => {
    const { t, lang } = useWords();
    const date = str(props.date);
    const ghosts = useGhosts(date.slice(0, 10), props.proposals !== false);
    return <DayView date={date} events={cleanEvents(props.events)} ghosts={ghosts} lang={lang} t={t} />;
  },
);

// useGhosts are the proposals that wait on a day, as ghosts: read from
// eriphe-core, and again whenever a proposal moved (one made, one
// answered anywhere).
function useGhosts(date: string, show: boolean): Ghost[] {
  const src = useProposals();
  const moved = useMoved(src, show ? null : []);
  const [ghosts, setGhosts] = useState<Ghost[]>([]);
  useEffect(() => {
    if (!show || !/^\d{4}-\d{2}-\d{2}$/.test(date)) {
      setGhosts([]);
      return;
    }
    let live = true;
    src.day(date).then(
      (ps) => live && setGhosts(ghostsOf(ps)),
      () => {},
    );
    return () => {
      live = false;
    };
  }, [src, date, show, moved]);
  return ghosts;
}

// ghostsOf: one ghost per time of a pending proposal.
export function ghostsOf(ps: ProposalView[]): Ghost[] {
  return ps
    .filter((p) => p.state === "pending")
    .flatMap((p) => (p.when ?? []).map((w) => ({ ref: p.ref, title: p.text, start: w.start, end: w.end, allDay: w.allDay, tainted: p.tainted })));
}

// onlyAsks tells whether a surface shows external content (createSurface's
// metadata, extensions.eriphe.tainted) and was written by a model: it can
// only ask. Code's own (author "core", the attention queue's) shows a
// proposal as its card does, with the proposal's own taint.
type Meta = { metadata?: { extensions?: { eriphe?: { tainted?: boolean; author?: string } } } };
const onlyAsks = (s: unknown) => {
  const m = (s as Meta | undefined)?.metadata?.extensions?.eriphe;
  return Boolean(m?.tainted) && m?.author !== "core";
};

// KEEPS are the kinds of proposals that only ask whether to keep something
// (a memory, a reminder): yes runs nothing. Their header asks it and their
// buttons say Garder and Oublier; any other kind does something once
// accepted: Proposition, Oui and Non.
const KEEPS: Record<string, string> = { memory: "espace.a2.proposal.memory", intention: "espace.a2.proposal.intention" };

// decidedWord is the header of a decided proposal: kept or forgotten for
// what only asked to keep, approved or declined otherwise.
function decidedWord(state: string, keeps: boolean): string {
  if (state !== "approved" && state !== "rejected") return "espace.a2.proposal.gone";
  if (keeps) return state === "approved" ? "espace.a2.proposal.kept" : "espace.a2.proposal.forgotten";
  return `espace.a2.proposal.${state}`;
}

// Proposal shows one proposal of Eriphe's, read from eriphe-core by its ref
// (kind:id), and again when it moved; its Oui and Non send an "answer" action naming this surface,
// this component and that proposal: eriphe-core decides it only if the
// surface shows it and it still waits, as its card in the conversation. A
// surface showing external content can only ask: there, no Oui or Non, the
// proposal is answered in the conversation.
const Proposal = createComponentImplementation(
  { name: "Proposal", schema: z.object({ proposalId: CommonSchemas.DynamicString }) },
  ({ props, context }) => {
    const { t } = useWords();
    const src = useProposals();
    const ref = str(props.proposalId);
    const [p, setP] = useState<ProposalView | null | undefined>(undefined);
    const [busy, setBusy] = useState(false);
    const [failed, setFailed] = useState(false);
    useEffect(() => {
      let live = true;
      setP(undefined);
      src.read(ref).then(
        (v) => live && setP(v),
        () => live && setP(null),
      );
      return () => {
        live = false;
      };
    }, [src, ref]);
    // Answered elsewhere (its card, a channel, another surface): read again.
    const moved = useMoved(src, [ref]);
    useEffect(() => {
      if (!moved) return;
      let live = true;
      src.read(ref).then(
        (v) => live && setP(v),
        () => {},
      );
      return () => {
        live = false;
      };
    }, [src, ref, moved]);
    const answer = async (yes: boolean) => {
      if (!p || busy) return;
      setBusy(true);
      setFailed(false);
      const surfaceId = context.dataContext.surface?.id ?? "";
      const ok = await src.answer({ surfaceId, componentId: context.componentModel.id, ref: p.ref, yes }).catch(() => false);
      if (!ok) setFailed(true);
      const again = await src.read(ref).catch(() => null);
      setP(again ?? { ...p, state: ok ? (yes ? "approved" : "rejected") : p.state });
      setBusy(false);
    };
    if (p === undefined) return <div className="a2-proposal a2-proposal-loading" aria-busy="true"><span className="a2-shimmer" aria-hidden="true" /></div>;
    if (p === null) return <p className="a2-empty">{t("espace.a2.proposal.none")}</p>;
    const pending = p.state === "pending";
    const elsewhere = onlyAsks(context.dataContext.surface);
    const keeps = KEEPS[p.kind ?? ""];
    return (
      <section className={`a2-proposal a2-proposal-${pending ? "pending" : "decided"}`} aria-label={t(keeps ?? "espace.a2.proposal")}>
        <p className="a2-proposal-kicker">
          <IconView name={pending ? "star" : p.state === "approved" ? "check" : "close"} />
          {pending ? t(keeps ?? "espace.a2.proposal") : t(decidedWord(p.state, Boolean(keeps)))}
        </p>
        <p className="a2-proposal-text">{p.text}</p>
        {p.why && <p className="a2-proposal-why">{p.why}</p>}
        {p.tainted && <p className="a2-proposal-taint">{t("espace.a2.proposal.tainted")}</p>}
        {p.fields && p.fields.length > 0 && (
          <dl className="a2-proposal-fields">
            {p.fields.map((f) => (
              <div key={f.name}>
                <dt>{f.name}</dt>
                <dd>{f.value}</dd>
              </div>
            ))}
          </dl>
        )}
        {pending && elsewhere && <p className="a2-proposal-why a2-proposal-inchat">{t("espace.a2.proposal.inchat")}</p>}
        {pending && !elsewhere && (
          <div className="a2-proposal-actions">
            <button type="button" className="btn a2-button a2-button-primary" disabled={busy} onClick={() => answer(true)}>
              {t(keeps ? "espace.a2.keep" : "espace.a2.yes")}
            </button>
            <button type="button" className="btn a2-button" disabled={busy} onClick={() => answer(false)}>
              {t(keeps ? "espace.a2.forget" : "espace.a2.no")}
            </button>
          </div>
        )}
        {failed && <p className="a2-proposal-failed" role="alert">{t("espace.a2.proposal.failed")}</p>}
      </section>
    );
  },
);

const ALERT_ICON = { info: "info", success: "check", warning: "warning", error: "error" } as const;

const Alert = createComponentImplementation(
  {
    name: "Alert",
    schema: z.object({
      level: z.enum(["info", "success", "warning", "error"]).optional(),
      title: CommonSchemas.DynamicString.optional(),
      text: CommonSchemas.DynamicString,
    }),
  },
  ({ props }) => {
    const { t } = useWords();
    const level = props.level ?? "info";
    const urgent = level === "warning" || level === "error";
    return (
      <div className={`a2-alert a2-alert-${level}`} role={urgent ? "alert" : "status"}>
        <IconView name={ALERT_ICON[level]} />
        <div>
          <span className="sr-only">{t(`espace.a2.level.${level}`)}, </span>
          {props.title && <p className="a2-alert-title">{props.title}</p>}
          <p className="a2-alert-text">{props.text}</p>
        </div>
      </div>
    );
  },
);

// Placeholder holds a surface's place while its author prepares it. (The
// engine's own placeholder, a child not arrived yet, is another thing:
// Surfaces.tsx shows nothing for it.)
const Placeholder = createComponentImplementation(
  { name: "Placeholder", schema: z.object({ text: CommonSchemas.DynamicString.optional() }) },
  ({ props }) => {
    const { t } = useWords();
    return (
      <div className="a2-placeholder" role="status" aria-busy="true">
        <p className="a2-placeholder-text">{str(props.text) || t("espace.a2.preparing")}</p>
        <span className="a2-shimmer" aria-hidden="true" />
        <span className="a2-shimmer a2-shimmer-short" aria-hidden="true" />
      </div>
    );
  },
);

export const components: ReactComponentImplementation[] = [
  Text,
  Markdown,
  Image,
  Icon,
  box("row"),
  box("column"),
  List,
  Card,
  Tabs,
  Divider,
  Button,
  Choice,
  TextField,
  DateField,
  Metric,
  Chart,
  Table,
  Timeline,
  Message,
  Day,
  Proposal,
  Alert,
  Placeholder,
];

// catalog is the catalog with its functions in the user's language.
export const catalog = (lang: Lang = "fr") => new Catalog(CATALOG.id, "1.0", components, functions(lang));
