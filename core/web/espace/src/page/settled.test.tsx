// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import type { ProposalView } from "../data/api";
import { makeT, WordsContext } from "../i18n";
import strings from "../strings.json";
import { outcomeOf, Settled } from "./Page";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

// What is already decided, in the page's ⋯ menu: each card with how it
// went, done, failed (why: the tool's own words, marked when someone
// else's, else our reason), declined or withdrawn.
const card = (x: Partial<ProposalView>): ProposalView => ({ ref: "calls:1", card: 1, state: "approved", text: "Accepter « Picnic »", ...x });

describe("the cards already decided", () => {
  let el: HTMLDivElement;
  let root: Root;
  beforeEach(() => {
    el = document.createElement("div");
    document.body.append(el);
    root = createRoot(el);
  });
  afterEach(() => {
    act(() => root.unmount());
    el.remove();
  });

  it("says how each went, and why one failed", async () => {
    const cards = [
      card({ ref: "a:1", exec: "done" }),
      card({ ref: "a:2", exec: "failed", execWhy: "error", execError: "the calendar server refused it (403 Forbidden)", execErrorExternal: true }),
      card({ ref: "a:3", exec: "failed", execWhy: "gone" }),
      card({ ref: "a:4", state: "rejected" }),
      card({ ref: "a:5", state: "gone" }),
    ];
    expect(cards.map(outcomeOf)).toEqual(["done", "failed", "failed", "declined", "withdrawn"]);
    await act(async () =>
      root.render(
        <WordsContext.Provider value={{ t: makeT(strings.fr), lang: "fr" }}>
          <Settled cards={cards} />
        </WordsContext.Provider>,
      ),
    );
    const lines = [...el.querySelectorAll(".pg-settled li")];
    expect(lines.map((li) => li.querySelector(".pg-settled-word")?.textContent)).toEqual(["Fait", "Échec", "Échec", "Refusé", "Retiré"]);
    expect(lines[1].className).toBe("failed");
    expect(lines[1].querySelector(".pg-settled-why")?.textContent).toContain("403 Forbidden");
    expect(lines[1].querySelector(".pg-settled-why [role=img]")).not.toBeNull(); // someone else's words, marked
    expect(lines[2].querySelector(".pg-settled-why")?.textContent).toBe("L'outil n'est plus là.");
    expect(lines[0].querySelector(".pg-settled-why")).toBeNull();
  });
});
