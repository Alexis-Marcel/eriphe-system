import type { Glance } from "../data/api";
import type { Lang } from "../i18n";
import { sparkPath, variation } from "./glance";

// TileFace is what a node's tile shows (graph/glance.ts): its glance's
// label and variation, its recent values as a line. Neutral greys only:
// the tile tells that a figure moved, not whether it is good news.
export function TileFace({ g, lang }: { g: Glance; lang: Lang }) {
  const { arrow, text } = variation(g, lang);
  const spark = sparkPath(g, 124, 18);
  return (
    <span className="gl-face" aria-hidden="true">
      <span className="gl-row">
        <span className="gl-label">{g.label}</span>
        <span className="gl-delta">
          <i>{arrow}</i>
          {text}
        </span>
      </span>
      {spark && (
        <svg className="gl-spark" width={124} height={18} viewBox="0 0 124 18">
          <path d={spark.area} className="gl-area" />
          <path d={spark.line} className="gl-line" />
          <circle cx={spark.last[0]} cy={spark.last[1]} r="2" className="gl-dot" />
        </svg>
      )}
    </span>
  );
}
