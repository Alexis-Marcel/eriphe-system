import type { Glance } from "../data/api";
import { BASE, fmt, useGlance, type Style } from "./glance";

// TileFace is what a node's tile shows (prototype): its glance's label
// and variation, live; in style B and C its recent values; in C a muted
// colour for good or bad news (by the glance's good: a rise of the errors
// is bad). It reads the live figures alone: the graph is not drawn again
// at each tick.
export function TileFace({ id, g, style }: { id: string; g: Glance; style: Style }) {
  const l = useGlance((s) => s.live[id]);
  if (!l) return null;
  const { arrow, text } = fmt(g, l.delta);
  const good = (l.delta >= 0) === (g.good === "up");
  const tone = style === "C" ? (good ? " good" : " bad") : "";
  return (
    <span className={`gl-face s${style}${tone}`}>
      <span className="gl-row">
        <span className="gl-label">{g.label}</span>
        <span className="gl-delta">
          <i aria-hidden="true">{arrow}</i>
          {text}
        </span>
      </span>
      {style !== "A" && g.spark && <Spark values={l.series} ref0={BASE[id] ?? l.series[0]} />}
    </span>
  );
}

// Spark draws the day so far as a line, the last value a dot; its scale
// holds the day's start (ref0), so that a little noise never looks like a move.
function Spark({ values, ref0 }: { values: number[]; ref0: number }) {
  const W = 124, H = 18;
  const lo = Math.min(ref0, ...values), hi = Math.max(ref0, ...values);
  const span = hi - lo || 1;
  const pts = values.map((v, i) => [(i / (values.length - 1)) * W, H - 2 - ((v - lo) / span) * (H - 4)] as const);
  const d = pts.map(([x, y], i) => `${i ? "L" : "M"}${x.toFixed(1)},${y.toFixed(1)}`).join("");
  const [lx, ly] = pts[pts.length - 1];
  return (
    <svg className="gl-spark" width={W} height={H} viewBox={`0 0 ${W} ${H}`} aria-hidden="true">
      <path d={`${d}L${W},${H}L0,${H}Z`} className="gl-area" />
      <path d={d} className="gl-line" />
      <circle cx={lx} cy={ly} r="2" className="gl-dot" />
    </svg>
  );
}
