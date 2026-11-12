import type { Lang } from "../i18n";
import { number, shortDate, time, wall } from "./format";

// The catalog's Chart: one or more series as lines or bars, drawn in SVG
// here (no chart library, nothing that injects styles). Geometry is SVG
// attributes; colours are classes (.a2-s0 … .a2-s3, a palette checked for
// colour blindness on the tile's paper). One axis, recessive grid, a
// legend from two series, a native tooltip on every mark and the numbers
// in a table for screen readers.

export type Point = { x: string | number; y: number };
export type Series = { name: string; points: Point[] };

const W = 320, H = 168;
const PAD = { l: 38, r: 8, t: 8, b: 22 };
const SLOTS = 4; // validated hues; a fifth series and on are grey

const isDate = (x: unknown) => typeof x === "string" && wall(x) !== null;

// cleanSeries keeps what has the catalog's shape: a name and points with a
// finite y.
export function cleanSeries(v: unknown): Series[] {
  if (!Array.isArray(v)) return [];
  return v.flatMap((s) => {
    if (!s || typeof s !== "object") return [];
    const { name, points } = s as { name?: unknown; points?: unknown };
    const pts = (Array.isArray(points) ? points : []).filter(
      (p): p is Point => !!p && typeof p === "object" && (typeof p.x === "string" || typeof p.x === "number") && typeof p.y === "number" && isFinite(p.y),
    );
    return [{ name: typeof name === "string" ? name : "", points: pts }];
  });
}

// The x values in order: numbers and dates sorted, labels as they come.
function categories(series: Series[]): (string | number)[] {
  const seen = new Map<string, string | number>();
  for (const s of series) for (const p of s.points) if (!seen.has(String(p.x))) seen.set(String(p.x), p.x);
  const xs = [...seen.values()];
  if (xs.every((x) => typeof x === "number")) return (xs as number[]).sort((a, b) => a - b);
  if (xs.every(isDate)) return (xs as string[]).sort();
  return xs;
}

// Round ticks covering [lo, hi].
function ticks(lo: number, hi: number, count = 3): number[] {
  if (lo === hi) [lo, hi] = lo === 0 ? [0, 1] : [Math.min(0, lo), Math.max(0, hi)];
  const raw = (hi - lo) / count;
  const mag = 10 ** Math.floor(Math.log10(raw));
  const step = [1, 2, 2.5, 5, 10].map((m) => m * mag).find((s) => s >= raw) ?? raw;
  const out: number[] = [];
  for (let v = Math.floor(lo / step) * step; v <= hi + step * 1e-9; v += step) out.push(+v.toFixed(10));
  if (out[out.length - 1] < hi) out.push(out[out.length - 1] + step);
  return out;
}

export function Chart({ kind, series, unit, lang, label }: { kind: "line" | "bar"; series: Series[]; unit?: string; lang: Lang; label: string }) {
  const xs = categories(series);
  const ys = series.flatMap((s) => s.points.map((p) => p.y));
  if (!xs.length || !ys.length) return null;
  const lo = Math.min(...ys), hi = Math.max(...ys);
  const t = kind === "bar" ? ticks(Math.min(0, lo), Math.max(0, hi)) : ticks(lo, hi);
  const y0 = t[0], y1 = t[t.length - 1];
  const iw = W - PAD.l - PAD.r, ih = H - PAD.t - PAD.b;
  const Y = (v: number) => PAD.t + ih - ((v - y0) / (y1 - y0 || 1)) * ih;
  const idx = new Map(xs.map((x, i) => [String(x), i]));
  const band = iw / xs.length;
  const X = (x: string | number) => PAD.l + (kind === "bar" ? band * (idx.get(String(x)) ?? 0) + band / 2 : xs.length === 1 ? iw / 2 : (iw * (idx.get(String(x)) ?? 0)) / (xs.length - 1));
  // Dates of one day are told by their time, others by their date.
  const oneDay = xs.every((x) => typeof x === "string" && wall(x)?.time && x.slice(0, 10) === String(xs[0]).slice(0, 10));
  const xLabel = (x: string | number) => {
    const w = typeof x === "string" ? wall(x) : null;
    return w ? (oneDay ? time(lang, w) : shortDate(lang, w)) : typeof x === "number" ? number(lang, x) : x;
  };
  const yLabel = (v: number) => number(lang, v);
  const sep = lang === "fr" ? "\u202f: " : ": ";
  const withUnit = (v: number) => (unit ? `${yLabel(v)} ${unit}` : yLabel(v));
  // Labels that fit: every one when they are few and short, else three.
  const few = xs.length <= (xs.every((x) => String(xLabel(x)).length <= 5) ? 7 : 4);
  const shownX = few ? xs : [xs[0], xs[Math.floor((xs.length - 1) / 2)], xs[xs.length - 1]];
  const cls = (i: number) => `a2-s${i < SLOTS ? i : "x"}`;
  const zero = Y(Math.max(y0, Math.min(0, y1)));

  const bars = () => {
    const inner = Math.min(band * 0.72, 18 * series.length + 2 * (series.length - 1));
    const bw = Math.max(2, (inner - 2 * (series.length - 1)) / series.length);
    return series.map((s, si) =>
      s.points.map((p, pi) => {
        const x = X(p.x) - inner / 2 + si * (bw + 2);
        const top = Math.min(Y(p.y), zero), h = Math.max(1, Math.abs(Y(p.y) - zero));
        const r = Math.min(3, bw / 2, h);
        // Rounded at the data end, square on the baseline.
        const d = p.y >= 0
          ? `M${x},${top + h}V${top + r}q0,-${r} ${r},-${r}h${bw - 2 * r}q${r},0 ${r},${r}V${top + h}Z`
          : `M${x},${top}V${top + h - r}q0,${r} ${r},${r}h${bw - 2 * r}q${r},0 ${r},-${r}V${top}Z`;
        return (
          <path key={`${si}.${pi}`} className={`a2-bar ${cls(si)}`} d={d}>
            <title>{`${s.name ? s.name + ", " : ""}${xLabel(p.x)}${sep}${withUnit(p.y)}`}</title>
          </path>
        );
      }),
    );
  };

  const lines = () =>
    series.map((s, si) => {
      const pts = [...s.points].sort((a, b) => (idx.get(String(a.x)) ?? 0) - (idx.get(String(b.x)) ?? 0));
      return (
        <g key={si} className={cls(si)}>
          <polyline className="a2-line" points={pts.map((p) => `${X(p.x).toFixed(1)},${Y(p.y).toFixed(1)}`).join(" ")} />
          {pts.map((p, pi) => (
            <g key={pi}>
              {pts.length <= 24 && <circle className="a2-dot" cx={X(p.x)} cy={Y(p.y)} r="2.5" />}
              <circle className="a2-hit" cx={X(p.x)} cy={Y(p.y)} r="8">
                <title>{`${s.name ? s.name + ", " : ""}${xLabel(p.x)}${sep}${withUnit(p.y)}`}</title>
              </circle>
            </g>
          ))}
        </g>
      );
    });

  return (
    <figure className="a2-chart">
      <svg viewBox={`0 0 ${W} ${H}`} role="img" aria-label={label}>
        <g className="a2-grid" aria-hidden="true">
          {t.map((v) => (
            <g key={v}>
              <line x1={PAD.l} x2={W - PAD.r} y1={Y(v)} y2={Y(v)} className={v === 0 ? "a2-zero" : undefined} />
              <text x={PAD.l - 6} y={Y(v) + 3.5} textAnchor="end">{yLabel(v)}</text>
            </g>
          ))}
          {shownX.map((x, i) => (
            <text key={i} x={X(x)} y={H - 6} textAnchor={kind === "line" && xs.length > 1 ? (x === xs[0] ? "start" : x === xs[xs.length - 1] ? "end" : "middle") : "middle"}>
              {xLabel(x)}
            </text>
          ))}
        </g>
        {kind === "bar" ? bars() : lines()}
      </svg>
      {(series.length > 1 || unit) && (
        <figcaption className="a2-legend">
          {series.length > 1 &&
            series.map((s, i) => (
              <span key={i} className="a2-legend-item">
                <span className={`a2-swatch ${cls(i)}`} aria-hidden="true" />
                {s.name}
              </span>
            ))}
          {unit && <span className="a2-legend-unit">{unit}</span>}
        </figcaption>
      )}
      <table className="sr-only">
        <caption>{label}</caption>
        <thead>
          <tr>
            <th scope="col" />
            {series.map((s, i) => (
              <th key={i} scope="col">{s.name}</th>
            ))}
          </tr>
        </thead>
        <tbody>
          {xs.map((x) => (
            <tr key={String(x)}>
              <th scope="row">{xLabel(x)}</th>
              {series.map((s, i) => {
                const p = s.points.find((q) => String(q.x) === String(x));
                return <td key={i}>{p ? withUnit(p.y) : ""}</td>;
              })}
            </tr>
          ))}
        </tbody>
      </table>
    </figure>
  );
}
