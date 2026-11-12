import type { ReactNode } from "react";

// The catalog's icons (catalog_v1.json, $defs/IconName), drawn here as
// inline SVG: no icon font, nothing loaded. Each is a few strokes on a
// 24 x 24 grid; the stroke and size are the stylesheet's (.a2-icon).

const P = (d: string) => <path d={d} />;

export const ICONS: Record<string, ReactNode> = {
  alert: <>{P("M6 16.5V11a6 6 0 0 1 12 0v5.5l1.5 1.5h-15L6 16.5Z")}{P("M10 20.5a2 2 0 0 0 4 0")}</>,
  arrowDown: <>{P("M12 5v14")}{P("m6 13 6 6 6-6")}</>,
  arrowUp: <>{P("M12 19V5")}{P("m6 11 6-6 6 6")}</>,
  calendar: <>{P("M4 6.5A1.5 1.5 0 0 1 5.5 5h13A1.5 1.5 0 0 1 20 6.5v12a1.5 1.5 0 0 1-1.5 1.5h-13A1.5 1.5 0 0 1 4 18.5v-12Z")}{P("M4 10h16")}{P("M8 3v4")}{P("M16 3v4")}</>,
  chart: <>{P("M4 4v16h16")}{P("M8 16v-4")}{P("M12 16V8")}{P("M16 16v-6")}</>,
  check: P("m5 12.5 4.5 4.5L19 7.5"),
  clock: <><circle cx="12" cy="12" r="8.5" />{P("M12 7.5V12l3 2")}</>,
  close: <>{P("M6 6l12 12")}{P("M18 6 6 18")}</>,
  cloud: P("M7 18.5a4.5 4.5 0 0 1-.6-8.96A6 6 0 0 1 18 9.5a4.5 4.5 0 0 1-.5 9H7Z"),
  document: <>{P("M6 3h8l4 4v14H6V3Z")}{P("M14 3v4h4")}{P("M9 12h6")}{P("M9 16h6")}</>,
  error: <><circle cx="12" cy="12" r="8.5" />{P("m9 9 6 6")}{P("m15 9-6 6")}</>,
  event: <>{P("M4 6.5A1.5 1.5 0 0 1 5.5 5h13A1.5 1.5 0 0 1 20 6.5v12a1.5 1.5 0 0 1-1.5 1.5h-13A1.5 1.5 0 0 1 4 18.5v-12Z")}{P("M4 10h16")}{P("M8 3v4")}{P("M16 3v4")}{P("M8.5 14h3v3h-3z")}</>,
  folder: P("M3 7.5A1.5 1.5 0 0 1 4.5 6H9l2 2h8.5A1.5 1.5 0 0 1 21 9.5v8a1.5 1.5 0 0 1-1.5 1.5h-15A1.5 1.5 0 0 1 3 17.5v-10Z"),
  home: <>{P("M4 11 12 4l8 7")}{P("M6 9.5V20h12V9.5")}{P("M10 20v-5h4v5")}</>,
  info: <><circle cx="12" cy="12" r="8.5" />{P("M12 11v5")}{P("M12 8v.01")}</>,
  link: <>{P("M10 14a4 4 0 0 0 5.66 0l3-3a4 4 0 0 0-5.66-5.66l-1 1")}{P("M14 10a4 4 0 0 0-5.66 0l-3 3a4 4 0 0 0 5.66 5.66l1-1")}</>,
  lock: <>{P("M6 11h12v9H6z")}{P("M8.5 11V8a3.5 3.5 0 0 1 7 0v3")}</>,
  mail: <>{P("M3.5 6.5h17v11h-17z")}{P("m4 7 8 6 8-6")}</>,
  message: P("M4 5.5h16v10H9l-5 4v-14Z"),
  money: <><circle cx="12" cy="12" r="8.5" />{P("M14.5 9.5c-.5-1-1.4-1.5-2.5-1.5-1.5 0-2.5.8-2.5 2s1 1.7 2.5 2 2.5.8 2.5 2-1 2-2.5 2c-1.1 0-2-.5-2.5-1.5")}{P("M12 6.5V8")}{P("M12 16v1.5")}</>,
  note: <>{P("M5 4h14v11l-5 5H5V4Z")}{P("M14 20v-5h5")}{P("M8.5 9h7")}</>,
  person: <><circle cx="12" cy="8.5" r="3.5" />{P("M5 20a7 7 0 0 1 14 0")}</>,
  phone: P("M6.5 4h3l1.5 4-2 1.5a10 10 0 0 0 5.5 5.5L16 13l4 1.5v3a2 2 0 0 1-2 2A15 15 0 0 1 4.5 6a2 2 0 0 1 2-2Z"),
  place: <>{P("M12 21s-6.5-6-6.5-11a6.5 6.5 0 0 1 13 0c0 5-6.5 11-6.5 11Z")}<circle cx="12" cy="10" r="2.3" /></>,
  refresh: <>{P("M19.5 12a7.5 7.5 0 1 1-2.2-5.3")}{P("M19.5 4v4h-4")}</>,
  search: <><circle cx="10.5" cy="10.5" r="6" />{P("m15 15 5 5")}</>,
  server: <>{P("M4 4.5h16v6H4z")}{P("M4 13.5h16v6H4z")}{P("M7.5 7.5h.01")}{P("M7.5 16.5h.01")}</>,
  settings: <><circle cx="12" cy="12" r="3" />{P("M12 3v2.5M12 18.5V21M3 12h2.5M18.5 12H21M5.6 5.6l1.8 1.8M16.6 16.6l1.8 1.8M5.6 18.4l1.8-1.8M16.6 7.4l1.8-1.8")}</>,
  star: P("m12 3.5 2.6 5.4 5.9.8-4.3 4.1 1 5.8L12 16.8l-5.2 2.8 1-5.8-4.3-4.1 5.9-.8L12 3.5Z"),
  task: <>{P("M5 4.5h14v15H5z")}{P("m8.5 12 2.5 2.5 4.5-5")}</>,
  trendDown: <>{P("m3.5 7 6.5 6.5 3.5-3.5 7 7")}{P("M20.5 12v5h-5")}</>,
  trendUp: <>{P("m3.5 17 6.5-6.5 3.5 3.5 7-7")}{P("M15.5 7h5v5")}</>,
  warning: <>{P("M12 3 2.5 20h19L12 3Z")}{P("M12 10v4.5")}{P("M12 17.5v.01")}</>,
};

export const ICON_NAMES = Object.keys(ICONS);

// Icon draws one icon of the set, hidden from assistive technology: what it
// means is said by the text beside it. An unknown name draws nothing.
export function Icon({ name, className = "" }: { name: unknown; className?: string }) {
  const shape = typeof name === "string" ? ICONS[name] : undefined;
  if (!shape) return null;
  return (
    <svg className={`a2-icon ${className}`.trim()} viewBox="0 0 24 24" aria-hidden="true" focusable="false">
      {shape}
    </svg>
  );
}
