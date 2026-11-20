// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import type { PickAction, PickList } from "../data/api";
import { pickLists } from "../doc/blocks";
import type { NodePage } from "../data/api";
import { makeT, WordsContext } from "../i18n";
import strings from "../strings.json";
import { createAdapter, type Adapter } from "./adapter";
import { CATALOG } from "./catalog";
import { buttonText, PicksContext, type Picks } from "./picks";
import { Surfaces } from "./Surfaces";

// Picking items in a List bound to a source's items, and running an
// action on them: the boxes, « tout sélectionner », the bar only once
// something is picked, the buttons' words as eriphe-core built them, the
// confirm of a tool that throws away, the line of what was done.

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const archive: PickAction = {
  name: "archive",
  one: "Déplacer 1 élément → archive (mail/move)",
  many: "Déplacer {n} éléments → archive (mail/move)",
  tool: "mail/move",
  args: ["to : archive"],
  hint: "Ranger",
};
const trash: PickAction = { name: "trash", one: "Mettre 1 élément à la corbeille (mail/trash)", many: "Mettre {n} éléments à la corbeille (mail/trash)", tool: "mail/trash", confirm: true };

const list: PickList = {
  component: "root",
  source: "inbox",
  items: [
    { n: 0, id: "m1", title: "Facture", detail: "Sam" },
    { n: 1, id: "m2", title: "Relance", detail: "Lou" },
    { n: 2, id: "m3", title: "Lettre" },
  ],
  actions: [archive, trash],
};

const surface = "n1--mails";
const messages = [
  {
    version: "1.0" as const,
    createSurface: {
      surfaceId: surface,
      catalogId: CATALOG.id,
      components: [
        { id: "root", component: "List", children: { path: "/inbox/messages", componentId: "row" } },
        { id: "row", component: "Text", text: { "@path": "subject" } },
      ],
      dataModel: { inbox: { messages: [{ id: "m1", subject: "Facture" }, { id: "m2", subject: "Relance" }, { id: "m3", subject: "Lettre" }] } },
    },
  },
];

let el: HTMLDivElement;
let root: Root;
let adapter: Adapter;
let runs: { action: string; ids: string[] }[];
let answer: { ok: boolean; text: string };

async function mount(withPicks = true) {
  const picks: Picks = {
    list: (s, c) => (s === surface && c === list.component ? list : undefined),
    kept: new Map(),
    run: async (_s, _l, a, ids) => {
      runs.push({ action: a.name, ids });
      return answer;
    },
  };
  adapter = createAdapter("fr");
  root = createRoot(el);
  await act(async () =>
    root.render(
      <WordsContext.Provider value={{ t: makeT(strings.fr), lang: "fr" }}>
        <PicksContext.Provider value={withPicks ? picks : null}>
          <Surfaces adapter={adapter} />
        </PicksContext.Provider>
      </WordsContext.Provider>,
    ),
  );
  await act(async () => adapter.applyMessages(messages));
}

const boxes = () => [...el.querySelectorAll<HTMLInputElement>(".a2-pickable .a2-pick")];
const bar = () => el.querySelector(".a2-pickbar");
const buttons = () => [...el.querySelectorAll<HTMLButtonElement>(".a2-pickact")];
const click = (e: Element | undefined) => act(async () => (e as HTMLElement).click());

beforeEach(() => {
  el = document.createElement("div");
  document.body.append(el);
  runs = [];
  answer = { ok: true, text: "2 déplacés" };
});

afterEach(() => {
  act(() => root.unmount());
  adapter.dispose();
  el.remove();
});

describe("picking items in a list", () => {
  it("shows a box per row, and no bar before something is picked", async () => {
    await mount();
    expect(boxes()).toHaveLength(3);
    expect(boxes()[1].getAttribute("aria-label")).toBe("Choisir « Relance »");
    expect(bar()).toBeNull();
    expect(el.textContent).toContain("Tout sélectionner");
  });

  it("is a plain list where nothing may be picked", async () => {
    await mount(false);
    expect(boxes()).toHaveLength(0);
    expect(el.querySelector(".a2-list")).not.toBeNull();
  });

  it("says the buttons in code's words, and runs exactly the picked ids", async () => {
    await mount();
    await click(boxes()[0]);
    expect(buttons().map((b) => b.textContent)).toEqual(["Déplacer 1 élément → archive (mail/move)Ranger", "Mettre 1 élément à la corbeille (mail/trash)"]);
    await click(boxes()[2]);
    expect(bar()?.textContent).toContain("2 sélectionné(s)");
    expect(buttons()[0].firstChild?.textContent).toBe("Déplacer 2 éléments → archive (mail/move)");
    await click(buttons()[0]);
    expect(runs).toEqual([{ action: "archive", ids: ["m1", "m3"] }]);
    expect(el.querySelector(".a2-pickdone")?.textContent).toBe("2 déplacés");
    expect(bar()).toBeNull(); // done: nothing picked any more
  });

  it("asks once more for a tool that throws away, with its exact call", async () => {
    await mount();
    await click(el.querySelector(".a2-pickall .a2-pick")!);
    expect(bar()?.textContent).toContain("3 sélectionné(s)");
    await click(buttons()[1]);
    expect(runs).toEqual([]);
    const ask = el.querySelector(".a2-pickask")!;
    expect(ask.textContent).toContain("mail/trash");
    expect(ask.textContent).toContain("Facture · Sam");
    expect(ask.textContent).toContain("Lettre");
    await click([...ask.querySelectorAll("button")].find((b) => b.textContent === "Lancer"));
    expect(runs).toEqual([{ action: "trash", ids: ["m1", "m2", "m3"] }]);
  });

  it("keeps the pick when the page draws its blocks again", async () => {
    await mount();
    await click(boxes()[1]);
    await act(async () => adapter.applyMessages([{ version: "1.0", deleteSurface: { surfaceId: surface } }]));
    expect(boxes()).toHaveLength(0);
    await act(async () => adapter.applyMessages(messages));
    expect(boxes().map((b) => b.checked)).toEqual([false, true, false]);
    expect(bar()?.textContent).toContain("1 sélectionné(s)");
  });

  it("keeps the pick and says why when nothing was done", async () => {
    answer = { ok: false, text: "Cet outil n'est jamais permis : rien n'a été fait." };
    await mount();
    await click(boxes()[1]);
    await click(buttons()[0]);
    expect(el.querySelector(".a2-pickdone.failed")?.textContent).toContain("jamais permis");
    expect(boxes()[1].checked).toBe(true);
  });
});

// A view in sections: two Lists of the same source, each filtered by
// eriphe-core (its rows), the first with an action that throws away.
const del: PickAction = {
  name: "del",
  one: "Supprimer 1 élément (mail/delete)",
  many: "Supprimer {n} éléments (mail/delete)",
  all: "Supprimer les {n} (mail/delete)",
  tool: "mail/delete",
  confirm: true,
};
const drop: PickList = {
  component: "drop",
  source: "inbox",
  filtered: true,
  items: [
    { n: 0, id: "m1", title: "Facture", detail: "Sam" },
    { n: 2, id: "m3", title: "Lettre" },
  ],
  actions: [del],
};
const info: PickList = { component: "info", source: "inbox", filtered: true, items: [{ n: 1, id: "m2", title: "Relance" }], actions: [] };
const sections = [
  {
    version: "1.0" as const,
    createSurface: {
      surfaceId: surface,
      catalogId: CATALOG.id,
      components: [
        { id: "root", component: "Column", children: ["drop", "info"] },
        { id: "drop", component: "List", children: { path: "/inbox/messages", componentId: "row" } },
        { id: "info", component: "List", children: { path: "/inbox/messages", componentId: "row" }, emptyText: "Rien" },
        { id: "row", component: "Text", text: { "@path": "subject" } },
      ],
      dataModel: { inbox: { messages: [{ id: "m1", subject: "Facture" }, { id: "m2", subject: "Relance" }, { id: "m3", subject: "Lettre" }] } },
    },
  },
];

describe("a view in sections", () => {
  let lists: Record<string, PickList>;
  const mountSections = async (live = true) => {
    const picks: Picks = {
      list: (s, c) => (s === surface ? lists[c] : undefined),
      kept: new Map(),
      run: live
        ? async (_s, l, a, ids) => {
            runs.push({ action: `${l.component}:${a.name}`, ids });
            return answer;
          }
        : undefined,
    };
    adapter = createAdapter("fr");
    root = createRoot(el);
    await act(async () =>
      root.render(
        <WordsContext.Provider value={{ t: makeT(strings.fr), lang: "fr" }}>
          <PicksContext.Provider value={picks}>
            <Surfaces adapter={adapter} />
          </PicksContext.Provider>
        </WordsContext.Provider>,
      ),
    );
    await act(async () => adapter.applyMessages(sections));
  };
  const section = (i: number) => el.querySelectorAll(".a2-column > *")[i] as HTMLElement;
  const texts = (e: Element) => [...e.querySelectorAll(".a2-text")].map((x) => x.textContent);
  beforeEach(() => {
    lists = { drop, info };
  });

  it("shows in each List only the rows eriphe-core kept, and keeps the picks", async () => {
    await mountSections();
    expect(texts(section(0))).toEqual(["Facture", "Lettre"]);
    expect(texts(section(1))).toEqual(["Relance"]);
    // Only the section with an action is picked in.
    expect(section(0).querySelectorAll(".a2-pickable .a2-pick")).toHaveLength(2);
    expect(section(1).querySelectorAll(".a2-pick")).toHaveLength(0);
    await click(section(0).querySelectorAll<HTMLInputElement>(".a2-pickable .a2-pick")[1]);
    expect(bar()?.textContent).toContain("1 sélectionné(s)");
    await click(buttons()[0]);
    expect(el.querySelector(".a2-pickask")?.textContent).toContain("Lettre");
    expect(el.querySelector(".a2-pickask")?.textContent).not.toContain("Facture");
  });

  it("acts on all a section's rows in one click, confirming the very items, and sends those", async () => {
    await mountSections();
    const whole = [...el.querySelectorAll<HTMLButtonElement>(".a2-pickwhole")];
    expect(whole.map((b) => b.textContent)).toEqual(["Supprimer les 2 (mail/delete)"]);
    await click(whole[0]);
    expect(runs).toEqual([]);
    const ask = el.querySelector(".a2-pickask")!;
    expect([...ask.querySelectorAll("li")].map((li) => li.textContent)).toEqual(["Facture · Sam", "Lettre"]);
    // A mail comes in the section while the confirm is open: not taken.
    lists = { drop: { ...drop, items: [...drop.items, { n: 3, id: "m4", title: "Nouveau" }] }, info };
    await act(async () => adapter.applyMessages([{ version: "1.0", updateDataModel: { surfaceId: surface, path: "/inbox/messages/3", value: { id: "m4", subject: "Nouveau" } } }]));
    await click([...ask.querySelectorAll("button")].find((b) => b.textContent === "Lancer"));
    expect(runs).toEqual([{ action: "drop:del", ids: ["m1", "m3"] }]);
  });

  it("keeps the confirm open when the page draws its blocks again", async () => {
    await mountSections();
    await click(el.querySelector(".a2-pickwhole")!);
    await act(async () => adapter.applyMessages([{ version: "1.0", deleteSurface: { surfaceId: surface } }]));
    await act(async () => adapter.applyMessages(sections));
    const ask = el.querySelector(".a2-pickask")!;
    expect([...ask.querySelectorAll("li")].map((li) => li.textContent)).toEqual(["Facture · Sam", "Lettre"]);
    await click([...ask.querySelectorAll("button")].find((b) => b.textContent === "Lancer"));
    expect(runs).toEqual([{ action: "drop:del", ids: ["m1", "m3"] }]);
  });

  it("shows the filtered rows on an older version, with nothing to pick", async () => {
    await mountSections(false);
    expect(texts(section(0))).toEqual(["Facture", "Lettre"]);
    expect(el.querySelectorAll(".a2-pick")).toHaveLength(0);
    expect(el.querySelector(".a2-pickwhole")).toBeNull();
  });

  it("says its empty text when the filter keeps nothing", async () => {
    lists = { drop, info: { ...info, items: [] } };
    await mountSections();
    expect(el.querySelector(".a2-empty")?.textContent).toBe("Rien");
  });
});

describe("buttonText", () => {
  it("is code's words, one or many", () => {
    expect(buttonText(archive, 1)).toBe("Déplacer 1 élément → archive (mail/move)");
    expect(buttonText(archive, 4)).toBe("Déplacer 4 éléments → archive (mail/move)");
  });
});

describe("pickLists", () => {
  it("takes the page's lists, their rows the stream's latest, by List", () => {
    const page = {
      node: { id: "n1" },
      version: 3,
      versions: [],
      cards: [],
      parts: [{ block: { name: "mails", components: [], data: {}, lists: [list] } }, { block: { name: "other", components: [], data: {} } }],
    } as unknown as NodePage;
    const fresh = [{ n: 0, id: "m9", title: "Nouveau" }];
    const got = pickLists(page, (block, component) => (block === "mails" && component === "root" ? fresh : undefined));
    expect([...got.keys()]).toEqual([surface]);
    expect(got.get(surface)?.[0].items).toEqual(fresh);
    expect(pickLists(page, () => undefined).get(surface)?.[0].items).toHaveLength(3);
  });
});
