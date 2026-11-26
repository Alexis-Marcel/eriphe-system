import type { ReactNode } from "react";
import { useWords } from "../i18n";
import { number } from "./format";
import { Spark } from "./spark";

// A page's figures ([!figures], page/callouts.tsx): each value in the
// user's language, its change in ink, a sparkline of its recent values.
// Motion is CSS only (blocks.css), none under prefers-reduced-motion.

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

// A figure as FiguresView draws it: a number is written in the user's
// language, any other value as it is (a page's keeps its bold and links);
// its change in its own unit when it has one (18 385 € ▲ 1,8 %), else
// the value's.
export type FigureItem = { label: ReactNode; value?: number | ReactNode; unit?: string; delta?: number; deltaUnit?: string; deltaLabel?: ReactNode; trend?: number[] };

// FiguresView draws a page's [!figures].
export function FiguresView({ title, items }: { title?: string; items: FigureItem[] }) {
  const { t, lang } = useWords();
  return (
    <div className="a2-figs-block">
      <BlockTitle text={title ?? ""} />
      <div className={`a2-figs n${Math.min(items.length, 4)}`}>
        {items.map((f, i) => {
          const value = typeof f.value === "number" ? number(lang, f.value) : f.value;
          const unit = f.unit ?? "";
          // Its change in ink, and one sign of which way: ▲ up, ▼ down.
          const delta = f.delta;
          const du = f.deltaUnit || unit;
          const size = delta === undefined ? "" : `${number(lang, Math.abs(delta))}${du ? ` ${du}` : ""}`;
          const said = delta === undefined ? "" : t(delta > 0 ? "espace.a2.delta.up" : delta < 0 ? "espace.a2.delta.down" : "espace.a2.delta.flat", size);
          const trend = f.trend ?? [];
          return (
            <div className="a2-fig" key={i}>
              <span className="a2-fig-label">{f.label}</span>
              {/* keyed by its value: a figure that moved comes in again, a
                  faint flash (blocks.css) */}
              <span className="a2-fig-value" key={typeof value === "string" ? value : undefined}>
                {value === "" || value === undefined || value === null ? "—" : value}
                {unit && <small> {unit}</small>}
              </span>
              {(delta !== undefined || f.deltaLabel) && (
                <span className="a2-fig-delta">
                  {delta !== undefined && delta !== 0 && <b aria-hidden="true">{delta > 0 ? "▲" : "▼"} {size}</b>}
                  {said && <span className="sr-only">{said}</span>}
                  {f.deltaLabel ? <> {f.deltaLabel}</> : ""}
                </span>
              )}
              {trend.length > 1 && <Spark values={trend} />}
            </div>
          );
        })}
      </div>
    </div>
  );
}
