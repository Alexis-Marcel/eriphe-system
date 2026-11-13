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
