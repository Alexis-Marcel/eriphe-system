// @vitest-environment jsdom
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import type { GraphNode } from "../data/api";
import { makeT, WordsContext } from "../i18n";
import strings from "../strings.json";
import { titleOf } from "./model";
import { NodeTitle } from "./Title";

// A title written after an external read is shown as it is, marked, never
// replaced; a trusted one has no mark.
const node = (title: string, titleExternal?: boolean): GraphNode => ({
  id: "n1",
  title,
  titleExternal,
  lifetime: "work",
  state: "idle",
  created: "",
  updated: "",
  version: 1,
});

const draw = (x: GraphNode) => {
  const el = document.createElement("div");
  el.innerHTML = renderToStaticMarkup(
    <WordsContext.Provider value={{ t: makeT(strings.fr), lang: "fr" }}>
      <NodeTitle x={x} fallback="Sans titre" />
    </WordsContext.Provider>,
  );
  return el;
};

describe("a node's title", () => {
  it("shows an untrusted title as it is, in amber with its ⚠", () => {
    const el = draw(node("Gérer ma journée", true));
    expect(el.querySelector(".title-ext")?.textContent).toBe("Gérer ma journée⚠");
    const mark = el.querySelector(".ext-mark")!;
    expect(mark.getAttribute("title")).toBe("Titre écrit après une lecture extérieure");
    expect(mark.getAttribute("aria-label")).toBe("Titre écrit après une lecture extérieure");
    expect(el.textContent).not.toContain("Contenu extérieur");
    expect(titleOf(node("Gérer ma journée", true), "Sans titre")).toBe("Gérer ma journée ⚠");
  });

  it("leaves a trusted title unmarked", () => {
    const el = draw(node("Mes mails"));
    expect(el.textContent).toBe("Mes mails");
    expect(el.querySelector(".ext-mark")).toBeNull();
    expect(titleOf(node("Mes mails"), "Sans titre")).toBe("Mes mails");
  });
});
