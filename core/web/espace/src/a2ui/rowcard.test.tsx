// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import type { Answer, ProposalView, SourceList } from "../data/api";
import { makeT, WordsContext } from "../i18n";
import { Card } from "../page/Card";
import strings from "../strings.json";
import { createAdapter } from "./adapter";
import { CATALOG } from "./catalog";
import { ListsContext, type Lists } from "./lists";
import { ProposalsContext, type Proposals } from "./proposals";
import { Surfaces } from "./Surfaces";

// One way to say yes. A card is drawn by the page from what eriphe-core
// told of the rows, never from the block (no catalog component says yes):
// each item with its card has its Oui on its row; one card on several
// rows (the brain's call on two mails) says they share it, lights its
// siblings on hover, and a Oui on either answers it once; a batch (a Fold
// with a list) whose rows share one card carries its Oui on its line, and
// unfolded, the rows unticked leave their calls out. The card read in
// full says where it acts, quietly, its exact call behind one closed
// « détails ».

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const surface = "n1--courrier";
const card = (ref: string, text: string, extra: Partial<ProposalView> = {}): ProposalView => ({ ref, card: Number(ref.split(":")[1]), kind: "calls", state: "pending", text, ...extra });
const cards: Record<string, ProposalView> = {
  "access:7": card("access:7", "Mettre 2 mails à la corbeille", {
    kind: "access",
    context: "compte perso",
    summary: ["« Votre colis est arrivé » · Relais", "« Colis en point relais » · Relais"],
    fields: [{ name: "mail/trash", value: 'account : perso\nids : ["c2","c4"]', detail: true }],
  }),
  "calls:1": card("calls:1", "Répondre à Sam"),
  "calls:2": card("calls:2", "Ajouter le rendez-vous"),
  "calls:3": card("calls:3", "Répondre à Alex"),
  "calls:9": card("calls:9", "Mettre 3 mails à la corbeille", {
    choices: [
      { id: 91, text: "Billets", n: 1, state: "proposed", items: ["c1"] },
      { id: 92, text: "Votre colis est arrivé", n: 1, state: "proposed", items: ["c2"] },
      { id: 94, text: "Colis en point relais", n: 1, state: "proposed", items: ["c4"] },
    ],
    acts: ["Mettre 1 mail à la corbeille", "Mettre 2 mails à la corbeille", "Mettre 3 mails à la corbeille"],
  }),
};
const answered: Answer[] = [];
const proposals: Proposals = {
  read: async (ref) => cards[ref] ?? null,
  answer: async (a) => {
    answered.push(a);
    return true;
  },
  day: async () => [],
  follow: () => () => {},
};

let el: HTMLDivElement;
let root: Root;

const wrap = (node: React.ReactNode, lists: SourceList[]) => {
  const ls: Lists = { list: (s, c) => (s === surface ? lists.find((l) => l.component === c) : undefined) };
  return (
    <WordsContext.Provider value={{ t: makeT(strings.fr), lang: "fr" }}>
      <ProposalsContext.Provider value={proposals}>
        <ListsContext.Provider value={ls}>{node}</ListsContext.Provider>
      </ProposalsContext.Provider>
    </WordsContext.Provider>
  );
};

async function mount(components: unknown[], lists: SourceList[], data: Record<string, unknown>, tainted = false) {
  const adapter = createAdapter("fr");
  root = createRoot(el);
  await act(async () => root.render(wrap(<Surfaces adapter={adapter} />, lists)));
  await act(async () =>
    adapter.applyMessages([
      {
        version: "1.0",
        createSurface: {
          surfaceId: surface,
          catalogId: CATALOG.id,
          components: components as never,
          dataModel: data,
          metadata: { extensions: { eriphe: tainted ? { author: "node", tainted: true } : { author: "node" } } },
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
});
afterEach(() => {
  act(() => root.unmount());
  el.remove();
});

const mails = [
  { id: "c1", subject: "Billets" },
  { id: "c2", subject: "Votre colis est arrivé" },
  { id: "c4", subject: "Colis en point relais" },
];
const told = (component: string, rowCards: (string | undefined)[]): SourceList => ({
  component,
  source: "mails",
  items: mails.map((m, n) => ({ n, id: m.id, title: m.subject, card: rowCards[n] })),
});
const plainList = [
  { id: "root", component: "List", children: { path: "/mails/messages", componentId: "row" } },
  { id: "row", component: "Item", title: { "@path": "subject" } },
];

describe("a card on its rows", () => {
  it("three items, each with its own card: one Oui on each row, in a block someone else's words wrote too", async () => {
    await mount(plainList, [told("root", ["calls:1", "calls:2", "calls:3"])], { mails: { messages: mails } }, true);
    const yes = [...el.querySelectorAll<HTMLButtonElement>(".a2-list > li .a2-inline-yes")];
    expect(yes.map((b) => b.title)).toEqual(["Répondre à Sam", "Ajouter le rendez-vous", "Répondre à Alex"]);
    expect(el.querySelectorAll(".a2-inline-shared")).toHaveLength(0);
    await act(async () => yes[1].click());
    expect(answered.map((a) => [a.ref, a.yes])).toEqual([["calls:2", true]]);
    // No box, no action bar, nothing else that acts.
    expect(el.querySelector("input[type=checkbox], [role=toolbar]")).toBeNull();
  });

  it("one card on two rows: its Oui on each, says they share it, lights its siblings, and a Oui answers it once", async () => {
    await mount(plainList, [told("root", [undefined, "access:7", "access:7"])], { mails: { messages: mails } });
    const rows = [...el.querySelectorAll<HTMLLIElement>(".a2-list > li")];
    expect(rows).toHaveLength(3);
    expect(rows[0].querySelector(".a2-inline-yes")).toBeNull();
    const yes = [...el.querySelectorAll<HTMLButtonElement>(".a2-inline-yes")];
    expect(yes.map((b) => b.title)).toEqual(["Mettre 2 mails à la corbeille · compte perso", "Mettre 2 mails à la corbeille · compte perso"]);
    expect([...el.querySelectorAll(".a2-inline-shared")].map((s) => s.getAttribute("title"))).toEqual([
      "Une seule carte pour ces 2 lignes : un Oui sur l'une les fait toutes",
      "Une seule carte pour ces 2 lignes : un Oui sur l'une les fait toutes",
    ]);
    const first = rows[1].querySelector<HTMLElement>(".a2-inline")!;
    await act(async () => first.dispatchEvent(new MouseEvent("mouseover", { bubbles: true, relatedTarget: document.body })));
    expect(rows[2].classList.contains("a2-card-sibling")).toBe(true);
    await act(async () => first.dispatchEvent(new MouseEvent("mouseout", { bubbles: true, relatedTarget: document.body })));
    expect(rows[2].classList.contains("a2-card-sibling")).toBe(false);
    await act(async () => yes[1].click());
    expect(answered.map((a) => a.ref)).toEqual(["access:7"]);
  });
});

describe("a batch", () => {
  const batch = [
    { id: "root", component: "Fold", list: "jeter", label: "{n} mails à jeter", child: "jeter" },
    { id: "jeter", component: "List", children: { path: "/mails/messages", componentId: "row" } },
    { id: "row", component: "Item", title: { "@path": "subject" } },
  ];

  it("carries the Oui of the card its rows share on its line; unfolded, a row unticked leaves its call out", async () => {
    await mount(batch, [told("jeter", ["calls:9", "calls:9", "calls:9"])], { mails: { messages: mails } });
    const line = el.querySelector(".a2-batch .a2-fold-line")!;
    expect(line.querySelector(".a2-fold-label")?.textContent).toBe("3 mails à jeter");
    const yes = () => line.querySelector<HTMLButtonElement>(".a2-fold-card .a2-inline-yes")!;
    expect(yes().textContent).toBe("Mettre 3 mails à la corbeille");
    await act(async () => line.querySelector<HTMLButtonElement>(".a2-fold-head")!.click());
    const boxes = [...el.querySelectorAll<HTMLInputElement>(".a2-batchrows input[type=checkbox]")];
    expect(boxes.map((b) => b.checked)).toEqual([true, true, true]);
    // The rows carry no Oui of their own: the line's is theirs.
    expect(el.querySelectorAll(".a2-batchrows .a2-inline-yes")).toHaveLength(0);
    await act(async () => boxes[1].click());
    expect(yes().textContent).toBe("Mettre 2 mails à la corbeille");
    expect(line.querySelector(".a2-batch-kept")?.textContent).toContain("1");
    await act(async () => yes().click());
    expect(answered).toHaveLength(1);
    expect(answered[0]).toMatchObject({ ref: "calls:9", yes: true, only: [91, 94], left: [92] });
  });

  it("every row unticked: no Oui to give", async () => {
    await mount(batch, [told("jeter", ["calls:9", "calls:9", "calls:9"])], { mails: { messages: mails } });
    const line = el.querySelector(".a2-batch .a2-fold-line")!;
    await act(async () => line.querySelector<HTMLButtonElement>(".a2-fold-head")!.click());
    for (const b of el.querySelectorAll<HTMLInputElement>(".a2-batchrows input[type=checkbox]")) await act(async () => b.click());
    expect(line.querySelector<HTMLButtonElement>(".a2-fold-card .a2-inline-yes")!.disabled).toBe(true);
  });

  it("rows with cards of their own: each its Oui, no box, nothing on the line", async () => {
    await mount(batch, [told("jeter", ["calls:1", undefined, "calls:3"])], { mails: { messages: mails } });
    const line = el.querySelector(".a2-batch .a2-fold-line")!;
    expect(line.querySelector(".a2-fold-card")).toBeNull();
    await act(async () => line.querySelector<HTMLButtonElement>(".a2-fold-head")!.click());
    expect(el.querySelectorAll(".a2-batchrows input[type=checkbox]")).toHaveLength(0);
    expect(el.querySelectorAll(".a2-batchrows .a2-inline-yes")).toHaveLength(2);
  });
});

describe("a card read in full", () => {
  it("says where it acts, quietly; its call behind one closed « détails »; one Oui and a discreet ✕", async () => {
    root = createRoot(el);
    await act(async () => root.render(wrap(<Card refText="access:7" surface="console" />, [])));
    await act(async () => new Promise((r) => setTimeout(r, 0)));
    expect(el.querySelector(".a2-proposal-text")?.textContent).toBe("Mettre 2 mails à la corbeille · compte perso");
    expect(el.querySelector(".a2-proposal-context")?.textContent).toBe(" · compte perso");
    expect([...el.querySelectorAll(".a2-proposal-summary li")].map((l) => l.textContent)).toEqual(cards["access:7"].summary);
    const details = [...el.querySelectorAll<HTMLDetailsElement>("details.a2-proposal-details")];
    expect(details).toHaveLength(1);
    expect(details[0].open).toBe(false);
    expect(details[0].textContent).toContain('ids : ["c2","c4"]');
    expect(el.querySelector(".a2-proposal-yes")).toBeNull();
    const acts = el.querySelector(".a2-proposal-actions")!;
    expect(acts.querySelector(".a2-button-primary")?.textContent).toBe("Oui");
    const no = acts.querySelector<HTMLButtonElement>(".a2-proposal-no")!;
    expect(no.textContent).toBe("");
    await act(async () => no.click());
    expect(answered.map((a) => [a.ref, a.yes])).toEqual([["access:7", false]]);
  });
});
