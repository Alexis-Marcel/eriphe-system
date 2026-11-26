// A Metric's trend: its last values as a small line under the figure, the
// last one a dot. Drawn in SVG with attributes only (no style: the CSP),
// its colour the stylesheet's; it says nothing a screen reader needs that
// the figure and its change do not.

const W = 120, H = 28, PAD = 2.5;

export function sparkPoints(vals: number[]): [number, number][] {
  if (vals.length < 2) return [];
  const lo = Math.min(...vals), hi = Math.max(...vals);
  const span = hi - lo || 1;
  return vals.map((v, i) => [+(PAD + (i * (W - 2 * PAD)) / (vals.length - 1)).toFixed(2), +(H - PAD - ((v - lo) * (H - 2 * PAD)) / span).toFixed(2)]);
}

export function Spark({ values }: { values: number[] }) {
  const pts = sparkPoints(values);
  if (!pts.length) return null;
  const line = pts.map((p) => p.join(",")).join(" ");
  const [lx, ly] = pts[pts.length - 1];
  const area = `${pts[0][0]},${H} ${line} ${lx},${H}`;
  return (
    <svg className="a2-spark" viewBox={`0 0 ${W} ${H}`} preserveAspectRatio="none" aria-hidden="true" focusable="false">
      <polygon className="a2-spark-area" points={area} />
      <polyline className="a2-spark-line" points={line} vectorEffect="non-scaling-stroke" />
      {/* a dot that stays round however the line is stretched */}
      <polyline className="a2-spark-dot" points={`${lx},${ly} ${lx},${ly}`} vectorEffect="non-scaling-stroke" />
    </svg>
  );
}
