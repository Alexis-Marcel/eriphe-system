// The stylesheet as written (vitest draws no CSS: read as text).
import css from "../espace.css?raw";
import { describe, expect, it } from "vitest";

// A graph's labels read (WCAG AA, 4.5:1) in both themes, a done node's
// too: its core fades, its label is in the faint grey (--fg3), held here
// against the graph's ground, from its middle (--fill) to its edges (--bg).

// theme is the colours a theme block sets: the first block opened by head.
function theme(head: string): Record<string, string> {
  const at = css.indexOf(head);
  expect(at).toBeGreaterThanOrEqual(0);
  const body = css.slice(at, css.indexOf("}", at));
  return Object.fromEntries([...body.matchAll(/(--[\w-]+):\s*(#[0-9a-f]{6})/gi)].map((m) => [m[1], m[2]]));
}

const lum = (hex: string) => {
  const [r, g, b] = [1, 3, 5].map((i) => parseInt(hex.slice(i, i + 2), 16) / 255).map((c) => (c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4));
  return 0.2126 * r + 0.7152 * g + 0.0722 * b;
};
const ratio = (a: string, b: string) => {
  const [x, y] = [lum(a), lum(b)].sort((p, q) => q - p);
  return (x + 0.05) / (y + 0.05);
};

describe("the graph's labels", () => {
  for (const [name, head] of [
    ["light", ":root {"],
    ["dark, chosen", ':root[data-theme="dark"] {'],
    ["dark, the system\'s", ':root:not([data-theme="light"]) {'],
  ]) {
    it(`read on the ground, done or not (${name})`, () => {
      const t = theme(head);
      for (const fg of ["--fg", "--fg2", "--fg3"]) for (const bg of ["--bg", "--fill"]) expect(ratio(t[fg], t[bg]), `${fg} on ${bg}`).toBeGreaterThanOrEqual(4.5);
    });
  }

  it("of a done node take the faint grey, not a fade", () => {
    expect(css).toMatch(/\.gr-node\.done \.gr-label b \{ color: var\(--fg3\); \}/);
  });
});
