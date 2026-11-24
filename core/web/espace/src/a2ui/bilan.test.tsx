// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import type { ProposalView, SourceList } from "../data/api";
import { makeT, WordsContext } from "../i18n";
import strings from "../strings.json";
import { createAdapter } from "./adapter";
import { CATALOG } from "./catalog";
import { ListsContext, type Lists } from "./lists";
import { ProposalsContext, type Proposals } from "./proposals";
import { Surfaces } from "./Surfaces";

// A view as a bilan: an item's line with its one Oui, drawn by the page
// for the card eriphe-core says waits on that row (never from the block's
// data, which names none); a batch (Fold with list) carrying the Oui of
// the card its rows share, the user unticking what they keep.

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const surface = "n1--mails";

const views: ProposalView[] = [
  {
    ref: "calls:1",
    card: 1, verb: "Faire",
    state: "pending",
    text: "Envoyer la réponse à Sam",
    fields: [
      { name: "Ta réponse, prête", value: "Bonjour Sam,\n\nMerci, ça me va.\n\n<b>À bientôt</b>" },
      { name: "Compte", value: "perso" },
      { name: "mail/send_message", value: "account : perso\nreply_to : m1", detail: true },
    ],
  },
  { ref: "calls:2", card: 2, verb: "Faire", state: "pending", text: "Autre chose" },
  {
    ref: "calls:3",
    card: 3, verb: "Faire",
    state: "pending",
    text: "Mettre 3 mails à la corbeille",
    choices: ["t1", "t2", "t3"].map((id, i) => ({ id: 30 + i, text: id, n: 1, state: "proposed", items: [id] })),
    acts: ["Mettre 1 mail à la corbeille", "Mettre 2 mails à la corbeille", "Mettre 3 mails à la corbeille"],
  },
];
const answered: string[] = [];
const chosen: { only?: number[]; left?: number[] }[] = [];
const proposals: Proposals = {
  read: async (ref) => views.find((v) => v.ref === ref) ?? null,
  answer: async (a) => {
    answered.push(a.ref);
    chosen.push({ only: a.only, left: a.left });
    return true;
  },
  day: async () => [],
  follow: () => () => {},
};

let el: HTMLDivElement;
let root: Root;
// The rows: m1 with the card code told of (calls:1); m2 whose data names
// another card (calls:2) that code did not tell of; t1 to t3, one card
// over all three (calls:3).
async function mount(tainted: boolean) {
  const todo: SourceList = {
    component: "todo",
    source: "mails",
    filtered: true,
    open: true,
    items: [
      { n: 0, id: "m1", title: "Devis", card: "calls:1" },
      { n: 1, id: "m2", title: "Autre" },
    ],
  };
  const jeter: SourceList = {
    component: "jeter",
    source: "mails",
    filtered: true,
    items: [
      { n: 2, id: "t1", title: "Pub", card: "calls:3" },
      { n: 3, id: "t2", title: "Lettre", card: "calls:3" },
      { n: 4, id: "t3", title: "Promo", card: "calls:3" },
    ],
  };
  const ls: Lists = {
    list: (s, c) => (s === surface ? [todo, jeter].find((l) => l.component === c) : undefined),
    open: async () => ({ title: "Devis", from: "Sam <sam@example.org>", body: "Le devis.", raw: "{}" }),
    opened: new Map(),
  };
  const adapter = createAdapter("fr");
  root = createRoot(el);
  await act(async () =>
    root.render(
      <WordsContext.Provider value={{ t: makeT(strings.fr), lang: "fr" }}>
        <ProposalsContext.Provider value={proposals}>
          <ListsContext.Provider value={ls}>
            <Surfaces adapter={adapter} />
          </ListsContext.Provider>
        </ProposalsContext.Provider>
      </WordsContext.Provider>,
    ),
  );
  const items = [
    { id: "m1", subject: "Devis", _card: "calls:1" },
    { id: "m2", subject: "Autre", _card: "calls:2" },
    { id: "t1", subject: "Pub", _card: "" },
    { id: "t2", subject: "Lettre", _card: "" },
    { id: "t3", subject: "Promo", _card: "" },
  ];
  await act(async () =>
    adapter.applyMessages([
      {
        version: "1.0",
        createSurface: {
          surfaceId: surface,
          catalogId: CATALOG.id,
          components: [
            { id: "root", component: "Column", children: ["todo", "fold"] },
            { id: "todo", component: "List", children: { path: "/mails/messages", componentId: "item" } },
            { id: "item", component: "Item", title: { "@path": "subject" } },
            { id: "fold", component: "Fold", list: "jeter", label: "{n} mails à jeter", child: "jeter" },
            { id: "jeter", component: "List", children: { path: "/mails/messages", componentId: "trow" } },
            { id: "trow", component: "Item", title: { "@path": "subject" } },
          ],
          dataModel: { mails: { messages: items } },
          metadata: { extensions: { eriphe: { author: "node", tainted } } },
        },
      },
    ]),
  );
  await act(async () => new Promise((r) => setTimeout(r, 0)));
}

beforeEach(() => {
  el = document.createElement("div");
  document.body.append(el);
  answered.length = 0;
  chosen.length = 0;
});
afterEach(() => {
  act(() => root.unmount());
  el.remove();
});

const yeses = () => [...el.querySelectorAll<HTMLButtonElement>(".a2-inline-yes")];

describe("an item's one Oui", () => {
  for (const tainted of [false, true]) {
    it(`answers the card code told of on its row, never one the data names (${tainted ? "an external block" : "a block of the user's"})`, async () => {
      await mount(tainted);
      // m2's data names calls:2, but code told of no card on m2: no Oui.
      expect(yeses().map((b) => b.title)).toEqual(["Envoyer la réponse à Sam", "Mettre 3 mails à la corbeille"]);
      await act(async () => yeses()[0].click());
      expect(answered).toEqual(["calls:1"]);
    });
  }
});

describe("a batch", () => {
  it("says how many, carries its card's Oui, and leaves out what is unticked", async () => {
    await mount(false);
    const head = el.querySelector<HTMLButtonElement>(".a2-batch .a2-fold-head")!;
    expect(head.textContent).toContain("3 mails à jeter");
    const yes = () => el.querySelector<HTMLButtonElement>(".a2-batch .a2-fold-card .a2-inline-yes")!;
    expect(yes().textContent).toBe("Mettre 3 mails à la corbeille");
    await act(async () => head.click());
    const boxes = [...el.querySelectorAll<HTMLInputElement>(".a2-batchrows input")];
    expect(boxes.every((b) => b.checked)).toBe(true);
    await act(async () => boxes[1].click());
    expect(yes().textContent).toBe("Mettre 2 mails à la corbeille");
    await act(async () => yes().click());
    expect(answered).toEqual(["calls:3"]);
    expect(chosen).toEqual([{ only: [30, 32], left: [31] }]);
  });
});

describe("an item opened with its card", () => {
  it("shows what its Oui makes, as text, its Oui there, the row's own keeping quiet, and one « détails »", async () => {
    await mount(false);
    await act(async () => el.querySelector<HTMLButtonElement>(".a2-openbtn")!.click());
    await act(async () => new Promise((r) => setTimeout(r, 0)));
    const box = el.querySelector(".a2-onyes")!;
    expect(box.querySelector(".a2-onyes-what")?.textContent).toBe("Si tu acceptesEnvoyer la réponse à Sam");
    expect(box.querySelector(".a2-onyes-content figcaption")?.textContent).toBe("Ta réponse, prête");
    expect(box.querySelector(".a2-onyes-text")?.textContent).toContain("<b>À bientôt</b>"); // text, never HTML
    expect(box.querySelector("b")).toBeNull();
    expect(box.querySelector(".a2-onyes-facts")?.textContent).toBe("Compteperso");
    expect(box.textContent).not.toContain("account : perso"); // the call is under « détails »
    const opened = el.querySelector(".a2-opened")!;
    expect([...opened.querySelectorAll("summary")].map((s) => s.textContent)).toEqual(["détails"]);
    expect([...opened.querySelectorAll(".a2-read-more h5")].map((h) => h.textContent)).toEqual(["Ce qui sera lancé", "L'élément, tel que lu"]);
    await act(async () => box.querySelector<HTMLButtonElement>(".a2-inline-yes")!.click());
    expect(answered).toEqual(["calls:1"]);
  });
});
