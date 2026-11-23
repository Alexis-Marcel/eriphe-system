import type { After, Before, Scenario } from "./scenarios";

// What the LLM writes for a page, counted: its node's body as it would
// be in the file, blocks fenced, JSON compact (no spaces), the text the
// same on both sides. name and external are not written in the JSON (the
// fence names the block, code marks what is external). Tokens are an
// estimate (characters / 3.5, about right for French and JSON); the
// characters are exact.

const json = (b: Before | After) => {
  const { name: _n, external: _e, ...rest } = b as Before & After;
  // today's order: components, data, sources; after: sources, components
  return JSON.stringify(rest);
};

export const fence = (b: Before | After) => "```eriphe:block " + b.name + "\n" + json(b) + "\n```\n\n";
export const body = (parts: (string | Before | After)[]) => parts.map((p) => (typeof p === "string" ? p : fence(p))).join("");
export const blocksOnly = (parts: (string | Before | After)[]) => parts.filter((p) => typeof p !== "string").map((p) => fence(p as Before)).join("");

// pretty: the JSON as a person reads it, one component a line.
export function pretty(b: Before | After): string {
  const { name: _n, external: _e, components, ...rest } = b as Before & After;
  const comps = Array.isArray(components) ? "[\n" + components.map((c) => "  " + JSON.stringify(c)).join(",\n") + "\n]" : JSON.stringify(components, null, 1).replace(/\n\s*/g, " ");
  const more = Object.entries(rest).map(([k, v]) => `"${k}": ${JSON.stringify(v)}`);
  return "```eriphe:block " + b.name + "\n{" + [...more, `"components": ${comps}`].join(",\n ") + "}\n```";
}

export type Size = { id: string; title: string; before: number; after: number; beforeBody: number; afterBody: number; beforeComps: number; afterComps: number };

const comps = (parts: (string | Before | After)[]) =>
  parts.reduce((n, p) => n + (typeof p === "string" ? 0 : Array.isArray(p.components) ? p.components.length : 1), 0);

export function sizeOf(s: Scenario): Size {
  return {
    id: s.id,
    title: s.title,
    before: blocksOnly(s.before).length,
    after: blocksOnly(s.after).length,
    beforeBody: body(s.before).length,
    afterBody: body(s.after).length,
    beforeComps: comps(s.before),
    afterComps: comps(s.after),
  };
}

export const tokens = (chars: number) => Math.round(chars / 3.5);
export const pct = (a: number, b: number) => `${b < a ? "−" : "+"}${Math.round((Math.abs(a - b) / a) * 100)} %`;
