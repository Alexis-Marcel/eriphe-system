// @vitest-environment jsdom
import type { ReactNode } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { UnaskedMark } from "../graph/Title";
import { makeT, WordsContext } from "../i18n";
import strings from "../strings.json";
import { Origin } from "./Page";

// The ⋯ menu says who wanted the node, as eriphe-core recorded it; a node
// the user did not ask for has a sign.
const draw = (node: ReactNode) => {
  const el = document.createElement("div");
  el.innerHTML = renderToStaticMarkup(
    <WordsContext.Provider value={{ t: makeT(strings.fr), lang: "fr" }}>{node}</WordsContext.Provider>,
  );
  return el;
};

describe("where a node comes from", () => {
  it("says the user asked for it, without a sign", () => {
    const el = draw(<Origin origin={{ text: "Ouvert à ta demande : « Garde un œil sur mes mails »" }} />);
    expect(el.querySelector("h3")?.textContent).toBe("D'où il vient");
    expect(el.querySelector("p")?.textContent).toBe("Ouvert à ta demande : « Garde un œil sur mes mails »");
    expect(el.querySelector(".pg-origin.unasked")).toBeNull();
    expect(el.textContent).not.toContain("↯");
  });

  it("marks a node a skill opened", () => {
    const el = draw(<Origin origin={{ unasked: true, text: "Ouvert par le skill tri, à son heure, sans demande de ta part." }} />);
    expect(el.querySelector(".pg-origin.unasked p")?.textContent).toBe("↯ Ouvert par le skill tri, à son heure, sans demande de ta part.");
  });

  it("draws the sign, its words in its tooltip", () => {
    const mark = draw(<UnaskedMark />).querySelector(".unasked-mark");
    expect(mark?.textContent).toBe("↯");
    expect(mark?.getAttribute("title")).toBe("Tu ne l'as pas demandé : un skill ou un événement l'a ouvert.");
    expect(mark?.getAttribute("aria-label")).toBe(mark?.getAttribute("title"));
  });
});
