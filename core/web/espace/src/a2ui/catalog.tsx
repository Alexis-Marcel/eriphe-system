import { useEffect, useId, useRef, useState, type KeyboardEvent, type ReactNode } from "react";
import { createComponentImplementation, type ReactComponentImplementation } from "@a2ui/react";
import { Catalog, CommonSchemas, type ComponentContext } from "@a2ui/web_core/v1_0";
import { z } from "zod";
import { useWords, type Lang } from "../i18n";
import { Chart as ChartView, cleanSeries } from "./chart";
import { Code as CodeView } from "./code";
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
      variant: z.enum(["title", "subtitle", "body", "caption", "mark"]).optional(),
    }),
  },
  ({ props }) => {
    const v = props.variant ?? "body";
    const cls = `a2-text a2-text-${v}`;
    // A title is a heading, under the tile's: assistive technology can
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
// line, open or not: an action on all it sums up (« Archiver les 2 »).
const Fold = createComponentImplementation(
  {
    name: "Fold",
    schema: z.object({
      label: CommonSchemas.DynamicString,
      child: CommonSchemas.Child,
      aside: CommonSchemas.Child.optional(),
      open: CommonSchemas.DynamicBoolean.optional(),
    }),
  },
  ({ props, buildChild }) => {
    const { t } = useWords();
    const id = useId();
    const [open, setOpen] = useState(props.open === true);
    const toggle = (
      <button type="button" className="a2-fold-head" aria-expanded={open} aria-controls={id} title={t(open ? "espace.a2.fold.close" : "espace.a2.fold.open")} onClick={() => setOpen(!open)}>
        <span className="a2-fold-label">{str(props.label)}</span>
        <span className="a2-fold-chev" aria-hidden="true">{open ? "▾" : "▸"}</span>
      </button>
    );
    return (
      <div className={`a2-fold${open ? " a2-fold-open" : ""}`}>
        {props.aside ? (
          <div className="a2-fold-line">
            {toggle}
            <div className="a2-fold-aside">{buildChild(props.aside)}</div>
          </div>
        ) : (
          toggle
        )}
        {open && (
          <div id={id} className="a2-fold-body">
            {props.child ? buildChild(props.child) : null}
          </div>
        )}
      </div>
    );
  },
);

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

// useDataAt is the surface's data at a path, kept up to date; undefined
// with no path.
function useDataAt(context: ComponentContext, path: string | null): unknown {
  const [v, setV] = useState<unknown>(undefined);
  useEffect(() => {
    if (!path) {
      setV(undefined);
      return;
    }
    const sub = context.dataContext.subscribeDynamicValue<unknown>({ "@path": path }, setV);
    setV(sub.value);
    return () => sub.unsubscribe();
  }, [context.dataContext, path]);
  return v;
}

// Mark is the user's mark on something a node's block shows: a toggle for
// each mark it offers, pressed when it is theirs, read where eriphe-core
// keeps it (/marks/<list>/<item> of the block's data). A click sends "mark" with ids only,
// the mark or "" to take it away; eriphe-core checks it against this Mark
// and writes the data again. done alone is a box to check.
const MARK_ID = /^[A-Za-z0-9_][A-Za-z0-9_.:@=+-]{0,127}$/;
const MarkId = z.union([z.string(), CommonSchemas.DataBinding]);
const MARK_ICON = { keep: "star", drop: "close", done: "check" } as const;

const Mark = createComponentImplementation(
  {
    name: "Mark",
    schema: z.object({ list: MarkId, item: MarkId, marks: z.array(z.enum(["keep", "drop", "done"])).min(1) }),
  },
  ({ props, context }) => {
    const { t } = useWords();
    const list = str(props.list), item = str(props.item);
    const ok = MARK_ID.test(list) && MARK_ID.test(item);
    // Shown as eriphe-core keeps it, never ahead of it: it writes the
    // data again once it took the mark.
    const shown = str(useDataAt(context, ok ? `/marks/${list}/${item}` : null));
    const set = (m: string) => void context.dispatchAction({ event: { name: "mark", context: { list, item, mark: shown === m ? "" : m } } });
    const marks = props.marks ?? [];
    return (
      <span className={`a2-marks${shown ? " a2-marked-" + shown : ""}`}>
        {marks.map((m) => (
          <button
            key={m}
            type="button"
            className={`a2-mark a2-mark-${m}${marks.length === 1 && m === "done" ? " a2-mark-box" : ""}`}
            aria-pressed={shown === m}
            disabled={!ok}
            onClick={() => set(m)}
          >
            <IconView name={MARK_ICON[m]} />
            <span className={marks.length === 1 && m === "done" ? "sr-only" : undefined}>{t(`espace.a2.mark.${m}`)}</span>
          </button>
        ))}
      </span>
    );
  },
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

// Progress says where a thing stands: its state word in a pill, coloured
// by its tone, and when it has steps a bar of them, done, running, to come.
const TONES = ["default", "attention", "urgent", "success", "muted"] as const;
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
    }),
  },
  ({ props }) => {
    const { t } = useWords();
    const total = Math.max(0, Math.min(PROGRESS_MAX, Math.round(num(props.total) ?? 0)));
    const done = Math.max(0, Math.min(total, Math.round(num(props.value) ?? 0)));
    const current = Math.max(0, Math.min(total - done, Math.round(num(props.current) ?? 0)));
    const said = t("espace.a2.progress", done, total) + (current ? `, ${t("espace.a2.progress.current", current)}` : "");
    return (
      <div className={`a2-progress a2-tone-${props.tone ?? "default"}`}>
        <span className="a2-pill">{str(props.label)}</span>
        {total > 0 && (
          <>
            <span className="a2-bar" role="img" aria-label={said}>
              {Array.from({ length: total }, (_, i) => (
                <span key={i} className={i < done ? "a2-seg-done" : i < done + current ? "a2-seg-now" : "a2-seg"} />
              ))}
            </span>
            <span className="a2-progress-count" aria-hidden="true">{done}/{total}</span>
          </>
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
    }),
  },
  ({ props }) => {
    const { t, lang } = useWords();
    const series = cleanSeries(props.series);
    const names = series.map((s) => s.name).filter(Boolean).join(", ");
    const label = t(`espace.a2.chart.${props.kind}`, names || "—");
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

// Item is one line of a list: what it is and, quieter, where it comes
// from; under it a note; aside its due date and a short mark (« extérieur
// »). Its level puts it forward (high: it waits for the user) or makes it
// one quiet line (low).
const LEVELS = ["high", "normal", "low"];

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
    const detail = str(props.detail), note = str(props.note), mark = str(props.mark);
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
// proposal as its card does, with the proposal's own taint; so does a
// thread's surface (answers: code bound its Proposals to the thread's
// cards, whatever the thread read).
type Meta = { metadata?: { extensions?: { eriphe?: { tainted?: boolean; author?: string; answers?: boolean } } } };
const onlyAsks = (s: unknown) => {
  const m = (s as Meta | undefined)?.metadata?.extensions?.eriphe;
  return Boolean(m?.tainted) && m?.author !== "core" && !m?.answers;
};
// external tells whether a surface shows external content, whoever wrote it.
const external = (s: unknown) => Boolean((s as Meta | undefined)?.metadata?.extensions?.eriphe?.tainted);

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
  { name: "Proposal", schema: z.object({ proposalId: CommonSchemas.DynamicString, compact: z.boolean().optional() }) },
  ({ props, context }) => {
    const { t } = useWords();
    const src = useProposals();
    const ref = str(props.proposalId);
    const [whole, setWhole] = useState(false); // a compact one opened
    const [p, setP] = useState<ProposalView | null | undefined>(undefined);
    const [busy, setBusy] = useState<"yes" | "no" | null>(null); // the answer on its way
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
      setBusy(yes ? "yes" : "no");
      setFailed(false);
      const surfaceId = context.dataContext.surface?.id ?? "";
      // Bound inside a list (a row's card): where, so that only this row's answers.
      const at = context.dataContext.path && context.dataContext.path !== "/" ? { at: context.dataContext.path } : {};
      const ok = await src.answer({ surfaceId, componentId: context.componentModel.id, ref: p.ref, yes, ...at }).catch(() => false);
      if (!ok) setFailed(true);
      const again = await src.read(ref).catch(() => null);
      setP(again ?? { ...p, state: ok ? (yes ? "approved" : "rejected") : p.state });
      setBusy(null);
    };
    if (p === undefined) return <div className="a2-proposal a2-proposal-loading" aria-busy="true"><span className="a2-shimmer" aria-hidden="true" /></div>;
    if (p === null) return <p className="a2-empty">{t("espace.a2.proposal.none")}</p>;
    const pending = p.state === "pending";
    const elsewhere = onlyAsks(context.dataContext.surface);
    const keeps = KEEPS[p.kind ?? ""];
    // Approved, an action's call runs, then is done or failed: the
    // stream's "proposals" moves bring each step. What only keeps
    // something runs nothing: Gardé or Oublié.
    const exec = p.state === "approved" && !keeps ? p.exec : undefined;
    const kicker = pending
      ? t(keeps ?? "espace.a2.proposal")
      : exec
        ? t(`espace.a2.proposal.${exec === "failed" ? "execfailed" : exec}`)
        : t(decidedWord(p.state, Boolean(keeps)));
    const icon = pending ? "star" : exec === "failed" || p.state !== "approved" ? "close" : exec === "running" ? "clock" : "check";
    // Compact and decided: one line, opened in place by a click.
    if (props.compact && !pending && !whole) {
      return (
        <button type="button" className={`a2-proposal-line${exec ? " a2-proposal-exec-" + exec : ""}`} aria-expanded="false" onClick={() => setWhole(true)}>
          <IconView name={icon} />
          <span className="a2-proposal-line-kicker">{kicker}</span>
          <span className="a2-proposal-line-text">{p.text}</span>
        </button>
      );
    }
    return (
      <section
        className={`a2-proposal a2-proposal-${pending ? "pending" : "decided"}${exec ? " a2-proposal-exec-" + exec : ""}`}
        aria-label={t(keeps ?? "espace.a2.proposal")}
        aria-busy={exec === "running" || undefined}
      >
        <p className="a2-proposal-kicker" role={exec ? "status" : undefined}>
          <IconView name={icon} />
          {kicker}
          {/* Drawn from external content: a small badge, the whole sentence for a screen reader and on hover. */}
          {p.tainted && (
            <span className="a2-badge a2-badge-external" title={t("espace.a2.proposal.tainted")}>
              <IconView name="warning" />
              <span aria-hidden="true">{t("espace.a2.proposal.external")}</span>
              <span className="sr-only">{t("espace.a2.proposal.tainted")}</span>
            </span>
          )}
        </p>
        {exec === "failed" && p.execWhy && <p className="a2-proposal-failed">{t(`espace.a2.proposal.why.${p.execWhy}`)}</p>}
        <p className="a2-proposal-text">{p.text}</p>
        {p.why && <p className="a2-proposal-why">{p.why}</p>}
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
        {p.from && <p className="a2-proposal-from">{p.node ? <a href={`/espace?open=node-${p.node}`}>{p.from}</a> : p.from}</p>}
        {pending && p.yes && <p className="a2-proposal-yes">{p.yes}</p>}
        {pending && elsewhere && <p className="a2-proposal-why a2-proposal-inchat">{t("espace.a2.proposal.inchat")}</p>}
        {pending && !elsewhere && (
          <div className="a2-proposal-actions">
            <button type="button" className={`btn a2-button a2-button-primary${busy === "yes" ? " is-busy" : ""}`} disabled={!!busy} aria-busy={busy === "yes"} onClick={() => answer(true)}>
              {t(keeps ? "espace.a2.keep" : "espace.a2.yes")}
            </button>
            <button type="button" className={`btn a2-button${busy === "no" ? " is-busy" : ""}`} disabled={!!busy} aria-busy={busy === "no"} onClick={() => answer(false)}>
              {t(keeps ? "espace.a2.forget" : "espace.a2.no")}
            </button>
          </div>
        )}
        {failed && <p className="a2-proposal-failed" role="alert">{t("espace.a2.proposal.failed")}</p>}
      </section>
    );
  },
);

// Offers shows the actions eriphe-core offers on a row of a thread, or on
// a group of rows: its data, bound under /thread/items, one button each,
// with code's words. A click sends "offer" with that offer's ids as code
// wrote them, never its words; eriphe-core checks it against this Offers
// and the store, and makes a card: nothing runs.
const OFFER_KEYS = ["thread", "source", "item", "state", "offer"] as const;
type Offer = { label: string } & Partial<Record<(typeof OFFER_KEYS)[number], unknown>>;

const Offers = createComponentImplementation(
  // A list the engine resolves; eriphe-core takes a binding only.
  { name: "Offers", schema: z.object({ offers: listOr(z.record(z.string(), z.unknown())) }) },
  ({ props, context }) => {
    const offers = (Array.isArray(props.offers) ? props.offers : []).filter(
      (o): o is Offer => !!o && typeof o === "object" && typeof o.label === "string" && typeof o.offer === "string",
    );
    if (!offers.length) return null;
    const send = (o: Offer) => {
      const ids = Object.fromEntries(OFFER_KEYS.filter((k) => k in o).map((k) => [k, o[k]]));
      void context.dispatchAction({ event: { name: "offer", context: ids } });
    };
    return (
      <span className="a2-offers">
        {offers.map((o, i) => (
          <button key={i} type="button" className="btn a2-button a2-button-default" onClick={() => send(o)}>
            {o.label}
          </button>
        ))}
      </span>
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
  Link,
  Code,
  box("row"),
  box("column"),
  List,
  Grid,
  Card,
  Tabs,
  Fold,
  Divider,
  Button,
  Mark,
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
  Day,
  Proposal,
  Offers,
  Alert,
  Placeholder,
];

// catalog is the catalog with its functions in the user's language.
export const catalog = (lang: Lang = "fr") => new Catalog(CATALOG.id, "1.0", components, functions(lang));
