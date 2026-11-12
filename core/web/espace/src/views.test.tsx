import { renderToString } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { place } from "./engine";
import { fixtures } from "./fixtures";
import { WordsContext, makeT } from "./i18n";
import strings from "./strings.json";
import { View, type Act } from "./views";

const act: Act = { open: () => {}, answer: () => {}, ask: () => {} };

// Every object of the fixtures, in the shape eriphe-core gives, renders in
// its short and full forms, in both languages, with no word left as a key.
describe("views", () => {
  for (const lang of ["fr", "en"] as const) {
    it(`render every kind (${lang})`, () => {
      const { comp, objects } = fixtures(lang, new Date("2026-11-12T09:00:00"));
      const store = new Map(objects.map((o) => [o.ref, o]));
      const layout = place(comp, store);
      expect(layout.placed.length).toBeGreaterThan(5);
      expect(layout.placed.some((p) => p.obj.ref === "mail:invented")).toBe(false);
      for (const o of objects) {
        for (const [size, full] of [["s", false], ["l", false], ["xl", true]] as const) {
          const html = renderToString(
            <WordsContext.Provider value={{ t: makeT(strings[lang]), lang }}>
              <View o={o} size={size} full={full} all={objects} act={act} />
            </WordsContext.Provider>,
          );
          expect(html, `${o.ref} ${size}`).not.toMatch(/espace\.[a-z]/);
          expect(html).not.toContain("style=");
          expect(html.length).toBeGreaterThan(20);
        }
      }
    });
  }
});
