import { useContext, useEffect, useId, useRef, useState, type KeyboardEvent, type ReactNode } from "react";
import { createComponentImplementation, type ReactComponentImplementation } from "@a2ui/react";
import { Catalog, CommonSchemas } from "@a2ui/web_core/v1_0";
import { z } from "zod";
import { useWords, type Lang } from "../i18n";
import { Chart as ChartView, cleanSeries } from "./chart";
import { Code as CodeView } from "./code";
import { Agenda as AgendaView, AGENDA_DAYS, addDays, cleanEvents, today, type AgendaCards, type Ghost } from "./agenda";
import { useNow } from "./now";
import { functions, num, number, time, wall, when } from "./format";
import { Icon as IconView, ICON_NAMES } from "./icons";
import { Markdown as MarkdownView } from "./markdown";
import { BatchContext, ListsContext, sharedCard, SourceRows, useList, type Row } from "./lists";
import { NodeTitle } from "../graph/Title";
import { InlineYes } from "./inline";
import { cleanTrend, Spark } from "./spark";
import { useMoved, useProposals, type ProposalView } from "./proposals";
import { viaCore } from "./media";
import { blocks, BlockTitle, shown } from "./blocks";

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
      variant: z.enum(["title", "subtitle", "body", "caption", "mark"]).optional(),
    }),
  },
  ({ props }) => {
    const v = props.variant ?? "body";
    const cls = `a2-text a2-text-${v}`;
    // An empty text draws nothing: a row's heading or mark bound to a
    // field only some rows fill.
    if (props.text === "" || props.text == null) return null;
    // A title is a heading, under the page's: assistive technology can
    // jump between them.
    if (v === "title") return <h3 className={cls}>{props.text}</h3>;
    if (v === "subtitle") return <h4 className={cls}>{props.text}</h4>;
    // A mark: a short word attached to what follows it (« extérieur »).
    if (v === "mark") return <span className={cls}>{props.text}</span>;
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
  ({ props, buildChild, context }) => {
    // A list of a source's items eriphe-core told of (lists.tsx): one
    // that filters them shows only the rows it kept (a section); a row a
    // card waits on carries its Oui; one whose source opens its items
    // opens a row in place (opener.tsx).
    const surface = context.dataContext.surface?.id;
    const told = useList(surface, context.componentModel.id);
    let rows: Row[] = kids(props.children, buildChild).map((node, n) => ({ n, node }));
    if (told?.list.filtered) {
      const kept = new Set(told.list.items.map((it) => it.n));
      rows = rows.filter((r) => kept.has(r.n));
    }
    if (!rows.length) return props.emptyText ? <p className="a2-empty">{props.emptyText}</p> : null;
    if (told && surface) return <SourceRows surface={surface} list={told.list} lists={told.lists} rows={rows} direction={props.direction ?? "vertical"} />;
    return (
      <ul className={`a2-list a2-list-${props.direction ?? "vertical"}`}>
        {rows.map((r) => (
          <li key={r.n}>{r.node}</li>
        ))}
      </ul>
    );
  },
);

// Grid is a gallery: as many cells to a row as fit, each as wide as its
// size says, all the same.
const Grid = createComponentImplementation(
  {
    name: "Grid",
    schema: z.object({
      children: CommonSchemas.ChildList,
      size: z.enum(["small", "medium", "large"]).optional(),
      emptyText: CommonSchemas.DynamicString.optional(),
    }),
  },
  ({ props, buildChild }) => {
    const items = kids(props.children, buildChild);
    if (!items.length) return props.emptyText ? <p className="a2-empty">{props.emptyText}</p> : null;
    return (
      <ul className={`a2-cells a2-cells-${props.size ?? "medium"}`}>
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

// Fold is a line that opens in place: what it sums up (« 4 étapes faites
// »), and under it its child, drawn only when open. Its aside stays on its
// line, open or not (a button that asks).
const Fold = createComponentImplementation(
  {
    name: "Fold",
    schema: z.object({
      label: CommonSchemas.DynamicString,
      child: CommonSchemas.Child,
      aside: CommonSchemas.Child.optional(),
      open: CommonSchemas.DynamicBoolean.optional(),
      list: z.string().optional(),
    }),
  },
  ({ props, buildChild, context }) => {
    // A batch: the List it names, as eriphe-core told of it (lists.tsx).
    const surface = context.dataContext.surface?.id;
    const told = useList(surface, props.list ?? "");
    if (props.list && told && surface) return <BatchFold surface={surface} told={told} label={str(props.label)} open={props.open === true} child={props.child ? buildChild(props.child) : null} />;
    return <FoldView label={str(props.label).replace(/\{n\}\s*/, "")} open={props.open === true} aside={props.aside ? buildChild(props.aside) : null} child={props.child ? buildChild(props.child) : null} />;
  },
);

function FoldView({ label, open: first, aside, child }: { label: string; open: boolean; aside: ReactNode; child: ReactNode }) {
  // A plain Fold: its line, its aside, its child once opened.
  const { t } = useWords();
  const id = useId();
  const [open, setOpen] = useState(first);
  const toggle = (
    <button type="button" className="a2-fold-head" aria-expanded={open} aria-controls={id} title={t(open ? "espace.a2.fold.close" : "espace.a2.fold.open")} onClick={() => setOpen(!open)}>
      <span className="a2-fold-label">{label}</span>
      <span className="a2-fold-chev" aria-hidden="true">{open ? "▾" : "▸"}</span>
    </button>
  );
  return (
    <div className={`a2-fold${open ? " a2-fold-open" : ""}`}>
      {aside ? (
        <div className="a2-fold-line">
          {toggle}
          <div className="a2-fold-aside">{aside}</div>
        </div>
      ) : (
        toggle
      )}
      {open && (
        <div id={id} className="a2-fold-body">
          {child}
        </div>
      )}
    </div>
  );
}

// BatchFold is a Fold that names a List of a source's items: its line says
// how many rows the List has ({n} in its label) and carries the Oui of the
// card its rows share (« Mettre 38 mails à la corbeille »); opened, the
// List shows each row with its box, all ticked (BatchRows), and the user
// unticks what they keep: the Oui then leaves their calls out, and says
// how many go. Rows with cards of their own carry each its Oui. Empty,
// nothing shows: what is settled leaves the page.
function BatchFold({ surface, told, label, open: first, child }: { surface: string; told: NonNullable<ReturnType<typeof useList>>; label: string; open: boolean; child: ReactNode }) {
  const { t } = useWords();
  const { list } = told;
  const id = useId();
  const [open, setOpen] = useState(first);
  const [left, setLeft] = useState<ReadonlySet<string>>(() => new Set());
  const items = list.items;
  const card = sharedCard(items);
  const toggle = (itemId: string) =>
    setLeft((was) => {
      const next = new Set(was);
      if (next.has(itemId)) next.delete(itemId);
      else next.add(itemId);
      return next;
    });
  if (!items.length) return null;
  return (
    <BatchContext.Provider value={{ list: list.component, left, toggle }}>
      <div className={`a2-fold a2-batch${open ? " a2-fold-open" : ""}`}>
        <div className="a2-fold-line">
          <button type="button" className="a2-fold-head" aria-expanded={open} aria-controls={id} title={t(open ? "espace.a2.fold.close" : "espace.a2.fold.open")} onClick={() => setOpen(!open)}>
            <span className="a2-fold-chev" aria-hidden="true">{open ? "▾" : "▸"}</span>
            <span className="a2-fold-label">{label.replace("{n}", String(items.length))}</span>
            {left.size > 0 && <span className="a2-batch-kept">{t("espace.batch.kept", left.size)}</span>}
          </button>
          {card && (
            <span className="a2-fold-card">
              <InlineYes refText={card} say left={left} where={{ surfaceId: surface, componentId: list.component }} />
            </span>
          )}
        </div>
        {open && (
          <div id={id} className="a2-fold-body">
            {child}
          </div>
        )}
      </div>
    </BatchContext.Provider>
  );
}

// --- Content ------------------------------------------------------------

const Markdown = createComponentImplementation(
  { name: "Markdown", schema: z.object({ text: CommonSchemas.DynamicString }) },
  ({ props }) => {
    const { t } = useWords();
    return <MarkdownView text={str(props.text)} newTab={t("espace.a2.newtab")} />;
  },
);

export { viaCore };

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

// linkTo is the address a Link may open, parsed once: http(s), a host, no
// user or password, nothing a reader could misread; null otherwise.
// eriphe-core checked it (a2ui.LinkURLOK); the page checks again, and both
// the href and the host it shows come from this one parse.
export function linkTo(raw: string): URL | null {
  if (!raw || raw.length > 2048 || /[\s\\\u0000-\u001f\u007f-\u009f]/.test(raw)) return null;
  try {
    const u = new URL(raw);
    if ((u.protocol !== "http:" && u.protocol !== "https:") || u.username || u.password || !u.hostname) return null;
    return u;
  } catch {
    return null;
  }
}

// A Link opens a page outside, in a new tab, on the user's click only:
// nothing goes with it (no opener, no referrer). Its address shows in
// clear: its host beside the label, or the whole address when there is no
// label or when the surface shows external content (a model that read it
// could have written the address: the user sees all of it before a click).
// Its url is never computed (core/a2ui/links.go): a literal or a binding.
const Link = createComponentImplementation(
  {
    name: "Link",
    schema: z.object({
      url: z.union([z.string(), CommonSchemas.DataBinding]),
      label: CommonSchemas.DynamicString.optional(),
    }),
  },
  ({ props, context }) => {
    const { t } = useWords();
    const label = str(props.label);
    const u = linkTo(str(props.url));
    if (!u) return <span className="a2-link a2-link-off" title={t("espace.a2.link.off")}>{label || t("espace.a2.link.off")}</span>;
    const whole = !label || external(context.dataContext.surface);
    return (
      <a className="a2-link" href={u.href} target="_blank" rel="noopener noreferrer" referrerPolicy="no-referrer" title={u.href}>
        <IconView name="link" />
        {label && <span className="a2-link-label">{label}</span>}
        <span className="a2-link-host">{whole ? u.href : u.host}</span>
        <span className="sr-only"> {t("espace.a2.newtab")}</span>
      </a>
    );
  },
);

const Code = createComponentImplementation(
  {
    name: "Code",
    schema: z.object({
      text: CommonSchemas.DynamicString,
      language: z.string().optional(),
      mode: z.enum(["code", "diff"]).optional(),
    }),
  },
  ({ props }) => <CodeView text={str(props.text)} language={props.language ?? ""} mode={props.mode ?? "code"} />,
);

// --- Input --------------------------------------------------------------

// Button only asks Eriphe: its author's words, sent as the user's message
// (a2ui/actions.go). It never acts: a yes is a card, which code draws.
const Button = createComponentImplementation(
  {
    name: "Button",
    schema: z.object({
      label: CommonSchemas.DynamicString,
      icon: IconName.optional(),
      variant: z.enum(["primary", "default", "quiet"]).optional(),
      action: CommonSchemas.Action,
      // What it sets up: eriphe-core hides it once that holds
      // (core/setup_buttons.go); the page never sees one that did.
      until: z.object({ tool: z.string(), monitor: z.string(), skill: z.string() }).partial().optional(),
    }),
  },
  ({ props }) => (
    <button type="button" className={`btn a2-button a2-button-${props.variant ?? "default"}`} onClick={() => props.action?.()}>
      {props.icon ? <IconView name={props.icon} /> : null}
      {props.label}
    </button>
  ),
);

// external tells whether a surface shows external content, whoever wrote it.
type Meta = { metadata?: { extensions?: { eriphe?: { tainted?: boolean; author?: string } } } };
const external = (s: unknown) => Boolean((s as Meta | undefined)?.metadata?.extensions?.eriphe?.tainted);

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
      value: TextOrNumber.optional(),
      list: z.string().optional(),
      unit: CommonSchemas.DynamicString.optional(),
      delta: CommonSchemas.DynamicNumber.optional(),
      deltaLabel: CommonSchemas.DynamicString.optional(),
      trend: listOr(z.number()).optional(),
    }),
  },
  ({ props, context }) => {
    const { t, lang } = useWords();
    const trend = cleanTrend(props.trend);
    // A figure that counts a List's rows, as eriphe-core told of them: its
    // filters applied, and again at each read of its source (lists.tsx).
    const told = useList(context.dataContext.surface?.id, props.list ?? "");
    const n = props.list ? (told ? told.list.items.length : undefined) : typeof props.value === "number" ? props.value : undefined;
    const value = n !== undefined ? number(lang, n) : props.list ? "—" : str(props.value);
    const unit = str(props.unit);
    // Its change in ink, and one sign of which way: ▲ up, ▼ down, none
    // when it did not move. Colour says a state, never a change.
    const delta = num(props.delta);
    const size = delta === undefined ? "" : `${number(lang, Math.abs(delta))}${unit ? ` ${unit}` : ""}`;
    const sign = !delta ? "" : delta > 0 ? "▲" : "▼";
    const said = delta === undefined ? "" : t(delta > 0 ? "espace.a2.delta.up" : delta < 0 ? "espace.a2.delta.down" : "espace.a2.delta.flat", size);
    return (
      <div className="a2-metric">
        <p className="a2-metric-label">{props.label}</p>
        <p className="a2-metric-value">
          {value}
          {unit && <span className="a2-metric-unit"> {unit}</span>}
        </p>
        {delta !== undefined && (
          <p className="a2-delta">
            {sign && (
              <span className="a2-delta-sign" aria-hidden="true">
                {sign}
              </span>
            )}
            <span aria-hidden="true">{size}</span>
            <span className="sr-only">{said}</span>
            {props.deltaLabel && <span className="a2-delta-label"> {props.deltaLabel}</span>}
          </p>
        )}
        {trend.length > 1 && <Spark values={trend} />}
      </div>
    );
  },
);

// Progress says where a thing stands: its state word in a pill, coloured
// by its tone, and when it has steps a bar of them, done, running, to come.
const TONES = ["default", "urgent", "success", "muted"] as const;
const PROGRESS_MAX = 40; // segments drawn at most

const Progress = createComponentImplementation(
  {
    name: "Progress",
    schema: z.object({
      label: CommonSchemas.DynamicString,
      tone: z.enum(TONES).optional(),
      value: CommonSchemas.DynamicNumber.optional(),
      current: CommonSchemas.DynamicNumber.optional(),
      total: CommonSchemas.DynamicNumber.optional(),
      step: CommonSchemas.DynamicString.optional(),
    }),
  },
  ({ props }) => {
    // Its word and, quieter, how far (« 3 sur 5 ») and the step it is at;
    // under them, a bar of its steps: done in ink, the one in progress
    // half drawn, those to come a track. Its tone colours the word only.
    const { t } = useWords();
    const total = Math.max(0, Math.min(PROGRESS_MAX, Math.round(num(props.total) ?? 0)));
    const done = Math.max(0, Math.min(total, Math.round(num(props.value) ?? 0)));
    const current = Math.max(0, Math.min(total - done, Math.round(num(props.current) ?? 0)));
    const said = t("espace.a2.progress", done, total) + (current ? `, ${t("espace.a2.progress.current", current)}` : "");
    const step = str(props.step);
    return (
      <div className={`a2-progress a2-tone-${props.tone ?? "default"}`}>
        <p className="a2-progress-head">
          <span className="a2-progress-label">{str(props.label)}</span>
          {total > 0 && <span className="a2-progress-count">{t("espace.a2.progress.of", done, total)}</span>}
          {step && <span className="a2-progress-step">{step}</span>}
        </p>
        {total > 0 && (
          <span className="a2-bar" role="img" aria-label={said}>
            {Array.from({ length: total }, (_, i) => (
              <span key={i} className={i < done ? "a2-seg-done" : i < done + current ? "a2-seg-now" : "a2-seg"} />
            ))}
          </span>
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
      kind: z.enum(["line", "bar", "stack"]),
      series: listOr(Series),
      unit: CommonSchemas.DynamicString.optional(),
      title: CommonSchemas.DynamicString.optional(),
    }),
  },
  ({ props }) => {
    const { t, lang } = useWords();
    const series = cleanSeries(props.series);
    const names = series.map((s) => s.name).filter(Boolean).join(", ");
    const label = t(`espace.a2.chart.${props.kind}`, names || "—");
    const title = <BlockTitle text={str(props.title)} />;
    if (!series.some((s) => s.points.length))
      return (
        <>
          {title}
          <p className="a2-empty">{t("espace.a2.chart.empty")}</p>
        </>
      );
    return (
      <>
        {title}
        <ChartView kind={props.kind} series={series} unit={str(props.unit) || undefined} lang={lang} label={label} />
      </>
    );
  },
);

const TableColumn = z.object({ key: z.string(), label: z.string(), kind: z.enum(["text", "number", "date", "change"]).optional() });

// A change, in percent, in ink: its sign written (+, −), none at zero.
function change(lang: Lang, v: unknown): string {
  const n = num(v);
  if (n === undefined) return str(v);
  return `${n > 0 ? "+" : n < 0 ? "−" : ""}${number(lang, Math.abs(n), 1)} %`;
}

function cell(lang: Lang, kind: string | undefined, v: unknown): string {
  if (v === null || v === undefined) return "";
  if (Array.isArray(v)) return v.map((x) => (typeof x === "object" ? "" : cell(lang, kind, x))).filter(Boolean).join(", ");
  if (kind === "change") return change(lang, v);
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

// A node's own file a node_find row names (a photo kept with it):
// eriphe-core gives its address (nodeFindMedia, node_find.go), which only
// it signs. An image shows as a thumbnail; another file (a PDF), as a
// link to download it.
const MEDIA = "/espace/media?";

function isMedia(v: unknown): v is string {
  return typeof v === "string" && v.startsWith(MEDIA);
}

function mediaImage(src: string): boolean {
  const u = new URLSearchParams(src.slice(MEDIA.length)).get("u") ?? "";
  return !u.toLowerCase().endsWith(".pdf");
}

function mediaCell(v: unknown, alt: string, file: string): ReactNode | undefined {
  const xs = (Array.isArray(v) ? v : [v]).filter(isMedia);
  if (!xs.length) return undefined;
  return xs.map((src) =>
    mediaImage(src) ? (
      <img key={src} className="a2-thumb" src={src} alt={alt} loading="lazy" />
    ) : (
      <a key={src} className="a2-file" href={src} download={`${alt}.pdf`}>
        {file}
      </a>
    ),
  );
}

// The fields of a row a Table shows by itself when its columns are not
// written: not these, which say what a row is to code (a node_find row's
// id, status, when it changed; an image; code's fields, _note…).
const UNSHOWN = new Set(["id", "status", "changed", "photo", "image"]);

// columnsOf are a Table's columns when its author wrote none: the fields
// of its first row, in their order, the key as their header (none for a
// title), numbers to the right.
export function columnsOf(rows: Record<string, unknown>[]): z.infer<typeof TableColumn>[] {
  const keys = Object.keys(rows[0] ?? {}).filter((k) => !UNSHOWN.has(k) && !k.startsWith("_"));
  return keys.map((key) => ({
    key,
    label: key === "title" ? "" : key.charAt(0).toUpperCase() + key.slice(1),
    kind: rows.every((r) => r[key] === undefined || r[key] === null || typeof r[key] === "number") ? "number" : "text",
  }));
}

const Table = createComponentImplementation(
  {
    name: "Table",
    schema: z.object({
      columns: z.array(TableColumn).min(1).optional(),
      rows: z.union([z.string(), z.array(z.record(z.string(), z.unknown())), CommonSchemas.DataBinding]),
      title: CommonSchemas.DynamicString.optional(),
      units: z.record(z.string(), z.string()).optional(),
      emptyText: CommonSchemas.DynamicString.optional(),
    }),
  },
  ({ props }) => {
    const { t, lang } = useWords();
    const lists = useContext(ListsContext);
    const rows = (Array.isArray(props.rows) ? props.rows : []).filter((r): r is Record<string, unknown> => !!r && typeof r === "object");
    const cols = props.columns ?? columnsOf(rows);
    const units: Record<string, string> = props.units ?? {};
    const stars = (n: string) => t("espace.blocks.stars", n);
    const title = str(props.title);
    if (!rows.length)
      return (
        <>
          <BlockTitle text={title} />
          <p className="a2-empty">{str(props.emptyText) || t("espace.a2.table.empty")}</p>
        </>
      );
    return (
      <>
      <BlockTitle text={title} aside={title ? <span className="a2-count">{rows.length}</span> : undefined} />
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
            {rows.map((r, i) => {
              // A row that names a node of the graph (a node_find source's):
              // its title as the graph shows it, marked when it may be
              // someone else's words; its first cell opens its page.
              const n = typeof r.id === "string" ? lists?.node?.(r.id) : undefined;
              return (
                <tr key={i}>
                  {cols.map((c, j) => {
                    let v: ReactNode = mediaCell(r[c.key], c.label, t("espace.props.file")) ?? (units[c.key] ? shown(lang, r[c.key], units[c.key], stars) : cell(lang, c.kind, r[c.key]));
                    if (n && c.key === "title") v = <NodeTitle x={n} fallback={str(r.title)} />;
                    if (n && j === 0 && lists?.openNode) {
                      const open = lists.openNode;
                      v = (
                        <button type="button" className="linkish a2-row-open" onClick={() => open(n.id)}>
                          {v}
                        </button>
                      );
                    }
                    return (
                      <td key={c.key} className={`a2-col-${c.kind ?? "text"}`}>
                        {v}
                      </td>
                    );
                  })}
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
      </>
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
  { name: "Timeline", schema: z.object({ items: listOr(TimelineItem), title: CommonSchemas.DynamicString.optional() }) },
  ({ props }) => {
    const { t, lang } = useWords();
    const items = (Array.isArray(props.items) ? props.items : []).filter(
      (it): it is z.infer<typeof TimelineItem> => !!it && typeof it === "object" && typeof it.label === "string",
    );
    // Every step dated: what happened, its date in the margin (its time
    // alone when it is today's).
    const dated = items.length > 1 && items.every((it) => wall(it.date));
    // It says where the work stands (« 2 sur 6 · Acompte »), with a bar of
    // its steps, while some are still to do: no Progress beside it. A log
    // (every step done or failed) says nothing of the kind.
    const done = items.filter((it) => it.state === "done").length;
    const current = items.find((it) => it.state === "current");
    const left = items.length > 1 && items.some((it) => it.state === "current" || it.state === "pending" || !STATES.includes(it.state));
    return (
      <>
      <BlockTitle text={str(props.title)} />
      {left && (
        <div className="a2-tl-prog">
          <p className="a2-tl-prog-words">
            <b>{t("espace.a2.progress.of", done, items.length)}</b>
            {current && <span> · {current.label}</span>}
          </p>
          <span className="a2-bar" role="img" aria-label={t("espace.a2.progress", done, items.length)}>
            {items.map((it, i) => (
              <span key={i} className={it.state === "done" ? "a2-seg-done" : it.state === "current" ? "a2-seg-now" : "a2-seg"} />
            ))}
          </span>
        </div>
      )}
      <ol className={`a2-timeline${dated ? " a2-timeline-dated" : ""}`}>
        {items.map((it, i) => {
          const state = STATES.includes(it.state) ? it.state : "pending";
          const w = wall(it.date);
          return (
            <li key={i} className={`a2-step a2-step-${state}`} aria-current={state === "current" ? "step" : undefined}>
              {dated && w && (
                <time className="a2-step-when" dateTime={it.date}>
                  {sent(lang, it.date!)}
                </time>
              )}
              <span className="a2-step-dot" aria-hidden="true">
                {state === "done" ? <IconView name="check" /> : state === "failed" ? <IconView name="close" /> : null}
              </span>
              <span className="a2-step-body">
                <span className="a2-step-label">{it.label}</span>
                <span className="sr-only">, {t(`espace.a2.step.${state}`)}</span>
                {!dated && w && <span className="a2-step-date"> {when(lang, w)}</span>}
                {it.detail && <span className="a2-step-detail">{it.detail}</span>}
              </span>
            </li>
          );
        })}
      </ol>
      </>
    );
  },
);

// Item is one line of a list: what it is and, quieter, where it comes
// from; under it a note; aside its due date and a short mark (« extérieur
// »). Its level makes it one quiet line (low); what waits for the user is
// the item's card, on its row, never a level.
const LEVELS = ["normal", "low"];

const Item = createComponentImplementation(
  {
    name: "Item",
    schema: z.object({
      title: CommonSchemas.DynamicString,
      detail: CommonSchemas.DynamicString.optional(),
      note: CommonSchemas.DynamicString.optional(),
      due: CommonSchemas.DynamicString.optional(),
      mark: CommonSchemas.DynamicString.optional(),
      level: CommonSchemas.DynamicString.optional(),
    }),
  },
  ({ props }) => {
    const { t, lang } = useWords();
    const level = LEVELS.includes(str(props.level)) ? str(props.level) : "normal";
    // A sender as people read it ("Sam <sam@example.org>" is Sam), as Message does.
    const detail = /<[^<>@\s]+@[^<>\s]+>$/.test(str(props.detail)) ? nameOf(str(props.detail)) : str(props.detail), note = str(props.note), mark = str(props.mark);
    const due = wall(str(props.due));
    return (
      <div className={`a2-item a2-item-${level}`}>
        <div className="a2-item-main">
          <p className="a2-item-head">
            <span className="a2-item-title">{str(props.title)}</span>
            {detail && <span className="a2-item-detail">{detail}</span>}
          </p>
          {note && <p className="a2-item-note">{note}</p>}
        </div>
        {(due || mark) && (
          <div className="a2-item-side">
            {mark && <span className="a2-badge a2-badge-external">{mark}</span>}
            {due && (
              <time className="a2-due" dateTime={str(props.due)}>
                {t("espace.a2.item.due", when(lang, due))}
              </time>
            )}
          </div>
        )}
      </div>
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

// initials: what a person's circle shows, one or two letters of their name.
export function initials(name: string): string {
  const words = nameOf(name).split(/[\s.\-_]+/).filter((w) => /\p{L}/u.test(w));
  return (words.length > 1 ? words[0][0] + words[words.length - 1][0] : (words[0] ?? "?").slice(0, 1)).toUpperCase();
}

const Fact = z.object({ label: z.string(), value: z.string() });

const MAIL = /^[A-Za-z0-9._%+-]{1,64}@[A-Za-z0-9-]+(\.[A-Za-z0-9-]+)*\.[A-Za-z]{2,24}$/;
const PHONE = /^\+?[0-9][0-9 .()-]{4,22}$/;

// factLink is where a fact opens on a click, built by the page from its
// value, never written: a value that is an e-mail address, whole, mails it
// (mailto:, the address alone); one that is a phone number, whole, calls
// it (tel:, its digits). Anything else is text: no other scheme, nothing
// added to the address.
export function factLink(value: string): string | null {
  const v = value.trim();
  if (MAIL.test(v)) return `mailto:${v}`;
  const digits = v.replace(/[^0-9]/g, "");
  if (PHONE.test(v) && digits.length >= 6 && digits.length <= 15) return `tel:${v.startsWith("+") ? "+" : ""}${digits}`;
  return null;
}

// Person: a name and what they are to the user, a sentence of where things
// stand with them, and a few facts as lines (a phone, a next meeting). A
// phone or an e-mail address opens on a click (factLink); any other fact
// is text, selectable.
const Person = createComponentImplementation(
  {
    name: "Person",
    schema: z.object({
      name: CommonSchemas.DynamicString,
      detail: CommonSchemas.DynamicString.optional(),
      note: CommonSchemas.DynamicString.optional(),
      facts: listOr(Fact).optional(),
    }),
  },
  ({ props }) => {
    const id = useId();
    const name = nameOf(str(props.name));
    const facts = (Array.isArray(props.facts) ? props.facts : []).filter((f): f is z.infer<typeof Fact> => !!f && typeof f.label === "string" && typeof f.value === "string").slice(0, 8);
    return (
      <section className="a2-person" aria-labelledby={id}>
        <span className="a2-person-face" aria-hidden="true">{initials(name)}</span>
        <div className="a2-person-main">
          <p className="a2-person-head">
            <span id={id} className="a2-person-name">{name}</span>
            {props.detail && <span className="a2-person-detail">{props.detail}</span>}
          </p>
          {props.note && <p className="a2-person-note">{props.note}</p>}
          {facts.length > 0 && (
            <dl className="a2-person-facts">
              {facts.map((f, i) => (
                <div key={i}>
                  <dt>{f.label}</dt>
                  <dd>{factLink(f.value) ? <a href={factLink(f.value)!} rel="noopener noreferrer">{f.value}</a> : f.value}</dd>
                </div>
              ))}
            </dl>
          )}
        </div>
      </section>
    );
  },
);

// Quote: someone else's words, as they wrote them, and who and when; the
// page they come from shown in clear, opened on a click only, as a Link's.
const Quote = createComponentImplementation(
  {
    name: "Quote",
    schema: z.object({
      text: CommonSchemas.DynamicString,
      from: CommonSchemas.DynamicString.optional(),
      date: CommonSchemas.DynamicString.optional(),
      url: z.union([z.string(), CommonSchemas.DataBinding]).optional(),
    }),
  },
  ({ props, context }) => {
    const { t, lang } = useWords();
    const from = nameOf(str(props.from)), date = str(props.date);
    const u = linkTo(str(props.url));
    // As a Link: the whole address when the block shows external content.
    const whole = external(context.dataContext.surface);
    return (
      <figure className="a2-quote">
        <blockquote className="a2-quote-text" cite={u?.href}>
          {str(props.text)}
        </blockquote>
        {(from || date || u) && (
          <figcaption className="a2-quote-from">
            {from && <span className="a2-quote-who">{from}</span>}
            {date && wall(date) && (
              <time className="a2-quote-when" dateTime={date}>
                {sent(lang, date)}
              </time>
            )}
            {u && (
              <a className="a2-link a2-quote-link" href={u.href} target="_blank" rel="noopener noreferrer" referrerPolicy="no-referrer" title={u.href}>
                <IconView name="link" />
                <span className="a2-link-host">{whole ? u.href : u.host}</span>
                <span className="sr-only"> {t("espace.a2.newtab")}</span>
              </a>
            )}
          </figcaption>
        )}
      </figure>
    );
  },
);

// Agenda is anything from a calendar: its days from date (today when
// absent), the first by the hour. Bound to a source's items, each event's
// card is what eriphe-core told of the row (lists.tsx), never the data's.
// A status is the source's word, said by the page in the user's (agenda.tsx).
const Agenda = createComponentImplementation(
  {
    name: "Agenda",
    schema: z.object({
      date: CommonSchemas.DynamicString.optional(),
      days: z.number().int().min(1).max(AGENDA_DAYS).optional(),
      events: listOr(
        z.object({
          title: z.string(),
          start: z.string(),
          end: z.string().optional(),
          place: z.string().optional(),
          allDay: z.boolean().optional(),
          status: z.string().optional(),
        }),
      ),
      proposals: z.boolean().optional(),
    }),
  },
  ({ props, context }) => {
    const { t, lang } = useWords();
    const now = useNow();
    const date = /^\d{4}-\d{2}-\d{2}/.test(str(props.date)) ? str(props.date).slice(0, 10) : today(now).date;
    const days = Math.max(1, Math.min(AGENDA_DAYS, props.days ?? 1));
    const ghosts = useGhosts(date, days, props.proposals !== false);
    const surface = context.dataContext.surface?.id;
    const told = useList(surface, context.componentModel.id);
    const cards: AgendaCards | undefined =
      told && surface ? { where: { surfaceId: surface, componentId: told.list.component }, card: (n) => told.list.items.find((it) => it.n === n)?.card } : undefined;
    return <AgendaView date={date} days={days} events={cleanEvents(props.events)} ghosts={ghosts} cards={cards} lang={lang} t={t} now={now} />;
  },
);

// useGhosts are the proposals that wait on the days shown, as ghosts: read
// from eriphe-core, and again whenever a proposal moved (one made, one
// answered anywhere).
function useGhosts(date: string, days: number, show: boolean): Ghost[] {
  const src = useProposals();
  const moved = useMoved(src, show ? null : []);
  const [ghosts, setGhosts] = useState<Ghost[]>([]);
  useEffect(() => {
    if (!show || !/^\d{4}-\d{2}-\d{2}$/.test(date)) {
      setGhosts([]);
      return;
    }
    let live = true;
    Promise.all(Array.from({ length: days }, (_, i) => src.day(addDays(date, i)))).then(
      (all) => live && setGhosts(ghostsOf(all.flat())),
      () => {},
    );
    return () => {
      live = false;
    };
  }, [src, date, days, show, moved]);
  return ghosts;
}

// ghostsOf: one ghost per time of a pending proposal.
export function ghostsOf(ps: ProposalView[]): Ghost[] {
  return ps
    .filter((p) => p.state === "pending")
    .flatMap((p) => (p.when ?? []).map((w) => ({ ref: p.ref, title: p.text, start: w.start, end: w.end, allDay: w.allDay, tainted: p.tainted })));
}

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
  Link,
  Code,
  ...blocks,
  box("row"),
  box("column"),
  List,
  Grid,
  Card,
  Tabs,
  Fold,
  Divider,
  Button,
  Choice,
  TextField,
  DateField,
  Metric,
  Progress,
  Chart,
  Table,
  Timeline,
  Item,
  Message,
  Person,
  Quote,
  Agenda,
  Alert,
  Placeholder,
];

// catalog is the catalog with its functions in the user's language.
export const catalog = (lang: Lang = "fr") => new Catalog(CATALOG.id, "1.0", components, functions(lang));
