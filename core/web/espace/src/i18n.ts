import { createContext, useContext } from "react";

// The app's words come from eriphe-core (web_text_espace.go, through
// /espace/boot); strings.json is their copy for the dev server. A word with
// %s or %d takes its arguments in order.

export type Lang = "fr" | "en";
export type T = (key: string, ...args: (string | number)[]) => string;

export function makeT(words: Record<string, string>): T {
  return (key, ...args) => {
    let i = 0;
    return (words[key] ?? key).replace(/%[sd]/g, () => String(args[i++] ?? ""));
  };
}

export type Words = { t: T; lang: Lang };

export const WordsContext = createContext<Words>({ t: makeT({}), lang: "fr" });
export const useWords = () => useContext(WordsContext);

const locale = (lang: Lang) => (lang === "en" ? "en-GB" : "fr-FR");

// A time of day, "09:30" or "09 h 30" as each language writes it.
export function clock(lang: Lang, iso: string): string {
  const hm = iso.length > 5 ? iso.slice(11, 16) : iso;
  return lang === "fr" ? hm.replace(":", " h ") : hm;
}

export function dayLabel(lang: Lang, date: string): string {
  return new Date(date + "T12:00").toLocaleDateString(locale(lang), { weekday: "long", day: "numeric", month: "long" });
}

export function shortDate(lang: Lang, date: string): string {
  return new Date(date.slice(0, 10) + "T12:00").toLocaleDateString(locale(lang), { day: "numeric", month: "short" });
}
