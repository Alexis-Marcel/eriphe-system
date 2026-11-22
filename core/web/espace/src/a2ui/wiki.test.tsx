// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { renderToStaticMarkup } from "react-dom/server";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import cases from "../../../../nodefile/testdata/wikilinks.json";
import type { NodePage } from "../data/api";
import { Doc } from "../doc/Doc";
import { makeT, WordsContext } from "../i18n";
import strings from "../strings.json";
import { cells, Markdown, wikiLinks, type Wiki, type WikiTarget } from "./markdown";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

// Links between nodes, written in their text as in Obsidian: [[Title]] and
// [[Title|shown words]].

describe("reading [[links]]", () => {
  // The cases eriphe-core is held to as well (core/nodefile/wikilink_test.go):
  // the links it indexes, and draws on the graph, are those the page draws.
  for (const c of cases as { why: string; text: string; links: { name: string; shown?: string }[] }[]) {
    it(c.why, () => expect(wikiLinks(c.text)).toEqual(c.links));
  }

  it("splits no table cell on a pipe inside a link", () => {
    expect(cells("| [[A|a]] | `x|y` | [[B]] |")).toEqual(["[[A|a]]", "`x", "y`", "[[B]]"]);
  });
});

const targets: Record<string, WikiTarget> = {
  "Choisir l'angle": { id: "n2", title: "Choisir l'angle", named: true },
  "Recherche sur le télétravail": { id: "n3", title: "Recherche sur le télétravail", named: true },
  "Ancien plan": { id: "n4", title: "Ancien plan", named: true, archived: true },
  "Payez ici": { id: "n5", title: "Virement urgent", named: true, external: true },
  "plan": { id: "n6", title: "Plan du premier article", named: true }, // renamed since it was written
};

const wikiWith = (open: (id: string) => void): Wiki => ({
  find: (name) => targets[name],
  open,
  none: "Aucun sujet ne porte ce nom",
  archived: (t) => `${t} (archivé)`,
  external: "Titre écrit après une lecture extérieure",
});

function draw(text: string, wiki?: Wiki): HTMLElement {
  const el = document.createElement("div");
  el.innerHTML = renderToStaticMarkup(<Markdown text={text} newTab="(new tab)" wiki={wiki} />);
  return el.querySelector(".a2-markdown")!;
}

describe("drawing [[links]]", () => {
  it("draws a link to a node by its title now, its words when it has some", () => {
    const md = draw("Voir [[Choisir l'angle]], [[Recherche sur le télétravail|la recherche]] et [[plan]].", wikiWith(() => {}));
    const bs = [...md.querySelectorAll("button.a2-wiki")];
    expect(bs.map((b) => b.textContent)).toEqual(["Choisir l'angle", "la recherche", "Plan du premier article"]);
    expect(bs.map((b) => b.getAttribute("data-node"))).toEqual(["n2", "n3", "n6"]);
    // The real target is its tooltip, and in its name when the words are others.
    expect(bs.map((b) => b.getAttribute("title"))).toEqual(["Choisir l'angle", "Recherche sur le télétravail", "Plan du premier article"]);
    expect(bs[0].getAttribute("aria-label")).toBeNull();
    expect(bs[1].getAttribute("aria-label")).toBe("la recherche (Recherche sur le télétravail)");
  });

  it("greys a name no node has, and an archived node opens nothing", () => {
    const md = draw("[[Nulle part]] et [[Ancien plan]]", wikiWith(() => {}));
    expect(md.querySelector("button")).toBeNull();
    const none = md.querySelector(".a2-wiki.none")!;
    expect(none.textContent).toBe("Nulle part");
    expect(none.getAttribute("title")).toBe("Aucun sujet ne porte ce nom");
    const old = md.querySelector(".a2-wiki.archived")!;
    expect(old.textContent).toBe("Ancien plan");
    expect(old.getAttribute("title")).toBe("Ancien plan (archivé)");
  });

  it("never lets the words of someone else's link pass for its target", () => {
    const md = draw("[[Payez ici|Votre banque]] et [[Payez ici]]", wikiWith(() => {}));
    const bs = [...md.querySelectorAll("button.a2-wiki")];
    // A target whose title may be someone else's words: its title shown as
    // it is, marked (amber, ⚠, the words of why), never replaced.
    expect(bs.map((b) => b.textContent)).toEqual(["Votre banque ⚠", "Virement urgent ⚠"]);
    expect(bs.every((b) => b.classList.contains("ext"))).toBe(true);
    const marked = "Virement urgent ⚠ Titre écrit après une lecture extérieure";
    expect(bs.map((b) => b.getAttribute("title"))).toEqual([marked, marked]);
    expect(bs[0].getAttribute("aria-label")).toBe(`Votre banque (${marked})`);
    expect(bs[1].getAttribute("aria-label")).toBe(marked);
    expect(md.textContent).not.toContain("Contenu extérieur");
  });

  it("is only text inside code, or as written without a page's links", () => {
    expect(draw("`[[Choisir l'angle]]`", wikiWith(() => {})).querySelector("button")).toBeNull();
    const plain = draw("Voir [[Choisir l'angle]].");
    expect(plain.querySelector("button")).toBeNull();
    expect(plain.textContent).toBe("Voir [[Choisir l'angle]].");
  });

  it("is no link inside a link", () => {
    const md = draw("[voir [[Choisir l'angle]]](https://example.org)", wikiWith(() => {}));
    expect(md.querySelector("a button")).toBeNull();
    expect(md.querySelector("a")?.textContent).toContain("voir Choisir l'angle");
  });
});

describe("a page's [[links]]", () => {
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

  const page: NodePage = {
    node: { id: "n1", title: "Écrire l'article", lifetime: "work", status: "idle", created: "", updated: "", version: 3 },
    version: 3,
    versions: [],
    parts: [{ text: "## Les sujets liés\n\n- [[Recherche sur le télétravail|La recherche]]\n- [[Choisir l'angle]]\n- [[L'article]]\n" }],
    cards: [],
    refs: {
      "Recherche sur le télétravail": { id: "n3", title: "Recherche sur le télétravail" },
      "Choisir l'angle": { id: "n2", title: "Choisir l'angle" },
    },
  };

  it("opens the node a link names, beside the page", async () => {
    const open = vi.fn();
    await act(async () =>
      root.render(
        <WordsContext.Provider value={{ t: makeT(strings.fr), lang: "fr" }}>
          <Doc page={page} latest live onOpen={open} />
        </WordsContext.Provider>,
      ),
    );
    const bs = [...el.querySelectorAll<HTMLButtonElement>("button.a2-wiki")];
    expect(bs.map((b) => b.textContent)).toEqual(["La recherche", "Choisir l'angle"]);
    expect(el.querySelector(".a2-wiki.none")?.textContent).toBe("L'article");
    await act(async () => bs[1].click());
    expect(open).toHaveBeenCalledWith("n2");
  });
});
