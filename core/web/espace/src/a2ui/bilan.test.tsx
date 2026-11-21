// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import type { PickAction, PickList, ProposalView } from "../data/api";
import { makeT, WordsContext } from "../i18n";
import strings from "../strings.json";
import { createAdapter } from "./adapter";
import { CATALOG } from "./catalog";
import { PicksContext, type Picks } from "./picks";
import { ProposalsContext, type Proposals } from "./proposals";
import { Surfaces } from "./Surfaces";

// A view as a bilan: an item's line with its one Oui (Proposal inline),
// answered in an external block only for the card eriphe-core says waits
// on that row; a batch (Fold with list) that acts on all its rows, ticked
// beforehand, the user unticking what they keep.

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const trash: PickAction = { name: "jeter", one: "Mettre 1 élément à la corbeille", many: "Mettre les {n} à la corbeille", all: "Tout mettre à la corbeille", tool: "mail/trash" };
const surface = "n1--mails";

const views: ProposalView[] = [
  {
    ref: "calls:1",
    card: 1,
    state: "pending",
    text: "Envoyer la réponse à Sam",
    fields: [
      { name: "Ta réponse, prête", value: "Bonjour Sam,\n\nMerci, ça me va.\n\n<b>À bientôt</b>" },
      { name: "Compte", value: "perso" },
      { name: "mail/send_message", value: "account : perso\nreply_to : m1", detail: true },
    ],
  },
  { ref: "calls:2", card: 2, state: "pending", text: "Autre chose" },
];
const answered: string[] = [];
const proposals: Proposals = {
  read: async (ref) => views.find((v) => v.ref === ref) ?? null,
  answer: async (a) => {
    answered.push(a.ref);
    return true;
  },
  day: async () => [],
  follow: () => () => {},
};

let el: HTMLDivElement;
let root: Root;
let runs: string[][];

// The rows: m1 with the card code told of (calls:1); m2 whose data names
// another card (calls:2) that code did not tell of.
async function mount(tainted: boolean) {
  const todo: PickList = {
    component: "todo",
    source: "mails",
    filtered: true,
    open: true,
    rowCard: true, // its row template draws its card (an inline Proposal)
    items: [
      { n: 0, id: "m1", title: "Devis", card: "calls:1" },
      { n: 1, id: "m2", title: "Autre" },
    ],
    actions: [],
  };
  const jeter: PickList = {
    component: "jeter",
    source: "mails",
    filtered: true,
    items: [
      { n: 2, id: "t1", title: "Pub" },
      { n: 3, id: "t2", title: "Lettre" },
      { n: 4, id: "t3", title: "Promo" },
    ],
    actions: [trash],
  };
  const picks: Picks = {
    list: (s, c) => (s === surface ? [todo, jeter].find((l) => l.component === c) : undefined),
    kept: new Map(),
    run: async (_s, _l, _a, ids) => {
      runs.push(ids);
      return { ok: true, text: `${ids.length} mis à la corbeille` };
    },
    open: async () => ({ title: "Devis", from: "Sam <sam@example.org>", body: "Le devis.", raw: "{}" }),
    opened: new Map(),
  };
  const adapter = createAdapter("fr");
  root = createRoot(el);
  await act(async () =>
    root.render(
      <WordsContext.Provider value={{ t: makeT(strings.fr), lang: "fr" }}>
        <ProposalsContext.Provider value={proposals}>
          <PicksContext.Provider value={picks}>
            <Surfaces adapter={adapter} />
          </PicksContext.Provider>
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
            { id: "todo", component: "List", children: { path: "/mails/messages", componentId: "row" } },
            { id: "row", component: "Row", children: ["item", "yes"] },
            { id: "item", component: "Item", title: { "@path": "subject" } },
            { id: "yes", component: "Proposal", inline: true, proposalId: { "@path": "_card" } },
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
  runs = [];
  answered.length = 0;
});
afterEach(() => {
  act(() => root.unmount());
  el.remove();
});

const yeses = () => [...el.querySelectorAll<HTMLButtonElement>(".a2-inline-yes")];

describe("an item's one Oui", () => {
  it("answers each row's card in a block of the user's", async () => {
    await mount(false);
    expect(yeses().map((b) => b.title)).toEqual(["Envoyer la réponse à Sam", "Autre chose"]);
    await act(async () => yeses()[0].click());
    expect(answered).toEqual(["calls:1"]);
  });

  it("in an external block, answers only the card code told of on that row", async () => {
    await mount(true);
    // m2's data names calls:2, but code told of no card on m2: no Oui.
    expect(yeses().map((b) => b.title)).toEqual(["Envoyer la réponse à Sam"]);
  });
});

describe("a batch", () => {
  it("says how many, acts on all, and on what is left ticked", async () => {
    await mount(false);
    const head = el.querySelector<HTMLButtonElement>(".a2-batch .a2-fold-head")!;
    expect(head.textContent).toContain("3 mails à jeter");
    const act1 = () => el.querySelector<HTMLButtonElement>(".a2-batch-act")!;
    expect(act1().textContent).toBe("Tout mettre à la corbeille");
    await act(async () => head.click());
    const boxes = [...el.querySelectorAll<HTMLInputElement>(".a2-batchrows input")];
    expect(boxes.every((b) => b.checked)).toBe(true);
    await act(async () => boxes[1].click());
    expect(act1().textContent).toBe("Mettre les 2 à la corbeille");
    await act(async () => act1().click());
    expect(runs).toEqual([["t1", "t3"]]);
  });
});

describe("an item opened with its card", () => {
  it("shows what its Oui makes, as text, its Oui there, the row's own keeping quiet, and one « détails »", async () => {
    await mount(false);
    await act(async () => el.querySelector<HTMLButtonElement>(".a2-openbtn")!.click());
    await act(async () => new Promise((r) => setTimeout(r, 0)));
    const box = el.querySelector(".a2-onyes")!;
    expect(box.querySelector(".a2-onyes-what")?.textContent).toBe("Sur OuiEnvoyer la réponse à Sam");
    expect(box.querySelector(".a2-onyes-content figcaption")?.textContent).toBe("Ta réponse, prête");
    expect(box.querySelector(".a2-onyes-text")?.textContent).toContain("<b>À bientôt</b>"); // text, never HTML
    expect(box.querySelector("b")).toBeNull();
    expect(box.querySelector(".a2-onyes-facts")?.textContent).toBe("Compteperso");
    expect(box.textContent).not.toContain("account : perso"); // the call is under « détails »
    const opened = el.querySelector(".a2-opened")!;
    expect([...opened.querySelectorAll("summary")].map((s) => s.textContent)).toEqual(["détails"]);
    expect([...opened.querySelectorAll(".a2-read-more h5")].map((h) => h.textContent)).toEqual(["Ce que Oui lance", "L'élément, tel que lu"]);
    await act(async () => box.querySelector<HTMLButtonElement>(".a2-inline-yes")!.click());
    expect(answered).toEqual(["calls:1"]);
  });
});
