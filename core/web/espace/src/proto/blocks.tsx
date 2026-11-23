import { AnimatePresence, animate, motion, useReducedMotion } from "motion/react";
import { useEffect, useLayoutEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { sparkPoints } from "../a2ui/spark";
import { relative } from "../a2ui/format";
import { factLink, initials, nameOf } from "../a2ui/catalog";

// The prototype's higher-level blocks: one component a block, keyed
// props with defaults, and everything else the renderer's: fields it
// guesses, sections, units, hierarchy, empty / loading / error states,
// phone layout (container queries in proto.css), motion. The data a block
// reads is resolved by Page.tsx (a source's items with code's fields).

export type Row = Record<string, unknown>;
// What a block's source gives the renderer: its rows, or a state.
export type Feed = { rows?: Row[]; loading?: boolean; error?: string; every?: number; read?: string; items?: { title: string; detail?: string } };
export type Ctx = { feed?: Feed; onDone?: (ids: string[]) => void; hero?: boolean };

const EASE = [0.2, 0, 0, 1] as const;
const str = (v: unknown) => (typeof v === "string" ? v : typeof v === "number" ? String(v) : "");
const fr = new Intl.NumberFormat("fr-FR", { maximumFractionDigits: 2 });
const isNum = (v: unknown): v is number => typeof v === "number" && isFinite(v);

// --- units: one key, many looks ---------------------------------------------------

// shown writes a value with its unit: « € » an amount, « ±% » a signed change,
// « % » a share, « ★ » a rating out of 5, anything else after the number.
export function shown(v: unknown, unit?: string): ReactNode {
  if (!isNum(v)) return str(v);
  if (unit === "★") return <Stars n={v} />;
  if (unit === "±%") return <span className={v > 0 ? "bv-up" : v < 0 ? "bv-down" : ""}>{v > 0 ? "+" : v < 0 ? "−" : ""}{fr.format(Math.abs(v))} %</span>;
  if (unit === "%") return `${fr.format(v)} %`;
  if (unit === "€") return `${fr.format(v)} €`;
  return unit ? `${fr.format(v)} ${unit}` : fr.format(v);
}
function Stars({ n }: { n: number }) {
  return (
    <span className="bv-stars" role="img" aria-label={`${n} sur 5`}>
      {[1, 2, 3, 4, 5].map((i) => (
        <i key={i} className={i <= Math.round(n) ? "on" : ""} />
      ))}
    </span>
  );
}

// --- the block's frame: its title, its state ------------------------------------------

export function Head({ title, aside }: { title?: string; aside?: ReactNode }) {
  if (!title && !aside) return null;
  return (
    <header className="bv-head">
      {title && <h3>{title}</h3>}
      {aside}
    </header>
  );
}

// A source that failed keeps its last value and says so, once, quietly.
function Stale({ feed }: { feed?: Feed }) {
  if (!feed?.error || !feed.rows) return null;
  return (
    <p className="bv-stale" role="status">
      <i aria-hidden="true" /> {feed.error}
    </p>
  );
}

// Skeletons drawn by kind: the LLM never writes a loading state.
export function Skeleton({ kind }: { kind: string }) {
  if (kind === "Figures") return <div className="bv-sk bv-sk-figs">{[0, 1, 2].map((i) => <span key={i} />)}</div>;
  if (kind === "Gallery") return <div className="bv-sk bv-sk-gal">{[0, 1, 2, 3].map((i) => <span key={i} />)}</div>;
  return <div className="bv-sk bv-sk-rows">{[0, 1, 2, 3].map((i) => <span key={i} />)}</div>;
}

// Empty, said by the renderer from what it knows: when the source was read, how often.
export function Empty({ feed, what = "Rien ici pour l'instant" }: { feed?: Feed; what?: string }) {
  const next = feed?.every ? feed.every === 60 ? "relu toutes les heures" : `relu toutes les ${feed.every > 60 ? `${feed.every / 60} h` : `${feed.every} min`}` : "";
  return (
    <div className="bv-empty">
      <span className="bv-empty-ring" aria-hidden="true" />
      <p>{what}</p>
      {next && <small>Lu à l'instant · {next}</small>}
    </div>
  );
}

function Failed({ feed }: { feed: Feed }) {
  return (
    <div className="bv-empty failed" role="status">
      <span className="bv-empty-ring" aria-hidden="true" />
      <p>{feed.error}</p>
      <small>Rien n'est perdu : un nouvel essai se fait tout seul.</small>
    </div>
  );
}

// --- Figures -----------------------------------------------------------------------

type Figure = { label: string; value: number | string; unit?: string; delta?: number; deltaLabel?: string; trend?: number[] };

// Ticker: a number that rolls to its new value (≤ 240 ms), at once when
// motion is reduced; the figure flashes softly when it moved.
function Ticker({ value, unit, plain }: { value: number; unit?: string; plain?: boolean }) {
  const reduce = useReducedMotion();
  const ref = useRef<HTMLSpanElement>(null);
  const last = useRef(value);
  const [moved, setMoved] = useState<"" | "up" | "down">("");
  useEffect(() => {
    const from = last.current;
    last.current = value;
    if (from === value || !ref.current) return;
    setMoved(value > from ? "up" : "down");
    const t = setTimeout(() => setMoved(""), 900);
    if (reduce) {
      ref.current.textContent = fr.format(value);
      return () => clearTimeout(t);
    }
    const c = animate(from, value, { duration: 0.24, ease: EASE, onUpdate: (v) => ref.current && (ref.current.textContent = fr.format(Math.round(v))) });
    return () => (c.stop(), clearTimeout(t));
  }, [value, reduce]);
  return (
    <span className={`${plain ? "bv-tick" : "bv-fig-value"} ${moved}`}>
      <span ref={ref}>{fr.format(value)}</span>
      {unit && <small>{unit === "€" ? " €" : ` ${unit}`}</small>}
    </span>
  );
}

function Spark({ values }: { values: number[] }) {
  const pts = sparkPoints(values);
  if (!pts.length) return null;
  const line = pts.map((p) => p.join(",")).join(" ");
  const [lx, ly] = pts[pts.length - 1];
  return (
    <svg className="bv-spark" viewBox="0 0 120 28" preserveAspectRatio="none" aria-hidden="true">
      <polygon className="area" points={`${pts[0][0]},28 ${line} ${lx},28`} />
      <polyline className="line" points={line} vectorEffect="non-scaling-stroke" />
      <polyline className="dot" points={`${lx},${ly} ${lx},${ly}`} vectorEffect="non-scaling-stroke" />
    </svg>
  );
}

export function Figures({ p, ctx }: { p: Row; ctx: Ctx }) {
  const items = (Array.isArray(p.items) ? p.items : []) as Figure[];
  return (
    <>
      <Head title={str(p.title)} />
      <div className={`bv-figs n${Math.min(items.length, 4)}${ctx.hero ? " hero" : ""}`}>
        {items.map((f, i) => (
          <div className="bv-fig" key={f.label + i}>
            <span className="bv-fig-label">{f.label}</span>
            {isNum(f.value) ? <Ticker value={f.value} unit={f.unit} /> : <span className="bv-fig-value text">{f.value}</span>}
            {(isNum(f.delta) || f.deltaLabel) && (
              <span className="bv-fig-delta">
                {isNum(f.delta) && f.delta !== 0 && <b>{f.delta > 0 ? "▲" : "▼"} {fr.format(Math.abs(f.delta))}{f.unit === "€" ? " €" : ""}</b>} {f.deltaLabel}
              </span>
            )}
            {f.trend && <Spark values={f.trend} />}
          </div>
        ))}
      </div>
    </>
  );
}

// --- Sections: a source's items, grouped by code's fields ------------------------------

type Group = { name: string; kind: "open" | "batch" | "folded"; rows: Row[] };

// groupsOf sorts items into sections from code's fields: a group with
// something to do opens first, a group whose items share one card is a
// batch, the rest folds. No section name is written in the block.
export function groupsOf(rows: Row[]): Group[] {
  const by = new Map<string, Row[]>();
  for (const r of rows) {
    if (r._state === "done") continue;
    const g = str(r._group) || "Le reste";
    by.set(g, [...(by.get(g) ?? []), r]);
  }
  const gs: Group[] = [...by].map(([name, rs]) => {
    const cards = new Set(rs.map((r) => str(r._card)).filter(Boolean));
    const batch = rs.length > 1 && cards.size === 1 && rs.every((r) => r._card);
    const open = rs.some((r) => r._state === "act" || r._state === "look");
    return { name, rows: rs, kind: batch ? "batch" : open ? "open" : "folded" };
  });
  const rank = { open: 0, batch: 1, folded: 2 };
  return gs.sort((a, b) => rank[a.kind] - rank[b.kind]);
}

function Avatar({ name }: { name: string }) {
  // a calm hue from the name, the same every time
  let h = 0;
  for (const c of name) h = (h * 31 + c.charCodeAt(0)) % 5;
  return (
    <span className={`bv-av h${h}`} aria-hidden="true">
      {initials(name)}
    </span>
  );
}

function ItemRow({ r, feed, rich, onDone }: { r: Row; feed?: Feed; rich: boolean; onDone?: (ids: string[]) => void }) {
  const [done, setDone] = useState(false);
  const title = str(r[feed?.items?.title ?? "title"]);
  const who = nameOf(str(r[feed?.items?.detail ?? "detail"]));
  const note = str(r._note);
  const when = typeof r.date === "string" ? relative("fr", r.date) : "";
  const yes = () => {
    setDone(true);
    setTimeout(() => onDone?.([str(r.id)]), 420);
  };
  return (
    <div className={`bv-row${rich ? " rich" : ""}${done ? " done" : ""}`}>
      {rich && <Avatar name={who || title} />}
      <div className="bv-row-main">
        {rich && note ? (
          <>
            <span className="bv-row-title">{note}</span>
            <span className="bv-row-sub">
              <b>{who}</b> · {title}
            </span>
          </>
        ) : (
          <>
            <span className="bv-row-title">{title}</span>
            <span className="bv-row-sub">
              {who && <b>{who}</b>}
              {who && note ? " · " : ""}
              {note}
            </span>
          </>
        )}
      </div>
      <div className="bv-row-end">
        {rich && !!r._cardText && <span className="bv-row-card">{str(r._cardText)}</span>}
        {when && <time>{when}</time>}
        {rich && !!r._card && (
          <button className="bv-yes" onClick={yes} disabled={done} aria-label="Oui">
            {done ? <Check /> : "Oui"}
          </button>
        )}
      </div>
    </div>
  );
}

function Check() {
  return (
    <svg className="bv-check" viewBox="0 0 16 16" aria-hidden="true">
      <motion.path d="M3.5 8.5 L6.5 11.5 L12.5 4.5" initial={{ pathLength: 0 }} animate={{ pathLength: 1 }} transition={{ duration: 0.22, ease: EASE }} />
    </svg>
  );
}

// Rows that enter and leave: the row's height folds away, the others
// slide up; no stagger inside a list.
function Rows({ rows, feed, rich, max, onDone }: { rows: Row[]; feed?: Feed; rich: boolean; max: number; onDone?: (ids: string[]) => void }) {
  const [all, setAll] = useState(false);
  const shownRows = all ? rows : rows.slice(0, max);
  return (
    <ul className="bv-rows">
      <AnimatePresence initial={false}>
        {shownRows.map((r) => (
          <motion.li
            key={str(r.id)}
            layout="position"
            initial={{ opacity: 0, height: 0 }}
            animate={{ opacity: 1, height: "auto" }}
            exit={{ opacity: 0, height: 0 }}
            transition={{ duration: 0.2, ease: EASE }}
          >
            <ItemRow r={r} feed={feed} rich={rich} onDone={onDone} />
          </motion.li>
        ))}
      </AnimatePresence>
      {rows.length > max && (
        <li>
          <button className="bv-more" onClick={() => setAll(!all)}>
            {all ? "Moins" : `${rows.length - max} de plus`}
          </button>
        </li>
      )}
    </ul>
  );
}

// A batch or a folded group: one line, its count rolling when rows leave.
function Fold({ g, feed, onDone }: { g: Group; feed?: Feed; onDone?: (ids: string[]) => void }) {
  const [open, setOpen] = useState(false);
  const [going, setGoing] = useState(false);
  const yes = () => {
    setGoing(true);
    setTimeout(() => onDone?.(g.rows.map((r) => str(r.id))), 380);
  };
  return (
    <motion.div className={`bv-fold ${g.kind}${going ? " going" : ""}`} layout="position" transition={{ duration: 0.2, ease: EASE }}>
      <div className="bv-fold-line">
        <button className="bv-fold-toggle" aria-expanded={open} onClick={() => setOpen(!open)}>
          <span className="bv-chev" aria-hidden="true" />
          <span className="bv-fold-name">{g.name}</span>
          <span className="bv-count">
            <Ticker value={g.rows.length} plain />
          </span>
        </button>
        {g.kind === "batch" && (
          <button className="bv-yes" onClick={yes} disabled={going}>
            {going ? <Check /> : "Oui, pour tous"}
          </button>
        )}
      </div>
      <AnimatePresence initial={false}>
        {open && (
          <motion.div className="bv-fold-body" initial={{ height: 0, opacity: 0 }} animate={{ height: "auto", opacity: 1 }} exit={{ height: 0, opacity: 0 }} transition={{ duration: 0.2, ease: EASE }}>
            <Rows rows={g.rows} feed={feed} rich={false} max={8} onDone={onDone} />
          </motion.div>
        )}
      </AnimatePresence>
    </motion.div>
  );
}

export function Sections({ p, ctx }: { p: Row; ctx: Ctx }) {
  const feed = ctx.feed;
  const groups = useMemo(() => groupsOf(feed?.rows ?? []), [feed?.rows]);
  if (feed?.loading) return <Skeleton kind="Sections" />;
  if (feed?.error && !feed.rows) return <Failed feed={feed} />;
  if (!groups.length) return <Empty feed={feed} />;
  const open = groups.filter((g) => g.kind === "open");
  return (
    <>
      <Head title={str(p.title)} />
      <Stale feed={feed} />
      {!open.length && (
        <p className="bv-clear">
          <Check /> Rien à traiter
        </p>
      )}
      {open.map((g) => (
        <section className="bv-sec" key={g.name}>
          <h4>
            {g.name} <span className="bv-count">{g.rows.length}</span>
          </h4>
          <Rows rows={g.rows} feed={feed} rich max={6} onDone={ctx.onDone} />
        </section>
      ))}
      <AnimatePresence initial={false}>
        {groups
          .filter((g) => g.kind !== "open")
          .map((g) => (
            <motion.div key={g.name} exit={{ opacity: 0, height: 0 }} transition={{ duration: 0.2, ease: EASE }}>
              <Fold g={g} feed={feed} onDone={ctx.onDone} />
            </motion.div>
          ))}
      </AnimatePresence>
    </>
  );
}

// --- Gallery ------------------------------------------------------------------------

const IMAGE_KEYS = ["photo", "image", "img", "picture", "cover"];
const imageOf = (r: Row) => str(r[IMAGE_KEYS.find((k) => k in r) ?? ""]) || str(Object.values(r).find((v) => typeof v === "string" && v.startsWith("/espace/media")));
const titleOf = (r: Row) => str(r.title ?? r.name ?? r.label);

export function Gallery({ p, ctx }: { p: Row; ctx: Ctx }) {
  const rows = (Array.isArray(p.items) ? (p.items as Row[]) : ctx.feed?.rows) ?? [];
  const units = (p.units ?? {}) as Record<string, string>;
  const by = str(p.by);
  const cats = by ? [...new Set(rows.map((r) => str(r[by])))] : [];
  const [cat, setCat] = useState("");
  const [all, setAll] = useState(false);
  if (ctx.feed?.loading) return <Skeleton kind="Gallery" />;
  if (!rows.length) return <Empty feed={ctx.feed} />;
  const picked = cat ? rows.filter((r) => str(r[by]) === cat) : rows;
  // a long gallery shows two rows' worth, then the rest on demand
  const showing = all ? picked : picked.slice(0, 8);
  const few = rows.length <= 4 && !by;
  return (
    <>
      <Head title={str(p.title)} aside={<span className="bv-head-n">{rows.length}</span>} />
      {cats.length > 1 && (
        <div className="bv-chips" role="tablist">
          {["", ...cats].map((c) => (
            <button key={c || "*"} role="tab" aria-selected={cat === c} className={cat === c ? "on" : ""} onClick={() => setCat(c)}>
              {c || "Tout"}
              <small>{c ? rows.filter((r) => str(r[by]) === c).length : rows.length}</small>
            </button>
          ))}
        </div>
      )}
      <motion.ul className={`bv-gal${few ? " few" : ""}`} layout>
        <AnimatePresence initial={false} mode="popLayout">
          {showing.map((r, i) => (
            <motion.li key={str(r.id) || i} layout initial={{ opacity: 0, scale: 0.96 }} animate={{ opacity: 1, scale: 1 }} exit={{ opacity: 0, scale: 0.96 }} transition={{ duration: 0.18, ease: EASE }}>
              <figure className="bv-tile">
                <span className="bv-tile-img">{imageOf(r) && <img src={imageOf(r)} alt="" loading="lazy" decoding="async" />}</span>
                <figcaption>
                  <span className="bv-tile-title">{titleOf(r)}</span>
                  {Object.keys(units).length > 0 && (
                    <span className="bv-tile-facts">
                      {Object.entries(units).map(([k, u]) => (k in r ? <span key={k}>{shown(r[k], u)}</span> : null))}
                    </span>
                  )}
                </figcaption>
              </figure>
            </motion.li>
          ))}
        </AnimatePresence>
      </motion.ul>
      {picked.length > 8 && (
        <button className="bv-more flush" onClick={() => setAll(!all)}>
          {all ? "Moins" : `Voir les ${picked.length - 8} autres`}
        </button>
      )}
    </>
  );
}

// --- Table --------------------------------------------------------------------------

const HIDDEN = new Set(["id", "status", "changed", "photo", "image"]);
const label = (k: string) => (k === "title" ? "" : k.charAt(0).toUpperCase() + k.slice(1));

export function Table({ p, ctx }: { p: Row; ctx: Ctx }) {
  const rows = (Array.isArray(p.rows) ? (p.rows as Row[]) : ctx.feed?.rows) ?? [];
  const units = (p.units ?? {}) as Record<string, string>;
  if (ctx.feed?.loading) return <Skeleton kind="Table" />;
  if (!rows.length) return <Empty feed={ctx.feed} />;
  const cols = Object.keys(rows[0]).filter((k) => !HIDDEN.has(k) && !k.startsWith("_"));
  const numeric = (k: string) => rows.every((r) => r[k] == null || isNum(r[k]));
  const nodes = !Array.isArray(p.rows);
  return (
    <>
      <Head title={str(p.title)} aside={<span className="bv-head-n">{rows.length}</span>} />
      <div className={`bv-table${cols.length <= 3 ? " slim" : ""}`} role="table">
        <div className="bv-tr bv-th" role="row">
          {cols.map((k) => (
            <span key={k} role="columnheader" className={numeric(k) ? "num" : ""}>
              {label(k)}
            </span>
          ))}
        </div>
        {rows.map((r, i) => (
          <div className={`bv-tr${nodes ? " opens" : ""}`} role="row" key={str(r.id) || i}>
            {cols.map((k, j) => (
              <span key={k} role="cell" className={`${numeric(k) ? "num" : ""}${j === 0 ? " first" : ""}`} data-label={label(k)}>
                {shown(r[k], units[k])}
              </span>
            ))}
          </div>
        ))}
      </div>
    </>
  );
}

// --- Chart --------------------------------------------------------------------------

const DAY = 86_400_000;
export function Chart({ p }: { p: Row }) {
  const series = (p.series ?? {}) as Record<string, number[] | Record<string, number>>;
  const unit = str(p.unit);
  const names = Object.keys(series);
  if (p.kind === "stack") {
    const xs = [...new Set(names.flatMap((n) => Object.keys(series[n] as Record<string, number>)))];
    return (
      <>
        <Head title={str(p.title)} />
        <div className="bv-stack">
          {xs.map((x) => (
            <div className="bv-stack-row" key={x}>
              <span className="bv-stack-x">{x}</span>
              <span className="bv-stack-bar">
                {names.map((n, i) => {
                  const v = (series[n] as Record<string, number>)[x] ?? 0;
                  return <motion.i key={n} className={`s${i}`} initial={{ flexGrow: 0 }} animate={{ flexGrow: v }} transition={{ duration: 0.24, ease: EASE }} title={`${n} ${v} ${unit}`} />;
                })}
              </span>
            </div>
          ))}
          <div className="bv-legend">
            {names.map((n, i) => (
              <span key={n}>
                <i className={`s${i}`} /> {n}
              </span>
            ))}
          </div>
        </div>
      </>
    );
  }
  const vals = (series[names[0]] as number[]) ?? [];
  const lo = Math.min(...vals), hi = Math.max(...vals), span = hi - lo || 1;
  const W = 600, H = 150;
  const pts = vals.map((v, i) => [(i * W) / Math.max(1, vals.length - 1), H - 8 - ((v - lo) * (H - 24)) / span]);
  const line = pts.map((q) => q.join(",")).join(" ");
  const end = p.end ? new Date(str(p.end)) : new Date();
  const start = new Date(end.getTime() - (vals.length - 1) * DAY);
  const d = (x: Date) => x.toLocaleDateString("fr-FR", { day: "numeric", month: "short" });
  const last = vals.at(-1) ?? 0;
  const change = vals.length ? ((last - vals[0]) / vals[0]) * 100 : 0;
  return (
    <>
      <Head title={str(p.title)} aside={<span className="bv-head-n">{shown(+change.toFixed(1), "±%")}</span>} />
      <div className="bv-chart">
        <svg viewBox={`0 0 ${W} ${H}`} preserveAspectRatio="none" aria-hidden="true">
          <defs>
            <linearGradient id="bv-fade" x1="0" x2="0" y1="0" y2="1">
              <stop offset="0" className="stop-a" />
              <stop offset="1" className="stop-b" />
            </linearGradient>
          </defs>
          <polygon className="area" points={`0,${H} ${line} ${W},${H}`} fill="url(#bv-fade)" />
          <motion.polyline className="line" points={line} vectorEffect="non-scaling-stroke" initial={{ pathLength: 0 }} animate={{ pathLength: 1 }} transition={{ duration: 0.24, ease: EASE }} />
        </svg>
        <span className="bv-chart-hi">{shown(hi, unit)}</span>
        <span className="bv-chart-lo">{shown(lo, unit)}</span>
        <div className="bv-chart-x">
          <span>{d(start)}</span>
          <span>{d(end)}</span>
        </div>
      </div>
    </>
  );
}

// --- Timeline, its progress said by itself --------------------------------------------

type Step = { label: string; state: string; date?: string; detail?: string };
export function Timeline({ p }: { p: Row }) {
  const items = (Array.isArray(p.items) ? p.items : []) as Step[];
  const done = items.filter((s) => s.state === "done").length;
  const cur = items.find((s) => s.state === "current");
  const date = (s?: string) => (s ? new Date(s).toLocaleDateString("fr-FR", { day: "numeric", month: "short" }) : "");
  return (
    <>
      <Head title={str(p.title)} />
      {items.some((s) => s.state !== "done") && (
        <div className="bv-prog">
          <div className="bv-prog-words">
            <b>
              {done} sur {items.length}
            </b>
            {cur && <span> · {cur.label}</span>}
          </div>
          <div className="bv-prog-bar" aria-hidden="true">
            {items.map((s, i) => (
              <i key={i} className={s.state} />
            ))}
          </div>
        </div>
      )}
      <ol className="bv-steps">
        {items.map((s) => (
          <motion.li key={s.label} layout="position" className={s.state} transition={{ duration: 0.2, ease: EASE }}>
            <span className="bv-dot" aria-hidden="true">
              {s.state === "done" && <Check />}
            </span>
            <span className="bv-step-main">
              <span className="bv-step-label">{s.label}</span>
              {s.detail && <span className="bv-step-detail">{s.detail}</span>}
            </span>
            {s.date && <time>{date(s.date)}</time>}
          </motion.li>
        ))}
      </ol>
    </>
  );
}

// --- Asks, Quote, Person --------------------------------------------------------------

export function Asks({ p }: { p: Row }) {
  const items = (Array.isArray(p.items) ? p.items : []).map(str);
  const [sent, setSent] = useState("");
  return (
    <div className="bv-asks">
      {items.map((t, i) => (
        <button key={t} className={`bv-ask${i === 0 ? " primary" : ""}${sent === t ? " sent" : ""}`} onClick={() => setSent(t)} disabled={!!sent}>
          {t}
        </button>
      ))}
    </div>
  );
}

export function Quote({ p }: { p: Row }) {
  return (
    <figure className="bv-quote">
      <blockquote>{str(p.text)}</blockquote>
      <figcaption>
        <b>{nameOf(str(p.from))}</b>
        {p.date ? ` · ${relative("fr", p.date)}` : ""}
        {p.url ? <span className="bv-quote-url"> · {new URL(str(p.url)).host}</span> : null}
      </figcaption>
    </figure>
  );
}

export function Person({ p }: { p: Row }) {
  const facts = (Array.isArray(p.facts) ? p.facts : []) as { label: string; value: string }[];
  return (
    <div className="bv-person">
      <Avatar name={str(p.name)} />
      <div>
        <b>{str(p.name)}</b>
        <span className="bv-person-detail">{str(p.detail)}</span>
        {p.note ? <p>{str(p.note)}</p> : null}
        {facts.length > 0 && (
          <dl>
            {facts.map((f) => {
              const href = factLink(f.value);
              return (
                <div key={f.label}>
                  <dt>{f.label}</dt>
                  <dd>{href ? <a href={href}>{f.value}</a> : f.value}</dd>
                </div>
              );
            })}
          </dl>
        )}
      </div>
    </div>
  );
}

// --- dispatch -------------------------------------------------------------------------

export const KINDS: Record<string, (x: { p: Row; ctx: Ctx }) => ReactNode> = { Figures, Sections, Gallery, Table, Chart, Timeline, Asks, Quote, Person };

// Blocks that sit two by two on a wide page: small, self-contained.
export const PAIRS = new Set(["Chart", "Quote", "Person", "Timeline"]);

// useWidth: the page's width, for the few choices CSS cannot make.
export function useWidth<T extends HTMLElement>(): [React.RefObject<T | null>, number] {
  const ref = useRef<T>(null);
  const [w, setW] = useState(0);
  useLayoutEffect(() => {
    if (!ref.current) return;
    const o = new ResizeObserver(([e]) => setW(e.contentRect.width));
    o.observe(ref.current);
    return () => o.disconnect();
  }, []);
  return [ref, w];
}
