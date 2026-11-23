// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import type { NodePage, SourceList } from "../data/api";
import { sourceLists } from "../doc/blocks";
import { makeT, WordsContext } from "../i18n";
import strings from "../strings.json";
import { createAdapter, type Adapter } from "./adapter";
import { CATALOG } from "./catalog";
import { ListsContext, sharedCard, type Lists } from "./lists";
import { Surfaces } from "./Surfaces";

// The Lists of a source's items eriphe-core tells the page of: a section
// shows only the rows code kept, in the source's order; nothing in them
// acts (no box, no bar, no button on all the rows).

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const surface = "n1--mails";
const drop: SourceList = {
  component: "drop",
  source: "inbox",
  filtered: true,
  items: [
    { n: 0, id: "m1", title: "Facture", detail: "Sam" },
    { n: 2, id: "m3", title: "Lettre" },
  ],
};
const info: SourceList = { component: "info", source: "inbox", filtered: true, items: [{ n: 1, id: "m2", title: "Relance" }] };
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

let el: HTMLDivElement;
let root: Root;
let adapter: Adapter;

beforeEach(() => {
  el = document.createElement("div");
  document.body.append(el);
});

afterEach(() => {
  act(() => root.unmount());
  adapter.dispose();
  el.remove();
});

describe("a view in sections", () => {
  let lists: Record<string, SourceList>;
  const mountSections = async () => {
    const ls: Lists = { list: (s, c) => (s === surface ? lists[c] : undefined) };
    adapter = createAdapter("fr");
    root = createRoot(el);
    await act(async () =>
      root.render(
        <WordsContext.Provider value={{ t: makeT(strings.fr), lang: "fr" }}>
          <ListsContext.Provider value={ls}>
            <Surfaces adapter={adapter} />
          </ListsContext.Provider>
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

  it("shows in each List only the rows eriphe-core kept, and nothing that acts", async () => {
    await mountSections();
    expect(texts(section(0))).toEqual(["Facture", "Lettre"]);
    expect(texts(section(1))).toEqual(["Relance"]);
    expect(el.querySelector("input, [role=toolbar]")).toBeNull();
  });

  it("says its empty text when the filter keeps nothing", async () => {
    lists = { drop, info: { ...info, items: [] } };
    await mountSections();
    expect(el.querySelector(".a2-empty")?.textContent).toBe("Rien");
  });
});

describe("a figure that counts a List", () => {
  const counted = [
    {
      version: "1.0" as const,
      createSurface: {
        surfaceId: surface,
        catalogId: CATALOG.id,
        components: [
          { id: "root", component: "Column", children: ["compte", "drop"] },
          { id: "compte", component: "Metric", label: "À jeter", list: "drop" },
          { id: "drop", component: "List", children: { path: "/inbox/messages", componentId: "row" } },
          { id: "row", component: "Text", text: { "@path": "subject" } },
        ],
        dataModel: { inbox: { messages: [{ id: "m1", subject: "Facture" }, { id: "m2", subject: "Relance" }, { id: "m3", subject: "Lettre" }] } },
      },
    },
  ];
  const mount = async (told: Record<string, SourceList>) => {
    const ls: Lists = { list: (s, c) => (s === surface ? told[c] : undefined) };
    adapter = createAdapter("fr");
    root = createRoot(el);
    await act(async () =>
      root.render(
        <WordsContext.Provider value={{ t: makeT(strings.fr), lang: "fr" }}>
          <ListsContext.Provider value={ls}>
            <Surfaces adapter={adapter} />
          </ListsContext.Provider>
        </WordsContext.Provider>,
      ),
    );
    await act(async () => adapter.applyMessages(counted));
  };

  it("shows the rows eriphe-core kept, its filters applied, never the source's whole list", async () => {
    await mount({ drop });
    expect(el.querySelector(".a2-metric-value")?.textContent).toBe("2");
  });

  it("says nothing it does not know", async () => {
    await mount({});
    expect(el.querySelector(".a2-metric-value")?.textContent).toBe("—");
  });
});

describe("sharedCard", () => {
  it("is the card every row shares, or none", () => {
    expect(sharedCard([{ n: 0, id: "a", card: "calls:1" }, { n: 1, id: "b", card: "calls:1" }])).toBe("calls:1");
    expect(sharedCard([{ n: 0, id: "a", card: "calls:1" }, { n: 1, id: "b" }])).toBeUndefined();
    expect(sharedCard([])).toBeUndefined();
  });
});

describe("sourceLists", () => {
  it("takes the page's lists, their rows the stream's latest, by List", () => {
    const list: SourceList = { component: "root", source: "inbox", items: [{ n: 0, id: "m1" }, { n: 1, id: "m2" }, { n: 2, id: "m3" }] };
    const page = {
      node: { id: "n1" },
      version: 3,
      versions: [],
      cards: [],
      parts: [{ block: { name: "mails", components: [], data: {}, lists: [list] } }, { block: { name: "other", components: [], data: {} } }],
    } as unknown as NodePage;
    const fresh = [{ n: 0, id: "m9", title: "Nouveau" }];
    const got = sourceLists(page, (block, component) => (block === "mails" && component === "root" ? fresh : undefined));
    expect([...got.keys()]).toEqual([surface]);
    expect(got.get(surface)?.[0].items).toEqual(fresh);
    expect(sourceLists(page, () => undefined).get(surface)?.[0].items).toHaveLength(3);
  });
});
