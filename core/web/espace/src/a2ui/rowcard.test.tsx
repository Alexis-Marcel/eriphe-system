// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import type { PickList, ProposalView } from "../data/api";
import { makeT, WordsContext } from "../i18n";
import strings from "../strings.json";
import { createAdapter } from "./adapter";
import { CATALOG } from "./catalog";
import { PicksContext, type Picks } from "./picks";
import { ProposalsContext, type Proposals } from "./proposals";
import { Surfaces } from "./Surfaces";

// A call to approve on rows (the brain's, asked in conversation): one card
// on the rows it names, in a plain List whose template draws no card. The
// page adds each row's Oui; the rows that share the card say so, light up
// together on hover, and a Oui on either answers the one card. In a batch
// (a Fold with a list), folded, the card is on the batch's line. The card
// read in full says where it acts, quietly, its exact call behind one
// closed « détails », and no line of what Oui does.

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const surface = "n1--courrier";
const card: ProposalView = {
  ref: "access:7",
  card: 7,
  kind: "access",
  state: "pending",
  text: "Mettre 2 mails à la corbeille",
  context: "compte perso",
  summary: ["« Votre colis est arrivé » · Relais", "« Colis en point relais » · Relais"],
  fields: [{ name: "mail/trash", value: 'account : perso\nids : ["c2","c4"]', detail: true }],
};
const answered: string[] = [];
const proposals: Proposals = {
  read: async (ref) => (ref === card.ref ? card : null),
  answer: async (a) => {
    answered.push(a.ref);
    return true;
  },
  day: async () => [],
  follow: () => () => {},
};

let el: HTMLDivElement;
let root: Root;

async function mount(components: unknown[], lists: PickList[], data: Record<string, unknown>) {
  const picks: Picks = { list: (s, c) => (s === surface ? lists.find((l) => l.component === c) : undefined), kept: new Map(), run: async () => ({ ok: true, text: "" }) };
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
  await act(async () =>
    adapter.applyMessages([
      {
        version: "1.0",
        createSurface: { surfaceId: surface, catalogId: CATALOG.id, components: components as never, dataModel: data, metadata: { extensions: { eriphe: { author: "node" } } } },
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
const told = (component: string): PickList => ({
  component,
  source: "mails",
  items: [
    { n: 0, id: "c1", title: "Billets" },
    { n: 1, id: "c2", title: "Votre colis est arrivé", card: "access:7" },
    { n: 2, id: "c4", title: "Colis en point relais", card: "access:7" },
  ],
  actions: [],
});

describe("one card on several rows", () => {
  it("shows its Oui on each row it names, says they share it, lights its siblings, and a Oui answers it once", async () => {
    await mount(
      [
        { id: "root", component: "List", children: { path: "/mails/messages", componentId: "row" } },
        { id: "row", component: "Item", title: { "@path": "subject" } },
      ],
      [told("root")],
      { mails: { messages: mails } },
    );
    const rows = [...el.querySelectorAll<HTMLLIElement>(".a2-list > li")];
    expect(rows).toHaveLength(3);
    expect(rows[0].querySelector(".a2-inline-yes")).toBeNull();
    const yes = [...el.querySelectorAll<HTMLButtonElement>(".a2-inline-yes")];
    expect(yes.map((b) => b.title)).toEqual(["Mettre 2 mails à la corbeille · compte perso", "Mettre 2 mails à la corbeille · compte perso"]);
    expect([...el.querySelectorAll(".a2-inline-shared")].map((s) => s.getAttribute("title"))).toEqual([
      "Une seule carte pour ces 2 lignes : un Oui sur l'une les fait toutes",
      "Une seule carte pour ces 2 lignes : un Oui sur l'une les fait toutes",
    ]);
    // Hovered on one row: its sibling lights up; left, it goes out.
    const first = rows[1].querySelector<HTMLElement>(".a2-inline")!;
    await act(async () => first.dispatchEvent(new MouseEvent("mouseover", { bubbles: true, relatedTarget: document.body })));
    expect(rows[2].classList.contains("a2-card-sibling")).toBe(true);
    await act(async () => first.dispatchEvent(new MouseEvent("mouseout", { bubbles: true, relatedTarget: document.body })));
    expect(rows[2].classList.contains("a2-card-sibling")).toBe(false);
    await act(async () => yes[1].click());
    expect(answered).toEqual(["access:7"]);
  });

  it("shows it on a folded batch's line", async () => {
    await mount(
      [
        { id: "root", component: "Fold", list: "jeter", label: "{n} mails", child: "jeter" },
        { id: "jeter", component: "List", children: { path: "/mails/messages", componentId: "row" } },
        { id: "row", component: "Item", title: { "@path": "subject" } },
      ],
      [told("jeter")],
      { mails: { messages: mails } },
    );
    const line = el.querySelector(".a2-batch .a2-fold-line")!;
    expect([...line.querySelectorAll<HTMLButtonElement>(".a2-fold-card .a2-inline-yes")].map((b) => b.title)).toEqual(["Mettre 2 mails à la corbeille · compte perso"]);
  });

  it("read in full: where it acts, quietly; its call behind one closed « détails »; no line of what Oui does", async () => {
    await mount([{ id: "root", component: "Proposal", proposalId: "access:7" }], [], {});
    expect(el.querySelector(".a2-proposal-text")?.textContent).toBe("Mettre 2 mails à la corbeille · compte perso");
    expect(el.querySelector(".a2-proposal-context")?.textContent).toBe(" · compte perso");
    expect([...el.querySelectorAll(".a2-proposal-summary li")].map((l) => l.textContent)).toEqual(card.summary);
    const details = [...el.querySelectorAll<HTMLDetailsElement>("details.a2-proposal-details")];
    expect(details).toHaveLength(1);
    expect(details[0].open).toBe(false);
    expect(details[0].textContent).toContain('ids : ["c2","c4"]');
    expect(el.querySelector(".a2-proposal-yes")).toBeNull();
  });
});
