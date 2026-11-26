// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { ProposalsContext, type Proposals, type ProposalView } from "../a2ui/proposals";
import { setReads, type NodePage, type PageAct } from "../data/api";
import { makeT, WordsContext } from "../i18n";
import strings from "../strings.json";
import { Doc } from "./Doc";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

// A do: under a heading (page_acts.go): its line says, in eriphe-core's
// words, what its Oui does to the items ticked under the heading; each
// row it acts on has a box; Oui sends the ids ticked, then the line shows
// the card it made.
const body = `## À jeter
[Jeter ces mails](do:mail/trash)

- [Pub Maison Lune][m1] : publicité
- [Lettre d'info][m2]
- [Autre pub][m3]

## Ailleurs
[Archiver](do:mail/archive)

- [Facture][m4]

[m1]: mail:m1
[m2]: mail:m2
[m3]: mail:m3
[m4]: mail:m4
`;

const pageOf = (): NodePage => ({
  node: { id: "n1", title: "Mes mails", weight: 0.6, status: "waits", created: "", updated: "", version: 2 },
  version: 2,
  versions: [],
  body,
  items: [],
  placed: [],
  cards: [],
});

const trash: PageAct = {
  line: 2,
  text: "[Jeter ces mails](do:mail/trash)",
  tool: "mail/trash",
  items: [
    { id: "m1", label: "m1", line: 4, row: 4, title: "Les clés pour samedi", detail: "Sam <sam@example.org>", misleading: true },
    { id: "m2", label: "m2", line: 5, row: 5, title: "Lettre d'info" },
    { id: "m3", label: "m3", line: 6, row: 6, title: "Autre pub" },
  ],
  acts: ["Mettre 1 mail à la corbeille", "Mettre 2 mails à la corbeille", "Mettre 3 mails à la corbeille"],
  verbs: ["Jeter", "Jeter les 2", "Jeter les 3"],
};
const archive: PageAct = { line: 9, text: "[Archiver](do:mail/archive)", tool: "mail/archive", items: [], refused: "author", why: "Qui a écrit cette action n'a pas cet outil : elle ne fait rien." };

describe("a do: under a heading", () => {
  let el: HTMLDivElement;
  let root: Root;
  let yeses: { line: number; text: string; items: string[] }[];
  beforeEach(() => {
    el = document.createElement("div");
    document.body.append(el);
    root = createRoot(el);
    yeses = [];
    setReads({
      page: async () => null,
      trace: async () => [],
      file: async () => null,
      open: async () => ({}),
      act: async (_id, line) => (line === 2 ? trash : line === 9 ? archive : null),
      actYes: async (_id, line, text, items) => {
        yeses.push({ line, text, items });
        return { card: "calls:21" };
      },
    });
  });
  afterEach(() => {
    act(() => root.unmount());
    el.remove();
    setReads(null);
  });
  const done: ProposalView = { ref: "calls:21", card: 21, kind: "calls", state: "approved", exec: "done", verb: "Jeter", text: "2 retirés" };
  const draw = async (latest = true) => {
    const src: Proposals = { read: async (ref) => (ref === "calls:21" ? done : null), answer: async () => true, follow: () => () => {} };
    await act(async () =>
      root.render(
        <WordsContext.Provider value={{ t: makeT(strings.fr), lang: "fr" }}>
          <ProposalsContext.Provider value={src}>
            <Doc page={pageOf()} latest={latest} live />
          </ProposalsContext.Provider>
        </WordsContext.Provider>,
      ),
    );
    await act(async () => {});
  };
  const row = (line: number) => el.querySelector<HTMLElement>(`.md-item[data-line="${line}"]`)!;
  const head = () => el.querySelector<HTMLElement>(".md-do[data-line='2']")!;

  it("says what its Oui does in code's words, and boxes the rows it acts on", async () => {
    await draw();
    expect(head().querySelector(".a2-inline-what")?.textContent).toBe("Mettre 3 mails à la corbeille");
    expect(head().querySelector(".a2-inline-yes")?.textContent).toBe("Jeter les 3");
    expect(head().textContent).not.toContain("Jeter ces mails");
    for (const line of [4, 5, 6]) expect(row(line).querySelector<HTMLInputElement>("input.md-do-tick")?.checked).toBe(true);
    expect(row(11).querySelector("input.md-do-tick")).toBeNull();
    // The item as its server read it, where the page's words do not say it.
    expect(row(4).querySelector(".md-ref-lie .md-ref-real")?.textContent).toBe("« Les clés pour samedi » · Sam");
  });

  it("leaves out the rows unticked, and shows the card its Oui made", async () => {
    await draw();
    await act(async () => row(5).querySelector<HTMLInputElement>("input.md-do-tick")!.click());
    expect(head().querySelector(".a2-inline-what")?.textContent).toBe("Mettre 2 mails à la corbeille");
    expect(head().querySelector(".md-do-kept")?.textContent).toBe("1 gardé(s)");
    await act(async () => head().querySelector<HTMLButtonElement>(".a2-inline-yes")!.click());
    await act(async () => {});
    expect(yeses).toEqual([{ line: 2, text: "[Jeter ces mails](do:mail/trash)", items: ["m1", "m3"] }]);
    expect(el.querySelector(".md-do[data-line='2'] .a2-inline-state")?.textContent).toBe("Fait");
    expect(row(4).querySelector("input.md-do-tick")).toBeNull();
    expect(row(5).querySelector<HTMLInputElement>("input.md-do-tick")?.checked).toBe(false);
  });

  it("draws a do: that acts on nothing as its words, why in its tooltip", async () => {
    await draw();
    const quiet = el.querySelector<HTMLElement>(".md-do-quiet[data-line='9']");
    expect(quiet?.textContent).toBe("Archiver");
    expect(quiet?.title).toBe(archive.why);
    expect(el.querySelector(".md-do[data-line='9']")).toBeNull();
  });

  it("acts on nothing on an older version", async () => {
    await draw(false);
    expect(el.querySelector(".md-do")).toBeNull();
    expect(el.querySelector("input.md-do-tick")).toBeNull();
    expect(el.querySelector(".md-link-do")?.textContent).toBe("Jeter ces mails");
  });
});
