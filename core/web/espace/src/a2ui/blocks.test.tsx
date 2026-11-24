// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import type { ProposalView, SourceList } from "../data/api";
import { makeT, WordsContext, type Lang } from "../i18n";
import strings from "../strings.json";
import { createAdapter } from "./adapter";
import { groupsOf, rowsFrom } from "./blocks";
import { CATALOG } from "./catalog";
import { ListsContext, type Lists } from "./lists";
import { ProposalsContext, type Proposals } from "./proposals";
import { Surfaces } from "./Surfaces";

// The blocks a model writes as one component: Sections sorts a source's
// items into groups by code's fields, each row with the card eriphe-core
// told of (never the data's), a batch with its yes on its line; Figures,
// Gallery (images only through eriphe-core), Asks (a gesture by its
// place), a Table whose columns and units the page supplies, a Timeline
// that says its progress. Fixed words in the user's language.

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const surface = "n1--mails";
const views: ProposalView[] = [
  { ref: "calls:1", card: 1, verb: "Envoyer", state: "pending", text: "Envoyer la réponse à Sam" },
  { ref: "calls:2", card: 2, verb: "Archiver", state: "pending", text: "Archiver" },
  {
    ref: "calls:3",
    card: 3,
    verb: "Jeter les 3",
    state: "pending",
    text: "Mettre 3 mails à la corbeille",
    choices: ["t1", "t2", "t3"].map((id, i) => ({ id: 30 + i, text: id, n: 1, state: "proposed", items: [id] })),
  },
];
const answered: { ref: string; only?: number[]; left?: number[] }[] = [];
const proposals: Proposals = {
  read: async (ref) => views.find((v) => v.ref === ref) ?? null,
  answer: async (a) => {
    answered.push({ ref: a.ref, only: a.only, left: a.left });
    return true;
  },
  day: async () => [],
  follow: () => () => {},
};

const mails = [
  { id: "m1", subject: "Devis", from: "Sam <sam@example.org>", _note: "Il attend ta réponse", _state: "act", _group: "À faire", _card: "calls:1" },
  // its data names calls:2; code told of no card on it
  { id: "m2", subject: "Réunion", from: "Alex <alex@example.org>", _note: "Pour info", _state: "", _group: "Pour info", _card: "calls:2" },
  { id: "t1", subject: "Pub", from: "shop@example.org", _group: "À jeter" },
  { id: "t2", subject: "Lettre", from: "news@example.org", _group: "À jeter" },
  { id: "t3", subject: "Promo", from: "deals@example.org", _group: "À jeter" },
  { id: "d1", subject: "Réglé", from: "Sam <sam@example.org>", _state: "done", _group: "À faire" },
];
const told: SourceList = {
  component: "root",
  source: "mails",
  items: [
    { n: 0, id: "m1", title: "Devis", detail: "Sam <sam@example.org>", card: "calls:1" },
    { n: 1, id: "m2", title: "Réunion", detail: "Alex <alex@example.org>" },
    { n: 2, id: "t1", title: "Pub", card: "calls:3" },
    { n: 3, id: "t2", title: "Lettre", card: "calls:3" },
    { n: 4, id: "t3", title: "Promo", card: "calls:3" },
    { n: 5, id: "d1", title: "Réglé" },
  ],
};

let el: HTMLDivElement;
let root: Root;
let asked: [string, string][] = [];

async function mount(lang: Lang, components: Record<string, unknown>[], dataModel: Record<string, unknown> = {}, lists: Partial<Lists> = {}) {
  const ls: Lists = {
    list: (s, c) => (s === surface && c === told.component ? told : undefined),
    ask: (s, c) => asked.push([s, c]),
    ...lists,
  };
  const adapter = createAdapter(lang);
  root = createRoot(el);
  await act(async () =>
    root.render(
      <WordsContext.Provider value={{ t: makeT(strings[lang]), lang }}>
        <ProposalsContext.Provider value={proposals}>
          <ListsContext.Provider value={ls}>
            <Surfaces adapter={adapter} />
          </ListsContext.Provider>
        </ProposalsContext.Provider>
      </WordsContext.Provider>,
    ),
  );
  await act(async () =>
    adapter.applyMessages([{ version: "1.0", createSurface: { surfaceId: surface, catalogId: CATALOG.id, components, dataModel } }]),
  );
  await act(async () => new Promise((r) => setTimeout(r, 0)));
}

beforeEach(() => {
  el = document.createElement("div");
  document.body.append(el);
  answered.length = 0;
  asked = [];
});
afterEach(() => {
  act(() => root.unmount());
  el.remove();
});

const $ = <T extends Element = HTMLElement>(s: string) => el.querySelector<T>(s);
const $$ = <T extends Element = HTMLElement>(s: string) => [...el.querySelectorAll<T>(s)];
// a number's spaces as people read them
const texts = (s: string) => $$(s).map((n) => n.textContent?.replace(/[  ]/g, " "));
const sections = [{ id: "root", component: "Sections", title: "Courrier", items: { "@path": "/mails" } }];

describe("Sections", () => {
  it("sorts the rows by code's fields: groups to act on open first, a batch folded with its yes, the rest folded, done rows gone", async () => {
    await mount("fr", sections, { mails });
    expect($(".a2-head-title")?.textContent).toBe("Courrier");
    expect(texts(".a2-sec-head")).toEqual(["À faire 1"]);
    // A rich row: Eriphe's note first, then who and what.
    expect($(".a2-sec .a2-sec-title")?.textContent).toBe("Il attend ta réponse");
    expect($(".a2-sec .a2-sec-sub")?.textContent).toBe("Sam · Devis");
    expect($(".a2-sec .a2-sec-av")?.textContent).toBe("S");
    expect(texts(".a2-sec-fold-name")).toEqual(["À jeter", "Pour info"]);
    expect(el.textContent).not.toContain("Réglé");
  });

  it("puts on each row the card code told of, never one its data names, and the yes says its verb", async () => {
    await mount("fr", sections, { mails });
    // m2's data names calls:2: no yes for it; the batch's yes is on its line.
    expect(texts(".a2-inline-yes")).toEqual(["Envoyer", "Jeter les 3"]);
    await act(async () => $<HTMLButtonElement>(".a2-sec .a2-inline-yes")!.click());
    expect(answered.map((a) => a.ref)).toEqual(["calls:1"]);
  });

  it("opens a batch with its rows ticked, and its yes leaves out what is unticked", async () => {
    await mount("fr", sections, { mails });
    const head = $<HTMLButtonElement>(".a2-sec-fold.batch .a2-sec-fold-head")!;
    expect(head.getAttribute("aria-expanded")).toBe("false");
    await act(async () => head.click());
    const boxes = $$<HTMLInputElement>(".a2-sec-fold.batch input.a2-tick");
    expect(boxes.map((b) => b.checked)).toEqual([true, true, true]);
    await act(async () => boxes[1].click());
    expect($(".a2-sec-fold.batch .a2-batch-kept")?.textContent).toBe("1 gardé(s)");
    await act(async () => $<HTMLButtonElement>(".a2-sec-fold.batch .a2-inline-yes")!.click());
    expect(answered).toEqual([{ ref: "calls:3", only: [30, 32], left: [31] }]);
  });

  it("gives a batch no boxes when its card is one call over all its rows", async () => {
    const one: SourceList = { ...told, items: told.items.map((it) => (it.card === "calls:3" ? { ...it, card: "calls:2" } : it)) };
    await mount("fr", sections, { mails }, { list: () => one });
    await act(async () => $<HTMLButtonElement>(".a2-sec-fold.batch .a2-sec-fold-head")!.click());
    expect($$(".a2-sec-fold.batch .a2-sec-row")).toHaveLength(3);
    expect($$(".a2-sec-fold.batch input.a2-tick")).toHaveLength(0);
    expect($(".a2-sec-fold.batch .a2-inline-yes")?.textContent).toBe("Archiver");
  });

  it("says when nothing is left to act on, when it is empty, and while it reads", async () => {
    await mount("fr", sections, { mails: mails.filter((m) => m._group !== "À faire") }, { list: () => undefined });
    expect($(".a2-secs-clear")?.textContent).toBe("Rien à traiter.");
    act(() => root.unmount());
    await mount("en", sections, { mails: [] }, { list: () => undefined });
    expect($(".a2-none")?.textContent).toBe("Nothing here yet");
    act(() => root.unmount());
    await mount("en", sections, {});
    expect($(".a2-sk")?.getAttribute("aria-busy")).toBe("true");
    expect($(".a2-sk")?.getAttribute("aria-label")).toBe("Reading…");
  });

  it("shows six rows of a group, then « n de plus »", async () => {
    const many = Array.from({ length: 9 }, (_, i) => ({ id: `x${i}`, subject: `Mail ${i}`, _state: "look", _group: "À lire" }));
    await mount("fr", sections, { mails: many }, { list: () => undefined });
    expect($$(".a2-sec .a2-sec-row")).toHaveLength(6);
    const more = $<HTMLButtonElement>(".a2-sec-more")!;
    expect(more.textContent).toBe("3 de plus");
    await act(async () => more.click());
    expect($$(".a2-sec .a2-sec-row")).toHaveLength(9);
  });

  it("groups by code's fields alone", () => {
    const rows = rowsFrom(mails, told);
    expect(groupsOf(rows, "Le reste").map((g) => [g.name, g.kind, g.rows.length])).toEqual([
      ["À faire", "open", 1],
      ["À jeter", "batch", 3],
      ["Pour info", "folded", 1],
    ]);
    expect(groupsOf(rowsFrom([{ id: "a", title: "A" }], undefined), "Le reste").map((g) => g.name)).toEqual(["Le reste"]);
  });
});

describe("Figures", () => {
  for (const lang of ["fr", "en"] as const) {
    it(`writes numbers, units and changes in the language (${lang})`, async () => {
      await mount(lang, [
        { id: "root", component: "Figures", items: [{ label: "PEA", value: 18385, unit: "€", delta: 1.8, deltaLabel: "cette semaine", trend: [1, 2, 3] }, { label: "Lignes", value: 6 }, { label: "Prochain", value: "Jeudi" }] },
      ]);
      const fr = lang === "fr";
      expect(texts(".a2-fig-value")).toEqual([fr ? "18 385 €" : "18,385 €", "6", "Jeudi"]);
      expect($(".a2-fig-delta b")?.textContent).toBe(fr ? "▲ 1,8 €" : "▲ 1.8 €");
      expect($(".a2-fig-delta .sr-only")?.textContent).toBe(fr ? "en hausse de 1,8 €" : "up 1.8 €");
      expect($$(".a2-fig svg")).toHaveLength(1);
      expect($(".a2-figs")?.className).toContain("n3");
    });
  }
});

describe("Gallery", () => {
  const pieces = Array.from({ length: 10 }, (_, i) => ({
    id: `p${i}`,
    title: `Pièce ${i}`,
    kind: i < 7 ? "Hauts" : "Bas",
    note: 4,
    photo: i === 0 ? "https://example.org/a.jpg" : `/espace/media?u=${i}`,
  }));
  it("loads an image only through eriphe-core, filters by a field, and shows eight, then the others", async () => {
    await mount("fr", [{ id: "root", component: "Gallery", title: "Garde-robe", items: { "@path": "/pieces" }, by: "kind", units: { note: "★" } }], { pieces });
    // The model's own address is never fetched.
    expect($$(".a2-tile-img img").map((i) => i.getAttribute("src"))).not.toContain("https://example.org/a.jpg");
    expect($$(".a2-tile")).toHaveLength(8);
    expect($(".a2-stars")?.getAttribute("aria-label")).toBe("4 sur 5");
    expect(texts(".a2-chip")).toEqual(["Tout10", "Hauts7", "Bas3"]);
    const more = $<HTMLButtonElement>(".a2-gal-block .a2-sec-more")!;
    expect(more.textContent).toBe("Voir les 2 autres");
    await act(async () => more.click());
    expect($$(".a2-tile")).toHaveLength(10);
    await act(async () => $$<HTMLButtonElement>(".a2-chip")[2].click());
    expect(texts(".a2-tile-title")).toEqual(["Pièce 7", "Pièce 8", "Pièce 9"]);
  });

  it("opens a node of the graph its row names", async () => {
    const opened: string[] = [];
    await mount("en", [{ id: "root", component: "Gallery", items: [{ id: "n7", title: "Recipe" }] }], {}, {
      node: (id) => (id === "n7" ? ({ id } as never) : undefined),
      openNode: (id) => opened.push(id),
    });
    await act(async () => $<HTMLButtonElement>(".a2-tile-open")!.click());
    expect(opened).toEqual(["n7"]);
  });
});

describe("Asks", () => {
  it("sends an ask by its place, never acts, and keeps the places of the asks code hid", async () => {
    await mount("fr", [{ id: "root", component: "Asks", items: ["", "Écris-le pour des managers", { text: "Brancher l'agenda", until: { tool: "calendar" } }] }]);
    const buttons = $$<HTMLButtonElement>(".a2-ask");
    expect(buttons.map((b) => b.textContent)).toEqual(["Écris-le pour des managers", "Brancher l'agenda"]);
    expect(buttons[0].className).toContain("a2-button-primary");
    await act(async () => buttons[1].click());
    expect(asked).toEqual([[surface, "root#2"]]);
    expect(answered).toEqual([]);
  });

  it("sends nothing where the page takes no ask (an older version)", async () => {
    await mount("fr", [{ id: "root", component: "Asks", items: ["Vas-y avec le plan B"] }], {}, { ask: undefined });
    await act(async () => $<HTMLButtonElement>(".a2-ask")!.click());
    expect(asked).toEqual([]);
  });
});

describe("Table and Timeline", () => {
  it("a Table without columns shows its rows' fields, with their units, numbers to the right", async () => {
    await mount("fr", [{ id: "root", component: "Table", title: "Recettes", rows: { "@path": "/r" }, units: { note: "★", temps: "min", prix: "€" } }], {
      r: [
        { id: "x", title: "Soupe", note: 4, temps: 35, prix: 6.5 },
        { id: "y", title: "Gratin", note: 5, temps: 70, prix: 8 },
      ],
    });
    expect($(".a2-head-title")?.textContent).toBe("Recettes");
    expect(texts(".a2-table thead th")).toEqual(["", "Note", "Temps", "Prix"]);
    const cells = [...$(".a2-table tbody tr")!.querySelectorAll("td")];
    expect(cells.map((c) => c.textContent)).toEqual(["Soupe", "", "35 min", "6,5 €"]);
    expect(cells[1].querySelector(".a2-stars")?.getAttribute("aria-label")).toBe("4 sur 5");
  });

  it("a Timeline says where the work stands, while steps are left", async () => {
    const items = [
      { label: "Devis", state: "done" },
      { label: "Acompte", state: "current" },
      { label: "Cartons", state: "pending" },
    ];
    await mount("fr", [{ id: "root", component: "Timeline", title: "Déménagement", items }]);
    expect($(".a2-tl-prog-words")?.textContent).toBe("1 sur 3 · Acompte");
    expect($$(".a2-tl-prog .a2-bar > span").map((s) => s.className)).toEqual(["a2-seg-done", "a2-seg-now", "a2-seg"]);
    act(() => root.unmount());
    await mount("en", [{ id: "root", component: "Timeline", items: items.map((it) => ({ ...it, state: "done" })) }]);
    expect($(".a2-tl-prog")).toBeNull();
  });
});
