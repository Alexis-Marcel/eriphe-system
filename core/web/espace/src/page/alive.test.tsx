// @vitest-environment jsdom
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import type { PageAlive } from "../data/api";
import { makeT, WordsContext } from "../i18n";
import strings from "../strings.json";
import { Alive } from "./Page";

// The ⋯ menu of a permanent subject says what keeps it alive, as
// eriphe-core says it: its sources, monitors and skills, and when it last
// changed.
const draw = (alive: PageAlive) => {
  const el = document.createElement("div");
  el.innerHTML = renderToStaticMarkup(
    <WordsContext.Provider value={{ t: makeT(strings.fr), lang: "fr" }}>
      <Alive alive={alive} />
    </WordsContext.Provider>,
  );
  return el;
};

describe("what keeps a subject alive", () => {
  it("lists its sources, monitors and skills", () => {
    const el = draw({
      sources: [{ name: "boite", what: "lit mail.boite toutes les 5 min" }],
      monitors: [{ name: "mails-recus", what: "Toutes les 15 min, regarde mail.list" }],
      skills: [{ name: "tri-mails", what: "lancé quand la surveillance « mails-recus » voit quelque chose" }],
      updated: new Date().toISOString(),
    });
    expect(el.querySelector("h3")?.textContent).toBe("Ce qui le fait vivre");
    expect([...el.querySelectorAll("h4")].map((h) => h.textContent)).toEqual(["Sources", "Surveillances", "Skills"]);
    expect(el.querySelector(".pg-alive-sources li")?.textContent).toBe("boite lit mail.boite toutes les 5 min");
    expect(el.querySelector(".pg-alive-skills li b")?.textContent).toBe("tri-mails");
    expect(el.querySelector(".pg-alive-at")?.textContent).toMatch(/^Mis à jour /);
  });

  it("says when nothing keeps it up to date", () => {
    const el = draw({ updated: new Date().toISOString() });
    expect(el.querySelector("h4")).toBeNull();
    expect(el.textContent).toContain("Rien ne le tient à jour");
  });
});
