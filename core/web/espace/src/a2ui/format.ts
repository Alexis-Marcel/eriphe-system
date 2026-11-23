import { CommonSchemas, computed, createFunctionImplementation, getValue, type FunctionImplementation, type Signal } from "@a2ui/web_core/v1_0";
import { z } from "zod";
import type { Lang } from "../i18n";

// How the components and the catalog's functions write numbers and dates,
// in the user's language. A date is written as the source wrote it (its
// wall-clock time, whatever the browser's zone): "09:30+01:00" is 09:30.

export const locale = (lang: Lang) => (lang === "en" ? "en-GB" : "fr-FR");

// Wall is an ISO 8601 date or date-time read as written: a Date whose UTC
// fields are the written ones, and whether a time was written.
export type Wall = { at: Date; time: boolean };

const WRITTEN = /^(\d{4})-(\d{2})-(\d{2})(?:[T ](\d{2}):(\d{2})(?::(\d{2}))?)?/;

export function wall(iso: unknown): Wall | null {
  const m = typeof iso === "string" ? WRITTEN.exec(iso) : null;
  if (!m) return null;
  const at = new Date(Date.UTC(+m[1], +m[2] - 1, +m[3], +(m[4] ?? 0), +(m[5] ?? 0), +(m[6] ?? 0)));
  return isNaN(at.getTime()) ? null : { at, time: m[4] !== undefined };
}

// Minutes since midnight, as written.
export const minutesOf = (w: Wall) => w.at.getUTCHours() * 60 + w.at.getUTCMinutes();

export function number(lang: Lang, n: number, decimals?: number): string {
  const d = decimals === undefined ? undefined : Math.max(0, Math.min(10, Math.round(decimals)));
  return new Intl.NumberFormat(locale(lang), { minimumFractionDigits: d, maximumFractionDigits: d ?? 2 }).format(n);
}

// A number given as a number or a numeric string, else undefined.
export function num(v: unknown): number | undefined {
  if (typeof v === "number" && isFinite(v)) return v;
  if (typeof v === "string" && v.trim() !== "" && isFinite(Number(v))) return Number(v);
  return undefined;
}

const fmt = (lang: Lang, o: Intl.DateTimeFormatOptions) => new Intl.DateTimeFormat(locale(lang), { ...o, timeZone: "UTC" });

// clock writes a time of day the one way the workspace does, whatever
// it dates (a page changed, an event, the time it is now): « 9 h 05 » in
// French, « 9:05 am » in English.
export function clock(lang: Lang, hours: number, minutes: number): string {
  const mm = String(minutes).padStart(2, "0");
  if (lang !== "en") return `${hours} h ${mm}`;
  return `${hours % 12 || 12}:${mm} ${hours < 12 ? "am" : "pm"}`;
}

// A time of day as written (its wall clock), the way clock writes it;
// compact, "09:30" in both languages (a chart's axis).
export function time(lang: Lang, w: Wall, compact = false): string {
  if (!compact) return clock(lang, w.at.getUTCHours(), w.at.getUTCMinutes());
  return fmt(lang, { hour: "2-digit", minute: "2-digit", hour12: false }).format(w.at);
}

// A day: "jeudi 12 novembre".
export const day = (lang: Lang, w: Wall) => fmt(lang, { weekday: "long", day: "numeric", month: "long" }).format(w.at);

// A short date: "12 nov.", with the year when it is not this one's.
export function shortDate(lang: Lang, w: Wall, now = new Date()): string {
  const year = w.at.getUTCFullYear() !== now.getFullYear() ? { year: "numeric" as const } : {};
  return fmt(lang, { day: "numeric", month: "short", ...year }).format(w.at);
}

// A date, short, with its time when it has one.
export const when = (lang: Lang, w: Wall, now = new Date()) => (w.time ? `${shortDate(lang, w, now)}, ${time(lang, w)}` : shortDate(lang, w, now));

// stamp says when something happened (an instant eriphe-core gives: a
// page changed, a trace's entry, an archive), in the browser's time:
// today its time alone (« 13 h 13 », « 1:13 pm »), yesterday « hier,
// 13 h 13 », this year the day (« 19 nov. », « Nov 19 »), else with the
// year (« 19 nov. 2025 », « Nov 19, 2025 »). The one way the workspace
// dates what happened; its time is clock's, as every time of day.
export function stamp(lang: Lang, iso: unknown, now = new Date()): string {
  if (typeof iso !== "string" || !iso) return "";
  const d = new Date(iso);
  if (isNaN(d.getTime())) return "";
  const en = lang === "en";
  const midnight = (x: Date) => new Date(x.getFullYear(), x.getMonth(), x.getDate()).getTime();
  const days = Math.round((midnight(now) - midnight(d)) / 86_400_000);
  const hm = clock(lang, d.getHours(), d.getMinutes());
  if (days === 0) return hm;
  if (days === 1) return `${en ? "yesterday" : "hier"}, ${hm}`;
  const year = d.getFullYear() !== now.getFullYear() ? { year: "numeric" as const } : {};
  return new Intl.DateTimeFormat(en ? "en-US" : "fr-FR", { day: "numeric", month: "short", ...year }).format(d);
}

// since says when a page last changed, in its head: within the hour, how
// long ago ("il y a 30 min"), so that it never reads as a second clock
// beside an Agenda's now line; then its stamp.
export function since(lang: Lang, iso: unknown, now = new Date()): string {
  if (typeof iso !== "string" || !iso) return "";
  const ago = (now.getTime() - Date.parse(iso)) / 60_000;
  if (!(ago >= 0 && ago < 60)) return stamp(lang, iso, now);
  const rtf = new Intl.RelativeTimeFormat(locale(lang), { numeric: "auto", style: "short" });
  return ago < 1 ? rtf.format(0, "second") : rtf.format(-Math.floor(ago), "minute");
}

// relative says an instant from now: "il y a 2 heures", "demain". Past a
// week, the date.
export function relative(lang: Lang, iso: unknown, now = Date.now()): string {
  const w = wall(iso);
  if (!w || typeof iso !== "string") return "";
  const at = /(?:Z|[+-]\d{2}:?\d{2})$/.test(iso) ? Date.parse(iso) : Date.parse(w.time ? iso : iso.slice(0, 10) + "T00:00");
  if (isNaN(at)) return "";
  const s = (at - now) / 1000;
  const rtf = new Intl.RelativeTimeFormat(locale(lang), { numeric: "auto" });
  const abs = Math.abs(s);
  if (abs < 45) return rtf.format(0, "second");
  if (abs < 3600) return rtf.format(Math.round(s / 60), "minute");
  if (abs < 86400) return rtf.format(Math.round(s / 3600), "hour");
  if (abs < 7 * 86400) return rtf.format(Math.round(s / 86400), "day");
  return shortDate(lang, w, new Date(now));
}

// pattern writes a date by a Unicode TR35 pattern ("EEEE d MMMM",
// "HH:mm"), as written; text in quotes is kept.
export function pattern(lang: Lang, iso: unknown, pat: string): string {
  const w = wall(iso);
  if (!w) return "";
  const d = w.at;
  const name = (o: Intl.DateTimeFormatOptions) => fmt(lang, o).format(d);
  const two = (n: number) => String(n).padStart(2, "0");
  const h = d.getUTCHours();
  const tokens: Record<string, () => string> = {
    yyyy: () => String(d.getUTCFullYear()),
    yy: () => String(d.getUTCFullYear()).slice(-2),
    MMMM: () => name({ month: "long" }),
    MMM: () => name({ month: "short" }),
    MM: () => two(d.getUTCMonth() + 1),
    M: () => String(d.getUTCMonth() + 1),
    EEEE: () => name({ weekday: "long" }),
    E: () => name({ weekday: "short" }),
    dd: () => two(d.getUTCDate()),
    d: () => String(d.getUTCDate()),
    HH: () => two(h),
    H: () => String(h),
    hh: () => two(h % 12 || 12),
    h: () => String(h % 12 || 12),
    mm: () => two(d.getUTCMinutes()),
    ss: () => two(d.getUTCSeconds()),
    a: () => (h < 12 ? "AM" : "PM"),
  };
  return (pat || "yyyy-MM-dd").replace(/'([^']*)'|yyyy|yy|MMMM|MMM|MM|M|EEEE|E|dd|d|HH|H|hh|h|mm|ss|a/g, (tok, quoted) =>
    quoted !== undefined ? quoted : tokens[tok](),
  );
}

// template splits a formatString text into its literal parts and the
// paths of its ${…} (\${ is a literal "${").
export function template(text: string): (string | { path: string })[] {
  const out: (string | { path: string })[] = [];
  let lit = "";
  for (let i = 0; i < text.length; i++) {
    if (text.startsWith("\\${", i)) {
      lit += "${";
      i += 2;
    } else if (text.startsWith("${", i)) {
      const end = text.indexOf("}", i + 2);
      if (end < 0) {
        lit += text.slice(i);
        break;
      }
      if (lit) out.push(lit);
      lit = "";
      out.push({ path: text.slice(i + 2, end).trim() });
      i = end;
    } else {
      lit += text[i];
    }
  }
  if (lit) out.push(lit);
  return out;
}

const shown = (v: unknown) => (v === null || v === undefined ? "" : typeof v === "object" ? JSON.stringify(v) : String(v));

// The catalog's functions (catalog_v1.json, functions), in the user's
// language. Written here rather than taken from the engine's basic
// catalog, which would bring its whole module along.
export function functions(lang: Lang): FunctionImplementation[] {
  const formatNumber = createFunctionImplementation(
    {
      name: "formatNumber",
      returnType: "string",
      schema: z.object({ value: CommonSchemas.DynamicNumber, decimals: CommonSchemas.DynamicNumber.optional() }).strict(),
    },
    (args) => {
      const n = num(args.value);
      return n === undefined ? "" : number(lang, n, num(args.decimals));
    },
  );
  const formatRelative = createFunctionImplementation(
    { name: "formatRelative", returnType: "string", schema: z.object({ value: CommonSchemas.DynamicValue }).strict() },
    (args) => relative(lang, args.value),
  );
  const formatString = createFunctionImplementation(
    { name: "formatString", returnType: "string", schema: z.object({ value: CommonSchemas.DynamicString }).strict() },
    (args, ctx) => {
      // Each ${path} stays bound: the text follows the data.
      const parts = template(String(args.value ?? "")).map((p) => (typeof p === "string" ? p : ctx.resolveSignal<unknown>({ "@path": p.path })));
      return computed(() => parts.map((p) => (typeof p === "string" ? p : shown(getValue(p as Signal<unknown>)))).join(""));
    },
  );
  const formatDate = createFunctionImplementation(
    { name: "formatDate", returnType: "string", schema: z.object({ value: CommonSchemas.DynamicValue, format: CommonSchemas.DynamicString }).strict() },
    (args) => pattern(lang, args.value, String(args.format ?? "")),
  );
  return [formatString, formatNumber, formatDate, formatRelative];
}
